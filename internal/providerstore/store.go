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

// Exists distinguishes a deliberately absent cache root from a present but
// malformed provider store. Callers may treat only the former as a cache miss.
func (store Store) Exists() (bool, error) {
	parent := filepath.Dir(store.root)
	if err := requireRealDirectory(parent); err != nil {
		return false, err
	}
	info, err := os.Lstat(store.root)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect provider store root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("provider store root must be a real directory: %s", store.root)
	}
	return true, nil
}

// Remove deletes the complete deployment-owned provider store, including
// abandoned temporary workspaces. It refuses to follow a replaced or corrupt
// store path and is absent-safe so clean can be repeated.
func (store Store) Remove() (bool, error) {
	exists, err := store.Exists()
	if err != nil || !exists {
		return false, err
	}
	if err := os.RemoveAll(store.root); err != nil {
		return false, fmt.Errorf("remove provider store: %w", err)
	}
	if err := syncStoreDirectory(filepath.Dir(store.root)); err != nil {
		return false, err
	}
	return true, nil
}

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
	return store.publishArtifact(ctx, logicalPath, kind, reader, nil)
}

// PublishExpected atomically publishes an artifact only when the streamed
// bytes match the caller's already-validated descriptor. A mismatch never
// creates a digest-addressed blob.
func (store Store) PublishExpected(ctx context.Context, expected ArtifactDescriptor, reader io.Reader) (ArtifactDescriptor, error) {
	if err := expected.Validate(); err != nil {
		return ArtifactDescriptor{}, err
	}
	return store.publishArtifact(ctx, expected.LogicalPath, expected.Kind, reader, &expected)
}

func (store Store) publishArtifact(ctx context.Context, logicalPath string, kind string, reader io.Reader, expected *ArtifactDescriptor) (ArtifactDescriptor, error) {
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
	if expected != nil && descriptor != *expected {
		return ArtifactDescriptor{}, fmt.Errorf("provider artifact bytes do not match expected descriptor")
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
	if descriptor.Kind == "deb" {
		if err := store.recordArtifactVerification(descriptor); err != nil {
			return ArtifactDescriptor{}, fmt.Errorf("record provider artifact verification: %w", err)
		}
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
	path, err := store.InspectArtifactPath(descriptor)
	if err != nil {
		return err
	}
	return VerifyArtifactFile(path, descriptor)
}

// OpenVerifiedArtifact opens and hashes one immutable store artifact. The
// returned descriptor-bound file remains stable if its store path is replaced;
// callers that parse it should call VerifyOpenArtifact again before closing to
// detect in-place modification during parsing.
func (store Store) OpenVerifiedArtifact(descriptor ArtifactDescriptor) (*os.File, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	path, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		return nil, err
	}
	hex := strings.TrimPrefix(string(descriptor.SHA256), "sha256:")
	if err := store.requireDirectories("blobs", "sha256", hex[:2]); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(store.root)
	if err != nil {
		return nil, fmt.Errorf("open provider store root: %w", err)
	}
	relative, err := filepath.Rel(store.root, path)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("resolve provider store artifact path: %w", err)
	}
	file, openErr := root.Open(relative)
	closeRootErr := root.Close()
	if openErr != nil {
		return nil, fmt.Errorf("open provider store artifact: %w", errors.Join(openErr, closeRootErr))
	}
	if closeRootErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("close provider store root: %w", closeRootErr)
	}
	if err := VerifyOpenArtifact(file, descriptor); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// VerifyOpenArtifact hashes the exact open file rather than reopening its
// pathname. It resets the file offset to the start before returning.
func VerifyOpenArtifact(file *os.File, descriptor ArtifactDescriptor) error {
	if file == nil {
		return fmt.Errorf("verify open provider artifact requires a file")
	}
	if err := descriptor.Validate(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect open provider artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("open provider artifact must be a regular file")
	}
	wantSize, err := strconv.ParseInt(descriptor.Size, 10, 64)
	if err != nil {
		return fmt.Errorf("parse artifact size: %w", err)
	}
	if info.Size() != wantSize {
		return fmt.Errorf("open provider artifact size is %d, want %d", info.Size(), wantSize)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek open provider artifact: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash open provider artifact: %w", err)
	}
	got := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
	if got != descriptor.SHA256 {
		return fmt.Errorf("open provider artifact digest is %s, want %s", got, descriptor.SHA256)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("reset open provider artifact: %w", err)
	}
	return nil
}

// VerifyArtifactFile validates one regular file against an immutable artifact
// descriptor. Backends use it for same-filesystem staged links that will be
// consumed directly by a resolver or materializer.
func VerifyArtifactFile(path string, descriptor ArtifactDescriptor) error {
	if err := InspectArtifactFile(path, descriptor); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open provider artifact file: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash provider artifact file: %w", err)
	}
	got := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
	if got != descriptor.SHA256 {
		return fmt.Errorf("provider artifact file digest is %s, want %s", got, descriptor.SHA256)
	}
	return nil
}

// InspectArtifactFile validates a staged artifact's path, type, and size
// without hashing it. It is used only after the same exact staged file was
// already verified or published during the current operation.
func InspectArtifactFile(path string, descriptor ArtifactDescriptor) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("provider artifact file path must be absolute and clean")
	}
	if err := descriptor.Validate(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect provider artifact file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("provider artifact file must be regular: %s", path)
	}
	wantSize, err := strconv.ParseInt(descriptor.Size, 10, 64)
	if err != nil {
		return fmt.Errorf("parse artifact size: %w", err)
	}
	if info.Size() != wantSize {
		return fmt.Errorf("provider artifact file size is %d, want %d", info.Size(), wantSize)
	}
	return nil
}

// InspectArtifactPath validates the immutable store layout, final file type,
// and declared size without reading and hashing the file contents.
func (store Store) InspectArtifactPath(descriptor ArtifactDescriptor) (string, error) {
	if err := descriptor.Validate(); err != nil {
		return "", err
	}
	path, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		return "", err
	}
	hex := strings.TrimPrefix(string(descriptor.SHA256), "sha256:")
	if err := store.requireDirectories("blobs", "sha256", hex[:2]); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect provider store blob: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("provider store blob must be a regular file: %s", path)
	}
	wantSize, err := strconv.ParseInt(descriptor.Size, 10, 64)
	if err != nil {
		return "", fmt.Errorf("parse artifact size: %w", err)
	}
	if info.Size() != wantSize {
		return "", fmt.Errorf("provider store blob size is %d, want %d", info.Size(), wantSize)
	}
	return path, nil
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
