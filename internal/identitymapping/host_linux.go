//go:build linux

package identitymapping

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/omry/reploy/internal/fsowner"
)

const hostInventoryLimit = 1 << 20

// hostFileReadPermit limits canceled synchronous file operations to one
// in-flight helper. The caller can stop waiting when its context expires,
// while the helper retains the permit until its regular-file operation ends.
var hostFileReadPermit = make(chan struct{}, 1)

// CheckHost reads current authority for explicit proposed maps. It changes no
// account, delegation, deployment metadata or runtime resource.
func CheckHost(ctx context.Context, directory string, uids, gids []Interval) (HostIdentity, error) {
	if ctx == nil {
		return HostIdentity{}, fmt.Errorf("host check requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return HostIdentity{}, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil || canonical != absolute {
		return HostIdentity{}, fmt.Errorf("mapping owner directory must be canonical")
	}
	root, owner, err := fsowner.OpenDirectory(absolute)
	if err != nil {
		return HostIdentity{}, err
	}
	defer root.Close()
	before, err := root.Stat(".")
	if err != nil {
		return HostIdentity{}, err
	}
	uid, gid := owner.IDs()
	b := hostBackend{os.Getuid(), os.Geteuid(), os.Getgid(), os.Getegid(),
		os.Getenv("SUDO_UID") != "" || os.Getenv("SUDO_GID") != "" || os.Getenv("SUDO_USER") != "", readHostFile, runHostCommand}
	identity, err := checkHost(ctx, b, uid, gid, uids, gids)
	if err != nil {
		return HostIdentity{}, err
	}
	after, err := os.Lstat(absolute)
	if err != nil || !os.SameFile(before, after) {
		return HostIdentity{}, fmt.Errorf("mapping owner directory changed during host check")
	}
	afterUID, afterGID := fsowner.FromInfo(after).IDs()
	if afterUID != uid || afterGID != gid {
		return HostIdentity{}, fmt.Errorf("mapping owner directory ownership changed during host check")
	}
	return identity, nil
}

func readHostFile(ctx context.Context, path string) (string, error) {
	return readHostFileBounded(ctx, path, readHostFileSync)
}

type hostFileReadResult struct {
	content string
	err     error
}

func readHostFileBounded(ctx context.Context, path string, operation func(string) (string, error)) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("identity authority read requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case hostFileReadPermit <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-hostFileReadPermit
		return "", err
	}
	completed := make(chan hostFileReadResult, 1)
	go func() {
		defer func() { <-hostFileReadPermit }()
		content, err := operation(path)
		completed <- hostFileReadResult{content: content, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-completed:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return result.content, result.err
	}
}

func readHostFileSync(path string) (string, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("read identity authority %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > hostInventoryLimit {
		return "", fmt.Errorf("identity authority %s is not a bounded regular file", path)
	}
	ownerUID, _ := fsowner.FromInfo(info).IDs()
	if ownerUID != 0 || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("identity authority %s is not administrator-owned", path)
	}
	content, err := io.ReadAll(io.LimitReader(file, hostInventoryLimit+1))
	if err != nil || len(content) > hostInventoryLimit {
		return "", fmt.Errorf("identity authority %s cannot be read completely", path)
	}
	return string(content), nil
}

type boundedOutput struct{ buffer bytes.Buffer }

func (b *boundedOutput) Len() int       { return b.buffer.Len() }
func (b *boundedOutput) String() string { return b.buffer.String() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > hostInventoryLimit-b.Len() {
		return 0, fmt.Errorf("host command output exceeded its bound")
	}
	return b.buffer.Write(p)
}

func runHostCommand(ctx context.Context, name string, args ...string) (string, error) {
	path := "/usr/bin/" + name
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("identity host check requires %s", name)
	}
	uid, _ := fsowner.FromInfo(info).IDs()
	if !info.Mode().IsRegular() || uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("host utility %s is not trusted", name)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	cmd.WaitDelay = time.Second
	var output boundedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("identity host utility %s failed: %w", name, err)
	}
	return output.String(), nil
}
