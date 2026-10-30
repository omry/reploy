package dockerdeploy

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func mappedEntryStateFixtureV1(t *testing.T, kind string) deploy.StateV1 {
	t.Helper()
	current, _ := runtimeCurrentBuildFixture(t)
	state := identityGuardDocumentV1(t, current.State, false, false)
	switch kind {
	case "ordinary":
	case "declaration":
		state = identityGuardDocumentV1(t, state, true, false)
	case "unresolved":
		state = identityGuardDocumentV1(t, state, true, true)
	case "policy":
		state = policyGuardStateV1(t, state)
	case "retained":
		// A declaration removed from the blueprint cannot erase retained
		// mapping authority, even if the remaining state is incomplete.
		state.IdentityMapping = &deploy.IdentityMappingStateV1{}
	default:
		t.Fatalf("unknown entry fixture %q", kind)
	}
	return state
}

func TestMappedEntryModesV1(t *testing.T) {
	for _, kind := range []string{"ordinary", "declaration", "unresolved", "policy", "retained"} {
		t.Run(kind, func(t *testing.T) {
			state := mappedEntryStateFixtureV1(t, kind)
			before, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			wantDocument, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []mappedEntryModeV1{mappedEntryFreshInstanceV1, mappedEntryOccupiedExecV1, mappedEntryHostNativeV1} {
				document, err := runtimeDocumentForEntryModeV1(state, mode)
				if kind == "ordinary" || mode == mappedEntryFreshInstanceV1 {
					if err != nil || !reflect.DeepEqual(document, wantDocument) {
						t.Fatalf("mode %d changed ordinary document or fresh classification: %v", mode, err)
					}
				} else {
					want := errMappedOccupiedEntryV1
					if mode == mappedEntryHostNativeV1 {
						want = errMappedHostNativeEntryV1
					}
					if !errors.Is(err, want) || !reflect.DeepEqual(document, blueprint.Document{}) {
						t.Fatalf("mode %d returned document or wrong refusal: %v", mode, err)
					}
				}
			}
			after, err := json.Marshal(state)
			if err != nil || string(before) != string(after) {
				t.Fatal("entry classification changed owner state", err)
			}
			// Fresh classification cannot bypass the unchanged public guard.
			_, err = unmappedRuntimeDocumentV1(state)
			if (err == nil) != (kind == "ordinary") {
				t.Fatalf("public guard changed for %s: %v", kind, err)
			}
		})
	}
}

func TestMappedEntryInvalidModeAndBlueprintV1(t *testing.T) {
	for _, kind := range []string{"ordinary", "retained", "policy"} {
		state := mappedEntryStateFixtureV1(t, kind)
		for _, mode := range []mappedEntryModeV1{0, 4, 255} {
			if _, err := runtimeDocumentForEntryModeV1(state, mode); err == nil || !strings.Contains(err.Error(), "unsupported runtime entry mode") {
				t.Fatalf("unknown mode %d accepted: %v", mode, err)
			}
		}
		state.Blueprint = blueprint.ResolvedDocumentV1("")
		for _, mode := range []mappedEntryModeV1{mappedEntryFreshInstanceV1, mappedEntryOccupiedExecV1, mappedEntryHostNativeV1} {
			if document, err := runtimeDocumentForEntryModeV1(state, mode); err == nil || !reflect.DeepEqual(document, blueprint.Document{}) {
				t.Fatalf("malformed owner blueprint returned a document in mode %d: %v", mode, err)
			}
		}
	}
}
