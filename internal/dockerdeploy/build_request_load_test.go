package dockerdeploy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func TestLoadBuildRequestV1UsesOnlyLockedDesiredInputs(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	document := resolvedRequestTestDocument()
	platform := document.Blueprint.Compatibility.Platforms[0]
	payload := testResolvedBlueprintV1(t, document)
	overlay := deploy.AddOverlayOptions(deploy.EmptyRequestOverlayV1(), []deploy.QualifiedOption{{Application: "application", Option: "debug"}})
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: payload, Platform: platform, Overlay: overlay,
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	source := testPythonResolvedSource(
		"application/application/python", "demo", "1.0",
		canonical.Digest("sha256:"+strings.Repeat("a", 64)), canonical.Digest("sha256:"+strings.Repeat("b", 64)),
	)
	loaded, err := LoadBuildRequestV1(operation, []providers.ResolvedSourceInput{source})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.State, state) || !reflect.DeepEqual(loaded.Document, document) ||
		loaded.Request.Platform != platform ||
		len(loaded.Request.Sources) != 1 || !reflect.DeepEqual(loaded.Request.Sources[0], source) {
		t.Fatalf("loaded build request = %#v", loaded)
	}
	if len(loaded.Request.Components) != 3 {
		t.Fatalf("resolved components = %#v", loaded.Request.Components)
	}
	if _, err := LoadBuildRequestV1(operation, nil); err == nil || !strings.Contains(err.Error(), "array") {
		t.Fatalf("nil sources error = %v", err)
	}
}

func TestLoadBuildRequestV1DoesNotInterpretCurrentLock(t *testing.T) {
	dir := t.TempDir()
	_, lock := publicationLockFixture(t, dir, "4", "5", "6")
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	lockDigest, err := operation.PublishBuildLock(lock, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	document := resolvedRequestTestDocument()
	state := deploy.StateV1{
		Schema: deploy.StateSchemaV1, Blueprint: testResolvedBlueprintV1(t, document),
		Platform: lock.Platform, Overlay: deploy.EmptyRequestOverlayV1(),
		Current: &deploy.EnvironmentGenerationState{
			Reference: "reploy/env/demo-deadbeef:g-current", ImageDigest: lock.FinalImage.Digest,
			RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: lockDigest,
			Platform: lock.Platform, RuntimePolicyDigest: policyDigest,
		},
	}
	if err := operation.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ".reploy", "locks", strings.Replace(string(lockDigest), ":", "-", 1)+".json")); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBuildRequestV1(operation, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.State, state) {
		t.Fatalf("loaded state = %#v, want %#v", loaded.State, state)
	}
}

func TestLoadBuildRequestV1RejectsMissingAndLegacyState(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBuildRequestV1(operation, []providers.ResolvedSourceInput{}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing state error = %v", err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, ".reploy")
	legacy := []byte(`{"schema_version":1,"blueprint":{"raw":"file:secret"}}`)
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	operation, err = deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if _, err := LoadBuildRequestV1(operation, []providers.ResolvedSourceInput{}); !errors.Is(err, deploy.ErrLegacyStateUnsupported) {
		t.Fatalf("legacy state error = %v", err)
	}
	after, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, legacy) {
		t.Fatal("legacy state changed")
	}
}
