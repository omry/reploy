package dockerdeploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/legacyprovider"
)

func TestGeneratedImageIdentityIsDirectoryKeyedAndSemantic(t *testing.T) {
	bundle := legacyprovider.Bundle{
		Provider: blueprint.ComponentTypePython, RecipeVersion: "python-v1", Platform: "linux/amd64",
		BaseIdentity: "python@sha256:base",
		Artifacts:    []legacyprovider.Artifact{{Identifier: "demo", Kind: "wheel", Path: "demo.whl", SHA256: strings.Repeat("a", 64)}},
		Executables:  map[string]legacyprovider.ExecutableOutput{},
	}
	first, err := generatedImageIdentity("demo", t.TempDir(), GeneratedImageStaging, []legacyprovider.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reference != first.Repository+":staging" || !strings.HasPrefix(first.Repository, "reploy/demo-") {
		t.Fatalf("identity = %#v", first)
	}
	second, err := generatedImageIdentity("demo", t.TempDir(), GeneratedImageStaging, []legacyprovider.Bundle{bundle})
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
	artifactA := legacyprovider.Artifact{Identifier: "a", Kind: "wheel", Path: "a.whl", SHA256: strings.Repeat("a", 64)}
	artifactB := legacyprovider.Artifact{Identifier: "b", Kind: "wheel", Path: "b.whl", SHA256: strings.Repeat("b", 64)}
	makeBundle := func(artifacts []legacyprovider.Artifact) legacyprovider.Bundle {
		return legacyprovider.Bundle{
			Provider: blueprint.ComponentTypePython, RecipeVersion: "python-v1", Platform: "linux/amd64",
			BaseIdentity: "python@sha256:base", Artifacts: artifacts,
			Executables: map[string]legacyprovider.ExecutableOutput{},
		}
	}
	first, err := generatedImageFingerprint([]legacyprovider.Bundle{makeBundle([]legacyprovider.Artifact{artifactA, artifactB})})
	if err != nil {
		t.Fatal(err)
	}
	second, err := generatedImageFingerprint([]legacyprovider.Bundle{makeBundle([]legacyprovider.Artifact{artifactB, artifactA})})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("fingerprints differ: %s != %s", first, second)
	}
}
