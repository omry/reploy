package providerstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorePublishesImmutableBlobAtContentPath(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.Publish(context.Background(), "wheels/demo.whl", "wheel", strings.NewReader("wheel bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Size != "11" {
		t.Fatalf("size = %q", descriptor.Size)
	}
	path, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "wheel bytes" {
		t.Fatalf("content = %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("blob mode = %o", info.Mode().Perm())
	}
	if err := store.VerifyArtifact(descriptor); err != nil {
		t.Fatal(err)
	}
}

func TestStoreReusesExistingValidBlobAndCleansTemporaryFiles(t *testing.T) {
	deployment := t.TempDir()
	root := filepath.Join(deployment, ".reploy", StoreDirName)
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Publish(context.Background(), "first/demo.whl", "wheel", strings.NewReader("same"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Publish(context.Background(), "second/demo.whl", "wheel", strings.NewReader("same"))
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("digests differ: %s != %s", first.SHA256, second.SHA256)
	}
	entries, err := os.ReadDir(filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary entries = %#v", entries)
	}
}

func TestStoreRejectsCorruptExistingBlobWithoutReplacingIt(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.Publish(context.Background(), "demo.deb", "deb", strings.NewReader("expected"))
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt!"), 0o444); err != nil {
		t.Fatal(err)
	}
	_, err = store.Publish(context.Background(), "demo.deb", "deb", strings.NewReader("expected"))
	if err == nil || !strings.Contains(err.Error(), "existing provider store blob is invalid") {
		t.Fatalf("error = %v", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "corrupt!" {
		t.Fatalf("existing blob was replaced: %q", content)
	}
}

func TestStoreCancellationRemovesUnpublishedBlob(t *testing.T) {
	deployment := t.TempDir()
	root := filepath.Join(deployment, ".reploy", StoreDirName)
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Publish(ctx, "demo.whl", "wheel", strings.NewReader("content")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary entries = %#v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, "blobs")); !os.IsNotExist(err) {
		t.Fatalf("blob directory error = %v", err)
	}
}

func TestStoreRejectsSymlinkedStoreRoot(t *testing.T) {
	deployment := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(deployment, ".reploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(deployment, ".reploy", StoreDirName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(context.Background(), "demo.whl", "wheel", strings.NewReader("content")); err == nil || !strings.Contains(err.Error(), "must be a real directory") {
		t.Fatalf("error = %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside entries = %#v", entries)
	}
}

func TestStoreManifestPublicationReusesOnlyIdenticalContent(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	reference := StoreObjectRef{Kind: BundleManifestKind, Digest: storeRefDigest("a")}
	if err := store.PublishManifest(context.Background(), reference, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishManifest(context.Background(), reference, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishManifest(context.Background(), reference, []byte("other")); err == nil || !strings.Contains(err.Error(), "content differs") {
		t.Fatalf("error = %v", err)
	}
	content, err := store.LoadManifest(reference)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first" {
		t.Fatalf("manifest was replaced: %q", content)
	}
}

func TestStoreLoadManifestRejectsSymlink(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	reference := StoreObjectRef{Kind: BundleManifestKind, Digest: storeRefDigest("b")}
	if err := store.PublishManifest(context.Background(), reference, []byte("manifest")); err != nil {
		t.Fatal(err)
	}
	path, err := store.ManifestPath(reference)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("manifest"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.LoadManifest(reference); err == nil || !strings.Contains(err.Error(), "must be a regular file") {
		t.Fatalf("error = %v", err)
	}
}

func TestStoreLoadManifestRejectsSymlinkedParent(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	reference := StoreObjectRef{Kind: BundleManifestKind, Digest: storeRefDigest("c")}
	path, err := store.ManifestPath(reference)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	hex := strings.TrimPrefix(string(reference.Digest), "sha256:")
	outsideDir := filepath.Join(outside, "sha256", hex[:2])
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, filepath.Base(path)), []byte("manifest"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store.Root(), "manifests")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.LoadManifest(reference); err == nil || !strings.Contains(err.Error(), "must be a real directory") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewStoreRequiresAbsoluteCleanDeploymentRoot(t *testing.T) {
	for _, root := range []string{"", "relative/provider-store", t.TempDir() + string(filepath.Separator) + ".." + string(filepath.Separator) + "provider-store"} {
		if _, err := NewStore(root); err == nil {
			t.Fatalf("invalid root accepted: %q", root)
		}
	}
}
