package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestControlledSessionIncidentReceiptRoundTripHasOnlyAllowlistedFacts(t *testing.T) {
	receipt := controlledSessionIncidentReceiptFixtureV1("run-0000000000000001")
	content, err := EncodeControlledSessionIncidentReceiptV1(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"pty", "environment_value", "secret", "container_log", "docker_output", "message"} {
		if bytes.Contains(content, []byte(forbidden)) {
			t.Fatalf("incident receipt contains forbidden field %q: %s", forbidden, content)
		}
	}
	decoded, err := DecodeControlledSessionIncidentReceiptV1(content)
	if err != nil || !reflect.DeepEqual(decoded, receipt) {
		t.Fatalf("decoded receipt = %#v, error=%v", decoded, err)
	}
	unknown := append(append([]byte{}, content[:len(content)-1]...), []byte(`,"message":"raw failure"}`)...)
	if _, err := DecodeControlledSessionIncidentReceiptV1(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := DecodeControlledSessionIncidentReceiptV1(append([]byte("\n"), content...)); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical receipt error = %v", err)
	}
}

func TestOperationLockPreparesRetrievesAndAcknowledgesExactIncidentReceipt(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	const runID = "run-0000000000000001"
	channel := filepath.Join(dir, ".reploy", "sessions", runID)
	target, err := lock.PrepareControlledSessionIncidentReceiptV1(channel, runID)
	if err != nil {
		t.Fatal(err)
	}
	if target.Path() != filepath.Join(dir, ".reploy", "incidents", runID+".json") {
		t.Fatalf("incident target = %q", target.Path())
	}
	info, err := os.Lstat(target.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o600 {
		t.Fatalf("incident target mode = %v", info.Mode())
	}
	if err := WriteControlledSessionIncidentReceiptV1(target.File(), controlledSessionIncidentReceiptFixtureV1(runID)); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	receipts, err := lock.ReadControlledSessionIncidentReceiptsV1()
	if err != nil || len(receipts) != 1 || receipts[0].LiveRunID != runID {
		t.Fatalf("incident receipts = %#v, error=%v", receipts, err)
	}
	removed, err := lock.AcknowledgeControlledSessionIncidentReceiptV1(runID)
	if err != nil || !removed {
		t.Fatalf("acknowledge = %t, %v", removed, err)
	}
	receipts, err = lock.ReadControlledSessionIncidentReceiptsV1()
	if err != nil || len(receipts) != 0 {
		t.Fatalf("acknowledged receipts = %#v, error=%v", receipts, err)
	}
	if removed, err := lock.AcknowledgeControlledSessionIncidentReceiptV1(runID); err != nil || removed {
		t.Fatalf("repeated acknowledgement = %t, %v", removed, err)
	}
}

func TestWriteControlledSessionIncidentReceiptClearsIncompleteTarget(t *testing.T) {
	for _, test := range []struct {
		name       string
		writeError error
		syncErrors []error
	}{
		{name: "short write"},
		{name: "partial write error", writeError: errors.New("disk full")},
		{name: "sync error", syncErrors: []error{errors.New("sync failed"), nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &controlledSessionIncidentReceiptTestFileV1{
				writeLimit: 7,
				writeError: test.writeError,
				syncErrors: append([]error(nil), test.syncErrors...),
			}
			if test.name == "sync error" {
				target.writeLimit = -1
			}

			err := writeControlledSessionIncidentReceiptV1(target, controlledSessionIncidentReceiptFixtureV1("run-0000000000000001"))
			if err == nil {
				t.Fatal("incomplete incident receipt write succeeded")
			}
			if len(target.content) != 0 {
				t.Fatalf("incomplete incident receipt target contains %d bytes", len(target.content))
			}
			if target.truncates != 2 || target.seeks != 2 {
				t.Fatalf("target resets = truncate %d, seek %d; want 2 each", target.truncates, target.seeks)
			}
		})
	}
}

func TestOperationLockIncidentReceiptRetentionIsBoundedWithoutEviction(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	for index := 0; index < controlledSessionIncidentRetentionLimitV1; index++ {
		runID := fmt.Sprintf("run-%016x", index)
		target, err := lock.PrepareControlledSessionIncidentReceiptV1(filepath.Join(dir, ".reploy", "sessions", runID), runID)
		if err != nil {
			t.Fatalf("prepare receipt %d: %v", index, err)
		}
		if err := WriteControlledSessionIncidentReceiptV1(target.File(), controlledSessionIncidentReceiptFixtureV1(runID)); err != nil {
			t.Fatal(err)
		}
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
	}
	runID := fmt.Sprintf("run-%016x", controlledSessionIncidentRetentionLimitV1)
	if _, err := lock.PrepareControlledSessionIncidentReceiptV1(filepath.Join(dir, ".reploy", "sessions", runID), runID); err == nil || !strings.Contains(err.Error(), "retention limit") {
		t.Fatalf("retention error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".reploy", "incidents"))
	if err != nil || len(entries) != controlledSessionIncidentRetentionLimitV1 {
		t.Fatalf("retained entries = %d, error=%v", len(entries), err)
	}
}

func TestOperationLockRemovesOnlyAbandonedEmptyIncidentTargets(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	const abandoned = "run-0000000000000001"
	target, err := lock.PrepareControlledSessionIncidentReceiptV1(filepath.Join(dir, ".reploy", "sessions", abandoned), abandoned)
	if err != nil {
		t.Fatal(err)
	}
	path := target.Path()
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	const next = "run-0000000000000002"
	nextTarget, err := lock.PrepareControlledSessionIncidentReceiptV1(filepath.Join(dir, ".reploy", "sessions", next), next)
	if err != nil {
		t.Fatal(err)
	}
	defer nextTarget.Remove()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("abandoned empty incident target remains: %v", err)
	}
}

func TestOperationLockRetainsEmptyIncidentTargetWhileWatchdogDescriptorIsLive(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	const live = "run-0000000000000001"
	target, err := lock.PrepareControlledSessionIncidentReceiptV1(filepath.Join(dir, ".reploy", "sessions", live), live)
	if err != nil {
		t.Fatal(err)
	}
	path := target.Path()
	const next = "run-0000000000000002"
	nextTarget, err := lock.PrepareControlledSessionIncidentReceiptV1(filepath.Join(dir, ".reploy", "sessions", next), next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("live empty incident target was removed: %v", err)
	}
	if err := nextTarget.Remove(); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	const later = "run-0000000000000003"
	laterTarget, err := lock.PrepareControlledSessionIncidentReceiptV1(filepath.Join(dir, ".reploy", "sessions", later), later)
	if err != nil {
		t.Fatal(err)
	}
	defer laterTarget.Remove()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("released empty incident target remains: %v", err)
	}
}

func controlledSessionIncidentReceiptFixtureV1(runID string) ControlledSessionIncidentReceiptV1 {
	container := func(role string, id string) ControlledSessionIncidentContainerV1 {
		return ControlledSessionIncidentContainerV1{
			Role: role, ID: strings.Repeat(id, 64), DeploymentID: "reploy/env/" + role,
			GenerationReference: "reploy/env/" + role + ":g-current",
			BuildIdentity:       "sha256:" + strings.Repeat(id, 64),
			CleanupStatus:       ControlledSessionIncidentResourceVerifiedAbsentV1,
		}
	}
	return ControlledSessionIncidentReceiptV1{
		Schema: ControlledSessionIncidentReceiptSchemaV1, LiveRunID: runID, BootSession: "boot-session",
		RecordedAt: time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		Trigger:    ControlledSessionIncidentParentLostV1,
		Controller: container("controller", "c"), Workload: container("workload", "d"),
		ChannelCleanupStatus: ControlledSessionIncidentResourceVerifiedAbsentV1,
		CleanupStatus:        ControlledSessionIncidentCleanupSucceededV1,
		RecoveryAction:       ControlledSessionIncidentRecoveryNoneV1,
	}
}

type controlledSessionIncidentReceiptTestFileV1 struct {
	content    []byte
	writeLimit int
	writeError error
	syncErrors []error
	truncates  int
	seeks      int
}

func (file *controlledSessionIncidentReceiptTestFileV1) Truncate(size int64) error {
	file.truncates++
	if size != 0 {
		return fmt.Errorf("unexpected truncate size %d", size)
	}
	file.content = nil
	return nil
}

func (file *controlledSessionIncidentReceiptTestFileV1) Seek(offset int64, whence int) (int64, error) {
	file.seeks++
	if offset != 0 || whence != io.SeekStart {
		return 0, fmt.Errorf("unexpected seek %d/%d", offset, whence)
	}
	return 0, nil
}

func (file *controlledSessionIncidentReceiptTestFileV1) Write(content []byte) (int, error) {
	count := len(content)
	if file.writeLimit >= 0 && file.writeLimit < count {
		count = file.writeLimit
	}
	file.content = append(file.content, content[:count]...)
	return count, file.writeError
}

func (file *controlledSessionIncidentReceiptTestFileV1) Sync() error {
	if len(file.syncErrors) == 0 {
		return nil
	}
	err := file.syncErrors[0]
	file.syncErrors = file.syncErrors[1:]
	return err
}
