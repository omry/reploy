package dockerdeploy

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

func TestPortableToolPythonFreshPTD2337TwoNeutralFixture(t *testing.T) {
	fresh, records := portableToolPythonFreshTwoNeutralFixtureV1(t)
	if err := validatePortableToolPythonFreshPlanV1(&fresh); err != nil {
		t.Fatal(err)
	}
	if err := providers.ValidatePortableToolPlanV1(fresh.Plan); err != nil {
		t.Fatal(err)
	}
	if len(records.Artifacts) != 2 || len(fresh.Plan.Tools) != 1 || len(fresh.Plan.Tools[0].Responsibilities.BindingArtifacts) != 2 || len(fresh.Plan.Tools[0].Exports) != 2 {
		t.Fatalf("two-neutral fixture = %#v, records = %#v", fresh, records)
	}
	selected, projection, err := portableToolPythonSelectionPlanV1(fresh.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Tools) != 1 || len(projection.Components) != 1 || len(projection.Components[0].Bindings) != 2 {
		t.Fatalf("two-neutral selected plan = %#v, projection = %#v", selected, projection)
	}
}

// TestPreparedPythonGraphDockerIntegrationPTD2337NeutralBindings exercises
// the production resolver, closed-wheel materialization, and alias layer with
// two synthetic bindings selected by one application-scoped tool. Keeping the
// bindings in one selected entry also proves that the operation and lock joins
// retain the complete multi-binding closure rather than one arbitrary member.
func TestPreparedPythonGraphDockerIntegrationPTD2337NeutralBindings(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	base := os.Getenv("REPLOY_PTD2337_PYTHON_BASE")
	if base == "" {
		base = "python:3.12-slim"
	}
	imagePlatformOutput, err := runDockerOutput(ctx, "image", "inspect", "--platform", platform.Canonical, "--format", "{{.Os}}/{{.Architecture}}", base)
	if err != nil {
		t.Skipf("local %s image for %s is required for PTD-23.3.7 neutral acceptance: %v", base, platform.Canonical, err)
	}
	if imagePlatform := strings.TrimSpace(imagePlatformOutput); imagePlatform != platform.Canonical {
		t.Skipf("local %s image platform %q does not match selected platform %s", base, imagePlatform, platform.Canonical)
	}
	baseIDOutput, err := runDockerOutput(ctx, "image", "inspect", "--platform", platform.Canonical, "--format", "{{index .RepoDigests 0}}", base)
	if err != nil {
		t.Skipf("local %s image repository digest is required for PTD-23.3.7 neutral acceptance: %v", base, err)
	}
	base = strings.TrimSpace(baseIDOutput)
	if base == "" || base == "<no value>" {
		t.Skipf("local %s image has no repository digest for PTD-23.3.7 neutral acceptance", base)
	}
	packedProbe := packIntegrationProbe(t, platform)
	previousLocate := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packedProbe, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocate })

	storeRoot := t.TempDir()
	store, err := providerstore.NewStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	fresh, records := portableToolPythonFreshTwoNeutralFixtureV1(t)
	if len(fresh.Projection.Components) != 1 || len(fresh.Projection.Components[0].Bindings) != 2 {
		t.Fatalf("two-neutral projection = %#v", fresh.Projection)
	}
	component := fresh.Projection.Components[0]

	wheels := map[string]struct {
		module  string
		message string
	}{}
	for _, binding := range component.Bindings {
		wheels[binding.Distribution] = struct {
			module  string
			message string
		}{
			module:  strings.ReplaceAll(binding.Distribution, "-", "_"),
			message: "hello from " + binding.Distribution,
		}
	}
	descriptors := make(map[string]providerstore.ArtifactDescriptor, len(wheels))
	for _, binding := range component.Bindings {
		fixture := wheels[binding.Distribution]
		wheelContent := preparedPTD2337NeutralBindingWheelV1(t, fixture.module, binding.Distribution, binding.CLI.Name, fixture.message)
		filename := binding.Wheel.Filename
		wheel, err := store.Publish(ctx, "wheels/"+filename, "wheel", bytes.NewReader(wheelContent))
		if err != nil {
			t.Fatal(err)
		}
		inspection, err := pythonprovider.InspectWheelReaderV1(ctx, bytes.NewReader(wheelContent), int64(len(wheelContent)), filename, wheel)
		if err != nil {
			t.Fatal(err)
		}
		if len(inspection.ConsoleScripts) != 1 || inspection.ConsoleScripts[0].Name != binding.CLI.Name || inspection.Distribution != binding.Distribution {
			t.Fatalf("neutral wheel inspection for %s = %#v", binding.Distribution, inspection)
		}
		descriptors[binding.Distribution] = wheel
	}
	rebindPortableToolPythonFreshTwoNeutralRecordsV1(t, &fresh, &records, descriptors)
	component = fresh.Projection.Components[0]

	previousRecords := portableToolPythonFreshLockRecordsV1
	t.Cleanup(func() { portableToolPythonFreshLockRecordsV1 = previousRecords })
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}

	components, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		fresh.Plan, []providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 1 || len(projection.Components) != 1 || len(projection.Components[0].Bindings) != 2 {
		t.Fatalf("projected two-neutral components = %#v, projection = %#v", components, projection)
	}
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: base,
		Exports: map[string]blueprint.BaseExecutableExport{
			"python": {Executable: "/usr/local/bin/python3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	components = append(components, providers.ResolvedComponentRequestV1{
		Component: "base", Provider: blueprint.ComponentTypeBase, Request: baseRequest,
	})
	sort.Slice(components, func(left, right int) bool { return components[left].Component < components[right].Component })
	request := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: reuseTestDigest("a"),
		Platform: platform, Components: components, Sources: []providers.ResolvedSourceInput{},
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err != nil {
		t.Fatal(err)
	}
	preparedBase, err := PrepareProviderBase(ctx, store, request)
	if err != nil {
		t.Fatal(err)
	}
	finalImageConfig, err := ProviderFinalImageConfigV1(preparedBase.Config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecutePreparedPythonGraph(ctx, PreparedPythonGraphExecutionInput{
		Store: store, Plan: preparedBase.Plan, BaseDescriptor: preparedBase.Descriptor,
		BaseCatalog: preparedBase.Catalog, Sources: request.Sources,
		SourceWheels: []providerstore.ArtifactDescriptor{},
		PortablePython: &PortableToolPythonFreshPlanV1{
			Plan: fresh.Plan, Projection: projection, Closures: fresh.Closures,
		},
		DesiredPortableToolPlan: &fresh.Plan,
		FinalImageConfig:        finalImageConfig,
		RunOptions:              RunOptions{Stdout: os.Stdout, Stderr: os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Materializations) != 1 || len(result.Bundles) != 1 {
		t.Fatalf("two-neutral graph result = %#v", result)
	}
	image := result.Materializations[0].Image.Digest
	t.Cleanup(func() { _, _ = runDockerOutput(context.Background(), "image", "rm", "--force", string(image)) })
	bundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component.Component, result.Bundles[0].Payload.ProviderPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Wheels) != 2 || len(bundle.Outputs) != 2 {
		t.Fatalf("two-neutral bundle closure = %#v", bundle)
	}
	wheelCount := make(map[string]int, len(bundle.Wheels))
	for _, wheel := range bundle.Wheels {
		wheelCount[wheel.Distribution]++
		want, found := descriptors[wheel.Distribution]
		if !found || wheel.Artifact != want {
			t.Fatalf("two-neutral bundle wheel = %#v, want exact descriptor %v", wheel, want)
		}
	}
	for _, binding := range component.Bindings {
		if wheelCount[binding.Distribution] != 1 {
			t.Fatalf("selected %s wheel count = %d, want exactly once", binding.Distribution, wheelCount[binding.Distribution])
		}
	}
	outputs := make(map[string]string, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		if _, exists := outputs[output.Name]; exists {
			t.Fatalf("duplicate generated output %q", output.Name)
		}
		outputs[output.Name] = output.Path
	}
	for _, binding := range component.Bindings {
		if outputs[binding.CLI.Name] == "" {
			t.Fatalf("generated output for %q missing from %#v", binding.CLI.Name, bundle.Outputs)
		}
	}
	catalogCounts := make(map[string]int, len(bundle.Outputs))
	for _, output := range result.Catalog {
		if output.SupplierComponent != component.Component {
			continue
		}
		catalogCounts[output.Name]++
		want, found := outputs[output.Name]
		if !found || output.Candidate.InvocationPath != want {
			t.Fatalf("catalog output %q = %#v, want generated path %q", output.Name, output, want)
		}
	}
	declaredExports := make(map[string]providers.PortableToolExportV1, len(fresh.Plan.Tools[0].Exports))
	for _, declared := range fresh.Plan.Tools[0].Exports {
		declaredExports[declared.Name] = declared
	}
	for _, binding := range component.Bindings {
		declared, found := declaredExports[binding.CLI.Name]
		if !found || declared.Path != binding.CLI.Path {
			t.Fatalf("selected export for %q = %#v, want declared CLI %#v", binding.CLI.Name, declared, binding.CLI)
		}
		if catalogCounts[binding.CLI.Name] != 1 {
			t.Fatalf("catalog output %q count = %d, want exactly once", binding.CLI.Name, catalogCounts[binding.CLI.Name])
		}
		generatedPath := outputs[binding.CLI.Name]
		if !strings.HasPrefix(generatedPath, pythonprovider.InstallRoot+"/") || strings.Contains(generatedPath, storeRoot) {
			t.Fatalf("generated target for %q = %q contains an invalid or host staging prefix", binding.CLI.Name, generatedPath)
		}
		resolvedTarget := runDockerIntegration(t, ctx, "run", "--rm", "--platform", platform.Canonical, "--entrypoint", "/bin/sh", string(image), "-c", "readlink \"$1\"", "reploy-alias-probe", declared.Path)
		if strings.TrimSpace(resolvedTarget) != generatedPath {
			t.Fatalf("declared export %q resolves to %q, want generated script %q", declared.Path, resolvedTarget, generatedPath)
		}
		output := runDockerIntegration(t, ctx, "run", "--rm", "--platform", platform.Canonical, "--entrypoint", declared.Path, string(image))
		if strings.TrimSpace(output) != wheels[binding.Distribution].message {
			t.Fatalf("portable binding %q alias output = %q", binding.CLI.Name, output)
		}
	}
}

// rebindPortableToolPythonFreshTwoNeutralRecordsV1 turns the intentionally
// skeletal neutral fixture into a complete exact acquisition graph. The
// selected plan, release manifest, artifact source records, and selected
// closure all name the bytes already published in the provider store. This
// lets the production verified-wheel producer take its cache-hit path while
// every declared mirror remains unreachable.
func rebindPortableToolPythonFreshTwoNeutralRecordsV1(
	t *testing.T,
	fresh *PortableToolPythonFreshPlanV1,
	records *toolcatalog.EmbeddedPortableToolLockRecordSetV1,
	descriptors map[string]providerstore.ArtifactDescriptor,
) {
	t.Helper()
	if fresh == nil || records == nil || len(fresh.Plan.Tools) != 1 || len(fresh.Projection.Components) != 1 || len(fresh.Closures) != 1 {
		t.Fatalf("two-neutral rebind inputs = fresh=%#v records=%#v", fresh, records)
	}
	entry := &fresh.Plan.Tools[0]
	component := fresh.Projection.Components[0]
	const namespace = "tool:fixture-tool/releases/1.0.0"

	artifactReferences := make(map[string]providers.PortableToolRecordReferenceV1, len(component.Bindings))
	artifactValues := make(map[string]portabletool.BindingArtifactRecordV1, len(component.Bindings))
	manifestMappings := make([]portabletool.ArtifactSourceMappingV1, 0, len(component.Bindings))
	for _, binding := range component.Bindings {
		descriptor, found := descriptors[binding.Distribution]
		if !found {
			t.Fatalf("no published descriptor for selected %s binding", binding.Distribution)
		}
		artifactIndex := -1
		for index := range entry.Responsibilities.BindingArtifacts {
			if entry.Responsibilities.BindingArtifacts[index].Reference == binding.Artifact {
				artifactIndex = index
				break
			}
		}
		if artifactIndex < 0 {
			t.Fatalf("selected %s artifact record is missing", binding.Distribution)
		}
		selected := &entry.Responsibilities.BindingArtifacts[artifactIndex]
		oldReference := selected.Reference
		selected.Record.Value["size"] = descriptor.Size
		selected.Record.Value["sha256"] = string(descriptor.SHA256)
		selected.Reference = portableToolPythonFreshReferenceV1(
			t, selected.Record.Value["id"].(string), selected.Record,
		)
		artifactReferences[binding.Distribution] = selected.Reference
		var artifactValue portabletool.BindingArtifactRecordV1
		decodePortableToolPythonFreshRecordV1(t, selected.Record, &artifactValue)
		artifactValues[binding.Distribution] = artifactValue

		sourceIndex := -1
		for index := range records.Artifacts {
			if records.Artifacts[index].Artifact == oldReference {
				sourceIndex = index
				break
			}
		}
		if sourceIndex < 0 {
			t.Fatalf("no source record for selected %s artifact %v", binding.Distribution, oldReference)
		}
		sourceID := namespace + "/revisions/1/sources/" + binding.Distribution + "-linux-amd64"
		mirror := "https://example.invalid/wheels/" + descriptor.LogicalPath[strings.LastIndex(descriptor.LogicalPath, "/")+1:]
		sourceRecord := portableToolPythonFreshRecordV1(t, portabletool.ArtifactSourceRecordV1{
			Schema: portabletool.ArtifactSourceRecordSchemaV1, ID: sourceID, SHA256: descriptor.SHA256,
			Mirrors: []string{mirror}, Provenance: []string{mirror}, Diagnostics: []string{},
		})
		sourceReference := portableToolPythonFreshReferenceV1(t, sourceID, sourceRecord)
		records.Artifacts[sourceIndex].Artifact = selected.Reference
		records.Artifacts[sourceIndex].Descriptor = descriptor
		records.Artifacts[sourceIndex].Source = providers.PortableToolSelectedRecordV1{
			Reference: sourceReference, Record: sourceRecord,
		}
		records.Artifacts[sourceIndex].Mirrors = []string{mirror}
		manifestMappings = append(manifestMappings, portabletool.ArtifactSourceMappingV1{
			ArtifactSHA256: descriptor.SHA256,
			Artifact:       portabletool.RecordReferenceV1(selected.Reference),
			Source:         portabletool.RecordReferenceV1(sourceReference),
		})
	}
	sort.Slice(manifestMappings, func(left, right int) bool {
		return manifestMappings[left].ArtifactSHA256 < manifestMappings[right].ArtifactSHA256
	})
	profileValue := portabletool.ValidationProfileRecordV1{
		Schema: portabletool.ValidationProfileSchemaV1,
		ID:     namespace + "/validation/profiles/default",
		Tool:   "fixture-tool", Version: "1.0.0",
		Probes: []portabletool.RecordProbeV1{{Path: "/opt/neutral/bin/neutral-cli", Args: []string{"--version"}}},
	}
	profileRecord := portableToolPythonFreshRecordV1(t, profileValue)
	profileReference := portableToolPythonFreshReferenceV1(t, profileValue.ID, profileRecord)
	entry.ValidationProfiles = []providers.PortableToolValidationProfileV1{{
		Reference: profileReference, Record: profileRecord,
	}}
	manifestValue := portabletool.ReleaseManifestV1{
		Schema: portabletool.ReleaseManifestSchemaV1, ID: namespace + "/revisions/1/manifest",
		Tool: "fixture-tool", Version: "1.0.0", Aliases: []string{}, Revision: "1",
		Contract: portabletool.RecordReferenceV1{
			ID: namespace + "/contract", Digest: portableToolPythonFreshDigestV1(t, "neutral-release-contract"),
		},
		Targets: []portabletool.RecordReferenceV1{{
			ID: namespace + "/targets/debian/12/amd64", Digest: portableToolPythonFreshDigestV1(t, "neutral-target"),
		}},
		ArtifactSources:    manifestMappings,
		Provenance:         []string{"https://example.invalid/fixture-tool/1.0.0"},
		ValidationProfiles: []portabletool.RecordReferenceV1{portabletool.RecordReferenceV1(profileReference)},
	}
	manifestRecord := portableToolPythonFreshRecordV1(t, manifestValue)
	manifestReference := portableToolPythonFreshReferenceV1(t, manifestValue.ID, manifestRecord)
	if len(records.Releases) != 1 {
		t.Fatalf("two-neutral release records = %d, want one", len(records.Releases))
	}
	records.Releases[0] = providers.PortableToolReleaseManifestInputV1{
		Scope: entry.Scope, Tool: entry.Provenance.Tool,
		Manifest: providers.PortableToolSelectedRecordV1{Reference: manifestReference, Record: manifestRecord},
	}
	entry.Provenance.ManifestDigest = manifestReference.Digest

	contracts := make([]toolcatalog.SelectedBindingContractRecordV1, 0, len(entry.Responsibilities.BindingContracts))
	contractNames := make([]string, 0, len(entry.Responsibilities.BindingContracts))
	contractByName := make(map[string]portabletool.BindingContractV1, len(entry.Responsibilities.BindingContracts))
	for _, selected := range entry.Responsibilities.BindingContracts {
		var value portabletool.BindingContractV1
		decodePortableToolPythonFreshRecordV1(t, selected.Record, &value)
		contracts = append(contracts, toolcatalog.SelectedBindingContractRecordV1{
			Reference: portabletool.RecordReferenceV1(selected.Reference), Record: value,
		})
		contractNames = append(contractNames, value.Name)
		contractByName[value.Name] = value
	}
	sort.Strings(contractNames)
	sort.Slice(contracts, func(left, right int) bool { return contracts[left].Record.Name < contracts[right].Record.Name })
	artifactRecords := make([]toolcatalog.SelectedBindingArtifactRecordV1, 0, len(artifactValues))
	for _, distribution := range sortedNeutralDistributionsV1(artifactValues) {
		artifact := artifactValues[distribution]
		artifactRecords = append(artifactRecords, toolcatalog.SelectedBindingArtifactRecordV1{
			Reference: portabletool.RecordReferenceV1(artifactReferences[distribution]), Record: artifact,
		})
	}
	exports := make([]toolcatalog.ToolExportV1, 0, len(entry.Exports))
	for _, exported := range entry.Exports {
		exports = append(exports, toolcatalog.ToolExportV1{Name: exported.Name, Path: exported.Path})
	}
	targetBindings := make([]toolcatalog.SelectedTargetBindingV1, 0, len(component.Bindings))
	for _, binding := range component.Bindings {
		contract := contractByName[binding.Distribution]
		if contract.Name == "" {
			for _, candidate := range contractByName {
				if candidate.Package == binding.Distribution {
					contract = candidate
					break
				}
			}
		}
		targetBindings = append(targetBindings, toolcatalog.SelectedTargetBindingV1{
			Name: contract.Name, Contract: portabletool.RecordReferenceV1(binding.Contract),
			Artifacts: []portabletool.RecordReferenceV1{
				portabletool.RecordReferenceV1(artifactReferences[binding.Distribution]),
			},
			PackageSets: []portabletool.RecordReferenceV1{},
			Exports:     []toolcatalog.ToolExportV1{{Name: binding.CLI.Name, Path: binding.CLI.Path}},
		})
	}
	sort.Slice(targetBindings, func(left, right int) bool { return targetBindings[left].Name < targetBindings[right].Name })
	closure := &fresh.Closures[0]
	closure.Provenance = toolcatalog.ReleaseProvenanceV1{
		Tool: entry.Provenance.Tool, Version: entry.Provenance.Version,
		Revision: entry.Provenance.Revision, ManifestDigest: manifestReference.Digest,
	}
	closure.Contract = toolcatalog.SelectedContractProjectionV1{
		Context: "runtime", Bindings: contractNames, Selections: map[string][]string{},
		Runtime: nil, Exports: exports,
	}
	closure.Target = toolcatalog.SelectedTargetProjectionV1{
		Identity: toolcatalog.TargetIdentityV1{
			Platform: "linux/amd64", OSReleaseID: "debian", VersionID: "12",
			OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt",
		},
		PackageSets: []portabletool.RecordReferenceV1{}, Bindings: targetBindings,
		Payloads: []portabletool.RecordReferenceV1{}, Selections: []toolcatalog.SelectedTargetSelectionV1{}, Exports: exports,
	}
	closure.Records = toolcatalog.SelectedClosureRecordsV1{
		BindingContracts: contracts, BindingArtifacts: artifactRecords,
		Payloads: []toolcatalog.SelectedPayloadRecordV1{}, PackageSets: []toolcatalog.SelectedPackageSetRecordV1{},
	}
	closure.Profiles = []toolcatalog.ValidationProfileRecordV1{profileValue}
	references := make([]portabletool.RecordReferenceV1, 0, len(contracts)+len(artifactRecords))
	for _, contract := range contracts {
		references = append(references, contract.Reference)
	}
	for _, artifact := range artifactRecords {
		references = append(references, artifact.Reference)
	}
	sort.Slice(references, func(left, right int) bool {
		if references[left].ID != references[right].ID {
			return references[left].ID < references[right].ID
		}
		return references[left].Digest < references[right].Digest
	})
	identityInput := struct {
		Tool     string                                   `json:"tool"`
		Version  string                                   `json:"version"`
		Contract toolcatalog.SelectedContractProjectionV1 `json:"contract"`
		Target   toolcatalog.SelectedTargetProjectionV1   `json:"target"`
		Records  []portabletool.RecordReferenceV1         `json:"records"`
	}{
		Tool: entry.Provenance.Tool, Version: entry.Provenance.Version,
		Contract: closure.Contract, Target: closure.Target, Records: references,
	}
	identity, err := canonical.Sum("portable-tool-selected-closure", toolcatalog.SelectedClosureIdentityV1, identityInput)
	if err != nil {
		t.Fatal(err)
	}
	closure.Identity = identity
	entry.SelectedClosureDigest = identity
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		fresh.Plan, []providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Projection = projection
	if err := validatePortableToolPythonFreshPlanV1(fresh); err != nil {
		t.Fatalf("rebound two-neutral fresh plan: %v", err)
	}
}

func sortedNeutralDistributionsV1(values map[string]portabletool.BindingArtifactRecordV1) []string {
	result := make([]string, 0, len(values))
	for distribution := range values {
		result = append(result, distribution)
	}
	sort.Strings(result)
	return result
}

func decodePortableToolPythonFreshRecordV1(t *testing.T, record providers.CanonicalProviderData, target any) {
	t.Helper()
	encoded, err := canonical.Marshal(record.Value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}
}

func portableToolPythonFreshTwoNeutralFixtureV1(t *testing.T) (
	PortableToolPythonFreshPlanV1,
	toolcatalog.EmbeddedPortableToolLockRecordSetV1,
) {
	t.Helper()
	fresh, records := portableToolPythonFreshNeutralFixtureV1(t)
	entry := &fresh.Plan.Tools[0]
	const namespace = "tool:fixture-tool/releases/1.0.0"
	contract := portabletool.BindingContractV1{
		Schema: portabletool.BindingContractSchemaV1, ID: namespace + "/bindings/python-alt/contract",
		Name: "python-alt", Package: "neutral-binding-alt", Requirements: []string{"neutral-binding-alt==1.0.0"},
		SupportedPython: []string{"3.12"}, SupportedTags: []string{"py3-none-any"},
		BundledComponents: []portabletool.BundledComponentV1{},
		CLI:               portabletool.ToolExportV1{Name: "neutral-alt-cli", Path: "/opt/neutral-alt/bin/neutral-alt-cli"},
	}
	contractRecord := portableToolPythonFreshRecordV1(t, contract)
	contractReference := portableToolPythonFreshReferenceV1(t, contract.ID, contractRecord)
	artifact := portabletool.BindingArtifactRecordV1{
		Schema: portabletool.BindingArtifactSchemaV1, ID: namespace + "/bindings/python-alt/artifacts/linux-amd64",
		Binding: "python-alt", Contract: contractReference, Name: "neutral-binding-alt", EcosystemVersion: "1.0.0",
		Platform: "linux/amd64", Filename: "neutral_binding_alt-1.0.0-py3-none-any.whl",
		Size: "1", SHA256: portableToolPythonFreshDigestV1(t, "neutral-alt-wheel"), Resolver: "https-sha256",
		Tags: []string{"py3-none-any"}, RequiresPython: ">=3.12,<3.13",
		BundledComponents: []portabletool.BundledComponentV1{},
	}
	artifactRecord := portableToolPythonFreshRecordV1(t, artifact)
	artifactReference := portableToolPythonFreshReferenceV1(t, artifact.ID, artifactRecord)
	entry.Responsibilities.BindingContracts = append(entry.Responsibilities.BindingContracts,
		providers.PortableToolSelectedRecordV1{Reference: contractReference, Record: contractRecord})
	entry.Responsibilities.BindingArtifacts = append(entry.Responsibilities.BindingArtifacts,
		providers.PortableToolSelectedRecordV1{Reference: artifactReference, Record: artifactRecord})
	entry.Exports = append(entry.Exports, providers.PortableToolExportV1{Name: contract.CLI.Name, Path: contract.CLI.Path})
	sort.Slice(entry.Responsibilities.BindingContracts, func(left, right int) bool {
		return entry.Responsibilities.BindingContracts[left].Reference.ID < entry.Responsibilities.BindingContracts[right].Reference.ID
	})
	sort.Slice(entry.Responsibilities.BindingArtifacts, func(left, right int) bool {
		return entry.Responsibilities.BindingArtifacts[left].Reference.ID < entry.Responsibilities.BindingArtifacts[right].Reference.ID
	})
	sort.Slice(entry.Exports, func(left, right int) bool { return entry.Exports[left].Name < entry.Exports[right].Name })
	_, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(fresh.Plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	fresh.Projection = projection
	records.Artifacts = append(records.Artifacts, toolcatalog.EmbeddedPortableToolArtifactSourceV1{
		Scope: "application:neutral", Tool: "fixture-tool", Artifact: artifactReference,
		Descriptor: providerstore.ArtifactDescriptor{
			LogicalPath: "wheels/" + artifact.Filename, Kind: "wheel", Size: artifact.Size, SHA256: artifact.SHA256,
		},
		Source: providers.PortableToolSelectedRecordV1{
			Reference: providers.PortableToolRecordReferenceV1{
				ID:     namespace + "/bindings/python-alt/artifacts/linux-amd64/source",
				Digest: portableToolPythonFreshDigestV1(t, "neutral-alt-source"),
			},
			Record: providers.CanonicalProviderData{Schema: portabletool.ArtifactSourceRecordSchemaV1, Value: map[string]any{}},
		},
		Mirrors: []string{"https://example.invalid/neutral-binding-alt.whl"},
	})
	return fresh, records
}

func preparedPTD2337NeutralBindingWheelV1(t *testing.T, module, distribution, cli, message string) []byte {
	t.Helper()
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	files := map[string]string{
		module + "/__init__.py": "",
		module + "/__main__.py": "def main():\n    print('" + message + "')\n",
		distributionUnderscoreV1(distribution) + "-1.0.0.dist-info/METADATA":         "Metadata-Version: 2.1\nName: " + distribution + "\nVersion: 1.0.0\nRequires-Python: >=3.12,<3.13\n\n",
		distributionUnderscoreV1(distribution) + "-1.0.0.dist-info/WHEEL":            "Wheel-Version: 1.0\nGenerator: reploy-integration\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		distributionUnderscoreV1(distribution) + "-1.0.0.dist-info/entry_points.txt": "[console_scripts]\n" + cli + " = " + module + ".__main__:main\n",
		distributionUnderscoreV1(distribution) + "-1.0.0.dist-info/RECORD":           "",
	}
	for name, body := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

func distributionUnderscoreV1(distribution string) string {
	return strings.ReplaceAll(distribution, "-", "_")
}
