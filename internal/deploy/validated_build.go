package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const (
	ValidatedBuildSchemaV1   = "validated-build-v1"
	validatedBuildFilenameV1 = "validated-build.json"
)

// ValidatedBuildV1 records one opt-in trial build without making it the
// deployment's current generation. Every input identity is retained so an
// editor can report whether the currently saved choices are still validated.
type ValidatedBuildV1 struct {
	Schema                 string                      `json:"schema"`
	BlueprintDigest        canonical.Digest            `json:"blueprint_digest"`
	OverlayDigest          canonical.Digest            `json:"overlay_digest"`
	PackageOverridesDigest canonical.Digest            `json:"package_overrides_digest"`
	Platform               blueprint.Platform          `json:"platform"`
	BuildLockDigest        canonical.Digest            `json:"build_lock_digest"`
	Image                  providers.RealizedImageV1   `json:"image"`
	ImageReference         string                      `json:"image_reference"`
	PendingCleanup         []ValidatedBuildReferenceV1 `json:"pending_cleanup,omitempty"`
	// PendingStorageCleanup keeps successful validation authoritative while
	// superseded locks or provider-store objects await another cleanup attempt.
	PendingStorageCleanup bool `json:"pending_storage_cleanup,omitempty"`
	// Discarded prevents reuse after image references have been removed while
	// retaining a durable storage-cleanup retry record.
	Discarded bool `json:"discarded,omitempty"`
}

// ValidatedBuildReferenceV1 retains enough trusted identity to remove a
// superseded Docker reference without depending on its build lock remaining.
type ValidatedBuildReferenceV1 struct {
	Image          providers.RealizedImageV1 `json:"image"`
	ImageReference string                    `json:"image_reference"`
}

func PackageOverridesDigestV1(overrides PackageOverridesV1) (canonical.Digest, error) {
	content, err := EncodePackageOverridesV1(overrides)
	if err != nil {
		return "", err
	}
	// EncodePackageOverridesV1 normalizes YAML map ordering, scalar spelling,
	// exclusion ordering, and omitted optional fields. Hash that normalized
	// representation so every value accepted by the sidecar schema has a stable
	// identity.
	return canonical.Sum("package-overrides", "package-overrides-v1", string(content))
}

func ValidateValidatedBuildV1(record ValidatedBuildV1) error {
	if record.Schema != ValidatedBuildSchemaV1 {
		return fmt.Errorf("validated build schema %q is unsupported", record.Schema)
	}
	if err := record.BlueprintDigest.Validate(); err != nil {
		return fmt.Errorf("validated build blueprint digest: %w", err)
	}
	if err := record.OverlayDigest.Validate(); err != nil {
		return fmt.Errorf("validated build overlay digest: %w", err)
	}
	if err := record.PackageOverridesDigest.Validate(); err != nil {
		return fmt.Errorf("validated build package overrides digest: %w", err)
	}
	if err := record.Platform.Validate(); err != nil {
		return fmt.Errorf("validated build platform: %w", err)
	}
	if err := record.BuildLockDigest.Validate(); err != nil {
		return fmt.Errorf("validated build lock digest: %w", err)
	}
	if err := validateValidatedBuildReferenceV1(
		ValidatedBuildReferenceV1{Image: record.Image, ImageReference: record.ImageReference},
		"validated build",
	); err != nil {
		return err
	}
	previous := ""
	for index, pending := range record.PendingCleanup {
		if err := validateValidatedBuildReferenceV1(pending, fmt.Sprintf("validated build pending cleanup %d", index)); err != nil {
			return err
		}
		if pending.ImageReference == record.ImageReference {
			return fmt.Errorf("validated build pending cleanup %d duplicates the current image reference", index)
		}
		if index > 0 && previous >= pending.ImageReference {
			return fmt.Errorf("validated build pending cleanup references must be unique and sorted")
		}
		previous = pending.ImageReference
	}
	if record.Discarded {
		if len(record.PendingCleanup) != 0 {
			return fmt.Errorf("discarded validated build cannot retain pending image references")
		}
		if !record.PendingStorageCleanup {
			return fmt.Errorf("discarded validated build must retain pending storage cleanup")
		}
	}
	return nil
}

func validateValidatedBuildReferenceV1(reference ValidatedBuildReferenceV1, description string) error {
	if err := reference.Image.Validate(); err != nil {
		return fmt.Errorf("%s image: %w", description, err)
	}
	if !safeRecoveryIdentity(reference.ImageReference) {
		return fmt.Errorf("%s image reference must be nonempty safe text", description)
	}
	if err := validateSafeImageReference(description, reference.ImageReference, false); err != nil {
		return err
	}
	return nil
}

func EncodeValidatedBuildV1(record ValidatedBuildV1) ([]byte, error) {
	if err := ValidateValidatedBuildV1(record); err != nil {
		return nil, err
	}
	return canonical.Marshal(record)
}

func DecodeValidatedBuildV1(content []byte) (ValidatedBuildV1, error) {
	var record ValidatedBuildV1
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return ValidatedBuildV1{}, fmt.Errorf("decode validated build: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ValidatedBuildV1{}, fmt.Errorf("decode validated build: multiple JSON values are not supported")
		}
		return ValidatedBuildV1{}, fmt.Errorf("decode validated build trailer: %w", err)
	}
	if err := ValidateValidatedBuildV1(record); err != nil {
		return ValidatedBuildV1{}, err
	}
	encoded, err := EncodeValidatedBuildV1(record)
	if err != nil {
		return ValidatedBuildV1{}, err
	}
	if !bytes.Equal(content, encoded) {
		return ValidatedBuildV1{}, fmt.Errorf("validated build is not canonical JSON")
	}
	return record, nil
}

func (lock *OperationLock) ReadValidatedBuildV1() (ValidatedBuildV1, bool, error) {
	if lock == nil {
		return ValidatedBuildV1{}, false, fmt.Errorf("read validated build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.validatedBuildPathLocked()
	if err != nil {
		return ValidatedBuildV1{}, false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return ValidatedBuildV1{}, false, nil
	}
	if err != nil {
		return ValidatedBuildV1{}, false, fmt.Errorf("inspect validated build: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ValidatedBuildV1{}, false, fmt.Errorf("validated build path must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ValidatedBuildV1{}, false, fmt.Errorf("read validated build: %w", err)
	}
	record, err := DecodeValidatedBuildV1(content)
	if err != nil {
		return ValidatedBuildV1{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return record, true, nil
}

func (lock *OperationLock) CommitValidatedBuildV1(record ValidatedBuildV1) error {
	if lock == nil {
		return fmt.Errorf("commit validated build requires an operation lock")
	}
	content, err := EncodeValidatedBuildV1(record)
	if err != nil {
		return err
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.validatedBuildPathLocked()
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("validated build path must be a regular file: %s", path)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect validated build: %w", statErr)
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("commit validated build: %w", err)
	}
	return nil
}

func (lock *OperationLock) RemoveValidatedBuildV1() error {
	if lock == nil {
		return fmt.Errorf("remove validated build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.validatedBuildPathLocked()
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect validated build: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("validated build path must be a regular file: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove validated build: %w", err)
	}
	if err := syncAtomicStateDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync validated build directory: %w", err)
	}
	return nil
}

func (lock *OperationLock) validatedBuildPathLocked() (string, error) {
	if lock.released || lock.file == nil || lock.path == "" {
		return "", fmt.Errorf("operation lock is not held")
	}
	return filepath.Join(filepath.Dir(lock.path), validatedBuildFilenameV1), nil
}
