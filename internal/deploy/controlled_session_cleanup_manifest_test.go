package deploy

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestControlledSessionCleanupManifestDerivesExactDurableOwnership(t *testing.T) {
	ownership := controlledSessionOwnershipFixtureV1(t.TempDir(), "run-0000000000000001", "reploy/env/workload:g-current")
	ownership.BootSession = "boot-session"
	ownership.NetworkID = strings.Repeat("d", 64)
	ownership.NetworkName = "reploy-session-network"
	manifest, err := ControlledSessionCleanupManifestFromOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.LiveRunID != ownership.LiveRunID || manifest.BootSession != ownership.BootSession ||
		manifest.DockerEndpoint != ownership.DockerEndpoint || manifest.ChannelDirectory != ownership.ChannelDirectory || manifest.Controller != ownership.Controller ||
		manifest.Workload != ownership.Workload || len(manifest.Networks) != 1 || len(manifest.Volumes) != 0 {
		t.Fatalf("cleanup manifest = %#v", manifest)
	}
	wantNetwork := ControlledSessionNetworkOwnershipV1{
		Role: ControlledSessionNetworkRoleV1, ID: ownership.NetworkID, Name: ownership.NetworkName,
	}
	if manifest.Networks[0] != wantNetwork {
		t.Fatalf("cleanup manifest network = %#v, want %#v", manifest.Networks[0], wantNetwork)
	}
	wantReceipt := filepath.Join(filepath.Dir(filepath.Dir(ownership.ChannelDirectory)), "incidents", ownership.LiveRunID+".json")
	if manifest.IncidentReceipt != wantReceipt {
		t.Fatalf("incident receipt = %q, want %q", manifest.IncidentReceipt, wantReceipt)
	}
	content, err := EncodeControlledSessionCleanupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte(ownership.SessionHandle)) || bytes.Contains(content, []byte(`"schema"`)) {
		t.Fatalf("cleanup manifest includes unnecessary protocol or version authority: %s", content)
	}
	decoded, err := DecodeControlledSessionCleanupManifest(content)
	if err != nil || !reflect.DeepEqual(decoded, manifest) {
		t.Fatalf("decoded manifest = %#v, error=%v", decoded, err)
	}
}

func TestControlledSessionCleanupManifestRejectsPlannedNetworkWithoutExactID(t *testing.T) {
	ownership := controlledSessionOwnershipFixtureV1(t.TempDir(), "run-0000000000000001", "reploy/env/workload:g-current")
	ownership.BootSession = "boot-session"
	ownership.NetworkName = "reploy-session-network"
	if _, err := ControlledSessionCleanupManifestFromOwnership(ownership); err == nil || !strings.Contains(err.Error(), "network ID") {
		t.Fatalf("incomplete network ownership error = %v", err)
	}
}

func TestControlledSessionCleanupManifestRejectsRemoteDockerEndpoint(t *testing.T) {
	ownership := controlledSessionOwnershipFixtureV1(t.TempDir(), "run-0000000000000001", "reploy/env/workload:g-current")
	ownership.BootSession = "boot-session"
	ownership.DockerEndpoint = "tcp://builder.example:2376"
	if _, err := ControlledSessionCleanupManifestFromOwnership(ownership); err == nil || !strings.Contains(err.Error(), "local unix or npipe") {
		t.Fatalf("remote Docker endpoint error = %v", err)
	}
}

func TestControlledSessionCleanupManifestRejectsInvalidDurableOwnership(t *testing.T) {
	ownership := controlledSessionOwnershipFixtureV1(t.TempDir(), "run-0000000000000001", "reploy/env/workload:g-current")
	ownership.BootSession = "boot-session"
	ownership.Workload.ID = ownership.Controller.ID
	if _, err := ControlledSessionCleanupManifestFromOwnership(ownership); err == nil || !strings.Contains(err.Error(), "different containers") {
		t.Fatalf("invalid durable ownership error = %v", err)
	}
}

func TestControlledSessionCleanupManifestRequiresExactChannelAndCanonicalArrays(t *testing.T) {
	ownership := controlledSessionOwnershipFixtureV1(t.TempDir(), "run-0000000000000001", "reploy/env/workload:g-current")
	ownership.BootSession = "boot-session"
	manifest, err := ControlledSessionCleanupManifestFromOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ChannelDirectory = filepath.VolumeName(ownership.ChannelDirectory) + string(filepath.Separator)
	if err := ValidateControlledSessionCleanupManifest(manifest); err == nil || !strings.Contains(err.Error(), "private session directory") {
		t.Fatalf("broad channel directory error = %v", err)
	}
	manifest.ChannelDirectory = ownership.ChannelDirectory
	manifest.Networks = nil
	if err := ValidateControlledSessionCleanupManifest(manifest); err == nil || !strings.Contains(err.Error(), "must use arrays") {
		t.Fatalf("nil resources error = %v", err)
	}
	manifest.Networks = []ControlledSessionNetworkOwnershipV1{
		{Role: ControlledSessionNetworkRoleV1, ID: strings.Repeat("a", 64), Name: "network-a"},
		{Role: ControlledSessionNetworkRoleV1, ID: strings.Repeat("b", 64), Name: "network-b"},
	}
	if err := ValidateControlledSessionCleanupManifest(manifest); err == nil || !strings.Contains(err.Error(), "only one network") {
		t.Fatalf("multiple network resources error = %v", err)
	}
}

func TestDecodeControlledSessionCleanupManifestRejectsUnknownAndNoncanonicalJSON(t *testing.T) {
	ownership := controlledSessionOwnershipFixtureV1(t.TempDir(), "run-0000000000000001", "reploy/env/workload:g-current")
	ownership.BootSession = "boot-session"
	manifest, err := ControlledSessionCleanupManifestFromOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	content, err := EncodeControlledSessionCleanupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append(append([]byte{}, content[:len(content)-1]...), []byte(`,"extra":true}`)...)
	if _, err := DecodeControlledSessionCleanupManifest(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	pretty := append([]byte("\n"), content...)
	if _, err := DecodeControlledSessionCleanupManifest(pretty); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical error = %v", err)
	}
}
