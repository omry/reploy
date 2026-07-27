package providerstore

import (
	"context"
	"errors"
	"io/fs"
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

func TestStorePublishExpectedRejectsChangedBytesBeforeBlobPublication(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := store.Publish(context.Background(), "debs/expected.deb", "deb", strings.NewReader("expected"))
	if err != nil {
		t.Fatal(err)
	}
	otherDeployment := t.TempDir()
	otherStore, err := NewStore(otherDeployment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherStore.PublishExpected(context.Background(), expected, strings.NewReader("changed")); err == nil || !strings.Contains(err.Error(), "expected descriptor") {
		t.Fatalf("error = %v", err)
	}
	expectedPath, err := otherStore.BlobPath(expected.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(expectedPath); !os.IsNotExist(err) {
		t.Fatalf("mismatched blob was published: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(otherStore.Root(), "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary entries = %#v, err = %v", entries, err)
	}
	published, err := otherStore.PublishExpected(context.Background(), expected, strings.NewReader("expected"))
	if err != nil || published != expected {
		t.Fatalf("published = %#v, err = %v", published, err)
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

func TestStoreRemoveTemporaryEntriesPreservesPublishedObjects(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.Publish(context.Background(), "keep.deb", "deb", strings.NewReader("published"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("abandoned-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "nested", "partial"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "tmp", "partial-blob"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveTemporaryEntries(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary entries remain: %#v", entries)
	}
	if err := store.VerifyArtifact(descriptor); err != nil {
		t.Fatalf("published object was changed: %v", err)
	}
}

func TestStoreRemoveTemporaryEntriesDoesNotCreateMissingStore(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveTemporaryEntries(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(deployment, ".reploy")); !os.IsNotExist(err) {
		t.Fatalf("cleanup created deployment state: %v", err)
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

func TestStoreValidationRecordPublicationReusesOnlyIdenticalContent(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	reference := StoreObjectRef{Kind: ValidationRecordKind, Digest: storeRefDigest("d")}
	if err := store.PublishValidationRecord(context.Background(), reference, []byte("validation")); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishValidationRecord(context.Background(), reference, []byte("validation")); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishValidationRecord(context.Background(), reference, []byte("other")); err == nil || !strings.Contains(err.Error(), "content differs") {
		t.Fatalf("error = %v", err)
	}
	content, err := store.LoadValidationRecord(reference)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "validation" {
		t.Fatalf("validation record was replaced: %q", content)
	}
}

func TestStoreRemoveUnreachableKeepsExactClosure(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	keepBlob, err := store.Publish(context.Background(), "keep.deb", "deb", strings.NewReader("keep"))
	if err != nil {
		t.Fatal(err)
	}
	dropBlob, err := store.Publish(context.Background(), "drop.deb", "deb", strings.NewReader("drop"))
	if err != nil {
		t.Fatal(err)
	}
	keepBlobRef, err := keepBlob.StoreObjectRef()
	if err != nil {
		t.Fatal(err)
	}
	keepManifest := StoreObjectRef{Kind: BundleManifestKind, Digest: storeRefDigest("e")}
	dropManifest := StoreObjectRef{Kind: BundleManifestKind, Digest: storeRefDigest("f")}
	keepValidation := StoreObjectRef{Kind: ValidationRecordKind, Digest: storeRefDigest("1")}
	dropValidation := StoreObjectRef{Kind: ValidationRecordKind, Digest: storeRefDigest("2")}
	for _, item := range []struct {
		reference StoreObjectRef
		content   string
	}{
		{keepManifest, "keep manifest"}, {dropManifest, "drop manifest"},
	} {
		if err := store.PublishManifest(context.Background(), item.reference, []byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		reference StoreObjectRef
		content   string
	}{
		{keepValidation, "keep validation"}, {dropValidation, "drop validation"},
	} {
		if err := store.PublishValidationRecord(context.Background(), item.reference, []byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	reachable := []StoreObjectRef{keepBlobRef, keepManifest, keepValidation}
	if err := store.RemoveUnreachable(reachable); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyArtifact(keepBlob); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadManifest(keepManifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadValidationRecord(keepValidation); err != nil {
		t.Fatal(err)
	}
	dropBlobPath, err := store.BlobPath(dropBlob.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		dropBlobPath,
		mustManifestPath(t, store, dropManifest),
		mustValidationRecordPath(t, store, dropValidation),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("unreachable object remains at %s: %v", path, err)
		}
	}
}

func TestStoreRemoveUnreachablePreflightsLayoutBeforeDeletion(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.Publish(context.Background(), "keep.whl", "wheel", strings.NewReader("keep"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := StoreObjectRef{Kind: BundleManifestKind, Digest: storeRefDigest("3")}
	if err := store.PublishManifest(context.Background(), manifest, []byte("manifest")); err != nil {
		t.Fatal(err)
	}
	manifestPath := mustManifestPath(t, store, manifest)
	if err := os.WriteFile(filepath.Join(filepath.Dir(manifestPath), "unexpected"), []byte("unknown"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveUnreachable([]StoreObjectRef{}); err == nil || !strings.Contains(err.Error(), "unrecognized object name") {
		t.Fatalf("error = %v", err)
	}
	if err := store.VerifyArtifact(descriptor); err != nil {
		t.Fatalf("recognized object was removed before layout validation: %v", err)
	}
}

func mustManifestPath(t *testing.T, store Store, reference StoreObjectRef) string {
	t.Helper()
	path, err := store.ManifestPath(reference)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func mustValidationRecordPath(t *testing.T, store Store, reference StoreObjectRef) string {
	t.Helper()
	path, err := store.ValidationRecordPath(reference)
	if err != nil {
		t.Fatal(err)
	}
	return path
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

func TestStoreRemoveDeletesObjectsAndTemporaryWorkspacesAndIsAbsentSafe(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), "packages/demo.deb", "deb", strings.NewReader("demo")); err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("clean-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "partial"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := store.Remove()
	if err != nil || !removed {
		t.Fatalf("first remove = %v, %v", removed, err)
	}
	if _, err := os.Lstat(store.Root()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("provider store remains after clean: %v", err)
	}
	removed, err = store.Remove()
	if err != nil || removed {
		t.Fatalf("second remove = %v, %v", removed, err)
	}
}

func TestStoreRemoveRejectsReplacedRootWithoutFollowingIt(t *testing.T) {
	deployment := t.TempDir()
	store, err := NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.Root()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.Root()); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.Remove(); err == nil || removed {
		t.Fatalf("remove replaced store = %v, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Fatalf("clean followed replaced store root: %v", err)
	}
}
