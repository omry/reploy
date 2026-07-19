package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func mountInputFixture() (providers.ResolvedBundle, providers.MaterializationTransaction, providerstore.ArtifactDescriptor) {
	transaction := rendererTransaction()
	transaction.Script.LogicalPath = "scripts/python-web.sh"
	transaction.Argv[2].RelativePath = transaction.Script.LogicalPath
	wheel := providerstore.ArtifactDescriptor{
		LogicalPath: "wheels/hydra.whl", Kind: "wheel", Size: "200", SHA256: rendererDigest("6"),
	}
	transaction.Argv[6].RelativePath = wheel.LogicalPath
	bundle := providers.ResolvedBundle{
		Identity: transaction.Mounts[1].SourceDigest,
		Payload: providers.ResolvedBundleIdentityV1{
			NodeID:    transaction.NodeID,
			Artifacts: []providerstore.ArtifactDescriptor{transaction.Script, wheel},
		},
	}
	return bundle, transaction, wheel
}

func TestMaterializationMountInputsMapsLogicalArtifactsWithoutStorePaths(t *testing.T) {
	bundle, transaction, wheel := mountInputFixture()
	inputs, err := MaterializationMountInputs(bundle, transaction)
	if err != nil {
		t.Fatal(err)
	}
	want := []MaterializationMountInput{
		{ID: "script", SourceDigest: transaction.Script.SHA256, Files: []MaterializationMountFile{{RelativePath: transaction.Script.LogicalPath, Artifact: transaction.Script}}},
		{ID: "wheels", SourceDigest: bundle.Identity, Files: []MaterializationMountFile{{RelativePath: wheel.LogicalPath, Artifact: wheel}}},
	}
	if !reflect.DeepEqual(inputs, want) {
		t.Fatalf("inputs = %#v, want %#v", inputs, want)
	}
}

func TestBindVerifiedMaterializationArtifactsBindsOnlyBundleMembers(t *testing.T) {
	bundle, transaction, wheel := mountInputFixture()
	inputs, err := MaterializationMountInputs(bundle, transaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := bindVerifiedMaterializationArtifacts(inputs, map[canonical.Digest]string{wheel.SHA256: "/store/tmp/verified/wheel.whl"}); err != nil {
		t.Fatal(err)
	}
	if inputs[1].Files[0].verifiedPath != "/store/tmp/verified/wheel.whl" || inputs[0].Files[0].verifiedPath != "" {
		t.Fatalf("bound inputs = %#v", inputs)
	}
	if err := bindVerifiedMaterializationArtifacts(inputs, map[canonical.Digest]string{rendererDigest("f"): "/store/tmp/other"}); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("unbound verified artifact error = %v", err)
	}
}

func TestMaterializationMountInputsRejectsOpenArtifactMappings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*providers.ResolvedBundle, *providers.MaterializationTransaction)
		want   string
	}{
		{name: "node", mutate: func(bundle *providers.ResolvedBundle, _ *providers.MaterializationTransaction) {
			bundle.Payload.NodeID = "python/other"
		}, want: "does not match"},
		{name: "unknown artifact", mutate: func(_ *providers.ResolvedBundle, transaction *providers.MaterializationTransaction) {
			transaction.Argv[6].RelativePath = "wheels/missing.whl"
		}, want: "absent"},
		{name: "duplicate reference", mutate: func(_ *providers.ResolvedBundle, transaction *providers.MaterializationTransaction) {
			transaction.Argv = append(transaction.Argv, transaction.Argv[6])
		}, want: "more than once"},
		{name: "wrong bundle digest", mutate: func(_ *providers.ResolvedBundle, transaction *providers.MaterializationTransaction) {
			transaction.Mounts[1].SourceDigest = rendererDigest("7")
		}, want: "bundle identity"},
		{name: "unused artifact", mutate: func(bundle *providers.ResolvedBundle, _ *providers.MaterializationTransaction) {
			bundle.Payload.Artifacts = append(bundle.Payload.Artifacts, providerstore.ArtifactDescriptor{
				LogicalPath: "wheels/unused.whl", Kind: "wheel", Size: "10", SHA256: rendererDigest("8"),
			})
		}, want: "does not account"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle, transaction, _ := mountInputFixture()
			test.mutate(&bundle, &transaction)
			if _, err := MaterializationMountInputs(bundle, transaction); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
