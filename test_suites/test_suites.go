// Copyright 2025 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package testsuites

import (
	"android/soong/android"
	"android/soong/java"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

var (
	pctx = android.NewPackageContext("android/soong/test_suites")
)

func init() {
	android.RegisterParallelSingletonType("testsuites", testSuiteFilesFactory)
	android.RegisterModuleType("compatibility_test_suite_package", compatibilityTestSuitePackageFactory)
	android.RegisterModuleType("test_suite_package", testSuitePackageFactory)
	pctx.Import("android/soong/android")
}

func testSuiteFilesFactory() android.Singleton {
	return &testSuiteFiles{}
}

type testModulesInstallsMap map[android.ModuleProxy]android.InstallPaths

func (t testModulesInstallsMap) testModules() []android.ModuleProxy {
	return slices.Collect(maps.Keys(t))
}

type testSuiteFiles struct{}

func (t *testSuiteFiles) GenerateBuildActions(ctx android.SingletonContext) {
	hostOutTestCases := android.PathForHostInstall(ctx, "testcases")
	files := make(map[string]testModulesInstallsMap)
	sharedLibRoots32 := make(map[string][]string)
	sharedLibRoots64 := make(map[string][]string)
	sharedLibGraph := make(map[string][]string)
	allTestSuiteInstalls := make(map[string]android.Paths)
	var toInstall []android.FilePair
	var oneVariantInstalls []android.FilePair
	var allCompatibilitySuitePackages []compatibilitySuitePackageInfo

	var allTestSuiteConfigs []testSuiteConfig
	allTestSuitesWithHostSharedLibs := []string{
		"general-tests",
		"device-tests",
		"vts",
		"tvts",
		"art-host-tests",
		"host-unit-tests",
		"camera-hal-tests",
		"automotive-tests",
		"automotive-general-tests",
		"automotive-sdv-tests",
	}

	ctx.VisitAllModuleProxies(func(m android.ModuleProxy) {
		commonInfo := android.OtherModuleProviderOrDefault(ctx, m, android.CommonModuleInfoProvider)
		testSuiteSharedLibsInfo := android.OtherModuleProviderOrDefault(ctx, m, android.TestSuiteSharedLibsInfoProvider)
		makeName := android.OtherModuleProviderOrDefault(ctx, m, android.MakeNameInfoProvider).Name
		if makeName != "" && commonInfo.Target.Os == ctx.Config().BuildOS {
			sharedLibGraph[makeName] = append(sharedLibGraph[makeName], testSuiteSharedLibsInfo.MakeNames...)
		}

		if tsm, ok := android.OtherModuleProvider(ctx, m, android.TestSuiteInfoProvider); ok {
			installFilesProvider := android.OtherModuleProviderOrDefault(ctx, m, android.InstallFilesProvider)

			for _, testSuite := range tsm.TestSuites {
				if files[testSuite] == nil {
					files[testSuite] = make(testModulesInstallsMap)
				}
				files[testSuite][m] = append(files[testSuite][m],
					installFilesProvider.InstallFiles...)

				if makeName != "" {
					if commonInfo.Target.Arch.ArchType.Bitness() == "32" {
						sharedLibRoots32[testSuite] = append(sharedLibRoots32[testSuite], makeName)
					} else {
						sharedLibRoots64[testSuite] = append(sharedLibRoots64[testSuite], makeName)
					}
				}
			}

			if testSuiteInstalls, ok := android.OtherModuleProvider(ctx, m, android.TestSuiteInstallsInfoProvider); ok {
				for _, testSuite := range tsm.TestSuites {
					for _, f := range testSuiteInstalls.Files {
						allTestSuiteInstalls[testSuite] = append(allTestSuiteInstalls[testSuite], f.Dst)
					}
					for _, f := range testSuiteInstalls.OneVariantInstalls {
						allTestSuiteInstalls[testSuite] = append(allTestSuiteInstalls[testSuite], f.Dst)
					}
				}
				installs := android.OtherModuleProviderOrDefault(ctx, m, android.InstallFilesProvider).InstallFiles
				oneVariantInstalls = append(oneVariantInstalls, testSuiteInstalls.OneVariantInstalls...)
				for _, f := range testSuiteInstalls.Files {
					alreadyInstalled := false
					for _, install := range installs {
						if install.String() == f.Dst.String() {
							alreadyInstalled = true
							break
						}
					}
					if !alreadyInstalled {
						toInstall = append(toInstall, f)
					}
				}
			}
		}

		if info, ok := android.OtherModuleProvider(ctx, m, compatibilitySuitePackageProvider); ok {
			allCompatibilitySuitePackages = append(allCompatibilitySuitePackages, info)
		}
		if info, ok := android.OtherModuleProvider(ctx, m, testSuitePackageProvider); ok {
			allTestSuiteConfigs = append(allTestSuiteConfigs, info)
			if info.buildHostSharedLibsZip || info.includeHostSharedLibsInMainZip {
				allTestSuitesWithHostSharedLibs = append(allTestSuitesWithHostSharedLibs, info.name)
			}
		}
	})

	sort.Strings(allTestSuitesWithHostSharedLibs)
	slices.SortFunc(allTestSuiteConfigs, func(a testSuiteConfig, b testSuiteConfig) int {
		return strings.Compare(a.name, b.name)
	})

	for suite, suiteInstalls := range allTestSuiteInstalls {
		allTestSuiteInstalls[suite] = android.SortedUniquePaths(suiteInstalls)
	}

	hostSharedLibs32 := gatherHostSharedLibs(ctx, sharedLibRoots32, sharedLibGraph, android.X86)
	hostSharedLibs64 := gatherHostSharedLibs(ctx, sharedLibRoots64, sharedLibGraph, android.X86_64)
	hostSharedLibs := hostSharedLibs64
	for suite, libs := range hostSharedLibs32 {
		hostSharedLibs[suite] = append(hostSharedLibs[suite], libs...)
	}

	if !ctx.Config().KatiEnabled() {
		for _, testSuite := range android.SortedKeys(files) {
			testSuiteSymbolsZipFile := android.PathForHostInstall(ctx, fmt.Sprintf("%s-symbols.zip", testSuite))
			testSuiteMergedMappingProtoFile := android.PathForHostInstall(ctx, fmt.Sprintf("%s-symbols-mapping.textproto", testSuite))
			allTestModules := files[testSuite].testModules()
			allTestModulesOrProxy := make([]android.ModuleOrProxy, 0, len(allTestModules))
			for _, m := range allTestModules {
				allTestModulesOrProxy = append(allTestModulesOrProxy, m)
			}
			android.BuildSymbolsZip(ctx, allTestModulesOrProxy, testSuiteSymbolsZipFile, testSuiteMergedMappingProtoFile)

			ctx.DistForGoalWithFilenameTag(testSuite, testSuiteSymbolsZipFile, testSuiteSymbolsZipFile.Base())
			ctx.DistForGoalWithFilenameTag(testSuite, testSuiteMergedMappingProtoFile, testSuiteMergedMappingProtoFile.Base())
		}
	}

	// https://source.corp.google.com/h/googleplex-android/platform/superproject/main/+/main:build/make/core/main.mk;l=674;drc=46bd04e115d34fd62b3167128854dfed95290eb0
	testInstalledSharedLibs := make(map[string]android.Paths)
	testInstalledSharedLibsDeduper := make(map[string]bool)
	for _, install := range toInstall {
		testInstalledSharedLibsDeduper[install.Dst.String()] = true
	}
	for _, suite := range allTestSuitesWithHostSharedLibs {
		var myTestCases android.WritablePath = hostOutTestCases
		switch suite {
		case "vts", "tvts":
			suiteInfo := ctx.Config().CompatibilityTestcases()[suite]
			outDir := suiteInfo.OutDir
			if outDir == "" {
				continue
			}
			rel, err := filepath.Rel(ctx.Config().OutDir(), outDir)
			if err != nil || strings.HasPrefix(rel, "..") {
				panic(fmt.Sprintf("Could not make COMPATIBILITY_TESTCASES_OUT_%s (%s) relative to the out dir (%s)", suite, suiteInfo.OutDir, ctx.Config().OutDir()))
			}
			myTestCases = android.PathForArbitraryOutput(ctx, rel)
		}

		for _, f := range hostSharedLibs[suite] {
			dir := filepath.Base(filepath.Dir(f.String()))
			out := android.JoinWriteablePath(ctx, myTestCases, dir, filepath.Base(f.String()))
			if _, ok := testInstalledSharedLibsDeduper[out.String()]; !ok {
				ctx.Build(pctx, android.BuildParams{
					Rule:   android.Cp,
					Input:  f,
					Output: out,
				})
			}
			testInstalledSharedLibsDeduper[out.String()] = true
			testInstalledSharedLibs[suite] = append(testInstalledSharedLibs[suite], out)
		}
	}

	filePairSorter := func(arr []android.FilePair) func(i, j int) bool {
		return func(i, j int) bool {
			c := strings.Compare(arr[i].Dst.String(), arr[j].Dst.String())
			if c < 0 {
				return true
			} else if c > 0 {
				return false
			}
			return arr[i].Src.String() < arr[j].Src.String()
		}
	}

	sort.Slice(toInstall, filePairSorter(toInstall))
	// Dedup, as multiple tests may install the same test data to the same folder
	toInstall = slices.Compact(toInstall)

	// Dedup the oneVariant files by only the dst locations, and ignore installs from other variants
	sort.Slice(oneVariantInstalls, filePairSorter(oneVariantInstalls))
	oneVariantInstalls = slices.CompactFunc(oneVariantInstalls, func(a, b android.FilePair) bool {
		return a.Dst.String() == b.Dst.String()
	})

	for _, install := range toInstall {
		ctx.Build(pctx, android.BuildParams{
			Rule:   android.Cp,
			Input:  install.Src,
			Output: install.Dst,
		})
	}
	for _, install := range oneVariantInstalls {
		ctx.Build(pctx, android.BuildParams{
			Rule:   android.Cp,
			Input:  install.Src,
			Output: install.Dst,
		})
	}

	robolectricZip, robolectrictListZip := buildTestSuite(ctx, "robolectric-tests", files["robolectric-tests"])
	ctx.Phony("robolectric-tests", robolectricZip, robolectrictListZip)
	ctx.DistForGoal("robolectric-tests", robolectricZip, robolectrictListZip)

	ravenwoodZip, ravenwoodListZip := buildTestSuite(ctx, "ravenwood-tests", files["ravenwood-tests"])
	ctx.Phony("ravenwood-tests", ravenwoodZip, ravenwoodListZip)
	ctx.DistForGoal("ravenwood-tests", ravenwoodZip, ravenwoodListZip)

	for _, testSuiteConfig := range allTestSuiteConfigs {
		files := allTestSuiteInstalls[testSuiteConfig.name]
		sharedLibs := testInstalledSharedLibs[testSuiteConfig.name]
		packageTestSuite(ctx, files, sharedLibs, testSuiteConfig)
	}

	for _, suite := range allCompatibilitySuitePackages {
		modules := slices.Collect(maps.Keys(files[suite.Name]))
		sort.Slice(modules, func(i, j int) bool {
			return modules[i].String() < modules[j].String()
		})

		buildCompatibilitySuitePackage(ctx, suite, slices.Clone(allTestSuiteInstalls[suite.Name]), modules)
	}
}

// Get a mapping from testSuite -> list of host shared libraries, given:
// - sharedLibRoots: Mapping from testSuite -> androidMk name of all test modules in the suite
// - sharedLibGraph: Mapping from androidMk name of module -> androidMk names of its shared libs
// - archType: ArchType to match for including shared libs
//
// This mimics how make did it historically, which is filled with inaccuracies. Make didn't
// track variants and treated all variants as if they were merged into one big module. This means
// you can have a test that's only included in the "vts" test suite on the device variant, and
// only has a shared library on the host variant, and that shared library will still be included
// into the vts test suite.
func gatherHostSharedLibs(ctx android.SingletonContext, sharedLibRoots, sharedLibGraph map[string][]string, archType android.ArchType) map[string]android.Paths {
	hostOutTestCases := android.PathForHostInstall(ctx, "testcases")
	hostOut := filepath.Dir(hostOutTestCases.String())

	for k, v := range sharedLibGraph {
		sharedLibGraph[k] = android.SortedUniqueStrings(v)
	}

	suiteToSharedLibModules := make(map[string]map[string]bool)
	for suite, modules := range sharedLibRoots {
		suiteToSharedLibModules[suite] = make(map[string]bool)
		var queue []string
		for _, root := range android.SortedUniqueStrings(modules) {
			queue = append(queue, sharedLibGraph[root]...)
		}
		for len(queue) > 0 {
			mod := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			if suiteToSharedLibModules[suite][mod] {
				continue
			}
			suiteToSharedLibModules[suite][mod] = true
			queue = append(queue, sharedLibGraph[mod]...)
		}
	}

	hostSharedLibs := make(map[string]android.Paths)

	ctx.VisitAllModuleProxies(func(m android.ModuleProxy) {
		if makeName, ok := android.OtherModuleProvider(ctx, m, android.MakeNameInfoProvider); ok {
			commonInfo := android.OtherModuleProviderOrDefault(ctx, m, android.CommonModuleInfoProvider)
			if commonInfo.SkipInstall || commonInfo.Target.Arch.ArchType != archType {
				return
			}
			installFilesProvider := android.OtherModuleProviderOrDefault(ctx, m, android.InstallFilesProvider)
			for suite, sharedLibModulesInSuite := range suiteToSharedLibModules {
				if sharedLibModulesInSuite[makeName.Name] {
					for _, f := range installFilesProvider.InstallFiles {
						if strings.HasSuffix(f.String(), ".so") && strings.HasPrefix(f.String(), hostOut) {
							hostSharedLibs[suite] = append(hostSharedLibs[suite], f)
						}
					}
				}
			}
		}
	})
	for suite, files := range hostSharedLibs {
		hostSharedLibs[suite] = android.SortedUniquePaths(files)
	}

	return hostSharedLibs
}

type testSuiteConfig struct {
	name                           string
	buildHostSharedLibsZip         bool
	includeHostSharedLibsInMainZip bool
	hostJavaToolFiles              android.Paths
}

func buildTestSuite(ctx android.SingletonContext, suiteName string, files testModulesInstallsMap) (android.Path, android.Path) {
	var installedPaths android.Paths
	for _, module := range files.testModules() {
		installedPaths = append(installedPaths, files[module].Paths()...)
	}

	installedPaths = android.SortedUniquePaths(installedPaths)

	outputFile := pathForPackaging(ctx, suiteName+".zip")
	rule := android.NewRuleBuilder(pctx, ctx)
	rule.Command().BuiltTool("soong_zip").
		FlagWithOutput("-o ", outputFile).
		FlagWithArg("-P ", "host/testcases").
		FlagWithArg("-C ", pathForTestCases(ctx).String()).
		FlagWithRspFileInputList("-r ", outputFile.ReplaceExtension(ctx, "rsp"), installedPaths).
		Flag("-sha256") // necessary to save cas_uploader's time

	testList := buildTestList(ctx, suiteName+"_list", installedPaths)
	testListZipOutputFile := pathForPackaging(ctx, suiteName+"_list.zip")

	rule.Command().BuiltTool("soong_zip").
		FlagWithOutput("-o ", testListZipOutputFile).
		FlagWithArg("-C ", pathForPackaging(ctx).String()).
		FlagWithInput("-f ", testList).
		Flag("-sha256")

	rule.Build(strings.ReplaceAll(suiteName, "-", "_")+"_zip", suiteName+".zip")

	return outputFile, testListZipOutputFile
}

func buildTestList(ctx android.SingletonContext, listFile string, installedPaths android.Paths) android.Path {
	buf := &strings.Builder{}
	for _, p := range installedPaths {
		if p.Ext() != ".config" {
			continue
		}
		pc, err := toTestListPath(p.String(), pathForTestCases(ctx).String(), "host/testcases")
		if err != nil {
			ctx.Errorf("Failed to convert path: %s, %v", p.String(), err)
			continue
		}
		buf.WriteString(pc)
		buf.WriteString("\n")
	}
	outputFile := pathForPackaging(ctx, listFile)
	android.WriteFileRuleVerbatim(ctx, outputFile, buf.String())
	return outputFile
}

func toTestListPath(path, relativeRoot, prefix string) (string, error) {
	dest, err := filepath.Rel(relativeRoot, path)
	if err != nil {
		return "", err
	}
	return filepath.Join(prefix, dest), nil
}

func pathForPackaging(ctx android.PathContext, pathComponents ...string) android.OutputPath {
	pathComponents = append([]string{"packaging"}, pathComponents...)
	return android.PathForOutput(ctx, pathComponents...)
}

func pathForTestCases(ctx android.PathContext) android.InstallPath {
	return android.PathForHostInstall(ctx, "testcases")
}

func packageTestSuite(ctx android.SingletonContext, files, sharedLibs android.Paths, suiteConfig testSuiteConfig) {
	hostOutTestCases := android.PathForHostInstall(ctx, "testcases")
	targetOutTestCases := android.PathForDeviceFirstInstall(ctx, "testcases")
	hostOut := filepath.Dir(hostOutTestCases.String())
	targetOut := filepath.Dir(targetOutTestCases.String())

	testsZip := pathForPackaging(ctx, suiteConfig.name+".zip")
	testsListTxt := pathForPackaging(ctx, suiteConfig.name+"_list.txt")
	testsListZip := pathForPackaging(ctx, suiteConfig.name+"_list.zip")
	testsConfigsZip := pathForPackaging(ctx, suiteConfig.name+"_configs.zip")
	testsHostSharedLibsZip := pathForPackaging(ctx, suiteConfig.name+"_host-shared-libs.zip")
	var listLines []string

	// use intermediate files to hold the file inputs, to prevent argument list from being too long
	testsZipCmdHostFileInput := android.PathForIntermediates(ctx, suiteConfig.name+"_host_list.txt")
	testsZipCmdTargetFileInput := android.PathForIntermediates(ctx, suiteConfig.name+"_target_list.txt")
	var testsZipCmdHostFileInputContent, testsZipCmdTargetFileInputContent []string

	testsZipBuilder := android.NewRuleBuilder(pctx, ctx)
	testsZipCmd := testsZipBuilder.Command().
		BuiltTool("soong_zip").
		Flag("-sha256").
		Flag("-d").
		FlagWithOutput("-o ", testsZip).
		FlagWithArg("-P ", "host").
		FlagWithArg("-C ", hostOut)

	testsConfigsZipBuilder := android.NewRuleBuilder(pctx, ctx)
	testsConfigsZipCmd := testsConfigsZipBuilder.Command().
		BuiltTool("soong_zip").
		Flag("-d").
		FlagWithOutput("-o ", testsConfigsZip).
		FlagWithArg("-P ", "host").
		FlagWithArg("-C ", hostOut)

	for _, f := range files {
		if strings.HasPrefix(f.String(), hostOutTestCases.String()) {
			testsZipCmdHostFileInputContent = append(testsZipCmdHostFileInputContent, f.String())
			testsZipCmd.Implicit(f)

			if strings.HasSuffix(f.String(), ".config") {
				testsConfigsZipCmd.FlagWithInput("-f ", f)
				listLines = append(listLines, strings.Replace(f.String(), hostOut, "host", 1))
			}
		}
	}

	if suiteConfig.includeHostSharedLibsInMainZip {
		for _, f := range sharedLibs {
			if strings.HasPrefix(f.String(), hostOutTestCases.String()) {
				testsZipCmdHostFileInputContent = append(testsZipCmdHostFileInputContent, f.String())
				testsZipCmd.Implicit(f)
			}
		}
	}

	android.WriteFileRule(ctx, testsZipCmdHostFileInput, strings.Join(testsZipCmdHostFileInputContent, " "))

	testsZipCmd.
		FlagWithArg("-P ", "host").
		FlagWithInput("-l ", testsZipCmdHostFileInput).
		FlagWithArg("-P ", "target").
		FlagWithArg("-C ", targetOut)
	testsConfigsZipCmd.
		FlagWithArg("-P ", "target").
		FlagWithArg("-C ", targetOut)

	for _, f := range files {
		if strings.HasPrefix(f.String(), targetOutTestCases.String()) {
			testsZipCmdTargetFileInputContent = append(testsZipCmdTargetFileInputContent, f.String())
			testsZipCmd.Implicit(f)

			if strings.HasSuffix(f.String(), ".config") {
				testsConfigsZipCmd.FlagWithInput("-f ", f)
				listLines = append(listLines, strings.Replace(f.String(), targetOut, "target", 1))
			}
		}
	}

	android.WriteFileRule(ctx, testsZipCmdTargetFileInput, strings.Join(testsZipCmdTargetFileInputContent, " "))
	testsZipCmd.FlagWithInput("-l ", testsZipCmdTargetFileInput)

	if len(suiteConfig.hostJavaToolFiles) > 0 {
		testsZipCmd.FlagWithArg("-P ", "host/tools")
		testsZipCmd.Flag("-j")

		for _, hostJavaTool := range suiteConfig.hostJavaToolFiles {
			testsZipCmd.FlagWithInput("-f ", hostJavaTool)
		}
	}

	testsZipBuilder.Build(suiteConfig.name, "building "+suiteConfig.name+" zip")
	testsConfigsZipBuilder.Build(suiteConfig.name+"-configs", "building "+suiteConfig.name+" configs zip")

	if suiteConfig.buildHostSharedLibsZip {
		testsHostSharedLibsZipBuilder := android.NewRuleBuilder(pctx, ctx)
		testsHostSharedLibsZipCmd := testsHostSharedLibsZipBuilder.Command().
			BuiltTool("soong_zip").
			Flag("-d").
			FlagWithOutput("-o ", testsHostSharedLibsZip).
			FlagWithArg("-P ", "host").
			FlagWithArg("-C ", hostOut)

		for _, f := range sharedLibs {
			if strings.HasPrefix(f.String(), hostOutTestCases.String()) && strings.HasSuffix(f.String(), ".so") {
				testsHostSharedLibsZipCmd.FlagWithInput("-f ", f)
			}
		}

		testsHostSharedLibsZipBuilder.Build(suiteConfig.name+"-host-shared-libs", "building "+suiteConfig.name+"host shared libs")
	}

	android.WriteFileRule(ctx, testsListTxt, strings.Join(listLines, "\n"))

	testsListZipBuilder := android.NewRuleBuilder(pctx, ctx)
	testsListZipBuilder.Command().
		BuiltTool("soong_zip").
		Flag("-d").
		FlagWithOutput("-o ", testsListZip).
		FlagWithArg("-e ", suiteConfig.name+"_list").
		FlagWithInput("-f ", testsListTxt)
	testsListZipBuilder.Build(suiteConfig.name+"_list_zip", "building "+suiteConfig.name+" list zip")

	ctx.Phony(suiteConfig.name, testsZip, testsListZip, testsConfigsZip)
	ctx.DistForGoal(suiteConfig.name, testsZip, testsListZip, testsConfigsZip)
	if suiteConfig.buildHostSharedLibsZip {
		ctx.DistForGoal(suiteConfig.name, testsHostSharedLibsZip)
	}
	ctx.Phony("tests", android.PathForPhony(ctx, suiteConfig.name))
}

func buildCompatibilitySuitePackage(
	ctx android.SingletonContext,
	suite compatibilitySuitePackageInfo,
	testSuiteFiles android.Paths,
	testSuiteModules []android.ModuleProxy,
) {
	testSuiteName := suite.Name
	subdir := fmt.Sprintf("android-%s", testSuiteName)

	hostOutSuite := android.PathForHostInstall(ctx, testSuiteName)
	hostOutTestCases := hostOutSuite.Join(ctx, subdir, "testcases")
	hostOutTools := hostOutSuite.Join(ctx, subdir, "tools")
	testSuiteFiles = slices.DeleteFunc(testSuiteFiles, func(f android.Path) bool {
		return !strings.HasPrefix(f.String(), hostOutTestCases.String()+"/")
	})

	// Some make rules still rely on the zip being at this location
	out := hostOutSuite.Join(ctx, fmt.Sprintf("android-%s.zip", testSuiteName))
	builder := android.NewRuleBuilder(pctx, ctx)
	cmd := builder.Command().BuiltTool("soong_zip").
		FlagWithOutput("-o ", out).
		FlagWithArg("-e ", subdir+"/tools/version.txt").
		FlagWithInput("-f ", ctx.Config().BuildNumberFile(ctx))

	// Tools need to be copied to the test suite folder for other tools to use, like
	// <suite>-tradefed run commandAndExit
	copyTool := func(tool android.Path) {
		builder.Command().Text("cp").Input(tool).Output(hostOutTools.Join(ctx, tool.Base()))
	}

	hostTools := suite.ToolFiles
	for _, hostTool := range hostTools {
		cmd.
			FlagWithArg("-e ", subdir+"/tools/"+hostTool.Base()).
			FlagWithInput("-f ", hostTool)
		copyTool(hostTool)
	}

	if suite.Readme != nil {
		cmd.
			FlagWithArg("-e ", subdir+"/tools/"+suite.Readme.Base()).
			FlagWithInput("-f ", suite.Readme)
		copyTool(suite.Readme)
	}

	if suite.DynamicConfig != nil {
		cmd.
			FlagWithArg("-e ", subdir+"/testcases/"+testSuiteName+".dynamic").
			FlagWithInput("-f ", suite.DynamicConfig)
		builder.Command().Text("cp").Input(suite.DynamicConfig).Output(hostOutTestCases.Join(ctx, testSuiteName+".dynamic"))
	}

	licenceInfos := slices.Concat(android.GetNoticeModuleInfos(ctx, testSuiteModules), suite.ToolNoticeInfo)
	if len(licenceInfos) > 0 {
		notice := android.PathForOutput(ctx, "compatibility_test_suites", testSuiteName, "NOTICE.txt")
		android.BuildNoticeTextOutputFromNoticeModuleInfos(ctx, notice, "compatibility_"+testSuiteName, "Test suites",
			android.BuildNoticeFromLicenseDataArgs{
				Title:       "Notices for files contained in the test suites filesystem image:",
				StripPrefix: []string{hostOutSuite.String()},
				Filter:      slices.Concat(testSuiteFiles.Strings(), hostTools.Strings()),
				Replace: []android.NoticeReplace{
					{
						From: ctx.Config().HostJavaToolPath(ctx, "").String(),
						To:   "/" + subdir + "/tools",
					},
					{
						From: ctx.Config().HostToolPath(ctx, "").String(),
						To:   "/" + subdir + "/tools",
					},
				},
			},
			licenceInfos)

		cmd.
			FlagWithArg("-e ", subdir+"/NOTICE.txt").
			FlagWithInput("-f ", notice)
	}

	cmd.FlagWithArg("-C ", hostOutSuite.String())
	for _, f := range testSuiteFiles {
		cmd.FlagWithInput("-f ", f)
	}

	addJdkToZip(ctx, cmd, subdir)

	builder.Build("compatibility_zip_"+testSuiteName, fmt.Sprintf("Compatibility test suite zip %q", testSuiteName))

	ctx.Phony(testSuiteName, out)
	ctx.DistForGoal(testSuiteName, out)
}

func addJdkToZip(ctx android.SingletonContext, command *android.RuleBuilderCommand, subdir string) {
	jdkHome := filepath.Dir(ctx.Config().Getenv("ANDROID_JAVA_HOME")) + "/linux-x86"
	glob := jdkHome + "/**/*"
	files, err := ctx.GlobWithDeps(glob, nil)
	if err != nil {
		ctx.Errorf("Could not glob %s: %s", glob, err)
		return
	}
	paths := android.PathsForSource(ctx, files)

	command.
		FlagWithArg("-P ", subdir+"/jdk").
		FlagWithArg("-C ", jdkHome).
		FlagWithArg("-D ", jdkHome).
		Flag("-sha256").Implicits(paths)
}

// compatibility_test_suite_package builds a zip file of all the tests tagged with this suite's
// name. It's the equivalent of compatibility.mk from the make build system.
//
// In soong, the module does nothing. But a singleton finds all the compatibility_test_suite_package
// modules in the tree and emits build rules for them. This is because they need to search
// all the modules in the tree for ones tagged with the appropriate test suite, but this could
// maybe be replaced with reverse dependencies.
func compatibilityTestSuitePackageFactory() android.Module {
	m := &compatibilityTestSuitePackage{}
	android.InitAndroidModule(m)
	m.AddProperties(&m.properties)
	return m
}

type compatibilityTestSuitePackageProperties struct {
	Tradefed       *string
	Readme         *string `android:"path"`
	Tools          []string
	Dynamic_config *string `android:"path"`
}

type compatibilityTestSuitePackage struct {
	android.ModuleBase
	properties compatibilityTestSuitePackageProperties
}

type compatibilitySuitePackageInfo struct {
	Name           string
	Readme         android.Path
	DynamicConfig  android.Path
	ToolFiles      android.Paths
	ToolNoticeInfo android.NoticeModuleInfos
}

var compatibilitySuitePackageProvider = blueprint.NewProvider[compatibilitySuitePackageInfo]()

type ctspTradefedDeptagType struct {
	blueprint.BaseDependencyTag
}

var ctspTradefedDeptag = ctspTradefedDeptagType{}

type ctspHostJavaToolDeptagType struct {
	blueprint.BaseDependencyTag
}

var ctspHostJavaToolDeptag = ctspHostJavaToolDeptagType{}

type ctspHostToolDeptagType struct {
	blueprint.BaseDependencyTag
}

var ctspHostToolDeptag = ctspHostToolDeptagType{}

func (m *compatibilityTestSuitePackage) DepsMutator(ctx android.BottomUpMutatorContext) {
	variations := ctx.Config().BuildOSTarget.Variations()
	ctx.AddVariationDependencies(variations, ctspTradefedDeptag, proptools.String(m.properties.Tradefed))
	ctx.AddVariationDependencies(variations, ctspHostJavaToolDeptag, proptools.String(m.properties.Tradefed)+"-tests")
	ctx.AddVariationDependencies(variations, ctspHostJavaToolDeptag, "tradefed")
	ctx.AddVariationDependencies(variations, ctspHostJavaToolDeptag, "loganalysis")
	ctx.AddVariationDependencies(variations, ctspHostJavaToolDeptag, "compatibility-host-util")
	ctx.AddVariationDependencies(variations, ctspHostJavaToolDeptag, "compatibility-tradefed")
	ctx.AddVariationDependencies(variations, ctspHostToolDeptag, "test-utils-script")
	for _, tool := range m.properties.Tools {
		ctx.AddVariationDependencies(variations, ctspHostJavaToolDeptag, tool)
	}
}

func (m *compatibilityTestSuitePackage) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	if matched, err := regexp.MatchString("[a-zA-Z0-9_-]+", m.Name()); err != nil || !matched {
		ctx.ModuleErrorf("Invalid test suite name, must match [a-zA-Z0-9_-]+, got %q", m.Name())
		return
	}

	tradefedName := proptools.String(m.properties.Tradefed)
	tradefed := ctx.GetDirectDepProxyWithTag(tradefedName, ctspTradefedDeptag)

	tradefedFiles := android.OtherModuleProviderOrDefault(ctx, tradefed, android.InstallFilesProvider).InstallFiles

	if len(tradefedFiles) != 2 || tradefedFiles[0].Base() != tradefedName || tradefedFiles[1].Base() != tradefedName+".jar" {
		ctx.PropertyErrorf("tradefed", "Dependency %q did not provide expected files, produced: %s", tradefedName, tradefedFiles.Strings())
	}

	var toolFiles android.Paths
	for _, f := range tradefedFiles {
		toolFiles = append(toolFiles, f)
	}
	toolNoticeinfo := android.NoticeModuleInfos{android.GetNoticeModuleInfo(ctx, tradefed)}

	ctx.VisitDirectDepsProxyWithTag(ctspHostJavaToolDeptag, func(dep android.ModuleProxy) {
		file := android.OtherModuleProviderOrDefault(ctx, dep, java.JavaInfoProvider).InstallFile
		if file == nil {
			ctx.PropertyErrorf("tools", "Dependency %q did not provide java installfile, is it a java module?", ctx.OtherModuleName(dep))
		}
		toolFiles = append(toolFiles, file)
		toolNoticeinfo = append(toolNoticeinfo, android.GetNoticeModuleInfo(ctx, dep))
	})

	ctx.VisitDirectDepsProxyWithTag(ctspHostToolDeptag, func(dep android.ModuleProxy) {
		files := android.OtherModuleProviderOrDefault(ctx, dep, android.InstallFilesProvider).InstallFiles
		if len(files) != 1 {
			ctx.ModuleErrorf("Dependency %q did not provide expected single file", ctx.OtherModuleName(dep))
		}
		for _, f := range files {
			toolFiles = append(toolFiles, f)
		}
		toolNoticeinfo = append(toolNoticeinfo, android.GetNoticeModuleInfo(ctx, dep))
	})

	var readme android.Path
	if m.properties.Readme != nil {
		readme = android.PathForModuleSrc(ctx, *m.properties.Readme)
	}

	var dynamicConfig android.Path
	if m.properties.Dynamic_config != nil {
		dynamicConfig = android.PathForModuleSrc(ctx, *m.properties.Dynamic_config)
	}

	android.SetProvider(ctx, compatibilitySuitePackageProvider, compatibilitySuitePackageInfo{
		Name:           m.Name(),
		Readme:         readme,
		DynamicConfig:  dynamicConfig,
		ToolFiles:      toolFiles,
		ToolNoticeInfo: toolNoticeinfo,
	})
}

func testSuitePackageFactory() android.Module {
	m := &testSuitePackage{}
	android.InitAndroidModule(m)
	m.AddProperties(&m.properties)
	return m
}

type testSuitePackageProperties struct {
	Build_host_shared_libs_zip           *bool
	Include_host_shared_libs_in_main_zip *bool
	Host_java_tools                      []string
}

type testSuitePackage struct {
	android.ModuleBase
	properties testSuitePackageProperties
}

var testSuitePackageProvider = blueprint.NewProvider[testSuiteConfig]()

type tspHostJavaToolDeptagType struct {
	blueprint.BaseDependencyTag
}

var tspHostJavaToolDeptag = tspHostJavaToolDeptagType{}

func (t *testSuitePackage) DepsMutator(ctx android.BottomUpMutatorContext) {
	variations := ctx.Config().BuildOSTarget.Variations()
	ctx.AddVariationDependencies(variations, tspHostJavaToolDeptag, t.properties.Host_java_tools...)
}

// testSuitePackage does not have build actions of its own.
// It sets a provider with info about its packaging config.
// A singleton will create the build rules to create the .zip files.
func (t *testSuitePackage) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	var toolFiles android.Paths
	ctx.VisitDirectDepsProxyWithTag(tspHostJavaToolDeptag, func(dep android.ModuleProxy) {
		file := android.OtherModuleProviderOrDefault(ctx, dep, java.JavaInfoProvider).InstallFile
		if file == nil {
			ctx.PropertyErrorf("host_java_tools", "Dependency %q did not provide java installfile, is it a java module?", ctx.OtherModuleName(dep))
		}
		toolFiles = append(toolFiles, file)
	})

	android.SetProvider(ctx, testSuitePackageProvider, testSuiteConfig{
		name:                           t.Name(),
		buildHostSharedLibsZip:         proptools.Bool(t.properties.Build_host_shared_libs_zip),
		includeHostSharedLibsInMainZip: proptools.Bool(t.properties.Include_host_shared_libs_in_main_zip),
		hostJavaToolFiles:              toolFiles,
	})
}
