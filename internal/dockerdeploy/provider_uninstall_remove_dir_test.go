package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRemoveProviderUninstallDeploymentTransfersLockThenDeletesTombstone(t *testing.T) {
	dir := t.TempDir()
	operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, dir)
	installation := installedBuildPublicationInstallation(dir)
	plan := providerUninstallPlanV1{
		State: current.State, Installation: installation, Environment: "demo",
		GenerationReference: current.Generation.Reference, Backend: installBackendLinuxSystemd, RemoveDir: true,
	}
	tombstone := filepath.Join(filepath.Dir(dir), ".removed")
	order := []string{}
	lease := new(deploy.ControlLeaseV1)
	var stdout bytes.Buffer
	err := removeProviderUninstallDeploymentWithV1(t.Context(), operation, "control-0000000000000001", lease, plan, RunOptions{Stdout: &stdout}, providerUninstallRemoveDirBackendV1{
		newStore: func(root string) (providerstore.Store, error) {
			order = append(order, "store")
			if root != dir {
				t.Fatalf("store root = %q", root)
			}
			return providerstore.Store{}, nil
		},
		load: func(_ context.Context, got *deploy.OperationLock, _ providerstore.Store, environment string, root string) (CurrentBuild, bool, error) {
			order = append(order, "load")
			if got != operation || environment != "demo" || root != dir {
				t.Fatalf("load identity: operation=%p environment=%q root=%q", got, environment, root)
			}
			return current, true, nil
		},
		complete: func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error {
			return errors.New("unexpected marker completion")
		},
		releaseLease: func(got *deploy.ControlLeaseV1) error {
			order = append(order, "lease")
			if got != lease {
				t.Fatalf("lease identity = %p, want %p", got, lease)
			}
			return nil
		},
		removeMarker: func(got *deploy.OperationLock, markerID string) error {
			order = append(order, "marker")
			if got != operation || markerID != "control-0000000000000001" {
				t.Fatalf("marker identity: operation=%p id=%q", got, markerID)
			}
			return nil
		},
		reserve: func(root string) (string, error) {
			order = append(order, "reserve")
			if root != dir {
				t.Fatalf("reserve root = %q", root)
			}
			return tombstone, nil
		},
		rename: func(oldPath string, newPath string) error {
			order = append(order, "rename:"+oldPath+"->"+newPath)
			return nil
		},
		unlock: func(got *deploy.OperationLock) error {
			order = append(order, "unlock")
			return got.Unlock()
		},
		removeReference: func(_ context.Context, image providers.RealizedImageV1, reference string, environment string, root string) error {
			order = append(order, "reference")
			if !reflect.DeepEqual(image, current.Lock.FinalImage) || reference != current.Generation.Reference || environment != "demo" || root != dir {
				t.Fatalf("reference cleanup identity: image=%#v reference=%q environment=%q root=%q", image, reference, environment, root)
			}
			return nil
		},
		removeAll: func(root string) error {
			order = append(order, "remove:"+root)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("remove deployment: %v", err)
	}
	want := []string{
		"store", "load", "reserve", "marker", "lease", "rename:" + dir + "->" + tombstone,
		"unlock", "reference", "remove:" + tombstone,
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("removal order = %#v, want %#v", order, want)
	}
	if !strings.Contains(stdout.String(), "uninstalled service: demo") {
		t.Fatalf("success output = %q", stdout.String())
	}
	if err := operation.RequireHeld(); err == nil {
		t.Fatal("deployment removal retained operation lock")
	}
}

func TestRemoveProviderUninstallDeploymentRestoresPublicPathWhenReferenceRemovalFails(t *testing.T) {
	dir := t.TempDir()
	operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, dir)
	plan := providerUninstallPlanV1{
		Installation: installedBuildPublicationInstallation(dir), Environment: "demo",
		GenerationReference: current.Generation.Reference, RemoveDir: true,
	}
	tombstone := filepath.Join(filepath.Dir(dir), ".removed")
	want := errors.New("docker unavailable")
	renames := [][2]string{}
	removed := false
	lease := new(deploy.ControlLeaseV1)
	err := removeProviderUninstallDeploymentWithV1(t.Context(), operation, "control-0000000000000002", lease, plan, RunOptions{}, providerUninstallRemoveDirBackendV1{
		newStore: func(string) (providerstore.Store, error) { return providerstore.Store{}, nil },
		load: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
			return current, true, nil
		},
		complete: func(*deploy.OperationLock, string, *deploy.ControlLeaseV1) error {
			return errors.New("unexpected complete")
		},
		releaseLease: func(got *deploy.ControlLeaseV1) error {
			if got != lease {
				t.Fatalf("lease identity = %p, want %p", got, lease)
			}
			return nil
		},
		removeMarker: func(*deploy.OperationLock, string) error { return nil },
		reserve:      func(string) (string, error) { return tombstone, nil },
		rename: func(oldPath string, newPath string) error {
			renames = append(renames, [2]string{oldPath, newPath})
			return nil
		},
		unlock: func(operation *deploy.OperationLock) error { return operation.Unlock() },
		removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
			return want
		},
		removeAll: func(string) error {
			removed = true
			return nil
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("reference failure = %v, want %v", err, want)
	}
	wantRenames := [][2]string{{dir, tombstone}, {tombstone, dir}}
	if !reflect.DeepEqual(renames, wantRenames) {
		t.Fatalf("renames = %#v, want %#v", renames, wantRenames)
	}
	if removed {
		t.Fatal("reference failure deleted tombstone")
	}
}

func TestReserveProviderUninstallTombstoneLeavesOnlyAnAbsentSiblingPath(t *testing.T) {
	dir := t.TempDir()
	tombstone, err := reserveProviderUninstallTombstoneV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(tombstone) != filepath.Dir(dir) || !strings.HasPrefix(filepath.Base(tombstone), "."+filepath.Base(dir)+".reploy-uninstall-") {
		t.Fatalf("tombstone = %q for %q", tombstone, dir)
	}
	if _, err := os.Lstat(tombstone); !os.IsNotExist(err) {
		t.Fatalf("reserved tombstone left filesystem entry: %v", err)
	}
}

func TestRemoveProviderUninstallDeploymentCancellationCompletesAdmission(t *testing.T) {
	dir := t.TempDir()
	operation, _, _ := installedBuildPublicationSourceFixtureAtDir(t, dir)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	completed := false
	lease := new(deploy.ControlLeaseV1)
	err := removeProviderUninstallDeploymentWithV1(ctx, operation, "control-0000000000000004", lease, providerUninstallPlanV1{RemoveDir: true}, RunOptions{}, providerUninstallRemoveDirBackendV1{
		newStore: func(string) (providerstore.Store, error) { return providerstore.Store{}, nil },
		load: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
			return CurrentBuild{}, false, nil
		},
		complete: func(got *deploy.OperationLock, markerID string, gotLease *deploy.ControlLeaseV1) error {
			completed = true
			if got != operation || markerID != "control-0000000000000004" || gotLease != lease {
				t.Fatalf("completion identity: operation=%p marker=%q lease=%p", got, markerID, gotLease)
			}
			return got.Unlock()
		},
		releaseLease:    func(*deploy.ControlLeaseV1) error { return nil },
		removeMarker:    func(*deploy.OperationLock, string) error { return nil },
		reserve:         func(string) (string, error) { return "unused", nil },
		rename:          func(string, string) error { return nil },
		unlock:          func(*deploy.OperationLock) error { return nil },
		removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error { return nil },
		removeAll:       func(string) error { return nil },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if !completed {
		t.Fatal("cancellation left admission outstanding")
	}
}
