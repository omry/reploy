package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var preparePackIndexCacheTemporary = preparePackIndexCacheFile
var replacePackIndexCacheFile = atomicReplacePackIndexCacheFile
var syncPackIndexCacheDirectory = syncPackIndexCacheParent

func readPackIndexPath(path string) ([]byte, error) {
	file, err := openPackIndexFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func writePackIndexCachePath(path string, content []byte) (err error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create blueprint index cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".blueprint-index-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary blueprint index cache: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := preparePackIndexCacheTemporary(temporary, content); err != nil {
		return fmt.Errorf("prepare blueprint index cache: %w", err)
	}
	if err := replacePackIndexCacheFile(temporaryPath, path); err != nil {
		return fmt.Errorf("publish blueprint index cache: %w", err)
	}
	if err := syncPackIndexCacheDirectory(directory); err != nil {
		return fmt.Errorf("sync blueprint index cache directory: %w", err)
	}
	return nil
}

func preparePackIndexCacheFile(temporary *os.File, content []byte) error {
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	return temporary.Close()
}
