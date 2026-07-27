package python

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type preparedTestSink struct{}

func (preparedTestSink) Publish(_ context.Context, logicalPath string, kind string, reader io.Reader) (providerstore.ArtifactDescriptor, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, err
	}
	digest := sha256.Sum256(content)
	return providerstore.ArtifactDescriptor{
		LogicalPath: logicalPath, Kind: kind, Size: strconv.Itoa(len(content)),
		SHA256: canonical.Digest(fmt.Sprintf("sha256:%x", digest)),
	}, nil
}

func TestWheelNodeResolverReturnsCanonicalBundleThroughGraph(t *testing.T) {
	dir := t.TempDir()
	writeTestWheel(t, dir, "demo_server-1.2.3-py3-none-any.whl", "Demo-Server", "1.2.3", map[string]string{"demo-server": "demo:main"})
	plan, platform, upstream, catalog, selectedEvidence := preparedNodeTestPlan(t, "demo-server==1.2.3")
	resolver := WheelNodeResolver{
		PrepareWheels: func(context.Context, providerapi.ResolveInput, providerapi.ExecutableEvidence) (string, error) {
			return dir, nil
		},
		ResolveInterpreter: func(_ context.Context, requirement providerapi.ExecutableRequirement, candidates []providerapi.RealizedOutput, gotUpstream providerapi.RealizedImageV1, gotPlatform blueprint.Platform) (providerapi.ExecutableEvidence, error) {
			if requirement.ID != "interpreter" || len(candidates) != 1 || gotUpstream != upstream || gotPlatform != platform {
				return providerapi.ExecutableEvidence{}, fmt.Errorf("unexpected interpreter validation input")
			}
			return selectedEvidence, nil
		},
	}
	result, err := providerapi.ExecuteProviderGraph(context.Background(), providerapi.GraphExecutionRequest{
		Plan: plan, Platform: platform, SourceCandidates: []providerapi.ResolvedSourceInput{},
		BaseImage: upstream, BaseCatalog: catalog,
		ReusableArtifacts: map[providerapi.NodeID][]providerstore.StoreObjectRef{},
		CachedResolutions: map[providerapi.NodeID]providerapi.ResolveResult{},
		Validators: func(node providerapi.NodeSpec) (providerapi.ProviderOwnerValidators, error) {
			if node.Provider != blueprint.ComponentTypePython {
				return providerapi.ProviderOwnerValidators{}, fmt.Errorf("unexpected provider %q", node.Provider)
			}
			return providerapi.ProviderOwnerValidators{
				Profile: ValidateRequirementProfileV1, Bundle: ValidateResolvedBundlePayloadV1,
			}, nil
		},
		PrepareNode: func(ctx context.Context, request providerapi.GraphNodePrepareRequest) (providerapi.GraphNodePreparation, error) {
			resolution, err := providerapi.ResolveProviderNode(ctx, request.Resolve, resolver, preparedTestSink{}, providerapi.ProviderOwnerValidators{
				Profile: ValidateRequirementProfileV1, Bundle: ValidateResolvedBundlePayloadV1,
			})
			if err != nil {
				return providerapi.GraphNodePreparation{}, err
			}
			return providerapi.GraphNodePreparation{Resolution: resolution, Consumer: preparedTestConsumerValidation()}, nil
		},
		MaterializeNode: func(_ context.Context, request providerapi.GraphNodeMaterializeRequest) (providerapi.GraphNodeMaterializeResult, error) {
			return providerapi.GraphNodeMaterializeResult{
				Image: providerapi.RealizedImageV1{
					Digest: schemaTestDigest("d"), ConfigDigest: schemaTestDigest("e"), RootFSSubject: schemaTestDigest("f"),
				},
				TransactionDigest:    schemaTestDigest("9"),
				GeneratedExecutables: []providerapi.RealizedGeneratedExecutable{},
				Outputs:              preparedTestRealizedOutputs(request.Input.Bundle),
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Bundles) != 1 || len(result.Profiles) != 1 || len(result.PrefixImages) != 2 || len(result.Materializations) != 1 || len(result.Catalog) != 2 {
		t.Fatalf("graph result = %#v", result)
	}
	bundle, err := DecodeCanonicalBundleDataV1("application", result.Bundles[0].Payload.ProviderPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Wheels) != 1 || len(bundle.Wheels[0].Tags) != 1 || bundle.Wheels[0].Tags[0] != "py3-none-any" {
		t.Fatalf("wheels = %#v", bundle.Wheels)
	}
	if bundle.Script != materializationScriptDescriptor() || len(result.Bundles[0].Payload.Artifacts) != 2 {
		t.Fatalf("script or artifacts = %#v; %#v", bundle.Script, result.Bundles[0].Payload.Artifacts)
	}
	if len(bundle.Outputs) != 1 || bundle.Outputs[0].EntryPoint != "demo:main" || bundle.Outputs[0].Path != "/opt/reploy/providers/python/application/bin/demo-server" {
		t.Fatalf("outputs = %#v", bundle.Outputs)
	}
	if result.Profiles[0].SelectedExecutables[0].Facts.Value["version"] != "3.13.2" {
		t.Fatalf("profile = %#v", result.Profiles[0])
	}
}

func TestWheelNodeResolverRejectsUnexpectedOrLinkedOutput(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(*testing.T, string)
	}{
		{name: "unexpected file", write: func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "resolver.log"), []byte("log"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wheel symlink", write: func(t *testing.T, dir string) {
			target := filepath.Join(t.TempDir(), "demo_server-1.2.3-py3-none-any.whl")
			writeTestWheel(t, filepath.Dir(target), filepath.Base(target), "Demo-Server", "1.2.3", nil)
			if err := os.Symlink(target, filepath.Join(dir, filepath.Base(target))); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			test.write(t, dir)
			plan, platform, upstream, catalog, selectedEvidence := preparedNodeTestPlan(t, "demo-server==1.2.3")
			resolver := WheelNodeResolver{
				PrepareWheels: func(context.Context, providerapi.ResolveInput, providerapi.ExecutableEvidence) (string, error) {
					return dir, nil
				},
				ResolveInterpreter: func(context.Context, providerapi.ExecutableRequirement, []providerapi.RealizedOutput, providerapi.RealizedImageV1, blueprint.Platform) (providerapi.ExecutableEvidence, error) {
					return selectedEvidence, nil
				},
			}
			_, err := providerapi.ResolveProviderNode(context.Background(), providerapi.ResolveNodeRequest{
				Plan: plan, NodeID: "python/application", EarlierCatalog: catalog,
				Platform: platform, SourceCandidates: []providerapi.ResolvedSourceInput{}, Upstream: upstream,
				ReusableArtifacts: []providerstore.StoreObjectRef{},
			}, resolver, preparedTestSink{}, providerapi.ProviderOwnerValidators{
				Profile: ValidateRequirementProfileV1, Bundle: ValidateResolvedBundlePayloadV1,
			})
			if err == nil || !strings.Contains(err.Error(), "unexpected entry") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func preparedTestConsumerValidation() providerapi.GraphConsumerValidation {
	input := func(id string, role string, invocationPath string) providerapi.ValidatedExecutableInput {
		evidence := schemaTestInterpreterEvidence()
		evidence.RequirementID = id
		evidence.Output = providerapi.QualifiedOutput{Component: "base", Name: id}
		evidence.InvocationPath = invocationPath
		evidence.Terminal.RequirementID = id
		evidence.Terminal.Path = invocationPath
		evidence.Access.Paths[0].Path = invocationPath
		return providerapi.ValidatedExecutableInput{ID: id, Role: role, Policy: providerapi.ValidationPolicyCompatible, Evidence: evidence}
	}
	return providerapi.GraphConsumerValidation{
		Carrier:             input("carrier", providerapi.ExecutableRoleCarrier, "/bin/sh"),
		EnvironmentLauncher: input("cleanenv", providerapi.ExecutableRoleEnvironmentLauncher, "/usr/bin/env"),
		FinalImageConfig: providerapi.ImageConfigPolicy{
			User: "1000:1000", WorkingDir: "/work", Environment: []providerapi.EnvironmentVariable{},
			Entrypoint: []string{}, Command: []string{}, Healthcheck: providerapi.ImageHealthcheckNone,
			StopSignal: "SIGTERM", Labels: []providerapi.ImageLabel{},
		},
	}
}

func preparedTestRealizedOutputs(bundle providerapi.ResolvedBundle) []providerapi.RealizedOutput {
	result := make([]providerapi.RealizedOutput, 0, len(bundle.Payload.Outputs))
	for _, resolved := range bundle.Payload.Outputs {
		evidence := schemaTestInterpreterEvidence()
		evidence.RequirementID = ""
		evidence.Output = providerapi.QualifiedOutput{Component: resolved.SupplierComponent, Name: resolved.Name}
		evidence.InvocationPath = resolved.Candidate.InvocationPath
		evidence.Terminal.RequirementID = ""
		evidence.Terminal.Path = resolved.Candidate.InvocationPath
		evidence.Access.Paths[0].Path = resolved.Candidate.InvocationPath
		result = append(result, providerapi.RealizedOutput{
			SupplierComponent: resolved.SupplierComponent,
			SupplierNode:      resolved.SupplierNode,
			Name:              resolved.Name,
			Candidate:         resolved.Candidate,
			Evidence:          evidence,
		})
	}
	return result
}

func TestWheelNodeResolverRequiresResolvedSourceArtifactDigest(t *testing.T) {
	dir := t.TempDir()
	wheel := "demo_server-1.2.3-py3-none-any.whl"
	writeTestWheel(t, dir, wheel, "Demo-Server", "1.2.3", nil)
	plan, platform, upstream, catalog, selectedEvidence := preparedNodeTestPlan(t, "demo-server==1.2.3")
	wheelDigest, err := fileSHA256(filepath.Join(dir, wheel))
	if err != nil {
		t.Fatal(err)
	}
	source := testPythonSourceInput(
		"application", "demo-server", "1.2.3", schemaTestDigest("4"), canonical.Digest("sha256:"+wheelDigest),
	)
	unusedSource := source
	unusedSource.LogicalPackage = "unused-source"
	unusedSource.SourceInputDigest = schemaTestDigest("6")
	unusedSource.OutputArtifactDigest = schemaTestDigest("7")
	resolver := WheelNodeResolver{
		PrepareWheels: func(context.Context, providerapi.ResolveInput, providerapi.ExecutableEvidence) (string, error) {
			return dir, nil
		},
		ResolveInterpreter: func(context.Context, providerapi.ExecutableRequirement, []providerapi.RealizedOutput, providerapi.RealizedImageV1, blueprint.Platform) (providerapi.ExecutableEvidence, error) {
			return selectedEvidence, nil
		},
	}
	request := providerapi.ResolveNodeRequest{
		Plan: plan, NodeID: "python/application", EarlierCatalog: catalog,
		Platform: platform, SourceCandidates: []providerapi.ResolvedSourceInput{source, unusedSource}, Upstream: upstream,
		ReusableArtifacts: []providerstore.StoreObjectRef{},
	}
	validators := providerapi.ProviderOwnerValidators{Profile: ValidateRequirementProfileV1, Bundle: ValidateResolvedBundlePayloadV1}
	result, err := providerapi.ResolveProviderNode(context.Background(), request, resolver, preparedTestSink{}, validators)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := DecodeCanonicalBundleDataV1("application", result.Bundle.Payload.ProviderPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Sources) != 1 || bundle.Sources[0].LogicalPackage != source.LogicalPackage ||
		bundle.Sources[0].SourceInputDigest != source.SourceInputDigest || bundle.Sources[0].OutputArtifactDigest != source.OutputArtifactDigest {
		t.Fatalf("sources = %#v", bundle.Sources)
	}
	if err := ValidateResolvedSourceInputV2(bundle.Sources[0]); err != nil {
		t.Fatal(err)
	}
	if len(result.SelectedSources) != 1 || !reflect.DeepEqual(result.SelectedSources[0], source) {
		t.Fatalf("selected sources = %#v", result.SelectedSources)
	}
	component, profileSources, err := decodeProfileFactsV1(result.Profile.Facts)
	if err != nil {
		t.Fatal(err)
	}
	if component != "application" || len(profileSources) != 1 || profileSources[0].OutputArtifactDigest != source.OutputArtifactDigest {
		t.Fatalf("profile sources = %#v for %q", profileSources, component)
	}
	staleRequest := request
	staleRequest.SourceCandidates = append([]providerapi.ResolvedSourceInput{}, request.SourceCandidates...)
	staleRequest.SourceCandidates[0].OutputArtifactDigest = schemaTestDigest("5")
	if _, err := providerapi.ResolveProviderNode(context.Background(), staleRequest, resolver, preparedTestSink{}, validators); err == nil || !strings.Contains(err.Error(), "want resolved artifact") {
		t.Fatalf("stale source artifact error = %v", err)
	}
}

func preparedNodeTestPlan(t *testing.T, requirement string) (providerapi.ProviderPlanV1, blueprint.Platform, providerapi.RealizedImageV1, []providerapi.RealizedOutput, providerapi.ExecutableEvidence) {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	packageRequest, err := CanonicalPackageRequestV1(requirement)
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "base"},
		Requirements: []providerapi.CanonicalPackageRequest{packageRequest},
	})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := (ComponentProvider{}).Plan(providerapi.PlanInput{
		Platform:   platform,
		Components: []providerapi.ResolvedComponentRequestV1{{Component: "application", Provider: blueprint.ComponentTypePython, Request: request}},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseOutput := providerapi.OutputDeclaration{
		SupplierComponent: "base", Name: "python", Kind: providerapi.OutputKindExecutable,
		CandidatePath: "/usr/bin/python3", Provenance: providerapi.CanonicalProviderData{Schema: "base-export-v1", Value: canonical.Object{}},
	}
	base := providerapi.NodeSpec{
		ID: "base", Provider: blueprint.ComponentTypeBase, Components: []string{"base"},
		Request:            providerapi.CanonicalProviderRequest{Schema: "base-provider-request-v1", Provider: blueprint.ComponentTypeBase, Value: canonical.Object{}},
		OutputDeclarations: []providerapi.OutputDeclaration{baseOutput},
		Requirements: providerapi.RequirementDeclaration{
			Executables: []providerapi.ExecutableRequirement{}, Files: []providerapi.FileRequirement{},
			ProviderData: providerapi.CanonicalProviderData{Schema: "base-requirements-v1", Value: canonical.Object{}},
		},
	}
	plan := providerapi.ProviderPlanV1{
		Schema: providerapi.ProviderPlanSchemaV1, Nodes: []providerapi.NodeSpec{base, nodes[0]},
		Edges: []providerapi.ProviderEdgeV1{{
			Supplier: "base", Consumer: "python/application", RequirementID: "interpreter",
			Output: providerapi.QualifiedOutput{Component: "base", Name: "python"},
		}},
	}
	catalogEvidence := schemaTestInterpreterEvidence()
	catalogEvidence.RequirementID = ""
	catalogEvidence.Terminal.RequirementID = ""
	catalogEvidence.Facts = providerapi.CanonicalProviderData{Schema: "base-python-facts-v1", Value: canonical.Object{}}
	selectedEvidence := schemaTestInterpreterEvidence()
	selectedEvidence.Facts = CanonicalInterpreterFactsV1("3.13.2")
	catalog := []providerapi.RealizedOutput{{
		SupplierComponent: "base", SupplierNode: "base", Name: "python",
		Candidate: providerapi.ExecutableCandidate{InvocationPath: "/usr/bin/python3", Provenance: baseOutput.Provenance},
		Evidence:  catalogEvidence,
	}}
	upstream := providerapi.RealizedImageV1{Digest: schemaTestDigest("a"), ConfigDigest: schemaTestDigest("b"), RootFSSubject: schemaTestDigest("c")}
	return plan, platform, upstream, catalog, selectedEvidence
}

func TestRequirementAllowsVersion(t *testing.T) {
	tests := []struct {
		requirement string
		version     string
		want        bool
	}{
		{"demo>=1.4,<2", "1.7.2", true},
		{"demo>=1.4,<2", "2.0", false},
		{"demo==1.4.*", "1.4.9", true},
		{"demo!=1.4.*", "1.4.9", false},
		{"demo~=1.4.5", "1.4.9", true},
		{"demo~=1.4.5", "1.5", false},
	}
	for _, test := range tests {
		got, checked := requirementAllowsVersion(test.requirement, test.version)
		if !checked || got != test.want {
			t.Errorf("requirementAllowsVersion(%q, %q) = (%v, %v), want (%v, true)", test.requirement, test.version, got, checked, test.want)
		}
	}
}

func TestInterpreterVersionSatisfiesObservedRuntime(t *testing.T) {
	tests := []struct {
		constraint string
		version    string
		want       bool
	}{
		{constraint: "", version: "3.13.2", want: true},
		{constraint: ">=3.11", version: "3.13.2", want: true},
		{constraint: ">=3.11,<3.13", version: "3.13.2", want: false},
		{constraint: "~=3.12.0", version: "3.12.7", want: true},
	}
	for _, test := range tests {
		got, err := InterpreterVersionSatisfies(test.constraint, test.version)
		if err != nil {
			t.Fatalf("InterpreterVersionSatisfies(%q, %q): %v", test.constraint, test.version, err)
		}
		if got != test.want {
			t.Errorf("InterpreterVersionSatisfies(%q, %q) = %v, want %v", test.constraint, test.version, got, test.want)
		}
	}
	if _, err := InterpreterVersionSatisfies("not-a-specifier", "3.13.2"); err == nil {
		t.Fatal("unsupported interpreter constraint was accepted")
	}
}

func TestInspectPreparedWheelDistributionsV1ReturnsSortedValidatedClosure(t *testing.T) {
	dir := t.TempDir()
	writeTestWheel(t, dir, "zeta-2-py3-none-any.whl", "zeta", "2", nil)
	writeTestWheel(t, dir, "alpha-1-py3-none-any.whl", "Alpha", "1", nil)
	distributions, err := InspectPreparedWheelDistributionsV1(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(distributions, []string{"alpha", "zeta"}) {
		t.Fatalf("distributions = %#v", distributions)
	}
	if err := os.WriteFile(filepath.Join(dir, "unexpected.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPreparedWheelDistributionsV1(context.Background(), dir); err == nil ||
		!strings.Contains(err.Error(), `unexpected entry "unexpected.txt"`) {
		t.Fatalf("unexpected-entry error = %v", err)
	}
}

func TestInspectWheelDeclaredDependenciesV1ReadsOnlyRequiresDistNames(t *testing.T) {
	dir := t.TempDir()
	filename := "root-1-py3-none-any.whl"
	writeTestWheelWithDependencies(t, dir, filename, "root", "1", []string{
		"Hydra_Core>=1.3",
		"omegaconf>=2.3; python_version >= '3.10'",
		"hydra-core<2",
	})
	dependencies, err := InspectWheelDeclaredDependenciesV1(
		filepath.Join(dir, filename), []string{"hydra-core", "omegaconf"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dependencies, []string{"hydra-core", "omegaconf"}) {
		t.Fatalf("dependencies = %#v", dependencies)
	}
	dependencies, err = InspectWheelDeclaredDependenciesV1(
		filepath.Join(dir, filename), []string{"hydra-core"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dependencies, []string{"hydra-core"}) {
		t.Fatalf("unresolved dependency became an override candidate: %#v", dependencies)
	}
}

func TestInspectWheelDeclaredDependenciesV1AcceptsMetadataEndingAtEOF(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "root-1-py3-none-any.whl")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	writeZipFile(t, archive, "root-1.dist-info/METADATA", "Metadata-Version: 2.1\nName: root\nVersion: 1\nRequires-Dist: dependency>=1")
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	dependencies, err := InspectWheelDeclaredDependenciesV1(filename, []string{"dependency"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dependencies, []string{"dependency"}) {
		t.Fatalf("dependencies = %#v", dependencies)
	}
}

func TestInspectWheelDeclaredDependenciesV1BoundsRetainedMetadataFields(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "root-1-py3-none-any.whl")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	writeZipFile(t, archive, "root-1.dist-info/METADATA",
		"Metadata-Version: 2.1\nName: root\nVersion: 1\nRequires-Dist: "+
			strings.Repeat("a", maxWheelMetadataFieldBytes+1)+"\n",
	)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWheelDeclaredDependenciesV1(filename, []string{"dependency"}); err == nil ||
		!strings.Contains(err.Error(), "Requires-Dist field exceeds") {
		t.Fatalf("oversized metadata error = %v", err)
	}
}

func TestInspectWheelDeclaredDependenciesV1StreamsLargeUnrelatedMetadata(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "root-1-py3-none-any.whl")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	writeZipFile(t, archive, "root-1.dist-info/METADATA",
		"Metadata-Version: 2.1\nName: root\nVersion: 1\nDescription: "+
			strings.Repeat("x", maxWheelMetadataFieldBytes*2)+"\nRequires-Dist: dependency>=1\n",
	)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	dependencies, err := InspectWheelDeclaredDependenciesV1(filename, []string{"dependency"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dependencies, []string{"dependency"}) {
		t.Fatalf("dependencies = %#v", dependencies)
	}
}

func writeTestWheel(t *testing.T, dir string, filename string, name string, version string, scripts map[string]string) {
	writeTestWheelContent(t, dir, filename, name, version, scripts, nil)
}

func writeTestWheelWithDependencies(t *testing.T, dir string, filename string, name string, version string, dependencies []string) {
	writeTestWheelContent(t, dir, filename, name, version, nil, dependencies)
}

func writeTestWheelContent(t *testing.T, dir string, filename string, name string, version string, scripts map[string]string, dependencies []string) {
	t.Helper()
	file, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	distInfo := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info/"
	var metadata strings.Builder
	metadata.WriteString("Metadata-Version: 2.1\nName: " + name + "\nVersion: " + version + "\n")
	for _, dependency := range dependencies {
		metadata.WriteString("Requires-Dist: " + dependency + "\n")
	}
	metadata.WriteString("\n")
	writeZipFile(t, archive, distInfo+"METADATA", metadata.String())
	writeZipFile(t, archive, distInfo+"WHEEL", "Wheel-Version: 1.0\nGenerator: reploy-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n")
	if len(scripts) > 0 {
		var entries strings.Builder
		entries.WriteString("[console_scripts]\n")
		for name, target := range scripts {
			entries.WriteString(name + " = " + target + "\n")
		}
		writeZipFile(t, archive, distInfo+"entry_points.txt", entries.String())
	}
	writeZipFile(t, archive, distInfo+"RECORD", "")
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZipFile(t *testing.T, archive *zip.Writer, name string, content string) {
	t.Helper()
	writer, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}
