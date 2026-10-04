package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

var ErrStagedTerminalRemoval = errors.New("staged terminal removal is pending; retry stage --remove")

// RequireWritable enforces terminal staged removal for callers already holding
// a lock, including locks obtained for read-only diagnostics. Retirement has
// one scoped continuation, bound to the unchanged canonical state at this path.
func (lock *OperationLock) RequireWritable() error {
	if lock == nil {
		return fmt.Errorf("writer requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	return lock.requireWritableLocked()
}

// RequireOwnerWritable excludes publication and new work even on the scoped
// retirement lock. Its permission allows retirement progress, never revival.
func (lock *OperationLock) RequireOwnerWritable() error {
	if lock == nil {
		return fmt.Errorf("writer requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	return lock.requireOwnerWritableLocked()
}

func (lock *OperationLock) requireOwnerWritableLocked() error {
	if err := lock.requireWritableLocked(); err != nil {
		return err
	}
	if len(lock.terminalRemovalState) != 0 {
		return fmt.Errorf("staged terminal removal cannot admit owner writes")
	}
	return nil
}

func (lock *OperationLock) requireWritableLocked() error {
	path, err := lock.statePathV1Locked()
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if len(lock.terminalRemovalState) != 0 {
			return fmt.Errorf("terminal removal state authority is missing")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("writer state path must be a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(lock.terminalRemovalState) != 0 && !bytes.Equal(content, lock.terminalRemovalState) {
		return fmt.Errorf("terminal removal state identity changed")
	}
	guarded, err := inspectStagedTerminalRemovalStateV1(content)
	if err != nil {
		return err
	}
	if !guarded {
		return nil
	}
	if len(lock.terminalRemovalState) != 0 {
		return nil
	}
	return ErrStagedTerminalRemoval
}

func inspectStagedTerminalRemovalStateV1(content []byte) (bool, error) {
	// Malformed JSON cannot establish that terminal authority is absent. Valid
	// unguarded legacy state remains subject to its existing reader/recovery policy.
	var probe struct {
		Staging map[string]json.RawMessage `json:"staging"`
	}
	if probeErr := json.Unmarshal(content, &probe); probeErr != nil {
		return false, fmt.Errorf("malformed terminal removal state: %w", probeErr)
	}
	if _, present := probe.Staging["terminal_removal"]; !present {
		return false, nil
	}
	state, err := DecodeStateV1(content)
	if err != nil {
		return false, fmt.Errorf("terminal removal authority: %w", err)
	}
	if state.Staging == nil || !state.Staging.TerminalRemoval {
		return false, fmt.Errorf("invalid terminal removal authority")
	}
	return true, nil
}

func preflightStagedWriterV1(dir string) error {
	path := filepath.Join(dir, ".reploy", stateFilenameV1)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("writer state path must be a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	guarded, err := inspectStagedTerminalRemovalStateV1(content)
	if err != nil {
		return err
	}
	if guarded {
		return ErrStagedTerminalRemoval
	}
	return nil
}

// AcquireStagedRemovalOperationLock is the explicit removal continuation. It
// acquires the same kernel lock; it does not grant permission to ordinary writes.
// A guarded retry must retain canonical staged state and no publication intent.
func AcquireStagedRemovalOperationLock(ctx context.Context, dir string) (*OperationLock, error) {
	lock, err := acquireOperationLock(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	state, found, err := lock.ReadStateV1()
	if err == nil && found && state.Staging != nil && state.Staging.TerminalRemoval {
		err = lock.BeginStagedRemovalV1(state)
	}
	if err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return lock, nil
}

// BeginStagedRemovalV1 publishes the durable guard before any retirement effect.
// A failed write, including an applied-then-error write, authorizes no effects.
// Retained state and locks remain the image inventory; the guard holds none.
func (lock *OperationLock) BeginStagedRemovalV1(expected StateV1) error {
	if lock == nil {
		return fmt.Errorf("staged removal requires an operation lock")
	}
	if _, pending, err := lock.ReadPendingBuild(); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("staged removal requires completed publication")
	}
	if _, pending, err := lock.ReadPendingValidatedBuildV1(); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("staged removal requires completed validated publication")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.statePathV1Locked()
	if err != nil {
		return err
	}
	current, found, err := readStateV1Path(path)
	if err != nil {
		return err
	}
	if !found || current.Staging == nil || current.Deployment != nil || !reflect.DeepEqual(current, expected) {
		return fmt.Errorf("staged removal state changed or is not staged")
	}
	staging := *current.Staging
	staging.TerminalRemoval = true
	current.Staging = &staging
	content, err := EncodeStateV1(current)
	if err != nil {
		return err
	}
	if !expected.Staging.TerminalRemoval {
		if err := writeAtomicStateFile(path, content, 0o600); err != nil {
			return fmt.Errorf("commit staged removal guard: %w", err)
		}
	} else if err := syncAtomicStateFileDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("confirm staged removal guard durability: %w", err)
	}
	observed, found, err := readStateV1Path(path)
	if err != nil || !found || !reflect.DeepEqual(current, observed) {
		return fmt.Errorf("staged removal guard was not confirmed: %v", err)
	}
	lock.terminalRemovalState = content
	return nil
}

// A removal continuation must preserve the exact state authorizing current
// reference retirement. Queue and validated retirement progress live separately.
func (lock *OperationLock) requireStateCommitAllowedLocked() error {
	if err := lock.requireWritableLocked(); err != nil {
		return err
	}
	if len(lock.terminalRemovalState) != 0 {
		return fmt.Errorf("terminal removal cannot replace retained state")
	}
	return nil
}
