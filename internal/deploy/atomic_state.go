package deploy

import (
	"os"
	"path/filepath"
)

var replaceAtomicStateFile = atomicReplaceFile
var syncAtomicStateFileDirectory = syncAtomicStateDirectory

func writeAtomicStateFile(path string, content []byte, mode os.FileMode) (err error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".state-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceAtomicStateFile(temporaryPath, path); err != nil {
		return err
	}
	if err := syncAtomicStateFileDirectory(directory); err != nil {
		return err
	}
	return nil
}
