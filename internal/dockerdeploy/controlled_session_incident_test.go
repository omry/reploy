package dockerdeploy

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

func TestControlledSessionIncidentRetrievalSurfaceIsReadOnlyAndAcknowledgedExplicitly(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	const runID = "run-0000000000000001"
	target, err := operation.PrepareControlledSessionIncidentReceiptV1(filepath.Join(dir, ".reploy", "sessions", runID), runID)
	if err != nil {
		t.Fatal(err)
	}
	receipt := controlledSessionIncidentRetrievalFixtureV1(runID)
	if err := deploy.WriteControlledSessionIncidentReceiptV1(target.File(), receipt); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}

	receipts, err := ListControlledSessionIncidentReceiptsV1(t.Context(), dir)
	if err != nil || len(receipts) != 1 || !reflect.DeepEqual(receipts[0], receipt) {
		t.Fatalf("retrieved receipts = %#v, error=%v", receipts, err)
	}
	removed, err := AcknowledgeControlledSessionIncidentReceiptV1(t.Context(), dir, runID)
	if err != nil || !removed {
		t.Fatalf("acknowledge receipt = %t, %v", removed, err)
	}
	receipts, err = ListControlledSessionIncidentReceiptsV1(t.Context(), dir)
	if err != nil || len(receipts) != 0 {
		t.Fatalf("receipts after acknowledgement = %#v, error=%v", receipts, err)
	}
}

func controlledSessionIncidentRetrievalFixtureV1(runID string) deploy.ControlledSessionIncidentReceiptV1 {
	container := func(role string, digit string) deploy.ControlledSessionIncidentContainerV1 {
		return deploy.ControlledSessionIncidentContainerV1{
			Role: role, ID: strings.Repeat(digit, 64), DeploymentID: "reploy/env/" + role,
			GenerationReference: "reploy/env/" + role + ":g-current",
			BuildIdentity:       "sha256:" + strings.Repeat(digit, 64),
			CleanupStatus:       deploy.ControlledSessionIncidentResourceVerifiedAbsentV1,
		}
	}
	return deploy.ControlledSessionIncidentReceiptV1{
		Schema: deploy.ControlledSessionIncidentReceiptSchemaV1, LiveRunID: runID, BootSession: "boot-session",
		RecordedAt: time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		Trigger:    deploy.ControlledSessionIncidentParentLostV1,
		Controller: container("controller", "c"), Workload: container("workload", "d"),
		ChannelCleanupStatus: deploy.ControlledSessionIncidentResourceVerifiedAbsentV1,
		CleanupStatus:        deploy.ControlledSessionIncidentCleanupSucceededV1,
		RecoveryAction:       deploy.ControlledSessionIncidentRecoveryNoneV1,
	}
}
