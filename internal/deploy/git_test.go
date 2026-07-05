package deploy

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestLoadGitPackResolvesBranchToCachedCommit(t *testing.T) {
	sourceRoot, commit := testGitSourcePack(t)
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("REPLOY_CACHE_DIR", cacheRoot)
	sourceURL := localFileURL(sourceRoot)
	ref, err := ParsePackRef("git:" + sourceURL + "?ref=main")
	if err != nil {
		t.Fatal(err)
	}

	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Ref.Raw != "git:"+sourceURL+"#demo_server/reploy?ref="+commit {
		t.Fatalf("resolved ref = %q", pack.Ref.Raw)
	}
	if !pack.Ref.IsPinned {
		t.Fatalf("resolved git ref should be pinned: %#v", pack.Ref)
	}
	if pack.RequestedRef.Raw != ref.Raw {
		t.Fatalf("requested ref = %q", pack.RequestedRef.Raw)
	}
	if pack.ResolvedArtifact == nil {
		t.Fatal("missing resolved artifact")
	}
	if pack.ResolvedArtifact.Scheme != "git" || pack.ResolvedArtifact.Package != sourceURL || pack.ResolvedArtifact.Version != commit {
		t.Fatalf("artifact = %#v", pack.ResolvedArtifact)
	}
	if !strings.HasPrefix(pack.ResolvedArtifact.CachePath, cacheRoot) {
		t.Fatalf("cache path = %q, want under %q", pack.ResolvedArtifact.CachePath, cacheRoot)
	}
	if pack.App.Provider.LocalSources["demo-server"] != "../.." {
		t.Fatalf("local sources = %#v", pack.App.Provider.LocalSources)
	}

	if err := os.RemoveAll(sourceRoot); err != nil {
		t.Fatal(err)
	}
	cachedPack, err := LoadResolvedPack(pack.Ref, pack.RequestedRef.Raw, pack.ResolvedArtifact)
	if err != nil {
		t.Fatal(err)
	}
	if cachedPack.App.Provider.Identifier != "demo-server" {
		t.Fatalf("provider identifier = %q", cachedPack.App.Provider.Identifier)
	}
}

func localFileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(slashed) >= 2 && slashed[1] == ':' {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func TestValidateGitPackRefUsesGitHubFacingErrors(t *testing.T) {
	err := validateGitPackRef(PackRef{
		Raw:    "github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml?ref=main",
		Scheme: "git",
		Source: "ftp://github.com/acme/demo.git",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "github blueprint source must use https, ssh, or file") {
		t.Fatalf("err = %v", err)
	}
	for _, leaked := range []string{
		"git:",
		"ftp://github.com/acme/demo.git",
		"git blueprint",
	} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error exposed internal git representation %q: %v", leaked, err)
		}
	}
}

func TestValidateGitPackRefUsesGitHubFacingErrorsForShorthand(t *testing.T) {
	err := validateGitPackRef(PackRef{
		Raw:    "demo-server",
		Scheme: "git",
		Source: "https://github.com/acme/demo.git",
		Query:  url.Values{"path": []string{"demo_pkg/reploy/demo.blueprint.yaml"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported github blueprint query parameter: path") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "git blueprint") || strings.Contains(err.Error(), "git:") {
		t.Fatalf("error exposed internal git representation: %v", err)
	}
}

func testGitSourcePack(t *testing.T) (string, string) {
	t.Helper()
	sourceRoot := t.TempDir()
	repository, err := git.PlainInit(sourceRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "pyproject.toml"), []byte("[project]\nname = \"demo-server\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blueprintDir := filepath.Join(sourceRoot, "demo_server", "reploy")
	if err := os.MkdirAll(blueprintDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(simplePackTestManifest(), "identifier: demo\n", "identifier: demo-server\n", 1)
	if err := os.WriteFile(filepath.Join(blueprintDir, "demo.blueprint.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"pyproject.toml",
		filepath.ToSlash(filepath.Join("demo_server", "reploy", "demo.blueprint.yaml")),
	} {
		if _, err := worktree.Add(path); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := worktree.Commit("add demo source pack", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Reploy Test",
			Email: "test@example.com",
			When:  time.Unix(1, 0),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sourceRoot, hash.String()
}
