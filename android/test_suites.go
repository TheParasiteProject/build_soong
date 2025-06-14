// Copyright 2020 Google Inc. All rights reserved.
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

package android

import (
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

//go:generate go run ../../blueprint/gobtools/codegen/gob_gen.go

func init() {
	RegisterParallelSingletonType("testsuites", testSuiteFilesFactory)
}

type compatibilitySuite struct {
	name     string
	tradefed string
	readme   string
	tools    []string
}

var all_compatibility_suites = []compatibilitySuite{
	{
		name:     "catbox",
		tradefed: "catbox-tradefed",
		readme:   "test/catbox/tools/catbox-tradefed/README",
		tools:    []string{"catbox-report-lib.jar"},
	},
}

func testSuiteFilesFactory() Singleton {
	return &testSuiteFiles{}
}

type testSuiteFiles struct{}

type TestSuiteModule interface {
	Module
	TestSuites() []string
}

// @auto-generate: gob
type TestSuiteInfo struct {
	// A suffix to append to the name of the test.
	// Useful because historically different variants of soong modules became differently-named
	// make modules, like "my_test.vendor" for the vendor variant.
	NameSuffix string

	TestSuites []string

	NeedsArchFolder bool

	MainFile Path

	MainFileStem string

	MainFileExt string

	ConfigFile Path

	ConfigFileSuffix string

	ExtraConfigs Paths

	PerTestcaseDirectory bool

	Data []DataPath

	NonArchData []DataPath

	CompatibilitySupportFiles []Path

	// Eqivalent of LOCAL_DISABLE_TEST_CONFIG in make
	DisableTestConfig bool
}

var TestSuiteInfoProvider = blueprint.NewProvider[TestSuiteInfo]()

// TestSuiteSharedLibsInfo is a provider of AndroidMk names of shared lib modules, for packaging
// shared libs into test suites. It's not intended as a general-purpose shared lib tracking
// mechanism. It's added to both test modules (to track their shared libs) and also shared lib
// modules (to track their transitive shared libs).
// @auto-generate: gob
type TestSuiteSharedLibsInfo struct {
	MakeNames []string
}

var TestSuiteSharedLibsInfoProvider = blueprint.NewProvider[TestSuiteSharedLibsInfo]()

// MakeNameInfoProvider records the AndroidMk name for the module. This will match the names
// referenced in TestSuiteSharedLibsInfo
// @auto-generate: gob
type MakeNameInfo struct {
	Name string
}

var MakeNameInfoProvider = blueprint.NewProvider[MakeNameInfo]()

// @auto-generate: gob
type filePair struct {
	src Path
	dst WritablePath
}

// @auto-generate: gob
type testSuiteInstallsInfo struct {
	Files              []filePair
	OneVariantInstalls []filePair
}

var testSuiteInstallsInfoProvider = blueprint.NewProvider[testSuiteInstallsInfo]()

type testModulesInstallsMap map[ModuleProxy]InstallPaths

func (t testModulesInstallsMap) testModules() []ModuleProxy {
	return slices.Collect(maps.Keys(t))
}

func (t *testSuiteFiles) GenerateBuildActions(ctx SingletonContext) {
	hostOutTestCases := pathForInstall(ctx, ctx.Config().BuildOSTarget.Os, ctx.Config().BuildOSTarget.Arch.ArchType, "testcases")
	files := make(map[string]testModulesInstallsMap)
	sharedLibRoots := make(map[string][]string)
	sharedLibGraph := make(map[string][]string)
	allTestSuiteInstalls := make(map[string]Paths)
	var toInstall []filePair
	var oneVariantInstalls []filePair

	ctx.VisitAllModuleProxies(func(m ModuleProxy) {
		commonInfo := OtherModuleProviderOrDefault(ctx, m, CommonModuleInfoProvider)
		testSuiteSharedLibsInfo := OtherModuleProviderOrDefault(ctx, m, TestSuiteSharedLibsInfoProvider)
		makeName := OtherModuleProviderOrDefault(ctx, m, MakeNameInfoProvider).Name
		if makeName != "" && commonInfo.Target.Os.Class == Host {
			sharedLibGraph[makeName] = append(sharedLibGraph[makeName], testSuiteSharedLibsInfo.MakeNames...)
		}

		if tsm, ok := OtherModuleProvider(ctx, m, TestSuiteInfoProvider); ok {
			installFilesProvider := OtherModuleProviderOrDefault(ctx, m, InstallFilesProvider)

			for _, testSuite := range tsm.TestSuites {
				if files[testSuite] == nil {
					files[testSuite] = make(testModulesInstallsMap)
				}
				files[testSuite][m] = append(files[testSuite][m],
					installFilesProvider.InstallFiles...)

				if makeName != "" {
					sharedLibRoots[testSuite] = append(sharedLibRoots[testSuite], makeName)
				}
			}

			if testSuiteInstalls, ok := OtherModuleProvider(ctx, m, testSuiteInstallsInfoProvider); ok {
				for _, testSuite := range tsm.TestSuites {
					for _, f := range testSuiteInstalls.Files {
						allTestSuiteInstalls[testSuite] = append(allTestSuiteInstalls[testSuite], f.dst)
					}
					for _, f := range testSuiteInstalls.OneVariantInstalls {
						allTestSuiteInstalls[testSuite] = append(allTestSuiteInstalls[testSuite], f.dst)
					}
				}
				installs := OtherModuleProviderOrDefault(ctx, m, InstallFilesProvider).InstallFiles
				oneVariantInstalls = append(oneVariantInstalls, testSuiteInstalls.OneVariantInstalls...)
				for _, f := range testSuiteInstalls.Files {
					alreadyInstalled := false
					for _, install := range installs {
						if install.String() == f.dst.String() {
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
	})

	for suite, suiteInstalls := range allTestSuiteInstalls {
		allTestSuiteInstalls[suite] = SortedUniquePaths(suiteInstalls)
	}

	hostSharedLibs := gatherHostSharedLibs(ctx, sharedLibRoots, sharedLibGraph)

	if !ctx.Config().KatiEnabled() {
		for _, testSuite := range SortedKeys(files) {
			testSuiteSymbolsZipFile := pathForTestSymbols(ctx, fmt.Sprintf("%s-symbols.zip", testSuite))
			testSuiteMergedMappingProtoFile := pathForTestSymbols(ctx, fmt.Sprintf("%s-symbols-mapping.textproto", testSuite))
			allTestModules := files[testSuite].testModules()
			allTestModulesOrProxy := make([]ModuleOrProxy, 0, len(allTestModules))
			for _, m := range allTestModules {
				allTestModulesOrProxy = append(allTestModulesOrProxy, m)
			}
			BuildSymbolsZip(ctx, allTestModulesOrProxy, testSuiteSymbolsZipFile, testSuiteMergedMappingProtoFile)

			ctx.DistForGoalWithFilenameTag(testSuite, testSuiteSymbolsZipFile, testSuiteSymbolsZipFile.Base())
			ctx.DistForGoalWithFilenameTag(testSuite, testSuiteMergedMappingProtoFile, testSuiteMergedMappingProtoFile.Base())
		}
	}

	// https://source.corp.google.com/h/googleplex-android/platform/superproject/main/+/main:build/make/core/main.mk;l=674;drc=46bd04e115d34fd62b3167128854dfed95290eb0
	testInstalledSharedLibs := make(map[string]Paths)
	testInstalledSharedLibsDeduper := make(map[string]bool)
	for _, install := range toInstall {
		testInstalledSharedLibsDeduper[install.dst.String()] = true
	}
	for _, suite := range []string{
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
	} {
		var myTestCases WritablePath = hostOutTestCases
		switch suite {
		case "vts", "tvts":
			suiteInfo := ctx.Config().productVariables.CompatibilityTestcases[suite]
			outDir := suiteInfo.OutDir
			if outDir == "" {
				continue
			}
			rel, err := filepath.Rel(ctx.Config().OutDir(), outDir)
			if err != nil || strings.HasPrefix(rel, "..") {
				panic(fmt.Sprintf("Could not make COMPATIBILITY_TESTCASES_OUT_%s (%s) relative to the out dir (%s)", suite, suiteInfo.OutDir, ctx.Config().OutDir()))
			}
			myTestCases = PathForArbitraryOutput(ctx, rel)
		}

		for _, f := range hostSharedLibs[suite] {
			dir := filepath.Base(filepath.Dir(f.String()))
			out := joinWriteablePath(ctx, myTestCases, dir, filepath.Base(f.String()))
			if _, ok := testInstalledSharedLibsDeduper[out.String()]; !ok {
				ctx.Build(pctx, BuildParams{
					Rule:   Cp,
					Input:  f,
					Output: out,
				})
			}
			testInstalledSharedLibsDeduper[out.String()] = true
			testInstalledSharedLibs[suite] = append(testInstalledSharedLibs[suite], out)
		}
	}

	filePairSorter := func(arr []filePair) func(i, j int) bool {
		return func(i, j int) bool {
			c := strings.Compare(arr[i].dst.String(), arr[j].dst.String())
			if c < 0 {
				return true
			} else if c > 0 {
				return false
			}
			return arr[i].src.String() < arr[j].src.String()
		}
	}

	sort.Slice(toInstall, filePairSorter(toInstall))
	// Dedup, as multiple tests may install the same test data to the same folder
	toInstall = slices.Compact(toInstall)

	// Dedup the oneVariant files by only the dst locations, and ignore installs from other variants
	sort.Slice(oneVariantInstalls, filePairSorter(oneVariantInstalls))
	oneVariantInstalls = slices.CompactFunc(oneVariantInstalls, func(a, b filePair) bool {
		return a.dst.String() == b.dst.String()
	})

	for _, install := range toInstall {
		ctx.Build(pctx, BuildParams{
			Rule:   Cp,
			Input:  install.src,
			Output: install.dst,
		})
	}
	for _, install := range oneVariantInstalls {
		ctx.Build(pctx, BuildParams{
			Rule:   Cp,
			Input:  install.src,
			Output: install.dst,
		})
	}

	robolectricZip, robolectrictListZip := buildTestSuite(ctx, "robolectric-tests", files["robolectric-tests"])
	ctx.Phony("robolectric-tests", robolectricZip, robolectrictListZip)
	ctx.DistForGoal("robolectric-tests", robolectricZip, robolectrictListZip)

	ravenwoodZip, ravenwoodListZip := buildTestSuite(ctx, "ravenwood-tests", files["ravenwood-tests"])
	ctx.Phony("ravenwood-tests", ravenwoodZip, ravenwoodListZip)
	ctx.DistForGoal("ravenwood-tests", ravenwoodZip, ravenwoodListZip)

	moblyTests := make(testModulesInstallsMap)
	pathForTestCasesString := pathForTestCases(ctx).String()
	for module, installedPaths := range files["mobly-tests"] {
		for _, installedPath := range installedPaths {
			if strings.HasPrefix(installedPath.String(), pathForTestCasesString) {
				moblyTests[module] = append(moblyTests[module], installedPath)
			}
		}
	}
	moblyZip, moblyListZip := buildTestSuite(ctx, "mobly-tests", moblyTests)
	ctx.Phony("mobly-tests", moblyZip, moblyListZip)
	ctx.DistForGoal("mobly-tests", moblyZip, moblyListZip)

	for _, testSuiteConfig := range testSuiteConfigs {
		files := allTestSuiteInstalls[testSuiteConfig.name]
		sharedLibs := testInstalledSharedLibs[testSuiteConfig.name]
		packageTestSuite(ctx, files, sharedLibs, testSuiteConfig)
	}

	for _, suite := range all_compatibility_suites {
		modules := slices.Collect(maps.Keys(files[suite.name]))
		sort.Slice(modules, func(i, j int) bool {
			return modules[i].String() < modules[j].String()
		})

		suite.build(ctx, slices.Clone(allTestSuiteInstalls[suite.name]), modules)
	}
}

// Get a mapping from testSuite -> list of host shared libraries, given:
// - sharedLibRoots: Mapping from testSuite -> androidMk name of all test modules in the suite
// - sharedLibGraph: Mapping from androidMk name of module -> androidMk names of its shared libs
//
// This mimics how make did it historically, which is filled with inaccuracies. Make didn't
// track variants and treated all variants as if they were merged into one big module. This means
// you can have a test that's only included in the "vts" test suite on the device variant, and
// only has a shared library on the host variant, and that shared library will still be included
// into the vts test suite.
func gatherHostSharedLibs(ctx SingletonContext, sharedLibRoots, sharedLibGraph map[string][]string) map[string]Paths {
	hostOutTestCases := pathForInstall(ctx, ctx.Config().BuildOSTarget.Os, ctx.Config().BuildOSTarget.Arch.ArchType, "testcases")
	hostOut := filepath.Dir(hostOutTestCases.String())

	for k, v := range sharedLibGraph {
		sharedLibGraph[k] = SortedUniqueStrings(v)
	}

	suiteToSharedLibModules := make(map[string]map[string]bool)
	for suite, modules := range sharedLibRoots {
		suiteToSharedLibModules[suite] = make(map[string]bool)
		var queue []string
		for _, root := range SortedUniqueStrings(modules) {
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

	hostSharedLibs := make(map[string]Paths)

	ctx.VisitAllModuleProxies(func(m ModuleProxy) {
		if makeName, ok := OtherModuleProvider(ctx, m, MakeNameInfoProvider); ok {
			commonInfo := OtherModuleProviderOrDefault(ctx, m, CommonModuleInfoProvider)
			if commonInfo.SkipInstall {
				return
			}
			installFilesProvider := OtherModuleProviderOrDefault(ctx, m, InstallFilesProvider)
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
		hostSharedLibs[suite] = SortedUniquePaths(files)
	}

	return hostSharedLibs
}

type testSuiteConfig struct {
	name                           string
	buildHostSharedLibsZip         bool
	includeHostSharedLibsInMainZip bool
	hostJavaTools                  []string
}

var testSuiteConfigs = []testSuiteConfig{
	{
		name: "performance-tests",
	},
	{
		name:                   "device-platinum-tests",
		buildHostSharedLibsZip: true,
	},
	{
		name:                           "device-tests",
		includeHostSharedLibsInMainZip: true,
	},
	{
		name: "automotive-tests",
	},
	{
		name:          "automotive-general-tests",
		hostJavaTools: []string{"compatibility-host-util", "cts-tradefed", "vts-tradefed"},
	},
	{
		name: "automotive-sdv-tests",
	},
}

func buildTestSuite(ctx SingletonContext, suiteName string, files testModulesInstallsMap) (Path, Path) {
	var installedPaths Paths
	for _, module := range files.testModules() {
		installedPaths = append(installedPaths, files[module].Paths()...)
	}

	installedPaths = SortedUniquePaths(installedPaths)

	outputFile := pathForPackaging(ctx, suiteName+".zip")
	rule := NewRuleBuilder(pctx, ctx)
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

func buildTestList(ctx SingletonContext, listFile string, installedPaths Paths) Path {
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
	WriteFileRuleVerbatim(ctx, outputFile, buf.String())
	return outputFile
}

func toTestListPath(path, relativeRoot, prefix string) (string, error) {
	dest, err := filepath.Rel(relativeRoot, path)
	if err != nil {
		return "", err
	}
	return filepath.Join(prefix, dest), nil
}

func pathForPackaging(ctx PathContext, pathComponents ...string) OutputPath {
	pathComponents = append([]string{"packaging"}, pathComponents...)
	return PathForOutput(ctx, pathComponents...)
}

func pathForTestCases(ctx PathContext) InstallPath {
	return pathForInstall(ctx, ctx.Config().BuildOS, X86, "testcases")
}

func pathForTestSymbols(ctx PathContext, pathComponents ...string) InstallPath {
	return pathForInstall(ctx, ctx.Config().BuildOS, ctx.Config().BuildArch, "", pathComponents...)
}

func packageTestSuite(ctx SingletonContext, files, sharedLibs Paths, suiteConfig testSuiteConfig) {
	hostOutTestCases := pathForInstall(ctx, ctx.Config().BuildOSTarget.Os, ctx.Config().BuildOSTarget.Arch.ArchType, "testcases")
	targetOutTestCases := pathForInstall(ctx, ctx.Config().AndroidFirstDeviceTarget.Os, ctx.Config().AndroidFirstDeviceTarget.Arch.ArchType, "testcases")
	hostOut := filepath.Dir(hostOutTestCases.String())
	targetOut := filepath.Dir(targetOutTestCases.String())

	testsZip := pathForPackaging(ctx, suiteConfig.name+".zip")
	testsListTxt := pathForPackaging(ctx, suiteConfig.name+"_list.txt")
	testsListZip := pathForPackaging(ctx, suiteConfig.name+"_list.zip")
	testsConfigsZip := pathForPackaging(ctx, suiteConfig.name+"_configs.zip")
	testsHostSharedLibsZip := pathForPackaging(ctx, suiteConfig.name+"_host-shared-libs.zip")
	var listLines []string

	// use intermediate files to hold the file inputs, to prevent argument list from being too long
	testsZipCmdHostFileInput := PathForIntermediates(ctx, suiteConfig.name+"_host_list.txt")
	testsZipCmdTargetFileInput := PathForIntermediates(ctx, suiteConfig.name+"_target_list.txt")
	var testsZipCmdHostFileInputContent, testsZipCmdTargetFileInputContent []string

	testsZipBuilder := NewRuleBuilder(pctx, ctx)
	testsZipCmd := testsZipBuilder.Command().
		BuiltTool("soong_zip").
		Flag("-sha256").
		Flag("-d").
		FlagWithOutput("-o ", testsZip).
		FlagWithArg("-P ", "host").
		FlagWithArg("-C ", hostOut)

	testsConfigsZipBuilder := NewRuleBuilder(pctx, ctx)
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

	WriteFileRule(ctx, testsZipCmdHostFileInput, strings.Join(testsZipCmdHostFileInputContent, " "))

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

	WriteFileRule(ctx, testsZipCmdTargetFileInput, strings.Join(testsZipCmdTargetFileInputContent, " "))
	testsZipCmd.FlagWithInput("-l ", testsZipCmdTargetFileInput)

	if len(suiteConfig.hostJavaTools) > 0 {
		testsZipCmd.FlagWithArg("-P ", "host/tools")
		testsZipCmd.Flag("-j")

		for _, hostJavaTool := range suiteConfig.hostJavaTools {
			testsZipCmd.FlagWithInput("-f ", ctx.Config().HostJavaToolPath(ctx, hostJavaTool+".jar"))
		}
	}

	testsZipBuilder.Build(suiteConfig.name, "building "+suiteConfig.name+" zip")
	testsConfigsZipBuilder.Build(suiteConfig.name+"-configs", "building "+suiteConfig.name+" configs zip")

	if suiteConfig.buildHostSharedLibsZip {
		testsHostSharedLibsZipBuilder := NewRuleBuilder(pctx, ctx)
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

	WriteFileRule(ctx, testsListTxt, strings.Join(listLines, "\n"))

	testsListZipBuilder := NewRuleBuilder(pctx, ctx)
	testsListZipBuilder.Command().
		BuiltTool("soong_zip").
		Flag("-d").
		FlagWithOutput("-o ", testsListZip).
		FlagWithArg("-e ", suiteConfig.name+"_list").
		FlagWithInput("-f ", testsListTxt)
	testsListZipBuilder.Build(suiteConfig.name+"_list_zip", "building "+suiteConfig.name+" list zip")

	ctx.Phony(suiteConfig.name, testsZip)
	ctx.DistForGoal(suiteConfig.name, testsZip, testsListZip, testsConfigsZip)
	if suiteConfig.buildHostSharedLibsZip {
		ctx.DistForGoal(suiteConfig.name, testsHostSharedLibsZip)
	}
	ctx.Phony("tests", PathForPhony(ctx, suiteConfig.name))
}

func (m *compatibilitySuite) build(ctx SingletonContext, testSuiteFiles Paths, testSuiteModules []ModuleProxy) {
	testSuiteName := m.name
	testSuiteTradefed := m.tradefed
	if matched, err := regexp.MatchString("[a-zA-Z0-9_-]+", testSuiteName); err != nil || !matched {
		ctx.Errorf("Invalid test suite name, must match [a-zA-Z0-9_-]+, got %q", testSuiteName)
		return
	}
	if matched, err := regexp.MatchString("[a-zA-Z0-9_-]+", testSuiteTradefed); err != nil || !matched {
		ctx.Errorf("Invalid test suite tradefed, must match [a-zA-Z0-9_-]+, got %q", testSuiteTradefed)
		return
	}
	subdir := fmt.Sprintf("android-%s", testSuiteName)

	hostOutSuite := pathForInstall(ctx, ctx.Config().BuildOSTarget.Os, ctx.Config().BuildOSTarget.Arch.ArchType, m.name)
	hostOutTestCases := pathForInstall(ctx, ctx.Config().BuildOSTarget.Os, ctx.Config().BuildOSTarget.Arch.ArchType, m.name, subdir, "testcases")
	testSuiteFiles = slices.DeleteFunc(testSuiteFiles, func(f Path) bool {
		return !strings.HasPrefix(f.String(), hostOutTestCases.String()+"/")
	})

	hostTools := Paths{
		ctx.Config().HostJavaToolPath(ctx, "tradefed.jar"),
		ctx.Config().HostJavaToolPath(ctx, "loganalysis.jar"),
		ctx.Config().HostJavaToolPath(ctx, "compatibility-host-util.jar"),
		ctx.Config().HostJavaToolPath(ctx, "compatibility-tradefed.jar"),
		ctx.Config().HostJavaToolPath(ctx, testSuiteTradefed+".jar"),
		ctx.Config().HostJavaToolPath(ctx, testSuiteTradefed+"-tests.jar"),
		ctx.Config().HostToolPath(ctx, testSuiteTradefed),
		ctx.Config().HostToolPath(ctx, "test-utils-script"),
	}

	out := PathForOutput(ctx, "compatibility_test_suites", testSuiteName, fmt.Sprintf("android-%s.zip", testSuiteName))
	builder := NewRuleBuilder(pctx, ctx)
	cmd := builder.Command().BuiltTool("soong_zip").
		FlagWithOutput("-o ", out).
		FlagWithArg("-e ", subdir+"/tools/version.txt").
		FlagWithInput("-f ", ctx.Config().BuildNumberFile(ctx))

	for _, hostTool := range hostTools {
		cmd.
			FlagWithArg("-e ", subdir+"/tools/"+hostTool.Base()).
			FlagWithInput("-f ", hostTool)
	}

	for _, tool := range m.tools {
		if matched, err := regexp.MatchString("[a-zA-Z0-9_-]+", tool); err != nil || !matched {
			ctx.Errorf("Invalid test suite tool, must match [a-zA-Z0-9_-]+, got %q", tool)
			continue
		}
		cmd.
			FlagWithArg("-e ", subdir+"/tools/"+tool).
			FlagWithInput("-f ", ctx.Config().HostJavaToolPath(ctx, tool))
	}

	if m.readme != "" {
		readme := ExistentPathForSource(ctx, m.readme)
		if readme.Valid() {
			cmd.
				FlagWithArg("-e ", subdir+"/tools/"+readme.Path().Base()).
				FlagWithInput("-f ", readme.Path())
		} else {
			// Defer error to execution time, as make historically did.
			builder.Command().Textf("echo Not found: %s && exit 1", proptools.ShellEscapeIncludingSpaces(m.readme))
		}
	}

	hostToolModules, err := m.getModulesForHostTools(ctx, hostTools)
	// Defer error to execution time, as make historically did.
	if err != nil {
		builder.Command().Textf("echo %s && exit 1", proptools.ShellEscapeIncludingSpaces(err.Error()))
	}
	modulesForLicense := slices.Concat(testSuiteModules, hostToolModules)
	if len(modulesForLicense) > 0 {
		notice := PathForOutput(ctx, "compatibility_test_suites", testSuiteName, "NOTICE.txt")
		BuildNoticeTextOutputFromLicenseMetadata(ctx, notice, "notice", "Test suites",
			BuildNoticeFromLicenseDataArgs{
				Title:       "Notices for files contained in the test suites filesystem image:",
				StripPrefix: []string{hostOutSuite.String()},
				Filter:      slices.Concat(testSuiteFiles.Strings(), hostTools.Strings()),
				Replace: []NoticeReplace{
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
			modulesForLicense...)

		cmd.
			FlagWithArg("-e ", subdir+"/NOTICE.txt").
			FlagWithInput("-f ", notice)
	}

	cmd.FlagWithArg("-C ", hostOutSuite.String())
	for _, f := range testSuiteFiles {
		cmd.FlagWithInput("-f ", f)
	}

	m.addJdk(ctx, cmd, subdir)

	builder.Build("compatibility_zip_"+testSuiteName, fmt.Sprintf("Compatibility test suite zip %q", testSuiteName))

	ctx.Phony(m.name, out)
	ctx.DistForGoal(m.name, out)
}

func (m *compatibilitySuite) addJdk(ctx SingletonContext, command *RuleBuilderCommand, subdir string) {
	jdkHome := filepath.Dir(ctx.Config().Getenv("ANDROID_JAVA_HOME")) + "/linux-x86"
	glob := jdkHome + "/**/*"
	files, err := ctx.GlobWithDeps(glob, nil)
	if err != nil {
		ctx.Errorf("Could not glob %s: %s", glob, err)
		return
	}
	paths := PathsForSource(ctx, files)

	command.
		FlagWithArg("-P ", subdir+"/jdk").
		FlagWithArg("-C ", jdkHome).
		FlagWithArg("-D ", jdkHome).
		Flag("-sha256").Implicits(paths)
}

func (m *compatibilitySuite) getModulesForHostTools(ctx SingletonContext, paths Paths) ([]ModuleProxy, error) {
	foundInstalledFiles := make(map[string]struct{})
	var modules []ModuleProxy
	pathStrings := paths.Strings()
	ctx.VisitAllModuleProxies(func(m ModuleProxy) {
		installFilesProvider := OtherModuleProviderOrDefault(ctx, m, InstallFilesProvider)

		found := false
		for _, installed := range installFilesProvider.InstallFiles {
			if slices.Contains(pathStrings, installed.String()) {
				if !found {
					modules = append(modules, m)
				}
				if _, ok := foundInstalledFiles[installed.String()]; ok {
					ctx.Errorf(fmt.Sprintf("File %q found by two different modules, one of them is %s(%s)", installed.String(), ctx.ModuleName(m), ctx.ModuleSubDir(m)))
					continue
				}
				foundInstalledFiles[installed.String()] = struct{}{}
				found = true
			}
		}
	})

	if len(foundInstalledFiles) != len(paths) {
		return nil, fmt.Errorf("Could not find modules for all compatibility files")
	}

	return modules, nil
}
