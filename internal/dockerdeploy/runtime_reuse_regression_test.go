package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

func TestCachedBasePreservesLockedManifestAlias(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	configID := "sha256:" + strings.Repeat("4", 64)
	first := "sha256:" + strings.Repeat("1", 64)
	locked := "sha256:" + strings.Repeat("9", 64)
	layer := "sha256:" + strings.Repeat("3", 64)
	inspection := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q,%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`, configID, "python@"+first, "python@"+locked, layer)
	expected, _, err := parseResolvedDockerBase("python:3.11-slim", platform, []byte(inspection), rendererDigest("9"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, data string
		match      bool
	}{
		{"same bytes", inspection, true},
		{"locked alias missing", strings.ReplaceAll(inspection, locked, "sha256:"+strings.Repeat("8", 64)), false},
		{"config changed", strings.ReplaceAll(inspection, configID, "sha256:"+strings.Repeat("5", 64)), false},
		{"rootfs changed", strings.ReplaceAll(inspection, layer, "sha256:"+strings.Repeat("6", 64)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			run := func(_ context.Context, args ...string) (string, error) {
				calls++
				if strings.Join(args, " ") != "image inspect python:3.11-slim" {
					t.Fatalf("unexpected registry access: %v", args)
				}
				return test.data, nil
			}
			got, _, found, err := inspectCachedBase(t.Context(), "python:3.11-slim", platform, run, &expected)
			if err != nil || !found || calls != 1 {
				t.Fatalf("found=%v calls=%d err=%v", found, calls, err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(expected)
			if bytes.Equal(gotJSON, wantJSON) != test.match {
				t.Fatalf("locked identity retained=%v want=%v", bytes.Equal(gotJSON, wantJSON), test.match)
			}
		})
	}
}

func TestAutomaticRuntimeBuildOutputAndFailureDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	dir := runtimeStateEnvelope(t, "state-v1")
	oldBuild, oldWorkload := runRuntimeProviderBuild, runCurrentWorkload
	t.Cleanup(func() { runRuntimeProviderBuild = oldBuild; runCurrentWorkload = oldWorkload })
	for _, action := range []string{"up", "restart"} {
		for _, verbose := range []bool{false, true} {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/verbose=%v/failure=%v", action, verbose, fail), func(t *testing.T) {
					var out, errOut, progress bytes.Buffer
					started := false
					runCurrentWorkload = func(context.Context, CurrentWorkloadRunInputV1) error { started = true; return nil }
					runRuntimeProviderBuild = func(_ context.Context, input ProviderBuildRunInputV1) (LockedProviderBuildExecutionResultV1, error) {
						fmt.Fprintln(input.Progress, "building image layers")
						command := "printf 'raw BuildKit transcript\\n'; printf 'pip installation detail\\n' >&2"
						if fail {
							command += "; exit 1"
						}
						return LockedProviderBuildExecutionResultV1{}, runCommand(CommandSpec{Name: "sh", Args: []string{"-c", command}}, input.RunOptions)
					}
					err := Runtime(RuntimeOptions{Dir: dir, Action: action, Verbose: verbose, Stdout: &out, Stderr: &errOut, Progress: &progress})
					if (err != nil) != fail || started == fail {
						t.Fatalf("err=%v started=%v", err, started)
					}
					if verbose {
						if !strings.Contains(out.String(), "BuildKit") || !strings.Contains(errOut.String(), "pip") {
							t.Fatal("verbose transcript missing")
						}
					} else {
						if out.Len() != 0 || errOut.Len() != 0 {
							t.Fatalf("quiet transcript leaked: %q %q", out.String(), errOut.String())
						}
						if fail && (!strings.Contains(err.Error(), "BuildKit") || !strings.Contains(err.Error(), "pip")) {
							t.Fatalf("failure diagnostics lost: %v", err)
						}
					}
					if !strings.Contains(progress.String(), "building image layers") {
						t.Fatal("build phase missing")
					}
				})
			}
		}
	}
}

func TestCurrentBuildMatchesRoundTrippedLocalOverrideExclusions(t *testing.T) {
	current, input := currentBuildReuseFixture(t)
	current.Lock.PackageOverrides.Choices = []deploy.PackageOverrideIntentChoiceV1{{Provider: "python", Package: "demo", Kind: "local", Exclude: []string{}}}
	input.PackageOverrides = current.Lock.PackageOverrides
	input.PackageOverrides.Choices = append([]deploy.PackageOverrideIntentChoiceV1{}, current.Lock.PackageOverrides.Choices...)
	content, err := deploy.EncodeBuildLockV1(current.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	current.Lock, err = deploy.DecodeBuildLockV1(content, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	if current.Lock.PackageOverrides.Choices[0].Exclude != nil {
		t.Fatal("empty optional exclusions did not disappear on disk")
	}
	refreshCurrentBuildReuseGeneration(t, &current)
	matched, err := CurrentBuildMatches(current, input)
	if err != nil || !matched {
		t.Fatalf("unchanged exclusions: match=%v err=%v", matched, err)
	}
	input.PackageOverrides.Choices[0].Exclude = []string{"build"}
	var progress bytes.Buffer
	input.Progress = &progress
	matched, err = CurrentBuildMatches(current, input)
	if err != nil || matched || !strings.Contains(progress.String(), "package overrides changed") {
		t.Fatalf("changed exclusions: match=%v err=%v progress=%q", matched, err, progress.String())
	}
}
