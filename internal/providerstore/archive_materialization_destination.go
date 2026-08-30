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

const archiveMaterializationRollbackPattern = ".reploy-materialize-rollback-*"

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

func (destination *archiveMaterializationDestination) requireCurrentIdentity() error {
	openedInfo, err := destination.directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened archive destination root: %w", err)
	}
	currentInfo, err := os.Lstat(destination.path)
	if err != nil {
		return fmt.Errorf("reinspect archive destination root: %w", err)
	}
	if !currentInfo.IsDir() || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, currentInfo) {
		return fmt.Errorf("archive destination root changed identity: %s", destination.path)
	}
	return nil
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
	if workspace == nil || workspace.root == nil {
		return fmt.Errorf("archive materialization workspace handle is unavailable")
	}
	stageDirectory, err := openArchiveMaterializationStageDirectory(workspace.root)
	if err != nil {
		return fmt.Errorf("open archive materialization stage directory: %w", err)
	}
	published, err := publishArchiveMaterializedDirectory(destination.directory, stageDirectory, workspace.name, name)
	if stageDirectory != nil {
		_ = stageDirectory.Close()
	}
	return destination.handlePublicationResult(workspace, name, published, err)
}

func (destination *archiveMaterializationDestination) handlePublicationResult(workspace *archiveMaterializationWorkspace, name string, published bool, publishErr error) error {
	if workspace == nil || (published && workspace.root == nil) {
		return errors.Join(publishErr, fmt.Errorf("archive materialization workspace is unavailable"))
	}
	workspace.published = published
	if publishErr == nil || !published {
		return publishErr
	}
	return errors.Join(publishErr, destination.rollbackPublished(workspace, name))
}

func (destination *archiveMaterializationDestination) validatePublished(workspace *archiveMaterializationWorkspace, name string) error {
	identityErr := destination.requireCurrentIdentity()
	if identityErr != nil {
		return errors.Join(identityErr, destination.rollbackPublished(workspace, name))
	}
	if identityErr := destination.requirePublishedIdentity(workspace, name); identityErr != nil {
		return errors.Join(identityErr, destination.rollbackPublished(workspace, name))
	}
	return nil
}

func (destination *archiveMaterializationDestination) requirePublishedIdentity(workspace *archiveMaterializationWorkspace, name string) error {
	if workspace == nil || workspace.root == nil {
		return fmt.Errorf("archive materialization workspace handle is unavailable")
	}
	if destination == nil || destination.root == nil {
		return fmt.Errorf("archive materialization destination handle is unavailable")
	}
	publishedInfo, err := workspace.root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect published archive materialization directory: %w", err)
	}
	destinationInfo, err := destination.root.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect published archive destination: %w", err)
	}
	if !destinationInfo.IsDir() || destinationInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(publishedInfo, destinationInfo) {
		return fmt.Errorf("archive destination name changed identity: %s", filepath.Join(destination.path, name))
	}
	return nil
}

func (destination *archiveMaterializationDestination) rollbackPublished(workspace *archiveMaterializationWorkspace, name string) error {
	identityErr := destination.requirePublishedIdentity(workspace, name)
	if identityErr != nil {
		closeErr := closeArchiveMaterializationWorkspaceRoot(workspace)
		if closeErr != nil {
			return errors.Join(identityErr, fmt.Errorf("close published archive during refused rollback: %w", closeErr))
		}
		return identityErr
	}

	quarantine, published, quarantineErr := destination.quarantinePublished(name)
	if quarantineErr != nil {
		if !published {
			return quarantineErr
		}
		if identityErr := destination.requirePublishedIdentity(workspace, quarantine); identityErr != nil {
			closeErr := closeArchiveMaterializationWorkspaceRoot(workspace)
			if closeErr != nil {
				return errors.Join(quarantineErr, identityErr, fmt.Errorf("close published archive during refused rollback: %w", closeErr))
			}
			return errors.Join(quarantineErr, identityErr)
		}
		return errors.Join(quarantineErr, destination.finishPublishedRollback(workspace, quarantine))
	}
	if identityErr := destination.requirePublishedIdentity(workspace, quarantine); identityErr != nil {
		closeErr := closeArchiveMaterializationWorkspaceRoot(workspace)
		if closeErr != nil {
			return errors.Join(identityErr, fmt.Errorf("close published archive during refused rollback: %w", closeErr))
		}
		return identityErr
	}
	return destination.finishPublishedRollback(workspace, quarantine)
}

func (destination *archiveMaterializationDestination) quarantinePublished(name string) (string, bool, error) {
	quarantine, err := destination.newRollbackName()
	if err != nil {
		return "", false, err
	}
	sourceRoot, err := destination.root.OpenRoot(name)
	if err != nil {
		return "", false, fmt.Errorf("open published archive for rollback: %w", err)
	}
	stageDirectory, err := openArchiveMaterializationStageDirectory(sourceRoot)
	if err != nil {
		_ = sourceRoot.Close()
		return "", false, fmt.Errorf("open published archive rollback handle: %w", err)
	}
	published, publishErr := publishArchiveMaterializedDirectory(destination.directory, stageDirectory, name, quarantine)
	if stageDirectory != nil {
		_ = stageDirectory.Close()
	}
	closeErr := sourceRoot.Close()
	if closeErr != nil {
		publishErr = errors.Join(publishErr, fmt.Errorf("close published archive rollback handle: %w", closeErr))
	}
	if publishErr != nil {
		return quarantine, published, fmt.Errorf("quarantine published archive: %w", publishErr)
	}
	return quarantine, published, nil
}

func (destination *archiveMaterializationDestination) newRollbackName() (string, error) {
	prefix := strings.TrimSuffix(archiveMaterializationRollbackPattern, "*")
	for {
		name := prefix + strings.ToLower(rand.Text())
		if _, err := destination.root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect archive materialization rollback quarantine: %w", err)
		}
	}
}

func (destination *archiveMaterializationDestination) finishPublishedRollback(workspace *archiveMaterializationWorkspace, name string) error {
	if workspace == nil || workspace.root == nil {
		return fmt.Errorf("archive materialization workspace handle is unavailable")
	}
	if destination == nil || destination.root == nil {
		return fmt.Errorf("archive materialization destination handle is unavailable")
	}
	prepareArchiveMaterializationWorkspaceCleanup(workspace.root)
	closeErr := closeArchiveMaterializationWorkspaceRoot(workspace)
	removeErr := destination.root.RemoveAll(name)
	syncErr := syncArchiveMaterializationDirectory(destination.root, ".")
	var rollbackErr error
	if closeErr != nil {
		rollbackErr = fmt.Errorf("close published archive during rollback: %w", closeErr)
	}
	if removeErr != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove published archive during rollback: %w", removeErr))
	}
	if syncErr != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("sync archive destination after rollback: %w", syncErr))
	}
	return rollbackErr
}

func closeArchiveMaterializationWorkspaceRoot(workspace *archiveMaterializationWorkspace) error {
	if workspace == nil || workspace.root == nil {
		return nil
	}
	closeErr := workspace.root.Close()
	workspace.root = nil
	workspace.published = true
	return closeErr
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
