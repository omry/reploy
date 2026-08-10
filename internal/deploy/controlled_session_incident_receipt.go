package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omry/reploy/internal/canonical"
)

const (
	ControlledSessionIncidentReceiptSchemaV1  = "controlled-session-incident-v1"
	ControlledSessionIncidentReceiptLimitV1   = 8 * 1024
	controlledSessionIncidentRetentionLimitV1 = 64
)

type ControlledSessionIncidentTriggerV1 string

const (
	ControlledSessionIncidentParentLostV1 ControlledSessionIncidentTriggerV1 = "parent-lost"
)

type ControlledSessionIncidentCleanupStatusV1 string

const (
	ControlledSessionIncidentCleanupSucceededV1 ControlledSessionIncidentCleanupStatusV1 = "succeeded"
	ControlledSessionIncidentCleanupFailedV1    ControlledSessionIncidentCleanupStatusV1 = "failed"
)

type ControlledSessionIncidentResourceStatusV1 string

const (
	ControlledSessionIncidentResourceVerifiedAbsentV1 ControlledSessionIncidentResourceStatusV1 = "verified-absent"
	ControlledSessionIncidentResourceCleanupFailedV1  ControlledSessionIncidentResourceStatusV1 = "cleanup-failed"
)

type ControlledSessionIncidentRecoveryActionV1 string

const (
	ControlledSessionIncidentRecoveryNoneV1          ControlledSessionIncidentRecoveryActionV1 = "none"
	ControlledSessionIncidentRecoveryNextOperationV1 ControlledSessionIncidentRecoveryActionV1 = "retry-next-operation"
)

// ControlledSessionIncidentContainerV1 contains only immutable ownership and
// the watchdog's allowlisted cleanup outcome. It deliberately has no free-form
// diagnostic or container-output field.
type ControlledSessionIncidentContainerV1 struct {
	Role                string                                    `json:"role"`
	ID                  string                                    `json:"id"`
	DeploymentID        string                                    `json:"deployment_id"`
	GenerationReference string                                    `json:"generation_reference"`
	BuildIdentity       string                                    `json:"build_identity"`
	CleanupStatus       ControlledSessionIncidentResourceStatusV1 `json:"cleanup_status"`
}

// ControlledSessionIncidentReceiptV1 is the bounded durable evidence written
// by the session watchdog after loss of its parent. The fixed fields cannot
// carry PTY bytes, environment values, secrets, arbitrary logs, or raw Docker
// output.
type ControlledSessionIncidentReceiptV1 struct {
	Schema               string                                    `json:"schema"`
	LiveRunID            string                                    `json:"live_run_id"`
	BootSession          string                                    `json:"boot_session"`
	RecordedAt           string                                    `json:"recorded_at"`
	Trigger              ControlledSessionIncidentTriggerV1        `json:"trigger"`
	Controller           ControlledSessionIncidentContainerV1      `json:"controller"`
	Workload             ControlledSessionIncidentContainerV1      `json:"workload"`
	ChannelCleanupStatus ControlledSessionIncidentResourceStatusV1 `json:"channel_cleanup_status"`
	CleanupStatus        ControlledSessionIncidentCleanupStatusV1  `json:"cleanup_status"`
	RecoveryAction       ControlledSessionIncidentRecoveryActionV1 `json:"recovery_action"`
}

type ControlledSessionIncidentReceiptTargetV1 struct {
	path string
	file *os.File
}

func (target *ControlledSessionIncidentReceiptTargetV1) Path() string {
	if target == nil {
		return ""
	}
	return target.path
}

func (target *ControlledSessionIncidentReceiptTargetV1) File() *os.File {
	if target == nil {
		return nil
	}
	return target.file
}

func (target *ControlledSessionIncidentReceiptTargetV1) Close() error {
	if target == nil || target.file == nil {
		return nil
	}
	err := target.file.Close()
	target.file = nil
	if err != nil {
		return fmt.Errorf("close controlled-session incident receipt target: %w", err)
	}
	return nil
}

func (target *ControlledSessionIncidentReceiptTargetV1) Remove() error {
	if target == nil {
		return nil
	}
	closeErr := target.Close()
	removeErr := os.Remove(target.path)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		removeErr = fmt.Errorf("remove controlled-session incident receipt target: %w", removeErr)
	} else {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}

func ControlledSessionIncidentReceiptPathV1(channelDirectory string, liveRunID string) (string, error) {
	if err := ValidateLiveRunIDV1(liveRunID); err != nil {
		return "", err
	}
	if !filepath.IsAbs(channelDirectory) || filepath.Clean(channelDirectory) != channelDirectory ||
		filepath.Base(channelDirectory) != liveRunID || filepath.Base(filepath.Dir(channelDirectory)) != "sessions" ||
		filepath.Base(filepath.Dir(filepath.Dir(channelDirectory))) != ".reploy" {
		return "", fmt.Errorf("controlled-session channel directory must identify the live-run private session directory")
	}
	stateDirectory := filepath.Dir(filepath.Dir(channelDirectory))
	return filepath.Join(stateDirectory, "incidents", liveRunID+".json"), nil
}

// PrepareControlledSessionIncidentReceiptV1 pre-creates the exact private
// watchdog write target while the deployment operation lock is held. The
// inherited descriptor keeps an advisory lock on that exact target until both
// the parent and watchdog close it, so recovery cannot unlink the file while a
// surviving watchdog can still write. Existing completed receipts are never
// overwritten or silently evicted.
func (lock *OperationLock) PrepareControlledSessionIncidentReceiptV1(channelDirectory string, liveRunID string) (*ControlledSessionIncidentReceiptTargetV1, error) {
	if lock == nil {
		return nil, fmt.Errorf("prepare controlled-session incident receipt requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if lock.released || lock.file == nil {
		return nil, fmt.Errorf("operation lock is not held")
	}
	path, err := ControlledSessionIncidentReceiptPathV1(channelDirectory, liveRunID)
	if err != nil {
		return nil, err
	}
	if filepath.Dir(filepath.Dir(path)) != filepath.Dir(lock.path) {
		return nil, fmt.Errorf("controlled-session incident receipt does not belong to the locked deployment")
	}
	directory := filepath.Dir(path)
	if err := prepareControlledSessionIncidentDirectoryV1(directory); err != nil {
		return nil, err
	}
	entries, err := cleanControlledSessionIncidentTargetsLockedV1(filepath.Dir(lock.path), directory)
	if err != nil {
		return nil, err
	}
	if entries >= controlledSessionIncidentRetentionLimitV1 {
		return nil, fmt.Errorf("controlled-session incident receipt retention limit of %d reached; retrieve and acknowledge existing receipts", controlledSessionIncidentRetentionLimitV1)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create controlled-session incident receipt target: %w", err)
	}
	if err := lockControlledSessionIncidentTargetV1(file); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("lock controlled-session incident receipt target: %w", err)
	}
	if err := syncAtomicStateFileDirectory(directory); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sync controlled-session incident receipt target: %w", err)
	}
	return &ControlledSessionIncidentReceiptTargetV1{path: path, file: file}, nil
}

func prepareControlledSessionIncidentDirectoryV1(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := createControlledSessionIncidentDirectoryV1(path); err != nil {
			return fmt.Errorf("create controlled-session incident receipt directory: %w", err)
		}
		if err := syncAtomicStateFileDirectory(filepath.Dir(path)); err != nil {
			return fmt.Errorf("sync controlled-session incident receipt directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect controlled-session incident receipt directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("controlled-session incident receipt path must be a real directory: %s", path)
	}
	if err := validateControlledSessionIncidentDirectorySecurityV1(path, info); err != nil {
		return err
	}
	return nil
}

func cleanControlledSessionIncidentTargetsLockedV1(stateDirectory string, directory string) (int, error) {
	queue, _, err := readLiveRunQueuePathV1(filepath.Join(stateDirectory, liveRunQueueFilenameV1))
	if err != nil {
		return 0, err
	}
	owned := make(map[string]bool, len(queue.ControlledSessions))
	for _, ownership := range queue.ControlledSessions {
		owned[ownership.LiveRunID] = true
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, fmt.Errorf("read controlled-session incident receipts: %w", err)
	}
	retained := 0
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		runID := strings.TrimSuffix(name, ".json")
		if !strings.HasSuffix(name, ".json") || ValidateLiveRunIDV1(runID) != nil || entry.Type()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("controlled-session incident receipt directory contains unexpected entry %q", name)
		}
		info, err := entry.Info()
		if err != nil {
			return 0, fmt.Errorf("inspect controlled-session incident receipt %q: %w", name, err)
		}
		path := filepath.Join(directory, name)
		if !info.Mode().IsRegular() || info.Size() > ControlledSessionIncidentReceiptLimitV1 {
			return 0, fmt.Errorf("controlled-session incident receipt %q is not a bounded regular file", name)
		}
		if info.Size() == 0 && !owned[runID] {
			inUse, err := controlledSessionIncidentTargetInUseV1(path)
			if err != nil {
				return 0, fmt.Errorf("inspect empty controlled-session incident target %q liveness: %w", name, err)
			}
			if !inUse {
				if err := os.Remove(path); err != nil {
					return 0, fmt.Errorf("remove abandoned empty controlled-session incident target %q: %w", name, err)
				}
				removed = true
				continue
			}
		}
		if info.Size() != 0 {
			content, err := os.ReadFile(path)
			if err != nil {
				return 0, fmt.Errorf("read controlled-session incident receipt %q: %w", name, err)
			}
			receipt, err := DecodeControlledSessionIncidentReceiptV1(content)
			if err != nil {
				return 0, fmt.Errorf("validate controlled-session incident receipt %q: %w", name, err)
			}
			if receipt.LiveRunID != runID {
				return 0, fmt.Errorf("controlled-session incident receipt %q names a different live run", name)
			}
		}
		retained++
	}
	if removed {
		if err := syncAtomicStateFileDirectory(directory); err != nil {
			return 0, fmt.Errorf("sync abandoned controlled-session incident target cleanup: %w", err)
		}
	}
	return retained, nil
}

func EncodeControlledSessionIncidentReceiptV1(receipt ControlledSessionIncidentReceiptV1) ([]byte, error) {
	if err := ValidateControlledSessionIncidentReceiptV1(receipt); err != nil {
		return nil, err
	}
	content, err := canonical.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encode controlled-session incident receipt: %w", err)
	}
	if len(content) > ControlledSessionIncidentReceiptLimitV1 {
		return nil, fmt.Errorf("controlled-session incident receipt exceeds %d bytes", ControlledSessionIncidentReceiptLimitV1)
	}
	return content, nil
}

func DecodeControlledSessionIncidentReceiptV1(content []byte) (ControlledSessionIncidentReceiptV1, error) {
	if len(content) == 0 {
		return ControlledSessionIncidentReceiptV1{}, io.EOF
	}
	if len(content) > ControlledSessionIncidentReceiptLimitV1 {
		return ControlledSessionIncidentReceiptV1{}, fmt.Errorf("controlled-session incident receipt exceeds %d bytes", ControlledSessionIncidentReceiptLimitV1)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var receipt ControlledSessionIncidentReceiptV1
	if err := decoder.Decode(&receipt); err != nil {
		return ControlledSessionIncidentReceiptV1{}, fmt.Errorf("decode controlled-session incident receipt: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ControlledSessionIncidentReceiptV1{}, fmt.Errorf("controlled-session incident receipt contains trailing JSON")
		}
		return ControlledSessionIncidentReceiptV1{}, fmt.Errorf("decode controlled-session incident receipt trailer: %w", err)
	}
	if err := ValidateControlledSessionIncidentReceiptV1(receipt); err != nil {
		return ControlledSessionIncidentReceiptV1{}, err
	}
	canonicalContent, err := canonical.Marshal(receipt)
	if err != nil {
		return ControlledSessionIncidentReceiptV1{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return ControlledSessionIncidentReceiptV1{}, fmt.Errorf("controlled-session incident receipt is not canonical JSON")
	}
	return receipt, nil
}

func ValidateControlledSessionIncidentReceiptV1(receipt ControlledSessionIncidentReceiptV1) error {
	if receipt.Schema != ControlledSessionIncidentReceiptSchemaV1 {
		return fmt.Errorf("controlled-session incident receipt schema must be %q", ControlledSessionIncidentReceiptSchemaV1)
	}
	if err := ValidateLiveRunIDV1(receipt.LiveRunID); err != nil {
		return fmt.Errorf("controlled-session incident receipt live run ID: %w", err)
	}
	if err := validateBootSessionIDV1(receipt.BootSession); err != nil {
		return fmt.Errorf("controlled-session incident receipt: %w", err)
	}
	recordedAt, err := time.Parse(time.RFC3339Nano, receipt.RecordedAt)
	if err != nil || recordedAt.Location() != time.UTC || recordedAt.Format(time.RFC3339Nano) != receipt.RecordedAt {
		return fmt.Errorf("controlled-session incident receipt recorded time must be canonical UTC RFC3339")
	}
	if receipt.Trigger != ControlledSessionIncidentParentLostV1 {
		return fmt.Errorf("controlled-session incident receipt trigger must be %q", ControlledSessionIncidentParentLostV1)
	}
	if err := validateControlledSessionIncidentContainerV1(receipt.Controller, "controller"); err != nil {
		return fmt.Errorf("controlled-session incident receipt controller: %w", err)
	}
	if err := validateControlledSessionIncidentContainerV1(receipt.Workload, "workload"); err != nil {
		return fmt.Errorf("controlled-session incident receipt workload: %w", err)
	}
	if receipt.Controller.ID == receipt.Workload.ID {
		return fmt.Errorf("controlled-session incident receipt containers must be different")
	}
	if err := validateControlledSessionIncidentResourceStatusV1(receipt.ChannelCleanupStatus); err != nil {
		return fmt.Errorf("controlled-session incident receipt channel: %w", err)
	}
	allSucceeded := receipt.Controller.CleanupStatus == ControlledSessionIncidentResourceVerifiedAbsentV1 &&
		receipt.Workload.CleanupStatus == ControlledSessionIncidentResourceVerifiedAbsentV1 &&
		receipt.ChannelCleanupStatus == ControlledSessionIncidentResourceVerifiedAbsentV1
	switch receipt.CleanupStatus {
	case ControlledSessionIncidentCleanupSucceededV1:
		if !allSucceeded || receipt.RecoveryAction != ControlledSessionIncidentRecoveryNoneV1 {
			return fmt.Errorf("successful controlled-session incident cleanup requires every resource verified absent and no recovery action")
		}
	case ControlledSessionIncidentCleanupFailedV1:
		if allSucceeded || receipt.RecoveryAction != ControlledSessionIncidentRecoveryNextOperationV1 {
			return fmt.Errorf("failed controlled-session incident cleanup requires a failed resource and next-operation recovery")
		}
	default:
		return fmt.Errorf("controlled-session incident receipt cleanup status is invalid")
	}
	return nil
}

func validateControlledSessionIncidentContainerV1(container ControlledSessionIncidentContainerV1, role string) error {
	ownership := ControlledSessionContainerOwnershipV1{
		Role: container.Role, ID: container.ID, Name: "receipt-owned-container",
		DeploymentID: container.DeploymentID, GenerationReference: container.GenerationReference,
		BuildIdentity: container.BuildIdentity,
	}
	if err := validateControlledSessionContainerOwnershipV1(ownership, role); err != nil {
		return err
	}
	return validateControlledSessionIncidentResourceStatusV1(container.CleanupStatus)
}

func validateControlledSessionIncidentResourceStatusV1(status ControlledSessionIncidentResourceStatusV1) error {
	switch status {
	case ControlledSessionIncidentResourceVerifiedAbsentV1, ControlledSessionIncidentResourceCleanupFailedV1:
		return nil
	default:
		return fmt.Errorf("cleanup status is invalid")
	}
}

func WriteControlledSessionIncidentReceiptV1(file *os.File, receipt ControlledSessionIncidentReceiptV1) error {
	if file == nil {
		return fmt.Errorf("write controlled-session incident receipt requires its pre-created target")
	}
	return writeControlledSessionIncidentReceiptV1(file, receipt)
}

type controlledSessionIncidentReceiptFileV1 interface {
	Truncate(size int64) error
	Seek(offset int64, whence int) (int64, error)
	Write(content []byte) (int, error)
	Sync() error
}

func writeControlledSessionIncidentReceiptV1(file controlledSessionIncidentReceiptFileV1, receipt ControlledSessionIncidentReceiptV1) error {
	content, err := EncodeControlledSessionIncidentReceiptV1(receipt)
	if err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate controlled-session incident receipt: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind controlled-session incident receipt: %w", err)
	}
	if count, err := file.Write(content); err != nil || count != len(content) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return errors.Join(
			fmt.Errorf("write controlled-session incident receipt: %w", err),
			clearIncompleteControlledSessionIncidentReceiptV1(file),
		)
	}
	if err := file.Sync(); err != nil {
		return errors.Join(
			fmt.Errorf("sync controlled-session incident receipt: %w", err),
			clearIncompleteControlledSessionIncidentReceiptV1(file),
		)
	}
	return nil
}

func clearIncompleteControlledSessionIncidentReceiptV1(file controlledSessionIncidentReceiptFileV1) error {
	var truncateErr error
	if err := file.Truncate(0); err != nil {
		truncateErr = fmt.Errorf("clear incomplete controlled-session incident receipt: %w", err)
	}
	var seekErr error
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		seekErr = fmt.Errorf("rewind cleared controlled-session incident receipt: %w", err)
	}
	var syncErr error
	if err := file.Sync(); err != nil {
		syncErr = fmt.Errorf("sync cleared controlled-session incident receipt: %w", err)
	}
	return errors.Join(truncateErr, seekErr, syncErr)
}

func (lock *OperationLock) ReadControlledSessionIncidentReceiptsV1() ([]ControlledSessionIncidentReceiptV1, error) {
	if lock == nil {
		return nil, fmt.Errorf("read controlled-session incident receipts requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if lock.released || lock.file == nil {
		return nil, fmt.Errorf("operation lock is not held")
	}
	directory := filepath.Join(filepath.Dir(lock.path), "incidents")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []ControlledSessionIncidentReceiptV1{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read controlled-session incident receipt directory: %w", err)
	}
	if len(entries) > controlledSessionIncidentRetentionLimitV1 {
		return nil, fmt.Errorf("controlled-session incident receipt directory exceeds retention limit")
	}
	receipts := make([]ControlledSessionIncidentReceiptV1, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || ValidateLiveRunIDV1(strings.TrimSuffix(name, ".json")) != nil || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("controlled-session incident receipt directory contains unexpected entry %q", name)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect controlled-session incident receipt %q: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Size() > ControlledSessionIncidentReceiptLimitV1 {
			return nil, fmt.Errorf("controlled-session incident receipt %q is not a bounded regular file", name)
		}
		if info.Size() == 0 {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return nil, fmt.Errorf("read controlled-session incident receipt %q: %w", name, err)
		}
		receipt, err := DecodeControlledSessionIncidentReceiptV1(content)
		if err != nil {
			return nil, fmt.Errorf("read controlled-session incident receipt %q: %w", name, err)
		}
		if receipt.LiveRunID+".json" != name {
			return nil, fmt.Errorf("controlled-session incident receipt %q names a different live run", name)
		}
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(left, right int) bool {
		if receipts[left].RecordedAt != receipts[right].RecordedAt {
			return receipts[left].RecordedAt < receipts[right].RecordedAt
		}
		return receipts[left].LiveRunID < receipts[right].LiveRunID
	})
	return receipts, nil
}

func (lock *OperationLock) AcknowledgeControlledSessionIncidentReceiptV1(liveRunID string) (bool, error) {
	if lock == nil {
		return false, fmt.Errorf("acknowledge controlled-session incident receipt requires an operation lock")
	}
	if err := ValidateLiveRunIDV1(liveRunID); err != nil {
		return false, err
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if lock.released || lock.file == nil {
		return false, fmt.Errorf("operation lock is not held")
	}
	path := filepath.Join(filepath.Dir(lock.path), "incidents", liveRunID+".json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect controlled-session incident receipt: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 || info.Size() > ControlledSessionIncidentReceiptLimitV1 {
		return false, fmt.Errorf("controlled-session incident receipt is not a completed bounded regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read controlled-session incident receipt before acknowledgement: %w", err)
	}
	receipt, err := DecodeControlledSessionIncidentReceiptV1(content)
	if err != nil {
		return false, fmt.Errorf("validate controlled-session incident receipt before acknowledgement: %w", err)
	}
	if receipt.LiveRunID != liveRunID {
		return false, fmt.Errorf("controlled-session incident receipt names a different live run")
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("acknowledge controlled-session incident receipt: %w", err)
	}
	if err := syncAtomicStateFileDirectory(filepath.Dir(path)); err != nil {
		return false, fmt.Errorf("sync controlled-session incident receipt acknowledgement: %w", err)
	}
	return true, nil
}
