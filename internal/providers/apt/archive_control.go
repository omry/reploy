package apt

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
)

const ArchiveInspectionScriptV1 = `set -eu
archive=$1
metadata=$(/usr/bin/dpkg-deb --field "$archive" Package Version Architecture)
printf '%s\000' "$metadata"
exec /usr/bin/dpkg-deb --fsys-tarfile "$archive"
`

// ArchiveInspectionArgvV1 renders one fixed, non-interpolated inspection
// command. It emits three control fields, a NUL frame separator, and then the
// decompressed filesystem tar stream.
func ArchiveInspectionArgvV1(containerPath string) ([]string, error) {
	if containerPath == "" || !path.IsAbs(containerPath) || path.Clean(containerPath) != containerPath || !strings.HasPrefix(containerPath, ResolveArchivesDirectory+"/") || !strings.HasSuffix(path.Base(containerPath), ".deb") {
		return nil, fmt.Errorf("APT archive inspection path must name a .deb in the resolver archive directory")
	}
	return []string{"/bin/sh", "-c", ArchiveInspectionScriptV1, "apt-archive-inspect-v1", containerPath}, nil
}

// ReadArchiveInspectionHeaderV1 consumes only the framed control header and
// returns the same buffered reader positioned at the payload tar stream.
func ReadArchiveInspectionHeaderV1(reader io.Reader, nativeArchitecture string) (PackageTuple, io.Reader, error) {
	if reader == nil {
		return PackageTuple{}, nil, fmt.Errorf("APT archive inspection reader is required")
	}
	buffered := bufio.NewReader(reader)
	header, err := buffered.ReadBytes(0)
	if err != nil {
		return PackageTuple{}, nil, fmt.Errorf("read APT archive inspection header: %w", err)
	}
	header = header[:len(header)-1]
	if len(header) == 0 || bytes.IndexAny(header, "\x00\r") >= 0 {
		return PackageTuple{}, nil, fmt.Errorf("APT archive inspection header is malformed")
	}
	lines := strings.Split(string(header), "\n")
	if len(lines) != 3 {
		return PackageTuple{}, nil, fmt.Errorf("APT archive inspection header must contain exactly three fields")
	}
	values := make([]string, 3)
	for index, label := range []string{"Package: ", "Version: ", "Architecture: "} {
		if !strings.HasPrefix(lines[index], label) || len(lines[index]) == len(label) {
			return PackageTuple{}, nil, fmt.Errorf("APT archive inspection header field %d is malformed", index)
		}
		values[index] = strings.TrimPrefix(lines[index], label)
		if strings.TrimSpace(values[index]) != values[index] || strings.ContainsAny(values[index], "\t\n") {
			return PackageTuple{}, nil, fmt.Errorf("APT archive inspection header field %d is malformed", index)
		}
	}
	if !debianPackageNameV1.MatchString(values[0]) || !validDebianVersionTokenV1(values[1]) {
		return PackageTuple{}, nil, fmt.Errorf("APT archive inspection package identity is invalid")
	}
	if values[2] != nativeArchitecture && values[2] != "all" {
		return PackageTuple{}, nil, fmt.Errorf("APT archive inspection package %q has unsupported architecture %q", values[0], values[2])
	}
	return PackageTuple{Name: values[0], Version: values[1], Architecture: values[2], Status: InstalledPackageStatusV1}, buffered, nil
}
