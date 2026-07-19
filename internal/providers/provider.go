// Package providers defines the backend-neutral contract between resolved
// blueprint components and ecosystem-specific bundle implementations.
package providers

import (
	"fmt"
	"path"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

// Bundle is the closed, checksummed provider result recorded in deployment
// state. Path is relative to the bundle root and is the only artifact location
// visible to a materialization recipe.
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

// Materialization is an offline, deterministic recipe. BundleMount is
// read-only; steps may reference only paths below it and declared fixed paths.
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

// ValidateBundle rejects incomplete or unsafe provider results before they are
// persisted or handed to a container backend.
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
