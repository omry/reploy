package python

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	maxSourceDistributionArchiveBytes  = 256 << 20
	maxSourceDistributionExpandedBytes = 1 << 30
	maxSourceDistributionFileBytes     = 128 << 20
	maxSourceDistributionMetadataBytes = 1 << 20
	maxSourceDistributionEntries       = 100_000
	maxSourceDistributionPathBytes     = 4096
)

type SourceDistributionMetadataV1 struct {
	Distribution string
	Version      string
	Root         string
}

// SourceDistributionRelativePathsV1 returns the validated archive paths below
// the sdist's single package root. It is intended for advisory post-build
// provenance; callers must not interpret the list as every input the backend
// may have read while producing the archive.
func SourceDistributionRelativePathsV1(filename string) ([]string, SourceDistributionMetadataV1, error) {
	metadata, err := inspectSourceDistributionArchive(filename)
	if err != nil {
		return nil, SourceDistributionMetadataV1{}, err
	}
	archive, closeArchive, err := openSourceDistributionArchive(filename)
	if err != nil {
		return nil, SourceDistributionMetadataV1{}, err
	}
	defer closeArchive()
	prefix := metadata.Root + "/"
	relative := []string{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, SourceDistributionMetadataV1{}, fmt.Errorf("read Python source distribution: %w", err)
		}
		name, err := normalizedSourceDistributionPath(header)
		if err != nil {
			return nil, SourceDistributionMetadataV1{}, err
		}
		if name == metadata.Root {
			continue
		}
		value, found := strings.CutPrefix(name, prefix)
		if !found || value == "" {
			return nil, SourceDistributionMetadataV1{}, fmt.Errorf(
				"Python source distribution path %q is outside package root %q",
				name,
				metadata.Root,
			)
		}
		relative = append(relative, value)
	}
	sort.Strings(relative)
	return relative, metadata, nil
}

// DescribeSourceDistributionFileV1 validates one closed PEP 517 source
// distribution and binds the same bytes to their provider-store descriptor.
func DescribeSourceDistributionFileV1(
	filename string,
	logicalPath string,
) (providerstore.ArtifactDescriptor, SourceDistributionMetadataV1, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, SourceDistributionMetadataV1{}, err
	}
	if !info.Mode().IsRegular() {
		return providerstore.ArtifactDescriptor{}, SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution %q must be a regular file", path.Base(filename),
		)
	}
	if info.Size() > maxSourceDistributionArchiveBytes {
		return providerstore.ArtifactDescriptor{}, SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution %q exceeds the %d-byte archive limit",
			path.Base(filename), maxSourceDistributionArchiveBytes,
		)
	}
	if !strings.HasSuffix(strings.ToLower(path.Base(filename)), ".tar.gz") {
		return providerstore.ArtifactDescriptor{}, SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution %q must be a .tar.gz archive", path.Base(filename),
		)
	}
	metadata, err := inspectSourceDistributionArchive(filename)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, SourceDistributionMetadataV1{}, err
	}
	digest, err := sourceDistributionFileDigest(filename)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, SourceDistributionMetadataV1{}, err
	}
	descriptor := providerstore.ArtifactDescriptor{
		LogicalPath: logicalPath,
		Kind:        "sdist",
		Size:        strconv.FormatInt(info.Size(), 10),
		SHA256:      digest,
	}
	if err := descriptor.Validate(); err != nil {
		return providerstore.ArtifactDescriptor{}, SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution descriptor: %w", err,
		)
	}
	if path.Dir(descriptor.LogicalPath) != "sdists" ||
		path.Base(descriptor.LogicalPath) != path.Base(filename) {
		return providerstore.ArtifactDescriptor{}, SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution descriptor does not identify %q beneath sdists",
			path.Base(filename),
		)
	}
	return descriptor, metadata, nil
}

// ExtractSourceDistributionFileV1 securely expands a previously validated
// sdist into an empty private directory. It never invokes tar from the host.
func ExtractSourceDistributionFileV1(filename string, destination string) (SourceDistributionMetadataV1, error) {
	metadata, err := inspectSourceDistributionArchive(filename)
	if err != nil {
		return SourceDistributionMetadataV1{}, err
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return SourceDistributionMetadataV1{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return SourceDistributionMetadataV1{}, fmt.Errorf("Python source distribution destination must be a real directory")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return SourceDistributionMetadataV1{}, err
	}
	if len(entries) != 0 {
		return SourceDistributionMetadataV1{}, fmt.Errorf("Python source distribution destination must be empty")
	}

	archive, closeArchive, err := openSourceDistributionArchive(filename)
	if err != nil {
		return SourceDistributionMetadataV1{}, err
	}
	defer closeArchive()
	symlinks := []tar.Header{}
	directoryModes := map[string]os.FileMode{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return SourceDistributionMetadataV1{}, fmt.Errorf("read Python source distribution: %w", err)
		}
		name, err := normalizedSourceDistributionPath(header)
		if err != nil {
			return SourceDistributionMetadataV1{}, err
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return SourceDistributionMetadataV1{}, err
			}
			directoryModes[target] = sourceDistributionMode(header.Mode, true)
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return SourceDistributionMetadataV1{}, err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, sourceDistributionMode(header.Mode, false))
			if err != nil {
				return SourceDistributionMetadataV1{}, err
			}
			_, copyErr := io.CopyN(output, archive, header.Size)
			closeErr := output.Close()
			if copyErr != nil || closeErr != nil {
				return SourceDistributionMetadataV1{}, errors.Join(copyErr, closeErr)
			}
		case tar.TypeSymlink:
			symlinks = append(symlinks, *header)
		}
	}
	for _, header := range symlinks {
		name, _ := normalizedSourceDistributionPath(&header)
		target := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return SourceDistributionMetadataV1{}, err
		}
		if err := os.Symlink(filepath.FromSlash(header.Linkname), target); err != nil {
			return SourceDistributionMetadataV1{}, err
		}
	}
	directories := make([]string, 0, len(directoryModes))
	for directory := range directoryModes {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(left int, right int) bool {
		return strings.Count(directories[left], string(filepath.Separator)) >
			strings.Count(directories[right], string(filepath.Separator))
	})
	for _, directory := range directories {
		if err := os.Chmod(directory, directoryModes[directory]); err != nil {
			return SourceDistributionMetadataV1{}, err
		}
	}
	return metadata, nil
}

func inspectSourceDistributionArchive(filename string) (SourceDistributionMetadataV1, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return SourceDistributionMetadataV1{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSourceDistributionArchiveBytes {
		return SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution must be a regular archive no larger than %d bytes",
			maxSourceDistributionArchiveBytes,
		)
	}
	archive, closeArchive, err := openSourceDistributionArchive(filename)
	if err != nil {
		return SourceDistributionMetadataV1{}, err
	}
	defer closeArchive()

	paths := map[string]byte{}
	symlinks := map[string]string{}
	root := ""
	var packageMetadata []byte
	hasPyproject := false
	hasLegacySetup := false
	expanded := int64(0)
	count := 0
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return SourceDistributionMetadataV1{}, fmt.Errorf("read Python source distribution: %w", err)
		}
		count++
		if count > maxSourceDistributionEntries {
			return SourceDistributionMetadataV1{}, fmt.Errorf(
				"Python source distribution exceeds the %d-entry limit", maxSourceDistributionEntries,
			)
		}
		name, err := normalizedSourceDistributionPath(header)
		if err != nil {
			return SourceDistributionMetadataV1{}, err
		}
		if _, found := paths[name]; found {
			return SourceDistributionMetadataV1{}, fmt.Errorf(
				"Python source distribution contains duplicate path %q", name,
			)
		}
		paths[name] = header.Typeflag
		entryRoot := strings.SplitN(name, "/", 2)[0]
		if root == "" {
			root = entryRoot
		} else if root != entryRoot {
			return SourceDistributionMetadataV1{}, fmt.Errorf(
				"Python source distribution must contain exactly one top-level directory",
			)
		}
		switch header.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxSourceDistributionFileBytes {
				return SourceDistributionMetadataV1{}, fmt.Errorf(
					"Python source distribution file %q exceeds the %d-byte file limit",
					name, maxSourceDistributionFileBytes,
				)
			}
			expanded += header.Size
			if expanded > maxSourceDistributionExpandedBytes {
				return SourceDistributionMetadataV1{}, fmt.Errorf(
					"Python source distribution exceeds the %d-byte expanded limit",
					maxSourceDistributionExpandedBytes,
				)
			}
			switch name {
			case path.Join(root, "PKG-INFO"):
				if header.Size > maxSourceDistributionMetadataBytes {
					return SourceDistributionMetadataV1{}, fmt.Errorf("Python source distribution PKG-INFO is too large")
				}
				packageMetadata = make([]byte, header.Size)
				if _, err := io.ReadFull(archive, packageMetadata); err != nil {
					return SourceDistributionMetadataV1{}, fmt.Errorf("read Python source distribution PKG-INFO: %w", err)
				}
			case path.Join(root, "pyproject.toml"):
				hasPyproject = true
			case path.Join(root, "setup.py"):
				hasLegacySetup = true
			}
		case tar.TypeSymlink:
			if err := validateSourceDistributionLink(name, header.Linkname, root); err != nil {
				return SourceDistributionMetadataV1{}, err
			}
			symlinks[name] = header.Linkname
		case tar.TypeLink:
			return SourceDistributionMetadataV1{}, fmt.Errorf(
				"Python source distribution hard link %q is not supported", name,
			)
		default:
			return SourceDistributionMetadataV1{}, fmt.Errorf(
				"Python source distribution path %q has unsupported archive type %d",
				name, header.Typeflag,
			)
		}
	}
	if root == "" || paths[root] != tar.TypeDir {
		return SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution must contain one explicit top-level directory",
		)
	}
	for name := range paths {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if _, found := symlinks[parent]; found {
				return SourceDistributionMetadataV1{}, fmt.Errorf(
					"Python source distribution path %q traverses symbolic-link ancestor %q",
					name, parent,
				)
			}
		}
	}
	if len(packageMetadata) == 0 {
		return SourceDistributionMetadataV1{}, fmt.Errorf("Python source distribution is missing root PKG-INFO")
	}
	if !hasPyproject && !hasLegacySetup {
		return SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution is missing pyproject.toml or legacy setup.py",
		)
	}
	identity, err := readSelectedCoreMetadata(
		bytes.NewReader(packageMetadata), "source distribution PKG-INFO", nil,
	)
	if err != nil {
		return SourceDistributionMetadataV1{}, fmt.Errorf("parse Python source distribution PKG-INFO: %w", err)
	}
	distribution := NormalizeDistributionName(identity.Name)
	if distribution == "" {
		return SourceDistributionMetadataV1{}, fmt.Errorf("Python source distribution PKG-INFO has an invalid Name")
	}
	if strings.TrimSpace(identity.Version) == "" || identity.Version != strings.TrimSpace(identity.Version) {
		return SourceDistributionMetadataV1{}, fmt.Errorf("Python source distribution PKG-INFO has an invalid Version")
	}
	suffix := "-" + identity.Version
	if !strings.HasSuffix(root, suffix) || NormalizeDistributionName(strings.TrimSuffix(root, suffix)) != distribution {
		return SourceDistributionMetadataV1{}, fmt.Errorf(
			"Python source distribution root %q does not match %s==%s",
			root, distribution, identity.Version,
		)
	}
	return SourceDistributionMetadataV1{
		Distribution: distribution, Version: identity.Version, Root: root,
	}, nil
}

func openSourceDistributionArchive(filename string) (*tar.Reader, func() error, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, func() error { return nil }, err
	}
	compressed, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, func() error { return nil }, fmt.Errorf("open Python source distribution gzip stream: %w", err)
	}
	closeArchive := func() error {
		return errors.Join(compressed.Close(), file.Close())
	}
	return tar.NewReader(compressed), closeArchive, nil
}

func normalizedSourceDistributionPath(header *tar.Header) (string, error) {
	name := strings.TrimSuffix(header.Name, "/")
	if name == "" || len(name) > maxSourceDistributionPathBytes || !utf8.ValidString(name) ||
		path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return "", fmt.Errorf("Python source distribution contains unsafe path %q", header.Name)
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("Python source distribution contains unsafe path %q", header.Name)
		}
	}
	return name, nil
}

func validateSourceDistributionLink(name string, target string, root string) error {
	if target == "" || len(target) > maxSourceDistributionPathBytes || !utf8.ValidString(target) ||
		path.IsAbs(target) || strings.ContainsAny(target, "\\\x00") {
		return fmt.Errorf("Python source distribution symbolic link %q has unsafe target %q", name, target)
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	if resolved != root && !strings.HasPrefix(resolved, root+"/") {
		return fmt.Errorf("Python source distribution symbolic link %q escapes its package root", name)
	}
	return nil
}

func sourceDistributionMode(mode int64, directory bool) os.FileMode {
	permissions := os.FileMode(mode) & 0o777
	if directory {
		return permissions | 0o500
	}
	return permissions&^0o222 | 0o400
}

func sourceDistributionFileDigest(filename string) (canonical.Digest, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))), nil
}
