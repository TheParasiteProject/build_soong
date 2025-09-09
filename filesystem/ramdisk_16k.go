// Copyright (C) 2025 The Android Open Source Project
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

package filesystem

import (
	"fmt"
	"strings"

	"github.com/google/blueprint/proptools"

	"android/soong/android"
)

type ramdisk16kImg struct {
	android.ModuleBase
	properties Ramdisk16kImgProperties
}

type Ramdisk16kImgProperties struct {
	// List or filegroup of prebuilt kernel module files. Should have .ko suffix.
	Srcs []string `android:"path,arch_variant"`

	// List specifying load order of kernel modules.
	Load []string

	// Path to the prebuilt 16KB kernel
	Kernel *string `android:"path"`
}

func Ramdisk16kImgFactory() android.Module {
	module := &ramdisk16kImg{}
	android.InitAndroidArchModule(module, android.DeviceSupported, android.MultilibFirst)
	module.AddProperties(&module.properties)
	return module
}

// Extracts version information from the kernel and packages the .ko modules in
// a version-specific subdirectory of the .img file.
func (p *ramdisk16kImg) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	if len(p.properties.Srcs) == 0 {
		return
	}
	outputDir := android.PathForModuleOut(ctx, "ramdisk_16k")
	output := outputDir.Join(ctx, "ramdisk_16k.img")
	intermediatesDir := outputDir.Join(ctx, "intermediates")
	builder := android.NewRuleBuilder(pctx, ctx).Sbox(
		outputDir,
		android.PathForModuleOut(ctx, "ramdisk_16k_intermediates.textproto"),
	)

	extractKernel := android.PathForSource(ctx, "build/make/tools/extract_kernel.py")
	lz4 := ctx.Config().HostToolPath(ctx, "lz4")

	// Determine the kernel version during execution.
	builder.Command().
		Textf("KERNEL_RELEASE=`").
		Input(extractKernel).
		Textf("--tools lz4:%s", lz4).Implicit(lz4).
		FlagWithInput("--input ", android.PathForModuleSrc(ctx, proptools.String(p.properties.Kernel))).
		Text("--output-release` ; ").
		Textf("IS_16K_KERNEL=`").
		Input(extractKernel).
		Textf("--tools lz4:%s", lz4).Implicit(lz4).
		FlagWithInput("--input ", android.PathForModuleSrc(ctx, proptools.String(p.properties.Kernel))).
		Flag("--output-config` ; ").
		Text(" if [[ \"$IS_16K_KERNEL\" == *\"CONFIG_ARM64_16K_PAGES=y\"* ]]; then SUFFIX=_16k; fi")

	modulesLoadFile := p.createModulesLoadFile(ctx)
	// Copy the .ko files and modules.load to a staging directory.
	// Kernel version is one of the path components of the staging directory.
	builder.Command().
		Textf("mkdir -p %s/lib/modules/\"$KERNEL_RELEASE\"\"$SUFFIX\"", intermediatesDir).
		Textf("&& cp -t %s/lib/modules/\"$KERNEL_RELEASE\"\"$SUFFIX\"", intermediatesDir).
		Inputs(android.PathsForModuleSrc(ctx, p.properties.Srcs)).
		Input(modulesLoadFile)

	// Run depmod.
	// This implementation is sligtly different than make, which first copies the .ko
	// files to lib/modules/0.0, runs depmod, and then does a recursive cp to the final
	// staging directory with kernel version as one of the path components.
	builder.Command().
		BuiltTool("depmod").
		Flag("-b").
		Flag(intermediatesDir.String()).
		Flag("\"$KERNEL_RELEASE\"\"$SUFFIX\"") // FIX

	builder.Command().
		BuiltTool("mkbootfs").
		Text(intermediatesDir.String()).
		Text(" | ").
		BuiltTool("lz4").
		Flag("-l").
		Flag("-12").
		Flag("--favor-decSpeed").
		FlagWithOutput(" > ", output)

	builder.Build("ramdisk_16k", "ramdisk_16k")

	android.SetProvider(ctx, FilesystemProvider, FilesystemInfo{
		Output: output,
	})
	android.SetProvider(ctx, ramdiskFragmentInfoProvider, ramdiskFragmentInfo{
		Output:       output,
		Ramdisk_name: "16K",
	})
}

func (p *ramdisk16kImg) createModulesLoadFile(ctx android.ModuleContext) android.Path {
	var loadOrder []string
	if len(p.properties.Load) > 0 {
		loadOrder = p.properties.Load
	} else {
		for _, src := range android.PathsForModuleSrc(ctx, p.properties.Srcs) {
			loadOrder = append(loadOrder, src.Base())
		}
	}

	modulesLoadFile := android.PathForModuleOut(ctx, "modules.load")
	var contents strings.Builder
	for _, l := range loadOrder {
		contents.WriteString(fmt.Sprintf("%s\n", l))
	}
	android.WriteFileRuleVerbatim(ctx, modulesLoadFile, contents.String())
	return modulesLoadFile
}
