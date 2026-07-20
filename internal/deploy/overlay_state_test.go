package deploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func writeOverlayTestState(t *testing.T, dir string) string {
	t.Helper()
	stateDir := filepath.Join(dir, ".reploy")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := blueprint.EncodeResolvedDocumentV1(overlayTestDocument())
	if err != nil {
		t.Fatal(err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	current := &EnvironmentGenerationState{
		Reference: "reploy/env/overlay-test:g-current", ImageDigest: digest,
		RootFSSubject: digest, BuildLockDigest: digest, Platform: platform, RuntimePolicyDigest: digest,
	}
	state := StateV1{
		Schema: StateSchemaV1, Blueprint: resolved, Platform: platform,
		Overlay: EmptyRequestOverlayV1(), Current: current,
	}
	content, err := EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(path, content, 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func readOverlayTestState(t *testing.T, path string) StateV1 {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := DecodeStateV1(content)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestMutateRequestOverlayV1WritesOneValidatedStateTransaction(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	result, err := mutateRequestOverlayV1(context.Background(), dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		overlay.SelectedOptions = append(overlay.SelectedOptions, QualifiedOption{Component: "app", Option: "debug"})
		return overlay, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Digest == "" || !reflect.DeepEqual(result.Overlay.SelectedOptions, []QualifiedOption{{Component: "app", Option: "debug"}}) {
		t.Fatalf("result = %#v", result)
	}
	state := readOverlayTestState(t, statePath)
	if !reflect.DeepEqual(state.Overlay, result.Overlay) {
		t.Fatalf("persisted overlay = %#v, want %#v", state.Overlay, result.Overlay)
	}
	if state.Blueprint == "" || state.Current == nil || state.Current.Reference != "reploy/env/overlay-test:g-current" {
		t.Fatalf("state identity changed unexpectedly: %#v", state)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, "requirements.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlay mutation projected requirements: %v", err)
	}
}

func TestMutateRequestOverlayV1NoopDoesNotRewriteState(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := mutateRequestOverlayV1(context.Background(), dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		return overlay, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatalf("result = %#v", result)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("no-op mutation rewrote state")
	}
}

func TestMutateRequestOverlayV1FailureLeavesStateUnchanged(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mutateRequestOverlayV1(context.Background(), dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		overlay.SelectedOptions = append(overlay.SelectedOptions,
			QualifiedOption{Component: "app", Option: "debug"},
			QualifiedOption{Component: "app", Option: "missing"},
		)
		return overlay, nil
	})
	if err == nil || !strings.Contains(err.Error(), "missing option") {
		t.Fatalf("error = %v", err)
	}
	after, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("failed multi-change mutated state")
	}
}

func TestMutateRequestOverlayV1RejectsLegacyStateWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".reploy")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "state.json")
	legacy := []byte(`{"schema_version":1,"bundle":{"prepared_fingerprint":"old"}}`)
	if err := os.WriteFile(path, legacy, 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := MutateRequestOverlayV1(context.Background(), dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		return overlay, nil
	})
	if !errors.Is(err, ErrLegacyStateUnsupported) {
		t.Fatalf("legacy mutation error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, legacy) {
		t.Fatal("legacy state was mutated")
	}
}

func TestMutateRequestOverlayV1ReplaceFailurePreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	originalReplace := replaceAtomicStateFile
	replaceAtomicStateFile = func(string, string) error { return errors.New("injected replace failure") }
	t.Cleanup(func() { replaceAtomicStateFile = originalReplace })
	_, err = mutateRequestOverlayV1(context.Background(), dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		overlay.SelectedOptions = append(overlay.SelectedOptions, QualifiedOption{Component: "app", Option: "debug"})
		return overlay, nil
	})
	if err == nil || !strings.Contains(err.Error(), "injected replace failure") {
		t.Fatalf("error = %v", err)
	}
	after, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("failed atomic replace changed original state")
	}
	temporary, err := filepath.Glob(filepath.Join(dir, ".reploy", ".state-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary state files remain: %#v", temporary)
	}
}

func TestMutateRequestOverlayV1HonorsOperationLock(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireOperationLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = mutateRequestOverlayV1(ctx, dir, overlayTestPackageValidator, func(_ blueprint.Document, overlay RequestOverlayV1) (RequestOverlayV1, error) {
		return overlay, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	after, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("contended transaction changed state")
	}
}
