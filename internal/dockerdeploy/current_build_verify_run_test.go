package dockerdeploy

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestVerifyCurrentBuildV1HoldsDeploymentLockThroughAudit(t *testing.T) {
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	document, err := blueprint.DecodeResolvedDocumentV1(current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	planned := CurrentRuntimePlanV1{Document: document, Docker: DockerExecutionPlan{}}
	details := CurrentBuildVerificationResultV1{
		StoreObjects: 7, Images: 3, Commands: 2,
	}
	var operation *deploy.OperationLock
	order := []string{}
	result, err := verifyCurrentBuildV1(
		t.Context(),
		VerifyCurrentBuildInputV1{
			DeploymentDir: dir,
			Runtime:       StagedProviderBuildRuntimeV1{UID: 501, GID: 20},
		},
		verifyCurrentBuildBackendV1{
			acquire: func(ctx context.Context, got string) (*deploy.OperationLock, error) {
				order = append(order, "acquire")
				if got != filepath.Clean(dir) {
					t.Fatalf("deployment directory = %q", got)
				}
				var acquireErr error
				operation, acquireErr = deploy.AcquireOperationLock(ctx, got)
				return operation, acquireErr
			},
			newStore: func(got string) (providerstore.Store, error) {
				order = append(order, "store")
				return providerstore.NewStore(got)
			},
			readState: func(held *deploy.OperationLock) (deploy.StateV1, bool, error) {
				order = append(order, "state")
				if held != operation || held.RequireHeld() != nil {
					t.Fatal("state read did not hold the deployment lock")
				}
				return current.State, true, nil
			},
			loadCurrent: func(
				_ context.Context,
				held *deploy.OperationLock,
				_ providerstore.Store,
				environment string,
				gotDir string,
			) (CurrentBuild, bool, error) {
				order = append(order, "current")
				if held != operation ||
					held.RequireHeld() != nil ||
					environment != document.Environment.ID ||
					gotDir != dir {
					t.Fatal("current-build load arguments changed")
				}
				return current, true, nil
			},
			plan: func(input CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) {
				order = append(order, "plan")
				if input.DeploymentDir != dir ||
					!reflect.DeepEqual(input.Current, current) ||
					input.Runtime.UID != 501 {
					t.Fatalf("runtime plan input = %#v", input)
				}
				return planned, nil
			},
			verify: func(
				_ context.Context,
				input CurrentBuildVerificationInputV1,
			) (CurrentBuildVerificationResultV1, error) {
				order = append(order, "verify")
				if operation.RequireHeld() != nil ||
					!reflect.DeepEqual(input.Current, current) ||
					!reflect.DeepEqual(input.Runtime, planned) {
					t.Fatal("verification did not retain the current build and lock")
				}
				return details, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"acquire", "store", "state", "current", "plan", "verify"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order = %v, want %v", order, wantOrder)
	}
	if result.Environment != document.Environment.ID ||
		result.Reference != current.Generation.Reference ||
		result.Details != details {
		t.Fatalf("result = %#v", result)
	}
	if operation.RequireHeld() == nil {
		t.Fatal("verification operation lock remains held")
	}
}
