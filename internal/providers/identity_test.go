package providers

import (
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func validResolvedRequest() ResolvedRequestV1 {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		panic(err)
	}
	return ResolvedRequestV1{
		Schema: ResolvedRequestSchemaV1, BlueprintDigest: testDigest("1"), OverlayDigest: testDigest("2"), Platform: platform,
		Components: []ResolvedComponentRequestV1{
			{Component: "application", Provider: blueprint.ComponentTypePython, Request: providerRequest(blueprint.ComponentTypePython, "python-provider-request-v1")},
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: providerRequest(blueprint.ComponentTypeBase, "base-provider-request-v1")},
		},
		Sources: []ResolvedSourceInput{{
			Schema: ResolvedSourceInputSchemaV1, Component: "application", LogicalPackage: "demo",
			SourceManifestDigest: testDigest("3"), BuilderProfile: "python-wheel-builder-v1",
			BuildSettings: providerData("python-build-settings-v1"), EcosystemMetadata: providerData("python-source-metadata-v1"), ArtifactDigest: testDigest("4"),
		}},
	}
}

func validateResolvedRequestOwner(request ResolvedRequestV1) error {
	if request.Components[0].Request.Schema != "python-provider-request-v1" || request.Sources[0].BuildSettings.Schema != "python-build-settings-v1" {
		return errors.New("unknown provider schema")
	}
	return nil
}

func TestResolvedRequestDigestBindsContentWithoutPaths(t *testing.T) {
	request := validResolvedRequest()
	first, err := ResolvedRequestDigest(request, validateResolvedRequestOwner)
	if err != nil {
		t.Fatal(err)
	}
	request.Sources[0].SourceManifestDigest = testDigest("5")
	second, err := ResolvedRequestDigest(request, validateResolvedRequestOwner)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("source manifest change did not change resolved request identity")
	}
	content, err := canonical.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(content)
	if strings.Contains(encoded, "/home/") || strings.Contains(encoded, "source_root") {
		t.Fatalf("resolved request contains a physical source locator: %s", encoded)
	}
}

func TestResolvedRequestRejectsMalformedRecords(t *testing.T) {
	valid := validResolvedRequest()
	tests := []struct {
		name   string
		mutate func(*ResolvedRequestV1)
		want   string
	}{
		{name: "schema", mutate: func(value *ResolvedRequestV1) { value.Schema = "resolved-request-v2" }, want: "schema"},
		{name: "component order", mutate: func(value *ResolvedRequestV1) {
			value.Components[0], value.Components[1] = value.Components[1], value.Components[0]
		}, want: "sorted"},
		{name: "request provider", mutate: func(value *ResolvedRequestV1) { value.Components[0].Request.Provider = blueprint.ComponentTypeAPT }, want: "does not match"},
		{name: "source target", mutate: func(value *ResolvedRequestV1) { value.Sources[0].Component = "missing" }, want: "missing or unsupported"},
		{name: "source schema", mutate: func(value *ResolvedRequestV1) { value.Sources[0].Schema = "source-v2" }, want: "source input schema"},
		{name: "source digest", mutate: func(value *ResolvedRequestV1) { value.Sources[0].ArtifactDigest = "bad" }, want: "artifact digest"},
		{name: "owner", mutate: func(value *ResolvedRequestV1) { value.Sources[0].BuildSettings.Schema = "unknown" }, want: "provider-owned"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneResolvedRequestForTest(valid)
			test.mutate(&candidate)
			if _, err := ResolvedRequestDigest(candidate, validateResolvedRequestOwner); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolverCacheKeyDigestIsNodeLocal(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	key := ResolverCacheKeyV1{
		Schema: ResolverCacheKeySchemaV1, NodeID: "python/application", RequestDigest: testDigest("6"), ProfileDigest: testDigest("7"), ResolverRecipe: "python-resolver-v1", Platform: platform,
	}
	first, err := ResolverCacheKeyDigest(key)
	if err != nil {
		t.Fatal(err)
	}
	key.ProfileDigest = testDigest("8")
	second, err := ResolverCacheKeyDigest(key)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("profile change did not invalidate resolver key")
	}
	key.NodeID = "base"
	if _, err := ResolverCacheKeyDigest(key); err == nil || !strings.Contains(err.Error(), "resolver node") {
		t.Fatalf("error = %v", err)
	}
}

func cloneResolvedRequestForTest(request ResolvedRequestV1) ResolvedRequestV1 {
	result := request
	result.Components = append([]ResolvedComponentRequestV1{}, request.Components...)
	result.Sources = append([]ResolvedSourceInput{}, request.Sources...)
	return result
}
