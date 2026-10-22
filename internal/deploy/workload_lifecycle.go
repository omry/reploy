package deploy

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const workloadLifecycleFilenameV1 = "workload-lifecycle.json"

// WorkloadLifecycleV1 is controller-owned, secret-free evidence for one
// process startup. A transfer claim is durable before the first payload byte.
// This file is masked from applications together with all .reploy metadata.
type WorkloadLifecycleV1 struct {
	Schema          string `json:"schema"`
	State           string `json:"state"`
	Endpoint        string `json:"endpoint"`
	Runtime         string `json:"runtime"`
	ContainerID     string `json:"container_id"`
	StartedAt       string `json:"started_at"`
	TransferClaimed bool   `json:"transfer_claimed"`
	StartupComplete bool   `json:"startup_complete"`
	HandoffComplete bool   `json:"handoff_complete"`
}

func (record WorkloadLifecycleV1) Validate() error {
	if record.Schema != "workload-lifecycle-v1" {
		return fmt.Errorf("invalid workload lifecycle schema")
	}
	switch record.State {
	case "down", "starting", "up", "stopping":
	default:
		return fmt.Errorf("invalid workload lifecycle state")
	}
	for _, value := range []string{record.Endpoint, record.Runtime} {
		if len(value) != 64 {
			return fmt.Errorf("invalid workload lifecycle binding")
		}
		if _, err := hex.DecodeString(value); err != nil {
			return fmt.Errorf("invalid workload lifecycle binding")
		}
	}
	if record.ContainerID != "" {
		if len(record.ContainerID) != 64 {
			return fmt.Errorf("invalid workload lifecycle container")
		}
		if _, err := hex.DecodeString(record.ContainerID); err != nil {
			return fmt.Errorf("invalid workload lifecycle container")
		}
		instant, err := time.Parse(time.RFC3339Nano, record.StartedAt)
		if err != nil || instant.IsZero() {
			return fmt.Errorf("invalid workload lifecycle start time")
		}
	} else if record.StartedAt != "" || record.TransferClaimed || record.HandoffComplete || record.StartupComplete || record.State == "up" {
		return fmt.Errorf("workload lifecycle evidence requires a process identity")
	}
	if record.State == "up" && !record.StartupComplete {
		return fmt.Errorf("up requires completed application startup")
	}
	if record.TransferClaimed && record.ContainerID == "" || record.HandoffComplete && !record.TransferClaimed {
		return fmt.Errorf("invalid workload lifecycle transfer evidence")
	}
	return nil
}

func (lock *OperationLock) ReadWorkloadLifecycleV1() (WorkloadLifecycleV1, bool, error) {
	if lock == nil {
		return WorkloadLifecycleV1{}, false, fmt.Errorf("workload lifecycle requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.requireHeldLocked(); err != nil {
		return WorkloadLifecycleV1{}, false, err
	}
	content, found, err := readWorkloadLifecycleFileV1(filepath.Join(filepath.Dir(lock.path), workloadLifecycleFilenameV1))
	if err != nil || !found {
		return WorkloadLifecycleV1{}, found, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var record WorkloadLifecycleV1
	if err := decoder.Decode(&record); err != nil {
		return record, true, fmt.Errorf("decode workload lifecycle: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return record, true, fmt.Errorf("workload lifecycle has trailing data")
	}
	return record, true, record.Validate()
}

func (lock *OperationLock) WriteWorkloadLifecycleV1(record WorkloadLifecycleV1) error {
	if lock == nil {
		return fmt.Errorf("workload lifecycle requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.requireWritableLocked(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(lock.path), workloadLifecycleFilenameV1)
	// Reject unsafe pre-existing files instead of silently repairing them.
	if _, _, err := readWorkloadLifecycleFileV1(path); err != nil {
		return err
	}
	content, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := writeAtomicStateFile(path, content, os.FileMode(0o600)); err != nil {
		return fmt.Errorf("write workload lifecycle: %w", err)
	}
	return nil
}
