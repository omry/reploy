package providerstore

import (
	"bytes"
	"context"
	"encoding/json"
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

const (
	artifactVerificationSchemaV1 = "artifact-verification-v1"
	artifactVerificationDir      = "artifact-verification"
)

type artifactVerificationV1 struct {
	Schema         string           `json:"schema"`
	SHA256         canonical.Digest `json:"sha256"`
	Size           string           `json:"size"`
	ModificationNS string           `json:"modification_ns"`
}

// VerifyCachedDeb trusts the previous content verification only when the
// deployment-local blob still has the declared size and exact recorded
// modification time. A missing or nonmatching stamp causes one full hash and
// refreshes the derived stamp; a blob-size mismatch is rejected during path
// inspection. The stamp is cache evidence only: it is not part of artifact or
// bundle identity and may be deleted safely.
func (store Store) VerifyCachedDeb(descriptor ArtifactDescriptor) error {
	if err := descriptor.Validate(); err != nil {
		return err
	}
	if descriptor.Kind != "deb" {
		return fmt.Errorf("cached Debian verification requires a deb artifact")
	}
	path, err := store.InspectArtifactPath(descriptor)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect cached Debian artifact: %w", err)
	}
	matches, err := store.artifactVerificationMatches(descriptor, info)
	if err != nil {
		return err
	}
	if matches {
		return nil
	}
	if err := VerifyArtifactFile(path, descriptor); err != nil {
		return err
	}
	verified, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect verified Debian artifact: %w", err)
	}
	if !sameArtifactVerificationMetadata(info, verified) {
		return fmt.Errorf("cached Debian artifact changed during verification")
	}
	return store.writeArtifactVerification(descriptor, verified)
}

func sameArtifactVerificationMetadata(before os.FileInfo, after os.FileInfo) bool {
	return os.SameFile(before, after) &&
		before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime())
}

func (store Store) recordArtifactVerification(descriptor ArtifactDescriptor) error {
	path, err := store.InspectArtifactPath(descriptor)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect published Debian artifact: %w", err)
	}
	return store.writeArtifactVerification(descriptor, info)
}

func (store Store) artifactVerificationMatches(
	descriptor ArtifactDescriptor,
	info os.FileInfo,
) (bool, error) {
	path, err := store.artifactVerificationPath(descriptor.SHA256)
	if err != nil {
		return false, err
	}
	stampInfo, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect artifact verification stamp: %w", err)
	}
	if !stampInfo.Mode().IsRegular() {
		return false, fmt.Errorf("artifact verification stamp must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read artifact verification stamp: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var stamp artifactVerificationV1
	if err := decoder.Decode(&stamp); err != nil {
		return false, nil
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return false, nil
	}
	return stamp.Schema == artifactVerificationSchemaV1 &&
		stamp.SHA256 == descriptor.SHA256 &&
		stamp.Size == descriptor.Size &&
		stamp.ModificationNS == strconv.FormatInt(info.ModTime().UnixNano(), 10), nil
}

func (store Store) writeArtifactVerification(
	descriptor ArtifactDescriptor,
	info os.FileInfo,
) error {
	stamp := artifactVerificationV1{
		Schema: artifactVerificationSchemaV1, SHA256: descriptor.SHA256,
		Size: descriptor.Size, ModificationNS: strconv.FormatInt(info.ModTime().UnixNano(), 10),
	}
	content, err := canonical.Marshal(stamp)
	if err != nil {
		return err
	}
	finalPath, err := store.artifactVerificationPath(descriptor.SHA256)
	if err != nil {
		return err
	}
	hex := strings.TrimPrefix(string(descriptor.SHA256), "sha256:")
	for _, directory := range []string{
		filepath.Join(store.root, artifactVerificationDir),
		filepath.Join(store.root, artifactVerificationDir, "sha256"),
		filepath.Join(store.root, artifactVerificationDir, "sha256", hex[:2]),
	} {
		if err := ensureRealDirectory(directory); err != nil {
			return err
		}
	}
	temporary, err := store.writeTemporary(context.Background(), "artifact-verification-*", bytes.NewReader(content))
	if err != nil {
		return err
	}
	defer os.Remove(temporary.path)
	if existing, err := os.Lstat(finalPath); err == nil {
		if !existing.Mode().IsRegular() {
			return fmt.Errorf("artifact verification stamp must be a regular file: %s", finalPath)
		}
		if err := os.Remove(finalPath); err != nil {
			return fmt.Errorf("replace artifact verification stamp: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect artifact verification stamp: %w", err)
	}
	if err := os.Rename(temporary.path, finalPath); err != nil {
		return fmt.Errorf("publish artifact verification stamp: %w", err)
	}
	return syncStoreDirectory(filepath.Dir(finalPath))
}

func (store Store) artifactVerificationPath(digest canonical.Digest) (string, error) {
	if err := digest.Validate(); err != nil {
		return "", fmt.Errorf("artifact verification digest: %w", err)
	}
	hex := strings.TrimPrefix(string(digest), "sha256:")
	return filepath.Join(store.root, artifactVerificationDir, "sha256", hex[:2], hex+".json"), nil
}
