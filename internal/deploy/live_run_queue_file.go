package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	candidate.BootSession, err = CurrentBootSessionIDV1()
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
	candidate.BootSession, err = CurrentBootSessionIDV1()
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

// RecoverLiveRunQueueV1 removes entries that cannot belong to a live owner in
// the current host boot session. It never replays work. Container identities
// are transferred atomically into non-scheduling cleanup inventory.
func (lock *OperationLock) RecoverLiveRunQueueV1() (LiveRunRecoveryV1, error) {
	if lock == nil {
		return LiveRunRecoveryV1{}, fmt.Errorf("recover live run queue requires an operation lock")
	}
	session, err := CurrentBootSessionIDV1()
	if err != nil {
		return LiveRunRecoveryV1{}, err
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return LiveRunRecoveryV1{}, err
	}
	return recoverLiveRunQueuePathV1(path, session)
}

func recoverLiveRunQueuePathV1(path string, session string) (LiveRunRecoveryV1, error) {
	if err := validateBootSessionIDV1(session); err != nil {
		return LiveRunRecoveryV1{}, err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return LiveRunRecoveryV1{}, err
	}
	directory := filepath.Dir(path)
	result := cloneLiveRunQueueV1(queue)
	result.Runs = result.Runs[:0]
	recovery := LiveRunRecoveryV1{Removed: []RecoveredLiveRunV1{}}
	for _, entry := range queue.Runs {
		reason := LiveRunRecoveryReasonV1("")
		switch {
		case entry.BootSession == "":
			reason = LiveRunRecoveryLegacyEntryV1
		case entry.BootSession != session:
			reason = LiveRunRecoveryPriorSessionV1
		default:
			abandoned, err := queueEntryLeaseAbandonedV1(directory, entry.ID)
			if err != nil {
				return LiveRunRecoveryV1{}, err
			}
			if abandoned {
				reason = LiveRunRecoveryAbandonedOwnerV1
			}
		}
		if reason == "" {
			result.Runs = append(result.Runs, entry)
			continue
		}
		if reason != LiveRunRecoveryAbandonedOwnerV1 {
			if err := removeQueueEntryLeasePathV1(filepath.Join(directory, entry.ID+controlLeaseSuffixV1)); err != nil {
				return LiveRunRecoveryV1{}, err
			}
		}
		recovery.Removed = append(recovery.Removed, RecoveredLiveRunV1{Run: entry, Reason: reason})
		if entry.Container != "" {
			result.Cleanup = append(result.Cleanup, LiveRunContainerCleanupV1{
				Container: entry.Container,
				RunID:     entry.ID,
				Kind:      entry.Kind,
				Name:      entry.Name,
				Reason:    reason,
			})
		}
	}
	promoteLiveRunsV1(&result)
	sort.Slice(result.Cleanup, func(i, j int) bool {
		return result.Cleanup[i].Container < result.Cleanup[j].Container
	})
	result.Cleanup = deduplicateLiveRunCleanupV1(result.Cleanup)
	if err := ValidateLiveRunQueueV1(result); err != nil {
		return LiveRunRecoveryV1{}, fmt.Errorf("validate recovered live run queue: %w", err)
	}
	if len(recovery.Removed) != 0 {
		if err := commitLiveRunQueuePathV1(path, result); err != nil {
			return LiveRunRecoveryV1{}, err
		}
	}
	retained := make(map[string]bool, len(result.Runs))
	for _, entry := range result.Runs {
		retained[entry.ID] = true
	}
	if err := removeOrphanedQueueEntryLeasesV1(directory, retained); err != nil {
		return LiveRunRecoveryV1{}, err
	}
	return recovery, nil
}

func removeOrphanedQueueEntryLeasesV1(directory string, retained map[string]bool) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("list queue-entry leases for recovery: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) <= len(controlLeaseSuffixV1) ||
			name[len(name)-len(controlLeaseSuffixV1):] != controlLeaseSuffixV1 {
			continue
		}
		id := name[:len(name)-len(controlLeaseSuffixV1)]
		if validateQueueEntryLeaseIDV1(id) != nil || retained[id] {
			continue
		}
		if _, err := queueEntryLeaseAbandonedV1(directory, id); err != nil {
			return fmt.Errorf("recover orphaned queue-entry lease %q: %w", id, err)
		}
	}
	return nil
}

func deduplicateLiveRunCleanupV1(cleanup []LiveRunContainerCleanupV1) []LiveRunContainerCleanupV1 {
	if len(cleanup) < 2 {
		return cleanup
	}
	result := cleanup[:1]
	for _, entry := range cleanup[1:] {
		if entry.Container == result[len(result)-1].Container {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (lock *OperationLock) CompleteLiveRunContainerCleanupV1(container string) (bool, error) {
	if lock == nil {
		return false, fmt.Errorf("complete live run container cleanup requires an operation lock")
	}
	if !safeRecoveryIdentity(container) {
		return false, fmt.Errorf("cleanup container must be nonempty safe text")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return false, err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return false, err
	}
	for index, entry := range queue.Cleanup {
		if entry.Container != container {
			continue
		}
		queue.Cleanup = append(queue.Cleanup[:index], queue.Cleanup[index+1:]...)
		if err := commitLiveRunQueuePathV1(path, queue); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// CompleteLiveRunWithContainerCleanupV1 atomically removes a completed live
// run while retaining its exact container identity for a later cleanup retry.
func (lock *OperationLock) CompleteLiveRunWithContainerCleanupV1(id string, container string) (bool, error) {
	if lock == nil {
		return false, fmt.Errorf("complete live run with container cleanup requires an operation lock")
	}
	if err := ValidateLiveRunIDV1(id); err != nil {
		return false, err
	}
	if !safeRecoveryIdentity(container) {
		return false, fmt.Errorf("cleanup container must be nonempty safe text")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.liveRunQueuePathLockedV1()
	if err != nil {
		return false, err
	}
	queue, _, err := readLiveRunQueuePathV1(path)
	if err != nil {
		return false, err
	}
	run := LiveRunV1{}
	for _, entry := range queue.Runs {
		if entry.ID == id && entry.Kind != LiveRunKindControlV1 {
			run = entry
			break
		}
	}
	updated, removed, err := RemoveLiveRunV1(queue, id)
	if err != nil || !removed {
		return removed, err
	}
	if run.Container != "" && run.Container != container {
		return false, fmt.Errorf("live run %q names container %q, not cleanup container %q", id, run.Container, container)
	}
	updated.Cleanup = append(updated.Cleanup, LiveRunContainerCleanupV1{
		Container: container,
		RunID:     run.ID,
		Kind:      run.Kind,
		Name:      run.Name,
		Reason:    LiveRunRecoveryCleanupFailedV1,
	})
	sort.Slice(updated.Cleanup, func(i, j int) bool {
		return updated.Cleanup[i].Container < updated.Cleanup[j].Container
	})
	updated.Cleanup = deduplicateLiveRunCleanupV1(updated.Cleanup)
	if err := commitLiveRunQueuePathV1(path, updated); err != nil {
		return false, err
	}
	return true, nil
}

// RecoverAbandonedControlMarkerV1 removes lifecycle queue entries whose owner
// no longer holds its kernel-backed lease. It is valid while the deployment
// operation lock is held and recovers waiting, ready, and active entries.
func (lock *OperationLock) RecoverAbandonedControlMarkerV1() (ControlMarkerV1, bool, error) {
	recovery, err := lock.RecoverLiveRunQueueV1()
	if err != nil {
		return ControlMarkerV1{}, false, err
	}
	for _, removed := range recovery.Removed {
		if removed.Run.Kind == LiveRunKindControlV1 {
			return ControlMarkerV1{
				ID:                  removed.Run.ID,
				Operation:           ControlOperationV1(removed.Run.Name),
				GenerationReference: removed.Run.GenerationReference,
				BootSession:         removed.Run.BootSession,
				Status:              removed.Run.Status,
			}, true, nil
		}
	}
	return ControlMarkerV1{}, false, nil
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
	if len(queue.Runs) == 0 && len(queue.Cleanup) == 0 {
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
