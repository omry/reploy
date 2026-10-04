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

const PendingValidatedBuildSchemaV1 = "pending-validated-build-v1"
const pendingValidatedBuildFilenameV1 = "pending-validated-build.json"

// The committed validated record selects previous or candidate ownership.
// This intent is durable before either candidate reference is created.
type PendingValidatedBuildV1 struct {
	Schema    string            `json:"schema"`
	Previous  *ValidatedBuildV1 `json:"previous"`
	Candidate ValidatedBuildV1  `json:"candidate"`
}

func ValidatePendingValidatedBuildV1(intent PendingValidatedBuildV1) error {
	if intent.Schema != PendingValidatedBuildSchemaV1 {
		return fmt.Errorf("pending validated build schema %q is unsupported", intent.Schema)
	}
	if err := ValidateValidatedBuildV1(intent.Candidate); err != nil {
		return fmt.Errorf("pending validated candidate: %w", err)
	}
	if intent.Candidate.Discarded || !intent.Candidate.PendingStorageCleanup {
		return fmt.Errorf("pending validated candidate must be live with pending storage cleanup")
	}
	if intent.Previous != nil {
		if err := ValidateValidatedBuildV1(*intent.Previous); err != nil {
			return fmt.Errorf("pending validated previous: %w", err)
		}
		retained := append([]ValidatedBuildReferenceV1(nil), intent.Previous.PendingCleanup...)
		if !intent.Previous.Discarded {
			retained = append(retained, ValidatedBuildReferenceV1{Image: intent.Previous.Image, ImageReference: intent.Previous.ImageReference})
			if intent.Previous.Companion != nil {
				retained = append(retained, ValidatedBuildReferenceV1{Image: intent.Previous.Companion.Image, ImageReference: intent.Previous.Companion.Reference, CompanionOwner: intent.Previous.Owner})
			}
		}
		for _, old := range retained {
			if old.ImageReference == intent.Candidate.ImageReference || (intent.Candidate.Companion != nil && old.ImageReference == intent.Candidate.Companion.Reference) {
				return fmt.Errorf("pending validated candidate conflicts with previous ownership")
			}
		}
	}
	return nil
}

func EncodePendingValidatedBuildV1(intent PendingValidatedBuildV1) ([]byte, error) {
	if err := ValidatePendingValidatedBuildV1(intent); err != nil {
		return nil, err
	}
	return canonical.Marshal(intent)
}

func DecodePendingValidatedBuildV1(content []byte) (PendingValidatedBuildV1, error) {
	var intent PendingValidatedBuildV1
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return intent, fmt.Errorf("decode pending validated build: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return intent, fmt.Errorf("pending validated build must contain one JSON value")
	}
	encoded, err := EncodePendingValidatedBuildV1(intent)
	if err != nil {
		return intent, err
	}
	if !bytes.Equal(encoded, content) {
		return intent, fmt.Errorf("pending validated build is not canonical JSON")
	}
	return intent, nil
}

func (lock *OperationLock) pendingValidatedBuildPathLocked() (string, error) {
	if err := lock.requireHeldLocked(); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(lock.path), pendingValidatedBuildFilenameV1), nil
}

func (lock *OperationLock) ReadPendingValidatedBuildV1() (PendingValidatedBuildV1, bool, error) {
	if lock == nil {
		return PendingValidatedBuildV1{}, false, fmt.Errorf("pending validated build requires an operation lock")
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
		return PendingValidatedBuildV1{}, false, err
	}
	if !info.Mode().IsRegular() {
		return PendingValidatedBuildV1{}, false, fmt.Errorf("pending validated build must be a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return PendingValidatedBuildV1{}, false, err
	}
	intent, err := DecodePendingValidatedBuildV1(content)
	return intent, err == nil, err
}

func (lock *OperationLock) WritePendingValidatedBuildV1(intent PendingValidatedBuildV1) error {
	if lock == nil {
		return fmt.Errorf("pending validated build requires an operation lock")
	}
	content, err := EncodePendingValidatedBuildV1(intent)
	if err != nil {
		return err
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.requireWritableLocked(); err != nil {
		return err
	}
	path, err := lock.pendingValidatedBuildPathLocked()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("pending validated build already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeAtomicStateFile(path, content, 0o600)
}

func (lock *OperationLock) RemovePendingValidatedBuildV1() error {
	if lock == nil {
		return fmt.Errorf("pending validated build requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.requireRetirementLocked(); err != nil {
		return err
	}
	path, err := lock.pendingValidatedBuildPathLocked()
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("pending validated build must be a regular file")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncAtomicStateFileDirectory(filepath.Dir(path))
}
