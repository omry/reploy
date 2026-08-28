package providerstore

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ArchiveFormat is one of the closed archive formats understood by the
// provider materializer.
type ArchiveFormat string

const (
	ArchiveFormatTarGz ArchiveFormat = "tar.gz"
	ArchiveFormatZip   ArchiveFormat = "zip"

	CoreMaxArchiveEntries       = 10_000
	CoreMaxArchiveUnpackedBytes = 1 << 30
	CoreMaxArchivePathBytes     = 4096

	archiveMaterializationMaxEntries       uint64 = CoreMaxArchiveEntries
	archiveMaterializationMaxUnpackedBytes uint64 = CoreMaxArchiveUnpackedBytes
	archiveMaterializationMaxPathBytes     uint64 = CoreMaxArchivePathBytes
)

const (
	ArchiveEntryKindDirectory = "directory"
	ArchiveEntryKindRegular   = "regular"
)

// ArchiveMaterializationRequest is the immutable, definition-selected
// contract for one offline archive installation. Expected counts and sizes
// deliberately use the repository's canonical decimal representation.
type ArchiveMaterializationRequest struct {
	Artifact             ArtifactDescriptor
	Format               ArchiveFormat
	DestinationRoot      string
	InstallDirectory     string
	ArchiveRoot          string
	ExpectedEntryCount   string
	ExpectedUnpackedSize string
	ExecutablePaths      []string
}

// ArchiveMaterializationEntry is one normalized archive member observed by
// the materializer. ArchivePath is the path in the archive; DestinationPath
// is its path relative to the published install directory.
type ArchiveMaterializationEntry struct {
	ArchivePath     string `json:"archive_path"`
	DestinationPath string `json:"destination_path"`
	Kind            string `json:"kind"`
	Size            string `json:"size"`
}

// ArchiveMaterializationResult reports the atomically published destination
// and the exact inventory observed while parsing the archive.
type ArchiveMaterializationResult struct {
	FinalPath            string                        `json:"final_path"`
	ObservedEntryCount   string                        `json:"observed_entry_count"`
	ObservedUnpackedSize string                        `json:"observed_unpacked_size"`
	ObservedEntries      []ArchiveMaterializationEntry `json:"observed_entries"`
}

type validatedArchiveMaterializationRequest struct {
	ArchiveMaterializationRequest
	archiveRoot string
	entryLimit  uint64
	sizeLimit   uint64
}

func validateArchiveMaterializationRequest(request ArchiveMaterializationRequest) (validatedArchiveMaterializationRequest, error) {
	if err := request.Artifact.Validate(); err != nil {
		return validatedArchiveMaterializationRequest{}, fmt.Errorf("archive materialization artifact: %w", err)
	}
	switch request.Format {
	case ArchiveFormatTarGz, ArchiveFormatZip:
	default:
		return validatedArchiveMaterializationRequest{}, fmt.Errorf("archive materialization format %q is unsupported", request.Format)
	}
	if err := validateArchiveDestinationRoot(request.DestinationRoot); err != nil {
		return validatedArchiveMaterializationRequest{}, err
	}
	if err := validateInstallDirectory(request.InstallDirectory); err != nil {
		return validatedArchiveMaterializationRequest{}, err
	}
	archiveRoot, err := validateArchiveRoot(request.ArchiveRoot)
	if err != nil {
		return validatedArchiveMaterializationRequest{}, err
	}
	entryLimit, err := parseArchiveLimit("expected archive entry count", request.ExpectedEntryCount, archiveMaterializationMaxEntries)
	if err != nil {
		return validatedArchiveMaterializationRequest{}, err
	}
	sizeLimit, err := parseArchiveLimit("expected archive unpacked size", request.ExpectedUnpackedSize, archiveMaterializationMaxUnpackedBytes)
	if err != nil {
		return validatedArchiveMaterializationRequest{}, err
	}
	for index, executable := range request.ExecutablePaths {
		normalized, normalizeErr := normalizeArchivePath(executable, false)
		if normalizeErr != nil {
			return validatedArchiveMaterializationRequest{}, fmt.Errorf("archive executable path %q: %w", executable, normalizeErr)
		}
		if normalized == "." || normalized != executable {
			return validatedArchiveMaterializationRequest{}, fmt.Errorf("archive executable path %q is not normalized", executable)
		}
		if !archivePathWithin(archiveRoot, normalized) {
			return validatedArchiveMaterializationRequest{}, fmt.Errorf("archive executable path %q is outside archive root %q", executable, request.ArchiveRoot)
		}
		if index > 0 && request.ExecutablePaths[index-1] >= executable {
			return validatedArchiveMaterializationRequest{}, fmt.Errorf("archive executable paths must be unique and sorted")
		}
	}
	request.ArchiveRoot = archiveRoot
	return validatedArchiveMaterializationRequest{
		ArchiveMaterializationRequest: request,
		archiveRoot:                   archiveRoot,
		entryLimit:                    entryLimit,
		sizeLimit:                     sizeLimit,
	}, nil
}

func validateArchiveDestinationRoot(destinationRoot string) error {
	if destinationRoot == "" || strings.ContainsRune(destinationRoot, 0) || !filepath.IsAbs(destinationRoot) || filepath.Clean(destinationRoot) != destinationRoot {
		return fmt.Errorf("archive destination root must be an absolute clean path: %q", destinationRoot)
	}
	info, err := os.Lstat(destinationRoot)
	if err != nil {
		return fmt.Errorf("inspect archive destination root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archive destination root must be a real directory: %s", destinationRoot)
	}
	return nil
}

func validateInstallDirectory(installDirectory string) error {
	if installDirectory == "" || strings.ContainsRune(installDirectory, 0) || !utf8.ValidString(installDirectory) || installDirectory == "." || installDirectory == ".." || strings.ContainsAny(installDirectory, `/\\`) || filepath.Base(installDirectory) != installDirectory {
		return fmt.Errorf("archive install directory must be one path component: %q", installDirectory)
	}
	if hasWindowsVolumePrefix(installDirectory) {
		return fmt.Errorf("archive install directory must not use a volume prefix: %q", installDirectory)
	}
	if err := validateWindowsSafeArchivePath(installDirectory); err != nil {
		return fmt.Errorf("archive install directory: %w", err)
	}
	return nil
}

func validateArchiveRoot(archiveRoot string) (string, error) {
	if archiveRoot == "." {
		return archiveRoot, nil
	}
	normalized, err := normalizeArchivePath(archiveRoot, false)
	if err != nil {
		return "", fmt.Errorf("archive root %q: %w", archiveRoot, err)
	}
	if normalized != archiveRoot {
		return "", fmt.Errorf("archive root %q must be a normalized relative slash path", archiveRoot)
	}
	return normalized, nil
}

func parseArchiveLimit(name string, value string, maximum uint64) (uint64, error) {
	if value == "" || value == "0" || (len(value) > 1 && value[0] == '0') {
		return 0, fmt.Errorf("%s must be a positive canonical decimal integer", name)
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("%s must be a positive canonical decimal integer", name)
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s must be a positive canonical decimal integer", name)
	}
	if parsed > maximum {
		return 0, fmt.Errorf("%s exceeds core limit %d", name, maximum)
	}
	return parsed, nil
}

func archivePathWithin(root string, member string) bool {
	return root == "." || member == root || strings.HasPrefix(member, root+"/")
}

func portableArchiveDestinationKey(value string) string {
	value = norm.NFC.String(value)
	var key strings.Builder
	key.Grow(len(value))
	for _, char := range value {
		representative := char
		for folded := unicode.SimpleFold(char); folded != char; folded = unicode.SimpleFold(folded) {
			if folded < representative {
				representative = folded
			}
		}
		key.WriteRune(representative)
	}
	return norm.NFC.String(key.String())
}

func normalizeArchivePath(value string, directory bool) (string, error) {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) || path.IsAbs(value) || hasWindowsVolumePrefix(value) {
		return "", fmt.Errorf("path must be a relative UTF-8 slash path")
	}
	if uint64(len(value)) > archiveMaterializationMaxPathBytes {
		return "", fmt.Errorf("path exceeds %d UTF-8 bytes", archiveMaterializationMaxPathBytes)
	}
	if directory {
		if strings.HasSuffix(value, "//") {
			return "", fmt.Errorf("path contains an empty component")
		}
		value = strings.TrimSuffix(value, "/")
	} else if strings.HasSuffix(value, "/") {
		return "", fmt.Errorf("regular-file path must not end with a slash")
	}
	parts := strings.Split(value, "/")
	for len(parts) > 0 && parts[0] == "." {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		if directory {
			return ".", nil
		}
		return "", fmt.Errorf("path has no normalized components")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("path contains an invalid component")
		}
	}
	normalized := strings.Join(parts, "/")
	if uint64(len(normalized)) > archiveMaterializationMaxPathBytes {
		return "", fmt.Errorf("path exceeds %d UTF-8 bytes", archiveMaterializationMaxPathBytes)
	}
	if err := validateWindowsSafeArchivePath(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func validateWindowsSafeArchivePath(value string) error {
	for _, component := range strings.Split(value, "/") {
		if strings.ContainsAny(component, "<>:\"|?*") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") || isWindowsReservedDeviceName(component) || containsWindowsControlByte(component) {
			return fmt.Errorf("path component %q is unsafe Windows filename", component)
		}
	}
	return nil
}

func containsWindowsControlByte(component string) bool {
	for _, char := range component {
		if char >= 1 && char <= 0x1f {
			return true
		}
	}
	return false
}

func isWindowsReservedDeviceName(component string) bool {
	basename := component
	if dot := strings.IndexByte(basename, '.'); dot >= 0 {
		basename = basename[:dot]
	}
	basename = strings.ToUpper(strings.TrimRight(basename, " ."))
	switch basename {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³":
		return true
	}
	if len(basename) == 4 && (strings.HasPrefix(basename, "COM") || strings.HasPrefix(basename, "LPT")) {
		return basename[3] >= '1' && basename[3] <= '9'
	}
	return false
}

func hasWindowsVolumePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}
