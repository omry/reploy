package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providerstore"
)

func TestPrepareMaterializationContextHardlinksVerifiedStoreArtifacts(t *testing.T) {
	deployment := t.TempDir()
	store, err := providerstore.NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	script, err := store.Publish(context.Background(), "scripts/python-web.sh", "script", strings.NewReader("#!/bin/sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	wheel, err := store.Publish(context.Background(), "wheels/demo.whl", "wheel", strings.NewReader("wheel bytes"))
	if err != nil {
		t.Fatal(err)
	}
	transaction := rendererTransaction()
	transaction.Script = script
	transaction.Mounts[0].SourceDigest = script.SHA256
	transaction.Mounts[1].SourceDigest = rendererDigest("5")
	inputs := []MaterializationMountInput{
		{ID: "script", SourceDigest: script.SHA256, Files: []MaterializationMountFile{{RelativePath: "python-web.sh", Artifact: script}}},
		{ID: "wheels", SourceDigest: rendererDigest("5"), Files: []MaterializationMountFile{{RelativePath: "demo.whl", Artifact: wheel}}},
	}
	prepared, cleanup, err := PrepareMaterializationContext(store, transaction, inputs)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(prepared.Sources) != 2 || prepared.Sources[0].ContextPath != "mounts/script" || prepared.Sources[1].ContextPath != "mounts/wheels" {
		t.Fatalf("sources = %#v", prepared.Sources)
	}
	for _, test := range []struct {
		path string
	}{
		{path: filepath.Join(prepared.Dir, "mounts", "script", "python-web.sh")},
		{path: filepath.Join(prepared.Dir, "mounts", "wheels", "demo.whl")},
	} {
		info, err := os.Stat(test.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() == false {
			t.Fatalf("staged path is not regular: %s", test.path)
		}
	}
	sourceInfo, err := os.Stat(mustBlobPath(t, store, wheel))
	if err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Stat(filepath.Join(prepared.Dir, "mounts", "wheels", "demo.whl"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, targetInfo) {
		t.Fatal("staged wheel was copied instead of hardlinked")
	}
	workspace := filepath.Dir(prepared.Dir)
	cleanup()
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists after cleanup: %v", err)
	}
}

func TestPrepareMaterializationContextRejectsCorruptBlob(t *testing.T) {
	deployment := t.TempDir()
	store, err := providerstore.NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	script, err := store.Publish(context.Background(), "scripts/python-web.sh", "script", strings.NewReader("#!/bin/sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	wheel, err := store.Publish(context.Background(), "wheels/demo.whl", "wheel", strings.NewReader("wheel bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mustBlobPath(t, store, wheel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mustBlobPath(t, store, wheel), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction := rendererTransaction()
	transaction.Script = script
	transaction.Mounts[0].SourceDigest = script.SHA256
	inputs := []MaterializationMountInput{
		{ID: "script", SourceDigest: script.SHA256, Files: []MaterializationMountFile{{RelativePath: "python-web.sh", Artifact: script}}},
		{ID: "wheels", SourceDigest: transaction.Mounts[1].SourceDigest, Files: []MaterializationMountFile{{RelativePath: "demo.whl", Artifact: wheel}}},
	}
	if _, cleanup, err := PrepareMaterializationContext(store, transaction, inputs); err == nil || !strings.Contains(err.Error(), "verify") {
		cleanup()
		t.Fatalf("error = %v", err)
	}
}

func mustBlobPath(t *testing.T, store providerstore.Store, artifact providerstore.ArtifactDescriptor) string {
	t.Helper()
	path, err := store.BlobPath(artifact.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
