package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryBlueprintsResolve(t *testing.T) {
	paths := []string{
		"../../examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml",
		"../../tests/e2e/python/packages/git-source-app/git_source_app/reploy/git-source-app.blueprint.yaml",
		"../../tests/e2e/python/packages/smoke-blueprint/smoke.blueprint.yaml",
	}
	for _, filename := range paths {
		t.Run(filepath.Base(filename), func(t *testing.T) {
			content, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			source, err := Decode(content)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Resolve(source); err != nil {
				t.Fatal(err)
			}
		})
	}
}
