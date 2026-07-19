package providerstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
)

const StoreDirName = "provider-store"

type Store struct {
	root string
}

func NewStore(deploymentRoot string) (Store, error) {
	if deploymentRoot == "" || !filepath.IsAbs(deploymentRoot) || filepath.Clean(deploymentRoot) != deploymentRoot {
		return Store{}, fmt.Errorf("provider store deployment root must be an absolute clean path: %q", deploymentRoot)
	}
	info, err := os.Lstat(deploymentRoot)
	if err != nil {
		return Store{}, fmt.Errorf("inspect provider store deployment root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Store{}, fmt.Errorf("provider store deployment root must be a real directory: %s", deploymentRoot)
	}
	return Store{root: filepath.Join(deploymentRoot, ".reploy", StoreDirName)}, nil
}

func (store Store) Root() string { return store.root }

// NewWorkspace creates private same-filesystem scratch beneath the
// deployment-owned store. Callers can therefore stage immutable blobs with
// hardlinks instead of copying their bytes.
func (store Store) NewWorkspace(pattern string) (string, error) {
	if pattern == "" || filepath.Base(pattern) != pattern || strings.ContainsAny(pattern, `/\\`) {
		return "", fmt.Errorf("provider store workspace pattern must be one path component")
	}
	if err := store.prepareBaseDirectories(); err != nil {
		return "", err
	}
	workspace, err := os.MkdirTemp(filepath.Join(store.root, "tmp"), pattern)
	if err != nil {
		return "", fmt.Errorf("create provider store workspace: %w", err)
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		_ = os.Remove(workspace)
		return "", fmt.Errorf("protect provider store workspace: %w", err)
	}
	return workspace, nil
}

// Publish implements providers.ArtifactSink without importing the provider
// package. It streams one raw artifact into private storage, then atomically
// publishes it under its content digest without replacing an existing object.
func (store Store) Publish(ctx context.Context, logicalPath string, kind string, reader io.Reader) (ArtifactDescriptor, error) {
	if ctx == nil {
		return ArtifactDescriptor{}, fmt.Errorf("artifact publication context is required")
	}
	if reader == nil {
		return ArtifactDescriptor{}, fmt.Errorf("artifact publication reader is required")
	}
	if err := validateArtifactName(logicalPath, kind); err != nil {
		return ArtifactDescriptor{}, err
	}
	temporary, err := store.writeTemporary(ctx, "blob-*", reader)
	if err != nil {
		return ArtifactDescriptor{}, err
	}
	defer os.Remove(temporary.path)

	descriptor := ArtifactDescriptor{
		LogicalPath: logicalPath,
		Kind:        kind,
		Size:        strconv.FormatInt(temporary.size, 10),
		SHA256:      temporary.digest,
	}
	if err := descriptor.Validate(); err != nil {
		return ArtifactDescriptor{}, err
	}
	finalPath, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		return ArtifactDescriptor{}, err
	}
	finalDir := filepath.Dir(finalPath)
	if err := ensureRealDirectory(filepath.Join(store.root, "blobs")); err != nil {
		return ArtifactDescriptor{}, err
	}
	if err := ensureRealDirectory(filepath.Join(store.root, "blobs", "sha256")); err != nil {
		return ArtifactDescriptor{}, err
	}
	if err := ensureRealDirectory(finalDir); err != nil {
		return ArtifactDescriptor{}, err
	}
	if err := publishTemporary(temporary.path, finalPath, func() error {
		if err := store.VerifyArtifact(descriptor); err != nil {
			return fmt.Errorf("existing provider store blob is invalid: %w", err)
		}
		return nil
	}); err != nil {
		return ArtifactDescriptor{}, fmt.Errorf("publish provider store blob: %w", err)
	}
	return descriptor, nil
}

func (store Store) PublishManifest(ctx context.Context, reference StoreObjectRef, content []byte) error {
	if err := validateBundleManifestReference(reference); err != nil {
		return err
	}
	return store.publishRecord(ctx, reference, content, "manifest")
}

func (store Store) PublishValidationRecord(ctx context.Context, reference StoreObjectRef, content []byte) error {
	if err := validateValidationRecordReference(reference); err != nil {
		return err
	}
	return store.publishRecord(ctx, reference, content, "validation record")
}

func (store Store) publishRecord(ctx context.Context, reference StoreObjectRef, content []byte, description string) error {
	temporary, err := store.writeTemporary(ctx, strings.ReplaceAll(description, " ", "-")+"-*", bytes.NewReader(content))
	if err != nil {
		return err
	}
	defer os.Remove(temporary.path)
	finalPath, err := store.recordPath(reference)
	if err != nil {
		return err
	}
	directory, err := recordDirectory(reference.Kind)
	if err != nil {
		return err
	}
	finalDir := filepath.Dir(finalPath)
	if err := ensureRealDirectory(filepath.Join(store.root, directory)); err != nil {
		return err
	}
	if err := ensureRealDirectory(filepath.Join(store.root, directory, "sha256")); err != nil {
		return err
	}
	if err := ensureRealDirectory(finalDir); err != nil {
		return err
	}
	if err := publishTemporary(temporary.path, finalPath, func() error {
		existing, err := readRegularFile(finalPath, "provider store "+description)
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, content) {
			return fmt.Errorf("existing provider store %s content differs", description)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("publish provider store %s: %w", description, err)
	}
	return nil
}

func (store Store) LoadManifest(reference StoreObjectRef) ([]byte, error) {
	if err := validateBundleManifestReference(reference); err != nil {
		return nil, err
	}
	return store.loadRecord(reference, "manifest")
}

func (store Store) LoadValidationRecord(reference StoreObjectRef) ([]byte, error) {
	if err := validateValidationRecordReference(reference); err != nil {
		return nil, err
	}
	return store.loadRecord(reference, "validation record")
}

func (store Store) loadRecord(reference StoreObjectRef, description string) ([]byte, error) {
	path, err := store.recordPath(reference)
	if err != nil {
		return nil, err
	}
	directory, err := recordDirectory(reference.Kind)
	if err != nil {
		return nil, err
	}
	hex := strings.TrimPrefix(string(reference.Digest), "sha256:")
	if err := store.requireDirectories(directory, "sha256", hex[:2]); err != nil {
		return nil, err
	}
	return readRegularFile(path, "provider store "+description)
}

func (store Store) ManifestPath(reference StoreObjectRef) (string, error) {
	if err := validateBundleManifestReference(reference); err != nil {
		return "", err
	}
	return store.recordPath(reference)
}

func (store Store) ValidationRecordPath(reference StoreObjectRef) (string, error) {
	if err := validateValidationRecordReference(reference); err != nil {
		return "", err
	}
	return store.recordPath(reference)
}

func (store Store) recordPath(reference StoreObjectRef) (string, error) {
	if err := reference.Validate(); err != nil {
		return "", err
	}
	directory, err := recordDirectory(reference.Kind)
	if err != nil {
		return "", err
	}
	hex := strings.TrimPrefix(string(reference.Digest), "sha256:")
	return filepath.Join(store.root, directory, "sha256", hex[:2], hex+".json"), nil
}

func recordDirectory(kind string) (string, error) {
	switch kind {
	case BundleManifestKind:
		return "manifests", nil
	case ValidationRecordKind:
		return "validation", nil
	default:
		return "", fmt.Errorf("provider store record kind %q is unsupported", kind)
	}
}

func validateBundleManifestReference(reference StoreObjectRef) error {
	if err := reference.Validate(); err != nil {
		return fmt.Errorf("provider store manifest reference: %w", err)
	}
	if reference.Kind != BundleManifestKind {
		return fmt.Errorf("provider store manifest reference kind must be %q", BundleManifestKind)
	}
	return nil
}

func validateValidationRecordReference(reference StoreObjectRef) error {
	if err := reference.Validate(); err != nil {
		return fmt.Errorf("provider store validation record reference: %w", err)
	}
	if reference.Kind != ValidationRecordKind {
		return fmt.Errorf("provider store validation record reference kind must be %q", ValidationRecordKind)
	}
	return nil
}

func (store Store) prepareBaseDirectories() error {
	if err := ensureRealDirectory(filepath.Dir(store.root)); err != nil {
		return err
	}
	if err := ensureRealDirectory(store.root); err != nil {
		return err
	}
	return ensureRealDirectory(filepath.Join(store.root, "tmp"))
}

func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("provider store path must be a real directory: %s", path)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect provider store directory: %w", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create provider store directory: %w", err)
		}
	}
	info, err = os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect created provider store directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider store path must be a real directory: %s", path)
	}
	return nil
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect provider store directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider store path must be a real directory: %s", path)
	}
	return nil
}

func (store Store) requireDirectories(parts ...string) error {
	directories := []string{filepath.Dir(store.root), store.root}
	current := store.root
	for _, part := range parts {
		current = filepath.Join(current, part)
		directories = append(directories, current)
	}
	for _, directory := range directories {
		if err := requireRealDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

type temporaryObject struct {
	path   string
	size   int64
	digest canonical.Digest
}

func (store Store) writeTemporary(ctx context.Context, pattern string, reader io.Reader) (temporaryObject, error) {
	if ctx == nil {
		return temporaryObject{}, fmt.Errorf("provider store publication context is required")
	}
	if reader == nil {
		return temporaryObject{}, fmt.Errorf("provider store publication reader is required")
	}
	if err := store.prepareBaseDirectories(); err != nil {
		return temporaryObject{}, err
	}
	temporary, err := os.CreateTemp(filepath.Join(store.root, "tmp"), pattern)
	if err != nil {
		return temporaryObject{}, fmt.Errorf("create provider store temporary object: %w", err)
	}
	path := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		if !closed {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return temporaryObject{}, fmt.Errorf("write provider store temporary object: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return temporaryObject{}, err
	}
	if err := temporary.Chmod(0o444); err != nil {
		return temporaryObject{}, fmt.Errorf("make provider store object read-only: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return temporaryObject{}, fmt.Errorf("sync provider store temporary object: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return temporaryObject{}, fmt.Errorf("close provider store temporary object: %w", err)
	}
	closed = true
	return temporaryObject{
		path: path, size: size,
		digest: canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))),
	}, nil
}

func publishTemporary(temporaryPath string, finalPath string, verifyExisting func() error) error {
	if err := os.Link(temporaryPath, finalPath); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		return verifyExisting()
	}
	return syncStoreDirectory(filepath.Dir(finalPath))
}

func readRegularFile(path string, description string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", description, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file: %s", description, path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", description, err)
	}
	return content, nil
}

func (store Store) BlobPath(digest canonical.Digest) (string, error) {
	if err := digest.Validate(); err != nil {
		return "", fmt.Errorf("provider store blob digest: %w", err)
	}
	hex := strings.TrimPrefix(string(digest), "sha256:")
	return filepath.Join(store.root, "blobs", "sha256", hex[:2], hex), nil
}

func (store Store) VerifyArtifact(descriptor ArtifactDescriptor) error {
	if err := descriptor.Validate(); err != nil {
		return err
	}
	path, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		return err
	}
	hex := strings.TrimPrefix(string(descriptor.SHA256), "sha256:")
	if err := store.requireDirectories("blobs", "sha256", hex[:2]); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect provider store blob: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("provider store blob must be a regular file: %s", path)
	}
	wantSize, err := strconv.ParseInt(descriptor.Size, 10, 64)
	if err != nil {
		return fmt.Errorf("parse artifact size: %w", err)
	}
	if info.Size() != wantSize {
		return fmt.Errorf("provider store blob size is %d, want %d", info.Size(), wantSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open provider store blob: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash provider store blob: %w", err)
	}
	got := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
	if got != descriptor.SHA256 {
		return fmt.Errorf("provider store blob digest is %s, want %s", got, descriptor.SHA256)
	}
	return nil
}

func validateArtifactName(logicalPath string, kind string) error {
	placeholder := ArtifactDescriptor{
		LogicalPath: logicalPath,
		Kind:        kind,
		Size:        "0",
		SHA256:      canonical.Digest("sha256:" + strings.Repeat("0", 64)),
	}
	return placeholder.Validate()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
