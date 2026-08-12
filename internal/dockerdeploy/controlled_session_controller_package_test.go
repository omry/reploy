package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestControlledSessionControllerPackageDockerfileAddsOnlyControllerTool(t *testing.T) {
	source := applicationRuntimeLayerTestRequest(t).Source
	extracted := probearchive.ExtractedController{
		Platform: source.Descriptor.Platform.Canonical,
		Path:     "/tmp/reploy",
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
		"COPY --chmod=0555 reploy /usr/local/bin/reploy",
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
	extracted := probearchive.ExtractedController{
		Platform: source.Descriptor.Platform.Canonical, Path: "/tmp/reploy", Size: "123",
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
		{subcommand: "attach", want: "controlled-session attach usage error"},
	} {
		command := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "reploy", string(prepared.Package.Image.Digest), "controlled-session", test.subcommand)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), test.want) {
			t.Fatalf("packaged %s command = error %v output %q", test.subcommand, err, output)
		}
	}
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "reploy", sourceReference, "controlled-session", "client")
	if output, err := command.CombinedOutput(); err == nil || strings.Contains(string(output), "REPLOY_SESSION_SOCKET") {
		t.Fatalf("source workload unexpectedly exposes Reploy: error %v output %q", err, output)
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
