package dockerdeploy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestVerifyRejectsAccountMismatchBeforeProviderOrImageChecks(t *testing.T) {
	f := baseOnlyCurrentBuildVerificationFixtureV1(t)
	f.runtime.Docker.Sandbox = testApplicationSandboxPlanV1(2000, 2001)
	_, err := verifyLoadedCurrentBuildV1(t.Context(), CurrentBuildVerificationInputV1{
		Current: f.current, Runtime: f.runtime, Store: f.store,
		RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			t.Fatal("account mismatch reached Docker validation")
			return nil, nil, nil
		},
	}, currentBuildVerificationBackendV1{
		verifyClosure: func(deploy.BuildLockV1, providerstore.Store, providers.RequirementProfileOwnerValidator, providers.ResolvedBundleOwnerValidator) ([]providerstore.StoreObjectRef, error) {
			t.Fatal("account mismatch reached provider work")
			return nil, nil
		},
		inspectImage: func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
			t.Fatal("account mismatch reached image inspection")
			return InspectedImageCandidate{}, nil
		},
	})
	assertAccountRecoveryV1(t, err, "recorded UID/GID 1000/1000", "expected 2000/2001", "reploy build")
}

func TestPortableVerifyRejectsAccountMismatchBeforeImageChecks(t *testing.T) {
	for _, environmentOnly := range []bool{false, true} {
		name := "payload"
		if environmentOnly {
			name = "environment"
		}
		t.Run(name, func(t *testing.T) {
			f := newPortableVerificationFixtureV1(t, environmentOnly)
			f.runtime.Docker.Sandbox = testApplicationSandboxPlanV1(2000, 2001)
			matches, err := CurrentBuildMatchesRuntimeV1(f.current, f.runtime.Docker)
			if err != nil || matches {
				t.Fatalf("mismatched portable runtime matches=%v error=%v", matches, err)
			}
			_, err = f.verify(t, f.current)
			assertAccountRecoveryV1(t, err, "recorded UID/GID 1000/1000", "expected 2000/2001", "reploy build")
			if len(f.inspected) != 0 {
				t.Fatalf("account mismatch reached image inspection: %v", f.inspected)
			}
		})
	}
}

func TestStatusReportsAccountMismatchBeforeDocker(t *testing.T) {
	f := baseOnlyCurrentBuildVerificationFixtureV1(t)
	f.runtime.Docker.Sandbox = testApplicationSandboxPlanV1(2000, 2001)
	var order []string
	dir := t.TempDir()
	backend := currentRuntimeObservationTestBackend(t, dir, f.current, f.runtime, &order)
	backend.matches = func(current CurrentBuild, plan DockerExecutionPlan) (bool, error) {
		return CurrentBuildMatchesRuntimeV1(current, plan)
	}
	backend.status = func(context.Context, DockerExecutionPlan, time.Duration) (RuntimeStatusV1, error) {
		t.Fatal("account mismatch reached Docker status")
		return RuntimeStatusV1{}, nil
	}
	err := runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{
		DeploymentDir: dir, Action: "status",
	}, backend)
	assertAccountRecoveryV1(t, err, "recorded UID/GID 1000/1000", "expected 2000/2001", "reploy build")
}

func TestAccountRecoveryForPreviousPrivilegedBuildAndInstalledPlan(t *testing.T) {
	f := baseOnlyCurrentBuildVerificationFixtureV1(t)
	f.current.Lock.RuntimeLayer.Account.UID, f.current.Lock.RuntimeLayer.Account.GID = "0", "0"
	err := verifyLockedRuntimeV1(f.current.Lock, f.runtime)
	assertAccountRecoveryV1(t, err, "recorded UID/GID 0/0", "expected 1000/1000", "reploy build")
	f.runtime.Docker.Phase = blueprint.PhaseInstalled
	err = verifyLockedRuntimeV1(f.current.Lock, f.runtime)
	assertAccountRecoveryV1(t, err, "recorded UID/GID 0/0", "expected 1000/1000", "reploy install")
}

func assertAccountRecoveryV1(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("account mismatch was accepted")
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q missing %q", err, fragment)
		}
	}
}
