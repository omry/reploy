package dockerdeploy

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// Workflow packaging, not an attestation or a new public evidence identity.
// Trust remains in the project-controlled jobs and their original store records.
type portableToolBashRunnerV1 struct {
	GOOS, GOARCH, HostArchitecture      string
	Docker                              struct{ Os, Arch, Version string }
	WorkflowSHA, RunID, RunAttempt, Job string
	RunnerName, RunnerOS, RunnerArch    string
	Cases                               map[canonical.Digest]providerstore.StoreObjectRef
}

func portableToolNativeArchitectureV1(machine string) string {
	switch strings.TrimSpace(machine) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

func requirePortableToolBashRunnerV1(r portableToolBashRunnerV1, arch, sha, run, attempt string) error {
	if r.GOOS != "linux" || r.GOARCH != arch || portableToolNativeArchitectureV1(r.HostArchitecture) != arch ||
		r.Docker.Os != "linux" || r.Docker.Arch != arch || r.Docker.Version == "" {
		return fmt.Errorf("Bash %s proof requires matching native host, process and Docker daemon", arch)
	}
	if sha != "" || run != "" || attempt != "" {
		job, runnerArch := "ci", "X64"
		if arch == "arm64" {
			job, runnerArch = "target-smoke", "ARM64"
		}
		if sha == "" || run == "" || attempt == "" || r.WorkflowSHA != sha || r.RunID != run || r.RunAttempt != attempt ||
			r.Job != job || r.RunnerName == "" || r.RunnerOS != "Linux" || r.RunnerArch != runnerArch {
			return fmt.Errorf("Bash %s runner context differs from the current native workflow job", arch)
		}
	}
	return nil
}

func requirePortableToolBashProducerAttemptV1(producer, consumer string) error {
	produced, producerErr := strconv.Atoi(producer)
	consumed, consumerErr := strconv.Atoi(consumer)
	if producerErr != nil || consumerErr != nil || produced < 1 || consumed < 1 ||
		strconv.Itoa(produced) != producer || strconv.Itoa(consumed) != consumer || produced > consumed {
		return fmt.Errorf("Bash artifact requires a valid producer attempt no later than the consumer attempt")
	}
	return nil
}

func observePortableToolBashRunnerV1(t *testing.T, arch string) portableToolBashRunnerV1 {
	t.Helper()
	r := portableToolBashRunnerV1{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		WorkflowSHA: os.Getenv("GITHUB_SHA"), RunID: os.Getenv("GITHUB_RUN_ID"), RunAttempt: os.Getenv("GITHUB_RUN_ATTEMPT"),
		Job: os.Getenv("GITHUB_JOB"), RunnerName: os.Getenv("RUNNER_NAME"), RunnerOS: os.Getenv("RUNNER_OS"), RunnerArch: os.Getenv("RUNNER_ARCH")}
	host, err := exec.CommandContext(t.Context(), "uname", "-m").Output()
	if err != nil {
		t.Fatal(err)
	}
	r.HostArchitecture = strings.TrimSpace(string(host))
	server, err := runDockerOutput(t.Context(), "version", "--format", "{{json .Server}}")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(server), &r.Docker); err != nil {
		t.Fatal(err)
	}
	if err := requirePortableToolBashRunnerV1(r, arch, r.WorkflowSHA, r.RunID, r.RunAttempt); err != nil {
		t.Fatal(err)
	}
	return r
}

func runPortableToolBashNativeMatrixV1(t *testing.T, arch string) {
	t.Helper()
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to exercise the native Bash matrix")
	}
	before := observePortableToolBashRunnerV1(t, arch)
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := portableToolIntegrationCasesForPlatformV1(cases, "bash", "linux/"+arch)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 6 {
		t.Fatal("native Bash proof requires exactly six current cases")
	}
	runPortableToolIntegrationCasesV1(t, selected)
	after := observePortableToolBashRunnerV1(t, arch)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("native runner context changed during case execution")
	}
	// The generic runner has already required every case and completed cleanup.
	// Publish context only beside a successfully retained original record index.
	if root := os.Getenv("REPLOY_PORTABLE_TOOL_EVIDENCE_DIR"); root != "" {
		name := strings.ReplaceAll(t.Name(), "/", "-")
		index, err := os.ReadFile(filepath.Join(root, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(index, &after.Cases); err != nil {
			t.Fatal(err)
		}
		payload, err := json.MarshalIndent(after, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name+"-runner.json"), append(payload, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := requirePortableToolBashRetainedArchitectureV1(root, arch, after.WorkflowSHA, after.RunID, after.RunAttempt, selected); err != nil {
			t.Fatal(err)
		}
		testPortableToolBashRetainedNegativesV1(t, root, arch, after.WorkflowSHA, after.RunID, after.RunAttempt, selected)
	}
}

func requirePortableToolBashRetainedArchitectureV1(root, arch, sha, run, attempt string, cases []toolcatalog.IntegrationCaseV1) error {
	name := "TestPortableToolBash" + strings.ToUpper(arch) + "MatrixDockerIntegration"
	var runner portableToolBashRunnerV1
	var refs map[canonical.Digest]providerstore.StoreObjectRef
	var native map[canonical.Digest]portableToolIntegrationNativeEvidenceV1
	for suffix, destination := range map[string]any{"-runner.json": &runner, ".json": &refs, "-native.json": &native} {
		payload, err := os.ReadFile(filepath.Join(root, name+suffix))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payload, destination); err != nil {
			return err
		}
	}
	if err := requirePortableToolBashRunnerV1(runner, arch, sha, run, attempt); err != nil {
		return err
	}
	if !reflect.DeepEqual(runner.Cases, refs) {
		return fmt.Errorf("runner context is not bound to the original record index")
	}
	selected, err := portableToolIntegrationCasesForPlatformV1(cases, "bash", "linux/"+arch)
	if err != nil {
		return err
	}
	requests, err := portableToolIntegrationRequestsV1(selected)
	if err != nil {
		return err
	}
	store, err := providerstore.NewStore(root)
	if err != nil {
		return err
	}
	if len(requests) != 6 || len(native) != 3 {
		return fmt.Errorf("retained Bash evidence lacks six exact cases and three runtime handoffs")
	}
	if err := RequireCurrentPortableToolCaseEvidenceV1(store, requests, refs); err != nil {
		return err
	}
	for _, request := range requests {
		payload, err := store.LoadValidationRecord(refs[request.Case.ID])
		if err != nil {
			return err
		}
		observation := PortableToolCaseObservationV1{payload: payload}
		if err := requirePortableToolIntegrationBashOutputV1(request.Case, observation); err != nil {
			return err
		}
		handoff, present := native[request.Case.ID]
		if request.Case.Support.Context == "runtime" {
			if !present {
				return fmt.Errorf("runtime Bash case lacks retained native handoff")
			}
			if err := requirePortableToolIntegrationNativeEvidenceV1(request.Case, request.Scope, observation, &handoff); err != nil {
				return err
			}
		} else if present {
			return fmt.Errorf("build-only case substituted for runtime handoff")
		}
	}
	return nil
}

func TestPortableToolBashCompleteRetainedEvidenceIntegration(t *testing.T) {
	amd, arm := os.Getenv("REPLOY_BASH_AMD64_EVIDENCE_DIR"), os.Getenv("REPLOY_BASH_ARM64_EVIDENCE_DIR")
	if amd == "" && arm == "" {
		t.Skip("set both native Bash artifact directories to require all twelve original records")
	}
	sha, run, attempt := os.Getenv("GITHUB_SHA"), os.Getenv("GITHUB_RUN_ID"), os.Getenv("GITHUB_RUN_ATTEMPT")
	if amd == "" || arm == "" || sha == "" || run == "" || attempt == "" {
		t.Fatal("complete Bash gate requires both actual native artifacts and current workflow identity")
	}
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	all, err := portableToolIntegrationCasesForToolV1(cases, "bash")
	if err != nil || len(all) != 12 {
		t.Fatalf("complete Bash gate must cover all twelve current cases: %v", err)
	}
	for arch, root := range map[string]string{"amd64": amd, "arm64": arm} {
		// A partial workflow rerun retains successful producers from earlier
		// attempts. Bind each artifact to its own observed producer attempt;
		// the exact workflow SHA, run and native job checks still apply below.
		name := "TestPortableToolBash" + strings.ToUpper(arch) + "MatrixDockerIntegration"
		payload, err := os.ReadFile(filepath.Join(root, name+"-runner.json"))
		if err != nil {
			t.Fatal(err)
		}
		var runner portableToolBashRunnerV1
		if err := json.Unmarshal(payload, &runner); err != nil {
			t.Fatal(err)
		}
		if err := requirePortableToolBashProducerAttemptV1(runner.RunAttempt, attempt); err != nil {
			t.Fatalf("%s original producer attempt: %v", arch, err)
		}
		if err := requirePortableToolBashRetainedArchitectureV1(root, arch, sha, run, runner.RunAttempt, all); err != nil {
			t.Fatalf("%s original evidence: %v", arch, err)
		}
		testPortableToolBashRetainedNegativesV1(t, root, arch, sha, run, runner.RunAttempt, all)
	}
}

func testPortableToolBashRetainedNegativesV1(t *testing.T, root, arch, sha, run, attempt string, all []toolcatalog.IntegrationCaseV1) {
	t.Helper()
	// Corrupt actual retained evidence; never manufacture successful records.
	for _, fault := range []string{"missing", "runner", "binding", "stale", "missing-case", "substituted", "handoff", "consumer"} {
		t.Run(arch+"/reject-"+fault, func(t *testing.T) {
			candidate := t.TempDir()
			name := "TestPortableToolBash" + strings.ToUpper(arch) + "MatrixDockerIntegration"
			for _, suffix := range []string{".json", "-runner.json", "-native.json"} {
				data, err := os.ReadFile(filepath.Join(root, name+suffix))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(candidate, name+suffix), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Copy original store bytes; a symlink would itself fail store validation.
			if err := os.CopyFS(filepath.Join(candidate, ".reploy"), os.DirFS(filepath.Join(root, ".reploy"))); err != nil {
				t.Fatal(err)
			}
			if err := requirePortableToolBashRetainedArchitectureV1(candidate, arch, sha, run, attempt, all); err != nil {
				t.Fatalf("untampered original copy must pass before this negative: %v", err)
			}
			changed := append([]toolcatalog.IntegrationCaseV1(nil), all...)
			switch fault {
			case "missing":
				if err := os.Remove(filepath.Join(candidate, name+".json")); err != nil {
					t.Fatal(err)
				}
			case "runner", "binding":
				var r portableToolBashRunnerV1
				data, err := os.ReadFile(filepath.Join(candidate, name+"-runner.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &r); err != nil {
					t.Fatal(err)
				}
				if fault == "runner" {
					r.GOARCH = "other-architecture"
				} else {
					r.Cases = nil
				}
				data, err = json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(candidate, name+"-runner.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "stale":
				for i := range changed {
					if changed[i].Fixture.Target.Platform == "linux/"+arch {
						changed[i].Fixture.BaseImageDigest = rendererDigest("f")
						break
					}
				}
			case "missing-case", "substituted":
				var refs map[canonical.Digest]providerstore.StoreObjectRef
				data, err := os.ReadFile(filepath.Join(candidate, name+".json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &refs); err != nil {
					t.Fatal(err)
				}
				var first canonical.Digest
				for id := range refs {
					if first == "" {
						first = id
					} else {
						if fault == "substituted" {
							refs[id] = refs[first]
						}
						break
					}
				}
				if fault == "missing-case" {
					delete(refs, first)
				}
				data, err = json.Marshal(refs)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(candidate, name+".json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
				// Preserve the binding so the case matcher must reject the wrong original record.
				var r portableToolBashRunnerV1
				data, err = os.ReadFile(filepath.Join(candidate, name+"-runner.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &r); err != nil {
					t.Fatal(err)
				}
				r.Cases = refs
				data, err = json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(candidate, name+"-runner.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "handoff":
				if err := os.WriteFile(filepath.Join(candidate, name+"-native.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "consumer":
				var handoffs map[canonical.Digest]portableToolIntegrationNativeEvidenceV1
				data, err := os.ReadFile(filepath.Join(candidate, name+"-native.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &handoffs); err != nil {
					t.Fatal(err)
				}
				for id, handoff := range handoffs {
					handoff.Consumers[0].InvocationPath = "/wrong/bash"
					handoffs[id] = handoff
					break
				}
				data, err = json.Marshal(handoffs)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(candidate, name+"-native.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := requirePortableToolBashRetainedArchitectureV1(candidate, arch, sha, run, attempt, changed); err == nil {
				t.Fatalf("%s substituted evidence passed", fault)
			}
		})
	}
}
