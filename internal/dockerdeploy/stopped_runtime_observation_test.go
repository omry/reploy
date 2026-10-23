package dockerdeploy

import (
	"bytes"
	"context"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Use actual stop orchestration, runtime rendering/publication and read-only
// input validation. Engine/build/admission edges use existing fixtures.
func TestStoppedWorkloadObservationAfterRuntimePublicationAndStop(t *testing.T) {
	for _, scope := range []string{"staging", "user", "system"} {
		for _, env := range []string{"unchanged", "changed", "empty", "missing", "restored"} {
			for _, action := range []string{"status", "logs"} {
				t.Run(scope+"/"+env+"/"+action, func(t *testing.T) {
					dir := t.TempDir()
					current, _ := runtimeCurrentBuildFixture(t)
					plan := currentRuntimeFilePlanV1()
					plan.Docker.DeploymentDir = dir
					plan.Docker.Workload = &WorkloadExecutionPlan{Command: "serve"}
					plan.Document.Environment.Workload = &blueprint.Workload{Command: "serve"}
					var err error
					current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(plan.Document)
					if err != nil {
						t.Fatal(err)
					}
					if scope != "staging" {
						current.State.Deployment = &deploy.DeploymentStateV1{Installation: deploy.InstallationStateV1{Scope: scope}}
					}
					file := filepath.Join(dir, ".env")
					if _, err := publishPrivateWorkloadEnvironmentFileV1(file, []byte("TOKEN=synthetic\n"), true); err != nil {
						t.Fatal(err)
					}
					operation, err := deploy.AcquireOperationLock(t.Context(), dir)
					if err != nil {
						t.Fatal(err)
					}
					privatePlan := plan
					privatePlan.Docker.PrivateEnvironment = true
					if _, err := PublishCurrentRuntimeInputsV1(operation, dir, privatePlan); err != nil {
						t.Fatal(err)
					}
					if err := operation.Unlock(); err != nil {
						t.Fatal(err)
					}
					order := []string{}
					backend := currentWorkloadRunTestBackend(t, dir, current, plan, &order)
					backend.publishInputs = PublishCurrentRuntimeInputsV1
					backend.runLifecycle = func(_ context.Context, input CurrentWorkloadLifecycleInputV1) error {
						if input.Action != "down" {
							t.Fatalf("unexpected lifecycle action %q", input.Action)
						}
						return nil
					}
					if err := runCurrentWorkloadV1(t.Context(), CurrentWorkloadRunInputV1{DeploymentDir: dir, Action: "down"}, backend); err != nil {
						t.Fatal(err)
					}
					switch env {
					case "changed":
						_, err = publishPrivateWorkloadEnvironmentFileV1(file, []byte("TOKEN=changed-synthetic\n"), true)
					case "empty":
						_, err = publishPrivateWorkloadEnvironmentFileV1(file, []byte{}, true)
					case "missing":
						err = os.Remove(file)
					case "restored":
						err = os.Remove(file)
						if err == nil {
							_, err = publishPrivateWorkloadEnvironmentFileV1(file, []byte("TOKEN=synthetic\n"), true)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					before := map[string][]byte{}
					for _, name := range []string{DockerEnvFileName, ComposeFileName} {
						before[name], err = os.ReadFile(filepath.Join(dir, name))
						if err != nil {
							t.Fatal(err)
						}
					}
					observe := currentRuntimeObservationTestBackend(t, dir, current, plan, &order)
					observe.requireInputs = RequireCurrentRuntimeInputsV1
					observe.readLifecycle = func(*deploy.OperationLock) (deploy.WorkloadLifecycleV1, bool, error) {
						return deploy.WorkloadLifecycleV1{State: "down", ContainerID: strings.Repeat("a", 64), StartupComplete: true, TransferClaimed: true, HandoffComplete: true}, true, nil
					}
					observe.status = func(context.Context, *deploy.OperationLock, DockerExecutionPlan, time.Duration) (RuntimeStatusV1, error) {
						return RuntimeStatusV1{Status: "down"}, nil
					}
					var stdout bytes.Buffer
					err = runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: action, RunOptions: RunOptions{Stdout: &stdout}}, observe)
					for name, want := range before {
						got, readErr := os.ReadFile(filepath.Join(dir, name))
						if readErr != nil || !bytes.Equal(got, want) {
							t.Fatalf("observation modified %s", name)
						}
					}
					if err != nil {
						t.Fatalf("normal stop should allow %s: %v", action, err)
					}
				})
			}
		}
	}
}

func TestStoppedWorkloadObservationRetainsPrivateFailedStartInputs(t *testing.T) {
	for _, present := range []bool{false, true} {
		for _, action := range []string{"status", "ps", "logs"} {
			t.Run(action+"/private-file-present="+strconv.FormatBool(present), func(t *testing.T) {
				dir, current, plan := stoppedRuntimeObservationFixture(t, true)
				if present {
					if _, err := publishPrivateWorkloadEnvironmentFileV1(filepath.Join(dir, ".env"), []byte("TOKEN=synthetic\n"), true); err != nil {
						t.Fatal(err)
					}
				}
				order := []string{}
				backend := currentRuntimeObservationTestBackend(t, dir, current, plan, &order)
				backend.requireInputs = RequireCurrentRuntimeInputsV1
				backend.readLifecycle = func(*deploy.OperationLock) (deploy.WorkloadLifecycleV1, bool, error) {
					return deploy.WorkloadLifecycleV1{State: "down", TransferClaimed: true, HandoffComplete: false}, true, nil
				}
				backend.status = func(_ context.Context, _ *deploy.OperationLock, p DockerExecutionPlan, _ time.Duration) (RuntimeStatusV1, error) {
					if !p.PrivateEnvironment {
						t.Fatal("lost retained private runtime selection")
					}
					return RuntimeStatusV1{Status: "down"}, nil
				}
				assertStoppedRuntimeObservationReadOnly(t, dir, func() error {
					return runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: action}, backend)
				}, false)
			})
		}
	}
}

func TestStoppedWorkloadObservationRejectsUnrecognizedInputs(t *testing.T) {
	for _, problem := range []string{"compose-bytes", "environment-bytes", "file-mode", "symlink", "stale-plan"} {
		t.Run(problem, func(t *testing.T) {
			dir, current, plan := stoppedRuntimeObservationFixture(t, false)
			if _, err := publishPrivateWorkloadEnvironmentFileV1(filepath.Join(dir, ".env"), []byte("TOKEN=synthetic\n"), true); err != nil {
				t.Fatal(err)
			}
			switch problem {
			case "compose-bytes":
				if err := os.WriteFile(filepath.Join(dir, ComposeFileName), []byte("foreign content\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "environment-bytes":
				if err := os.WriteFile(filepath.Join(dir, DockerEnvFileName), []byte("foreign content\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "file-mode":
				if !hasPOSIXPermissionBits() {
					t.Skip("POSIX modes required")
				}
				if err := os.Chmod(filepath.Join(dir, ComposeFileName), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(dir, "target")
				if err := os.Rename(filepath.Join(dir, ComposeFileName), target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(dir, ComposeFileName)); err != nil {
					t.Skipf("symlink unsupported: %v", err)
				}
			case "stale-plan":
				plan.Docker.Image = "reploy/env/other:g-other"
			}
			order := []string{}
			backend := currentRuntimeObservationTestBackend(t, dir, current, plan, &order)
			backend.requireInputs = RequireCurrentRuntimeInputsV1
			backend.readLifecycle = func(*deploy.OperationLock) (deploy.WorkloadLifecycleV1, bool, error) {
				return deploy.WorkloadLifecycleV1{State: "down"}, true, nil
			}
			backend.status = func(context.Context, *deploy.OperationLock, DockerExecutionPlan, time.Duration) (RuntimeStatusV1, error) {
				t.Fatal("invalid inputs reached status")
				return RuntimeStatusV1{}, nil
			}
			assertStoppedRuntimeObservationReadOnly(t, dir, func() error {
				return runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: "status"}, backend)
			}, true)
		})
	}
}

func TestStoppedInputAlternativeRequiresRecordedDownWorkload(t *testing.T) {
	for _, state := range []string{"missing", "up", "starting", "stopping"} {
		t.Run(state, func(t *testing.T) {
			dir, current, plan := stoppedRuntimeObservationFixture(t, false)
			if _, err := publishPrivateWorkloadEnvironmentFileV1(filepath.Join(dir, ".env"), []byte("TOKEN=synthetic\n"), true); err != nil {
				t.Fatal(err)
			}
			order := []string{}
			backend := currentRuntimeObservationTestBackend(t, dir, current, plan, &order)
			backend.requireInputs = RequireCurrentRuntimeInputsV1
			backend.readLifecycle = func(*deploy.OperationLock) (deploy.WorkloadLifecycleV1, bool, error) {
				return deploy.WorkloadLifecycleV1{State: state}, state != "missing", nil
			}
			backend.status = func(context.Context, *deploy.OperationLock, DockerExecutionPlan, time.Duration) (RuntimeStatusV1, error) {
				t.Fatal("unconfirmed active mode reached status")
				return RuntimeStatusV1{}, nil
			}
			assertStoppedRuntimeObservationReadOnly(t, dir, func() error {
				return runCurrentRuntimeObservationV1(t.Context(), CurrentRuntimeObservationInputV1{DeploymentDir: dir, Action: "status"}, backend)
			}, true)
		})
	}
}

func stoppedRuntimeObservationFixture(t *testing.T, private bool) (string, CurrentBuild, CurrentRuntimePlanV1) {
	t.Helper()
	dir := t.TempDir()
	current, _ := runtimeCurrentBuildFixture(t)
	plan := currentRuntimeFilePlanV1()
	plan.Docker.DeploymentDir = dir
	plan.Docker.Workload = &WorkloadExecutionPlan{Command: "serve"}
	plan.Document.Environment.Workload = &blueprint.Workload{Command: "serve"}
	var err error
	current.State.Blueprint, err = blueprint.EncodeResolvedDocumentV1(plan.Document)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	rendered := plan
	rendered.Docker.PrivateEnvironment = private
	if _, err := PublishCurrentRuntimeInputsV1(operation, dir, rendered); err != nil {
		t.Fatal(err)
	}
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	return dir, current, plan
}

func assertStoppedRuntimeObservationReadOnly(t *testing.T, dir string, run func() error, wantError bool) {
	t.Helper()
	type snapshot struct {
		content  []byte
		mode     os.FileMode
		modified time.Time
	}
	before := map[string]snapshot{}
	for _, name := range []string{ComposeFileName, DockerEnvFileName, ".env"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = snapshot{content, info.Mode(), info.ModTime()}
	}
	err := run()
	if (err != nil) != wantError {
		t.Fatalf("observation error=%v, wantError=%t", err, wantError)
	}
	for _, name := range []string{ComposeFileName, DockerEnvFileName, ".env"} {
		path := filepath.Join(dir, name)
		want, found := before[name]
		info, err := os.Lstat(path)
		if !found {
			if !os.IsNotExist(err) {
				t.Fatalf("observation created %s", name)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(content, want.content) || info.Mode() != want.mode || !info.ModTime().Equal(want.modified) {
			t.Fatalf("observation changed %s", name)
		}
	}
}

func TestStoppedRuntimeLogsExplainOnlyConfirmedContainerAbsence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}
	for _, test := range []struct {
		name, message string
		missing       bool
	}{
		{"docker missing", "Error response from daemon: No such container: demo", true},
		{"podman missing", "Error: no container with name or ID demo found: no such container", true},
		{"permission failure", "Error response from daemon: permission denied", false},
		{"transport failure", "Error: connection refused", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFakeCommand(t, dir, "docker", "#!/bin/sh\nprintf '%s\\n' \"$REPLOY_TEST_INSPECT_ERROR\" >&2\nexit 1\n", "@exit /b 1\r\n")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("REPLOY_TEST_INSPECT_ERROR", test.message)
			restore := stubDockerPreflight(t, func(context.Context, CommandSpec, time.Duration) (string, error) { return "unix:///fixture.sock", nil })
			defer restore()
			_, err := CurrentRuntimeContainerIDV1(t.Context(), DockerExecutionPlan{ContainerName: "demo"}, time.Second)
			if err == nil {
				t.Fatal("inspection failure was accepted")
			}
			explained := strings.Contains(err.Error(), "no longer exists; its logs are unavailable")
			if explained != test.missing {
				t.Fatalf("absence=%t, error=%v", explained, err)
			}
		})
	}
}
