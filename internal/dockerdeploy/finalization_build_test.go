package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func finalizationBuildFixture(t *testing.T) (providerstore.Store, FinalizationBuildRequest) {
	t.Helper()
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	source := InspectedImageCandidate{Image: providers.RealizedImageV1{
		Digest: rendererDigest("1"), ConfigDigest: rendererDigest("1"), RootFSSubject: rendererDigest("2"),
	}}
	source.Config = deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
	}
	source.Labels = map[string]string{"org.example.vendor": "inherited"}
	source.Descriptor = deploy.ImageDescriptor{
		Schema: deploy.ImageDescriptorSchemaV1, Platform: platform,
		AuthorReference: string(source.Image.Digest), ImmutableReference: string(source.Image.Digest), ConfigDigest: source.Image.ConfigDigest,
		RootFSDiffIDs: []canonical.Digest{rendererDigest("4")},
	}
	source.Image.RootFSSubject, err = deploy.RootFSSubject(source.Descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	validation := deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: source.Image.RootFSSubject,
		Profiles: []providers.ValidationEvidence{}, RuntimePolicy: rendererDigest("3"), ExposedOutputs: []providers.ExecutableEvidence{},
	}
	digest, err := deploy.PrefixValidationDigest(validation)
	if err != nil {
		t.Fatal(err)
	}
	return store, FinalizationBuildRequest{
		Source: source, Validation: validation,
		ValidationReference: providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: digest},
		Platform:            platform,
	}
}

func TestFinalizationDockerfileAddsOnlyFixedValidationLabels(t *testing.T) {
	_, request := finalizationBuildFixture(t)
	dockerfile, err := FinalizationDockerfile(request)
	if err != nil {
		t.Fatal(err)
	}
	text := string(dockerfile)
	for _, name := range []string{deploy.ValidationSchemaLabel, deploy.ValidationSubjectLabel, deploy.ValidationRecordLabel} {
		if !strings.Contains(text, `LABEL "`+name+`"=`) {
			t.Fatalf("Dockerfile omits %s:\n%s", name, text)
		}
	}
	if strings.Contains(text, "\nRUN ") || strings.Contains(text, "buildx") || strings.Count(text, "\nLABEL ") != 3 {
		t.Fatalf("finalization Dockerfile contains unexpected operations:\n%s", text)
	}
}

func TestBuildFinalizedImageCandidateUsesOrdinaryUntaggedBuild(t *testing.T) {
	store, request := finalizationBuildFixture(t)
	stubFinalizationBuildBaseReference(t, request.Source.Image.ConfigDigest)
	original := runFinalizationBuildCommand
	t.Cleanup(func() { runFinalizationBuildCommand = original })
	var workspace string
	runFinalizationBuildCommand = func(command CommandSpec, _ RunOptions) error {
		joined := strings.Join(command.Args, " ")
		if command.Name != "docker" || len(command.Args) < 2 || command.Args[0] != "build" || command.Args[1] != "--no-cache" || strings.Contains(joined, "buildx") || strings.Contains(joined, "--tag") {
			t.Fatalf("command = %#v", command)
		}
		iidPath := commandOption(t, command.Args, "--iidfile")
		workspace = filepath.Dir(iidPath)
		return os.WriteFile(iidPath, []byte(string(rendererDigest("4"))+"\n"), 0o600)
	}
	candidate, err := BuildFinalizedImageCandidate(store, request, RunOptions{NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ImageID != rendererDigest("4") {
		t.Fatalf("candidate = %#v", candidate)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("finalization workspace remains: %v", err)
	}
}

func TestFinalizationRejectsMismatchedValidationRecord(t *testing.T) {
	_, request := finalizationBuildFixture(t)
	request.ValidationReference.Digest = rendererDigest("f")
	if _, err := FinalizationDockerfile(request); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
}
