package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probearchive"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestInspectControlledSessionControllerSourceAcceptsLocalGenerationTagV1(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	configID := canonical.Digest("sha256:" + strings.Repeat("4", 64))
	diffID := canonical.Digest("sha256:" + strings.Repeat("5", 64))
	rootFS, err := deploy.RootFSSubject([]canonical.Digest{diffID})
	if err != nil {
		t.Fatal(err)
	}
	current := CurrentBuild{
		Generation: deploy.EnvironmentGenerationState{Reference: "reploy/env/controller:g-current"},
		Lock: deploy.BuildLockV1{
			Platform: platform,
			FinalImage: providers.RealizedImageV1{
				Digest: configID, ConfigDigest: configID, RootFSSubject: rootFS,
			},
		},
	}
	inspection := fmt.Sprintf(
		`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`,
		configID,
		diffID,
	)
	var calls [][]string
	source, err := inspectControlledSessionControllerSourceV1(t.Context(), current, func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		return inspection, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "image inspect "+current.Generation.Reference {
		t.Fatalf("Docker inspection calls = %#v", calls)
	}
	if source.Descriptor.AuthorReference != string(configID) || source.Descriptor.ImmutableReference != string(configID) || source.Image != current.Lock.FinalImage {
		t.Fatalf("controller source = %#v", source)
	}
}

func TestControlledSessionControllerPackageDockerfileAddsOnlyControllerTool(t *testing.T) {
	source := applicationRuntimeLayerTestRequest(t).Source
	extracted := probearchive.ExtractedSessionClient{
		Platform: source.Descriptor.Platform.Canonical,
		Path:     "/tmp/reploy-session-client",
		Size:     "123",
		SHA256:   rendererDigest("a"),
		Release:  currentControllerReleaseV1(),
	}
	content, expectedConfig, labels, err := controlledSessionControllerPackageDockerfileV1(source, extracted)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(content)
	for _, want := range []string{
		"FROM ${REPLOY_BASE_IMAGE}",
		"COPY --chmod=0555 reploy-session-client /usr/local/bin/reploy-session-client",
		"ENV PATH=",
		controlledSessionControllerPackageLabelV1,
		controlledSessionControllerArtifactLabelV1,
		controlledSessionControllerVersionLabelV1,
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, dockerfile)
		}
	}
	for _, forbidden := range []string{"RUN ", "ENTRYPOINT ", "CMD ", "REPLOY_SESSION_SOCKET"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Fatalf("Dockerfile contains %q:\n%s", forbidden, dockerfile)
		}
	}
	pathIndex := pathEnvironmentIndexV1(expectedConfig.Environment)
	if pathIndex < 0 || !strings.HasPrefix(expectedConfig.Environment[pathIndex].Value, controlledSessionControllerPathPrefixV1+":") {
		t.Fatalf("packaged PATH = %#v", expectedConfig.Environment)
	}
	if labels[controlledSessionControllerArtifactLabelV1] != string(extracted.SHA256) {
		t.Fatalf("artifact label = %#v", labels)
	}
}

func TestValidateControlledSessionControllerPackageImageRequiresOneExactLayer(t *testing.T) {
	source := applicationRuntimeLayerTestRequest(t).Source
	extracted := probearchive.ExtractedSessionClient{
		Platform: source.Descriptor.Platform.Canonical, Path: "/tmp/reploy-session-client", Size: "123",
		SHA256: rendererDigest("a"), Release: currentControllerReleaseV1(),
	}
	_, expectedConfig, labels, err := controlledSessionControllerPackageDockerfileV1(source, extracted)
	if err != nil {
		t.Fatal(err)
	}
	candidate := source
	candidate.Config = expectedConfig
	candidate.Labels = labels
	candidate.Descriptor.RootFSDiffIDs = append(append([]canonical.Digest{}, source.Descriptor.RootFSDiffIDs...), rendererDigest("b"))
	candidate.Descriptor.ConfigDigest = rendererDigest("c")
	candidate.Descriptor.ImmutableReference = string(rendererDigest("c"))
	candidate.Image.Digest = rendererDigest("c")
	candidate.Image.ConfigDigest = rendererDigest("c")
	candidate.Image.RootFSSubject, err = deploy.RootFSSubject(candidate.Descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateControlledSessionControllerPackageImageV1(source, candidate, expectedConfig, labels); err != nil {
		t.Fatal(err)
	}
	candidate.Descriptor.RootFSDiffIDs = append(candidate.Descriptor.RootFSDiffIDs, rendererDigest("d"))
	candidate.Image.RootFSSubject, err = deploy.RootFSSubject(candidate.Descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateControlledSessionControllerPackageImageV1(source, candidate, expectedConfig, labels); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("extra-layer validation error = %v", err)
	}
}

func TestBuildControlledSessionControllerPackageRejectsNonLinuxBeforeDocker(t *testing.T) {
	platform := blueprint.Platform{OS: "darwin", Architecture: "amd64", Canonical: "darwin/amd64"}
	current := CurrentBuild{Lock: deploy.BuildLockV1{Platform: platform}}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = buildControlledSessionControllerPackageV1(t.Context(), store, current, controlledSessionControllerPackageBackendV1{
		locateExecutable: func() (string, error) { called = true; return "", nil },
		buildCommand:     func(CommandSpec, RunOptions) error { called = true; return nil },
		docker:           func(context.Context, ...string) (string, error) { called = true; return "", nil },
		hostRelease:      currentControllerReleaseV1(),
	})
	if err == nil || !strings.Contains(err.Error(), "require a Linux controller image") || called {
		t.Fatalf("non-Linux preflight = called %t error %v", called, err)
	}
}

func TestValidateMatchingControllerReleaseRejectsDifferentBuild(t *testing.T) {
	release := currentControllerReleaseV1()
	release.BuildCommit += "different"
	if err := validateMatchingControllerReleaseV1(release); err == nil || !strings.Contains(err.Error(), "does not match host Reploy") {
		t.Fatalf("release mismatch error = %v", err)
	}
}

func TestControlledSessionControllerPackageDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("controller package integration requires supported Linux, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	sourceReference, platform := buildApplicationStartupVerifierIntegrationImage(t, ctx)

	root := repositoryRootForControllerPackageTestV1(t)
	outdir := t.TempDir()
	build := exec.CommandContext(ctx, filepath.Join(root, "tools", "build_reploy"), "--target", "linux-"+runtime.GOARCH, "--outdir", outdir)
	build.Dir = root
	build.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build packaged Reploy: %v\n%s", err, output)
	}
	packagedHost := filepath.Join(outdir, "linux-"+runtime.GOARCH, "reploy")
	manifest, err := probearchive.Verify(packagedHost)
	if err != nil {
		t.Fatal(err)
	}
	const maximumFocusedSessionClientSize = 10 * 1024 * 1024
	for _, entry := range manifest.SessionClients {
		size, err := strconv.ParseInt(entry.Size, 10, 64)
		if err != nil {
			t.Fatalf("parse %s session client size: %v", entry.Platform, err)
		}
		if size >= maximumFocusedSessionClientSize {
			t.Fatalf("%s session client is %d bytes; focused helper must stay below %d bytes", entry.Platform, size, maximumFocusedSessionClientSize)
		}
	}

	inspectionOutput, err := runDockerOutput(ctx, "image", "inspect", sourceReference)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := parseDockerImageInspectionDetails(sourceReference, platform, []byte(inspectionOutput))
	if err != nil {
		t.Fatal(err)
	}
	rootFS, err := deploy.RootFSSubject(inspection.Descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	sourceImage := providers.RealizedImageV1{
		Digest: inspection.Descriptor.ConfigDigest, ConfigDigest: inspection.Descriptor.ConfigDigest, RootFSSubject: rootFS,
	}
	current := CurrentBuild{
		Generation: deploy.EnvironmentGenerationState{Reference: sourceReference},
		Lock:       deploy.BuildLockV1{Platform: platform, FinalImage: sourceImage},
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := buildControlledSessionControllerPackageV1(ctx, store, current, controlledSessionControllerPackageBackendV1{
		locateExecutable: func() (string, error) { return packagedHost, nil },
		buildCommand:     runCommand,
		docker:           runDockerOutput,
		hostRelease:      manifest.Release,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeBuiltImageCandidate(context.Background(), prepared.Candidate, runDockerOutput); err != nil {
			t.Errorf("cleanup controller package: %v", err)
		}
	})
	if prepared.Package.SourceImage != sourceImage || prepared.Package.Image == sourceImage {
		t.Fatalf("controller package image authority = %#v", prepared.Package)
	}

	for _, test := range []struct{ subcommand, want string }{
		{subcommand: "client", want: "REPLOY_SESSION_SOCKET"},
		{subcommand: "attach", want: "reploy-session-client attach usage error"},
	} {
		command := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "reploy-session-client", string(prepared.Package.Image.Digest), test.subcommand)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), test.want) {
			t.Fatalf("packaged %s command = error %v output %q", test.subcommand, err, output)
		}
	}
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "reploy-session-client", sourceReference, "client")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "executable file not found") {
		t.Fatalf("source workload session client absence = error %v output %q", err, output)
	}
}

func repositoryRootForControllerPackageTestV1(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate controller package integration source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
