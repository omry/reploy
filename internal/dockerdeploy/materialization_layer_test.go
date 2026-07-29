package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func materializationLayerFixture(t *testing.T) (providerstore.Store, MaterializationLayerRequest) {
	t.Helper()
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script, err := store.Publish(context.Background(), "scripts/python-web.sh", "script", strings.NewReader("#!/bin/sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	wheel, err := store.Publish(context.Background(), "wheels/demo.whl", "wheel", strings.NewReader("wheel bytes"))
	if err != nil {
		t.Fatal(err)
	}
	transaction := rendererTransaction()
	transaction.Script = script
	transaction.Mounts[0].SourceDigest = script.SHA256
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	return store, MaterializationLayerRequest{
		Transaction: transaction, Platform: platform,
		MountInputs: []MaterializationMountInput{
			{ID: "script", SourceDigest: script.SHA256, Files: []MaterializationMountFile{{RelativePath: "python-web.sh", Artifact: script}}},
			{ID: "wheels", SourceDigest: transaction.Mounts[1].SourceDigest, Files: []MaterializationMountFile{{RelativePath: "demo.whl", Artifact: wheel}}},
		},
	}
}

func TestBuildMaterializationLayerReturnsUnacceptedImageID(t *testing.T) {
	store, request := materializationLayerFixture(t)
	stubMaterializationBuildBaseReference(t, request.Transaction.Upstream.ConfigDigest)
	original := runMaterializationBuildCommand
	t.Cleanup(func() { runMaterializationBuildCommand = original })
	var workspace string
	runMaterializationBuildCommand = func(spec CommandSpec, _ RunOptions) error {
		if spec.Name != "docker" || len(spec.Args) < 2 || spec.Args[0] != "build" || spec.Args[1] != "--no-cache" {
			t.Fatalf("command = %#v", spec)
		}
		if base := commandOption(t, spec.Args, "--build-arg"); !strings.HasPrefix(base, "REPLOY_BASE_IMAGE="+temporaryBuildReferencePrefix) {
			t.Fatalf("base build argument = %q", base)
		}
		iidPath := commandOption(t, spec.Args, "--iidfile")
		workspace = filepath.Dir(iidPath)
		dockerfilePath := commandOption(t, spec.Args, "--file")
		content, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "# syntax="+MaterializationDockerfileSyntax) {
			t.Fatalf("Dockerfile = %s", content)
		}
		return os.WriteFile(iidPath, []byte(string(rendererDigest("f"))+"\n"), 0o600)
	}
	image, err := BuildMaterializationLayer(store, request, RunOptions{NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if image.Built.ImageID != rendererDigest("f") {
		t.Fatalf("unvalidated image = %#v", image)
	}
	wantKey, wantDigest, err := MaterializationAssemblyKey(request.Transaction, request.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(image.AssemblyKey, wantKey) || image.AssemblyKeyDigest != wantDigest {
		t.Fatalf("assembly identity = %#v, %q", image.AssemblyKey, image.AssemblyKeyDigest)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("private build workspace still exists: %v", err)
	}
}

func TestBuildMaterializationLayerReturnsNoIdentityOnFailure(t *testing.T) {
	store, request := materializationLayerFixture(t)
	stubMaterializationBuildBaseReference(t, request.Transaction.Upstream.ConfigDigest)
	original := runMaterializationBuildCommand
	t.Cleanup(func() { runMaterializationBuildCommand = original })
	cause := errors.New("argument list too long")
	runMaterializationBuildCommand = func(CommandSpec, RunOptions) error { return cause }
	image, err := BuildMaterializationLayer(store, request, RunOptions{})
	var failure *providers.BuildErrorV1
	if err == nil || !errors.As(err, &failure) || failure.Code != "materialization.failed" || failure.Phase != "materialize" || !errors.Is(err, cause) || strings.Contains(err.Error(), "argument list too long") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(image, MaterializationLayerCandidate{}) {
		t.Fatalf("failed build returned image identity: %#v", image)
	}
}

func TestBuildMaterializationLayerRejectsMalformedIID(t *testing.T) {
	store, request := materializationLayerFixture(t)
	stubMaterializationBuildBaseReference(t, request.Transaction.Upstream.ConfigDigest)
	original := runMaterializationBuildCommand
	t.Cleanup(func() { runMaterializationBuildCommand = original })
	runMaterializationBuildCommand = func(spec CommandSpec, _ RunOptions) error {
		return os.WriteFile(commandOption(t, spec.Args, "--iidfile"), []byte("candidate:latest\n"), 0o600)
	}
	if image, err := BuildMaterializationLayer(store, request, RunOptions{}); err == nil || !reflect.DeepEqual(image, MaterializationLayerCandidate{}) {
		t.Fatalf("image = %#v, error = %v", image, err)
	}
}

func TestInspectBuiltImageCandidateDerivesObservedIdentity(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	imageID := rendererDigest("c")
	diffID := rendererDigest("d")
	inspection := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{"Labels":{"io.reploy.component":"python/web"}}}]`, imageID, diffID)
	var gotArgs []string
	run := func(_ context.Context, args ...string) (string, error) {
		gotArgs = append([]string{}, args...)
		return inspection, nil
	}
	result, err := inspectBuiltImageCandidate(context.Background(), BuiltImageCandidate{ImageID: imageID}, platform, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotArgs, []string{"image", "inspect", string(imageID)}) {
		t.Fatalf("Docker args = %v", gotArgs)
	}
	if result.Descriptor.ImmutableReference != string(imageID) || result.Descriptor.ManifestDigest != "" {
		t.Fatalf("descriptor = %#v", result.Descriptor)
	}
	if result.Image.Digest != imageID || result.Image.ConfigDigest != imageID {
		t.Fatalf("realized image = %#v", result.Image)
	}
	if result.Labels["io.reploy.component"] != "python/web" {
		t.Fatalf("labels = %#v", result.Labels)
	}
	wantSubject, err := deploy.RootFSSubject([]canonical.Digest{diffID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Image.RootFSSubject != wantSubject {
		t.Fatalf("rootfs subject = %q, want %q", result.Image.RootFSSubject, wantSubject)
	}
}

func TestInspectBuiltImageCandidateClassifiesMissingDockerImage(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	imageID := rendererDigest("c")
	_, err = inspectBuiltImageCandidate(
		t.Context(),
		BuiltImageCandidate{ImageID: imageID},
		platform,
		func(context.Context, ...string) (string, error) {
			return "", errors.New(
				"docker image inspect " + string(imageID) +
					": exit status 1: []\nError response from daemon: No such image: " +
					string(imageID),
			)
		},
	)
	var missing *dockerImageNotFoundError
	if !errors.As(err, &missing) ||
		missing.ImageID != imageID ||
		strings.Contains(err.Error(), "[]") ||
		strings.Contains(err.Error(), "Error response from daemon") {
		t.Fatalf("missing image error = %#v / %v", missing, err)
	}
}

func TestInspectBuiltImageCandidateRejectsDockerIdentityMismatch(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	candidateID := rendererDigest("e")
	actualID := rendererDigest("f")
	diffID := rendererDigest("1")
	inspection := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{}}]`, actualID, diffID)
	result, err := inspectBuiltImageCandidate(context.Background(), BuiltImageCandidate{ImageID: candidateID}, platform, func(context.Context, ...string) (string, error) {
		return inspection, nil
	})
	if err == nil || !reflect.DeepEqual(result, InspectedImageCandidate{}) {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestInspectMaterializationLayerCandidateBindsAssemblyAndImagePolicy(t *testing.T) {
	_, request := materializationLayerFixture(t)
	key, keyDigest, err := MaterializationAssemblyKey(request.Transaction, request.Platform)
	if err != nil {
		t.Fatal(err)
	}
	imageID := rendererDigest("7")
	diffID := rendererDigest("8")
	inspection := fmt.Sprintf(`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{"Env":[],"User":"1000:1000","WorkingDir":"/work","Entrypoint":[],"Cmd":[],"Healthcheck":{"Test":["NONE"]},"StopSignal":"SIGTERM","OnBuild":[],"Volumes":{},"Labels":{}}}]`, imageID, diffID)
	candidate := MaterializationLayerCandidate{
		Built: BuiltImageCandidate{ImageID: imageID}, AssemblyKey: key, AssemblyKeyDigest: keyDigest,
	}
	result, err := inspectMaterializationLayerCandidate(context.Background(), candidate, request, func(context.Context, ...string) (string, error) {
		return inspection, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssemblyKeyDigest != keyDigest || result.Image.Image.Digest != imageID {
		t.Fatalf("inspected candidate = %#v", result)
	}
}

func TestInspectMaterializationLayerCandidateRejectsAssemblyMismatchBeforeDocker(t *testing.T) {
	_, request := materializationLayerFixture(t)
	key, keyDigest, err := MaterializationAssemblyKey(request.Transaction, request.Platform)
	if err != nil {
		t.Fatal(err)
	}
	key.RendererProfile = "other-renderer-v1"
	calls := 0
	result, err := inspectMaterializationLayerCandidate(context.Background(), MaterializationLayerCandidate{
		Built: BuiltImageCandidate{ImageID: rendererDigest("7")}, AssemblyKey: key, AssemblyKeyDigest: keyDigest,
	}, request, func(context.Context, ...string) (string, error) {
		calls++
		return "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "assembly identity") || calls != 0 || !reflect.DeepEqual(result, InspectedMaterializationLayerCandidate{}) {
		t.Fatalf("result = %#v, calls = %d, error = %v", result, calls, err)
	}
}

func TestValidateInspectedMaterializationCandidateChecksControlledValuesOnly(t *testing.T) {
	transaction := rendererTransaction()
	transaction.FinalImageConfig.Environment = []providers.EnvironmentVariable{{Name: "REPLOY_MODE", Value: "managed"}}
	transaction.FinalImageConfig.Labels = []providers.ImageLabel{{Name: "io.reploy.component", Value: "python/web"}}
	disabledHealthcheck, err := canonicalizeDockerHealthcheck(dockerHealthcheck{Test: []string{"NONE"}})
	if err != nil {
		t.Fatal(err)
	}
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	candidate := InspectedImageCandidate{
		Descriptor: deploy.ImageDescriptor{
			Schema: deploy.ImageDescriptorSchemaV1, Platform: platform,
			AuthorReference: string(rendererDigest("2")), ImmutableReference: string(rendererDigest("2")),
			ConfigDigest: rendererDigest("2"), RootFSDiffIDs: []canonical.Digest{rendererDigest("4")},
		},
		Config: deploy.BaseConfig{
			Schema: deploy.BaseConfigSchemaV1,
			Environment: []deploy.ConfigEnvironmentVariable{
				{Name: "PATH", Value: "/usr/bin"},
				{Name: "REPLOY_MODE", Value: "managed"},
			},
			User: transaction.FinalImageConfig.User, WorkingDir: transaction.FinalImageConfig.WorkingDir,
			Entrypoint: []string{}, Command: []string{}, Healthcheck: disabledHealthcheck,
			StopSignal: transaction.FinalImageConfig.StopSignal, OnBuild: []string{}, Volumes: []string{},
		},
		Labels: map[string]string{
			"io.reploy.component": "python/web",
			"org.example.vendor":  "inherited-and-ignored",
		},
		Image: providers.RealizedImageV1{Digest: rendererDigest("2"), ConfigDigest: rendererDigest("2")},
	}
	candidate.Image.RootFSSubject, err = deploy.RootFSSubject(candidate.Descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateInspectedMaterializationCandidate(transaction, candidate); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*InspectedImageCandidate)
		want   string
	}{
		{name: "user", mutate: func(value *InspectedImageCandidate) { value.Config.User = "0:0" }, want: "user"},
		{name: "environment", mutate: func(value *InspectedImageCandidate) { value.Config.Environment[1].Value = "other" }, want: "environment"},
		{name: "entrypoint", mutate: func(value *InspectedImageCandidate) { value.Config.Entrypoint = []string{"/bin/false"} }, want: "entrypoint"},
		{name: "healthcheck", mutate: func(value *InspectedImageCandidate) { value.Config.Healthcheck = "" }, want: "healthcheck"},
		{name: "label", mutate: func(value *InspectedImageCandidate) { value.Labels["io.reploy.component"] = "other" }, want: "label"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := candidate
			value.Config.Environment = append([]deploy.ConfigEnvironmentVariable{}, candidate.Config.Environment...)
			value.Config.Entrypoint = append([]string{}, candidate.Config.Entrypoint...)
			value.Config.Command = append([]string{}, candidate.Config.Command...)
			value.Labels = map[string]string{}
			for name, labelValue := range candidate.Labels {
				value.Labels[name] = labelValue
			}
			test.mutate(&value)
			if err := ValidateInspectedMaterializationCandidate(transaction, value); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func commandOption(t *testing.T, args []string, option string) string {
	t.Helper()
	for index := range args {
		if args[index] == option && index+1 < len(args) {
			return args[index+1]
		}
	}
	t.Fatalf("command has no %s: %#v", option, args)
	return ""
}
