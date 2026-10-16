package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type podmanHealthcheckFixture struct {
	mu        sync.Mutex
	endpoint  string
	objects   map[string]podmanHealthcheckInspection
	updates   []string
	reject    bool
	wrongID   bool
	unchanged bool
	stall     bool
	docker    bool
}

func stubDockerHealthcheckProbe(t *testing.T, endpoint string) {
	t.Helper()
	original := probePodmanHealthcheckClient
	probePodmanHealthcheckClient = func(_ context.Context, actual string) (*podmanHealthcheckClient, error) {
		if actual != endpoint {
			t.Fatalf("healthcheck probe changed pinned endpoint: %q", actual)
		}
		return nil, nil
	}
	t.Cleanup(func() { probePodmanHealthcheckClient = original })
}

func newPodmanHealthcheckFixture(t *testing.T) *podmanHealthcheckFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("local Podman compatibility requires Linux")
	}
	// Full test names under a CI TMPDIR can exceed the Unix socket path limit.
	directory, err := os.MkdirTemp("/tmp", "reploy-hc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "engine.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &podmanHealthcheckFixture{endpoint: "unix://" + path, objects: map[string]podmanHealthcheckInspection{}}
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.serve))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return fixture
}

func (fixture *podmanHealthcheckFixture) serve(w http.ResponseWriter, r *http.Request) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if r.URL.Path == "/version" {
		if fixture.docker {
			io.WriteString(w, `{"Version":"29.0.0","Os":"linux","Components":[{"Name":"Engine","Version":"29.0.0"}]}`)
		} else {
			io.WriteString(w, `{"Version":"6.1.4","Os":"linux","Components":[{"Name":"Podman Engine","Version":"6.1.4"}]}`)
		}
		return
	}
	if r.URL.Path == "/v6.1.4/libpod/info" {
		io.WriteString(w, `{"host":{"security":{"rootless":false}}}`)
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 6 || parts[1] != "v6.1.4" || parts[2] != "libpod" || parts[3] != "containers" {
		http.NotFound(w, r)
		return
	}
	id := parts[4]
	object, ok := fixture.objects[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if parts[5] == "json" && r.Method == http.MethodGet {
		if fixture.wrongID {
			object.ID = strings.Repeat("f", 64)
		}
		json.NewEncoder(w).Encode(object)
		return
	}
	if parts[5] != "update" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if fixture.stall {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, map[string]any{"no_healthcheck": true}) {
		http.Error(w, "invalid update", http.StatusBadRequest)
		return
	}
	if fixture.reject {
		http.Error(w, "unsupported native update", http.StatusBadRequest)
		return
	}
	fixture.updates = append(fixture.updates, id)
	if !fixture.unchanged {
		object.Config.Healthcheck = &dockerHealthcheck{Test: []string{"NONE"}}
		fixture.objects[id] = object
	}
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintln(w, id)
}

func TestPodmanComposeReplacesOldTimerBeforePreparingNewContainer(t *testing.T) {
	fixture := newPodmanHealthcheckFixture(t)
	old, reused, fresh := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	oneoff, orphan := strings.Repeat("d", 64), strings.Repeat("e", 64)
	add := func(id string, interval int64, running bool) {
		object := fixturePodmanInspection(id, &dockerHealthcheck{Test: []string{"NONE"}, Interval: interval})
		object.Config.Labels = map[string]string{
			"com.docker.compose.project": "owned", "com.docker.compose.service": composeWorkloadService,
			"com.docker.compose.oneoff": "False",
		}
		object.State.Running = running
		if running {
			object.State.Pid, object.State.Status = 123, "running"
		}
		fixture.objects[id] = object
	}
	add(old, 30_000_000_000, true)
	add(reused, 0, true)
	add(oneoff, 30_000_000_000, true)
	add(orphan, 30_000_000_000, true)
	fixture.objects[oneoff].Config.Labels["com.docker.compose.oneoff"] = "True"
	fixture.objects[orphan].Config.Labels["com.docker.compose.service"] = "retired"
	stopped, removed, started := false, false, false
	raw := func(spec CommandSpec, options RunOptions) error {
		if host, _ := commandSpecEnvironmentValueV1(spec, "DOCKER_HOST"); host != fixture.endpoint {
			t.Fatal("Compose changed its selected endpoint")
		}
		if spec.Args[0] == "container" {
			if reflect.DeepEqual(spec.Args, []string{"container", "stop", old}) {
				stopped = true
				return nil
			}
			if !stopped || !reflect.DeepEqual(spec.Args, []string{"container", "rm", old}) || fixture.objects[old].Config.Healthcheck.Interval == 0 || len(fixture.updates) != 0 {
				t.Fatalf("old timer was disabled before removal: %v", spec.Args)
			}
			delete(fixture.objects, old)
			removed = true
			return nil
		}
		switch spec.Args[5] {
		case "ps":
			fmt.Fprintln(options.Stdout, oneoff, orphan)
			fmt.Fprintln(options.Stdout, reused)
			if removed {
				if _, exists := fixture.objects[fresh]; exists {
					fmt.Fprintln(options.Stdout, fresh)
				}
			} else {
				fmt.Fprintln(options.Stdout, old)
			}
		case "create":
			if !removed || !reflect.DeepEqual(spec.Args[5:], []string{"create", "--pull", "never", "--force-recreate"}) {
				t.Fatalf("Compose recreation flags or cleanup order changed: %v", spec.Args)
			}
			add(fresh, 30_000_000_000, false)
		case "start":
			if !reflect.DeepEqual(fixture.updates, []string{fresh}) || fixture.objects[fresh].Config.Healthcheck.Interval != 0 {
				t.Fatalf("fresh/reused preparation boundary wrong: %v", fixture.updates)
			}
			started = true
		default:
			t.Fatalf("unexpected Compose command: %v", spec.Args)
		}
		return nil
	}
	spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"compose", "--project-name", "owned", "-f", "compose.yaml", "up", "--pull", "never", "-d", "--force-recreate"}}, fixture.endpoint)
	handled, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context()}, raw)
	if !handled || err != nil || !started || !removed {
		t.Fatalf("handled=%v error=%v started=%v removed=%v", handled, err, started, removed)
	}
	for _, id := range []string{oneoff, orphan} {
		object, exists := fixture.objects[id]
		if !exists || !object.State.Running || object.Config.Healthcheck.Interval != 30_000_000_000 {
			t.Fatalf("unrelated project container changed: %s / %#v", id, object)
		}
	}
}

func TestPodmanComposeStopFailurePreservesOldWorkload(t *testing.T) {
	fixture := newPodmanHealthcheckFixture(t)
	id := strings.Repeat("a", 64)
	object := fixturePodmanInspection(id, &dockerHealthcheck{Test: []string{"NONE"}, Interval: 30_000_000_000})
	object.Config.Labels = map[string]string{
		"com.docker.compose.project": "owned", "com.docker.compose.service": composeWorkloadService,
		"com.docker.compose.oneoff": "False",
	}
	object.State.Running, object.State.Status, object.State.Pid = true, "running", 123
	fixture.objects[id] = object
	want := errors.New("graceful stop failed")
	raw := func(spec CommandSpec, options RunOptions) error {
		if reflect.DeepEqual(spec.Args, []string{"container", "stop", id}) {
			return want
		}
		if spec.Args[0] == "compose" && spec.Args[3] == "ps" {
			fmt.Fprintln(options.Stdout, id)
			return nil
		}
		t.Fatalf("removed or recreated workload after failed stop: %v", spec.Args)
		return nil
	}
	spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"compose", "--project-name", "owned", "up", "-d"}}, fixture.endpoint)
	_, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context()}, raw)
	if !errors.Is(err, want) || len(fixture.updates) != 0 {
		t.Fatalf("lost stop failure or changed old timer: %v / %v", err, fixture.updates)
	}
}

func TestPodmanComposeFailureNeverStartsAndCleansOnlyNewIDs(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			fixture := newPodmanHealthcheckFixture(t)
			fixture.reject = true
			id := strings.Repeat("a", 64)
			created, removed := false, false
			raw := func(spec CommandSpec, options RunOptions) error {
				if spec.Args[0] == "container" {
					if foreign || spec.Args[len(spec.Args)-1] != id {
						t.Fatal("cleanup crossed ownership boundary")
					}
					removed = true
					delete(fixture.objects, id)
					return nil
				}
				switch spec.Args[3] {
				case "ps":
					if created {
						fmt.Fprintln(options.Stdout, id)
					}
				case "create":
					created = true
					object := fixturePodmanInspection(id, &dockerHealthcheck{Test: []string{"NONE"}, Interval: 30_000_000_000})
					project := "owned"
					if foreign {
						project = "different"
					}
					object.Config.Labels = map[string]string{
						"com.docker.compose.project": project, "com.docker.compose.service": composeWorkloadService,
						"com.docker.compose.oneoff": "False",
					}
					fixture.objects[id] = object
				case "start":
					t.Fatal("started after failed preparation")
				}
				return nil
			}
			spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"compose", "--project-name", "owned", "up", "-d"}}, fixture.endpoint)
			_, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context()}, raw)
			if err == nil || removed == foreign {
				t.Fatalf("failure/cleanup wrong: %v / %v", err, removed)
			}
		})
	}
}

func fixturePodmanInspection(id string, health *dockerHealthcheck) podmanHealthcheckInspection {
	var object podmanHealthcheckInspection
	object.ID = id
	object.State.Status = "created"
	object.Config.Healthcheck = health
	return object
}

func TestPodmanHealthcheckPrepareNativeProtocol(t *testing.T) {
	for _, test := range []struct {
		name   string
		health *dockerHealthcheck
		update bool
	}{
		{"NONE", &dockerHealthcheck{Test: []string{"NONE"}, Interval: 30_000_000_000}, true},
		{"lowercase NONE", &dockerHealthcheck{Test: []string{"none"}, Interval: 30_000_000_000}, true},
		{"active", &dockerHealthcheck{Test: []string{"CMD", "true"}, Interval: 1_000_000_000}, false},
		{"absent", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPodmanHealthcheckFixture(t)
			id := strings.Repeat("a", 64)
			fixture.objects[id] = fixturePodmanInspection(id, test.health)
			client, err := identifyPodmanHealthcheckClient(t.Context(), fixture.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer client.http.CloseIdleConnections()
			actual, err := client.prepare(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if (len(fixture.updates) == 1) != test.update {
				t.Fatalf("updates = %v", fixture.updates)
			}
			if test.update {
				if !podmanNoneHealthcheck(actual.Config.Healthcheck) || actual.Config.Healthcheck.Interval != 0 {
					t.Fatalf("timer remains enabled: %#v", actual)
				}
			} else if !reflect.DeepEqual(actual.Config.Healthcheck, test.health) {
				t.Fatal("changed an active or absent healthcheck")
			}
		})
	}
}

func TestPodmanHealthcheckPrepareRejectsUnsafeEvidence(t *testing.T) {
	for _, name := range []string{"identity", "running", "startup", "unsupported", "not applied", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			fixture := newPodmanHealthcheckFixture(t)
			id := strings.Repeat("a", 64)
			object := fixturePodmanInspection(id, &dockerHealthcheck{Test: []string{"NONE"}, Interval: 30_000_000_000})
			switch name {
			case "identity":
				fixture.wrongID = true
			case "running":
				object.State.Running, object.State.Pid = true, 123
			case "startup":
				object.Config.StartupHealthCheck = &dockerHealthcheck{Test: []string{"CMD", "true"}}
			case "unsupported":
				fixture.reject = true
			case "not applied":
				fixture.unchanged = true
			case "cancelled":
				fixture.stall = true
			}
			fixture.objects[id] = object
			client, err := identifyPodmanHealthcheckClient(t.Context(), fixture.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer client.http.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if _, err := client.prepare(ctx, id); err == nil {
				t.Fatal("unsafe or unverified preparation was accepted")
			}
		})
	}
}

func TestPodmanCreateAndRunPrepareBeforeStart(t *testing.T) {
	for _, operation := range []string{"create", "run"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newPodmanHealthcheckFixture(t)
			id := strings.Repeat("a", 64)
			var commands [][]string
			var stdout strings.Builder
			raw := func(spec CommandSpec, options RunOptions) error {
				commands = append(commands, spec.Args)
				if host, _ := commandSpecEnvironmentValueV1(spec, "DOCKER_HOST"); host != fixture.endpoint {
					t.Fatalf("endpoint changed: %q", host)
				}
				switch spec.Args[0] {
				case "create":
					object := fixturePodmanInspection(id, &dockerHealthcheck{Test: []string{"NONE"}, Interval: 30_000_000_000})
					object.Config.OpenStdin = true
					fixture.objects[id] = object
					fmt.Fprintln(options.Stdout, id)
				case "start":
					if len(fixture.updates) != 1 || !reflect.DeepEqual(spec.Args, []string{"start", "--attach", "--interactive", id}) {
						t.Fatalf("started without preparation or stdin forwarding: %v", spec.Args)
					}
					fmt.Fprint(options.Stdout, "application output")
				}
				return nil
			}
			spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{operation, "--rm", "--name", "owned", "image", "true"}}, fixture.endpoint)
			handled, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context(), Stdout: &stdout}, raw)
			if !handled || err != nil {
				t.Fatalf("handled=%v error=%v", handled, err)
			}
			if operation == "create" && (len(commands) != 1 || stdout.String() != id+"\n") {
				t.Fatalf("create result changed: %v / %q", commands, stdout.String())
			}
			if operation == "run" && (len(commands) != 2 || stdout.String() != "application output") {
				t.Fatalf("run leaked create ID: %v / %q", commands, stdout.String())
			}
		})
	}
}

func TestPodmanPreparationFailureRemovesOnlyCreatedID(t *testing.T) {
	fixture := newPodmanHealthcheckFixture(t)
	fixture.reject = true
	id := strings.Repeat("a", 64)
	var commands [][]string
	raw := func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec.Args)
		if spec.Args[0] == "create" {
			fixture.objects[id] = fixturePodmanInspection(id, &dockerHealthcheck{Test: []string{"NONE"}, Interval: 30_000_000_000})
			fmt.Fprintln(options.Stdout, id)
		} else if !reflect.DeepEqual(spec.Args, []string{"container", "rm", "--force", id}) {
			t.Fatalf("unexpected operation after rejected preparation: %v", spec.Args)
		}
		return nil
	}
	spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"run", "--rm", "--name", "owned", "image"}}, fixture.endpoint)
	handled, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context()}, raw)
	if !handled || err == nil || len(commands) != 2 {
		t.Fatalf("handled=%v error=%v commands=%v", handled, err, commands)
	}
}

func TestPodmanRunPreservesFailureAndUncancelledCleanup(t *testing.T) {
	fixture := newPodmanHealthcheckFixture(t)
	id := strings.Repeat("a", 64)
	want := errors.New("application exit 7")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cleaned := false
	raw := func(spec CommandSpec, options RunOptions) error {
		switch spec.Args[0] {
		case "create":
			fixture.objects[id] = fixturePodmanInspection(id, &dockerHealthcheck{Test: []string{"NONE"}, Interval: 30_000_000_000})
			fmt.Fprintln(options.Stdout, id)
		case "start":
			cancel()
			return want
		case "container":
			if options.Context.Err() != nil || spec.Args[len(spec.Args)-1] != id {
				t.Fatal("cleanup lost its context or exact ownership")
			}
			cleaned = true
		}
		return nil
	}
	spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"run", "--rm", "--name", "owned", "image"}}, fixture.endpoint)
	_, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: ctx}, raw)
	if !errors.Is(err, want) || !cleaned {
		t.Fatalf("lost application failure or cleanup: %v / %v", err, cleaned)
	}
}

func TestPodmanCompatibilityLeavesDockerCommandsUnchanged(t *testing.T) {
	fixture := newPodmanHealthcheckFixture(t)
	fixture.docker = true
	spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"run", "--rm", "image"}}, fixture.endpoint)
	called := false
	handled, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context()}, func(CommandSpec, RunOptions) error {
		called = true
		return nil
	})
	if handled || err != nil || called {
		t.Fatalf("ordinary Docker was intercepted: %v / %v / %v", handled, err, called)
	}
}

func TestPodmanCreateKeepsConcurrentWarningsAndIDSeparate(t *testing.T) {
	fixture := newPodmanHealthcheckFixture(t)
	id := strings.Repeat("a", 64)
	var output strings.Builder
	raw := func(spec CommandSpec, options RunOptions) error {
		fixture.objects[id] = fixturePodmanInspection(id, &dockerHealthcheck{Test: []string{"NONE"}, Interval: 30_000_000_000})
		var writers sync.WaitGroup
		writers.Add(2)
		go func() { defer writers.Done(); fmt.Fprintln(options.Stdout, id) }()
		go func() { defer writers.Done(); fmt.Fprint(options.Stderr, "create warning\n") }()
		writers.Wait()
		return nil
	}
	spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"create", "--name", "owned", "image"}}, fixture.endpoint)
	_, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context(), Stdout: &output, Stderr: &output}, raw)
	if err != nil || output.String() != "create warning\n"+id+"\n" {
		t.Fatalf("metadata/warning streams corrupted: %v / %q", err, output.String())
	}
}

func TestPodmanCreatePreservesQuietErrorAndPartialCreateCleanup(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			fixture := newPodmanHealthcheckFixture(t)
			id := strings.Repeat("a", 64)
			want := errors.New("create failed")
			cleaned := false
			raw := func(spec CommandSpec, options RunOptions) error {
				if spec.Args[0] == "container" {
					cleaned = spec.Args[len(spec.Args)-1] == id
					return nil
				}
				fmt.Fprint(options.Stderr, "useful create diagnostic")
				if partial {
					fmt.Fprintln(options.Stdout, id)
				}
				return want
			}
			spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"create", "--name", "owned", "image"}}, fixture.endpoint)
			_, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context()}, raw)
			if !errors.Is(err, want) || !strings.Contains(err.Error(), "useful create diagnostic") || cleaned != partial {
				t.Fatalf("lost diagnostic/failure/ownership: %v / %v", err, cleaned)
			}
		})
	}
}

func TestPodmanOutputRunnerPreservesCreateDiagnosticAndExitCode(t *testing.T) {
	fixture := newPodmanHealthcheckFixture(t)
	const diagnostic = "engine rejected create: permission denied"
	dir := t.TempDir()
	writeFakeCommand(
		t,
		dir,
		"docker",
		"#!/bin/sh\nprintf '"+diagnostic+"\\n' >&2\nexit 125\n",
		"@echo off\necho "+diagnostic+" 1>&2\nexit /b 125\r\n",
	)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := executeDockerOutputAtEndpoint(t.Context(), fixture.endpoint, "container", "create", "--name", "owned", "image")
	if err == nil || !strings.Contains(err.Error(), diagnostic) {
		t.Fatalf("output runner error = %v, want engine diagnostic", err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 125 {
		t.Fatalf("output runner error = %v, want *exec.ExitError exit 125", err)
	}
}

func TestPodmanComposeQuietListPreservesDiagnosticAndCapture(t *testing.T) {
	fixture := newPodmanHealthcheckFixture(t)
	const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const diagnostic = "compose ps failed: engine unavailable"
	spec := pinDockerEndpointV1(CommandSpec{
		Name: "docker",
		Args: []string{"compose", "--project-name", "owned", "up", "-d"},
	}, fixture.endpoint)

	run := func(capture *CommandOutputCapture) error {
		raw := func(command CommandSpec, options RunOptions) error {
			if options.Stdout == nil || options.Stderr == nil {
				t.Fatalf("Compose list streams = %#v", options)
			}
			fmt.Fprintln(options.Stdout, id)
			fmt.Fprintln(options.Stderr, diagnostic)
			return exec.Command("sh", "-c", "exit 42").Run()
		}
		_, err := runPodmanHealthcheckCompatibility(spec, RunOptions{Context: t.Context(), Capture: capture}, raw)
		return err
	}

	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), diagnostic) || strings.Contains(err.Error(), id) {
		t.Fatalf("quiet Compose error = %v, want stderr diagnostic without parsed stdout ID", err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 42 {
		t.Fatalf("quiet Compose error = %v, want *exec.ExitError exit 42", err)
	}

	capture := NewCommandOutputCapture()
	err = run(capture)
	if err == nil || !strings.Contains(CommandOutputCaptureDiagnostic(err, capture), diagnostic) {
		t.Fatalf("quiet Compose capture diagnostic = %q, error = %v", CommandOutputCaptureDiagnostic(err, capture), err)
	}
	if !errors.As(err, &exit) || exit.ExitCode() != 42 {
		t.Fatalf("captured quiet Compose error = %v, want *exec.ExitError exit 42", err)
	}
}
