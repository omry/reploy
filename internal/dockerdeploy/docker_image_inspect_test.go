package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func TestParseDockerImageInspectionNormalizesCompleteBaseRecord(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("1", 64)
	manifestID := "sha256:" + strings.Repeat("2", 64)
	diffID := "sha256:" + strings.Repeat("3", 64)
	data := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{"Env":["PATH=/first","HOME=/root","PATH=/last"],"User":"1000:1000","WorkingDir":"/work","Entrypoint":["/entry"],"Cmd":["arg"],"Healthcheck":{"Test":["CMD","true"],"Interval":100,"Timeout":20,"StartPeriod":30,"StartInterval":5,"Retries":3},"StopSignal":"SIGTERM","OnBuild":[],"Volumes":{}}}]`, configID, "debian@"+manifestID, diffID)
	descriptor, config, err := parseDockerImageInspection("debian:13", platform, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ImmutableReference != "debian@"+manifestID || string(descriptor.ManifestDigest) != manifestID || string(descriptor.ConfigDigest) != configID {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	wantEnvironment := []string{"HOME=/root", "PATH=/last"}
	gotEnvironment := make([]string, 0, len(config.Environment))
	for _, variable := range config.Environment {
		gotEnvironment = append(gotEnvironment, variable.Name+"="+variable.Value)
	}
	if !reflect.DeepEqual(gotEnvironment, wantEnvironment) {
		t.Fatalf("environment = %v, want %v", gotEnvironment, wantEnvironment)
	}
	wantHealthcheck := `{"interval":"100","retries":"3","start_interval":"5","start_period":"30","test":["CMD","true"],"timeout":"20"}`
	if config.Healthcheck != wantHealthcheck {
		t.Fatalf("healthcheck = %s, want %s", config.Healthcheck, wantHealthcheck)
	}
}

func TestParseDockerImageInspectionUsesLocalImageIdentity(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/arm/v7")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("4", 64)
	diffID := "sha256:" + strings.Repeat("5", 64)
	data := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"arm","Variant":"v7","RootFS":{"Layers":[%q]},"Config":{}}]`, configID, diffID)
	descriptor, _, err := parseDockerImageInspection(configID, platform, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ImmutableReference != configID || descriptor.ManifestDigest != "" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestResolveBaseInspectsLocalImageIDWithoutPulling(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("4", 64)
	diffID := "sha256:" + strings.Repeat("5", 64)
	inspection := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`, configID, diffID)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		return inspection, nil
	}
	descriptor, _, err := resolveBase(context.Background(), configID, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, [][]string{{"image", "inspect", configID}}) {
		t.Fatalf("calls = %#v", calls)
	}
	if descriptor.ImmutableReference != configID || descriptor.ManifestDigest != "" || descriptor.ConfigDigest != canonical.Digest(configID) {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestInspectCachedBaseUsesMutableLocalReferenceWithoutPulling(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("4", 64)
	manifestID := "sha256:" + strings.Repeat("5", 64)
	diffID := "sha256:" + strings.Repeat("6", 64)
	inspection := fmt.Sprintf(
		`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`,
		configID,
		"python@"+manifestID,
		diffID,
	)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		return inspection, nil
	}
	descriptor, _, found, err := inspectCachedBase(
		t.Context(),
		"python:3.11-slim",
		platform,
		run,
	)
	if err != nil || !found {
		t.Fatalf("cached base = %#v, found=%v, error=%v", descriptor, found, err)
	}
	if !reflect.DeepEqual(calls, [][]string{{"image", "inspect", "python:3.11-slim"}}) {
		t.Fatalf("calls = %#v", calls)
	}
	if descriptor.ManifestDigest != canonical.Digest(manifestID) {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestInspectCachedBaseTreatsMissingMutableReferenceAsCacheMiss(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	_, _, found, err := inspectCachedBase(
		t.Context(),
		"python:3.11-slim",
		platform,
		func(context.Context, ...string) (string, error) {
			return "", fmt.Errorf("not found")
		},
	)
	if err != nil || found {
		t.Fatalf("found/error = %v/%v", found, err)
	}
}

func TestResolveBaseInspectsCachedDigestReferenceWithoutPulling(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("4", 64)
	manifestID := "sha256:" + strings.Repeat("5", 64)
	diffID := "sha256:" + strings.Repeat("6", 64)
	reference := "debian@" + manifestID
	inspection := fmt.Sprintf(
		`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`,
		configID,
		reference,
		diffID,
	)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		return inspection, nil
	}
	descriptor, _, err := resolveBase(context.Background(), reference, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, [][]string{{"image", "inspect", reference}}) {
		t.Fatalf("calls = %#v", calls)
	}
	if descriptor.ImmutableReference != reference ||
		descriptor.ManifestDigest != canonical.Digest(manifestID) ||
		descriptor.ConfigDigest != canonical.Digest(configID) {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestResolveBasePullsDigestReferenceWhenItIsNotCached(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("4", 64)
	manifestID := "sha256:" + strings.Repeat("5", 64)
	diffID := "sha256:" + strings.Repeat("6", 64)
	reference := "debian@" + manifestID
	inspection := fmt.Sprintf(
		`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`,
		configID,
		reference,
		diffID,
	)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if len(calls) == 1 {
			return "", fmt.Errorf("image is not cached")
		}
		if args[0] == "pull" {
			return "Digest: " + manifestID + "\n", nil
		}
		return inspection, nil
	}
	descriptor, _, err := resolveBase(context.Background(), reference, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "inspect", reference},
		{"pull", "--platform", "linux/amd64", reference},
		{"image", "inspect", reference},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	if descriptor.ImmutableReference != reference || descriptor.ManifestDigest != canonical.Digest(manifestID) {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestResolveBasePullsDigestReferenceWhenCachedPlatformDoesNotMatch(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("4", 64)
	manifestID := "sha256:" + strings.Repeat("5", 64)
	diffID := "sha256:" + strings.Repeat("6", 64)
	reference := "debian@" + manifestID
	inspection := func(architecture string) string {
		return fmt.Sprintf(
			`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":%q,"RootFS":{"Layers":[%q]},"Config":{}}]`,
			configID,
			reference,
			architecture,
			diffID,
		)
	}
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch len(calls) {
		case 1:
			return inspection("arm64"), nil
		case 2:
			return "Digest: " + manifestID + "\n", nil
		default:
			return inspection("amd64"), nil
		}
	}
	if _, _, err := resolveBase(context.Background(), reference, platform, run); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "inspect", reference},
		{"pull", "--platform", "linux/amd64", reference},
		{"image", "inspect", reference},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestParseDockerImageInspectionAcceptsConcreteVariantForVariantlessSelection(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("4", 64)
	manifestID := "sha256:" + strings.Repeat("5", 64)
	diffID := "sha256:" + strings.Repeat("6", 64)
	data := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"arm64","Variant":"v8","RootFS":{"Layers":[%q]},"Config":{}}]`, configID, "debian@"+manifestID, diffID)
	descriptor, _, err := parseDockerImageInspection("debian:13", platform, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Platform != platform {
		t.Fatalf("descriptor platform = %#v, want %#v", descriptor.Platform, platform)
	}
	explicit, err := blueprint.ParsePlatform("linux/arm64/v9")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseDockerImageInspection("debian:13", explicit, []byte(data)); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("explicit variant mismatch error = %v", err)
	}
}

func TestParseDockerImageInspectionNormalizesDockerHubOfficialRepository(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("c", 64)
	manifestID := "sha256:" + strings.Repeat("d", 64)
	diffID := "sha256:" + strings.Repeat("e", 64)
	data := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`, configID, "debian@"+manifestID, diffID)
	descriptor, _, err := parseDockerImageInspection("docker.io/library/debian:13", platform, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ImmutableReference != "debian@"+manifestID {
		t.Fatalf("immutable reference = %q", descriptor.ImmutableReference)
	}
}

func TestResolveBaseUsesExplicitPlatformAndRejectsUnsafeBaseConfig(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("6", 64)
	manifestID := "sha256:" + strings.Repeat("7", 64)
	diffID := "sha256:" + strings.Repeat("8", 64)
	inspection := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{"OnBuild":["RUN false"]}}]`, configID, "ubuntu@"+manifestID, diffID)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if args[0] == "pull" {
			return "Digest: " + manifestID + "\n", nil
		}
		return inspection, nil
	}
	_, _, err = resolveBase(context.Background(), "ubuntu:24.04", platform, run)
	if err == nil || !strings.Contains(err.Error(), "OnBuild") {
		t.Fatalf("error = %v", err)
	}
	wantPull := []string{"pull", "--platform", "linux/amd64", "ubuntu:24.04"}
	if len(calls) != 2 || !reflect.DeepEqual(calls[0], wantPull) || !reflect.DeepEqual(calls[1], []string{"image", "inspect", "ubuntu:24.04"}) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestResolveBaseUsesConfigDigestWhenPlatformPullOmitsRepoDigests(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("6", 64)
	manifestID := "sha256:" + strings.Repeat("7", 64)
	diffID := "sha256:" + strings.Repeat("8", 64)
	inspection := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"arm64","Variant":"v8","RootFS":{"Layers":[%q]},"Config":{}}]`, configID, diffID)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if args[0] == "pull" {
			return "Digest: " + manifestID + "\nStatus: Image is up to date\n", nil
		}
		return inspection, nil
	}
	descriptor, _, err := resolveBase(context.Background(), "debian:12-slim", platform, run)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ImmutableReference != configID || descriptor.ManifestDigest != "" || descriptor.Platform != platform {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if len(calls) != 2 || !reflect.DeepEqual(calls[1], []string{"image", "inspect", "debian:12-slim"}) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestResolveBaseRejectsMissingPullDigestBeforeMutableInspection(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		return "Status: Image is up to date\n", nil
	}
	_, _, err = resolveBase(context.Background(), "debian:13", platform, run)
	if err == nil || !strings.Contains(err.Error(), "immutable manifest digest") {
		t.Fatalf("error = %v", err)
	}
	if len(calls) != 1 || calls[0][0] != "pull" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestDockerPullManifestDigestRejectsMalformedAndConflictingEvidence(t *testing.T) {
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	got, err := dockerPullManifestDigest("Pulling\nDigest: " + digestA + "\nStatus: done\n")
	if err != nil || got != canonical.Digest(digestA) {
		t.Fatalf("digest = %q, error = %v", got, err)
	}
	if _, err := dockerPullManifestDigest("Digest: invalid\n"); err == nil {
		t.Fatal("malformed pull digest accepted")
	}
	if _, err := dockerPullManifestDigest("Digest: " + digestA + "\nDigest: " + digestB + "\n"); err == nil {
		t.Fatal("conflicting pull digests accepted")
	}
}

func TestParseDockerImageInspectionRejectsMismatchAndMissingIdentity(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := "sha256:" + strings.Repeat("9", 64)
	diffID := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "platform", data: fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":"arm64","RootFS":{"Layers":[%q]},"Config":{}}]`, configID, "debian@sha256:"+strings.Repeat("b", 64), diffID), want: "does not match"},
		{name: "repo digest", data: fmt.Sprintf(`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`, configID, diffID), want: "no repo digest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseDockerImageInspection("debian:13", platform, []byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveBaseRejectsUnsafeReferenceBeforeDocker(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	run := func(context.Context, ...string) (string, error) {
		called = true
		return "", nil
	}
	if _, _, err := resolveBase(context.Background(), "https://user:pass@example/image", platform, run); err == nil {
		t.Fatal("unsafe image reference was accepted")
	}
	if called {
		t.Fatal("Docker was invoked for an unsafe image reference")
	}
}

func TestResolveBaseRejectsMalformedAuthorReferencesBeforeDocker(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"--help", "-q", "ubuntu:", "bad//repository", "team/Uppercase"} {
		t.Run(reference, func(t *testing.T) {
			called := false
			run := func(context.Context, ...string) (string, error) {
				called = true
				return "", nil
			}
			if _, _, err := resolveBase(t.Context(), reference, platform, run); err == nil {
				t.Fatalf("resolveBase(%q) error = nil", reference)
			}
			if called {
				t.Fatalf("resolveBase(%q) invoked Docker", reference)
			}
		})
	}
}

func TestResolveBaseDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	descriptor, config, err := ResolveBase(ctx, "debian:bookworm-slim", platform)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(descriptor.ImmutableReference, "debian@sha256:") {
		t.Fatalf("immutable reference = %q", descriptor.ImmutableReference)
	}
	if len(descriptor.RootFSDiffIDs) == 0 || len(config.Environment) == 0 {
		t.Fatalf("incomplete inspection: descriptor=%#v config=%#v", descriptor, config)
	}
	pinnedDescriptor, _, err := ResolveBase(ctx, descriptor.ImmutableReference, platform)
	if err != nil {
		t.Fatal(err)
	}
	if pinnedDescriptor.ImmutableReference != descriptor.ImmutableReference ||
		pinnedDescriptor.ManifestDigest != descriptor.ManifestDigest ||
		pinnedDescriptor.ConfigDigest != descriptor.ConfigDigest {
		t.Fatalf("pinned descriptor = %#v, want identity %#v", pinnedDescriptor, descriptor)
	}
	localDescriptor, _, err := ResolveBase(ctx, string(descriptor.ConfigDigest), platform)
	if err != nil {
		t.Fatal(err)
	}
	if localDescriptor.ImmutableReference != string(descriptor.ConfigDigest) || localDescriptor.ManifestDigest != "" {
		t.Fatalf("local descriptor = %#v", localDescriptor)
	}
}
