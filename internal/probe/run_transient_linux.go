//go:build linux

package probe

import (
	"fmt"
	"os"
	"syscall"
)

func runTransientProcess(home string, uid int, gid int, command []string) error {
	info, err := os.Lstat(home)
	if err != nil {
		return fmt.Errorf("inspect transient home: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("transient home must be a real directory")
	}
	if err := os.Chown(home, uid, gid); err != nil {
		return fmt.Errorf("own transient home for %d:%d: %w", uid, gid, err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return fmt.Errorf("protect transient home: %w", err)
	}
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("clear supplementary groups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("select transient GID %d: %w", gid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("select transient UID %d: %w", uid, err)
	}
	if err := syscall.Exec(command[0], command, os.Environ()); err != nil {
		return fmt.Errorf("execute transient command %s: %w", command[0], err)
	}
	return nil
}
