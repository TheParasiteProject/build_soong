// Copyright 2024 Google Inc. All rights reserved.
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
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/blueprint"
)

var (
	// Command line tool to generate SBOM in Soong
	genSbom = pctx.HostBinToolVariable("genSbom", "gen_sbom")

	// Command to generate SBOM in Soong.
	genSbomRule = pctx.AndroidStaticRule("genSbomRule", blueprint.RuleParams{
		Command:     "rm -rf $out && ${genSbom} --output_file ${out} --metadata ${in} --product_out ${productOut} --soong_out ${soongOut} --build_version \"$$(cat ${buildFingerprintFile})\" --product_mfr \"${productManufacturer}\" --json ${unbundledModule}",
		CommandDeps: []string{"${genSbom}"},
	}, "productOut", "soongOut", "buildFingerprintFile", "productManufacturer", "unbundledModule")
)

func init() {
	RegisterSbomSingleton(InitRegistrationContext)
}

func RegisterSbomSingleton(ctx RegistrationContext) {
	ctx.RegisterParallelSingletonType("sbom_singleton", sbomSingletonFactory)
}

// sbomSingleton is used to generate build actions of generating SBOM of products.
type sbomSingleton struct {
}

func sbomSingletonFactory() Singleton {
	return &sbomSingleton{}
}

// Generates SBOM of products
func (this *sbomSingleton) GenerateBuildActions(ctx SingletonContext) {
	if !ctx.Config().HasDeviceProduct() {
		return
	}
	buildFingerprintFile := ctx.Config().BuildFingerprintFile(ctx)
	metadataDb := PathForOutput(ctx, "compliance-metadata", ctx.Config().DeviceProduct(), "compliance-metadata.db")
	productOut := filepath.Join(ctx.Config().OutDir(), "target", "product", String(ctx.Config().productVariables.DeviceName))

	if ctx.Config().HasUnbundledBuildApps() {
		unbundledApps := ctx.Config().UnbundledBuildApps()
		sbomFiles := []Path{}
		ctx.VisitAllModuleProxies(func(module ModuleProxy) {
			if !slices.Contains(unbundledApps, module.Name()) {
				return
			}
			if metadataInfo, ok := OtherModuleProvider(ctx, module, ComplianceMetadataProvider); ok && len(metadataInfo.filesContained) > 0 {
				implicits := []Path{}
				implicits = append(implicits, buildFingerprintFile)
				installedFile := metadataInfo.filesContained[0]
				implicits = append(implicits, PathForArbitraryOutput(ctx, strings.TrimPrefix(installedFile, ctx.Config().OutDir()+"/")))

				sbomFile := PathForOutput(ctx, "sbom", ctx.Config().DeviceProduct(), module.Name(), path.Base(installedFile)+".spdx.json")
				sbomFiles = append(sbomFiles, sbomFile)
				ctx.Build(pctx, BuildParams{
					Rule:      genSbomRule,
					Input:     metadataDb,
					Implicits: implicits,
					Output:    sbomFile,
					Args: map[string]string{
						"productOut":           productOut,
						"soongOut":             ctx.Config().soongOutDir,
						"buildFingerprintFile": buildFingerprintFile.String(),
						"productManufacturer":  ctx.Config().ProductVariables().ProductManufacturer,
						"unbundledModule":      "--unbundled_module " + module.Name(),
					},
				})
				ctx.DistForGoalsWithFilename([]string{"apps_only", "sbom"}, sbomFile, "sbom/"+sbomFile.Base())
			}
			return
		})
		ctx.Phony("sbom", sbomFiles...)
	} else {
		// When building SBOM of products, phony rule "sbom" is for generating product SBOM in Soong.
		implicits := []Path{}
		implicits = append(implicits, buildFingerprintFile)

		// Add installed_files.stamp as implicit input, which depends on all installed files of the product.
		installedFilesStamp := PathForOutput(ctx, "compliance-metadata", ctx.Config().DeviceProduct(), "installed_files.stamp")
		implicits = append(implicits, installedFilesStamp)

		sbomFile := PathForOutput(ctx, "sbom", ctx.Config().DeviceProduct(), "sbom.spdx.json")
		ctx.Build(pctx, BuildParams{
			Rule:      genSbomRule,
			Input:     metadataDb,
			Implicits: implicits,
			Output:    sbomFile,
			Args: map[string]string{
				"productOut":           productOut,
				"soongOut":             ctx.Config().soongOutDir,
				"buildFingerprintFile": buildFingerprintFile.String(),
				"productManufacturer":  ctx.Config().ProductVariables().ProductManufacturer,
			},
		})

		ctx.Build(pctx, BuildParams{
			Rule:   blueprint.Phony,
			Inputs: []Path{sbomFile},
			Output: PathForPhony(ctx, "sbom"),
		})
		ctx.DistForGoalWithFilename("droid", sbomFile, "sbom/sbom.spdx.json")
	}
}
