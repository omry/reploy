// Package providers defines the backend-neutral contract between resolved
// blueprint components and ecosystem-specific bundle implementations.
package providers

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

// Provider resolves every active component of one ecosystem as a unit. It
// must produce a closed artifact set before image materialization begins.
type Provider interface {
	Type() blueprint.ComponentType
	RecipeVersion() string
	Prerequisites(ResolveRequest) []Prerequisite
	Resolve(context.Context, ResolveRequest) (Bundle, error)
	Materialize(MaterializeRequest) (Materialization, error)
}

// ResolveRequest contains provider inputs that can affect artifact resolution.
// Components and translations are sorted by name before a provider is called.
type ResolveRequest struct {
	Platform     string
	BaseImage    string
	Components   []Component
	Translations []Translation
	DirectRoots  []string
	Executables  []ExecutableRequest
}

type Component struct {
	Name         string
	Requirements []string
}

type Translation struct {
	Name     string
	Root     string
	Mappings map[string]string
}

// ExecutableRequest asks the provider to prove that Binary is supplied by the
// selected component set and to return its absolute final-image path.
type ExecutableRequest struct {
	Name      string
	Component string
	Binary    string
}

type PrerequisitePhase string

const (
	PrerequisiteResolve     PrerequisitePhase = "resolve"
	PrerequisiteMaterialize PrerequisitePhase = "materialize"
)

type PrerequisiteSource string

const (
	PrerequisiteBaseImage PrerequisiteSource = "base-image"
	PrerequisiteBuilder   PrerequisiteSource = "builder"
	PrerequisiteProvider  PrerequisiteSource = "provider"
)

// Prerequisite is declarative. Reploy checks it in Source and never installs a
// missing tool or runtime implicitly.
type Prerequisite struct {
	Name       string
	Constraint string
	Phase      PrerequisitePhase
	Source     PrerequisiteSource
	ProbeArgv  []string
}

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

// NormalizeResolveRequest makes provider execution and fingerprints stable.
func NormalizeResolveRequest(request ResolveRequest) ResolveRequest {
	sort.Slice(request.Components, func(i, j int) bool { return request.Components[i].Name < request.Components[j].Name })
	sort.Slice(request.Translations, func(i, j int) bool { return request.Translations[i].Name < request.Translations[j].Name })
	sort.Strings(request.DirectRoots)
	sort.Slice(request.Executables, func(i, j int) bool { return request.Executables[i].Name < request.Executables[j].Name })
	return request
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
