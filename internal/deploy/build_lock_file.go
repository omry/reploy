package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const buildLockDirectoryName = "locks"

func (lock *OperationLock) PublishBuildLock(record BuildLockV1, validateProfileOwner providers.RequirementProfileOwnerValidator) (canonical.Digest, error) {
	if lock == nil {
		return "", fmt.Errorf("publish build lock requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	digest, err := BuildLockDigestV1(record, validateProfileOwner)
	if err != nil {
		return "", err
	}
	content, err := EncodeBuildLockV1(record, validateProfileOwner)
	if err != nil {
		return "", err
	}
	path, err := lock.buildLockPathLocked(digest, true)
	if err != nil {
		return "", err
	}
	if found, err := verifyBuildLockPath(path, digest, validateProfileOwner, content); found || err != nil {
		if err != nil {
			return "", err
		}
		return digest, nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".lock-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary build lock: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", fmt.Errorf("protect temporary build lock: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		return "", fmt.Errorf("write temporary build lock: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync temporary build lock: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close temporary build lock: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("publish build lock: %w", err)
		}
		if _, verifyErr := verifyBuildLockPath(path, digest, validateProfileOwner, content); verifyErr != nil {
			return "", verifyErr
		}
		return digest, nil
	}
	if err := syncAtomicStateDirectory(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("sync build lock directory: %w", err)
	}
	return digest, nil
}

func (lock *OperationLock) ReadBuildLock(digest canonical.Digest, validateProfileOwner providers.RequirementProfileOwnerValidator) (BuildLockV1, bool, error) {
	if lock == nil {
		return BuildLockV1{}, false, fmt.Errorf("read build lock requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.buildLockPathLocked(digest, false)
	if err != nil {
		return BuildLockV1{}, false, err
	}
	return readBuildLockPath(path, digest, validateProfileOwner)
}

func (lock *OperationLock) RemoveBuildLock(digest canonical.Digest, validateProfileOwner providers.RequirementProfileOwnerValidator) error {
	if lock == nil {
		return fmt.Errorf("remove build lock requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.buildLockPathLocked(digest, false)
	if err != nil {
		return err
	}
	_, found, err := readBuildLockPath(path, digest, validateProfileOwner)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove build lock: %w", err)
	}
	if err := syncAtomicStateDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync build lock directory: %w", err)
	}
	return nil
}

func (lock *OperationLock) RemoveOtherBuildLocks(current canonical.Digest, validateProfileOwner providers.RequirementProfileOwnerValidator) error {
	if lock == nil {
		return fmt.Errorf("clean build locks requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	currentPath, err := lock.buildLockPathLocked(current, false)
	if err != nil {
		return err
	}
	directory := filepath.Dir(currentPath)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read build lock directory: %w", err)
	}
	type verifiedBuildLock struct {
		digest canonical.Digest
		path   string
	}
	verified := make([]verifiedBuildLock, 0, len(entries))
	temporaryPaths := []string{}
	currentFound := false
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if isBuildLockTemporaryFilename(entry.Name()) {
			if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
				return fmt.Errorf("temporary build lock path must be a regular file: %s", path)
			}
			temporaryPaths = append(temporaryPaths, path)
			continue
		}
		digest, err := parseBuildLockFilename(entry.Name())
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("build lock path must be a regular file: %s", path)
		}
		if _, found, err := readBuildLockPath(path, digest, validateProfileOwner); err != nil || !found {
			if err != nil {
				return err
			}
			return fmt.Errorf("build lock disappeared during cleanup: %s", path)
		}
		if digest == current {
			currentFound = true
		}
		verified = append(verified, verifiedBuildLock{digest: digest, path: path})
	}
	if !currentFound {
		return fmt.Errorf("current build lock %s is missing", current)
	}
	removed := false
	for _, item := range verified {
		if item.digest == current {
			continue
		}
		if err := os.Remove(item.path); err != nil {
			return fmt.Errorf("remove non-current build lock %s: %w", item.digest, err)
		}
		removed = true
	}
	for _, path := range temporaryPaths {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove temporary build lock: %w", err)
		}
		removed = true
	}
	if removed {
		if err := syncAtomicStateDirectory(directory); err != nil {
			return fmt.Errorf("sync build lock directory: %w", err)
		}
	}
	return nil
}

func (lock *OperationLock) RemoveAllBuildLocks(validateProfileOwner providers.RequirementProfileOwnerValidator) error {
	if lock == nil {
		return fmt.Errorf("clean all build locks requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if lock.released || lock.file == nil || lock.path == "" {
		return fmt.Errorf("operation lock is not held")
	}
	directory := filepath.Join(filepath.Dir(lock.path), buildLockDirectoryName)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read build lock directory: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("build lock path must be a regular file: %s", path)
		}
		if isBuildLockTemporaryFilename(entry.Name()) {
			paths = append(paths, path)
			continue
		}
		digest, err := parseBuildLockFilename(entry.Name())
		if err != nil {
			return err
		}
		if _, found, err := readBuildLockPath(path, digest, validateProfileOwner); err != nil || !found {
			if err != nil {
				return err
			}
			return fmt.Errorf("build lock disappeared during cleanup: %s", path)
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove build lock: %w", err)
		}
	}
	if len(paths) != 0 {
		if err := syncAtomicStateDirectory(directory); err != nil {
			return fmt.Errorf("sync build lock directory: %w", err)
		}
	}
	return nil
}

func (lock *OperationLock) buildLockPathLocked(digest canonical.Digest, createDirectory bool) (string, error) {
	if lock.released || lock.file == nil || lock.path == "" {
		return "", fmt.Errorf("operation lock is not held")
	}
	if err := digest.Validate(); err != nil {
		return "", fmt.Errorf("build lock digest: %w", err)
	}
	directory := filepath.Join(filepath.Dir(lock.path), buildLockDirectoryName)
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) && createDirectory {
		if mkdirErr := os.Mkdir(directory, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
			return "", fmt.Errorf("create build lock directory: %w", mkdirErr)
		}
		info, err = os.Lstat(directory)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return filepath.Join(directory, buildLockFilename(digest)), nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect build lock directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("build lock path must be a real directory: %s", directory)
	}
	return filepath.Join(directory, buildLockFilename(digest)), nil
}

func buildLockFilename(digest canonical.Digest) string {
	return "sha256-" + strings.TrimPrefix(string(digest), "sha256:") + ".json"
}

func parseBuildLockFilename(filename string) (canonical.Digest, error) {
	hex, ok := strings.CutPrefix(filename, "sha256-")
	if !ok || !strings.HasSuffix(hex, ".json") {
		return "", fmt.Errorf("unrecognized build lock filename %q", filename)
	}
	hex = strings.TrimSuffix(hex, ".json")
	digest, err := canonical.ParseDigest("sha256:" + hex)
	if err != nil {
		return "", fmt.Errorf("unrecognized build lock filename %q: %w", filename, err)
	}
	return digest, nil
}

func isBuildLockTemporaryFilename(filename string) bool {
	value, ok := strings.CutPrefix(filename, ".lock-")
	if !ok || !strings.HasSuffix(value, ".tmp") {
		return false
	}
	value = strings.TrimSuffix(value, ".tmp")
	if value == "" {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func verifyBuildLockPath(path string, digest canonical.Digest, validateProfileOwner providers.RequirementProfileOwnerValidator, expected []byte) (bool, error) {
	record, found, err := readBuildLockPath(path, digest, validateProfileOwner)
	if err != nil || !found {
		return found, err
	}
	content, err := EncodeBuildLockV1(record, validateProfileOwner)
	if err != nil {
		return true, err
	}
	if !bytes.Equal(content, expected) {
		return true, fmt.Errorf("existing build lock %s content differs", digest)
	}
	return true, nil
}

func readBuildLockPath(path string, digest canonical.Digest, validateProfileOwner providers.RequirementProfileOwnerValidator) (BuildLockV1, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return BuildLockV1{}, false, nil
	}
	if err != nil {
		return BuildLockV1{}, false, fmt.Errorf("inspect build lock: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return BuildLockV1{}, false, fmt.Errorf("build lock path must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return BuildLockV1{}, false, fmt.Errorf("read build lock: %w", err)
	}
	record, err := DecodeBuildLockV1(content, validateProfileOwner)
	if err != nil {
		return BuildLockV1{}, false, err
	}
	actual, err := BuildLockDigestV1(record, validateProfileOwner)
	if err != nil {
		return BuildLockV1{}, false, err
	}
	if actual != digest {
		return BuildLockV1{}, false, fmt.Errorf("build lock content identity is %s, want filename identity %s", actual, digest)
	}
	return record, true, nil
}
