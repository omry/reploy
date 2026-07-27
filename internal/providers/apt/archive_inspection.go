package apt

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
)

const ArchiveFileListSchemaV1 = "apt-file-list-v1"

type ArchiveMemberV1 struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	LinkTarget string `json:"link_target"`
}

func ValidateArchiveExclusiveRootsV1(exclusiveRoots []string) error {
	_, err := archiveProtectedRootsV1(exclusiveRoots)
	return err
}

// InspectArchiveFileListV1 validates one dpkg-deb --fsys-tarfile stream and
// computes its canonical member-sequence digest without extracting payloads or
// retaining the complete list in memory.
func InspectArchiveFileListV1(ctx context.Context, reader io.Reader, exclusiveRoots []string) (canonical.Digest, error) {
	if ctx == nil {
		return "", fmt.Errorf("APT archive inspection context is required")
	}
	if reader == nil {
		return "", fmt.Errorf("APT archive inspection reader is required")
	}
	protected, err := archiveProtectedRootsV1(exclusiveRoots)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("reploy:apt-file-list:" + ArchiveFileListSchemaV1))
	_, _ = hash.Write([]byte{0, '['})
	tarReader := tar.NewReader(archiveContextReader{ctx: ctx, reader: reader})
	memberIndex := 0
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read APT archive member %d: %w", memberIndex, err)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Debian filesystem tar streams conventionally begin with the archive
		// root directory. It installs no path and therefore contributes no
		// normalized member identity. No other dot-only entry is accepted.
		if header.Typeflag == tar.TypeDir && (header.Name == "." || header.Name == "./") {
			continue
		}
		member, err := normalizeArchiveMemberV1(header)
		if err != nil {
			return "", fmt.Errorf("APT archive member %d: %w", memberIndex, err)
		}
		for _, root := range protected {
			if member.Path == root || strings.HasPrefix(member.Path, root+"/") {
				return "", fmt.Errorf("APT archive member %q claims protected root %q", member.Path, root)
			}
		}
		encoded, err := canonical.Marshal(member)
		if err != nil {
			return "", fmt.Errorf("encode APT archive member %d: %w", memberIndex, err)
		}
		if memberIndex > 0 {
			_, _ = hash.Write([]byte{','})
		}
		_, _ = hash.Write(encoded)
		memberIndex++
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	_, _ = hash.Write([]byte{']'})
	return canonical.Digest("sha256:" + hex.EncodeToString(hash.Sum(nil))), nil
}

func ArchiveFileListDigestV1(members []ArchiveMemberV1) (canonical.Digest, error) {
	if members == nil {
		return "", fmt.Errorf("APT archive members must use an array")
	}
	return canonical.Sum("apt-file-list", ArchiveFileListSchemaV1, members)
}

func normalizeArchiveMemberV1(header *tar.Header) (ArchiveMemberV1, error) {
	if header == nil {
		return ArchiveMemberV1{}, fmt.Errorf("tar header is required")
	}
	if header.Typeflag == tar.TypeGNUSparse {
		return ArchiveMemberV1{}, fmt.Errorf("sparse payloads are unsupported")
	}
	for key := range header.PAXRecords {
		if strings.HasPrefix(strings.ToLower(key), "gnu.sparse.") {
			return ArchiveMemberV1{}, fmt.Errorf("sparse payloads are unsupported")
		}
	}
	kind := ""
	directory := false
	switch header.Typeflag {
	case tar.TypeReg, tar.TypeRegA:
		kind = "regular"
	case tar.TypeDir:
		kind, directory = "directory", true
	case tar.TypeSymlink:
		kind = "symlink"
	case tar.TypeLink:
		kind = "hardlink"
	default:
		return ArchiveMemberV1{}, fmt.Errorf("tar type %d is unsupported", header.Typeflag)
	}
	memberPath, err := normalizeArchivePathV1(header.Name, directory)
	if err != nil {
		return ArchiveMemberV1{}, err
	}
	linkTarget := ""
	if kind == "symlink" {
		if header.Linkname == "" || !utf8.ValidString(header.Linkname) || strings.ContainsRune(header.Linkname, 0) {
			return ArchiveMemberV1{}, fmt.Errorf("symlink %q has an invalid target", memberPath)
		}
		linkTarget = header.Linkname
	} else if kind == "hardlink" {
		linkTarget, err = normalizeArchivePathV1(header.Linkname, false)
		if err != nil {
			return ArchiveMemberV1{}, fmt.Errorf("hardlink %q target: %w", memberPath, err)
		}
	}
	return ArchiveMemberV1{Path: memberPath, Kind: kind, LinkTarget: linkTarget}, nil
}

func normalizeArchivePathV1(value string, directory bool) (string, error) {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || path.IsAbs(value) {
		return "", fmt.Errorf("member path %q must be a relative UTF-8 path", value)
	}
	if directory {
		value = strings.TrimRight(value, "/")
	}
	parts := strings.Split(value, "/")
	for len(parts) > 0 && parts[0] == "." {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("member path %q has no normalized components", value)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("member path %q contains invalid component %q", value, part)
		}
	}
	return "/" + strings.Join(parts, "/"), nil
}

func archiveProtectedRootsV1(exclusiveRoots []string) ([]string, error) {
	if exclusiveRoots == nil {
		return nil, fmt.Errorf("APT archive exclusive roots must use an array")
	}
	roots := []string{"/.reploy-build", "/mnt"}
	seen := map[string]bool{"/.reploy-build": true, "/mnt": true}
	for index, root := range exclusiveRoots {
		if root == "" || !utf8.ValidString(root) || !path.IsAbs(root) || path.Clean(root) != root || root == "/" || strings.ContainsRune(root, 0) {
			return nil, fmt.Errorf("APT archive exclusive root %q is invalid", root)
		}
		if index > 0 && exclusiveRoots[index-1] >= root {
			return nil, fmt.Errorf("APT archive exclusive roots must be unique and sorted")
		}
		if seen[root] {
			return nil, fmt.Errorf("APT archive exclusive root %q duplicates a reserved root", root)
		}
		seen[root] = true
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots, nil
}

type archiveContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader archiveContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
