package providerstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func requireAbsentArchiveDestination(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect archive destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archive destination already exists as a symbolic link: %s", path)
	}
	return fmt.Errorf("archive destination already exists: %s", path)
}

type archiveMaterializer struct {
	ctx              context.Context
	stage            string
	request          validatedArchiveMaterializationRequest
	archivePaths     map[string]struct{}
	nodes            map[string]archiveMaterializedNode
	destinationPaths map[string]string
	executablePaths  map[string]string
	entries          []ArchiveMaterializationEntry
	entryCount       uint64
	unpackedSize     uint64
}

type archiveMaterializedNode struct {
	kind     string
	explicit bool
}

func (materializer *archiveMaterializer) accept(rawPath string, kind string, size int64, content io.Reader) error {
	if err := materializer.ctx.Err(); err != nil {
		return err
	}
	directory := kind == ArchiveEntryKindDirectory
	archivePath, err := normalizeArchivePath(rawPath, directory)
	if err != nil {
		return fmt.Errorf("archive member %q: %w", rawPath, err)
	}
	if !archivePathWithin(materializer.request.archiveRoot, archivePath) {
		return fmt.Errorf("archive member %q is outside archive root %q", archivePath, materializer.request.ArchiveRoot)
	}
	if _, exists := materializer.archivePaths[archivePath]; exists {
		return fmt.Errorf("archive contains duplicate normalized member path %q", archivePath)
	}
	if materializer.entryCount >= archiveMaterializationMaxEntries {
		return fmt.Errorf("archive entry count exceeds core limit %d", archiveMaterializationMaxEntries)
	}
	materializer.entryCount++
	if materializer.entryCount > materializer.request.entryLimit {
		return fmt.Errorf("archive entry count %d exceeds expected count %d", materializer.entryCount, materializer.request.entryLimit)
	}
	materializer.archivePaths[archivePath] = struct{}{}

	destinationPath, err := materializer.destinationPath(archivePath)
	if err != nil {
		return err
	}
	if directory {
		if err := materializer.acceptDirectory(destinationPath); err != nil {
			return err
		}
	} else {
		if size < 0 {
			return fmt.Errorf("archive member %q has a negative size", archivePath)
		}
		if uint64(size) > materializer.request.sizeLimit-materializer.unpackedSize {
			return fmt.Errorf("archive unpacked size exceeds expected or core limit")
		}
		_, declaredExecutable := materializer.executablePaths[archivePath]
		if err := materializer.acceptRegular(destinationPath, size, content, declaredExecutable); err != nil {
			return err
		}
		materializer.unpackedSize += uint64(size)
		if _, declared := materializer.executablePaths[archivePath]; declared {
			materializer.executablePaths[archivePath] = destinationPath
		}
	}

	materializer.entries = append(materializer.entries, ArchiveMaterializationEntry{
		ArchivePath: archivePath, DestinationPath: destinationPath, Kind: kind, Size: strconv.FormatInt(size, 10),
	})
	return nil
}

func (materializer *archiveMaterializer) destinationPath(archivePath string) (string, error) {
	if materializer.request.archiveRoot != "." && materializer.request.archiveRoot == materializer.request.InstallDirectory {
		if archivePath == materializer.request.archiveRoot {
			return ".", nil
		}
		prefix := materializer.request.archiveRoot + "/"
		if !strings.HasPrefix(archivePath, prefix) {
			return "", fmt.Errorf("archive member %q is outside archive root %q", archivePath, materializer.request.ArchiveRoot)
		}
		return strings.TrimPrefix(archivePath, prefix), nil
	}
	return archivePath, nil
}

func (materializer *archiveMaterializer) acceptDirectory(destinationPath string) error {
	if destinationPath == "." {
		node := materializer.nodes["."]
		if node.explicit {
			return fmt.Errorf("archive contains duplicate normalized destination path %q", destinationPath)
		}
		node.explicit = true
		materializer.nodes["."] = node
		return nil
	}
	if node, exists := materializer.nodes[destinationPath]; exists {
		if node.kind != ArchiveEntryKindDirectory {
			return fmt.Errorf("archive member %q collides with a regular file", destinationPath)
		}
		if node.explicit {
			return fmt.Errorf("archive contains duplicate normalized destination path %q", destinationPath)
		}
		node.explicit = true
		materializer.nodes[destinationPath] = node
		return nil
	}
	if err := materializer.reservePortableDestination(destinationPath); err != nil {
		return err
	}
	if err := materializer.ensureParent(destinationPath); err != nil {
		return err
	}
	pathOnDisk := filepath.Join(materializer.stage, filepath.FromSlash(destinationPath))
	if info, err := os.Lstat(pathOnDisk); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive directory %q collides with a non-directory", destinationPath)
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(pathOnDisk, 0o700); err != nil {
			return fmt.Errorf("create archive directory %q: %w", destinationPath, err)
		}
	} else {
		return fmt.Errorf("inspect archive directory %q: %w", destinationPath, err)
	}
	materializer.nodes[destinationPath] = archiveMaterializedNode{kind: ArchiveEntryKindDirectory, explicit: true}
	return nil
}

func (materializer *archiveMaterializer) acceptRegular(destinationPath string, size int64, content io.Reader, executable bool) error {
	if destinationPath == "." {
		return fmt.Errorf("archive regular member cannot materialize at the install root")
	}
	if node, exists := materializer.nodes[destinationPath]; exists {
		return fmt.Errorf("archive regular member %q collides with %s", destinationPath, node.kind)
	}
	if err := materializer.reservePortableDestination(destinationPath); err != nil {
		return err
	}
	if err := materializer.ensureParent(destinationPath); err != nil {
		return err
	}
	pathOnDisk := filepath.Join(materializer.stage, filepath.FromSlash(destinationPath))
	file, err := os.OpenFile(pathOnDisk, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create archive member %q: %w", destinationPath, err)
	}
	copyErr := copyArchiveMember(materializer.ctx, file, content, size)
	syncErr := error(nil)
	if copyErr == nil {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(copyErr, syncErr, closeErr)
	}
	mode := os.FileMode(0o444)
	if executable {
		mode = 0o555
	}
	if err := os.Chmod(pathOnDisk, mode); err != nil {
		return fmt.Errorf("normalize archive member %q mode: %w", destinationPath, err)
	}
	materializer.nodes[destinationPath] = archiveMaterializedNode{kind: ArchiveEntryKindRegular, explicit: true}
	return nil
}

func copyArchiveMember(ctx context.Context, destination *os.File, content io.Reader, size int64) error {
	if content == nil {
		return fmt.Errorf("archive regular member content is missing")
	}
	if size < 0 {
		return fmt.Errorf("archive regular member size is negative")
	}
	limited := io.LimitReader(contextReader{ctx: ctx, reader: content}, size+1)
	written, err := io.Copy(destination, limited)
	if err != nil {
		return fmt.Errorf("read archive member: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if written != size {
		return fmt.Errorf("archive member size is %d, want %d", written, size)
	}
	return nil
}

func (materializer *archiveMaterializer) ensureParent(destinationPath string) error {
	parts := strings.Split(destinationPath, "/")
	current := "."
	for _, part := range parts[:len(parts)-1] {
		if current == "." {
			current = part
		} else {
			current += "/" + part
		}
		if node, exists := materializer.nodes[current]; exists && node.kind != ArchiveEntryKindDirectory {
			return fmt.Errorf("archive path %q has a regular-file parent %q", destinationPath, current)
		}
		if err := materializer.reservePortableDestination(current); err != nil {
			return err
		}
		pathOnDisk := filepath.Join(materializer.stage, filepath.FromSlash(current))
		if info, err := os.Lstat(pathOnDisk); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("archive path %q has a non-directory parent %q", destinationPath, current)
			}
			if _, exists := materializer.nodes[current]; !exists {
				materializer.nodes[current] = archiveMaterializedNode{kind: ArchiveEntryKindDirectory}
			}
		} else if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(pathOnDisk, 0o700); err != nil {
				return fmt.Errorf("create archive parent %q: %w", current, err)
			}
			materializer.nodes[current] = archiveMaterializedNode{kind: ArchiveEntryKindDirectory}
		} else {
			return fmt.Errorf("inspect archive parent %q: %w", current, err)
		}
	}
	return nil
}

func (materializer *archiveMaterializer) reservePortableDestination(destinationPath string) error {
	key := portableArchiveDestinationKey(destinationPath)
	if existing, ok := materializer.destinationPaths[key]; ok {
		if existing != destinationPath {
			return fmt.Errorf("archive destination path %q aliases %q case-insensitively", destinationPath, existing)
		}
		return nil
	}
	materializer.destinationPaths[key] = destinationPath
	return nil
}

func (materializer *archiveMaterializer) validateExecutablePaths() error {
	for archivePath, destinationPath := range materializer.executablePaths {
		if destinationPath == "" {
			return fmt.Errorf("declared archive executable %q is missing or not a regular file", archivePath)
		}
	}
	return nil
}

func (materializer *archiveMaterializer) validateExpectedInventory() error {
	if materializer.entryCount != materializer.request.entryLimit {
		return fmt.Errorf("archive entry count %d does not match expected count %d", materializer.entryCount, materializer.request.entryLimit)
	}
	if materializer.unpackedSize != materializer.request.sizeLimit {
		return fmt.Errorf("archive unpacked size %d does not match expected size %d", materializer.unpackedSize, materializer.request.sizeLimit)
	}
	return nil
}

func normalizeMaterializedTree(root string) error {
	directories := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("materialized archive contains a symbolic link: %s", path)
		}
		if entry.IsDir() {
			if err := os.Chmod(path, 0o555); err != nil {
				return fmt.Errorf("normalize materialized directory %s: %w", path, err)
			}
			directories = append(directories, path)
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("materialized archive contains an unsupported special entry: %s", path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := syncStoreDirectory(directories[index]); err != nil {
			return fmt.Errorf("sync materialized directory %s: %w", directories[index], err)
		}
	}
	return nil
}

func cleanupArchiveMaterializationWorkspace(root string) {
	if _, err := os.Lstat(root); err != nil {
		return
	}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			_ = os.Remove(path)
			return filepath.SkipDir
		}
		if entry.IsDir() {
			_ = os.Chmod(path, 0o700)
		} else {
			_ = os.Chmod(path, 0o600)
		}
		return nil
	})
	_ = os.RemoveAll(root)
}
