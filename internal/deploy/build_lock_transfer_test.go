package deploy

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providerstore"
)

func TestOperationLocksTransferOnlyVerifiedBuildStoreClosure(t *testing.T) {
	sourceDir, sourceStore, build, keepReference, dropReference := buildReachabilityFixture(t)
	destinationDir := t.TempDir()
	destinationStore, err := providerstore.NewStore(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	sourceLock, err := AcquireOperationLock(t.Context(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceLock.Unlock()
	destinationLock, err := AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	defer destinationLock.Unlock()

	closure, err := sourceLock.TransferBuildLockStoreClosure(
		t.Context(), destinationLock, sourceStore, destinationStore, build, acceptBuildLockProfile, acceptBuildLockBundle,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []providerstore.StoreObjectRef{keepReference, build.Nodes[0].BundleManifest, build.ValidationRecord}
	if !reflect.DeepEqual(closure, want) {
		t.Fatalf("transferred closure = %#v, want %#v", closure, want)
	}
	if _, err := BuildLockStoreClosure(build, destinationStore, acceptBuildLockProfile, acceptBuildLockBundle); err != nil {
		t.Fatalf("destination closure: %v", err)
	}
	dropPath, err := destinationStore.BlobPath(dropReference.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dropPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced source artifact transferred: %v", err)
	}
	if err := sourceLock.RequireHeld(); err != nil {
		t.Fatalf("source lock released by transfer: %v", err)
	}
	if err := destinationLock.RequireHeld(); err != nil {
		t.Fatalf("destination lock released by transfer: %v", err)
	}
}

func TestOperationLocksRejectCorruptSourceArtifactWithoutPublishingIt(t *testing.T) {
	sourceDir, sourceStore, build, keepReference, _ := buildReachabilityFixture(t)
	keepPath, err := sourceStore.BlobPath(keepReference.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keepPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keepPath, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	destinationDir := t.TempDir()
	destinationStore, err := providerstore.NewStore(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	sourceLock, err := AcquireOperationLock(t.Context(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceLock.Unlock()
	destinationLock, err := AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	defer destinationLock.Unlock()

	_, err = sourceLock.TransferBuildLockStoreClosure(
		t.Context(), destinationLock, sourceStore, destinationStore, build, acceptBuildLockProfile, acceptBuildLockBundle,
	)
	if err == nil || !strings.Contains(err.Error(), "transfer source artifact") {
		t.Fatalf("corrupt source transfer error = %v", err)
	}
	destinationPath, err := destinationStore.BlobPath(keepReference.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(destinationPath); !os.IsNotExist(err) {
		t.Fatalf("corrupt source artifact was published: %v", err)
	}
}

func TestOperationLocksRequireDistinctHeldDeploymentStoresForTransfer(t *testing.T) {
	sourceDir, sourceStore, build, _, _ := buildReachabilityFixture(t)
	destinationDir := t.TempDir()
	destinationStore, err := providerstore.NewStore(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	sourceLock, err := AcquireOperationLock(t.Context(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceLock.Unlock()
	destinationLock, err := AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sourceLock.TransferBuildLockStoreClosure(
		t.Context(), sourceLock, sourceStore, sourceStore, build, acceptBuildLockProfile, acceptBuildLockBundle,
	); err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("same-lock transfer error = %v", err)
	}
	if err := destinationLock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceLock.TransferBuildLockStoreClosure(
		context.Background(), destinationLock, sourceStore, destinationStore, build, acceptBuildLockProfile, acceptBuildLockBundle,
	); err == nil || !strings.Contains(err.Error(), "destination provider store") || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("released destination transfer error = %v", err)
	}
}
