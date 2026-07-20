package apt

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestMaterializeBuildsOneClosedOfflineAPTTransaction(t *testing.T) {
	input := aptMaterializeInput(t)
	transaction, err := (ComponentProvider{}).Materialize(input)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.RecipeVersion != MaterializationRecipeVersion || transaction.Network != providerapi.NetworkPolicyNone || transaction.BuildIdentity.UID != "0" {
		t.Fatalf("transaction policy = %#v", transaction)
	}
	if !reflect.DeepEqual(transaction.ChildEnvironment, MaterializationChildEnvironmentV1()) {
		t.Fatalf("child environment = %#v", transaction.ChildEnvironment)
	}
	if len(transaction.Mounts) != 2 || transaction.Mounts[0].ID != aptArtifactMountID || transaction.Mounts[0].SourceDigest != input.Bundle.Identity || transaction.Mounts[1].ID != aptScriptMountID {
		t.Fatalf("mounts = %#v", transaction.Mounts)
	}
	if len(transaction.GeneratedExecutables) != 0 {
		t.Fatalf("APT generated executables are enabled before output realization: %#v", transaction.GeneratedExecutables)
	}
	bundle, err := DecodeCanonicalBundleDataV1(input.Bundle.Payload.ProviderPayload)
	if err != nil {
		t.Fatal(err)
	}
	wantArguments := 3 + len(materializationToolArgumentOrderV1) + 1 + len(bundle.BundlePackages)
	if len(transaction.Argv) != wantArguments || transaction.Argv[2].RelativePath != bundle.Script.LogicalPath || transaction.Argv[3+len(materializationToolArgumentOrderV1)].RelativePath != bundle.StateManifest.LogicalPath {
		t.Fatalf("argv = %#v", transaction.Argv)
	}
	for index, pkg := range bundle.BundlePackages {
		argument := transaction.Argv[4+len(materializationToolArgumentOrderV1)+index]
		if argument.Kind != providerapi.TypedArgumentMountedArtifact || argument.RelativePath != pkg.Artifact.LogicalPath {
			t.Fatalf("archive argument %d = %#v", index, argument)
		}
	}
	for index := 1; index < len(transaction.Prerequisites); index++ {
		if transaction.Prerequisites[index-1].ID >= transaction.Prerequisites[index].ID {
			t.Fatalf("prerequisites are not canonical: %#v", transaction.Prerequisites)
		}
	}
}

func TestMaterializeRejectsCurrentCarrierDrift(t *testing.T) {
	input := aptMaterializeInput(t)
	input.Carrier.Evidence.Terminal.SHA256 = canonical.Digest("sha256:" + strings.Repeat("e", 64))
	if _, err := (ComponentProvider{}).Materialize(input); err == nil || !strings.Contains(err.Error(), "locked prefix evidence") {
		t.Fatalf("error = %v", err)
	}
}

func TestMaterializeBaseOnlyClosureHasNoDebOperands(t *testing.T) {
	input := aptMaterializeInput(t)
	priorIdentity := input.Bundle.Identity
	bundleData := BundleV1{
		NativeArchitecture: "amd64",
		BasePackages: []BasePackage{{Tuple: PackageTuple{
			Name: "libc6", Version: "2.39", Architecture: "amd64", Status: InstalledPackageStatusV1,
		}}},
		BundlePackages: []BundlePackage{},
		Script:         materializationScriptDescriptorV1(),
	}
	manifest, err := materializationStateManifestBytesV1(bundleData)
	if err != nil {
		t.Fatal(err)
	}
	bundleData.StateManifest = materializationStateManifestDescriptorV1(manifest)
	payloadData, err := CanonicalBundleDataV1(bundleData)
	if err != nil {
		t.Fatal(err)
	}
	payload := input.Bundle.Payload
	payload.ProviderPayload = payloadData
	payload.Artifacts = []providerstore.ArtifactDescriptor{bundleData.StateManifest, bundleData.Script}
	sort.Slice(payload.Artifacts, func(left int, right int) bool {
		return payload.Artifacts[left].LogicalPath < payload.Artifacts[right].LogicalPath
	})
	payload.RequirementProfileDigest, err = providerapi.RequirementProfileDigest(input.Profile, ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	input.Bundle, err = providerapi.NewResolvedBundle(payload, ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := (ComponentProvider{}).Materialize(input)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.Argv[len(transaction.Argv)-1].RelativePath != bundleData.StateManifest.LogicalPath {
		t.Fatalf("base-only transaction contains a deb operand: %#v", transaction.Argv)
	}
	if priorIdentity == input.Bundle.Identity {
		t.Fatal("base-only fixture unexpectedly reused the mixed-closure identity")
	}
}

func aptMaterializeInput(t *testing.T) providerapi.MaterializeInput {
	t.Helper()
	platform := aptBasePlatform(t, "linux/amd64")
	base, err := NewBaseProfileEvidenceV1(platform, map[string]string{"ID": "debian", "VERSION_ID": "13"}, aptBaseTools(), "amd64", []string{})
	if err != nil {
		t.Fatal(err)
	}
	executables := aptTestExecutableBindings(base)
	request, err := CanonicalProviderRequestV1(APTProviderRequestV1{Components: []APTComponentRequestV1{{
		Component: "system", Packages: []blueprint.APTPackageRequest{{Name: "hello", Exports: map[string]blueprint.ExecutableExport{}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := CanonicalProfileFactsV1(base, executables)
	if err != nil {
		t.Fatal(err)
	}
	declaration := providerapi.RequirementDeclaration{
		Executables: []providerapi.ExecutableRequirement{}, Files: []providerapi.FileRequirement{},
		ProviderData: providerapi.CanonicalProviderData{Schema: request.Schema, Value: request.Value},
	}
	profile := providerapi.RequirementProfile{
		Schema: providerapi.RequirementProfileSchemaV1, Provider: blueprint.ComponentTypeAPT, Declaration: declaration,
		SelectedExecutables: []providerapi.ExecutableEvidence{}, SelectedFiles: []providerapi.FileEvidence{},
		Platform: platform, Facts: facts,
	}
	profileDigest, err := providerapi.RequirementProfileDigest(profile, ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	bundleData, err := NewBundleV1("amd64", aptMixedResolvePlan(), []PackageTuple{
		{Name: "iproute2", Version: "6.1-1", Architecture: "amd64", Status: InstalledPackageStatusV1},
		{Name: "libc6", Version: "2.39", Architecture: "amd64", Status: InstalledPackageStatusV1},
		{Name: "perl-modules", Version: "5.38", Architecture: "all", Status: InstalledPackageStatusV1},
	}, aptMixedBundlePackages())
	if err != nil {
		t.Fatal(err)
	}
	payloadData, err := CanonicalBundleDataV1(bundleData)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := []providerstore.ArtifactDescriptor{bundleData.Script, bundleData.StateManifest}
	for _, pkg := range bundleData.BundlePackages {
		artifacts = append(artifacts, pkg.Artifact)
	}
	sort.Slice(artifacts, func(left int, right int) bool { return artifacts[left].LogicalPath < artifacts[right].LogicalPath })
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	upstream := providerapi.RealizedImageV1{Digest: digest, ConfigDigest: digest, RootFSSubject: digest}
	bundle, err := providerapi.NewResolvedBundle(providerapi.ResolvedBundleIdentityV1{
		Schema: providerapi.ResolvedBundleSchemaV1, NodeID: "apt", Provider: blueprint.ComponentTypeAPT,
		Request: request, RequirementProfileDigest: profileDigest, RecipeVersion: RecipeVersion,
		Platform: platform, Upstream: upstream, Artifacts: artifacts, Outputs: []providerapi.ResolvedOutput{}, ProviderPayload: payloadData,
	}, ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	var carrier, launcher providerapi.ValidatedExecutableInput
	for _, executable := range executables {
		if executable.Role == providerapi.ExecutableRoleCarrier {
			carrier = executable
		} else if executable.Role == providerapi.ExecutableRoleEnvironmentLauncher {
			launcher = executable
		}
	}
	return providerapi.MaterializeInput{
		Bundle: bundle, Profile: profile, AssemblyParent: upstream, Carrier: carrier, EnvironmentLauncher: launcher,
		FinalImageConfig: providerapi.ImageConfigPolicy{
			User: "1000:1000", WorkingDir: "/work", Environment: []providerapi.EnvironmentVariable{}, Entrypoint: []string{}, Command: []string{},
			Healthcheck: providerapi.ImageHealthcheckNone, StopSignal: "SIGTERM", Labels: []providerapi.ImageLabel{},
		},
	}
}
