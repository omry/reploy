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
	"strings"
)

var ErrStagedTerminalRemoval = errors.New("terminal removal is pending; retry directory removal")

// RequireWritable enforces terminal directory removal for callers already holding
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
		return fmt.Errorf("terminal removal cannot admit owner writes")
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
		Staging    map[string]json.RawMessage `json:"staging"`
		Deployment map[string]json.RawMessage `json:"deployment"`
	}
	if probeErr := json.Unmarshal(content, &probe); probeErr != nil {
		return false, fmt.Errorf("malformed terminal removal state: %w", probeErr)
	}
	if err := rejectDuplicateStagedRemovalOwnersV1(content); err != nil {
		return false, err
	}
	staged := hasStagedTerminalRemovalMemberV1(probe.Staging)
	installed := hasStagedTerminalRemovalMemberV1(probe.Deployment)
	if !staged && !installed {
		return false, nil
	}
	state, err := DecodeStateV1(content)
	if err != nil {
		return false, fmt.Errorf("terminal removal authority: %w", err)
	}
	if !(state.Staging != nil && state.Staging.TerminalRemoval || state.Deployment != nil && state.Deployment.TerminalRemoval) {
		return false, fmt.Errorf("invalid terminal removal authority")
	}
	return true, nil
}

// hasStagedTerminalRemovalMemberV1 mirrors encoding/json's case-folded
// matching of terminal_removal on typed state fields. The caller strictly
// decodes every recognized spelling before treating the state as guarded.
func hasStagedTerminalRemovalMemberV1(fields map[string]json.RawMessage) bool {
	for name := range fields {
		if strings.EqualFold(name, "terminal_removal") {
			return true
		}
	}
	return false
}

// Duplicate top-level owner fields make the terminal guard ambiguous: JSON
// decoding keeps only the last value, while retained state authority may still
// be present in an earlier value. Decoder.Token returns unescaped member names,
// and EqualFold mirrors encoding/json's case-folded struct-field matching.
func rejectDuplicateStagedRemovalOwnersV1(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	root, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("malformed terminal removal state: %w", err)
	}
	if delimiter, ok := root.(json.Delim); !ok || delimiter != '{' {
		return nil
	}

	stagingSeen, deploymentSeen := false, false
	for decoder.More() {
		member, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("malformed terminal removal state: %w", err)
		}
		name, ok := member.(string)
		if !ok {
			return fmt.Errorf("malformed terminal removal state: expected an object member")
		}
		switch {
		case strings.EqualFold(name, "staging"):
			if stagingSeen {
				return fmt.Errorf("ambiguous terminal removal authority: duplicate staging member")
			}
			stagingSeen = true
		case strings.EqualFold(name, "deployment"):
			if deploymentSeen {
				return fmt.Errorf("ambiguous terminal removal authority: duplicate deployment member")
			}
			deploymentSeen = true
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("malformed terminal removal state: %w", err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("malformed terminal removal state: %w", err)
	}
	return nil
}

// AcquireInstalledRemovalOperationLock grants only the exact installed removal
// continuation. Ordinary acquisitions and caller-held owner writes stay closed.
func AcquireInstalledRemovalOperationLock(ctx context.Context, dir string) (*OperationLock, error) {
	lock, err := acquireOperationLock(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	state, found, err := lock.ReadStateV1()
	if err == nil && found && state.Deployment != nil && state.Deployment.TerminalRemoval {
		err = lock.BeginInstalledRemovalV1(state)
	}
	if err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return lock, nil
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
	return lock.beginTerminalRemovalV1(expected, false)
}

// BeginInstalledRemovalV1 uses the same durable exact-state guard as staging.
// Installed facts and current ownership remain intact for original-path and
// existing tombstone/control retries.
func (lock *OperationLock) BeginInstalledRemovalV1(expected StateV1) error {
	return lock.beginTerminalRemovalV1(expected, true)
}

func (lock *OperationLock) beginTerminalRemovalV1(expected StateV1, installed bool) error {
	if lock == nil {
		return fmt.Errorf("terminal removal requires an operation lock")
	}
	if _, pending, err := lock.ReadPendingBuild(); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("terminal removal requires completed publication")
	}
	if _, pending, err := lock.ReadPendingValidatedBuildV1(); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("terminal removal requires completed validated publication")
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
	if !found || !reflect.DeepEqual(current, expected) {
		return fmt.Errorf("terminal removal state changed")
	}
	guarded := false
	if installed {
		if current.Deployment == nil || current.Staging != nil || current.Current == nil {
			return fmt.Errorf("terminal removal state is not installed")
		}
		deployment := *current.Deployment
		guarded = deployment.TerminalRemoval
		deployment.TerminalRemoval = true
		current.Deployment = &deployment
	} else {
		if current.Staging == nil || current.Deployment != nil {
			return fmt.Errorf("terminal removal state is not staged")
		}
		staging := *current.Staging
		guarded = staging.TerminalRemoval
		staging.TerminalRemoval = true
		current.Staging = &staging
	}
	content, err := EncodeStateV1(current)
	if err != nil {
		return err
	}
	if !guarded {
		if err := writeAtomicStateFile(path, content, 0o600); err != nil {
			return fmt.Errorf("commit terminal removal guard: %w", err)
		}
	} else if err := syncAtomicStateFileDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("confirm terminal removal guard durability: %w", err)
	}
	observed, found, err := readStateV1Path(path)
	if err != nil || !found || !reflect.DeepEqual(current, observed) {
		return fmt.Errorf("terminal removal guard was not confirmed: %v", err)
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
