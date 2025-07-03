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
	"android/soong/android"

	"github.com/google/blueprint/proptools"
)

func init() {
	RegisterPrebuiltFilesystemComponents(android.InitRegistrationContext)
}

func RegisterPrebuiltFilesystemComponents(ctx android.RegistrationContext) {
	ctx.RegisterModuleType("android_system_image_prebuilt", PrebuiltSystemImageFactory)
}

type prebuiltSystemImageProperties struct {
	// A prebuilt system image file
	Src *string `android:"path"`

	// The property file used by the build_image tool to build the prebuilt system image.
	Prop *string `android:"path"`
}

type prebuiltSystemImage struct {
	systemImage
	prebuilt android.Prebuilt

	prebuiltProperties prebuiltSystemImageProperties
}

func (p *prebuiltSystemImage) Name() string {
	return p.prebuilt.Name(p.systemImage.Name())
}

var _ android.PrebuiltInterface = (*prebuiltSystemImage)(nil)

func (p *prebuiltSystemImage) Prebuilt() *android.Prebuilt {
	return &p.prebuilt
}

func (p *prebuiltSystemImage) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	input := android.PathForModuleSrc(ctx, proptools.String(p.prebuiltProperties.Src))
	rootDir := android.PathForModuleOut(ctx, p.rootDirString()).OutputPath
	output := android.PathForModuleOut(ctx, p.installFileName())
	buildImagePropFile := android.PathForModuleSrc(ctx, proptools.String(p.prebuiltProperties.Prop))
	p.output = output

	fsInfo := FilesystemInfo{
		ModuleName:         ctx.ModuleName(),
		PartitionName:      "system",
		RootDir:            rootDir,
		Output:             p.OutputPath(),
		SignedOutputPath:   p.SignedOutputPath(),
		BuildImagePropFile: buildImagePropFile,
	}
	p.updateAvbInFsInfo(ctx, &fsInfo)
	android.SetProvider(ctx, FilesystemProvider, fsInfo)
	p.setVbmetaPartitionProvider(ctx)

	ctx.Build(pctx, android.BuildParams{
		Rule:        android.Cp,
		Description: "install prebuilt system image",
		Output:      output,
		Input:       input,
	})
}

func PrebuiltSystemImageFactory() android.Module {
	module := &prebuiltSystemImage{}
	module.filesystemBuilder = module
	module.AddProperties(&module.prebuiltProperties)
	android.InitSingleSourcePrebuiltModule(module, &module.prebuiltProperties, "Src")
	initBaseFilesystemModule(module, &module.systemImage.filesystem)
	return module
}
