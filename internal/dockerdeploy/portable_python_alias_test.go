package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type portablePythonAliasFixtureV1 struct {
	Root        string
	Destination string
	Target      string
	Claims      portablePythonAliasClaimsV1
}

func newPortablePythonAliasFixtureV1(t *testing.T) portablePythonAliasFixtureV1 {
	t.Helper()
	root := t.TempDir()
	target := "/opt/reploy/providers/python/application/bin/demo"
	return portablePythonAliasFixtureV1{
		Root: root, Destination: filepath.Join(root, "exports", "demo"), Target: target,
		Claims: portablePythonAliasClaimsV1{},
	}
}

func TestPublishPortablePythonAliasV1PublishesFinalImageTarget(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	published, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err != nil || !published {
		t.Fatalf("publication = %v, %v", published, err)
	}
	link, err := os.Readlink(fixture.Destination)
	if err != nil || normalizePortablePythonAliasTargetV1(link) != fixture.Target {
		t.Fatalf("published link = %q, error=%v; want final-image target %q", link, err, fixture.Target)
	}
	if strings.Contains(link, fixture.Root) {
		t.Fatalf("link payload leaked host staging root: %q", link)
	}
	if got := fixture.Claims[fixture.Destination]; got != (portablePythonAliasClaimV1{Target: fixture.Target, Binding: "binding/demo"}) {
		t.Fatalf("claim = %#v", got)
	}
	info, err := os.Stat(filepath.Dir(fixture.Destination))
	if err != nil {
		t.Fatalf("stat created parent: %v", err)
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o755) {
		t.Fatalf("created parent mode = %v", info.Mode())
	}
}

func TestPublishPortablePythonAliasV1RejectsDestinationEscape(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	outside := filepath.Join(filepath.Dir(fixture.Root), "outside", "demo")
	_, err := publishPortablePythonAliasV1(fixture.Root, outside, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "escapes staging root") {
		t.Fatalf("escape error = %v", err)
	}
	if _, statErr := os.Lstat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("escaped destination changed: %v", statErr)
	}
}

func TestPublishPortablePythonAliasV1RejectsSymlinkedParent(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	elsewhere := filepath.Join(fixture.Root, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(fixture.Root, "exports")
	if err := os.Symlink(elsewhere, linked); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := publishPortablePythonAliasV1(fixture.Root, filepath.Join(linked, "demo"), fixture.Target, "binding/demo", fixture.Claims)
	if err == nil {
		t.Fatalf("symlinked parent error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(elsewhere, "demo")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink target changed: %v", statErr)
	}
}

func TestPublishPortablePythonAliasV1RejectsSymlinkedParentOnExactReplay(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	if _, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims); err != nil {
		t.Fatal(err)
	}
	originalParent := filepath.Dir(fixture.Destination)
	movedParent := filepath.Join(fixture.Root, "moved-exports")
	if err := os.Rename(originalParent, movedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(movedParent, originalParent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("symlinked exact replay parent error = %v", err)
	}
}

func TestPublishPortablePythonAliasV1RejectsExistingUnownedDestination(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	if err := os.MkdirAll(filepath.Dir(fixture.Destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.Destination, []byte("unowned"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "pre-existing and unowned") {
		t.Fatalf("existing destination error = %v", err)
	}
	content, readErr := os.ReadFile(fixture.Destination)
	if readErr != nil || string(content) != "unowned" {
		t.Fatalf("existing destination content = %q, error=%v", content, readErr)
	}
}

func TestPublishPortablePythonAliasV1RejectsCollisionAndDeduplicatesExactClaim(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	published, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err != nil || !published {
		t.Fatalf("first publication = %v, %v", published, err)
	}
	again, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err != nil || again {
		t.Fatalf("exact replay = %v, %v; want deduplicated", again, err)
	}
	otherTarget := "/opt/reploy/providers/python/application/bin/other"
	if _, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, otherTarget, "binding/demo", fixture.Claims); err == nil || !strings.Contains(err.Error(), "different target or binding") {
		t.Fatalf("target collision error = %v", err)
	}
	if _, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/other", fixture.Claims); err == nil || !strings.Contains(err.Error(), "different target or binding") {
		t.Fatalf("binding collision error = %v", err)
	}
}

func TestPublishPortablePythonAliasV1InterruptionLeavesNoUsableAlias(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldPublish := portablePythonAliasPublishTempV1
	portablePythonAliasPublishTempV1 = func(*os.Root, string, string) error {
		return errors.New("injected interruption")
	}
	t.Cleanup(func() { portablePythonAliasPublishTempV1 = oldPublish })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "injected interruption") {
		t.Fatalf("interruption error = %v", err)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("interrupted publication left destination: %v", statErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("interrupted publication recorded a successful claim")
	}
	entries, readErr := os.ReadDir(filepath.Dir(fixture.Destination))
	if readErr == nil && len(entries) != 0 {
		t.Fatalf("interrupted publication left temporary entries: %#v", entries)
	}
}

func TestPublishPortablePythonAliasV1RejectsWrongFinalImageTarget(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	wrongTargets := []string{
		"/opt/reploy/tools/playwright/bin/playwright",
		filepath.Join(fixture.Root, "opt/reploy/providers/python/application/bin/demo"),
		"/opt/reploy/providers/python/application/../bin/demo",
	}
	for _, target := range wrongTargets {
		t.Run(fmt.Sprintf("target-%d", len(target)), func(t *testing.T) {
			_, err := publishPortablePythonAliasV1(fixture.Root, filepath.Join(fixture.Root, "exports", fmt.Sprintf("demo-%d", len(target))), target, "binding/demo", fixture.Claims)
			if err == nil {
				t.Fatalf("wrong target %q was accepted", target)
			}
		})
	}
}
