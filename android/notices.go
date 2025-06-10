// Copyright 2019 Google Inc. All rights reserved.
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
	"path/filepath"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

func modulesOutputDirs(ctx BuilderContext, modules ...ModuleProxy) []string {
	dirs := make([]string, 0, len(modules))
	for _, module := range modules {
		paths, err := outputFilesForModule(ctx, module, "")
		if err != nil {
			continue
		}
		for _, path := range paths {
			if path != nil {
				dirs = append(dirs, filepath.Dir(path.String()))
			}
		}
	}
	return SortedUniqueStrings(dirs)
}

type BuilderAndOtherModuleProviderContext interface {
	BuilderContext
	OtherModuleProviderContext
}

func modulesLicenseMetadata(ctx OtherModuleProviderContext, modules ...ModuleProxy) Paths {
	result := make(Paths, 0, len(modules))
	mctx, isMctx := ctx.(ModuleContext)
	for _, module := range modules {
		var mf Path
		if isMctx && EqualModules(mctx.Module(), module) {
			mf = mctx.LicenseMetadataFile()
		} else {
			mf = OtherModuleProviderOrDefault(ctx, module, InstallFilesProvider).LicenseMetadataFile
		}
		if mf != nil {
			result = append(result, mf)
		}
	}
	return result
}

// buildNoticeOutputFromLicenseMetadata writes out a notice file.
func buildNoticeOutputFromLicenseMetadata(
	ctx BuilderAndOtherModuleProviderContext, tool, ruleName string, outputFile WritablePath,
	libraryName string, extraArgs BuildNoticeFromLicenseDataArgs, modules ...ModuleProxy) {
	depsFile := outputFile.ReplaceExtension(ctx, strings.TrimPrefix(outputFile.Ext()+".d", "."))
	if len(modules) == 0 {
		if mctx, ok := ctx.(ModuleContext); ok {
			modules = []ModuleProxy{{blueprint.CreateModuleProxy(mctx.Module())}}
		} else {
			panic(fmt.Errorf("%s %q needs a module to generate the notice for", ruleName, libraryName))
		}
	}
	if libraryName == "" {
		libraryName = modules[0].Name()
	}
	rule := NewRuleBuilder(pctx, ctx)
	cmd := rule.Command().
		BuiltTool(tool).
		FlagWithOutput("-o ", outputFile).
		FlagWithDepFile("-d ", depsFile).
		FlagForEachArg("--strip_prefix ", extraArgs.StripPrefix).
		FlagForEachArg("--strip_prefix ", modulesOutputDirs(ctx, modules...))

	if libraryName != "" {
		cmd.FlagWithArg("--product ", proptools.ShellEscapeIncludingSpaces(libraryName))
	}
	if extraArgs.Title != "" {
		cmd.FlagWithArg("--title ", proptools.ShellEscapeIncludingSpaces(extraArgs.Title))
	}
	// The difference between nil and empty slice matters here, to allow for empty filter sets
	if extraArgs.Filter != nil {
		cmd.Flag("--filter")
		cmd.FlagForEachArg("--filter_to ", extraArgs.Filter)
	}
	cmd.FlagForEachArg("--replace ", extraArgs.Replace.Args())
	cmd.Inputs(modulesLicenseMetadata(ctx, modules...))
	rule.Build(ruleName, "container notice file")
}

type NoticeReplace struct {
	From string
	To   string
}

type NoticeReplaces []NoticeReplace

func (r *NoticeReplaces) Args() []string {
	var result []string
	for _, i := range *r {
		result = append(result, i.From+":::"+i.To)
	}
	return result
}

// Additional optional arguments that can be passed to notice building functions
type BuildNoticeFromLicenseDataArgs struct {
	Title       string
	StripPrefix []string
	Filter      []string
	Replace     NoticeReplaces
}

// BuildNoticeTextOutputFromLicenseMetadata writes out a notice text file based
// on the license metadata files for the input `modules` defaulting to the
// current context module if none given.
func BuildNoticeTextOutputFromLicenseMetadata(
	ctx BuilderAndOtherModuleProviderContext, outputFile WritablePath, ruleName, libraryName string,
	extraArgs BuildNoticeFromLicenseDataArgs, modules ...ModuleProxy) {
	buildNoticeOutputFromLicenseMetadata(ctx, "textnotice", "text_notice_"+ruleName,
		outputFile, libraryName, extraArgs, modules...)
}

// BuildNoticeHtmlOutputFromLicenseMetadata writes out a notice text file based
// on the license metadata files for the input `modules` defaulting to the
// current context module if none given.
func BuildNoticeHtmlOutputFromLicenseMetadata(
	ctx BuilderAndOtherModuleProviderContext, outputFile WritablePath, ruleName, libraryName string,
	extraArgs BuildNoticeFromLicenseDataArgs, modules ...ModuleProxy) {
	buildNoticeOutputFromLicenseMetadata(ctx, "htmlnotice", "html_notice_"+ruleName,
		outputFile, libraryName, extraArgs, modules...)
}

// BuildNoticeXmlOutputFromLicenseMetadata writes out a notice text file based
// on the license metadata files for the input `modules` defaulting to the
// current context module if none given.
func BuildNoticeXmlOutputFromLicenseMetadata(
	ctx BuilderAndOtherModuleProviderContext, outputFile WritablePath, ruleName, libraryName string,
	extraArgs BuildNoticeFromLicenseDataArgs, modules ...ModuleProxy) {
	buildNoticeOutputFromLicenseMetadata(ctx, "xmlnotice", "xml_notice_"+ruleName,
		outputFile, libraryName, extraArgs, modules...)
}
