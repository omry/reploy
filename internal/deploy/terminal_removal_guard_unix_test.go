//go:build !windows

package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalGuardRejectsMovedLockAndWaiterV1(t *testing.T) {
	for _, moved := range []string{"lock-file", "deployment-directory"} {
		t.Run(moved, func(t *testing.T) {
			dir, lock, state := terminalGuardFixtureV1(t)
			// Open the waiter before isolation and prove it is waiting on the same
			// kernel object, then replace its original path before it gains ownership.
			file, err := os.OpenFile(lock.Path(), os.O_RDWR, 0)
			terminalGuardMustV1(t, err)
			defer file.Close()
			acquired, err := tryLockOperationFile(file)
			if err != nil || acquired {
				t.Fatalf("waiter contention=%v err=%v", acquired, err)
			}
			if moved == "lock-file" {
				terminalGuardMustV1(t, os.Rename(lock.Path(), lock.Path()+".moved"))
			} else {
				terminalGuardMustV1(t, os.Rename(dir, dir+".moved"))
				defer os.RemoveAll(dir + ".moved")
				terminalGuardMustV1(t, os.MkdirAll(filepath.Dir(lock.Path()), 0o755))
			}
			terminalGuardMustV1(t, os.WriteFile(lock.Path(), nil, 0o600))
			if err := lock.RequireHeld(); err == nil {
				t.Fatal("held descriptor accepted replacement")
			}
			if err := lock.BeginTerminalRemovalV1(state); err == nil {
				t.Fatal("guard committed through moved descriptor")
			}
			terminalGuardMustV1(t, lock.Unlock())
			acquired, err = tryLockOperationFile(file)
			if err != nil || !acquired {
				t.Fatalf("released waiter=%v err=%v", acquired, err)
			}
			waiter := &OperationLock{file: file, path: lock.Path(), directory: lock.directory, stateDirectory: lock.stateDirectory}
			defer waiter.Unlock()
			if err := waiter.RequireWritable(); err == nil {
				t.Fatal("waiter admitted replacement directory")
			}
			if err := waiter.BeginTerminalRemovalV1(state); err == nil {
				t.Fatal("waiter committed replacement guard")
			}
		})
	}
}
