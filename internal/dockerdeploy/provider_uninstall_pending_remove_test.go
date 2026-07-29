package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func pendingProviderUninstallRemovalFixture(
	t *testing.T,
) (string, string, CurrentBuild) {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "demo")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, dir)
	state := current.State
	state.Deployment = &deploy.DeploymentStateV1{
		Schema:       deploy.DeploymentStateSchemaV1,
		Installation: installedBuildPublicationInstallation(dir),
	}
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	tombstone, err := providerUninstallTombstoneV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, tombstone); err != nil {
		t.Fatal(err)
	}
	return dir, tombstone, current
}

func TestRetryPendingProviderUninstallRemovalAuthenticatesAndCompletes(t *testing.T) {
	dir, tombstone, current := pendingProviderUninstallRemovalFixture(t)
	var removedReference string
	result, found, err := retryPendingProviderUninstallRemovalWithV1(
		t.Context(),
		dir,
		"demo",
		providerUninstallPendingRemovalBackendV1{
			acquire: deploy.AcquireOperationLock,
			removeReference: func(
				_ context.Context,
				image providers.RealizedImageV1,
				reference string,
				environment string,
				deploymentDir string,
			) error {
				if image != current.Lock.FinalImage || environment != "demo" || deploymentDir != dir {
					t.Fatalf(
						"remove identity: image=%#v environment=%q dir=%q",
						image, environment, deploymentDir,
					)
				}
				removedReference = reference
				return nil
			},
			finalize: finalizePendingProviderUninstallRemovalV1,
		},
	)
	if err != nil || !found {
		t.Fatalf("retry result=%#v found=%v err=%v", result, found, err)
	}
	if removedReference != current.Generation.Reference {
		t.Fatalf("removed reference = %q, want %q", removedReference, current.Generation.Reference)
	}
	if result.DeploymentDir != dir || result.Environment != "demo" ||
		result.Service != "demo" || !result.RemovedDirectory {
		t.Fatalf("retry result = %#v", result)
	}
	if _, err := os.Lstat(tombstone); !os.IsNotExist(err) {
		t.Fatalf("pending removal remains: %v", err)
	}
}

func TestRetryPendingProviderUninstallRemovalRetainsTombstoneForAnotherRetry(t *testing.T) {
	dir, tombstone, _ := pendingProviderUninstallRemovalFixture(t)
	want := errors.New("permission denied")
	_, found, err := retryPendingProviderUninstallRemovalWithV1(
		t.Context(),
		dir,
		"",
		providerUninstallPendingRemovalBackendV1{
			acquire:         deploy.AcquireOperationLock,
			removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error { return nil },
			finalize:        func(string, string) error { return want },
		},
	)
	if !found || !errors.Is(err, want) ||
		!strings.Contains(err.Error(), "partial removal retained at "+tombstone) ||
		!strings.Contains(err.Error(), "retry uninstall against "+dir) {
		t.Fatalf("found=%v error=%v", found, err)
	}
	if info, statErr := os.Lstat(tombstone); statErr != nil || !info.IsDir() {
		t.Fatalf("retained tombstone = %v, %v", info, statErr)
	}
}

func TestRetryPendingProviderUninstallRemovalUsesPreservedControlState(t *testing.T) {
	dir, tombstone, _ := pendingProviderUninstallRemovalFixture(t)
	control, err := providerUninstallControlV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(control, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(tombstone, ".reploy"),
		filepath.Join(control, ".reploy"),
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tombstone, "partially-retained"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, found, err := retryPendingProviderUninstallRemovalWithV1(
		t.Context(),
		dir,
		"",
		providerUninstallPendingRemovalBackendV1{
			acquire:         deploy.AcquireOperationLock,
			removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error { return nil },
			finalize:        finalizePendingProviderUninstallRemovalV1,
		},
	)
	if err != nil || !found {
		t.Fatalf("retry found=%v err=%v", found, err)
	}
	for _, path := range []string{tombstone, control} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("pending removal path remains at %s: %v", path, err)
		}
	}
}

func TestFinalizePendingProviderUninstallRemovalRejectsUnexpectedControlFiles(t *testing.T) {
	dir, tombstone, _ := pendingProviderUninstallRemovalFixture(t)
	control, err := providerUninstallControlV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(control, 0o700); err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(control, "user-data")
	if err := os.WriteFile(unexpected, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = finalizePendingProviderUninstallRemovalV1(dir, tombstone)
	if err == nil || !strings.Contains(err.Error(), "contains unexpected entries") {
		t.Fatalf("finalize error = %v", err)
	}
	if _, err := os.Lstat(unexpected); err != nil {
		t.Fatalf("unexpected control file was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tombstone, ".reploy")); err != nil {
		t.Fatalf("tombstone state was moved before control validation: %v", err)
	}
}

func TestRetryPendingProviderUninstallRemovalRejectsMismatchedTarget(t *testing.T) {
	dir, tombstone, _ := pendingProviderUninstallRemovalFixture(t)
	operation, err := deploy.AcquireOperationLock(t.Context(), tombstone)
	if err != nil {
		t.Fatal(err)
	}
	state, found, err := operation.ReadStateV1()
	if err != nil || !found {
		t.Fatalf("read state found=%v err=%v", found, err)
	}
	state.Deployment.Installation.TargetDir = filepath.Join(filepath.Dir(dir), "other")
	if err := operation.CommitStateV1(state.Current, state); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	removed := false
	_, found, err = retryPendingProviderUninstallRemovalWithV1(
		t.Context(),
		dir,
		"",
		providerUninstallPendingRemovalBackendV1{
			acquire: deploy.AcquireOperationLock,
			removeReference: func(context.Context, providers.RealizedImageV1, string, string, string) error {
				removed = true
				return nil
			},
			finalize: func(string, string) error {
				removed = true
				return nil
			},
		},
	)
	if !found || err == nil || !strings.Contains(err.Error(), "does not match requested deployment") {
		t.Fatalf("found=%v error=%v", found, err)
	}
	if removed {
		t.Fatal("mismatched pending removal was mutated")
	}
}
