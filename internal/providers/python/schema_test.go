package python

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func schemaTestDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func schemaTestMaterializationScript() providerstore.ArtifactDescriptor {
	return materializationScriptDescriptor()
}

func TestCanonicalProviderRequestV1SortsAndDeduplicatesPackages(t *testing.T) {
	if _, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
	}); err == nil {
		t.Fatal("empty active Python request was accepted")
	}
	z, err := CanonicalPackageRequestV1("zeta==1")
	if err != nil {
		t.Fatal(err)
	}
	a, err := CanonicalPackageRequestV1("alpha==1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "base"}, Requirements: []providers.CanonicalPackageRequest{z, a, z},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanonicalProviderRequestV1(request); err != nil {
		t.Fatal(err)
	}
	values := request.Value["requirements"].([]any)
	if len(values) != 2 || values[0].(canonical.Object)["value"].(canonical.Object)["requirement"] != "alpha==1" {
		t.Fatalf("requirements = %#v", values)
	}
	request.Value["unknown"] = "value"
	if err := ValidateCanonicalProviderRequestV1(request); err == nil {
		t.Fatal("unknown provider request field was accepted")
	}
}

func TestPythonBundleV1CanonicalPayload(t *testing.T) {
	component := "application"
	bundle := PythonBundleV1{
		Interpreter: schemaTestInterpreterEvidence(),
		Script:      schemaTestMaterializationScript(),
		Wheels: []PythonWheelV1{{
			Distribution: "demo", Version: "1.0", Tags: []string{"py3-none-any"},
			Artifact: providerstore.ArtifactDescriptor{LogicalPath: "wheels/demo.whl", Kind: "wheel", Size: "100", SHA256: schemaTestDigest("a")},
		}},
		Outputs: []PythonConsoleScriptV1{{
			Name: "demo", Distribution: "demo", EntryPoint: "demo:main", Path: "/opt/reploy/providers/python/application/bin/demo",
		}},
		Sources: []providers.ResolvedSourceInput{},
	}
	data, err := CanonicalBundleDataV1(component, bundle)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := canonical.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if data.Schema != BundleSchemaV1 || strings.Contains(string(encoded), `"RequirementID"`) || !strings.Contains(string(encoded), `"requirement_id"`) {
		t.Fatalf("bundle data = %s", encoded)
	}
	decoded, err := DecodeCanonicalBundleDataV1(component, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Wheels) != 1 || decoded.Wheels[0].Distribution != "demo" || decoded.Outputs[0].EntryPoint != "demo:main" {
		t.Fatalf("decoded bundle = %#v", decoded)
	}
	wrongScript := bundle
	wrongScript.Script.SHA256 = schemaTestDigest("f")
	if err := ValidateBundleV1(component, wrongScript); err == nil || !strings.Contains(err.Error(), "provider-owned script") {
		t.Fatalf("script mismatch error = %v", err)
	}
	wrongWheelRoot := bundle
	wrongWheelRoot.Wheels = append([]PythonWheelV1{}, bundle.Wheels...)
	wrongWheelRoot.Wheels[0].Artifact.LogicalPath = "other/demo.whl"
	if err := ValidateBundleV1(component, wrongWheelRoot); err == nil || !strings.Contains(err.Error(), "beneath wheels") {
		t.Fatalf("wheel root error = %v", err)
	}
	withoutWheels := bundle
	withoutWheels.Wheels = []PythonWheelV1{}
	if err := ValidateBundleV1(component, withoutWheels); err == nil || !strings.Contains(err.Error(), "at least one wheel") {
		t.Fatalf("empty wheel error = %v", err)
	}
	bundle.Outputs[0].Path = "/usr/local/bin/demo"
	if err := ValidateBundleV1(component, bundle); err == nil || !strings.Contains(err.Error(), "component venv") {
		t.Fatalf("error = %v", err)
	}
}

func TestPythonOwnerValidatorsBindProfileAndBundlePayload(t *testing.T) {
	packageRequest, err := CanonicalPackageRequestV1("demo==1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{packageRequest},
	})
	if err != nil {
		t.Fatal(err)
	}
	interpreter := schemaTestInterpreterEvidence()
	interpreter.Facts = CanonicalInterpreterFactsV1("3.13.2")
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	profile := providers.RequirementProfile{
		Schema: providers.RequirementProfileSchemaV1, Provider: blueprint.ComponentTypePython,
		Declaration: providers.RequirementDeclaration{
			Executables: []providers.ExecutableRequirement{{
				ID: "interpreter", Command: "python", Supplier: "base", ValidationPolicy: providers.ValidationPolicyCompatible,
			}},
			Files:        []providers.FileRequirement{},
			ProviderData: providers.CanonicalProviderData{Schema: request.Schema, Value: request.Value},
		},
		SelectedExecutables: []providers.ExecutableEvidence{interpreter}, SelectedFiles: []providers.FileEvidence{},
		Platform: platform, Facts: CanonicalProfileFactsV1("application", []providers.ResolvedSourceInput{}),
	}
	profileDigest, err := providers.RequirementProfileDigest(profile, ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	driftedProfile := profile
	driftedProfile.Declaration.Executables = append([]providers.ExecutableRequirement{}, profile.Declaration.Executables...)
	driftedProfile.Declaration.Executables[0].VersionConstraint = ">=3.11"
	if _, err := providers.RequirementProfileDigest(driftedProfile, ValidateRequirementProfileV1); err == nil || !strings.Contains(err.Error(), "canonical request") {
		t.Fatalf("drifted Python profile error = %v", err)
	}
	artifact := providerstore.ArtifactDescriptor{LogicalPath: "wheels/demo.whl", Kind: "wheel", Size: "100", SHA256: schemaTestDigest("a")}
	pythonBundle := PythonBundleV1{
		Interpreter: interpreter,
		Script:      schemaTestMaterializationScript(),
		Wheels:      []PythonWheelV1{{Distribution: "demo", Version: "1.0", Tags: []string{"py3-none-any"}, Artifact: artifact}},
		Outputs: []PythonConsoleScriptV1{{
			Name: "demo", Distribution: "demo", EntryPoint: "demo:main", Path: "/opt/reploy/providers/python/application/bin/demo",
		}},
		Sources: []providers.ResolvedSourceInput{},
	}
	data, err := CanonicalBundleDataV1("application", pythonBundle)
	if err != nil {
		t.Fatal(err)
	}
	payload := providers.ResolvedBundleIdentityV1{
		Schema: providers.ResolvedBundleSchemaV1, NodeID: "python/application", Provider: blueprint.ComponentTypePython,
		Request: request, RequirementProfileDigest: profileDigest, RecipeVersion: RecipeVersion, Platform: platform,
		Upstream:        providers.RealizedImageV1{Digest: schemaTestDigest("b"), ConfigDigest: schemaTestDigest("c"), RootFSSubject: schemaTestDigest("d")},
		SelectedSources: []providers.ResolvedSourceInput{},
		Artifacts:       []providerstore.ArtifactDescriptor{schemaTestMaterializationScript(), artifact},
		Outputs: []providers.ResolvedOutput{{
			SupplierComponent: "application", SupplierNode: "python/application", Name: "demo",
			Candidate: providers.ExecutableCandidate{
				InvocationPath: "/opt/reploy/providers/python/application/bin/demo",
				Provenance: providers.CanonicalProviderData{
					Schema: ConsoleScriptOutputSchemaV1, Value: canonical.Object{"distribution": "demo", "entry_point": "demo:main"},
				},
			},
		}},
		ProviderPayload: data,
	}
	bundle, err := providers.NewResolvedBundle(payload, ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := providers.ValidateResolvedBundle(bundle, ValidateResolvedBundlePayloadV1); err != nil {
		t.Fatal(err)
	}
	wrongRecipe := payload
	wrongRecipe.RecipeVersion = "python-v2"
	if err := ValidateResolvedBundlePayloadV1(wrongRecipe); err == nil || !strings.Contains(err.Error(), "recipe version") {
		t.Fatalf("recipe mismatch error = %v", err)
	}
	payload.Outputs[0].Candidate.Provenance.Value["entry_point"] = "other:main"
	if err := ValidateResolvedBundlePayloadV1(payload); err == nil || !strings.Contains(err.Error(), "outputs") {
		t.Fatalf("output mismatch error = %v", err)
	}
}

func TestComponentProviderPlansOneTypedNodePerPythonComponent(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	packageRequest, err := CanonicalPackageRequestV1("demo==1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{packageRequest},
	})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := (ComponentProvider{}).Plan(providers.PlanInput{
		Platform:   platform,
		Components: []providers.ResolvedComponentRequestV1{{Component: "application", Provider: blueprint.ComponentTypePython, Request: request}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "python/application" || len(nodes[0].OutputDeclarations) != 0 {
		t.Fatalf("nodes = %#v", nodes)
	}
	requirement := nodes[0].Requirements.Executables[0]
	if requirement.Command != "python" || requirement.VersionConstraint != ">=3.11" || requirement.Supplier != "base" {
		t.Fatalf("requirement = %#v", requirement)
	}
}

func schemaTestInterpreterEvidence() providers.ExecutableEvidence {
	return providers.ExecutableEvidence{
		Schema: providers.ExecutableEvidenceSchemaV1, RequirementID: "interpreter",
		Output: providers.QualifiedOutput{Component: "base", Name: "python"}, InvocationPath: "/usr/bin/python3",
		LinkChain: []providers.LinkEvidence{},
		Terminal: providers.FileEvidence{
			Schema: providers.FileEvidenceSchemaV1, RequirementID: "interpreter", Path: "/usr/bin/python3", Kind: "regular", Mode: "0755", Size: "1", SHA256: schemaTestDigest("b"),
		},
		Access: providers.PortableAccessEvidence{
			Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1,
			Paths: []providers.AccessPathEvidence{{Path: "/usr/bin/python3", Kind: "regular", Mode: "0755", Required: "other-read-execute"}},
		},
		Facts: canonical.Envelope{Schema: "python-interpreter-facts-v1", Value: canonical.Object{}},
	}
}
