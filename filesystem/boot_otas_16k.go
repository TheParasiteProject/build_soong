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
	"github.com/google/blueprint/proptools"
	"strings"

	"android/soong/android"
)

type bootOtas16k struct {
	android.ModuleBase
	properties BootOtas16kProperties
}

type BootOtas16kProperties struct {
	Dtbo_image          *string `android:"path_device_first"`
	Dtbo_image_16k      *string `android:"path_device_first"`
	Boot_image          *string `android:"path_device_first"`
	Boot_image_16k      *string `android:"path_device_first"`
	Use_ota_incremental *bool
}

func BootOtas16kFactory() android.Module {
	module := &bootOtas16k{}
	android.InitAndroidArchModule(module, android.DeviceSupported, android.MultilibCommon)
	module.AddProperties(&module.properties)
	return module
}

func (b *bootOtas16k) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	bootOta4kZip := b.createOtaPackage(
		ctx,
		android.PathForModuleSrc(ctx, proptools.String(b.properties.Boot_image)),
		android.PathForModuleSrc(ctx, proptools.String(b.properties.Boot_image_16k)),
		android.PathForModuleSrc(ctx, proptools.String(b.properties.Dtbo_image)),
		"boot_ota_4k.zip",
	)
	bootOta16kZip := b.createOtaPackage(
		ctx,
		android.PathForModuleSrc(ctx, proptools.String(b.properties.Boot_image_16k)),
		android.PathForModuleSrc(ctx, proptools.String(b.properties.Boot_image)),
		android.PathForModuleSrc(ctx, proptools.String(b.properties.Dtbo_image_16k)),
		"boot_ota_16k.zip",
	)

	installDir := android.PathForModuleInstall(ctx, "boot_otas")
	ctx.PackageFile(installDir, bootOta4kZip.Base(), bootOta4kZip)
	ctx.PackageFile(installDir, bootOta16kZip.Base(), bootOta16kZip)
}

func (b *bootOtas16k) createOtaPackage(ctx android.ModuleContext, primaryBootImage, secondaryBootImage, dtboImage android.Path, filename string) android.Path {
	builder := android.NewRuleBuilder(pctx, ctx)
	zip := android.PathForModuleOut(ctx, filename)

	_, key := ctx.Config().DefaultSystemDevCertificate(ctx)
	cmd := builder.Command().
		BuiltTool("ota_from_raw_img").
		FlagWithArg("--package_key ", strings.TrimSuffix(key.String(), key.Ext())).
		Implicit(key).
		Textf("--max_timestamp $(cat %s)", ctx.Config().Getenv("BUILD_DATETIME_FILE"))

	if dtboImage != nil {
		cmd.FlagWithArg("--partition_name ", "boot,dtbo")
	} else {
		cmd.FlagWithArg("--partition_name ", "boot")
	}

	cmd.FlagWithOutput("--output ", zip).
		FlagWithInput("--delta_generator_path ", ctx.Config().HostToolPath(ctx, "delta_generator"))

	if proptools.Bool(b.properties.Use_ota_incremental) {
		cmd.Textf("%s:%s", secondaryBootImage, primaryBootImage).
			Implicit(secondaryBootImage).
			Implicit(primaryBootImage)
	} else {
		cmd.Input(primaryBootImage)
	}

	if dtboImage != nil {
		cmd.Input(dtboImage)
	}

	builder.Build(filename, filename)
	return zip
}
