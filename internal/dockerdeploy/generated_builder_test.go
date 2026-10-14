package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

func stubGeneratedImageBuilder(t *testing.T) {
	t.Helper()
	old := probeGeneratedImageBuilder
	probeGeneratedImageBuilder = func(context.Context, RunOptions) (*generatedImageBuilder, error) {
		return &generatedImageBuilder{endpoint: "unix:///private/test.sock", engine: "Docker", serverVersion: "29.8.1", clientVersion: "29.8.1"}, nil
	}
	t.Cleanup(func() { probeGeneratedImageBuilder = old })
}

func TestGeneratedBuilderIdentityFromStructuredServer(t *testing.T) {
	for _, test := range []struct{ name, platform, server, components, os, want string }{
		{"Docker Engine", "Docker Engine - Community", "29.8.1", `[{"Name":"Engine","Version":"29.8.1"}]`, "linux", "Docker"},
		{"Desktop", "Docker Desktop 4.93.0", "29.8.1", `[{"Name":"Engine","Version":"29.8.1"}]`, "linux", "Docker"},
		{"Podman with compatibility component", "linux/amd64/ubuntu-24.04", "4.9.3", `[{"Name":"Podman Engine","Version":"4.9.3"},{"Name":"Engine","Version":"4.9.3"}]`, "linux", "Podman"},
		{"Podman only", "linux/amd64/debian", "6.1.4", `[{"Name":"Podman Engine","Version":"6.1.4"}]`, "linux", "Podman"},
		{"unknown", "Docker-ish", "29.8.1", `[]`, "linux", ""},
		{"version conflict", "linux", "6.1.4", `[{"Name":"Podman Engine","Version":"6.1.4"},{"Name":"Engine","Version":"24.0"}]`, "linux", ""},
		{"identity conflict", "Docker Desktop", "6.1.4", `[{"Name":"Podman Engine","Version":"6.1.4"}]`, "linux", ""},
		{"duplicate", "linux", "6.1.4", `[{"Name":"Podman Engine","Version":"6.1.4"},{"Name":"Podman Engine","Version":"6.1.4"}]`, "linux", ""},
		{"old Docker", "Docker Engine", "23.0", `[{"Name":"Engine","Version":"23.0"}]`, "linux", ""},
		{"nonlinux", "Docker", "29.8.1", `[{"Name":"Engine","Version":"29.8.1"}]`, "windows", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"Client":{"Version":"29.8.1"},"Server":{"Platform":{"Name":` + string(mustJSON(t, test.platform)) + `},"Version":` + string(mustJSON(t, test.server)) + `,"Os":` + string(mustJSON(t, test.os)) + `,"Components":` + test.components + `}}`
			builder, err := parseGeneratedImageBuilder(raw)
			if test.want == "" {
				if err == nil {
					t.Fatal("unidentified engine accepted")
				}
				return
			}
			if err != nil || builder.engine != test.want || builder.clientVersion != "29.8.1" || builder.serverVersion != test.server {
				t.Fatalf("builder=%#v error=%v", builder, err)
			}
		})
	}
	for _, raw := range []string{`null`, `{}`, `{"Client":{}}`, `{"Server":{}}`, `not-json`} {
		if _, err := parseGeneratedImageBuilder(raw); err == nil {
			t.Fatalf("malformed response accepted: %s", raw)
		}
	}
}
func mustJSON(t *testing.T, v string) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGeneratedBuilderSelectionIsInvocationOwned(t *testing.T) {
	old := probeGeneratedImageBuilder
	defer func() { probeGeneratedImageBuilder = old }()
	calls := 0
	probeGeneratedImageBuilder = func(context.Context, RunOptions) (*generatedImageBuilder, error) {
		calls++
		return &generatedImageBuilder{mode: generatedPodmanAPI, endpoint: "unix:///one.sock", engine: "Podman", serverVersion: "6.1.4", clientVersion: "29.8.1"}, nil
	}
	ctx := withGeneratedBuilderSession(t.Context())
	var progress bytes.Buffer
	first, err := selectGeneratedImageBuilder(ctx, RunOptions{Progress: &progress})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_HOST", "unix:///different.sock")
	t.Setenv("DOCKER_BUILDKIT", "invalid")
	second, err := selectGeneratedImageBuilder(ctx, RunOptions{Progress: &progress})
	if err != nil || first != second || calls != 1 || strings.Count(progress.String(), "Using Podman") != 1 {
		t.Fatalf("selection reread or duplicated: calls=%d %q %v", calls, progress.String(), err)
	}
	if _, err := selectGeneratedImageBuilder(withGeneratedBuilderSession(t.Context()), RunOptions{}); err != nil || calls != 2 {
		t.Fatal("selection leaked across invocations")
	}
	if _, err := bindMaterializationBuilderContext(ctx, nil); err == nil {
		t.Fatal("typed materialization accepted a legacy nil marker")
	}
	if _, err := bindGenericMaterializationBuilderContext(ctx, nil); err != nil {
		t.Fatalf("generic legacy nil-marker candidate rejected: %v", err)
	}
	for _, other := range []*generatedImageBuilder{{endpoint: "unix:///other.sock", mode: generatedPodmanAPI}} {
		if _, err := bindMaterializationBuilderContext(ctx, other); err == nil {
			t.Fatal("different builder accepted")
		}
	}
	bound, err := bindMaterializationBuilderContext(t.Context(), first)
	if err != nil || currentGeneratedImageBuilder(bound) != first {
		t.Fatal("standalone layer lost its selected builder")
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	conflicting := BuiltImageCandidate{
		ImageID: rendererDigest("c"),
		builder: &generatedImageBuilder{mode: generatedPodmanAPI, endpoint: "unix:///other.sock"},
	}
	if _, err := inspectBuiltImageCandidate(ctx, conflicting, platform, func(context.Context, ...string) (string, error) {
		t.Fatal("conflicting generic inspection reached Docker runner")
		return "", nil
	}); err == nil || !strings.Contains(err.Error(), "materialization builder does not match the invocation") {
		t.Fatalf("conflicting generic inspection error = %v", err)
	}
	if err := removeBuiltImageCandidate(ctx, conflicting, func(context.Context, ...string) (string, error) {
		t.Fatal("conflicting generic cleanup reached Docker runner")
		return "", nil
	}); err == nil || !strings.Contains(err.Error(), "materialization builder does not match the invocation") {
		t.Fatalf("conflicting generic cleanup error = %v", err)
	}
	marker := BuiltImageCandidate{ImageID: rendererDigest("a"), builder: first}
	if _, err := inspectBuiltImageCandidate(t.Context(), marker, platform, func(ctx context.Context, _ ...string) (string, error) {
		if selected := currentGeneratedImageBuilder(ctx); selected != first {
			t.Fatalf("generic inspection changed selected builder: %#v", selected)
		}
		return "", errors.New("stop inspection")
	}); err == nil || !strings.Contains(err.Error(), "stop inspection") {
		t.Fatalf("generic inspection error = %v", err)
	}
	if err := removeBuiltImageCandidate(t.Context(), marker, func(ctx context.Context, _ ...string) (string, error) {
		if selected := currentGeneratedImageBuilder(ctx); selected != first {
			t.Fatalf("generic cleanup changed selected builder: %#v", selected)
		}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy := BuiltImageCandidate{ImageID: rendererDigest("b")}
	if _, err := inspectBuiltImageCandidate(ctx, legacy, platform, func(ctx context.Context, _ ...string) (string, error) {
		if selected := currentGeneratedImageBuilder(ctx); selected != first {
			t.Fatalf("legacy inspection changed selected builder: %#v", selected)
		}
		return "", errors.New("stop legacy inspection")
	}); err == nil || !strings.Contains(err.Error(), "stop legacy inspection") {
		t.Fatalf("legacy inspection error = %v", err)
	}
	if err := removeBuiltImageCandidate(ctx, legacy, func(ctx context.Context, args ...string) (string, error) {
		if selected := currentGeneratedImageBuilder(ctx); selected != first {
			t.Fatalf("legacy cleanup changed selected builder: %#v", selected)
		}
		if !reflect.DeepEqual(args, []string{"image", "rm", string(legacy.ImageID)}) {
			t.Fatalf("legacy cleanup args = %v", args)
		}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := selectGeneratedImageBuilder(canceled, RunOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}

func TestGeneratedBuilderProbeFailureAndCleanup(t *testing.T) {
	for _, scenario := range []string{"success", "build-failure", "missing-iid", "wrong-loaded-id", "ignored-run", "create-cancelled", "wrong-owner", "cleanup-failure"} {
		t.Run(scenario, func(t *testing.T) {
			store, err := providerstore.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			platform, err := blueprint.ParsePlatform("linux/amd64")
			if err != nil {
				t.Fatal(err)
			}
			builder := &generatedImageBuilder{mode: generatedPodmanAPI, endpoint: "unix:///private/probe.sock"}
			var workspace, proof, image string
			container := false
			removedContainer := false
			removedImage := false
			imageID := string(rendererDigest("a"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend := generatedBuilderProbeBackend{
				build: func(spec CommandSpec, options RunOptions) error {
					workspace = filepath.Dir(commandOption(t, spec.Args, "--iidfile"))
					image = commandOption(t, spec.Args, "--tag")
					raw, err := os.ReadFile(filepath.Join(workspace, "proof"))
					if err != nil {
						t.Fatal(err)
					}
					proof = string(raw)
					file, err := os.ReadFile(commandOption(t, spec.Args, "--file"))
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(file), "--network=none") || !strings.Contains(string(file), ",readonly") || commandEnvironmentValueV1(spec, "DOCKER_BUILDKIT") != "0" || commandEnvironmentValueV1(spec, "DOCKER_HOST") != builder.endpoint || spec.Args[1] != "--no-cache" {
						t.Fatalf("probe contract changed: %#v", spec)
					}
					if scenario == "build-failure" {
						return errors.New("RUN unsupported")
					}
					if scenario == "missing-iid" {
						return nil
					}
					return os.WriteFile(commandOption(t, spec.Args, "--iidfile"), []byte(imageID), 0600)
				},
				docker: func(c context.Context, args ...string) (string, error) {
					switch strings.Join(args[:2], " ") {
					case "image ls":
						if image != "" && !removedImage {
							return imageID + "\n" + imageID, nil
						}
						return "", nil
					case "image inspect":
						if scenario == "wrong-loaded-id" {
							return string(rendererDigest("b")), nil
						}
						return imageID, nil
					case "image rm":
						removedImage = true
						return "", nil
					case "container create":
						container = true
						if scenario == "create-cancelled" {
							cancel()
							return "", context.Canceled
						}
						return strings.TrimPrefix(imageID, "sha256:"), nil
					case "container inspect":
						if !container {
							return "", errors.New("No such container")
						}
						label := proof
						if scenario == "wrong-owner" {
							label = "other"
						}
						return strings.TrimPrefix(imageID, "sha256:") + "\t" + label, nil
					case "container cp":
						if scenario == "ignored-run" {
							return "", errors.New("proof absent")
						}
						return "", os.WriteFile(args[len(args)-1], []byte(proof), 0600)
					case "container rm":
						if c.Err() != nil {
							t.Fatal("cleanup retained canceled context")
						}
						if scenario == "cleanup-failure" {
							return "", errors.New("cleanup unavailable")
						}
						removedContainer = true
						return "", nil
					default:
						t.Fatalf("unexpected operation: %v", args)
						return "", nil
					}
				},
			}
			err = probeGeneratedBuilderFeaturesWithBackend(ctx, store, "reploy/build/12345678:base", platform, RunOptions{}, builder, backend)
			if (scenario == "success") != (err == nil) {
				t.Fatalf("scenario %s returned %v", scenario, err)
			}
			if scenario == "create-cancelled" && !removedContainer {
				t.Fatal("create succeeded but canceled response leaked container")
			}
			if scenario == "wrong-owner" && removedContainer {
				t.Fatal("unowned container removed")
			}
			if scenario == "wrong-owner" || scenario == "cleanup-failure" {
				if err == nil || !strings.Contains(err.Error(), "builder probe evidence retained at "+workspace) {
					t.Fatalf("cleanup failure did not retain evidence: %v", err)
				}
				if _, statErr := os.Stat(workspace); statErr != nil {
					t.Fatalf("cleanup failure did not retain workspace: %v", statErr)
				}
			} else {
				if scenario == "success" && (!removedContainer || !removedImage) {
					t.Fatal("probe resources leaked")
				}
				if _, statErr := os.Stat(workspace); !os.IsNotExist(statErr) {
					t.Fatalf("probe workspace retained after successful cleanup: %v", statErr)
				}
			}
		})
	}
}
