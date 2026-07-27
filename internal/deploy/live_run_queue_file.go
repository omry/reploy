package deploy

import (
	"fmt"
	"os"
	"path/filepath"
)

const liveRunQueueFilenameV1 = "live-runs.json"

func (lock *OperationLock) ReadLiveRunQueueV1() (LiveRunQueueV1, bool, error) {
	if lock == nil {
		return LiveRunQueueV1{}, false, fmt.Errorf("read live run queue requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return LiveRunQueueV1{}, false, err
	}
	return readLiveRunQueuePathV1(path)
}

func (lock *OperationLock) CommitLiveRunQueueV1(queue LiveRunQueueV1) error {
	if lock == nil {
		return fmt.Errorf("commit live run queue requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return err
	}
	return commitLiveRunQueuePathV1(path, queue)
}

func (lock *OperationLock) AdmitLiveRunV1(candidate LiveRunV1, wait bool) (LiveRunStatusV1, error) {
	if lock == nil {
		return "", fmt.Errorf("admit live run requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return "", err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return "", err
	}
	updated, status, err := AdmitLiveRunV1(queue, candidate, wait)
	if err != nil {
		return "", err
	}
	if err := commitLiveRunQueuePathV1(path, updated); err != nil {
		return "", err
	}
	return status, nil
}

func (lock *OperationLock) AdmitControlMarkerV1(candidate ControlMarkerV1, wait bool) (LiveRunStatusV1, error) {
	if lock == nil {
		return "", fmt.Errorf("admit control marker requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return "", err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return "", err
	}
	updated, status, err := AdmitControlMarkerV1(queue, candidate, wait)
	if err != nil {
		return "", err
	}
	if err := commitLiveRunQueuePathV1(path, updated); err != nil {
		return "", err
	}
	return status, nil
}

func (lock *OperationLock) RecordLiveRunContainerV1(id string, container string) error {
	if lock == nil {
		return fmt.Errorf("record live run container requires an operation lock")
	}
	if err := ValidateLiveRunIDV1(id); err != nil {
		return err
	}
	if !safeRecoveryIdentity(container) {
		return fmt.Errorf("live run container must be nonempty safe text")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return err
	}
	for index := range queue.Runs {
		run := &queue.Runs[index]
		if run.ID != id {
			continue
		}
		if run.Status != LiveRunStatusActiveV1 {
			return fmt.Errorf("cannot record a container for waiting live run %q", id)
		}
		if run.Container != "" && run.Container != container {
			return fmt.Errorf("live run %q already names container %q", id, run.Container)
		}
		run.Container = container
		return commitLiveRunQueuePathV1(path, queue)
	}
	return fmt.Errorf("live run %q is not outstanding", id)
}

func (lock *OperationLock) RemoveLiveRunV1(id string) (LiveRunQueueV1, bool, error) {
	if lock == nil {
		return LiveRunQueueV1{}, false, fmt.Errorf("remove live run requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return LiveRunQueueV1{}, false, err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return LiveRunQueueV1{}, false, err
	}
	updated, removed, err := RemoveLiveRunV1(queue, id)
	if err != nil || !removed {
		return updated, removed, err
	}
	if err := commitLiveRunQueuePathV1(path, updated); err != nil {
		return LiveRunQueueV1{}, false, err
	}
	return updated, true, nil
}

func (lock *OperationLock) RemoveControlMarkerV1(id string) (LiveRunQueueV1, bool, error) {
	if lock == nil {
		return LiveRunQueueV1{}, false, fmt.Errorf("remove control marker requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return LiveRunQueueV1{}, false, err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return LiveRunQueueV1{}, false, err
	}
	updated, removed, err := RemoveControlMarkerV1(queue, id)
	if err != nil || !removed {
		return updated, removed, err
	}
	if err := commitLiveRunQueuePathV1(path, updated); err != nil {
		return LiveRunQueueV1{}, false, err
	}
	return updated, true, nil
}

func (lock *OperationLock) ActivateReadyControlMarkerV1(id string) error {
	if lock == nil {
		return fmt.Errorf("activate ready control marker requires an operation lock")
	}
	if err := ValidateControlMarkerIDV1(id); err != nil {
		return err
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return err
	}
	for index := range queue.Runs {
		entry := &queue.Runs[index]
		if entry.ID != id || entry.Kind != LiveRunKindControlV1 {
			continue
		}
		if entry.Status != LiveRunStatusReadyV1 {
			return fmt.Errorf("control marker %q is not ready", id)
		}
		entry.Status = LiveRunStatusActiveV1
		return commitLiveRunQueuePathV1(path, queue)
	}
	return fmt.Errorf("control marker %q is not outstanding", id)
}

func (lock *OperationLock) CancelWaitingLiveRunsV1() (LiveRunQueueV1, []LiveRunV1, error) {
	if lock == nil {
		return LiveRunQueueV1{}, nil, fmt.Errorf("cancel waiting live runs requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return LiveRunQueueV1{}, nil, err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return LiveRunQueueV1{}, nil, err
	}
	updated, canceled, err := CancelWaitingLiveRunsV1(queue)
	if err != nil || len(canceled) == 0 {
		return updated, canceled, err
	}
	if err := commitLiveRunQueuePathV1(path, updated); err != nil {
		return LiveRunQueueV1{}, nil, err
	}
	return updated, canceled, nil
}

// RecoverAbandonedControlMarkerV1 removes lifecycle queue entries whose owner
// no longer holds its kernel-backed lease. It is valid while the deployment
// operation lock is held and recovers waiting, ready, and active entries.
func (lock *OperationLock) RecoverAbandonedControlMarkerV1() (ControlMarkerV1, bool, error) {
	if lock == nil {
		return ControlMarkerV1{}, false, fmt.Errorf("recover abandoned control marker requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return ControlMarkerV1{}, false, err
	}
	directory := filepath.Dir(path)
	first := ControlMarkerV1{}
	found := false
	for {
		queue, _, err := readLiveRunQueuePathV1(path)
		if err != nil {
			return ControlMarkerV1{}, false, err
		}
		removedOne := false
		for _, marker := range ControlMarkersV1(queue) {
			abandoned, err := controlLeaseAbandonedV1(directory, marker.ID)
			if err != nil {
				return ControlMarkerV1{}, false, err
			}
			if !abandoned {
				continue
			}
			updated, removed, err := RemoveControlMarkerV1(queue, marker.ID)
			if err != nil {
				return ControlMarkerV1{}, false, err
			}
			if !removed {
				return ControlMarkerV1{}, false, fmt.Errorf("abandoned lifecycle queue entry %q disappeared while the operation lock was held", marker.ID)
			}
			if err := commitLiveRunQueuePathV1(path, updated); err != nil {
				return ControlMarkerV1{}, false, err
			}
			if !found {
				first, found = marker, true
			}
			removedOne = true
			break
		}
		if !removedOne {
			return first, found, nil
		}
	}
}

func (lock *OperationLock) liveRunQueuePathLockedV1() (string, error) {
	if lock.released || lock.file == nil || lock.path == "" {
		return "", fmt.Errorf("operation lock is not held")
	}
	return filepath.Join(filepath.Dir(lock.path), liveRunQueueFilenameV1), nil
}

func readLiveRunQueuePathV1(path string) (LiveRunQueueV1, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return NewLiveRunQueueV1(), false, nil
	}
	if err != nil {
		return LiveRunQueueV1{}, false, fmt.Errorf("inspect live run queue: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return LiveRunQueueV1{}, false, fmt.Errorf("live run queue path must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return LiveRunQueueV1{}, false, fmt.Errorf("read live run queue: %w", err)
	}
	queue, err := DecodeLiveRunQueueV1(content)
	if err != nil {
		return LiveRunQueueV1{}, false, err
	}
	return queue, true, nil
}

func commitLiveRunQueuePathV1(path string, queue LiveRunQueueV1) error {
	content, err := EncodeLiveRunQueueV1(queue)
	if err != nil {
		return err
	}
	if len(queue.Runs) == 0 {
		return removeLiveRunQueuePathV1(path)
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("commit live run queue: %w", err)
	}
	return nil
}

func removeLiveRunQueuePathV1(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect live run queue: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("live run queue path must be a regular file: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove live run queue: %w", err)
	}
	if err := syncAtomicStateDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync live run queue directory: %w", err)
	}
	return nil
}
