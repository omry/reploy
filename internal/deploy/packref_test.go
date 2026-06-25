package deploy

import "testing"

func TestParseFilePackRef(t *testing.T) {
	ref, err := ParsePackRef("file:./arbiter.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "file" || ref.Source != "./arbiter.blueprint.yaml" || ref.Subdir != "" {
		t.Fatalf("unexpected ref: %#v", ref)
	}
	if ref.IsPinned {
		t.Fatalf("file refs should not be considered reproducibly pinned")
	}
}

func TestParseGitPackRefWithHTTPSAndSubdir(t *testing.T) {
	ref, err := ParsePackRef("git:https://github.com/omry/reploy.git//deploy/arbiter.blueprint.yaml?ref=v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "git" {
		t.Fatalf("scheme = %q", ref.Scheme)
	}
	if ref.Source != "https://github.com/omry/reploy.git" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "deploy/arbiter.blueprint.yaml" {
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
	ref, err := ParsePackRef("sl:https://github.com/omry/reploy.git//deploy/arbiter.blueprint.yaml?rev=abc123")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "sl" || ref.Query.Get("rev") != "abc123" || !ref.IsPinned {
		t.Fatalf("unexpected ref: %#v", ref)
	}
}

func TestParsePyPIPackRefRequiresSubdir(t *testing.T) {
	ref, err := ParsePackRef("pypi:arbiter-suite==0.1.0//arbiter_suite/reploy")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "arbiter-suite==0.1.0" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "arbiter_suite/reploy" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if !ref.IsPinned {
		t.Fatalf("pypi exact package ref should be pinned")
	}
}

func TestParsePyPIPackRefAllowsLatest(t *testing.T) {
	ref, err := ParsePackRef("pypi:arbiter-suite//arbiter_suite/reploy")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != "arbiter-suite" {
		t.Fatalf("source = %q", ref.Source)
	}
	if ref.Subdir != "arbiter_suite/reploy" {
		t.Fatalf("subdir = %q", ref.Subdir)
	}
	if ref.IsPinned {
		t.Fatalf("unpinned pypi ref should request latest, not be considered pinned")
	}
}

func TestParsePyPIPackRefRejectsMissingSubdir(t *testing.T) {
	_, err := ParsePackRef("pypi:arbiter-suite==0.1.0")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParsePackRefRejectsUnsupportedScheme(t *testing.T) {
	_, err := ParsePackRef("oci:example")
	if err == nil {
		t.Fatal("expected error")
	}
}
