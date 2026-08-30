package providerstore

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type archiveMaterializationDestination struct {
	path      string
	root      *os.Root
	directory *os.File
}

type archiveMaterializationWorkspace struct {
	destination *archiveMaterializationDestination
	name        string
	root        *os.Root
	published   bool
}

func openArchiveMaterializationDestination(path string) (*archiveMaterializationDestination, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open archive destination root: %w", err)
	}
	directory, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("open archive destination root handle: %w", err)
	}
	openedInfo, err := directory.Stat()
	if err != nil {
		directory.Close()
		root.Close()
		return nil, fmt.Errorf("inspect opened archive destination root: %w", err)
	}
	currentInfo, err := os.Lstat(path)
	if err != nil {
		directory.Close()
		root.Close()
		return nil, fmt.Errorf("reinspect archive destination root: %w", err)
	}
	if !currentInfo.IsDir() || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, currentInfo) {
		directory.Close()
		root.Close()
		return nil, fmt.Errorf("archive destination root changed identity while opening: %s", path)
	}
	return &archiveMaterializationDestination{path: path, root: root, directory: directory}, nil
}

func (destination *archiveMaterializationDestination) close() {
	if destination == nil {
		return
	}
	if destination.directory != nil {
		_ = destination.directory.Close()
	}
	if destination.root != nil {
		_ = destination.root.Close()
	}
}

func (destination *archiveMaterializationDestination) requireAbsent(name string) error {
	info, err := destination.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	path := filepath.Join(destination.path, name)
	if err != nil {
		return fmt.Errorf("inspect archive destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archive destination already exists as a symbolic link: %s", path)
	}
	return fmt.Errorf("archive destination already exists: %s", path)
}

func (destination *archiveMaterializationDestination) createWorkspace() (*archiveMaterializationWorkspace, error) {
	prefix := strings.TrimSuffix(archiveMaterializationWorkspacePattern, "*")
	for {
		name := prefix + strings.ToLower(rand.Text())
		if err := destination.root.Mkdir(name, 0o700); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return nil, fmt.Errorf("create archive materialization workspace: %w", err)
		}
		root, err := destination.root.OpenRoot(name)
		if err != nil {
			_ = destination.root.RemoveAll(name)
			return nil, fmt.Errorf("open archive materialization workspace: %w", err)
		}
		directory, err := root.Open(".")
		if err != nil {
			root.Close()
			_ = destination.root.RemoveAll(name)
			return nil, fmt.Errorf("open archive materialization workspace handle: %w", err)
		}
		openedInfo, openedErr := directory.Stat()
		currentInfo, currentErr := destination.root.Lstat(name)
		_ = directory.Close()
		if openedErr != nil || currentErr != nil || !currentInfo.IsDir() || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, currentInfo) {
			root.Close()
			_ = destination.root.RemoveAll(name)
			return nil, fmt.Errorf("archive materialization workspace changed identity while opening: %s", name)
		}
		return &archiveMaterializationWorkspace{destination: destination, name: name, root: root}, nil
	}
}

func (workspace *archiveMaterializationWorkspace) path() string {
	return filepath.Join(workspace.destination.path, workspace.name)
}

func (workspace *archiveMaterializationWorkspace) cleanup() {
	if workspace == nil || workspace.root == nil {
		return
	}
	if !workspace.published {
		prepareArchiveMaterializationWorkspaceCleanup(workspace.root)
	}
	_ = workspace.root.Close()
	workspace.root = nil
	if !workspace.published {
		_ = workspace.destination.root.RemoveAll(workspace.name)
	}
}

func (destination *archiveMaterializationDestination) publish(workspace *archiveMaterializationWorkspace, name string) error {
	stageDirectory, err := workspace.root.Open(".")
	if err != nil {
		return fmt.Errorf("open archive materialization stage directory: %w", err)
	}
	defer stageDirectory.Close()
	published, err := publishArchiveMaterializedDirectory(destination.directory, stageDirectory, workspace.name, name)
	workspace.published = published
	return err
}

func prepareArchiveMaterializationWorkspaceCleanup(root *os.Root) {
	_ = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		file, err := root.Open(path)
		if err != nil {
			return nil
		}
		mode := os.FileMode(0o600)
		if info.IsDir() {
			mode = 0o700
		}
		if err := file.Chmod(mode); err != nil {
			_ = root.Chmod(path, mode)
		}
		_ = file.Close()
		return nil
	})
}
