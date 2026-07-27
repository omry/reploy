package dockerdeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

func TestMatchReusablePythonWorkspaceSourcesRequiresUnchangedManifest(t *testing.T) {
	root := t.TempDir()
	document, observed := sourceReuseWorkspaceFixture(t, root)
	lockedDemo := testPythonResolvedSource("application", "demo", "1.0", observed[0].SourceManifestDigest, reuseTestDigest("1"))
	lockedOtherComponent := testPythonResolvedSource("tools", "demo", "1.0", observed[0].SourceManifestDigest, reuseTestDigest("2"))
	lockedChanged := testPythonResolvedSource("application", "changed", "1.0", reuseTestDigest("3"), reuseTestDigest("4"))

	current, err := ResolveSelectedPythonWorkspaceSources(document, root, []string{"changed", "demo"})
	if err != nil {
		t.Fatal(err)
	}
	reusable, err := MatchReusablePythonWorkspaceSources(
		[]providers.ResolvedSourceInput{lockedChanged, lockedDemo, lockedOtherComponent}, current,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(reusable) != 2 || !reflect.DeepEqual(reusable[0], lockedDemo) || !reflect.DeepEqual(reusable[1], lockedOtherComponent) {
		t.Fatalf("reusable sources = %#v", reusable)
	}
}

func sourceReuseWorkspaceFixture(t *testing.T, root string) (blueprint.Document, []PythonWorkspaceSource) {
	t.Helper()
	for _, name := range []string{"demo", "changed"} {
		directory := filepath.Join(root, name)
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "pyproject.toml"), []byte("[project]\nname='"+name+"'\nversion='1.0'\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document := blueprint.Document{Environment: blueprint.Environment{Workspace: blueprint.Workspace{
		PythonPackages: map[string]string{"demo": "demo", "changed": "changed"},
	}}}
	observed, err := ResolveSelectedPythonWorkspaceSources(document, root, []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	return document, observed
}
