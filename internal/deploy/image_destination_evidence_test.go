package deploy

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func imageDestinationLockFixtureV1(t *testing.T) (blueprint.Document, BuildLockV1) {
	t.Helper()
	lock := identityPolicyBuildLockFixtureV1(t)
	var document blueprint.Document
	document.Environment.Runtime.IdentityPolicy = &lock.IdentityPolicy.Policy
	mount := blueprint.EnvironmentMount{Target: "/data", Writable: true, UpdatePolicy: blueprint.UpdatePreserve}
	document.Environment.Mounts = map[string]blueprint.EnvironmentMount{"data": mount}
	document.Docker.Mounts = map[string]blueprint.DockerMount{"data": {Extends: "environment.mounts.data", Mode: blueprint.MountManagedBind, Source: "data", Contract: mount}}
	digest, err := ImageDestinationSelectionDigestV1(document)
	if err != nil {
		t.Fatal(err)
	}
	lock.ImageDestinations = &ImageDestinationEvidenceV1{Schema: ImageDestinationEvidenceSchemaV1, Image: lock.FinalImage, Platform: lock.Platform, SelectionDigest: digest, Destinations: []ImageDestinationV1{{Name: "data", Target: "/data", Resolved: "/real-data", Kind: "directory"}}}
	return document, lock
}
func TestImageDestinationEvidenceV1CanonicalLockAndOmission(t *testing.T) {
	document, lock := imageDestinationLockFixtureV1(t)
	if err := ValidateSelectedImageDestinationsV1(document, lock); err != nil {
		t.Fatal(err)
	}
	before, err := EncodeBuildLockV1(lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBuildLockV1(before, acceptBuildLockProfile)
	if err != nil || !reflect.DeepEqual(decoded, lock) {
		t.Fatal("round trip", err)
	}
	originalDigest, err := BuildLockDigestV1(lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	lock.ImageDestinations.Destinations[0].ContainsContent = true
	changedDigest, err := BuildLockDigestV1(lock, acceptBuildLockProfile)
	if err != nil || changedDigest == originalDigest {
		t.Fatal("content fact is not bound to lock identity", err)
	}
	ordinary := validBuildLock(t)
	without, err := EncodeBuildLockV1(ordinary, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(without, []byte("image_destinations")) {
		t.Fatal("ordinary lock acquired field")
	}
	// Compare to the exact legacy object shape, without round-tripping through the
	// new Go struct: adding an omitted optional field must not alter wire bytes.
	var legacy map[string]any
	if err := json.Unmarshal(without, &legacy); err != nil {
		t.Fatal(err)
	}
	expected, err := canonical.Marshal(legacy)
	if err != nil || !bytes.Equal(expected, without) {
		t.Fatal("ordinary canonical bytes changed", err)
	}
}
func TestImageDestinationEvidenceV1ClosedStructure(t *testing.T) {
	for name, mutate := range map[string]func(*ImageDestinationEvidenceV1){
		"schema":                 func(e *ImageDestinationEvidenceV1) { e.Schema = "other" },
		"image":                  func(e *ImageDestinationEvidenceV1) { e.Image.Digest = buildLockTestDigest("e") },
		"config":                 func(e *ImageDestinationEvidenceV1) { e.Image.ConfigDigest = buildLockTestDigest("e") },
		"rootfs":                 func(e *ImageDestinationEvidenceV1) { e.Image.RootFSSubject = buildLockTestDigest("e") },
		"platform":               func(e *ImageDestinationEvidenceV1) { e.Platform.Architecture = "arm64" },
		"selection":              func(e *ImageDestinationEvidenceV1) { e.SelectionDigest = "bad" },
		"null inventory":         func(e *ImageDestinationEvidenceV1) { e.Destinations = nil },
		"duplicate":              func(e *ImageDestinationEvidenceV1) { e.Destinations = append(e.Destinations, e.Destinations[0]) },
		"name":                   func(e *ImageDestinationEvidenceV1) { e.Destinations[0].Name = "" },
		"target":                 func(e *ImageDestinationEvidenceV1) { e.Destinations[0].Target = "relative" },
		"resolved root":          func(e *ImageDestinationEvidenceV1) { e.Destinations[0].Resolved = "/" },
		"resolved traversal":     func(e *ImageDestinationEvidenceV1) { e.Destinations[0].Resolved = "/a/../data" },
		"resolved host spelling": func(e *ImageDestinationEvidenceV1) { e.Destinations[0].Resolved = `C:\data` },
		"kind":                   func(e *ImageDestinationEvidenceV1) { e.Destinations[0].Kind = "empty" },
		"absent content": func(e *ImageDestinationEvidenceV1) {
			e.Destinations[0].Kind = "absent"
			e.Destinations[0].ContainsContent = true
		},
		"non-directory empty": func(e *ImageDestinationEvidenceV1) { e.Destinations[0].Kind = "non-directory" },
	} {
		t.Run(name, func(t *testing.T) {
			_, lock := imageDestinationLockFixtureV1(t)
			mutate(lock.ImageDestinations)
			if err := ValidateBuildLockV1(lock, acceptBuildLockProfile); err == nil {
				t.Fatal("malformed projection accepted")
			}
		})
	}
	document, lock := imageDestinationLockFixtureV1(t)
	lock.IdentityPolicy = nil
	if err := ValidateBuildLockV1(lock, acceptBuildLockProfile); err == nil {
		t.Fatal("projection without mapped policy accepted")
	}
	document.Environment.Runtime.IdentityPolicy = nil
	if err := ValidateSelectedImageDestinationsV1(document, lock); err == nil {
		t.Fatal("ordinary selected blueprint accepted mapped projection")
	}
	_, lock = imageDestinationLockFixtureV1(t)
	raw, err := EncodeBuildLockV1(lock, acceptBuildLockProfile)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"kind":"directory"`), []byte(`"kind":"directory","trust_me":true`), 1)
	if _, err := DecodeBuildLockV1(raw, acceptBuildLockProfile); err == nil {
		t.Fatal("unknown nested projection field accepted")
	}
}
func TestImageDestinationEvidenceV1SelectionInventory(t *testing.T) {
	for _, change := range []string{"missing", "root", "selection", "target", "name", "extra", "fewer"} {
		t.Run(change, func(t *testing.T) {
			document, lock := imageDestinationLockFixtureV1(t)
			switch change {
			case "missing":
				lock.ImageDestinations = nil
			case "root":
				lock.ImageDestinations.RootWritable = true
			case "selection":
				lock.ImageDestinations.SelectionDigest = buildLockTestDigest("e")
			case "target":
				lock.ImageDestinations.Destinations[0].Target = "/different"
			case "name":
				lock.ImageDestinations.Destinations[0].Name = "other"
			case "extra":
				e := lock.ImageDestinations.Destinations[0]
				e.Name = "z"
				lock.ImageDestinations.Destinations = append(lock.ImageDestinations.Destinations, e)
			case "fewer":
				lock.ImageDestinations.Destinations = []ImageDestinationV1{}
			}
			if err := ValidateSelectedImageDestinationsV1(document, lock); err == nil {
				t.Fatal("stale selected projection accepted")
			}
		})
	}
}
