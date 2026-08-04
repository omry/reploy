package probe

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func installApplicationRuntimeVerifier(destinationPath string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("installer requires container root")
	}
	if destinationPath == string(filepath.Separator) || filepath.Dir(destinationPath) != string(filepath.Separator) {
		return fmt.Errorf("runtime verifier must be installed as a root-level file")
	}
	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate installer executable: %w", err)
	}
	return installRuntimeVerifier(source, destinationPath)
}

func installRuntimeVerifier(sourcePath string, destinationPath string) (resultErr error) {
	if !filepath.IsAbs(sourcePath) || !filepath.IsAbs(destinationPath) || filepath.Clean(destinationPath) != destinationPath {
		return fmt.Errorf("runtime verifier installation requires normalized absolute paths")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open runtime verifier source: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, source.Close())
	}()

	if err := os.RemoveAll(destinationPath); err != nil {
		return fmt.Errorf("remove inherited runtime verifier path: %w", err)
	}
	temporaryPath := destinationPath + ".tmp"
	if err := os.RemoveAll(temporaryPath); err != nil {
		return fmt.Errorf("remove inherited runtime verifier temporary path: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			resultErr = errors.Join(resultErr, os.Remove(temporaryPath))
		}
	}()

	temporary, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o555)
	if err != nil {
		return fmt.Errorf("create runtime verifier: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, temporary.Close())
		}
	}()
	if _, err := io.Copy(temporary, source); err != nil {
		return fmt.Errorf("copy runtime verifier: %w", err)
	}
	if err := temporary.Chmod(0o555); err != nil {
		return fmt.Errorf("protect runtime verifier: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync runtime verifier: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close runtime verifier: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		return fmt.Errorf("commit runtime verifier: %w", err)
	}
	committed = true
	return nil
}
