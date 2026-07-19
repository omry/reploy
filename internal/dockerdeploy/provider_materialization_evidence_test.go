package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestProviderMaterializationEvidenceRunnerCollectsOneCompleteBatch(t *testing.T) {
	transaction := rendererTransaction()
	resolvedOutput := providers.ResolvedOutput{
		SupplierComponent: "web", SupplierNode: transaction.NodeID, Name: "serve",
		Candidate: providers.ExecutableCandidate{
			InvocationPath: "/opt/reploy/providers/python/web/bin/serve",
			Provenance: providers.CanonicalProviderData{
				Schema: "python-console-script-v1", Value: canonical.Object{"distribution": "demo", "entry_point": "demo:main"},
			},
		},
	}
	bundle := providers.ResolvedBundle{Payload: providers.ResolvedBundleIdentityV1{Outputs: []providers.ResolvedOutput{resolvedOutput}}}
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	input := MaterializationEvidenceInput{
		Candidate:   InspectedMaterializationLayerCandidate{Image: InspectedImageCandidate{Descriptor: descriptor}},
		Transaction: transaction,
		Bundle:      bundle,
	}
	previous := collectMaterializationExecutableEvidence
	t.Cleanup(func() { collectMaterializationExecutableEvidence = previous })
	collectCalls := 0
	collectMaterializationExecutableEvidence = func(_ context.Context, _ providerstore.Store, gotDescriptor deploy.ImageDescriptor, checks []FullImageExecutableProbe) ([]providers.ExecutableEvidence, error) {
		collectCalls++
		if !reflect.DeepEqual(gotDescriptor, descriptor) || len(checks) != 2 || checks[0].ID != "generated_000000" || checks[1].ID != "output_000000" {
			t.Fatalf("descriptor = %#v; checks = %#v", gotDescriptor, checks)
		}
		result := make([]providers.ExecutableEvidence, 0, len(checks))
		for _, check := range checks {
			observation := directExecutableObservation(check.ID, check.InvocationPath)
			evidence, err := ExecutableEvidenceFromProbe(observation, check.Binding)
			if err != nil {
				return nil, err
			}
			result = append(result, evidence)
		}
		return result, nil
	}
	generated, outputs, err := (ProviderMaterializationEvidenceRunner{}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collectCalls != 1 || len(generated) != 1 || len(outputs) != 1 {
		t.Fatalf("collect = %d, generated = %#v, outputs = %#v", collectCalls, generated, outputs)
	}
	if err := providers.ValidateMaterializationGeneratedExecutables(transaction, generated); err != nil {
		t.Fatal(err)
	}
	if err := providers.ValidateRealizedBundleOutputs(bundle, outputs); err != nil {
		t.Fatal(err)
	}
	if generated[0].Evidence.Facts.Value["id"] != transaction.GeneratedExecutables[0].ID || outputs[0].Evidence.Facts.Schema != resolvedOutput.Candidate.Provenance.Schema {
		t.Fatalf("generated = %#v; output = %#v", generated[0], outputs[0])
	}
}

func directExecutableObservation(id string, invocationPath string) probe.ExecutableObservationV1 {
	digest := rendererDigest("a")
	access := []probe.AccessObservationV1{{Path: "/", Kind: "directory", Mode: "0755", UID: "0", GID: "0"}}
	current := ""
	parts := strings.Split(strings.TrimPrefix(invocationPath, "/"), "/")
	for index, part := range parts {
		current += "/" + part
		kind := "directory"
		if index == len(parts)-1 {
			kind = "regular"
		}
		access = append(access, probe.AccessObservationV1{Path: current, Kind: kind, Mode: "0755", UID: "0", GID: "0"})
	}
	return probe.ExecutableObservationV1{
		ID: id, InvocationPath: invocationPath, Links: []probe.LinkObservationV1{},
		Terminal: probe.FileObservationV1{
			Path: invocationPath, Kind: "regular", Mode: "0755", Size: "1", SHA256: digest, UID: "0", GID: "0",
		},
		Access: access,
	}
}
