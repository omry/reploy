package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestControlledSessionWatchdogDisarmIndependentlyVerifiesCleanup(t *testing.T) {
	manifest := controlledSessionWatchdogManifestFixtureV1(t)
	content, err := deploy.EncodeControlledSessionCleanupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var ready bytes.Buffer
	inspectCount := 0
	containerRemoved := false
	channelVerified := false
	err = runControlledSessionWatchdogV1(bytes.NewReader(content), bytes.NewReader([]byte{controlledSessionWatchdogDisarmByte}), &ready, controlledSessionWatchdogCleanupBackendV1{
		currentBootSession: func() (string, error) { return manifest.BootSession, nil },
		inspectContainer: func(context.Context, string) (map[string]string, bool, error) {
			inspectCount++
			return nil, false, nil
		},
		removeContainer: func(context.Context, string) error { containerRemoved = true; return nil },
		removeChannel:   func(string) error { channelVerified = true; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if inspectCount != 2 || containerRemoved || !channelVerified || !bytes.Equal(ready.Bytes(), []byte{controlledSessionWatchdogReadyByte}) {
		t.Fatalf("inspect count=%d, container removed=%t, channel verified=%t, ready=%v", inspectCount, containerRemoved, channelVerified, ready.Bytes())
	}
}

func TestControlledSessionWatchdogParentLossRemovesOnlyManifestResources(t *testing.T) {
	manifest := controlledSessionWatchdogManifestFixtureV1(t)
	content, err := deploy.EncodeControlledSessionCleanupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	containers := map[string]map[string]string{
		manifest.Controller.ID: controlledSessionWatchdogLabelsV1(manifest.LiveRunID, manifest.Controller),
		manifest.Workload.ID:   controlledSessionWatchdogLabelsV1(manifest.LiveRunID, manifest.Workload),
	}
	var operations []string
	backend := controlledSessionWatchdogCleanupBackendV1{
		currentBootSession: func() (string, error) { return manifest.BootSession, nil },
		inspectContainer: func(_ context.Context, id string) (map[string]string, bool, error) {
			operations = append(operations, "inspect:"+id)
			labels, found := containers[id]
			return labels, found, nil
		},
		removeContainer: func(_ context.Context, id string) error {
			operations = append(operations, "remove:"+id)
			delete(containers, id)
			return nil
		},
		removeChannel: func(path string) error {
			operations = append(operations, "channel:"+path)
			return nil
		},
	}
	if err := runControlledSessionWatchdogV1(bytes.NewReader(content), strings.NewReader(""), io.Discard, backend); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"inspect:" + manifest.Workload.ID, "remove:" + manifest.Workload.ID, "inspect:" + manifest.Workload.ID,
		"inspect:" + manifest.Controller.ID, "remove:" + manifest.Controller.ID, "inspect:" + manifest.Controller.ID,
		"channel:" + manifest.ChannelDirectory,
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("operations = %#v, want %#v", operations, want)
	}
}

func TestControlledSessionWatchdogRefusesMismatchedOwnership(t *testing.T) {
	manifest := controlledSessionWatchdogManifestFixtureV1(t)
	removed := false
	backend := controlledSessionWatchdogCleanupBackendV1{
		currentBootSession: func() (string, error) { return manifest.BootSession, nil },
		inspectContainer: func(_ context.Context, id string) (map[string]string, bool, error) {
			container := manifest.Controller
			if id == manifest.Workload.ID {
				container = manifest.Workload
			}
			labels := controlledSessionWatchdogLabelsV1(manifest.LiveRunID, container)
			labels["io.reploy.session.live-run"] = "run-other"
			return labels, true, nil
		},
		removeContainer: func(context.Context, string) error { removed = true; return nil },
		removeChannel:   func(string) error { return nil },
	}
	err := cleanupControlledSessionFromWatchdogV1(t.Context(), manifest, backend)
	if err == nil || !strings.Contains(err.Error(), "ownership label") || removed {
		t.Fatalf("cleanup error=%v, removed=%t", err, removed)
	}
}

func TestControlledSessionWatchdogRejectsPriorBootBeforeReady(t *testing.T) {
	manifest := controlledSessionWatchdogManifestFixtureV1(t)
	content, err := deploy.EncodeControlledSessionCleanupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var ready bytes.Buffer
	err = runControlledSessionWatchdogV1(bytes.NewReader(content), strings.NewReader(""), &ready, controlledSessionWatchdogCleanupBackendV1{
		currentBootSession: func() (string, error) { return "different-boot", nil },
		inspectContainer: func(context.Context, string) (map[string]string, bool, error) {
			return nil, false, errors.New("must not inspect")
		},
		removeContainer: func(context.Context, string) error { return errors.New("must not remove") },
		removeChannel:   func(string) error { return errors.New("must not remove") },
	})
	if err == nil || !strings.Contains(err.Error(), "different host boot") || ready.Len() != 0 {
		t.Fatalf("prior-boot result error=%v, ready=%v", err, ready.Bytes())
	}
}

func TestControlledSessionWatchdogRechecksBootBeforeParentLossCleanup(t *testing.T) {
	manifest := controlledSessionWatchdogManifestFixtureV1(t)
	content, err := deploy.EncodeControlledSessionCleanupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	cleanupCalled := false
	err = runControlledSessionWatchdogV1(bytes.NewReader(content), strings.NewReader(""), io.Discard, controlledSessionWatchdogCleanupBackendV1{
		currentBootSession: func() (string, error) {
			checks++
			if checks == 1 {
				return manifest.BootSession, nil
			}
			return "different-boot", nil
		},
		inspectContainer: func(context.Context, string) (map[string]string, bool, error) {
			cleanupCalled = true
			return nil, false, nil
		},
		removeContainer: func(context.Context, string) error { cleanupCalled = true; return nil },
		removeChannel:   func(string) error { cleanupCalled = true; return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "different host boot") || checks != 2 || cleanupCalled {
		t.Fatalf("boot checks=%d, cleanup called=%t, error=%v", checks, cleanupCalled, err)
	}
}

func controlledSessionWatchdogManifestFixtureV1(t *testing.T) deploy.ControlledSessionCleanupManifest {
	t.Helper()
	plan := controlledSessionControllerIntegrationPlanV1(t, "test-image", []string{"/controller"})
	ownership := controlledSessionOwnershipFromPlanV1(plan, dockerControllerTestContainerIDV1, dockerWorkloadTestContainerIDV1)
	ownership.BootSession = "boot-session"
	manifest, err := deploy.ControlledSessionCleanupManifestFromOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func controlledSessionWatchdogLabelsV1(liveRunID string, container deploy.ControlledSessionContainerOwnershipV1) map[string]string {
	return map[string]string{
		"io.reploy.session.build":       container.BuildIdentity,
		"io.reploy.session.environment": container.DeploymentID,
		"io.reploy.session.generation":  container.GenerationReference,
		"io.reploy.session.live-run":    liveRunID,
		"io.reploy.session.role":        container.Role,
	}
}
