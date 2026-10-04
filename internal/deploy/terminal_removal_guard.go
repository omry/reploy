package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

func (lock *OperationLock) requireHeldLocked() error {
	if lock.released || lock.file == nil || lock.path == "" {
		return fmt.Errorf("operation lock is not held")
	}
	for _, entry := range []struct {
		path     string
		original os.FileInfo
	}{
		{filepath.Dir(filepath.Dir(lock.path)), lock.directory},
		{filepath.Dir(lock.path), lock.stateDirectory},
	} {
		current, err := os.Lstat(entry.path)
		if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(entry.original, current) {
			return fmt.Errorf("operation lock directory identity changed: %s", entry.path)
		}
	}
	current, err := os.Lstat(lock.path)
	if err != nil {
		return fmt.Errorf("inspect held operation lock: %w", err)
	}
	if !current.Mode().IsRegular() {
		return fmt.Errorf("operation lock file identity changed")
	}
	// File.Stat captures Windows file IDs without reopening with exclusive
	// sharing, which lazy Lstat identity loading cannot do for a held file.
	pathFile, err := os.Open(lock.path)
	if err != nil {
		return err
	}
	defer pathFile.Close()
	current, err = pathFile.Stat()
	if err != nil {
		return err
	}
	held, err := lock.file.Stat()
	if err != nil || !os.SameFile(held, current) {
		return fmt.Errorf("operation lock file identity changed")
	}
	return nil
}

// RequireWritable rejects terminal authority even for a caller-held diagnostic
// lock or a lock explicitly bound to removal. New ownership is never retirement.
func (lock *OperationLock) RequireWritable() error {
	if lock == nil {
		return fmt.Errorf("operation lock is not held")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	return lock.requireWritableLocked()
}

func (lock *OperationLock) requireWritableLocked() error {
	guard, err := lock.terminalStateLocked()
	if err != nil {
		return err
	}
	if len(guard) != 0 || len(lock.retirementState) != 0 {
		return fmt.Errorf("deployment is guarded for terminal removal")
	}
	return nil
}

// RequireRetirement permits existing cleanup operations under unguarded
// authority or an explicit continuation of the exact retained terminal state.
func (lock *OperationLock) RequireRetirement() error {
	if lock == nil {
		return fmt.Errorf("operation lock is not held")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	return lock.requireRetirementLocked()
}

func (lock *OperationLock) requireRetirementLocked() error {
	guard, err := lock.terminalStateLocked()
	if err != nil {
		return err
	}
	if len(guard) == 0 && len(lock.retirementState) == 0 {
		return nil
	}
	if len(guard) == 0 || !bytes.Equal(guard, lock.retirementState) {
		return fmt.Errorf("terminal removal requires unchanged explicitly bound authority")
	}
	return nil
}

// Inspect only the guard envelope for ordinary admission. Recognized raw
// legacy staging state without a guard remains available to its recovery API.
func (lock *OperationLock) terminalStateLocked() ([]byte, error) {
	if err := lock.requireHeldLocked(); err != nil {
		return nil, err
	}
	return readTerminalStateV1(filepath.Join(filepath.Dir(lock.path), stateFilenameV1))
}

func readTerminalStateV1(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("terminal state must be a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(content, &envelope); err != nil {
		return nil, fmt.Errorf("inspect terminal state envelope: %w", err)
	}
	present := false
	for key := range envelope {
		present = present || strings.EqualFold(key, "terminal_removal")
	}
	if !present {
		return nil, nil
	}
	state, err := DecodeStateV1(content)
	if err != nil {
		return nil, fmt.Errorf("validate terminal authority: %w", err)
	}
	if !state.TerminalRemoval {
		return nil, fmt.Errorf("terminal guard must be committed true")
	}
	return content, nil
}

// BeginTerminalRemovalV1 closes ordinary admission only after publication
// recovery. An uncertain write grants no retirement permission in this attempt.
func (lock *OperationLock) BeginTerminalRemovalV1(expected StateV1) error {
	if lock == nil {
		return fmt.Errorf("terminal removal requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.requireWritableLocked(); err != nil {
		return err
	}
	if expected.TerminalRemoval {
		return fmt.Errorf("begin terminal removal requires unguarded authority")
	}
	if err := lock.requireTerminalPublicationCompleteLocked(); err != nil {
		return err
	}
	if err := lock.requireExactTerminalStateLocked(expected); err != nil {
		return err
	}
	expected.TerminalRemoval = true
	content, err := EncodeStateV1(expected)
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(lock.path), stateFilenameV1)
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("commit terminal guard: %w", err)
	}
	return lock.bindTerminalStateLocked(expected)
}

// ResumeTerminalRemovalV1 is explicit removal retry, never ordinary admission.
// It rereads exact retained state and confirms its directory durability before
// permitting cleanup; callers validate retained owner inventories separately.
func (lock *OperationLock) ResumeTerminalRemovalV1(expected StateV1) error {
	if lock == nil {
		return fmt.Errorf("terminal removal requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.requireHeldLocked(); err != nil {
		return err
	}
	if err := lock.requireTerminalPublicationCompleteLocked(); err != nil {
		return err
	}
	if err := lock.requireExactTerminalStateLocked(expected); err != nil {
		return err
	}
	if err := syncAtomicStateFileDirectory(filepath.Dir(lock.path)); err != nil {
		return fmt.Errorf("confirm terminal guard durability: %w", err)
	}
	return lock.bindTerminalStateLocked(expected)
}

func (lock *OperationLock) requireTerminalPublicationCompleteLocked() error {
	for _, name := range []string{pendingBuildFilename, pendingValidatedBuildFilenameV1} {
		if _, err := os.Lstat(filepath.Join(filepath.Dir(lock.path), name)); !os.IsNotExist(err) {
			return fmt.Errorf("terminal removal requires completed publication: %s", name)
		}
	}
	return nil
}

func (lock *OperationLock) requireExactTerminalStateLocked(expected StateV1) error {
	if err := lock.requireHeldLocked(); err != nil {
		return err
	}
	content, err := EncodeStateV1(expected)
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(lock.path), stateFilenameV1)
	actual, found, err := readStateV1Path(path)
	if err != nil || !found {
		return fmt.Errorf("terminal removal requires canonical retained state: %v", err)
	}
	encoded, err := EncodeStateV1(actual)
	if err != nil || !bytes.Equal(encoded, content) {
		return fmt.Errorf("terminal removal retained state changed")
	}
	return nil
}

func (lock *OperationLock) bindTerminalStateLocked(expected StateV1) error {
	if !expected.TerminalRemoval {
		return fmt.Errorf("terminal continuation requires guarded authority")
	}
	if err := lock.requireExactTerminalStateLocked(expected); err != nil {
		return err
	}
	content, err := EncodeStateV1(expected)
	if err != nil {
		return err
	}
	lock.retirementState = content
	return nil
}

// A terminal continuation may persist only the existing validated owner's
// monotonic discard/cleanup progress, never install a new candidate or owner.
func (lock *OperationLock) requireValidatedRetirementLocked(next ValidatedBuildV1) error {
	guard, err := lock.terminalStateLocked()
	if err != nil {
		return err
	}
	if len(guard) == 0 && len(lock.retirementState) == 0 {
		return nil
	}
	if err := lock.requireRetirementLocked(); err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(lock.path), validatedBuildFilenameV1)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("validated retirement requires an existing regular record")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	previous, err := DecodeValidatedBuildV1(content)
	if err != nil {
		return err
	}
	if !next.Discarded || !next.PendingStorageCleanup {
		return fmt.Errorf("terminal validated progress must remain discarded")
	}
	expected := previous
	expected.Discarded = true
	expected.PendingStorageCleanup = true
	expected.PendingCleanup = next.PendingCleanup
	if !reflect.DeepEqual(expected, next) {
		return fmt.Errorf("terminal validated progress cannot replace owner authority")
	}
	allowed := append([]ValidatedBuildReferenceV1{}, previous.PendingCleanup...)
	if !previous.Discarded {
		allowed = append(allowed, ValidatedBuildReferenceV1{Image: previous.Image, ImageReference: previous.ImageReference})
		if previous.Companion != nil {
			allowed = append(allowed, ValidatedBuildReferenceV1{Image: previous.Companion.Image, ImageReference: previous.Companion.Reference, CompanionOwner: previous.Owner})
		}
		sort.Slice(allowed, func(i, j int) bool { return allowed[i].ImageReference < allowed[j].ImageReference })
		if !reflect.DeepEqual(allowed, next.PendingCleanup) {
			return fmt.Errorf("terminal discard must retain the complete owner inventory")
		}
	}
	for _, item := range next.PendingCleanup {
		found := false
		for _, old := range allowed {
			found = found || reflect.DeepEqual(item, old)
		}
		if !found {
			return fmt.Errorf("terminal cleanup cannot add owner authority")
		}
	}
	return nil
}

// Terminal cleanup cancels queued work rather than implicitly promoting it.
// Existing active owners and their cleanup inventory remain available.
func (lock *OperationLock) commitRemovalQueueLockedV1(path string, previous LiveRunQueueV1, next *LiveRunQueueV1) error {
	if len(lock.retirementState) != 0 {
		retained := make([]LiveRunV1, 0, len(next.Runs))
		for index := range next.Runs {
			for _, old := range previous.Runs {
				if old.ID == next.Runs[index].ID && old.Status == LiveRunStatusActiveV1 {
					retained = append(retained, next.Runs[index])
					break
				}
			}
		}
		next.Runs = retained
	}
	return commitLiveRunQueuePathV1(path, *next)
}
