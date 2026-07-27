package dockerdeploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
)

func rendererMountSources() []MaterializationMountSource {
	return []MaterializationMountSource{{ID: "script", ContextPath: "mounts/script"}, {ID: "wheels", ContextPath: "mounts/wheels"}}
}

func TestMaterializationDockerfileRendersPinnedAtomicLayer(t *testing.T) {
	transaction := rendererTransaction()
	transaction.FinalImageConfig.Environment = []providers.EnvironmentVariable{
		{Name: "HOME", Value: "/home/app"},
		{Name: "LITERAL", Value: "$HOME with spaces"},
	}
	transaction.FinalImageConfig.Entrypoint = []string{"/opt/reploy/providers/python/web/bin/app", "$(not-shell)"}
	transaction.FinalImageConfig.Command = []string{"serve"}
	transaction.FinalImageConfig.Labels = []providers.ImageLabel{{Name: "io.reploy.component", Value: "web $HOME"}}
	dockerfile, err := MaterializationDockerfile(transaction, rendererMountSources())
	if err != nil {
		t.Fatal(err)
	}
	text := string(dockerfile)
	wants := []string{
		"# syntax=" + MaterializationDockerfileSyntax,
		"ARG REPLOY_BASE_IMAGE=scratch\nFROM ${REPLOY_BASE_IMAGE}",
		"USER 0:0\nWORKDIR \"/\"",
		"RUN --network=none --mount=type=bind,source=mounts/script,target=/.reploy-build/script,readonly --mount=type=bind,source=mounts/wheels,target=/.reploy-build/wheels,readonly [\"/usr/bin/env\",\"-i\",\"/bin/sh\",\"-c\"",
		`"python-v1","0022","/bin/sh","-eu","/.reploy-build/script/python-web.sh"`,
		`"$(touch /tmp/not-shell)"`,
		`ENV LITERAL="\$HOME with spaces"`,
		`ENTRYPOINT ["/opt/reploy/providers/python/web/bin/app","$(not-shell)"]`,
		`CMD ["serve"]`,
		"HEALTHCHECK NONE\nSTOPSIGNAL SIGTERM",
		`LABEL "io.reploy.component"="web \$HOME"`,
	}
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("Dockerfile does not contain %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "\nRUN ") != 1 {
		t.Fatalf("Dockerfile must contain exactly one RUN:\n%s", text)
	}
	if strings.Contains(text, "docker/dockerfile:1.7") || strings.Contains(text, "buildx") {
		t.Fatalf("Dockerfile contains a prototype frontend or Buildx dependency:\n%s", text)
	}
}

func TestMaterializationDockerfileRejectsResolverPrivateOutput(t *testing.T) {
	transaction := rendererTransaction()
	transaction.Mounts = append(transaction.Mounts, providers.BuildMount{ID: "zz_output", SourceKind: providers.BuildMountSourcePrivateOutput, Destination: "/.reploy-build/output", ExpectedKind: "directory"})
	if _, err := MaterializationDockerfile(transaction, rendererMountSources()); err == nil || !strings.Contains(err.Error(), "disposable resolver") {
		t.Fatalf("error = %v", err)
	}
}

func TestMaterializationDockerfileRejectsUnrenderableBackendInputs(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*providers.MaterializationTransaction, *[]MaterializationMountSource)
		message string
	}{
		{name: "supplementary groups", mutate: func(transaction *providers.MaterializationTransaction, _ *[]MaterializationMountSource) {
			transaction.BuildIdentity.SupplementaryGIDs = []string{"10"}
		}, message: "supplementary"},
		{name: "missing source", mutate: func(_ *providers.MaterializationTransaction, sources *[]MaterializationMountSource) {
			*sources = (*sources)[:1]
		}, message: "do not match"},
		{name: "unsafe source", mutate: func(_ *providers.MaterializationTransaction, sources *[]MaterializationMountSource) {
			(*sources)[0].ContextPath = "mounts/script,ro"
		}, message: "unsupported"},
		{name: "newline environment", mutate: func(transaction *providers.MaterializationTransaction, _ *[]MaterializationMountSource) {
			transaction.FinalImageConfig.Environment = []providers.EnvironmentVariable{{Name: "VALUE", Value: "first\nsecond"}}
		}, message: "unsupported Dockerfile"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := rendererTransaction()
			sources := rendererMountSources()
			test.mutate(&transaction, &sources)
			if _, err := MaterializationDockerfile(transaction, sources); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want %q", err, test.message)
			}
		})
	}
}
