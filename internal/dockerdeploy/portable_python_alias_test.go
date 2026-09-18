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
	link, err := readPortablePythonAliasStagedEntryV1(fixture.Destination)
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

func TestPublishPortablePythonAliasV1RejectsParentSwappedAfterOpen(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	originalParent := filepath.Dir(fixture.Destination)
	movedParent := filepath.Join(fixture.Root, "moved-exports")
	elsewhere := filepath.Join(fixture.Root, "elsewhere")
	if err := os.MkdirAll(originalParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}

	oldOpen := portablePythonAliasOpenRootV1
	swapped := false
	var swapErr error
	portablePythonAliasOpenRootV1 = func(parent *os.Root, name string) (*os.Root, error) {
		child, err := parent.OpenRoot(name)
		if err != nil || name != filepath.Base(originalParent) || swapped {
			return child, err
		}
		swapped = true
		if err := os.Rename(originalParent, movedParent); err != nil {
			swapErr = err
			_ = child.Close()
			return nil, err
		}
		if err := os.Symlink(elsewhere, originalParent); err != nil {
			swapErr = err
			_ = child.Close()
			return nil, err
		}
		return child, nil
	}
	t.Cleanup(func() { portablePythonAliasOpenRootV1 = oldOpen })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if swapErr != nil {
		t.Skipf("cannot replace opened parent on this host: %v", swapErr)
	}
	if err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("parent swap error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(elsewhere, filepath.Base(fixture.Destination))); !os.IsNotExist(statErr) {
		t.Fatalf("parent swap redirected publication: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(movedParent, filepath.Base(fixture.Destination))); !os.IsNotExist(statErr) {
		t.Fatalf("parent swap changed held original directory: %v", statErr)
	}
}

func TestPublishPortablePythonAliasV1DoesNotReplaceRacingParent(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skipf("no-replace publication is unsupported on %s", runtime.GOOS)
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	oldPublish := portablePythonAliasPublishParentV1
	injected := false
	portablePythonAliasPublishParentV1 = func(parent *os.Root, temporary, destination string) error {
		injected = true
		if err := parent.Mkdir(destination, 0o755); err != nil {
			return err
		}
		return publishPortablePythonAliasNoReplaceV1(parent, temporary, destination)
	}
	t.Cleanup(func() { portablePythonAliasPublishParentV1 = oldPublish })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !injected || err == nil || !strings.Contains(err.Error(), "without replacement") {
		t.Fatalf("racing parent publication = injected %v, error %v", injected, err)
	}
	parent := filepath.Dir(fixture.Destination)
	info, statErr := os.Lstat(parent)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("racing parent was replaced or removed: info=%v, error=%v", info, statErr)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("racing parent received an alias: %v", statErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("racing parent recorded a successful claim")
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

func TestPublishPortablePythonAliasV1DestinationRacePreservesExistingEntry(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skipf("no-replace publication is unsupported on %s", runtime.GOOS)
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	oldPublish := portablePythonAliasPublishTempV1
	injected := false
	portablePythonAliasPublishTempV1 = func(parent *os.Root, temporary, destination string) error {
		injected = true
		file, err := parent.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := file.WriteString("racer"); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		return publishPortablePythonAliasNoReplaceV1(parent, temporary, destination)
	}
	t.Cleanup(func() { portablePythonAliasPublishTempV1 = oldPublish })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !injected {
		t.Fatal("destination race seam was not called")
	}
	if err == nil || !strings.Contains(err.Error(), "without replacement") {
		t.Fatalf("destination race error = %v", err)
	}
	content, readErr := os.ReadFile(fixture.Destination)
	if readErr != nil || string(content) != "racer" {
		t.Fatalf("destination race changed existing entry = %q, error=%v", content, readErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("destination race recorded a successful claim")
	}
}

func TestPublishPortablePythonAliasV1PostPublicationReadFailureRemovesDestination(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldRead := portablePythonAliasReadEntryV1
	readErr := errors.New("injected published alias read failure")
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			return "", readErr
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !errors.Is(err, readErr) || !strings.Contains(err.Error(), "resolve published Python alias") {
		t.Fatalf("post-publication read error = %v", err)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("post-publication read failure left destination: %v", statErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("post-publication read failure recorded a successful claim")
	}
}

func TestPublishPortablePythonAliasV1PreservesReplacementAfterReadFailure(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldRead := portablePythonAliasReadEntryV1
	readErr := errors.New("injected published alias read failure")
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			if err := os.Rename(fixture.Destination, fixture.Destination+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.Destination, []byte("unowned replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			return "", readErr
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !errors.Is(err, readErr) || !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("replacement cleanup error = %v", err)
	}
	content, readFileErr := os.ReadFile(fixture.Destination)
	if readFileErr != nil || string(content) != "unowned replacement" {
		t.Fatalf("unowned replacement = %q, error %v", content, readFileErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("failed publication recorded a claim")
	}
}

func TestPublishPortablePythonAliasV1PreservesReplacementParentOnRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	oldPublish := portablePythonAliasPublishTempV1
	publicationErr := errors.New("injected alias publication failure")
	parentPath := filepath.Dir(fixture.Destination)
	movedPath := parentPath + "-moved"
	portablePythonAliasPublishTempV1 = func(parent *os.Root, temporary, destination string) error {
		if err := os.Rename(parentPath, movedPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(parentPath, 0o755); err != nil {
			t.Fatal(err)
		}
		return publicationErr
	}
	t.Cleanup(func() { portablePythonAliasPublishTempV1 = oldPublish })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !errors.Is(err, publicationErr) || !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("parent rollback error = %v", err)
	}
	if info, statErr := os.Lstat(parentPath); statErr != nil || !info.IsDir() {
		t.Fatalf("replacement parent was removed: info=%v, error=%v", info, statErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("failed publication recorded a claim")
	}
}

func TestPublishPortablePythonAliasV1RejectsRenamedDestinationParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	oldRead := portablePythonAliasReadEntryV1
	parentPath := filepath.Dir(fixture.Destination)
	movedPath := parentPath + "-moved"
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			target, err := oldRead(parent, name)
			if err != nil {
				return target, err
			}
			if err := os.Rename(parentPath, movedPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(parentPath, 0o755); err != nil {
				t.Fatal(err)
			}
			return target, nil
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "destination parent changed identity") {
		t.Fatalf("renamed destination parent error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(movedPath, filepath.Base(fixture.Destination))); !os.IsNotExist(statErr) {
		t.Fatalf("renamed parent retained the alias after failure: %v", statErr)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("replacement parent received the alias: %v", statErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("renamed destination parent recorded a claim")
	}
}

func TestPublishPortablePythonAliasV1RejectsReplacedPublishedEntry(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldRead := portablePythonAliasReadEntryV1
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			target, err := oldRead(parent, name)
			if err != nil {
				return target, err
			}
			if err := os.Rename(fixture.Destination, fixture.Destination+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.Destination, []byte("unowned replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			return target, nil
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "published destination changed identity") {
		t.Fatalf("replaced published entry error = %v", err)
	}
	content, readErr := os.ReadFile(fixture.Destination)
	if readErr != nil || string(content) != "unowned replacement" {
		t.Fatalf("unowned replacement = %q, error %v", content, readErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("replaced published entry recorded a claim")
	}
}

func TestPublishPortablePythonAliasV1RejectsRenamedStagingRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	oldRead := portablePythonAliasReadEntryV1
	movedRoot := fixture.Root + "-moved"
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			target, err := oldRead(parent, name)
			if err != nil {
				return target, err
			}
			if err := os.Rename(fixture.Root, movedRoot); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(fixture.Root, 0o755); err != nil {
				t.Fatal(err)
			}
			return target, nil
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "staging root changed identity") {
		t.Fatalf("renamed staging root error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(movedRoot, "exports", filepath.Base(fixture.Destination))); !os.IsNotExist(statErr) {
		t.Fatalf("renamed staging root retained alias: %v", statErr)
	}
	if _, found := fixture.Claims[fixture.Destination]; found {
		t.Fatal("renamed staging root recorded a claim")
	}
}

func TestPublishPortablePythonAliasV1RejectsReplayAfterParentRename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	if _, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims); err != nil {
		t.Fatal(err)
	}
	oldRead := portablePythonAliasReadEntryV1
	parentPath := filepath.Dir(fixture.Destination)
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			target, err := oldRead(parent, name)
			if err != nil {
				return target, err
			}
			if err := os.Rename(parentPath, parentPath+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(parentPath, 0o755); err != nil {
				t.Fatal(err)
			}
			return target, nil
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "destination parent changed identity") {
		t.Fatalf("replay after parent rename error = %v", err)
	}
}

func TestPublishPortablePythonAliasV1RejectsReplayAfterRootRename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	if _, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims); err != nil {
		t.Fatal(err)
	}
	oldRead := portablePythonAliasReadEntryV1
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			target, err := oldRead(parent, name)
			if err != nil {
				return target, err
			}
			if err := os.Rename(fixture.Root, fixture.Root+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(fixture.Root, 0o755); err != nil {
				t.Fatal(err)
			}
			return target, nil
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "staging root changed identity") {
		t.Fatalf("replay after staging root rename error = %v", err)
	}
}

func TestPublishPortablePythonAliasV1RejectsReplacedExactReplay(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	if _, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims); err != nil {
		t.Fatal(err)
	}
	oldRead := portablePythonAliasReadEntryV1
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			target, err := oldRead(parent, name)
			if err != nil {
				return target, err
			}
			if err := os.Rename(fixture.Destination, fixture.Destination+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.Destination, []byte("unowned replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			return target, nil
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !strings.Contains(err.Error(), "changed identity during replay") {
		t.Fatalf("replaced exact replay error = %v", err)
	}
	content, readErr := os.ReadFile(fixture.Destination)
	if readErr != nil || string(content) != "unowned replacement" {
		t.Fatalf("unowned replacement = %q, error %v", content, readErr)
	}
}

func TestPublishPortablePythonAliasV1RejectsReplacedIntermediateAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	fixture.Destination = filepath.Join(fixture.Root, "exports", "nested", "demo")
	if _, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims); err != nil {
		t.Fatal(err)
	}
	oldOpen := portablePythonAliasOpenRootV1
	ancestorPath := filepath.Join(fixture.Root, "exports")
	injected := false
	portablePythonAliasOpenRootV1 = func(parent *os.Root, name string) (*os.Root, error) {
		child, err := oldOpen(parent, name)
		if err == nil && name == "nested" && !injected {
			injected = true
			if renameErr := os.Rename(ancestorPath, ancestorPath+"-moved"); renameErr != nil {
				t.Fatal(renameErr)
			}
			if mkdirErr := os.MkdirAll(filepath.Join(ancestorPath, "nested"), 0o755); mkdirErr != nil {
				t.Fatal(mkdirErr)
			}
		}
		return child, err
	}
	t.Cleanup(func() { portablePythonAliasOpenRootV1 = oldOpen })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !injected || err == nil || !strings.Contains(err.Error(), "parent component \"exports\" changed identity") {
		t.Fatalf("intermediate parent swap = injected %v, error %v", injected, err)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("replacement ancestor received alias: %v", statErr)
	}
}

func TestPublishPortablePythonAliasV1PreservesReplacementPrivateParentOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	fixture := newPortablePythonAliasFixtureV1(t)
	oldPublish := portablePythonAliasPublishParentV1
	publicationErr := errors.New("injected private parent publication failure")
	var temporaryPath string
	portablePythonAliasPublishParentV1 = func(parent *os.Root, temporary, destination string) error {
		temporaryPath = filepath.Join(fixture.Root, temporary)
		if err := os.Rename(temporaryPath, temporaryPath+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(temporaryPath, 0o755); err != nil {
			t.Fatal(err)
		}
		return publicationErr
	}
	t.Cleanup(func() { portablePythonAliasPublishParentV1 = oldPublish })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !errors.Is(err, publicationErr) || !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("private parent replacement error = %v", err)
	}
	if info, statErr := os.Lstat(temporaryPath); statErr != nil || !info.IsDir() {
		t.Fatalf("replacement private parent was removed: info=%v, error=%v", info, statErr)
	}
}

func TestPublishPortablePythonAliasV1CleansUpAfterCreatedTemporaryInspectionFailure(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldInspect := portablePythonAliasInspectCreatedTempV1
	inspectErr := errors.New("injected temporary inspection failure")
	var temporaryPath string
	portablePythonAliasInspectCreatedTempV1 = func(parent *os.Root, name string) (os.FileInfo, error) {
		temporaryPath = filepath.Join(filepath.Dir(fixture.Destination), name)
		info, err := oldInspect(parent, name)
		if err != nil {
			return nil, err
		}
		return info, inspectErr
	}
	t.Cleanup(func() { portablePythonAliasInspectCreatedTempV1 = oldInspect })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !errors.Is(err, inspectErr) {
		t.Fatalf("temporary inspection error = %v", err)
	}
	if _, statErr := os.Lstat(temporaryPath); !os.IsNotExist(statErr) {
		t.Fatalf("temporary alias remained after inspection failure: %v", statErr)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination appeared after inspection failure: %v", statErr)
	}
}

func TestPublishPortablePythonAliasV1PreservesUnknownTemporaryAfterInspectionFailure(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldInspect := portablePythonAliasInspectCreatedTempV1
	inspectErr := errors.New("injected temporary inspection failure")
	var temporaryPath string
	portablePythonAliasInspectCreatedTempV1 = func(parent *os.Root, name string) (os.FileInfo, error) {
		temporaryPath = filepath.Join(filepath.Dir(fixture.Destination), name)
		if err := os.Rename(temporaryPath, temporaryPath+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(temporaryPath, []byte("unowned replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		return nil, inspectErr
	}
	t.Cleanup(func() { portablePythonAliasInspectCreatedTempV1 = oldInspect })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !errors.Is(err, inspectErr) || !strings.Contains(err.Error(), "ownership identity unavailable") {
		t.Fatalf("unknown temporary inspection error = %v", err)
	}
	content, readErr := os.ReadFile(temporaryPath)
	if readErr != nil || string(content) != "unowned replacement" {
		t.Fatalf("unowned temporary replacement = %q, error %v", content, readErr)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination appeared after inspection failure: %v", statErr)
	}
}

func TestPublishPortablePythonAliasV1ReportsUnknownTemporaryAfterInspectionFailure(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldInspect := portablePythonAliasInspectCreatedTempV1
	inspectErr := errors.New("injected temporary inspection failure")
	var temporaryPath string
	portablePythonAliasInspectCreatedTempV1 = func(parent *os.Root, name string) (os.FileInfo, error) {
		temporaryPath = filepath.Join(filepath.Dir(fixture.Destination), name)
		return nil, inspectErr
	}
	t.Cleanup(func() { portablePythonAliasInspectCreatedTempV1 = oldInspect })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !errors.Is(err, inspectErr) || !strings.Contains(err.Error(), "ownership identity unavailable") {
		t.Fatalf("unknown temporary inspection error = %v", err)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination appeared after inspection failure: %v", statErr)
	}
	if _, statErr := os.Lstat(temporaryPath); statErr != nil {
		t.Fatalf("unknown temporary entry should remain for workspace cleanup: %v", statErr)
	}
}

func TestPublishPortablePythonAliasV1CleansUpAfterPublishedParentInspectionFailure(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldInspect := portablePythonAliasInspectPublishedParentV1
	inspectErr := errors.New("injected published parent inspection failure")
	portablePythonAliasInspectPublishedParentV1 = func(parent *os.Root, name string) (os.FileInfo, error) {
		return nil, inspectErr
	}
	t.Cleanup(func() { portablePythonAliasInspectPublishedParentV1 = oldInspect })
	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !errors.Is(err, inspectErr) {
		t.Fatalf("published parent inspection error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Dir(fixture.Destination)); !os.IsNotExist(statErr) {
		t.Fatalf("published parent remained after inspection failure: %v", statErr)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination appeared after parent inspection failure: %v", statErr)
	}
}

func TestPublishPortablePythonAliasV1PostPublicationReadFailureReportsDestinationRemovalFailure(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldRead := portablePythonAliasReadEntryV1
	oldRemove := portablePythonAliasRemoveV1
	readErr := errors.New("injected published alias read failure")
	removeErr := errors.New("injected published alias removal failure")
	removeCalls := 0
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if name == filepath.Base(fixture.Destination) {
			return "", readErr
		}
		return oldRead(parent, name)
	}
	portablePythonAliasRemoveV1 = func(parent *os.Root, name string) error {
		if name == filepath.Base(fixture.Destination) {
			removeCalls++
			return removeErr
		}
		return oldRemove(parent, name)
	}
	t.Cleanup(func() {
		portablePythonAliasReadEntryV1 = oldRead
		portablePythonAliasRemoveV1 = oldRemove
	})

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if err == nil || !errors.Is(err, readErr) || !errors.Is(err, removeErr) || !strings.Contains(err.Error(), "remove published Python alias destination") {
		t.Fatalf("read and destination removal errors = %v", err)
	}
	if removeCalls != 1 {
		t.Fatalf("destination removal calls = %d, want one best-effort removal", removeCalls)
	}
	if _, statErr := os.Lstat(fixture.Destination); statErr != nil {
		t.Fatalf("destination disappeared despite injected removal failure: %v", statErr)
	}
	linkTarget, linkErr := readPortablePythonAliasStagedEntryV1(fixture.Destination)
	if linkErr != nil || normalizePortablePythonAliasTargetV1(linkTarget) != fixture.Target {
		t.Fatalf("remaining destination = %q, error=%v; want the published alias for injected failure", linkTarget, linkErr)
	}
}

func TestPublishPortablePythonAliasV1PreservesReplacementTemporaryEntry(t *testing.T) {
	fixture := newPortablePythonAliasFixtureV1(t)
	oldRead := portablePythonAliasReadEntryV1
	readErr := errors.New("injected temporary alias read failure")
	var temporaryPath string
	portablePythonAliasReadEntryV1 = func(parent *os.Root, name string) (string, error) {
		if strings.HasPrefix(name, ".reploy-python-alias-") {
			temporaryPath = filepath.Join(filepath.Dir(fixture.Destination), name)
			if err := os.Rename(temporaryPath, temporaryPath+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(temporaryPath, []byte("unowned replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			return "", readErr
		}
		return oldRead(parent, name)
	}
	t.Cleanup(func() { portablePythonAliasReadEntryV1 = oldRead })

	_, err := publishPortablePythonAliasV1(fixture.Root, fixture.Destination, fixture.Target, "binding/demo", fixture.Claims)
	if !errors.Is(err, readErr) || !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("temporary replacement error = %v", err)
	}
	content, readFileErr := os.ReadFile(temporaryPath)
	if readFileErr != nil || string(content) != "unowned replacement" {
		t.Fatalf("unowned temporary replacement = %q, error %v", content, readFileErr)
	}
	if _, statErr := os.Lstat(fixture.Destination); !os.IsNotExist(statErr) {
		t.Fatalf("failed publication created a destination: %v", statErr)
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
