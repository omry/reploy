package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/canonical"
)

const (
	PendingValidatedBuildSchemaV1   = "pending-validated-build-v1"
	pendingValidatedBuildFilenameV1 = "pending-validated-build.json"
)

// PendingValidatedBuildV1 names the exact candidate references before either
// Docker tag is created. Recovery can remove an interrupted candidate without
// changing the previously committed validated build.
type PendingValidatedBuildV1 struct {
	Schema          string                     `json:"schema"`
	BuildLockDigest canonical.Digest           `json:"build_lock_digest"`
	Final           ValidatedBuildReferenceV1  `json:"final"`
	Portable        *ValidatedBuildReferenceV1 `json:"portable,omitempty"`
}

func ValidatePendingValidatedBuildV1(pending PendingValidatedBuildV1) error {
	if pending.Schema != PendingValidatedBuildSchemaV1 {
		return fmt.Errorf("pending validated build schema %q is unsupported", pending.Schema)
	}
	if err := pending.BuildLockDigest.Validate(); err != nil {
		return fmt.Errorf("pending validated build lock digest: %w", err)
	}
	if err := validateValidatedBuildReferenceV1(pending.Final, "pending validated final image"); err != nil {
		return err
	}
	if pending.Portable != nil {
		if err := validateValidatedBuildReferenceV1(*pending.Portable, "pending validated portable layer"); err != nil {
			return err
		}
		if pending.Portable.ImageReference == pending.Final.ImageReference {
			return fmt.Errorf("pending validated references must differ")
		}
	}
	return nil
}

func EncodePendingValidatedBuildV1(pending PendingValidatedBuildV1) ([]byte, error) {
	if err := ValidatePendingValidatedBuildV1(pending); err != nil {
		return nil, err
	}
	return canonical.Marshal(pending)
}

func DecodePendingValidatedBuildV1(content []byte) (PendingValidatedBuildV1, error) {
	var pending PendingValidatedBuildV1
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pending); err != nil {
		return PendingValidatedBuildV1{}, fmt.Errorf("decode pending validated build: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return PendingValidatedBuildV1{}, fmt.Errorf("pending validated build contains trailing JSON")
	}
	encoded, err := EncodePendingValidatedBuildV1(pending)
	if err != nil {
		return PendingValidatedBuildV1{}, err
	}
	if !bytes.Equal(content, encoded) {
		return PendingValidatedBuildV1{}, fmt.Errorf("pending validated build is not canonical JSON")
	}
	return pending, nil
}

func (lock *OperationLock) WritePendingValidatedBuildV1(pending PendingValidatedBuildV1) error {
	if lock == nil {
		return fmt.Errorf("write pending validated build requires an operation lock")
	}
	content, err := EncodePendingValidatedBuildV1(pending)
	if err != nil {
		return err
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.pendingValidatedBuildPathLocked()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("pending validated build already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect pending validated build: %w", err)
	}
	return writeAtomicStateFile(path, content, 0o600)
}

func (lock *OperationLock) ReadPendingValidatedBuildV1() (PendingValidatedBuildV1, bool, error) {
	if lock == nil {
		return PendingValidatedBuildV1{}, false, fmt.Errorf("read pending validated build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.pendingValidatedBuildPathLocked()
	if err != nil {
		return PendingValidatedBuildV1{}, false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return PendingValidatedBuildV1{}, false, nil
	}
	if err != nil {
		return PendingValidatedBuildV1{}, false, fmt.Errorf("inspect pending validated build: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return PendingValidatedBuildV1{}, false, fmt.Errorf("pending validated build path must be a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return PendingValidatedBuildV1{}, false, fmt.Errorf("read pending validated build: %w", err)
	}
	pending, err := DecodePendingValidatedBuildV1(content)
	if err != nil {
		return PendingValidatedBuildV1{}, false, err
	}
	return pending, true, nil
}

func (lock *OperationLock) RemovePendingValidatedBuildV1() error {
	if lock == nil {
		return fmt.Errorf("remove pending validated build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.pendingValidatedBuildPathLocked()
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect pending validated build: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("pending validated build path must be a regular file")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove pending validated build: %w", err)
	}
	return syncAtomicStateDirectory(filepath.Dir(path))
}

func (lock *OperationLock) pendingValidatedBuildPathLocked() (string, error) {
	if lock.released || lock.file == nil || lock.path == "" {
		return "", fmt.Errorf("operation lock is not held")
	}
	return filepath.Join(filepath.Dir(lock.path), pendingValidatedBuildFilenameV1), nil
}
