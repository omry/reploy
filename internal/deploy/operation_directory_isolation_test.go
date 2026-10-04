package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsolateOriginalDirectoryV1(t *testing.T) {
	dir, lock, state := terminalGuardFixtureV1(t)
	terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
	original, err := os.Stat(dir)
	terminalGuardMustV1(t, err)
	destination := dir + "-isolated"
	moved, err := lock.IsolateOriginalDirectory(destination)
	if err != nil || !moved {
		t.Fatalf("isolation moved=%v err=%v", moved, err)
	}
	isolated, err := os.Stat(destination)
	terminalGuardMustV1(t, err)
	if !os.SameFile(original, isolated) {
		t.Fatal("isolation substituted the original directory")
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("original path remains: %v", err)
	}
	if _, found, err := readStateV1Path(filepath.Join(destination, ".reploy", stateFilenameV1)); err != nil || !found {
		t.Fatalf("retained state erased: found=%v err=%v", found, err)
	}
	// Observe the actual kernel lock through the moved lock file. Unix must
	// retain exclusion across rename; Windows must have released before it.
	file, err := os.OpenFile(filepath.Join(destination, ".reploy", "operation.lock"), os.O_RDWR, 0)
	terminalGuardMustV1(t, err)
	defer file.Close()
	acquired, err := tryLockOperationFile(file)
	terminalGuardMustV1(t, err)
	if acquired != (runtime.GOOS == "windows") {
		t.Fatalf("kernel lock acquisition after rename=%v on %s", acquired, runtime.GOOS)
	}
	if acquired {
		terminalGuardMustV1(t, unlockOperationFile(file))
	}
	terminalGuardMustV1(t, lock.Unlock())
}

func TestIsolateOriginalDirectoryRequiresBoundAuthorityV1(t *testing.T) {
	var missing *OperationLock
	if moved, err := missing.IsolateOriginalDirectory("unused"); moved || err == nil {
		t.Fatal("nil operation admitted isolation")
	}
	dir, lock, state := terminalGuardFixtureV1(t)
	destination := dir + "-isolated"
	if moved, err := lock.IsolateOriginalDirectory(destination); moved || err == nil {
		t.Fatal("ordinary operation admitted isolation")
	}
	terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
	terminalGuardMustV1(t, lock.Unlock())
	diagnostic, err := AcquireExistingOperationLock(t.Context(), dir)
	terminalGuardMustV1(t, err)
	defer diagnostic.Unlock()
	if moved, err := diagnostic.IsolateOriginalDirectory(destination); moved || err == nil {
		t.Fatal("diagnostic operation admitted isolation")
	}
	guarded, found, err := diagnostic.ReadStateV1()
	if err != nil || !found {
		t.Fatalf("read retained authority: %v", err)
	}
	terminalGuardMustV1(t, diagnostic.ResumeTerminalRemovalV1(guarded))
	guarded.BlueprintSource = "substituted"
	content, err := EncodeStateV1(guarded)
	terminalGuardMustV1(t, err)
	terminalGuardMustV1(t, os.WriteFile(filepath.Join(dir, ".reploy", stateFilenameV1), content, 0o600))
	if moved, err := diagnostic.IsolateOriginalDirectory(destination); moved || err == nil {
		t.Fatal("changed retained authority admitted isolation")
	}
}

func TestIsolateOriginalDirectoryPreservesOccupiedDestinationV1(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir, lock, state := terminalGuardFixtureV1(t)
			terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
			destination := dir + "-isolated"
			switch kind {
			case "directory":
				terminalGuardMustV1(t, os.Mkdir(destination, 0o700))
			case "file":
				terminalGuardMustV1(t, os.WriteFile(destination, []byte("retain"), 0o600))
			case "symlink":
				if err := os.Symlink(dir, destination); err != nil {
					t.Skipf("host cannot create symlink: %v", err)
				}
			}
			before, err := os.Lstat(destination)
			terminalGuardMustV1(t, err)
			if moved, err := lock.IsolateOriginalDirectory(destination); moved || err == nil {
				t.Fatalf("occupied destination moved=%v err=%v", moved, err)
			}
			after, err := os.Lstat(destination)
			terminalGuardMustV1(t, err)
			if !os.SameFile(before, after) {
				t.Fatal("occupied destination replaced")
			}
			if _, found, err := readStateV1Path(filepath.Join(dir, ".reploy", stateFilenameV1)); err != nil || !found {
				t.Fatalf("failed rename erased original authority: %v", err)
			}
		})
	}
}

func TestIsolateOriginalDirectoryDestinationBoundaryV1(t *testing.T) {
	dir, lock, state := terminalGuardFixtureV1(t)
	terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
	for _, destination := range []string{"", dir, filepath.Join(dir, "child"), filepath.Join(t.TempDir(), "foreign")} {
		if moved, err := lock.IsolateOriginalDirectory(destination); moved || err == nil {
			t.Fatalf("invalid destination %q moved=%v err=%v", destination, moved, err)
		}
	}
	terminalGuardMustV1(t, lock.RequireRetirement())
}

func TestIsolateOriginalDirectoryRejectsReplacedSourceV1(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows replacement across release uses the captured-handle test")
	}
	dir, lock, state := terminalGuardFixtureV1(t)
	terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
	terminalGuardMustV1(t, os.Rename(dir, dir+"-parked"))
	terminalGuardMustV1(t, os.Mkdir(dir, 0o700))
	replacement, err := os.Stat(dir)
	terminalGuardMustV1(t, err)
	if moved, err := lock.IsolateOriginalDirectory(dir + "-isolated"); moved || err == nil {
		t.Fatal("stale lock isolated a replacement directory")
	}
	current, err := os.Stat(dir)
	terminalGuardMustV1(t, err)
	if !os.SameFile(replacement, current) {
		t.Fatal("replacement directory changed")
	}
}
