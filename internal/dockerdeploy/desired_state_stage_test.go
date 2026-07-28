package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

func TestStageDesiredStateV1WritesUnbuiltStateWithoutNativeProbeForSinglePlatform(t *testing.T) {
	dir := t.TempDir()
	document := targetPlatformDocument(t, "linux/amd64")

	result, err := StageDesiredStateV1(t.Context(), DesiredStateStageInputV1{DeploymentDir: dir, Document: document, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.State.Current != nil || result.State.Platform.Canonical != "linux/amd64" {
		t.Fatalf("result = %#v", result)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(state, result.State) {
		t.Fatalf("stored state = %#v, found = %v", state, found)
	}
}

func TestStageDesiredStateV1ProbesNativeOnlyForAmbiguousDefault(t *testing.T) {
	document := targetPlatformDocument(t, "linux/amd64", "linux/arm64")
	probeCalls := 0
	var selected blueprint.Platform
	result, err := stageDesiredStateV1(t.Context(), DesiredStateStageInputV1{
		DeploymentDir: t.TempDir(), Document: document,
	}, desiredStateStageBackendV1{
		probeNative: func(context.Context) (blueprint.Platform, error) {
			probeCalls++
			return targetPlatform(t, "linux/arm64"), nil
		},
		setState: func(_ context.Context, _ string, _ blueprint.Document, platform blueprint.Platform, _ string, create bool, _ *deploy.PackageOverridesV1) (deploy.DesiredStateUpdateResult, error) {
			if create {
				t.Fatal("update unexpectedly requested create-only publication")
			}
			selected = platform
			return deploy.DesiredStateUpdateResult{Changed: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || probeCalls != 1 || selected.Canonical != "linux/arm64" {
		t.Fatalf("result/probe/selected = %#v/%d/%#v", result, probeCalls, selected)
	}
}

func TestStageDesiredStateV1ExplicitPlatformSkipsNativeProbe(t *testing.T) {
	document := targetPlatformDocument(t, "linux/amd64", "linux/arm64")
	var selected blueprint.Platform
	_, err := stageDesiredStateV1(t.Context(), DesiredStateStageInputV1{
		DeploymentDir: t.TempDir(), Document: document, ExplicitPlatform: "linux/arm64",
	}, desiredStateStageBackendV1{
		setState: func(_ context.Context, _ string, _ blueprint.Document, platform blueprint.Platform, _ string, create bool, _ *deploy.PackageOverridesV1) (deploy.DesiredStateUpdateResult, error) {
			if create {
				t.Fatal("update unexpectedly requested create-only publication")
			}
			selected = platform
			return deploy.DesiredStateUpdateResult{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Canonical != "linux/arm64" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestStageDesiredStateV1PreservesNoopState(t *testing.T) {
	dir := t.TempDir()
	document := targetPlatformDocument(t, "linux/amd64")
	if _, err := deploy.SetDesiredStateV1(t.Context(), dir, document, targetPlatform(t, "linux/amd64"), registry.ValidatePackageRequest); err != nil {
		t.Fatal(err)
	}

	result, err := StageDesiredStateV1(t.Context(), DesiredStateStageInputV1{DeploymentDir: dir, Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("unchanged desired state was rewritten")
	}
}

func TestStageDesiredStateV1CreateRefusesExistingState(t *testing.T) {
	dir := t.TempDir()
	document := targetPlatformDocument(t, "linux/amd64")
	input := DesiredStateStageInputV1{DeploymentDir: dir, Document: document, Create: true}
	if _, err := StageDesiredStateV1(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	document.Blueprint.Version = "replacement"
	input.Document = document
	if _, err := StageDesiredStateV1(t.Context(), input); !errors.Is(err, deploy.ErrDesiredStateAlreadyExists) {
		t.Fatalf("second create error = %v", err)
	}
}

func TestRestageCurrentDesiredPlatformV1UsesNativeMatchForStoredBlueprint(t *testing.T) {
	dir := t.TempDir()
	document := targetPlatformDocument(t, "linux/amd64", "linux/arm64")
	if _, err := deploy.SetDesiredStateV1(t.Context(), dir, document, targetPlatform(t, "linux/amd64"), registry.ValidatePackageRequest); err != nil {
		t.Fatal(err)
	}
	probeCalls := 0
	result, err := restageCurrentDesiredPlatformV1(t.Context(), dir, "", func(context.Context) (blueprint.Platform, error) {
		probeCalls++
		return targetPlatform(t, "linux/arm64"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if probeCalls != 1 || !result.Changed || result.State.Platform.Canonical != "linux/arm64" {
		t.Fatalf("probe/result = %d/%#v", probeCalls, result)
	}
}

func TestRestageCurrentDesiredPlatformV1SkipsProbeForExplicitOrSingleTarget(t *testing.T) {
	tests := []struct {
		name     string
		document blueprint.Document
		explicit string
	}{
		{name: "explicit", document: targetPlatformDocument(t, "linux/amd64", "linux/arm64"), explicit: "linux/arm64"},
		{name: "single", document: targetPlatformDocument(t, "linux/amd64")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := deploy.SetDesiredStateV1(t.Context(), dir, test.document, test.document.Blueprint.Compatibility.Platforms[0], registry.ValidatePackageRequest); err != nil {
				t.Fatal(err)
			}
			result, err := restageCurrentDesiredPlatformV1(t.Context(), dir, test.explicit, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := test.document.Blueprint.Compatibility.Platforms[0]
			if test.explicit != "" {
				want = targetPlatform(t, test.explicit)
			}
			if result.State.Platform != want {
				t.Fatalf("result = %#v, want platform %#v", result, want)
			}
		})
	}
}

func TestForceRecoverLegacyComponentsStagingInvalidatesAndCleansPriorGeneration(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	previous := &deploy.EnvironmentGenerationState{
		Reference: "reploy/env/legacy-demo:g-old",
	}
	recovered := deploy.StateV1{
		Current: nil,
		Staging: &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1},
	}
	order := []string{}
	result, err := forceRecoverLegacyComponentsStagingV1(
		t.Context(),
		dir,
		"",
		RunOptions{DockerPreflightTimeout: 9 * time.Second},
		forceLegacyComponentsStagingBackendV1{
			acquire: func(context.Context, string) (*deploy.OperationLock, error) {
				order = append(order, "acquire")
				return operation, nil
			},
			prepare: func(
				_ *deploy.OperationLock,
				_ deploy.DesiredPlatformSelector,
				preserve bool,
			) (deploy.LegacyComponentsStagingRecoveryV1, error) {
				order = append(order, "prepare")
				if !preserve {
					t.Fatal("implicit recovery did not preserve the selected platform")
				}
				return deploy.LegacyComponentsStagingRecoveryV1{
					State:               recovered,
					PreviousCurrent:     previous,
					PreviousEnvironment: "legacy-demo",
				}, nil
			},
			admit: func(
				_ context.Context,
				gotDir string,
				gotOperation *deploy.OperationLock,
				input ControlAdmissionInputV1,
			) (AdmittedControlV1, error) {
				order = append(order, "admit")
				if gotDir != dir || gotOperation != operation ||
					input.Mode != ControlAdmissionForceV1 ||
					input.GenerationReference != previous.Reference ||
					input.DockerPreflightTimeout != 9*time.Second {
					t.Fatalf("admission = %q/%p/%#v", gotDir, gotOperation, input)
				}
				return AdmittedControlV1{
					Operation: operation,
					Marker:    deploy.ControlMarkerV1{ID: "control-0000000000000001"},
				}, nil
			},
			complete: func(got *deploy.OperationLock, marker string, _ *deploy.ControlLeaseV1) error {
				order = append(order, "complete")
				if got != operation || marker != "control-0000000000000001" {
					t.Fatalf("completion = %p/%q", got, marker)
				}
				return got.Unlock()
			},
			validateReference: func(reference, environment, gotDir string) error {
				order = append(order, "validate")
				if reference != previous.Reference ||
					environment != "legacy-demo" ||
					gotDir != dir {
					t.Fatalf(
						"validate = %q/%q/%q",
						reference,
						environment,
						gotDir,
					)
				}
				return nil
			},
			stopOwned: func(
				_ context.Context,
				got *deploy.OperationLock,
				environment string,
				gotDir string,
				options RunOptions,
			) error {
				order = append(order, "stop")
				if got != operation || gotDir != dir ||
					environment != "legacy-demo" ||
					options.DockerPreflightTimeout != 9*time.Second {
					t.Fatalf(
						"stop = %p/%q/%q/%#v",
						got,
						environment,
						gotDir,
						options,
					)
				}
				return nil
			},
			commit: func(
				got *deploy.OperationLock,
				recovery deploy.LegacyComponentsStagingRecoveryV1,
			) error {
				order = append(order, "commit")
				if got != operation || recovery.State.Current != nil {
					t.Fatalf("commit = %p/%#v", got, recovery)
				}
				return nil
			},
			removeReference: func(
				_ context.Context,
				reference string,
				environment string,
				gotDir string,
			) error {
				order = append(order, "remove")
				if reference != previous.Reference ||
					environment != "legacy-demo" ||
					gotDir != dir {
					t.Fatalf(
						"remove = %q/%q/%q",
						reference,
						environment,
						gotDir,
					)
				}
				return nil
			},
			syncControl: func(context.Context, string) (bool, error) {
				order = append(order, "sync")
				return true, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{
		"acquire", "prepare", "validate", "admit", "stop",
		"remove", "commit", "complete", "sync",
	}
	if !result.Changed || result.State.Current != nil ||
		!reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("result/order = %#v/%#v", result, order)
	}
}

func TestStopLegacyStagedWorkloadForRecoveryUsesDeploymentIdentity(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	if err := os.WriteFile(
		filepath.Join(dir, StateFileName),
		[]byte(`{"schema":"state-v1","Blueprint":{"Document":{"Environment":{"Components":{}}}}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, DockerEnvFileName),
		[]byte("REPLOY_CONTAINER_NAME=unrelated-project\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	wantProject, err := legacyStagedComposeProjectNameV1("legacy-demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = stopLegacyStagedWorkloadForRecoveryWithV1(
		t.Context(),
		operation,
		"legacy-demo",
		dir,
		RunOptions{DockerPreflightTimeout: 11 * time.Second},
		func(ctx context.Context, project string, timeout time.Duration) error {
			calls++
			if ctx != t.Context() ||
				project != wantProject ||
				timeout != 11*time.Second {
				t.Fatalf("project removal = %v/%q/%s", ctx, project, timeout)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("project removal calls = %d", calls)
	}
}

func TestStopLegacyStagedWorkloadForRecoveryHandlesMissingRuntimeInputs(t *testing.T) {
	dir := t.TempDir()
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	calls := 0
	wantProject, err := legacyStagedComposeProjectNameV1("legacy-demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	err = stopLegacyStagedWorkloadForRecoveryWithV1(
		t.Context(),
		operation,
		"legacy-demo",
		dir,
		RunOptions{},
		func(_ context.Context, project string, _ time.Duration) error {
			calls++
			if project != wantProject {
				t.Fatalf("project = %q, want %q", project, wantProject)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("project removal calls = %d", calls)
	}
}
