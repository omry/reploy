package dockerdeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/providers"
)

func TestMatchReusablePythonLocalSourcesRequiresUnchangedManifest(t *testing.T) {
	root := t.TempDir()
	overrides, observed := sourceReuseLocalFixture(t, root)
	lockedDemo := testPythonResolvedSource("application", "demo", "1.0", observed[0].SourceInputDigest, reuseTestDigest("1"))
	lockedOtherComponent := testPythonResolvedSource("tools", "demo", "1.0", observed[0].SourceInputDigest, reuseTestDigest("2"))
	lockedChanged := testPythonResolvedSource("application", "changed", "1.0", reuseTestDigest("3"), reuseTestDigest("4"))

	current, err := ObserveSelectedPythonLocalSources(overrides, []string{"changed", "demo"})
	if err != nil {
		t.Fatal(err)
	}
	reusable, err := MatchReusablePythonLocalSources(
		[]providers.ResolvedSourceInput{lockedChanged, lockedDemo, lockedOtherComponent}, current,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(reusable) != 2 || !reflect.DeepEqual(reusable[0], lockedDemo) || !reflect.DeepEqual(reusable[1], lockedOtherComponent) {
		t.Fatalf("reusable sources = %#v", reusable)
	}
}

func sourceReuseLocalFixture(t *testing.T, root string) ([]PythonLocalOverrideV1, []PythonLocalSource) {
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
	overrides := []PythonLocalOverrideV1{
		{Distribution: "changed", HostDir: filepath.Join(root, "changed")},
		{Distribution: "demo", HostDir: filepath.Join(root, "demo")},
	}
	observed, err := ObserveSelectedPythonLocalSources(overrides, []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	return overrides, observed
}
