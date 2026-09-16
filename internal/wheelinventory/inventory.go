// Package wheelinventory performs the bounded, content-free structural walk
// shared by Python wheel consumers.
package wheelinventory

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omry/reploy/internal/providerstore"
)

type Kind string

const (
	Regular   Kind = "regular"
	Directory Kind = "directory"
)

type Entry struct {
	File             *zip.File
	Path             string
	Kind             Kind
	UncompressedSize uint64
}

// Inventory is returned only after the complete archive structure has passed
// validation. Files are the corresponding archive/zip entries and must not be
// used after Close.
type Inventory struct {
	Files   []*zip.File
	Entries []Entry
	file    *os.File
}

// Open opens and validates one regular wheel file. The descriptor remains
// owned by the returned inventory until Close.
func Open(ctx context.Context, filename string) (*Inventory, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("wheel archive must be a regular file")
	}
	result, err := Read(ctx, file, info.Size())
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	result.file = file
	return result, nil
}

// Read validates a wheel from a descriptor-stable ReaderAt. It never opens a
// member and performs the complete structural inventory before returning.
func Read(ctx context.Context, reader io.ReaderAt, size int64) (*Inventory, error) {
	if ctx == nil {
		return nil, fmt.Errorf("wheel inventory context is required")
	}
	if reader == nil || size < 0 {
		return nil, fmt.Errorf("wheel inventory requires a reader and nonnegative size")
	}
	if file, ok := reader.(*os.File); ok {
		if file == nil {
			return nil, fmt.Errorf("wheel inventory requires an open regular file")
		}
		info, err := file.Stat()
		if err != nil {
			return nil, fmt.Errorf("inspect wheel archive: %w", err)
		}
		if !info.Mode().IsRegular() || info.Size() != size {
			return nil, fmt.Errorf("wheel archive must be a regular file with the declared size")
		}
	}
	if err := providerstore.PreflightZipCentralDirectory(ctx, reader, size); err != nil {
		return nil, fmt.Errorf("wheel ZIP preflight: %w", err)
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, fmt.Errorf("open wheel archive: %w", err)
	}
	if len(archive.File) > providerstore.CoreMaxArchiveEntries {
		return nil, fmt.Errorf("wheel archive contains more than %d entries", providerstore.CoreMaxArchiveEntries)
	}
	entries := make([]Entry, 0, len(archive.File))
	files := make([]*zip.File, 0, len(archive.File))
	seen := make(map[string]Kind, len(archive.File))
	prefixes := make(map[string]string)
	var total uint64
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if file.Flags&0x1 != 0 {
			return nil, fmt.Errorf("wheel archive member %q is encrypted", file.Name)
		}
		if file.NonUTF8 {
			return nil, fmt.Errorf("wheel archive member %q is not UTF-8", file.Name)
		}
		if err := providerstore.ValidateZipMetadata(&file.FileHeader); err != nil {
			return nil, err
		}
		trailingSlash := strings.HasSuffix(file.Name, "/")
		header := file.FileHeader
		if trailingSlash {
			header.Name = strings.TrimSuffix(header.Name, "/")
		}
		mode := header.Mode()
		regularMode := mode.IsRegular()
		if !mode.IsDir() && !regularMode {
			return nil, fmt.Errorf("wheel archive member %q has unsupported special type", file.Name)
		}
		// A slash is the directory marker for ZIP creators that omit mode
		// attributes. An explicit Unix regular-file kind contradicts it.
		creator := header.CreatorVersion >> 8
		explicitRegular := (creator == 3 || creator == 19) && (header.ExternalAttrs>>16)&0xf000 == 0x8000
		if trailingSlash && explicitRegular {
			return nil, fmt.Errorf("wheel regular-file member %q ends with a slash", file.Name)
		}
		isDirectory := trailingSlash || mode.IsDir()
		if isDirectory && file.UncompressedSize64 != 0 {
			return nil, fmt.Errorf("wheel directory %q has a nonzero payload", file.Name)
		}
		path, err := providerstore.NormalizeArchivePath(file.Name, isDirectory)
		if err != nil || path == "." {
			if err == nil {
				err = fmt.Errorf("path has no normalized components")
			}
			return nil, fmt.Errorf("wheel archive member %q: %w", file.Name, err)
		}
		kind := Regular
		if isDirectory {
			kind = Directory
		}
		key := providerstore.PortableArchiveDestinationKey(path)
		if priorKind, exists := seen[key]; exists {
			return nil, fmt.Errorf("wheel archive contains duplicate normalized path %q (%s/%s)", path, priorKind, kind)
		}
		if prior, exists := prefixes[key]; exists {
			if kind == Regular {
				return nil, fmt.Errorf("wheel archive regular member %q conflicts with directory prefix", path)
			}
			if prior != path {
				return nil, fmt.Errorf("wheel directory prefix %q aliases %q on portable filesystems", path, prior)
			}
		}
		if kind == Directory {
			prefixes[key] = path
		}
		for prefix, prefixKey := path, key; ; {
			index := strings.LastIndexByte(prefix, '/')
			if index < 0 {
				break
			}
			prefix = prefix[:index]
			prefixKey = prefixKey[:strings.LastIndexByte(prefixKey, '/')]
			if priorKind, exists := seen[prefixKey]; exists && priorKind == Regular {
				return nil, fmt.Errorf("wheel archive member %q conflicts with regular-file directory prefix %q", path, prefix)
			}
			if prior, exists := prefixes[prefixKey]; exists && prior != prefix {
				return nil, fmt.Errorf("wheel directory prefix %q aliases %q on portable filesystems", prefix, prior)
			}
			prefixes[prefixKey] = prefix
		}
		if file.UncompressedSize64 > providerstore.CoreMaxArchiveUnpackedBytes-total {
			return nil, fmt.Errorf("wheel archive declared uncompressed size exceeds %d bytes", providerstore.CoreMaxArchiveUnpackedBytes)
		}
		total += file.UncompressedSize64
		seen[key] = kind
		canonicalFile := *file
		canonicalFile.Name = path
		if isDirectory {
			canonicalFile.Name += "/"
		}
		entries = append(entries, Entry{File: &canonicalFile, Path: path, Kind: kind, UncompressedSize: file.UncompressedSize64})
		files = append(files, &canonicalFile)
	}
	return &Inventory{Files: files, Entries: entries}, nil
}

func (i *Inventory) Close() error {
	if i == nil || i.file == nil {
		return nil
	}
	err := i.file.Close()
	i.file = nil
	return err
}
