package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

func applicationRuntimeLayerTestRequest(t *testing.T) ApplicationRuntimeLayerBuildRequest {
	t.Helper()
	_, finalization := finalizationBuildFixture(t)
	verifier := deploy.ApplicationStartupVerifierContractV1()
	verifier.Artifact = rendererDigest("a")
	verifier.Size = "123"
	return ApplicationRuntimeLayerBuildRequest{
		Source: finalization.Source, Verifier: verifier,
		Account:  testApplicationLocalAccountV1(),
		Platform: finalization.Platform,
	}
}

func applicationRuntimeLayerTestCandidate(t *testing.T, request ApplicationRuntimeLayerBuildRequest) InspectedImageCandidate {
	t.Helper()
	candidate := request.Source
	candidate.Descriptor.RootFSDiffIDs = append(
		append([]canonical.Digest{}, candidate.Descriptor.RootFSDiffIDs...),
		rendererDigest("b"), rendererDigest("d"),
	)
	candidate.Descriptor.AuthorReference = string(rendererDigest("c"))
	candidate.Descriptor.ImmutableReference = string(rendererDigest("c"))
	candidate.Descriptor.ConfigDigest = rendererDigest("c")
	rootFS, err := deploy.RootFSSubject(candidate.Descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Image.Digest = candidate.Descriptor.ConfigDigest
	candidate.Image.ConfigDigest = candidate.Descriptor.ConfigDigest
	candidate.Image.RootFSSubject = rootFS
	return candidate
}

func TestApplicationRuntimeLayerDockerfileAddsOnlyFixedVerifier(t *testing.T) {
	request := applicationRuntimeLayerTestRequest(t)
	content, err := ApplicationRuntimeLayerDockerfile(request)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(content)
	for _, want := range []string{
		"# syntax=" + MaterializationDockerfileSyntax,
		"FROM ${REPLOY_BASE_IMAGE} AS reploy-runtime-account",
		"USER 0:0",
		`RUN ["/reploy-probe","install-local-account","reploy","1000","1000","/mnt/reploy-home"]`,
		"FROM ${REPLOY_BASE_IMAGE}",
		`RUN --mount=type=bind,source=reploy-probe,target=/reploy-build-probe,readonly ["/reploy-build-probe", "install-runtime-verifier", "/reploy-probe"]`,
		"COPY --from=reploy-runtime-account /etc/passwd /etc/group /etc/",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, dockerfile)
		}
	}
	for _, forbidden := range []string{"ADD ", "ENTRYPOINT ", "CMD "} {
		if strings.Contains(dockerfile, forbidden) {
			t.Fatalf("Dockerfile contains %q:\n%s", forbidden, dockerfile)
		}
	}
}

func TestApplicationRuntimeLayerDockerfileRestoresInheritedUser(t *testing.T) {
	request := applicationRuntimeLayerTestRequest(t)
	request.Source.Config.User = "12345:23456"
	content, err := ApplicationRuntimeLayerDockerfile(request)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(content)
	if !strings.Contains(dockerfile, "USER 0:0\nCOPY --from=reploy-runtime-account") || !strings.HasSuffix(dockerfile, "USER \"12345:23456\"\n") {
		t.Fatalf("Dockerfile does not switch to root and restore the inherited user:\n%s", dockerfile)
	}
}

func TestValidateInspectedApplicationRuntimeLayerCandidatePreservesConfigAndAddsOneLayer(t *testing.T) {
	request := applicationRuntimeLayerTestRequest(t)
	candidate := applicationRuntimeLayerTestCandidate(t, request)
	if err := ValidateInspectedApplicationRuntimeLayerCandidate(request, candidate); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*InspectedImageCandidate)
		want   string
	}{
		{name: "configuration", mutate: func(value *InspectedImageCandidate) { value.Config.User = "0:0" }, want: "configuration"},
		{name: "labels", mutate: func(value *InspectedImageCandidate) { value.Labels = map[string]string{"changed": "yes"} }, want: "configuration"},
		{name: "no layer", mutate: func(value *InspectedImageCandidate) {
			value.Descriptor.RootFSDiffIDs = append([]canonical.Digest{}, request.Source.Descriptor.RootFSDiffIDs...)
			value.Image.RootFSSubject = request.Source.Image.RootFSSubject
		}, want: "exactly two"},
		{name: "changed prefix", mutate: func(value *InspectedImageCandidate) {
			value.Descriptor.RootFSDiffIDs[0] = rendererDigest("d")
			rootFS, err := deploy.RootFSSubject(value.Descriptor.RootFSDiffIDs)
			if err != nil {
				t.Fatal(err)
			}
			value.Image.RootFSSubject = rootFS
		}, want: "exactly two"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := candidate
			changed.Descriptor.RootFSDiffIDs = append([]canonical.Digest{}, candidate.Descriptor.RootFSDiffIDs...)
			changed.Labels = make(map[string]string, len(candidate.Labels))
			for name, value := range candidate.Labels {
				changed.Labels[name] = value
			}
			test.mutate(&changed)
			err := ValidateInspectedApplicationRuntimeLayerCandidate(request, changed)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	if !reflect.DeepEqual(candidate.Config, request.Source.Config) {
		t.Fatal("fixture changed inherited config")
	}
}
