package dockerdeploy

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// portablePythonAliasSpecV1 identifies one selected Python command export.
// Target is a canonical path in the final image. Destination is the
// application-facing path that the overlay publishes. BindingIdentity binds
// the destination to the selected portable-tool record; callers derive it
// from the complete binding before entering materialization.
//
// Output and Facts are used only to bind the final-image probe observation to
// the selected output. They are intentionally carried by this internal seam so
// alias validation cannot accidentally create evidence for an unrelated
// component or command.
type portablePythonAliasSpecV1 struct {
	Name            string
	Component       string
	Destination     string
	Target          string
	BindingIdentity string
	Output          providers.QualifiedOutput
	Facts           providers.CanonicalProviderData
}

func validatePortablePythonAliasDestinationV1(destination string) error {
	if destination == "" || !utf8.ValidString(destination) || strings.ContainsAny(destination, "\x00\r\n") || strings.ContainsRune(destination, '\\') || !path.IsAbs(destination) || path.Clean(destination) != destination || destination == "/" {
		return fmt.Errorf("Python alias destination %q must be a non-root absolute clean Linux path", destination)
	}
	for _, component := range strings.Split(strings.TrimPrefix(destination, "/"), "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("Python alias destination %q has an unsafe path component", destination)
		}
	}
	return nil
}

func validatePortablePythonAliasBindingIdentityV1(identity string) error {
	if identity == "" || !utf8.ValidString(identity) || strings.ContainsAny(identity, "\x00\r\n") {
		return fmt.Errorf("Python alias binding identity must be nonempty valid text")
	}
	return nil
}

func validatePortablePythonAliasTargetV1(target string) error {
	if target == "" || !utf8.ValidString(target) || strings.ContainsAny(target, "\x00\r\n\\") || !path.IsAbs(target) || path.Clean(target) != target {
		return fmt.Errorf("Python alias target must be an absolute clean final-image path")
	}
	installRoot := path.Clean(pythonprovider.InstallRoot)
	if target == installRoot || !strings.HasPrefix(target, installRoot+"/") {
		return fmt.Errorf("Python alias target %q is outside the Python provider install root", target)
	}
	suffix := strings.TrimPrefix(target, installRoot+"/")
	parts := strings.Split(suffix, "/")
	if len(parts) < 3 || parts[len(parts)-2] != "bin" || parts[len(parts)-1] == "" {
		return fmt.Errorf("Python alias target %q is not a generated Python console script", target)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("Python alias target %q has an unsafe runtime path", target)
		}
	}
	return nil
}
