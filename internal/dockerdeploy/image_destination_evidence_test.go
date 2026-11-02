package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sort"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

// Existing D controls exercise the same production reader and exporter, with
// no declared mount destinations. They retain their account-only test API.
func identityDestinationDocumentV1(policy *blueprint.IdentityPolicyV1) blueprint.Document {
	var document blueprint.Document
	document.Environment.Runtime.IdentityPolicy = policy
	return document
}
func readDockerImageIdentityArchiveV1(ctx context.Context, input io.Reader, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) (deploy.ImageIdentityEvidenceV1, error) {
	result, err := readDockerPreparedImageArchiveV1(ctx, input, identityDestinationDocumentV1(policy), image, platform)
	return result.Identity, err
}
func exportDockerImageIdentityV1(ctx context.Context, policy *blueprint.IdentityPolicyV1, image providers.RealizedImageV1, platform blueprint.Platform) (deploy.ImageIdentityEvidenceV1, error) {
	result, err := exportDockerPreparedImageV1(ctx, identityDestinationDocumentV1(policy), image, platform)
	return result.Identity, err
}

// Unit fixtures model an image with absent data destinations. Production facts
// are produced only by the fully verified immutable archive reader.
func emptyImageDestinationEvidenceV1(t *testing.T, document blueprint.Document, image providers.RealizedImageV1, platform blueprint.Platform) *deploy.ImageDestinationEvidenceV1 {
	t.Helper()
	digest, err := deploy.ImageDestinationSelectionDigestV1(document)
	if err != nil {
		t.Fatal(err)
	}
	e := &deploy.ImageDestinationEvidenceV1{Schema: deploy.ImageDestinationEvidenceSchemaV1, Image: image, Platform: platform, SelectionDigest: digest, RootWritable: document.Environment.ImageRootWritable(), Destinations: []deploy.ImageDestinationV1{}}
	names := []string{}
	for name := range document.Docker.Mounts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := document.Docker.Mounts[name].Contract.Target
		e.Destinations = append(e.Destinations, deploy.ImageDestinationV1{Name: name, Target: target, Resolved: target, Kind: "absent"})
	}
	return e
}

func TestImageDestinationEvidenceV1ReuseAndRebind(t *testing.T) {
	for _, change := range []string{"exact", "missing", "image", "root", "mount-source", "mount-target", "metadata", "fact", "invalid-fact-structure"} {
		t.Run(change, func(t *testing.T) {
			current, input := currentBuildReuseFixture(t)
			input.Document = buildIdentityDocumentV1(input.Document)
			mount := blueprint.EnvironmentMount{Target: "/data", Writable: true, UpdatePolicy: blueprint.UpdatePreserve}
			input.Document.Environment.Mounts = map[string]blueprint.EnvironmentMount{"data": mount}
			input.Document.Docker.Mounts = map[string]blueprint.DockerMount{"data": {Extends: "environment.mounts.data", Mode: blueprint.MountManagedBind, Source: "data", Contract: mount}}
			bindBuildIdentityFixtureV1(t, &current, input.Document)
			// The existing selected-byte, runtime-policy and generation checks remain in
			// place; these mutations must not be repaired by automatic projection rebind.
			switch change {
			case "missing":
				current.Lock.ImageDestinations = nil
			case "image":
				current.Lock.ImageDestinations.Image.RootFSSubject = current.Lock.FinalImage.ConfigDigest
			case "root":
				input.Document.Environment.Mounts["root"] = blueprint.EnvironmentMount{Target: "/", Writable: true}
			case "mount-source":
				m := input.Document.Docker.Mounts["data"]
				m.Source = "different"
				input.Document.Docker.Mounts["data"] = m
			case "mount-target":
				m := input.Document.Docker.Mounts["data"]
				m.Contract.Target = "/different"
				input.Document.Docker.Mounts["data"] = m
			case "metadata":
				m := input.Document.Environment.Mounts["data"]
				m.Permissions = "0750"
				input.Document.Environment.Mounts["data"] = m
				b := input.Document.Docker.Mounts["data"]
				b.Contract = m
				input.Document.Docker.Mounts["data"] = b
			case "fact":
				current.Lock.ImageDestinations.Destinations[0].Kind = "directory"
			case "invalid-fact-structure":
				current.Lock.ImageDestinations.Destinations[0].Kind = "invented" // Structure validation is independent of the enclosing lock digest.
			}
			if change == "exact" {
				rebound, err := rebindCurrentBuildLockV1(current, input.Document)
				if err != nil || !reflect.DeepEqual(rebound.ImageDestinations, current.Lock.ImageDestinations) {
					t.Fatal("exact projection rebind failed", err)
				}
				return
			}
			if change == "fact" {
				if _, err := CurrentBuildMatches(current, input); err == nil {
					t.Fatal("changed facts accepted under original generation digest")
				}
				return
			}
			if change == "invalid-fact-structure" {
				if err := deploy.ValidateImageDestinationEvidenceV1(current.Lock.ImageDestinations, current.Lock.FinalImage, current.Lock.Platform); err == nil {
					t.Fatal("unsupported fact kind accepted")
				}
				return
			}
			if _, err := rebindCurrentBuildLockV1(current, input.Document); err == nil {
				t.Fatal("missing or changed projection rebound")
			}
		})
	}
}

func TestImageDestinationEvidenceV1PublicationRefusesBeforeEffects(t *testing.T) {
	for _, validated := range []bool{false, true} {
		for _, change := range []string{"missing", "root", "selection", "extra"} {
			t.Run(fmt.Sprint(validated)+"/"+change, func(t *testing.T) {
				dir, operation, store, lock, state := currentBuildFixture(t, false)
				defer operation.Unlock()
				document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
				if err != nil {
					t.Fatal(err)
				}
				document = buildIdentityDocumentV1(document)
				current := CurrentBuild{State: state, Generation: *state.Current, Lock: lock}
				bindBuildIdentityFixtureV1(t, &current, document)
				switch change {
				case "missing":
					current.Lock.ImageDestinations = nil
				case "root":
					current.Lock.ImageDestinations.RootWritable = true
				case "selection":
					current.Lock.ImageDestinations.SelectionDigest = current.Lock.FinalImage.ConfigDigest
				case "extra":
					current.Lock.ImageDestinations.Destinations = append(current.Lock.ImageDestinations.Destinations, deploy.ImageDestinationV1{Name: "extra", Target: "/extra", Resolved: "/extra", Kind: "absent"})
				}
				before := identityGuardTreeV1(t, dir)
				effects := 0
				if validated {
					inputs, inputErr := ValidatedBuildInputs(document, state.Overlay, deploy.EmptyPackageOverridesV1("demo"), dir, state.Platform)
					if inputErr != nil {
						t.Fatal(inputErr)
					}
					_, err = publishValidatedBuild(t.Context(), operation, store, "demo", dir, current.Lock, inputs, publishValidatedBuildBackendV1{newReferences: func(string, string) (EnvironmentImageReferences, error) {
						effects++
						return EnvironmentImageReferences{}, fmt.Errorf("unreachable")
					}})
				} else {
					_, err = publishBuild(t.Context(), operation, store, BuildPublicationInput{Environment: "demo", DeploymentDir: dir, Document: document, Lock: current.Lock}, buildPublicationBackend{newReferences: func(string, string) (EnvironmentImageReferences, error) {
						effects++
						return EnvironmentImageReferences{}, fmt.Errorf("unreachable")
					}})
				}
				if err == nil || effects != 0 || !reflect.DeepEqual(before, identityGuardTreeV1(t, dir)) {
					t.Fatalf("refusal changed deployment or reached engine: effects=%d err=%v", effects, err)
				}
			})
		}
	}
}
