package deploy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func validBuildLock(t *testing.T) BuildLockV1 {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	config := buildLockTestDigest("2")
	base := ImageDescriptor{
		Schema: ImageDescriptorSchemaV1, Platform: platform, AuthorReference: "local-base", ImmutableReference: string(config),
		ConfigDigest: config, RootFSDiffIDs: []canonical.Digest{buildLockTestDigest("3")},
	}
	baseRootFS, err := RootFSSubject(base.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	return BuildLockV1{
		Schema: BuildLockSchemaV1, BlueprintDigest: buildLockTestDigest("0"), Overlay: EmptyRequestOverlayV1(),
		ResolvedRequestDigest: buildLockTestDigest("1"), Platform: platform,
		Base:  base,
		Graph: ProviderGraphLockV1{Nodes: []providers.NodeID{"base"}, Edges: []providers.ProviderEdgeV1{}},
		Nodes: []NodeLockV1{}, RuntimePolicy: validRuntimePolicy(),
		ValidationRecord: providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: buildLockTestDigest("4")},
		FinalImage:       providers.RealizedImageV1{Digest: buildLockTestDigest("5"), ConfigDigest: buildLockTestDigest("5"), RootFSSubject: baseRootFS},
	}
}

func buildLockTestDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func acceptBuildLockProfile(providers.RequirementProfile) error { return nil }

func addValidAPTNode(t *testing.T, lock *BuildLockV1) {
	t.Helper()
	profile := providers.RequirementProfile{
		Schema: providers.RequirementProfileSchemaV1,
		Declaration: providers.RequirementDeclaration{
			Executables: []providers.ExecutableRequirement{}, Files: []providers.FileRequirement{},
			ProviderData: canonical.Envelope{Schema: "apt-requirements-v1", Value: canonical.Object{}},
		},
		SelectedExecutables: []providers.ExecutableEvidence{}, SelectedFiles: []providers.FileEvidence{}, Platform: lock.Platform,
		Facts: canonical.Envelope{Schema: "apt-facts-v1", Value: canonical.Object{}},
	}
	profileDigest, err := providers.RequirementProfileDigest(profile, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	baseRootFS, err := RootFSSubject(lock.Base.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	baseDigest := lock.Base.ManifestDigest
	if baseDigest == "" {
		baseDigest = lock.Base.ConfigDigest
	}
	upstream := providers.RealizedImageV1{Digest: baseDigest, ConfigDigest: lock.Base.ConfigDigest, RootFSSubject: baseRootFS}
	evidence, err := providers.NewValidationEvidence(upstream.RootFSSubject, profileDigest)
	if err != nil {
		t.Fatal(err)
	}
	lock.Graph.Nodes = []providers.NodeID{"apt", "base"}
	result := providers.RealizedImageV1{Digest: buildLockTestDigest("d"), ConfigDigest: buildLockTestDigest("d"), RootFSSubject: buildLockTestDigest("e")}
	lock.Nodes = []NodeLockV1{{
		NodeID: "apt", Provider: blueprint.ComponentTypeAPT, PlanDigest: buildLockTestDigest("9"), ResolverCacheKey: buildLockTestDigest("a"),
		RequirementProfile: profile, ValidationEvidence: evidence,
		BundleManifest:    providerstore.StoreObjectRef{Kind: providerstore.BundleManifestKind, Digest: buildLockTestDigest("b")},
		TransactionDigest: buildLockTestDigest("c"), Upstream: upstream,
		Result:  result,
		Outputs: []providers.RealizedOutput{},
	}}
	lock.FinalImage.RootFSSubject = result.RootFSSubject
}

func TestBuildLockV1CanonicalRoundTripAndIdentity(t *testing.T) {
	lock := validBuildLock(t)
	content, err := EncodeBuildLockV1(lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBuildLockV1(content, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeBuildLockV1(decoded, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, reencoded) {
		t.Fatalf("build lock encoding changed:\n%s\n%s", content, reencoded)
	}
	first, err := BuildLockDigestV1(lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildLockDigestV1(decoded, acceptBuildLockProfile)
	if err != nil || first != second {
		t.Fatalf("build lock identities = %q, %q; error = %v", first, second, err)
	}
}

func TestBuildLockV1AcceptsValidatedProviderNode(t *testing.T) {
	lock := validBuildLock(t)
	addValidAPTNode(t, &lock)
	if _, err := BuildLockDigestV1(lock, acceptBuildLockProfile); err != nil {
		t.Fatal(err)
	}
	lock.Nodes[0].ValidationEvidence.SubjectRootFS = buildLockTestDigest("f")
	if _, err := BuildLockDigestV1(lock, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "upstream root filesystem") {
		t.Fatalf("upstream evidence error = %v", err)
	}
}

func TestBuildLockV1RejectsDisconnectedImageLineage(t *testing.T) {
	t.Run("provider upstream", func(t *testing.T) {
		lock := validBuildLock(t)
		addValidAPTNode(t, &lock)
		lock.Nodes[0].Upstream.Digest = buildLockTestDigest("7")
		if _, err := BuildLockDigestV1(lock, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), `node "apt" upstream image`) {
			t.Fatalf("lineage error = %v", err)
		}
	})
	t.Run("final root filesystem", func(t *testing.T) {
		lock := validBuildLock(t)
		lock.FinalImage.RootFSSubject = buildLockTestDigest("6")
		if _, err := BuildLockDigestV1(lock, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "does not match the final graph prefix") {
			t.Fatalf("lineage error = %v", err)
		}
	})
}

func TestBuildLockV1RejectsInvalidNestedIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BuildLockV1)
		want   string
	}{
		{name: "schema", mutate: func(value *BuildLockV1) { value.Schema = "lock-v2" }, want: "schema"},
		{name: "base platform", mutate: func(value *BuildLockV1) { value.Base.Platform.Architecture = "arm64" }, want: "build lock base"},
		{name: "nil graph", mutate: func(value *BuildLockV1) { value.Graph.Nodes = nil }, want: "graph"},
		{name: "missing base", mutate: func(value *BuildLockV1) { value.Graph.Nodes = []providers.NodeID{} }, want: "base"},
		{name: "missing node lock", mutate: func(value *BuildLockV1) { value.Graph.Nodes = []providers.NodeID{"apt", "base"} }, want: "missing node"},
		{name: "runtime policy", mutate: func(value *BuildLockV1) { value.RuntimePolicy.Schema = "bad" }, want: "runtime policy"},
		{name: "validation kind", mutate: func(value *BuildLockV1) { value.ValidationRecord.Kind = providerstore.BlobKind }, want: "validation-record"},
		{name: "final image", mutate: func(value *BuildLockV1) { value.FinalImage.RootFSSubject = "bad" }, want: "final image"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lock := validBuildLock(t)
			test.mutate(&lock)
			_, err := BuildLockDigestV1(lock, acceptBuildLockProfile)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestProviderGraphLockRejectsCycles(t *testing.T) {
	graph := ProviderGraphLockV1{
		Nodes: []providers.NodeID{"apt", "base", "python/app"},
		Edges: []providers.ProviderEdgeV1{
			{Supplier: "apt", Consumer: "python/app", RequirementID: "python", Output: providers.QualifiedOutput{Component: "system", Name: "python"}},
			{Supplier: "python/app", Consumer: "apt", RequirementID: "tool", Output: providers.QualifiedOutput{Component: "app", Name: "tool"}},
		},
	}
	if _, err := validateProviderGraphLock(graph); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestDecodeBuildLockV1RejectsUnknownAndNoncanonicalJSON(t *testing.T) {
	content, err := EncodeBuildLockV1(validBuildLock(t), acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := bytes.Replace(content, []byte(`"schema":"lock-v1"`), []byte(`"schema":"lock-v1","unknown":true`), 1)
	if _, err := DecodeBuildLockV1(withUnknown, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown-field error = %v", err)
	}
	withWhitespace := append([]byte(" "), content...)
	if _, err := DecodeBuildLockV1(withWhitespace, acceptBuildLockProfile); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical error = %v", err)
	}
}
