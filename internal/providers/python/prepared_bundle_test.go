package python

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	source := providerapi.ResolvedSourceInput{
		Schema: providerapi.ResolvedSourceInputSchemaV1, Component: "application", LogicalPackage: "demo-server",
		SourceManifestDigest: schemaTestDigest("4"), BuilderProfile: "python-wheel-v1",
		BuildSettings:     providerapi.CanonicalProviderData{Schema: "python-source-build-settings-v1", Value: canonical.Object{}},
		EcosystemMetadata: providerapi.CanonicalProviderData{Schema: "python-source-metadata-v1", Value: canonical.Object{}},
		ArtifactDigest:    canonical.Digest("sha256:" + wheelDigest),
	}
	unusedSource := source
	unusedSource.LogicalPackage = "unused-source"
	unusedSource.SourceManifestDigest = schemaTestDigest("6")
	unusedSource.ArtifactDigest = schemaTestDigest("7")
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
	if len(bundle.Sources) != 1 || !reflect.DeepEqual(bundle.Sources[0], source) {
		t.Fatalf("sources = %#v", bundle.Sources)
	}
	if len(result.SelectedSources) != 1 || !reflect.DeepEqual(result.SelectedSources[0], source) {
		t.Fatalf("selected sources = %#v", result.SelectedSources)
	}
	component, profileSources, err := decodeProfileFactsV1(result.Profile.Facts)
	if err != nil {
		t.Fatal(err)
	}
	if component != "application" || len(profileSources) != 1 || profileSources[0].ArtifactDigest != source.ArtifactDigest {
		t.Fatalf("profile sources = %#v for %q", profileSources, component)
	}
	staleRequest := request
	staleRequest.SourceCandidates = append([]providerapi.ResolvedSourceInput{}, request.SourceCandidates...)
	staleRequest.SourceCandidates[0].ArtifactDigest = schemaTestDigest("5")
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

func TestPreparedBundleResolverReadsClosedArtifactsAndScripts(t *testing.T) {
	dir := t.TempDir()
	writeTestWheel(t, dir, "demo_server-1.2.3-py3-none-any.whl", "Demo-Server", "1.2.3", map[string]string{"demo-server": "demo:main"})
	resolver := PreparedBundleResolver{Dir: dir, BaseIdentity: "python@sha256:base"}
	resolved, err := resolver.ResolvePython(context.Background(), LegacyResolveRequest{
		Platform:   "linux/amd64",
		Components: []LegacyComponent{{Name: "application", Requirements: []string{"demo-server[http]>=1.2"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Artifacts) != 1 || resolved.Artifacts[0].Identifier != "demo-server" || len(resolved.Artifacts[0].SHA256) != 64 {
		t.Fatalf("artifacts = %#v", resolved.Artifacts)
	}
	if resolved.ConsoleScripts["demo-server"] != "demo-server" {
		t.Fatalf("scripts = %#v", resolved.ConsoleScripts)
	}
}

func TestPreparedBundleResolverEnforcesTranslationPrecedence(t *testing.T) {
	dir := t.TempDir()
	wheel := "demo_server-1.0-py3-none-any.whl"
	writeTestWheel(t, dir, wheel, "demo-server", "1.0", nil)
	request := LegacyResolveRequest{
		Components:   []LegacyComponent{{Name: "application", Requirements: []string{"demo-server"}}},
		Translations: []LegacyTranslation{{Name: "workspace", Root: "..", Mappings: map[string]string{"demo-server": "server"}}},
	}
	resolver := PreparedBundleResolver{Dir: dir, BaseIdentity: "python@sha256:base"}
	if _, err := resolver.ResolvePython(context.Background(), request); err == nil || !strings.Contains(err.Error(), "did not take precedence") {
		t.Fatalf("error = %v", err)
	}
	manifest := map[string]any{
		"schema_version": 1,
		"local_sources":  map[string]any{"demo-server": map[string]any{"wheel": wheel, "fingerprint": "test"}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, preparedBundleManifestName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolvePython(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedBundleResolverRejectsTranslatedVersionOutsideRequirement(t *testing.T) {
	dir := t.TempDir()
	wheel := "demo_server-1.4-py3-none-any.whl"
	writeTestWheel(t, dir, wheel, "demo-server", "1.4", nil)
	manifest := map[string]any{
		"schema_version": 1,
		"local_sources":  map[string]any{"demo-server": map[string]any{"wheel": wheel, "fingerprint": "test"}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, preparedBundleManifestName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	request := LegacyResolveRequest{
		Components:   []LegacyComponent{{Name: "application", Requirements: []string{"demo-server[imap]>=2.0,<3"}}},
		Translations: []LegacyTranslation{{Name: "workspace", Mappings: map[string]string{"demo-server": "server"}}},
	}
	_, err = (PreparedBundleResolver{Dir: dir, BaseIdentity: "python@sha256:base"}).ResolvePython(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), `built version 1.4 does not satisfy`) {
		t.Fatalf("error = %v", err)
	}
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

func TestPreparedBundleResolverRejectsMetadataAndDuplicateCollisions(t *testing.T) {
	t.Run("filename metadata mismatch", func(t *testing.T) {
		dir := t.TempDir()
		writeTestWheel(t, dir, "demo-1.0-py3-none-any.whl", "other", "1.0", nil)
		_, err := (PreparedBundleResolver{Dir: dir, BaseIdentity: "base"}).ResolvePython(context.Background(), LegacyResolveRequest{})
		if err == nil || !strings.Contains(err.Error(), "metadata identifies") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("duplicate console script", func(t *testing.T) {
		dir := t.TempDir()
		writeTestWheel(t, dir, "one-1.0-py3-none-any.whl", "one", "1.0", map[string]string{"demo": "one:main"})
		writeTestWheel(t, dir, "two-1.0-py3-none-any.whl", "two", "1.0", map[string]string{"demo": "two:main"})
		_, err := (PreparedBundleResolver{Dir: dir, BaseIdentity: "base"}).ResolvePython(context.Background(), LegacyResolveRequest{})
		if err == nil || !strings.Contains(err.Error(), "provided by both") {
			t.Fatalf("error = %v", err)
		}
	})
}

func writeTestWheel(t *testing.T, dir string, filename string, name string, version string, scripts map[string]string) {
	t.Helper()
	file, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	distInfo := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info/"
	writeZipFile(t, archive, distInfo+"METADATA", "Metadata-Version: 2.1\nName: "+name+"\nVersion: "+version+"\n\n")
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
