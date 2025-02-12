// Copyright (C) 2024 The Android Open Source Project
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
	"android/soong/android"
	"path/filepath"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

type SystemOtherImageProperties struct {
	// The system_other image always requires a reference to the system image. The system_other
	// partition gets built into the system partition's "b" slot in a/b partition builds. Thus, it
	// copies most of its configuration from the system image, such as filesystem type, avb signing
	// info, etc. Including it here does not automatically mean that it will pick up the system
	// image's dexpropt files, it must also be listed in Preinstall_dexpreopt_files_from for that.
	System_image *string

	// This system_other partition will include all the dexpreopt files from the apps on these
	// partitions.
	Preinstall_dexpreopt_files_from []string
}

type systemOtherImage struct {
	android.ModuleBase
	android.DefaultableModuleBase
	properties SystemOtherImageProperties
}

// The system_other image is the default contents of the "b" slot of the system image.
// It contains the dexpreopt files of all the apps on the device, for a faster first boot.
// Afterwards, at runtime, it will be used as a regular b slot for OTA updates, and the initial
// dexpreopt files will be deleted.
func SystemOtherImageFactory() android.Module {
	module := &systemOtherImage{}
	module.AddProperties(&module.properties)
	android.InitAndroidArchModule(module, android.DeviceSupported, android.MultilibCommon)
	android.InitDefaultableModule(module)
	return module
}

type systemImageDeptag struct {
	blueprint.BaseDependencyTag
}

var systemImageDependencyTag = systemImageDeptag{}

type dexpreoptDeptag struct {
	blueprint.BaseDependencyTag
}

var dexpreoptDependencyTag = dexpreoptDeptag{}

func (m *systemOtherImage) DepsMutator(ctx android.BottomUpMutatorContext) {
	if proptools.String(m.properties.System_image) == "" {
		ctx.ModuleErrorf("system_image property must be set")
		return
	}
	ctx.AddDependency(ctx.Module(), systemImageDependencyTag, *m.properties.System_image)
	ctx.AddDependency(ctx.Module(), dexpreoptDependencyTag, m.properties.Preinstall_dexpreopt_files_from...)
}

func (m *systemOtherImage) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	systemImage := ctx.GetDirectDepProxyWithTag(*m.properties.System_image, systemImageDependencyTag)
	systemInfo, ok := android.OtherModuleProvider(ctx, systemImage, FilesystemProvider)
	if !ok {
		ctx.PropertyErrorf("system_image", "Expected system_image module to provide FilesystemProvider")
		return
	}

	output := android.PathForModuleOut(ctx, "system_other.img")
	stagingDir := android.PathForModuleOut(ctx, "staging_dir")
	stagingDirTimestamp := android.PathForModuleOut(ctx, "staging_dir.timestamp")

	builder := android.NewRuleBuilder(pctx, ctx)
	builder.Command().Textf("rm -rf %s && mkdir -p %s", stagingDir, stagingDir)

	specs := make(map[string]android.PackagingSpec)
	for _, otherPartition := range m.properties.Preinstall_dexpreopt_files_from {
		dexModule := ctx.GetDirectDepProxyWithTag(otherPartition, dexpreoptDependencyTag)
		fsInfo, ok := android.OtherModuleProvider(ctx, dexModule, FilesystemProvider)
		if !ok {
			ctx.PropertyErrorf("preinstall_dexpreopt_files_from", "Expected module %q to provide FilesystemProvider", otherPartition)
			return
		}
		// Merge all the packaging specs into 1 map
		for k := range fsInfo.SpecsForSystemOther {
			if _, ok := specs[k]; ok {
				ctx.ModuleErrorf("Packaging spec %s given by two different partitions", k)
				continue
			}
			specs[k] = fsInfo.SpecsForSystemOther[k]
		}
	}

	// TOOD: CopySpecsToDir only exists on PackagingBase, but doesn't use any fields from it. Clean this up.
	(&android.PackagingBase{}).CopySpecsToDir(ctx, builder, specs, stagingDir)

	if len(m.properties.Preinstall_dexpreopt_files_from) > 0 {
		builder.Command().Textf("touch %s", filepath.Join(stagingDir.String(), "system-other-odex-marker"))
	}
	builder.Command().Textf("touch").Output(stagingDirTimestamp)
	builder.Build("assemble_filesystem_staging_dir", "Assemble filesystem staging dir")

	// Most of the time, if build_image were to call a host tool, it accepts the path to the
	// host tool in a field in the prop file. However, it doesn't have that option for fec, which
	// it expects to just be on the PATH. Add fec to the PATH.
	fec := ctx.Config().HostToolPath(ctx, "fec")
	pathToolDirs := []string{filepath.Dir(fec.String())}

	builder = android.NewRuleBuilder(pctx, ctx)
	builder.Command().
		Textf("PATH=%s:$PATH", strings.Join(pathToolDirs, ":")).
		BuiltTool("build_image").
		Text(stagingDir.String()). // input directory
		Input(systemInfo.BuildImagePropFile).
		Implicits(systemInfo.BuildImagePropFileDeps).
		Implicit(fec).
		Implicit(stagingDirTimestamp).
		Output(output).
		Text(stagingDir.String())

	builder.Build("build_system_other", "build system other")

	// Create a hermetic system_other.img with pinned timestamps
	builder = android.NewRuleBuilder(pctx, ctx)
	outputHermetic := android.PathForModuleOut(ctx, "for_target_files", "system_other.img")
	outputHermeticPropFile := m.propFileForHermeticImg(ctx, builder, systemInfo.BuildImagePropFile)
	builder.Command().
		Textf("PATH=%s:$PATH", strings.Join(pathToolDirs, ":")).
		BuiltTool("build_image").
		Text(stagingDir.String()). // input directory
		Input(outputHermeticPropFile).
		Implicits(systemInfo.BuildImagePropFileDeps).
		Implicit(fec).
		Implicit(stagingDirTimestamp).
		Output(outputHermetic).
		Text(stagingDir.String())

	builder.Build("build_system_other_hermetic", "build system other")

	fsInfo := FilesystemInfo{
		Output:         output,
		OutputHermetic: outputHermetic,
		RootDir:        stagingDir,
	}

	android.SetProvider(ctx, FilesystemProvider, fsInfo)

	ctx.SetOutputFiles(android.Paths{output}, "")
	ctx.CheckbuildFile(output)
}

func (f *systemOtherImage) propFileForHermeticImg(ctx android.ModuleContext, builder *android.RuleBuilder, inputPropFile android.Path) android.Path {
	propFilePinnedTimestamp := android.PathForModuleOut(ctx, "for_target_files", "prop")
	builder.Command().Textf("cat").Input(inputPropFile).Flag(">").Output(propFilePinnedTimestamp).
		Textf(" && echo use_fixed_timestamp=true >> %s", propFilePinnedTimestamp)
	return propFilePinnedTimestamp
}
