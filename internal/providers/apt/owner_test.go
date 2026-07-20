package apt

import (
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestAPTRequirementProfileBindsCanonicalBaseEvidence(t *testing.T) {
	request, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{{Component: "system", Packages: []blueprint.APTPackageRequest{{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := NewBaseProfileEvidenceV1(aptBasePlatform(t, "linux/amd64"), map[string]string{"ID": "debian", "VERSION_ID": "13"}, aptBaseTools(), "amd64", []string{})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := CanonicalProfileFactsV1(base, aptTestExecutableBindings(base))
	if err != nil {
		t.Fatal(err)
	}
	profile := providers.RequirementProfile{
		Schema:              providers.RequirementProfileSchemaV1,
		Provider:            blueprint.ComponentTypeAPT,
		Declaration:         providers.RequirementDeclaration{Executables: []providers.ExecutableRequirement{}, Files: []providers.FileRequirement{}, ProviderData: providers.CanonicalProviderData{Schema: request.Schema, Value: request.Value}},
		SelectedExecutables: []providers.ExecutableEvidence{}, SelectedFiles: []providers.FileEvidence{},
		Platform: base.Platform, Facts: facts,
	}
	if _, err := providers.RequirementProfileDigest(profile, ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	profile.Platform = aptBasePlatform(t, "linux/arm64")
	if _, err := providers.RequirementProfileDigest(profile, ValidateRequirementProfileV1); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("err = %v", err)
	}
}

func aptTestExecutableBindings(base BaseProfileEvidenceV1) []providers.ValidatedExecutableInput {
	digest := canonical.Digest("sha256:" + strings.Repeat("f", 64))
	result := make([]providers.ValidatedExecutableInput, 0, len(base.Tools))
	for _, tool := range base.Tools {
		role := providers.ExecutableRoleProviderPrerequisite
		component := "apt"
		if tool.Name == "sh" {
			role, component = providers.ExecutableRoleCarrier, "backend"
		} else if tool.Name == "env" {
			role, component = providers.ExecutableRoleEnvironmentLauncher, "backend"
		}
		result = append(result, providers.ValidatedExecutableInput{
			ID: tool.Name, Role: role, Policy: providers.ValidationPolicyCompatible,
			Evidence: providers.ExecutableEvidence{
				Schema: providers.ExecutableEvidenceSchemaV1, RequirementID: tool.Name,
				Output: providers.QualifiedOutput{Component: component, Name: tool.Name}, InvocationPath: tool.Path,
				LinkChain: []providers.LinkEvidence{},
				Terminal:  providers.FileEvidence{Schema: providers.FileEvidenceSchemaV1, RequirementID: tool.Name, Path: tool.Path, Kind: "regular", Mode: "0755", Size: "1", SHA256: digest},
				Access: providers.PortableAccessEvidence{Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1, Paths: []providers.AccessPathEvidence{
					{Path: "/", Kind: "directory", Mode: "0755", Required: "other-search"},
					{Path: tool.Path, Kind: "regular", Mode: "0755", Required: "other-read-execute"},
				}},
				Facts: providers.CanonicalProviderData{Schema: "apt-required-tool-v1", Value: canonical.Object{"interface": tool.Interface, "version": tool.Version}},
			},
		})
	}
	return result
}

func testAPTArtifact(logicalPath string, digest canonical.Digest) providerstore.ArtifactDescriptor {
	return providerstore.ArtifactDescriptor{LogicalPath: logicalPath, Kind: "deb", Size: "1", SHA256: digest}
}

func TestAPTResolvedBundleOwnerRequiresExactPayloadArtifacts(t *testing.T) {
	request, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{{Component: "system", Packages: []blueprint.APTPackageRequest{{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	bundle := BundleV1{NativeArchitecture: "amd64", BasePackages: []BasePackage{}, BundlePackages: []BundlePackage{{
		Tuple:    PackageTuple{Name: "hello", Version: "1", Architecture: "amd64", Status: InstalledPackageStatusV1},
		Artifact: testAPTArtifact("debs/hello.deb", digest), FileListDigest: digest,
	}}}
	bundle.Script = materializationScriptDescriptorV1()
	manifest, err := materializationStateManifestBytesV1(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.StateManifest = materializationStateManifestDescriptorV1(manifest)
	data, err := CanonicalBundleDataV1(bundle)
	if err != nil {
		t.Fatal(err)
	}
	payload := providers.ResolvedBundleIdentityV1{
		Schema: providers.ResolvedBundleSchemaV1, NodeID: "apt", Provider: blueprint.ComponentTypeAPT,
		Request: request, RequirementProfileDigest: digest, RecipeVersion: RecipeVersion,
		Platform:        aptBasePlatform(t, "linux/amd64"),
		Upstream:        providers.RealizedImageV1{Digest: digest, ConfigDigest: digest, RootFSSubject: digest},
		SelectedSources: []providers.ResolvedSourceInput{},
		Artifacts:       []providerstore.ArtifactDescriptor{bundle.BundlePackages[0].Artifact, bundle.Script, bundle.StateManifest}, Outputs: []providers.ResolvedOutput{}, ProviderPayload: data,
	}
	sort.Slice(payload.Artifacts, func(left int, right int) bool {
		return payload.Artifacts[left].LogicalPath < payload.Artifacts[right].LogicalPath
	})
	if _, err := providers.NewResolvedBundle(payload, ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal(err)
	}
	payload.Artifacts = []providerstore.ArtifactDescriptor{}
	if _, err := providers.NewResolvedBundle(payload, ValidateResolvedBundlePayloadV1); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolvedOutputsBindWellKnownAndExplicitCandidatesToLockedPackages(t *testing.T) {
	request, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{{
		Component: "system",
		Packages: []blueprint.APTPackageRequest{
			{Name: "python3", Exports: map[string]blueprint.ExecutableExport{}},
			{Name: "demo", Exports: map[string]blueprint.ExecutableExport{"demo-tool": {Executable: "/usr/bin/demo-tool"}}},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	bundle := BundleV1{
		NativeArchitecture: "amd64",
		BasePackages: []BasePackage{
			{Tuple: PackageTuple{Name: "demo", Version: "1", Architecture: "amd64", Status: InstalledPackageStatusV1}},
			{Tuple: PackageTuple{Name: "python3", Version: "3.13", Architecture: "amd64", Status: InstalledPackageStatusV1}},
		},
		BundlePackages: []BundlePackage{}, Script: materializationScriptDescriptorV1(),
	}
	manifest, err := materializationStateManifestBytesV1(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.StateManifest = materializationStateManifestDescriptorV1(manifest)
	outputs, err := ResolvedOutputsV1(request, "apt", bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 2 || outputs[0].Name != "demo-tool" || outputs[0].Candidate.InvocationPath != "/usr/bin/demo-tool" || outputs[1].Name != "python" || outputs[1].Candidate.InvocationPath != "/usr/bin/python3" {
		t.Fatalf("outputs = %#v", outputs)
	}

	bundle.BasePackages = bundle.BasePackages[1:]
	manifest, err = materializationStateManifestBytesV1(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.StateManifest = materializationStateManifestDescriptorV1(manifest)
	if _, err := ResolvedOutputsV1(request, "apt", bundle); err == nil || !strings.Contains(err.Error(), "outside the locked closure") {
		t.Fatalf("missing owner package error = %v", err)
	}
}
