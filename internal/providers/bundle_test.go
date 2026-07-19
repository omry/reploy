package providers

import (
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

func testDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func validPythonBundlePayload() ResolvedBundleIdentityV1 {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		panic(err)
	}
	return ResolvedBundleIdentityV1{
		Schema:                   ResolvedBundleSchemaV1,
		NodeID:                   "python/application",
		Provider:                 blueprint.ComponentTypePython,
		Request:                  providerRequest(blueprint.ComponentTypePython, "python-provider-request-v1"),
		RequirementProfileDigest: testDigest("1"),
		RecipeVersion:            "python-wheelhouse-v1",
		Platform:                 platform,
		Upstream: RealizedImageV1{
			Digest: testDigest("2"), ConfigDigest: testDigest("3"), RootFSSubject: testDigest("4"),
		},
		Artifacts: []providerstore.ArtifactDescriptor{{
			LogicalPath: "wheels/demo.whl", Kind: "wheel", Size: "1024", SHA256: testDigest("5"),
		}},
		Outputs: []ResolvedOutput{{
			SupplierComponent: "application",
			SupplierNode:      "python/application",
			Name:              "demo",
			Candidate: ExecutableCandidate{
				InvocationPath: "/opt/reploy/python/application/bin/demo",
				Provenance:     providerData("python-console-script-v1"),
			},
		}},
		ProviderPayload: providerData("python-bundle-v1"),
	}
}

func acceptTestBundleOwner(payload ResolvedBundleIdentityV1) error {
	if payload.Request.Schema != "python-provider-request-v1" || payload.ProviderPayload.Schema != "python-bundle-v1" {
		return errors.New("unknown provider-owned schema")
	}
	return nil
}

func TestResolvedBundleIdentityExcludesStoredIdentity(t *testing.T) {
	payload := validPythonBundlePayload()
	bundle, err := NewResolvedBundle(payload, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResolvedBundle(bundle, acceptTestBundleOwner); err != nil {
		t.Fatal(err)
	}
	second, err := NewResolvedBundle(payload, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if second.Identity != bundle.Identity {
		t.Fatalf("identities differ: %q != %q", second.Identity, bundle.Identity)
	}
	corrupt := bundle
	corrupt.Identity = testDigest("f")
	if err := ValidateResolvedBundle(corrupt, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
	changed := payload
	changed.RecipeVersion = "python-wheelhouse-v2"
	changedBundle, err := NewResolvedBundle(changed, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if changedBundle.Identity == bundle.Identity {
		t.Fatal("recipe version did not change bundle identity")
	}
}

func TestValidateResolvedBundleRejectsMalformedPayload(t *testing.T) {
	valid := validPythonBundlePayload()
	tests := []struct {
		name   string
		mutate func(*ResolvedBundleIdentityV1)
		want   string
	}{
		{name: "schema", mutate: func(value *ResolvedBundleIdentityV1) { value.Schema = "resolved-bundle-v2" }, want: "schema"},
		{name: "base bundle", mutate: func(value *ResolvedBundleIdentityV1) { value.Provider = blueprint.ComponentTypeBase }, want: "base root"},
		{name: "node", mutate: func(value *ResolvedBundleIdentityV1) { value.NodeID = "python/base" }, want: "python/<component>"},
		{name: "request provider", mutate: func(value *ResolvedBundleIdentityV1) { value.Request.Provider = blueprint.ComponentTypeAPT }, want: "does not match"},
		{name: "profile digest", mutate: func(value *ResolvedBundleIdentityV1) { value.RequirementProfileDigest = "bad" }, want: "profile digest"},
		{name: "upstream", mutate: func(value *ResolvedBundleIdentityV1) { value.Upstream.RootFSSubject = "bad" }, want: "rootfs"},
		{name: "artifact", mutate: func(value *ResolvedBundleIdentityV1) { value.Artifacts[0].LogicalPath = "../demo.whl" }, want: "artifact"},
		{name: "output node", mutate: func(value *ResolvedBundleIdentityV1) { value.Outputs[0].SupplierNode = "python/other" }, want: "supplier node"},
		{name: "output path", mutate: func(value *ResolvedBundleIdentityV1) { value.Outputs[0].Candidate.InvocationPath = "bin/demo" }, want: "absolute Linux path"},
		{name: "owner schema", mutate: func(value *ResolvedBundleIdentityV1) { value.ProviderPayload.Schema = "unknown" }, want: "provider-owned data"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneBundlePayloadForTest(valid)
			test.mutate(&candidate)
			if err := ValidateResolvedBundlePayload(candidate, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateResolvedBundleRequiresCanonicalOrdering(t *testing.T) {
	payload := validPythonBundlePayload()
	payload.Artifacts = append(payload.Artifacts,
		providerstore.ArtifactDescriptor{LogicalPath: "a.whl", Kind: "wheel", Size: "1", SHA256: testDigest("6")},
	)
	if err := ValidateResolvedBundlePayload(payload, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("artifact order error = %v", err)
	}
	payload = validPythonBundlePayload()
	payload.Outputs = append(payload.Outputs, ResolvedOutput{
		SupplierComponent: "application", SupplierNode: "python/application", Name: "alpha",
		Candidate: ExecutableCandidate{InvocationPath: "/opt/reploy/python/application/bin/alpha", Provenance: providerData("python-console-script-v1")},
	})
	if err := ValidateResolvedBundlePayload(payload, acceptTestBundleOwner); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("output order error = %v", err)
	}
}

func cloneBundlePayloadForTest(payload ResolvedBundleIdentityV1) ResolvedBundleIdentityV1 {
	result := payload
	result.Artifacts = append([]providerstore.ArtifactDescriptor{}, payload.Artifacts...)
	result.Outputs = append([]ResolvedOutput{}, payload.Outputs...)
	return result
}
