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

func TestParseGitPackRefWithHTTPSAndSubdir(t *testing.T) {
	ref, err := ParsePackRef("git:https://github.com/omry/reploy.git//deploy/demo.blueprint.yaml?ref=v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "git" {
		t.Fatalf("scheme = %q", ref.Scheme)
	}
	if ref.Source != "https://github.com/omry/reploy.git" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "deploy/demo.blueprint.yaml" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.Query.Get("ref") != "v1.2.3" {
		t.Fatalf("ref query = %q", ref.Query.Get("ref"))
	}
	if !ref.IsPinned {
		t.Fatalf("git ref with ref query should be pinned")
	}
}

func TestParseSaplingPackRefWithRevision(t *testing.T) {
	ref, err := ParsePackRef("sl:https://github.com/omry/reploy.git//deploy/demo.blueprint.yaml?rev=abc123")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "sl" || ref.Query.Get("rev") != "abc123" || !ref.IsPinned {
		t.Fatalf("unexpected ref: %#v", ref)
	}
}

func TestParsePyPIPackRefWithExplicitSubdir(t *testing.T) {
	ref, err := ParsePackRef("pypi:demo-suite==0.1.0//demo_suite/reploy")
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
	ref, err := ParsePackRef("pypi:demo-suite//demo_suite/reploy")
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

func TestParsePackRefRejectsUnsupportedScheme(t *testing.T) {
	_, err := ParsePackRef("oci:example")
	if err == nil {
		t.Fatal("expected error")
	}
}
