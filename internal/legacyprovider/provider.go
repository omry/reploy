// Package legacyprovider contains the pre-graph provider records retained only
// by the public Docker image lifecycle until the Slice 6 cutover.
package legacyprovider

import (
	"fmt"
	"path"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

type Bundle struct {
	Provider      blueprint.ComponentType
	RecipeVersion string
	Platform      string
	BaseIdentity  string
	Artifacts     []Artifact
	Executables   map[string]ExecutableOutput
}

type Artifact struct {
	Identifier string
	Version    string
	Kind       string
	Path       string
	SHA256     string
}

type ExecutableOutput struct {
	Component string
	Binary    string
	ImagePath string
}

type MaterializeRequest struct {
	Bundle Bundle
}

type Materialization struct {
	Provider    blueprint.ComponentType
	Version     string
	BundleMount string
	Artifacts   []Artifact
	Steps       []MaterializationStep
	Executables map[string]ExecutableOutput
}

type MaterializationStep struct {
	Argv []string
	Env  map[string]string
}

func ValidateBundle(bundle Bundle) error {
	if bundle.Provider == "" || bundle.RecipeVersion == "" || bundle.Platform == "" || bundle.BaseIdentity == "" {
		return fmt.Errorf("provider bundle identity is incomplete")
	}
	seenPaths := map[string]bool{}
	for index, artifact := range bundle.Artifacts {
		field := fmt.Sprintf("artifacts[%d]", index)
		if artifact.Identifier == "" || artifact.Kind == "" || artifact.Path == "" {
			return fmt.Errorf("%s identity is incomplete", field)
		}
		clean := path.Clean(artifact.Path)
		if path.IsAbs(artifact.Path) || clean != artifact.Path || strings.Contains(artifact.Path, `\`) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("%s.path must stay within the bundle", field)
		}
		if len(artifact.SHA256) != 64 || !isLowerHex(artifact.SHA256) {
			return fmt.Errorf("%s.sha256 must be a lowercase SHA-256 digest", field)
		}
		if seenPaths[clean] {
			return fmt.Errorf("provider bundle contains duplicate path %q", clean)
		}
		seenPaths[clean] = true
	}
	seenImagePaths := map[string]ExecutableOutput{}
	for name, executable := range bundle.Executables {
		if executable.Component == "" || executable.Binary == "" || !path.IsAbs(executable.ImagePath) {
			return fmt.Errorf("executable %q output is incomplete or not absolute", name)
		}
		if existing, exists := seenImagePaths[executable.ImagePath]; exists && existing.Binary != executable.Binary {
			return fmt.Errorf("executable %q conflicts at image path %q", name, executable.ImagePath)
		}
		seenImagePaths[executable.ImagePath] = executable
	}
	return nil
}

func isLowerHex(value string) bool {
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
