package deploy

import "testing"

func TestParseFilePackRef(t *testing.T) {
	ref, err := ParsePackRef("file:./demo.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "file" || ref.Source != "./demo.blueprint.yaml" || ref.Subdir != "" {
		t.Fatalf("unexpected ref: %#v", ref)
	}
	if ref.IsPinned {
		t.Fatalf("file refs should not be considered reproducibly pinned")
	}
}

func TestParsePyPIPackRefWithExplicitSubdir(t *testing.T) {
	ref, err := ParsePackRef("pypi:demo-suite==0.1.0#demo_suite/reploy/demo.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "demo-suite==0.1.0" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "demo_suite/reploy/demo.blueprint.yaml" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if !ref.IsPinned {
		t.Fatalf("pypi exact package ref should be pinned")
	}
}

func TestParsePyPIPackRefAllowsLatestWithExplicitSubdir(t *testing.T) {
	ref, err := ParsePackRef("pypi:demo-suite#demo_suite/reploy/demo.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "demo-suite" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "demo_suite/reploy/demo.blueprint.yaml" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.IsPinned {
		t.Fatalf("unpinned pypi ref should request latest, not be considered pinned")
	}
}

func TestParsePyPIPackRefRequiresExplicitBlueprintPath(t *testing.T) {
	_, err := ParsePackRef("pypi:Demo.Suite==0.1.0")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParsePyPIURLPackRef(t *testing.T) {
	ref, err := ParsePackRef("pypi://demo-suite/demo_suite/reploy/demo.blueprint.yaml?version=0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "demo-suite==0.1.0" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "demo_suite/reploy/demo.blueprint.yaml" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if !ref.IsPinned {
		t.Fatalf("pypi exact package ref should be pinned")
	}
}

func TestParsePyPIURLPackRefLatestIsUnpinned(t *testing.T) {
	ref, err := ParsePackRef("pypi://demo-suite/demo_suite/reploy/demo.blueprint.yaml?version=latest")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "demo-suite" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "demo_suite/reploy/demo.blueprint.yaml" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.IsPinned {
		t.Fatalf("latest pypi ref should not be considered pinned")
	}
}

func TestParsePyPIURLPackRefRequiresExplicitBlueprintPath(t *testing.T) {
	for _, raw := range []string{"pypi://demo-suite", "pypi://demo-suite/demo_suite/reploy"} {
		_, err := ParsePackRef(raw)
		if err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}

func TestParseSourcePackRefWithExplicitSubdir(t *testing.T) {
	ref, err := ParsePackRef("source:./demo-suite#demo_suite/reploy")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "source" || ref.Source != "./demo-suite" {
		t.Fatalf("unexpected ref: %#v", ref)
	}
	if ref.Subdir != "demo_suite/reploy" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.IsPinned {
		t.Fatalf("source refs should not be considered reproducibly pinned")
	}
}

func TestParseGitPackRefWithRefAndExplicitSubdir(t *testing.T) {
	ref, err := ParsePackRef("git:https://github.com/acme/demo.git#demo_server/reploy?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "git" || ref.Source != "https://github.com/acme/demo.git" {
		t.Fatalf("unexpected ref: %#v", ref)
	}
	if ref.Subdir != "demo_server/reploy" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.Query.Get("ref") != "main" {
		t.Fatalf("query = %#v", ref.Query)
	}
	if ref.IsPinned {
		t.Fatalf("branch git refs should resolve before being considered pinned")
	}
}

func TestParseGitPackRefWithCommitHashIsPinned(t *testing.T) {
	ref, err := ParsePackRef("git:https://github.com/acme/demo.git?ref=0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if !ref.IsPinned {
		t.Fatalf("full commit git refs should be considered pinned")
	}
}

func TestParseGitHubPackRefDefaultsToHTTPS(t *testing.T) {
	raw := "github://omry/arbiter/server/src/arbiter_server/reploy/arbiter.blueprint.yaml?ref=main"
	ref, err := ParsePackRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Raw != raw {
		t.Fatalf("raw = %q", ref.Raw)
	}
	if ref.Scheme != "git" {
		t.Fatalf("scheme = %q", ref.Scheme)
	}
	if ref.Source != "https://github.com/omry/arbiter.git" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "server/src/arbiter_server/reploy/arbiter.blueprint.yaml" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.Query.Get("ref") != "main" {
		t.Fatalf("query = %#v", ref.Query)
	}
	if ref.IsPinned {
		t.Fatalf("branch github refs should resolve before being considered pinned")
	}
}

func TestParseGitHubPackRefSupportsSSHTransport(t *testing.T) {
	ref, err := ParsePackRef("github://omry/arbiter/server/src/arbiter_server/reploy/arbiter.blueprint.yaml?ref=main&transport=ssh")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "ssh://git@github.com/omry/arbiter.git" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Query.Get("transport") != "" {
		t.Fatalf("transport leaked into git query: %#v", ref.Query)
	}
	if err := validateGitPackRef(ref); err != nil {
		t.Fatalf("ssh git ref did not validate: %v", err)
	}
}

func TestParseGitHubPackRefRequiresExplicitBlueprintFilePath(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{
			raw:  "github://omry/arbiter?ref=main",
			want: "github blueprint refs must include an explicit blueprint file path",
		},
		{
			raw:  "github://omry/arbiter/server/src/arbiter_server/reploy?ref=main",
			want: "github blueprint path must point to a *.blueprint.yaml file: server/src/arbiter_server/reploy",
		},
	} {
		_, err := ParsePackRef(tc.raw)
		if err == nil {
			t.Fatalf("expected error for %s", tc.raw)
		}
		if err.Error() != tc.want {
			t.Fatalf("err for %s = %v", tc.raw, err)
		}
	}
}

func TestParseGitHubPackRefRejectsUnsupportedQuery(t *testing.T) {
	_, err := ParsePackRef("github://omry/arbiter/server/src/arbiter_server/reploy/arbiter.blueprint.yaml?path=demo")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "unsupported github blueprint query parameter: path" {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePyPIPackRefRejectsDoubleSlashSubdir(t *testing.T) {
	_, err := ParsePackRef("pypi:demo-suite//demo_suite/reploy")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParsePackRefRejectsUnsupportedScheme(t *testing.T) {
	for _, ref := range []string{"oci:example", "sl:https://github.com/omry/reploy"} {
		_, err := ParsePackRef(ref)
		if err == nil {
			t.Fatalf("expected error for %s", ref)
		}
	}
}
