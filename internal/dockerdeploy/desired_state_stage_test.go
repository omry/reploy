package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"testing"

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
