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
