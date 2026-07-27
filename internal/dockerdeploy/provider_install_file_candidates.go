package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type preparedProviderInstallFileV1 struct {
	FinalPath     string
	TemporaryPath string
	Mode          os.FileMode
}

type preparedProviderInstallFilesV1 struct {
	Files []preparedProviderInstallFileV1
}

// Publish replaces each live file independently. A later failure deliberately
// leaves earlier replacements in place; the committed configuring state makes
// that partial activation recoverable by reinstall or uninstall.
func (prepared preparedProviderInstallFilesV1) Publish() error {
	if prepared.Files == nil {
		return fmt.Errorf("publish install file candidates requires an array")
	}
	for index, file := range prepared.Files {
		if file.FinalPath == "" || !filepath.IsAbs(file.FinalPath) || filepath.Clean(file.FinalPath) != file.FinalPath {
			return fmt.Errorf("publish install file candidate %d has an invalid destination", index)
		}
		if file.TemporaryPath == "" || !filepath.IsAbs(file.TemporaryPath) || filepath.Clean(file.TemporaryPath) != file.TemporaryPath {
			return fmt.Errorf("publish install file candidate %d has an invalid temporary path", index)
		}
		if index > 0 && prepared.Files[index-1].FinalPath >= file.FinalPath {
			return fmt.Errorf("publish install file candidates requires unique destinations sorted by path")
		}
		info, err := os.Lstat(file.TemporaryPath)
		if err != nil {
			return fmt.Errorf("inspect prepared install file %q: %w", file.TemporaryPath, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !providerInstallFileModeMatches(info.Mode(), file.Mode) {
			return fmt.Errorf("prepared install file has unexpected type or mode: %s", file.TemporaryPath)
		}
		parent := filepath.Dir(file.FinalPath)
		if _, err := nearestExistingInstallDirectoryV1(file.FinalPath); err != nil {
			return err
		}
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create install file destination directory %q: %w", parent, err)
		}
		if err := validateProviderInstallDirectoryAncestorsV1(parent); err != nil {
			return err
		}
		if err := replaceProviderInstallFile(file.TemporaryPath, file.FinalPath); err != nil {
			return fmt.Errorf("publish install file %q: %w", file.FinalPath, err)
		}
		if err := syncProviderInstallDirectory(parent); err != nil {
			return fmt.Errorf("sync install file directory %q: %w", parent, err)
		}
	}
	return nil
}

// prepareProviderInstallFileCandidatesV1 writes private candidates without
// changing any live destination. The caller must run the disk-space preflight
// first and defer Cleanup immediately after this function succeeds.
func prepareProviderInstallFileCandidatesV1(candidates []providerInstallFileCandidateV1) (prepared preparedProviderInstallFilesV1, err error) {
	if candidates == nil {
		return preparedProviderInstallFilesV1{}, fmt.Errorf("prepare install file candidates requires an array")
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, prepared.Cleanup())
		}
	}()
	for index, candidate := range candidates {
		if err := validateProviderInstallFileCandidateV1(candidate); err != nil {
			return prepared, fmt.Errorf("prepare install file candidate %d: %w", index, err)
		}
		if index > 0 && candidates[index-1].Path >= candidate.Path {
			return prepared, fmt.Errorf("prepare install file candidates requires unique paths sorted by destination")
		}
		parent, err := nearestExistingInstallDirectoryV1(candidate.Path)
		if err != nil {
			return prepared, err
		}
		temporary, err := writeProviderInstallFileCandidateV1(parent, candidate)
		if err != nil {
			return prepared, err
		}
		prepared.Files = append(prepared.Files, preparedProviderInstallFileV1{
			FinalPath: candidate.Path, TemporaryPath: temporary, Mode: candidate.Mode,
		})
	}
	return prepared, nil
}

func (prepared preparedProviderInstallFilesV1) Cleanup() error {
	var result error
	for _, file := range prepared.Files {
		if err := os.Remove(file.TemporaryPath); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, fmt.Errorf("remove install file candidate %q: %w", file.TemporaryPath, err))
		}
	}
	return result
}

func validateProviderInstallFileCandidateV1(candidate providerInstallFileCandidateV1) error {
	if candidate.Path == "" || !filepath.IsAbs(candidate.Path) || filepath.Clean(candidate.Path) != candidate.Path {
		return fmt.Errorf("destination must be an absolute clean path")
	}
	if candidate.Content == nil {
		return fmt.Errorf("content must be present")
	}
	if candidate.Mode == 0 || candidate.Mode != candidate.Mode.Perm() {
		return fmt.Errorf("mode must contain only nonzero permission bits")
	}
	if info, err := os.Lstat(candidate.Path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("existing destination must be a regular file: %s", candidate.Path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect install file destination %q: %w", candidate.Path, err)
	}
	return nil
}

func nearestExistingInstallDirectoryV1(destination string) (string, error) {
	if err := validateProviderInstallDirectoryAncestorsV1(filepath.Dir(destination)); err != nil {
		return "", err
	}
	for candidate := filepath.Dir(destination); ; candidate = filepath.Dir(candidate) {
		info, err := os.Lstat(candidate)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("install file destination ancestor must be a real directory: %s", candidate)
			}
			return candidate, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect install file destination ancestor %q: %w", candidate, err)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("install file destination has no existing directory ancestor: %s", destination)
		}
	}
}

func validateProviderInstallDirectoryAncestorsV1(directory string) error {
	directories := []string{}
	for candidate := filepath.Clean(directory); ; candidate = filepath.Dir(candidate) {
		directories = append(directories, candidate)
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
	}
	for left, right := 0, len(directories)-1; left < right; left, right = left+1, right-1 {
		directories[left], directories[right] = directories[right], directories[left]
	}
	for _, candidate := range directories {
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect install file destination ancestor %q: %w", candidate, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("install file destination ancestor must be a real directory: %s", candidate)
		}
	}
	return nil
}

func writeProviderInstallFileCandidateV1(parent string, candidate providerInstallFileCandidateV1) (path string, err error) {
	file, err := os.CreateTemp(parent, ".reploy-install-*")
	if err != nil {
		return "", fmt.Errorf("create install file candidate beside %q: %w", candidate.Path, err)
	}
	path = file.Name()
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, file.Close())
		}
		if err != nil {
			err = errors.Join(err, os.Remove(path))
		}
	}()
	if _, err := file.Write(candidate.Content); err != nil {
		return "", fmt.Errorf("write install file candidate for %q: %w", candidate.Path, err)
	}
	if err := file.Chmod(candidate.Mode); err != nil {
		return "", fmt.Errorf("set install file candidate mode for %q: %w", candidate.Path, err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync install file candidate for %q: %w", candidate.Path, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close install file candidate for %q: %w", candidate.Path, err)
	}
	closed = true
	if !strings.HasPrefix(filepath.Base(path), ".reploy-install-") {
		return "", fmt.Errorf("install file candidate name is not private")
	}
	return path, nil
}
