package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func TestBaseInspectionSelectsTargetPlatform(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:516b51cb9db2ef061e465c803e50f64361b82cefd91e525b889b967b83e83527"
	reference := "python:3.14-slim-bookworm@" + index
	configID := "sha256:" + strings.Repeat("4", 64)
	layer := "sha256:" + strings.Repeat("6", 64)
	selected := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{"Env":["SELECTED=amd64"]}}]`, configID, "python@"+index, layer)
	for _, mode := range []string{"fresh", "pre-pulled", "cached", "both-variants"} {
		t.Run(mode, func(t *testing.T) {
			var calls [][]string
			pulled := mode != "fresh"
			run := func(_ context.Context, args ...string) (string, error) {
				calls = append(calls, append([]string(nil), args...))
				if args[0] == "pull" {
					pulled = true
					return "Digest: " + index + "\n", nil
				}
				if !pulled {
					return "", fmt.Errorf("No such image")
				}
				if reflect.DeepEqual(args, []string{"image", "inspect", "--platform", "linux/amd64", reference}) {
					return selected, nil
				}
				if mode == "both-variants" {
					return strings.ReplaceAll(strings.ReplaceAll(selected, "amd64", "arm64"), configID, "sha256:"+strings.Repeat("7", 64)), nil
				}
				// Docker/containerd may expose an index without concrete metadata.
				return `[{"Os":"","Architecture":"","Config":null}]`, nil
			}
			if mode == "cached" {
				expected, _, err := parseResolvedDockerBase(reference, platform, []byte(selected), canonical.Digest(index))
				if err != nil {
					t.Fatal(err)
				}
				descriptor, config, found, err := inspectCachedBase(t.Context(), reference, platform, run, &expected)
				if err != nil || !found || descriptor.ConfigDigest != canonical.Digest(configID) || len(config.Environment) != 1 || config.Environment[0].Name != "SELECTED" || config.Environment[0].Value != "amd64" {
					t.Fatalf("cache found=%v descriptor=%#v config=%#v err=%v calls=%v", found, descriptor, config, err, calls)
				}
			} else {
				descriptor, config, err := resolveBase(t.Context(), reference, platform, run)
				if err != nil {
					t.Fatalf("resolve: %v; calls=%v", err, calls)
				}
				if descriptor.ConfigDigest != canonical.Digest(configID) || descriptor.Platform != platform || len(descriptor.RootFSDiffIDs) != 1 || descriptor.RootFSDiffIDs[0] != canonical.Digest(layer) || len(config.Environment) != 1 || config.Environment[0].Name != "SELECTED" || config.Environment[0].Value != "amd64" {
					t.Fatalf("wrong selected image: descriptor=%#v config=%#v", descriptor, config)
				}
			}
			want := [][]string{{"image", "inspect", "--platform", "linux/amd64", reference}}
			if mode == "fresh" {
				want = append(want, []string{"pull", "--platform", "linux/amd64", reference}, []string{"image", "inspect", "--platform", "linux/amd64", reference})
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls=%v want=%v", calls, want)
			}
		})
	}
}

func TestInspectDockerBasePlatformCompatibility(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	for _, test := range []struct {
		name, failure string
		fallback      bool
	}{
		{"old client", "unknown flag: --platform\nUsage: docker image inspect", true},
		{"old API", `"--platform" requires API version 1.49, but the Docker daemon API version is 1.43`, true},
		{"missing target", "image does not match the specified platform: wanted linux/amd64, actual linux/arm64", false},
		{"missing image", "No such image", false},
		{"daemon unavailable", "Cannot connect to the Docker daemon", false},
		{"unrelated flag", "unknown flag: --other", false},
		{"unrelated API error", "requires API version 1.49", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls [][]string
			output, err := inspectDockerBaseForPlatform(t.Context(), "debian:13", platform, func(_ context.Context, args ...string) (string, error) {
				calls = append(calls, append([]string(nil), args...))
				if len(calls) == 1 {
					return "", fmt.Errorf("%s", test.failure)
				}
				return "legacy inspection", nil
			})
			want := [][]string{{"image", "inspect", "--platform", "linux/amd64", "debian:13"}}
			if test.fallback {
				want = append(want, []string{"image", "inspect", "debian:13"})
			}
			if !reflect.DeepEqual(calls, want) || (err == nil) != test.fallback || (test.fallback && output != "legacy inspection") {
				t.Fatalf("output=%q err=%v calls=%v want=%v", output, err, calls, want)
			}
		})
	}
}

func TestBasePlatformInspectionNeverSynthesizesMetadata(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	configID := "sha256:" + strings.Repeat("4", 64)
	index := "sha256:" + strings.Repeat("5", 64)
	reference := "debian@" + index
	for _, legacy := range []bool{false, true} {
		for _, architecture := range []string{"", "arm64", "amd64"} {
			t.Run(fmt.Sprintf("legacy=%v/architecture=%s", legacy, architecture), func(t *testing.T) {
				run := func(_ context.Context, args ...string) (string, error) {
					if args[0] == "pull" {
						return "Digest: " + index, nil
					}
					if legacy && len(args) == 5 {
						return "", fmt.Errorf(`"--platform" requires API version 1.49, but the Docker daemon API version is 1.43`)
					}
					return fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":%q,"RootFS":{"Layers":[%q]},"Config":{}}]`, configID, reference, architecture, configID), nil
				}
				_, _, err := resolveBase(t.Context(), reference, platform, run)
				_, _, found, cachedErr := inspectCachedBase(t.Context(), reference, platform, run, nil)
				if (err == nil) != (architecture == "amd64") || cachedErr != nil || found != (architecture == "amd64") {
					t.Fatalf("resolve=%v cacheFound=%v cacheErr=%v", err, found, cachedErr)
				}
			})
		}
	}
}

func TestBasePlatformInspectionCancellationPreventsFallback(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	_, err := inspectDockerBaseForPlatform(ctx, "debian:13", platform, func(context.Context, ...string) (string, error) {
		calls++
		cancel()
		return "", fmt.Errorf("unknown flag: --platform")
	})
	if err != context.Canceled || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestPinnedTagBaseKeepsLockedRepositoryReference(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:" + strings.Repeat("1", 64)
	child := "sha256:" + strings.Repeat("2", 64)
	output := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`, child, "python@"+index, child)
	descriptor, _, err := parseResolvedDockerBase("python:3.14-slim-bookworm@"+index, platform, []byte(output), canonical.Digest(index))
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ImmutableReference != "python@"+index || descriptor.ManifestDigest != canonical.Digest(index) {
		t.Fatalf("pinned tag discarded its runnable repository identity: %#v", descriptor)
	}
}

func TestResolveBaseRegistersSelectedManifestBeforeReturning(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:" + strings.Repeat("1", 64)
	child := "sha256:" + strings.Repeat("2", 64)
	layer := "sha256:" + strings.Repeat("3", 64)
	reference := "python@" + index
	indexOutput := dockerPlatformInspectionFixture(child, index, layer, "application/vnd.oci.image.manifest.v1+json")
	childOutput := dockerLocalInspectionFixture(child, layer)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		switch {
		case reflect.DeepEqual(args, []string{"image", "inspect", "--platform", platform.Canonical, reference}):
			return indexOutput, nil
		case reflect.DeepEqual(args, []string{"image", "inspect", "--platform", platform.Canonical, child}):
			if countCalls(calls, "pull") == 0 {
				return "", fmt.Errorf("No such image: %s", child)
			}
			return childOutput, nil
		case len(args) == 4 && args[0] == "pull":
			return "Digest: " + child + "\n", nil
		default:
			return "", fmt.Errorf("unexpected Docker call: %#v", args)
		}
	}
	descriptor, config, err := resolveBase(t.Context(), reference, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ConfigDigest != canonical.Digest(child) || descriptor.ImmutableReference != reference || descriptor.Platform != platform {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if len(config.Environment) != 1 || config.Environment[0].Name != "SELECTED" || config.Environment[0].Value != "amd64" {
		t.Fatalf("config = %#v", config)
	}
	want := [][]string{
		{"image", "inspect", "--platform", platform.Canonical, reference},
		{"image", "inspect", "--platform", platform.Canonical, child},
		{"pull", "--platform", platform.Canonical, "python@" + child},
		{"image", "inspect", "--platform", platform.Canonical, child},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestResolveBaseSkipsPullWhenSelectedManifestIsAlreadyRegistered(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:" + strings.Repeat("4", 64)
	child := "sha256:" + strings.Repeat("5", 64)
	layer := "sha256:" + strings.Repeat("6", 64)
	reference := "python@" + index
	indexOutput := dockerPlatformInspectionFixture(child, index, layer, "application/vnd.docker.distribution.manifest.v2+json")
	childOutput := dockerLocalInspectionFixture(child, layer)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if reflect.DeepEqual(args, []string{"image", "inspect", "--platform", platform.Canonical, reference}) {
			return indexOutput, nil
		}
		if reflect.DeepEqual(args, []string{"image", "inspect", "--platform", platform.Canonical, child}) {
			return childOutput, nil
		}
		if args[0] == "pull" {
			t.Fatal("selected child was pulled despite exact local registration")
		}
		return "", fmt.Errorf("unexpected Docker call: %#v", args)
	}
	if _, _, err := resolveBase(t.Context(), reference, platform, run); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestResolveBaseDoesNotRegisterWithoutMatchingImageDescriptor(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:" + strings.Repeat("7", 64)
	child := "sha256:" + strings.Repeat("8", 64)
	layer := "sha256:" + strings.Repeat("9", 64)
	reference := "python@" + index
	for _, test := range []struct {
		name, mediaType, descriptorDigest string
	}{
		{name: "missing", mediaType: "", descriptorDigest: ""},
		{name: "index descriptor", mediaType: "application/vnd.oci.image.index.v1+json", descriptorDigest: child},
		{name: "wrong digest", mediaType: "application/vnd.oci.image.manifest.v1+json", descriptorDigest: index},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := dockerPlatformInspectionFixture(child, index, layer, test.mediaType)
			if test.descriptorDigest == "" {
				output = strings.Replace(output, `,"Descriptor":{"mediaType":"`+test.mediaType+`","digest":"`+child+`"}`, "", 1)
			} else {
				output = strings.Replace(output, `"digest":"`+child+`"`, `"digest":"`+test.descriptorDigest+`"`, 1)
			}
			calls := 0
			run := func(_ context.Context, args ...string) (string, error) {
				calls++
				if args[0] == "pull" {
					t.Fatal("unexpected selected-child pull")
				}
				return output, nil
			}
			if _, _, err := resolveBase(t.Context(), reference, platform, run); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("Docker calls = %d, want one inspection", calls)
			}
		})
	}
}

func TestInspectCachedBaseTreatsUnregisteredSelectedManifestAsCacheMiss(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:" + strings.Repeat("a", 64)
	child := "sha256:" + strings.Repeat("b", 64)
	layer := "sha256:" + strings.Repeat("c", 64)
	reference := "python@" + index
	output := dockerPlatformInspectionFixture(child, index, layer, "application/vnd.oci.image.manifest.v1+json")
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "pull" {
			t.Fatal("cached inspection attempted a pull")
		}
		if len(args) == 5 && args[4] == child {
			return "", fmt.Errorf("No such image: %s", child)
		}
		return output, nil
	}
	_, _, found, err := inspectCachedBase(t.Context(), reference, platform, run, nil)
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestResolveBaseRejectsSelectedRegistrationDrift(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:" + strings.Repeat("d", 64)
	child := "sha256:" + strings.Repeat("e", 64)
	layer := "sha256:" + strings.Repeat("f", 64)
	reference := "python@" + index
	indexOutput := dockerPlatformInspectionFixture(child, index, layer, "application/vnd.oci.image.manifest.v1+json")
	wrongLayer := "sha256:" + strings.Repeat("1", 64)
	wrongOutput := dockerLocalInspectionFixture(child, wrongLayer)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(args) == 5 && args[4] == reference {
			return indexOutput, nil
		}
		if len(args) == 5 && args[4] == child {
			if countCalls(calls, "pull") == 0 {
				return "", fmt.Errorf("No such image: %s", child)
			}
			return wrongOutput, nil
		}
		if args[0] == "pull" {
			return "Digest: " + child + "\n", nil
		}
		return "", fmt.Errorf("unexpected Docker call: %#v", args)
	}
	if _, _, err := resolveBase(t.Context(), reference, platform, run); err == nil || !strings.Contains(err.Error(), "rootfs layers") {
		t.Fatalf("error = %v, want post-pull layer drift", err)
	}
}

func TestResolveBaseRejectsSelectedRegistrationPullFailures(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:" + strings.Repeat("1", 64)
	child := "sha256:" + strings.Repeat("2", 64)
	layer := "sha256:" + strings.Repeat("3", 64)
	reference := "python@" + index
	for _, test := range []struct {
		name, pullOutput, pullError, want string
	}{
		{name: "pull failure", pullError: "registry connection failed", want: "registry connection failed"},
		{name: "digest mismatch", pullOutput: "Digest: sha256:" + strings.Repeat("4", 64) + "\n", want: "reported digest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			indexOutput := dockerPlatformInspectionFixture(child, index, layer, "application/vnd.oci.image.manifest.v1+json")
			run := func(_ context.Context, args ...string) (string, error) {
				if len(args) == 5 && args[4] == reference {
					return indexOutput, nil
				}
				if len(args) == 5 && args[4] == child {
					return "", fmt.Errorf("No such image: %s", child)
				}
				if args[0] == "pull" {
					if test.pullError != "" {
						return test.pullOutput, fmt.Errorf("%s", test.pullError)
					}
					return test.pullOutput, nil
				}
				return "", fmt.Errorf("unexpected Docker call: %#v", args)
			}
			if _, _, err := resolveBase(t.Context(), reference, platform, run); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveBasePreservesSelectedRegistrationDaemonErrors(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	index := "sha256:" + strings.Repeat("5", 64)
	child := "sha256:" + strings.Repeat("6", 64)
	layer := "sha256:" + strings.Repeat("7", 64)
	reference := "python@" + index
	indexOutput := dockerPlatformInspectionFixture(child, index, layer, "application/vnd.oci.image.manifest.v1+json")
	run := func(_ context.Context, args ...string) (string, error) {
		if len(args) == 5 && args[4] == reference {
			return indexOutput, nil
		}
		if len(args) == 5 && args[4] == child {
			return "", fmt.Errorf("Cannot connect to the Docker daemon")
		}
		if args[0] == "pull" {
			t.Fatal("daemon failure was mistaken for an unregistered child")
		}
		return "", fmt.Errorf("unexpected Docker call: %#v", args)
	}
	if _, _, err := resolveBase(t.Context(), reference, platform, run); err == nil || !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Fatalf("error = %v, want daemon failure", err)
	}
}

func dockerPlatformInspectionFixture(child, index, layer, mediaType string) string {
	descriptor := ""
	if mediaType != "" {
		descriptor = fmt.Sprintf(`,"Descriptor":{"mediaType":%q,"digest":%q}`, mediaType, child)
	}
	return fmt.Sprintf(`[{
		"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64",
		"RootFS":{"Layers":[%q]},"Config":{"Env":["SELECTED=amd64"]}%s
	}]`, child, "python@"+index, layer, descriptor)
}

func dockerLocalInspectionFixture(child, layer string) string {
	return fmt.Sprintf(`[{
		"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"amd64",
		"RootFS":{"Layers":[%q]},"Config":{"Env":["SELECTED=amd64"]}
	}]`, child, layer)
}

func countCalls(calls [][]string, command string) int {
	count := 0
	for _, args := range calls {
		if len(args) > 0 && args[0] == command {
			count++
		}
	}
	return count
}
