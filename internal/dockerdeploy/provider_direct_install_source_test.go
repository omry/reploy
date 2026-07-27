package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestWithDirectProviderInstallSourceV1StagesStateOnlyAndCleansUp(t *testing.T) {
	ref, _ := writeDesiredStateStagePack(t, "0.1.0")
	var workspace string
	err := withDirectProviderInstallSourceBackendV1(t.Context(), directProviderInstallSourceInputV1{Pack: ref, ExplicitPlatform: "linux/amd64"}, func(_ context.Context, dir string) error {
		workspace = filepath.Dir(dir)
		operation, err := deploy.AcquireOperationLock(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		state, found, err := operation.ReadStateV1()
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
		if err != nil || !found || state.Current != nil || state.Deployment != nil {
			t.Fatalf("direct source state=%#v found=%v error=%v", state, found, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != ReployInternalDir {
			t.Fatalf("direct source entries = %#v", entries)
		}
		return nil
	}, directProviderInstallSourceBackendV1{
		mkdirTemp: os.MkdirTemp, removeAll: os.RemoveAll, stage: StagePackDesiredStateV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("direct source workspace survived success: %v", err)
	}
}

func TestWithDirectProviderInstallSourceV1CleansUpAfterInstallFailure(t *testing.T) {
	ref, _ := writeDesiredStateStagePack(t, "0.1.0")
	want := errors.New("install failed")
	var workspace string
	err := withDirectProviderInstallSourceBackendV1(t.Context(), directProviderInstallSourceInputV1{Pack: ref, ExplicitPlatform: "linux/amd64"}, func(_ context.Context, dir string) error {
		workspace = filepath.Dir(dir)
		return want
	}, directProviderInstallSourceBackendV1{
		mkdirTemp: os.MkdirTemp, removeAll: os.RemoveAll, stage: StagePackDesiredStateV1,
	})
	if !errors.Is(err, want) {
		t.Fatalf("direct source error = %v", err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("direct source workspace survived failure: %v", err)
	}
}
