package dockerdeploy

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/fsowner"
)

// Unchanged upstream data; see seccomp/README.md for provenance and updates.
//
//go:embed seccomp/docker-v0.2.4.json
var podmanDockerSeccompProfile []byte

const podmanDockerSeccompSHA256 = "785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334"

// Keep the path stable: Compose includes security_opt paths in its config hash.
// This is a generated artifact, never a configurable or inherited policy.
func materializePodmanDockerSeccomp() (string, error) {
	if fmt.Sprintf("%x", sha256.Sum256(podmanDockerSeccompProfile)) != podmanDockerSeccompSHA256 {
		return "", fmt.Errorf("embedded Docker seccomp profile integrity mismatch")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	root, _, err := fsowner.OpenDirectory(cache)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	base, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	uid, _ := fsowner.FromInfo(base).IDs()
	if uid != os.Geteuid() || base.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("Docker seccomp cache root must be owned and not writable by other users")
	}
	for _, name := range []string{"reploy", "seccomp"} {
		if err := root.Mkdir(name, 0o700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return "", err
		}
		uid, _ := fsowner.FromInfo(info).IDs()
		if !info.IsDir() || uid != os.Geteuid() || info.Mode().Perm()&0o022 != 0 || name == "seccomp" && info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("Docker seccomp cache requires an owned private directory: %s", name)
		}
		child, err := fsowner.OpenChildDirectory(root, name)
		if err != nil {
			return "", err
		}
		_ = root.Close()
		root = child
	}
	name := "docker-v0.2.4-" + podmanDockerSeccompSHA256 + ".json"
	path := filepath.Join(cache, "reploy", "seccomp", name)
	info, err := root.Lstat(name)
	if err == nil {
		uid, _ := fsowner.FromInfo(info).IDs()
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 || uid != os.Geteuid() || info.Size() != int64(len(podmanDockerSeccompProfile)) {
			return "", fmt.Errorf("Docker seccomp cache file is not owned private regular data")
		}
		data, err := root.ReadFile(name)
		if err != nil || !bytes.Equal(data, podmanDockerSeccompProfile) {
			return "", fmt.Errorf("Docker seccomp cache profile integrity mismatch: %v", err)
		}
		return path, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".docker-seccomp-")
	if err != nil {
		return "", err
	}
	temporary := filepath.Base(file.Name())
	defer func() { _ = root.Remove(temporary) }()
	_, writeErr := file.Write(podmanDockerSeccompProfile)
	modeErr := file.Chmod(0o400)
	closeErr := file.Close()
	if writeErr != nil || modeErr != nil || closeErr != nil {
		return "", fmt.Errorf("write Docker seccomp data: %v; %v; %v", writeErr, modeErr, closeErr)
	}
	if err := root.Rename(temporary, name); err != nil {
		return "", err
	}
	return path, nil
}

func preparePodmanSeccompCommand(spec CommandSpec) (CommandSpec, error) {
	index := spec.builtinSeccompIndex
	if index <= 0 {
		return spec, nil
	}
	if index >= len(spec.Args) || spec.Args[index-1] != "--security-opt" || spec.Args[index] != "seccomp=builtin" {
		return spec, fmt.Errorf("application seccomp option changed before Podman creation")
	}
	profile, err := materializePodmanDockerSeccomp()
	if err != nil {
		return spec, fmt.Errorf("prepare pinned Docker seccomp profile: %w", err)
	}
	spec.Args = append([]string(nil), spec.Args...)
	spec.Args[index] = "seccomp=" + profile
	return preparePodmanTemporaryHomeCommand(spec)
}

func podmanSeccompCreateError(client *podmanHealthcheckClient, err error) error {
	return fmt.Errorf("Podman %s sandbox creation with pinned Docker seccomp policy failed (inline-profile support from upstream podman#29726 is required; no weaker fallback): %w", client.version, err)
}
