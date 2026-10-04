package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsIsolationMovesCapturedOriginalV1(t *testing.T) {
	dir, lock, state := terminalGuardFixtureV1(t)
	terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
	original, err := os.Stat(dir)
	terminalGuardMustV1(t, err)
	destination, parked := dir+"-isolated", dir+"-parked"
	ops := defaultDirectoryIsolationWindowsOps()
	var replacement os.FileInfo
	ops.unlock = func(operation *OperationLock) error {
		if err := operation.unlockLocked(); err != nil {
			return err
		}
		// Move the real original after release, then recreate its former path.
		// A path-based rename would now move the replacement instead.
		terminalGuardMustV1(t, os.Rename(dir, parked))
		terminalGuardMustV1(t, os.Mkdir(dir, 0o700))
		terminalGuardMustV1(t, os.WriteFile(filepath.Join(dir, "replacement"), []byte("preserve"), 0o600))
		replacement, err = os.Stat(dir)
		terminalGuardMustV1(t, err)
		return nil
	}
	moved, err := func() (bool, error) {
		lock.mutex.Lock()
		defer lock.mutex.Unlock()
		return isolateOriginalDirectoryWindowsLocked(lock, dir, destination, ops)
	}()
	if err != nil || !moved {
		t.Fatalf("captured isolation moved=%v err=%v", moved, err)
	}
	isolated, err := os.Stat(destination)
	terminalGuardMustV1(t, err)
	current, err := os.Stat(dir)
	terminalGuardMustV1(t, err)
	if !os.SameFile(original, isolated) || !os.SameFile(replacement, current) {
		t.Fatal("captured isolation substituted or deleted the recreated path")
	}
	if _, err := os.Stat(parked); !os.IsNotExist(err) {
		t.Fatalf("original object still parked: %v", err)
	}
}

func TestWindowsIsolationFaultsCloseCapturedHandleV1(t *testing.T) {
	for _, phase := range []string{"capture", "identity", "unlock", "rename", "close"} {
		t.Run(phase, func(t *testing.T) {
			dir, lock, state := terminalGuardFixtureV1(t)
			terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
			destination := dir + "-isolated"
			ops := defaultDirectoryIsolationWindowsOps()
			fault := errors.New("injected " + phase)
			closes := 0
			ops.close = func(file *os.File) error {
				closes++
				closeErr := file.Close()
				if phase == "close" {
					return errors.Join(closeErr, fault)
				}
				return closeErr
			}
			switch phase {
			case "capture":
				ops.open = func(string) (*os.File, error) { return nil, fault }
			case "identity":
				foreign := t.TempDir()
				ops.open = func(string) (*os.File, error) { return openIsolationDirectoryWindows(foreign) }
			case "unlock":
				ops.unlock = func(*OperationLock) error { return fault }
			case "rename":
				ops.rename = func(*os.File, string) error { return fault }
			}
			moved, err := func() (bool, error) {
				lock.mutex.Lock()
				defer lock.mutex.Unlock()
				return isolateOriginalDirectoryWindowsLocked(lock, dir, destination, ops)
			}()
			if err == nil || moved != (phase == "close") {
				t.Fatalf("phase %s moved=%v err=%v", phase, moved, err)
			}
			wantCloses := 1
			if phase == "capture" {
				wantCloses = 0
			}
			if closes != wantCloses {
				t.Fatalf("closed handles=%d want=%d", closes, wantCloses)
			}
			retained := dir
			if moved {
				retained = destination
			}
			if _, found, err := readStateV1Path(filepath.Join(retained, ".reploy", stateFilenameV1)); err != nil || !found {
				t.Fatalf("fault lost retained authority: %v", err)
			}
		})
	}
}

func TestWindowsIsolationRejectsCapturedReparsePointV1(t *testing.T) {
	dir, lock, state := terminalGuardFixtureV1(t)
	terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
	link := dir + "-link"
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("host cannot create directory symlink: %v", err)
	}
	ops := defaultDirectoryIsolationWindowsOps()
	ops.open = func(string) (*os.File, error) { return openIsolationDirectoryWindows(link) }
	// Even a substitution at capture cannot grant rename authority to a link.
	moved, err := func() (bool, error) {
		lock.mutex.Lock()
		defer lock.mutex.Unlock()
		return isolateOriginalDirectoryWindowsLocked(lock, dir, dir+"-isolated", ops)
	}()
	if moved || err == nil {
		t.Fatal("captured reparse point admitted isolation")
	}
	terminalGuardMustV1(t, lock.RequireRetirement())
}

func TestWindowsIsolationLongPathsV1(t *testing.T) {
	root := t.TempDir()
	dir := root
	for len(dir) < 300 {
		dir = filepath.Join(dir, strings.Repeat("x", 60))
	}
	terminalGuardMustV1(t, os.MkdirAll(dir, 0o700))
	lock, err := AcquireOperationLock(t.Context(), dir)
	terminalGuardMustV1(t, err)
	defer lock.Unlock()
	state := StateV1{Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t), Overlay: EmptyRequestOverlayV1(), BlueprintSource: "fixture", Staging: &StagingStateV1{Schema: StagingStateSchemaV1}}
	terminalGuardMustV1(t, lock.CommitStateV1(nil, state))
	terminalGuardMustV1(t, lock.BeginTerminalRemovalV1(state))
	if moved, err := lock.IsolateOriginalDirectory(dir + "-isolated"); err != nil || !moved {
		t.Fatalf("long-path isolation moved=%v err=%v", moved, err)
	}
}
