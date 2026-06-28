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
	ref, err := ParsePackRef("pypi:demo-suite==0.1.0#demo_suite/reploy")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "demo-suite==0.1.0" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "demo_suite/reploy" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if !ref.IsPinned {
		t.Fatalf("pypi exact package ref should be pinned")
	}
}

func TestParsePyPIPackRefAllowsLatestWithExplicitSubdir(t *testing.T) {
	ref, err := ParsePackRef("pypi:demo-suite#demo_suite/reploy")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "demo-suite" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "demo_suite/reploy" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.IsPinned {
		t.Fatalf("unpinned pypi ref should request latest, not be considered pinned")
	}
}

func TestParsePyPIPackRefDefaultsSubdirFromPackageName(t *testing.T) {
	ref, err := ParsePackRef("pypi:Demo.Suite==0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "Demo.Suite==0.1.0" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "demo_suite/reploy" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if !ref.IsPinned {
		t.Fatalf("pypi exact package ref should be pinned")
	}
}

func TestParsePyPIPackRefDefaultsSubdirForLatest(t *testing.T) {
	ref, err := ParsePackRef("pypi:demo-suite")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "demo-suite" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "demo_suite/reploy" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.IsPinned {
		t.Fatalf("unpinned pypi ref should request latest, not be considered pinned")
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
