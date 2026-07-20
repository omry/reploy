package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
)

const stateFilenameV1 = "state.json"

func (lock *OperationLock) ReadStateV1() (StateV1, bool, error) {
	if lock == nil {
		return StateV1{}, false, fmt.Errorf("read state requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.statePathV1Locked()
	if err != nil {
		return StateV1{}, false, err
	}
	return readStateV1Path(path)
}

// CommitStateV1 atomically replaces state only if its current generation still
// matches the caller's locked observation. The operation lock is the primary
// serialization boundary; the comparison also makes publication intent
// explicit and testable.
func (lock *OperationLock) CommitStateV1(expected *EnvironmentGenerationState, state StateV1) error {
	if lock == nil {
		return fmt.Errorf("commit state requires an operation lock")
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
	var observed *EnvironmentGenerationState
	if found {
		observed = current.Current
	}
	if !reflect.DeepEqual(observed, expected) {
		return fmt.Errorf("state current generation changed before commit")
	}
	content, err := EncodeStateV1(state)
	if err != nil {
		return err
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}

func (lock *OperationLock) statePathV1Locked() (string, error) {
	if lock.released || lock.file == nil || lock.path == "" {
		return "", fmt.Errorf("operation lock is not held")
	}
	return filepath.Join(filepath.Dir(lock.path), stateFilenameV1), nil
}

func readStateV1Path(path string) (StateV1, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return StateV1{}, false, nil
	}
	if err != nil {
		return StateV1{}, false, fmt.Errorf("inspect state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return StateV1{}, false, fmt.Errorf("state path must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return StateV1{}, false, fmt.Errorf("read state: %w", err)
	}
	state, err := DecodeStateV1(content)
	if err != nil {
		return StateV1{}, false, err
	}
	return state, true, nil
}
