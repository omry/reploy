package dockerdeploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func TestGeneratedImageIdentityIsDirectoryKeyedAndSemantic(t *testing.T) {
	bundle := providers.Bundle{
		Provider: blueprint.ComponentTypePython, RecipeVersion: "python-v1", Platform: "linux/amd64",
		BaseIdentity: "python@sha256:base",
		Artifacts:    []providers.Artifact{{Identifier: "demo", Kind: "wheel", Path: "demo.whl", SHA256: strings.Repeat("a", 64)}},
		Executables:  map[string]providers.ExecutableOutput{},
	}
	first, err := generatedImageIdentity("demo", t.TempDir(), GeneratedImageStaging, []providers.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reference != first.Repository+":staging" || !strings.HasPrefix(first.Repository, "reploy/demo-") {
		t.Fatalf("identity = %#v", first)
	}
	second, err := generatedImageIdentity("demo", t.TempDir(), GeneratedImageStaging, []providers.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	if first.Repository == second.Repository {
		t.Fatal("different deployment directories share a repository")
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatal("directory identity leaked into semantic fingerprint")
	}
}

func TestGeneratedImageFingerprintIgnoresArtifactOrdering(t *testing.T) {
	artifactA := providers.Artifact{Identifier: "a", Kind: "wheel", Path: "a.whl", SHA256: strings.Repeat("a", 64)}
	artifactB := providers.Artifact{Identifier: "b", Kind: "wheel", Path: "b.whl", SHA256: strings.Repeat("b", 64)}
	makeBundle := func(artifacts []providers.Artifact) providers.Bundle {
		return providers.Bundle{
			Provider: blueprint.ComponentTypePython, RecipeVersion: "python-v1", Platform: "linux/amd64",
			BaseIdentity: "python@sha256:base", Artifacts: artifacts,
			Executables: map[string]providers.ExecutableOutput{},
		}
	}
	first, err := generatedImageFingerprint([]providers.Bundle{makeBundle([]providers.Artifact{artifactA, artifactB})})
	if err != nil {
		t.Fatal(err)
	}
	second, err := generatedImageFingerprint([]providers.Bundle{makeBundle([]providers.Artifact{artifactB, artifactA})})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("fingerprints differ: %s != %s", first, second)
	}
}
