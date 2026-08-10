package deploy

import (
	"bytes"
	"fmt"
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
