package providerstore

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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
	archiveMaterializationMaxTrailingBytes uint64 = 64 << 10
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

// MaterializeArchive verifies and installs one closed archive without
// invoking any command or using the network.
func (store Store) MaterializeArchive(ctx context.Context, request ArchiveMaterializationRequest) (ArchiveMaterializationResult, error) {
	if ctx == nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("archive materialization context is required")
	}
	if err := ctx.Err(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	validated, err := validateArchiveMaterializationRequest(request)
	if err != nil {
		return ArchiveMaterializationResult{}, err
	}

	finalPath := filepath.Join(request.DestinationRoot, request.InstallDirectory)
	if err := requireAbsentArchiveDestination(finalPath); err != nil {
		return ArchiveMaterializationResult{}, err
	}

	archiveFile, err := store.OpenVerifiedArtifact(request.Artifact)
	if err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("open verified archive: %w", err)
	}
	defer archiveFile.Close()

	stage, err := os.MkdirTemp(request.DestinationRoot, "."+request.InstallDirectory+".reploy-materialize-*")
	if err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("create archive materialization workspace: %w", err)
	}
	defer cleanupArchiveMaterializationWorkspace(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("protect archive materialization workspace: %w", err)
	}

	materializer := archiveMaterializer{
		ctx:              ctx,
		stage:            stage,
		request:          validated,
		archivePaths:     map[string]struct{}{},
		nodes:            map[string]archiveMaterializedNode{".": {kind: ArchiveEntryKindDirectory, explicit: false}},
		destinationPaths: map[string]string{portableArchiveDestinationKey("."): "."},
		executablePaths:  make(map[string]string, len(request.ExecutablePaths)),
	}
	for _, executable := range request.ExecutablePaths {
		materializer.executablePaths[executable] = ""
	}

	if err := materializer.extract(archiveFile); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := materializer.validateExecutablePaths(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := materializer.validateExpectedInventory(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := VerifyOpenArtifact(archiveFile, request.Artifact); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("reverify archive after materialization: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := normalizeMaterializedTree(stage); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := syncStoreDirectory(stage); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("sync materialized archive: %w", err)
	}
	if err := requireAbsentArchiveDestination(finalPath); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ArchiveMaterializationResult{}, err
	}
	if err := publishArchiveMaterializedDirectory(stage, finalPath); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("publish materialized archive: %w", err)
	}
	if err := syncStoreDirectory(request.DestinationRoot); err != nil {
		return ArchiveMaterializationResult{}, fmt.Errorf("sync archive destination: %w", err)
	}

	return ArchiveMaterializationResult{
		FinalPath:            finalPath,
		ObservedEntryCount:   strconv.FormatUint(materializer.entryCount, 10),
		ObservedUnpackedSize: strconv.FormatUint(materializer.unpackedSize, 10),
		ObservedEntries:      append([]ArchiveMaterializationEntry(nil), materializer.entries...),
	}, nil
}

// MaterializeArchive is the package-level form for callers that keep the
// store separate from the request construction.
func MaterializeArchive(ctx context.Context, store Store, request ArchiveMaterializationRequest) (ArchiveMaterializationResult, error) {
	return store.MaterializeArchive(ctx, request)
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

func (materializer *archiveMaterializer) extract(file *os.File) error {
	switch materializer.request.Format {
	case ArchiveFormatTarGz:
		gzipReader, err := gzip.NewReader(contextReader{ctx: materializer.ctx, reader: file})
		if err != nil {
			return fmt.Errorf("open gzip archive: %w", err)
		}
		err = materializer.extractTar(gzipReader)
		if err == nil {
			err = consumeTarGzipPadding(materializer.ctx, gzipReader)
		}
		closeErr := gzipReader.Close()
		return errors.Join(err, closeErr)
	case ArchiveFormatZip:
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("inspect zip archive: %w", err)
		}
		if err := preflightZipCentralDirectory(materializer.ctx, file, info.Size()); err != nil {
			return fmt.Errorf("zip central directory preflight: %w", err)
		}
		reader, err := zip.NewReader(file, info.Size())
		if err != nil {
			return fmt.Errorf("open zip archive: %w", err)
		}
		return materializer.extractZip(reader)
	default:
		return fmt.Errorf("archive materialization format %q is unsupported", materializer.request.Format)
	}
}

// preflightZipCentralDirectory bounds ZIP directory parsing before zip.Reader
// can allocate one File per central-directory record. It counts fixed-size
// central headers instead of trusting the forgeable EOCD record count.
func preflightZipCentralDirectory(ctx context.Context, file *os.File, size int64) error {
	const (
		directoryEndSignature    = uint32(0x06054b50)
		directory64LocSignature  = uint32(0x07064b50)
		directory64EndSignature  = uint32(0x06064b50)
		directoryHeaderSignature = uint32(0x02014b50)
		directoryEndLen          = 22
		directory64LocLen        = 20
		directory64EndLen        = 56
		directoryHeaderLen       = 46
		maxInt64                 = uint64(1<<63 - 1)
	)
	if err := ctx.Err(); err != nil {
		return err
	}
	if size < directoryEndLen {
		return zip.ErrFormat
	}
	tailSize := int64(65 * 1024)
	if tailSize > size {
		tailSize = size
	}
	tail := make([]byte, int(tailSize))
	if err := readZipPreflightAt(file, size-tailSize, tail); err != nil {
		return err
	}
	eocdIndex := -1
	for index := len(tail) - directoryEndLen; index >= 0; index-- {
		if binary.LittleEndian.Uint32(tail[index:index+4]) != directoryEndSignature {
			continue
		}
		commentSize := int(binary.LittleEndian.Uint16(tail[index+20 : index+22]))
		if index+directoryEndLen+commentSize <= len(tail) {
			eocdIndex = index
			break
		}
	}
	if eocdIndex < 0 {
		return zip.ErrFormat
	}
	eocdOffset := size - tailSize + int64(eocdIndex)
	declaredRecords := uint64(binary.LittleEndian.Uint16(tail[eocdIndex+10 : eocdIndex+12]))
	directorySize := uint64(binary.LittleEndian.Uint32(tail[eocdIndex+12 : eocdIndex+16]))
	directoryOffset := uint64(binary.LittleEndian.Uint32(tail[eocdIndex+16 : eocdIndex+20]))
	directoryEndOffset := eocdOffset
	if declaredRecords == 0xffff || directorySize == 0xffffffff || directoryOffset == 0xffffffff {
		zip64Offset, zip64Records, zip64Size, zip64DirectoryOffset, err := readZip64DirectoryEnd(file, size, eocdOffset, directory64LocLen, directory64LocSignature, directory64EndLen, directory64EndSignature)
		if err != nil {
			return err
		}
		declaredRecords = zip64Records
		directorySize = zip64Size
		directoryOffset = zip64DirectoryOffset
		directoryEndOffset = zip64Offset
	}
	if declaredRecords > archiveMaterializationMaxEntries {
		return fmt.Errorf("ZIP central directory declares %d entries, exceeds core limit %d", declaredRecords, archiveMaterializationMaxEntries)
	}
	if directorySize > maxInt64 || directoryOffset > maxInt64 || directoryEndOffset < 0 || directoryEndOffset > size {
		return zip.ErrFormat
	}
	if directorySize > uint64(directoryEndOffset) {
		return zip.ErrFormat
	}
	directoryStart := directoryEndOffset - int64(directorySize)
	if directoryStart < 0 || directoryStart > directoryEndOffset {
		return zip.ErrFormat
	}
	var header [directoryHeaderLen]byte
	actualRecords := uint64(0)
	for offset := directoryStart; offset < directoryEndOffset; {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := directoryEndOffset - offset
		if remaining < directoryHeaderLen {
			return zip.ErrFormat
		}
		if err := readZipPreflightAt(file, offset, header[:]); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(header[:4]) != directoryHeaderSignature {
			return zip.ErrFormat
		}
		entrySize := uint64(directoryHeaderLen) +
			uint64(binary.LittleEndian.Uint16(header[28:30])) +
			uint64(binary.LittleEndian.Uint16(header[30:32])) +
			uint64(binary.LittleEndian.Uint16(header[32:34]))
		if entrySize > uint64(remaining) {
			return zip.ErrFormat
		}
		actualRecords++
		if actualRecords > archiveMaterializationMaxEntries {
			return fmt.Errorf("ZIP central directory contains more than core limit %d entries", archiveMaterializationMaxEntries)
		}
		offset += int64(entrySize)
	}
	if actualRecords != declaredRecords {
		return zip.ErrFormat
	}
	return nil
}

func readZip64DirectoryEnd(file *os.File, size int64, eocdOffset int64, locatorLen int, locatorSignature uint32, recordLen int, recordSignature uint32) (int64, uint64, uint64, uint64, error) {
	if eocdOffset < int64(locatorLen) {
		return 0, 0, 0, 0, zip.ErrFormat
	}
	var locator [20]byte
	locatorOffset := eocdOffset - int64(locatorLen)
	if err := readZipPreflightAt(file, locatorOffset, locator[:]); err != nil {
		return 0, 0, 0, 0, err
	}
	if binary.LittleEndian.Uint32(locator[:4]) != locatorSignature || binary.LittleEndian.Uint32(locator[4:8]) != 0 || binary.LittleEndian.Uint32(locator[16:20]) != 1 {
		return 0, 0, 0, 0, zip.ErrFormat
	}
	zip64Offset := binary.LittleEndian.Uint64(locator[8:16])
	if size < int64(recordLen) || zip64Offset >= uint64(eocdOffset) || zip64Offset > uint64(1<<63-1) || zip64Offset > uint64(size-int64(recordLen)) {
		return 0, 0, 0, 0, zip.ErrFormat
	}
	var record [56]byte
	if err := readZipPreflightAt(file, int64(zip64Offset), record[:]); err != nil {
		return 0, 0, 0, 0, err
	}
	if binary.LittleEndian.Uint32(record[:4]) != recordSignature || binary.LittleEndian.Uint64(record[4:12]) < 44 {
		return 0, 0, 0, 0, zip.ErrFormat
	}
	recordSize := binary.LittleEndian.Uint64(record[4:12])
	if recordSize > uint64(size)-zip64Offset-12 {
		return 0, 0, 0, 0, zip.ErrFormat
	}
	return int64(zip64Offset), binary.LittleEndian.Uint64(record[32:40]), binary.LittleEndian.Uint64(record[40:48]), binary.LittleEndian.Uint64(record[48:56]), nil
}

func readZipPreflightAt(file *os.File, offset int64, buffer []byte) error {
	count, err := file.ReadAt(buffer, offset)
	if err != nil {
		return err
	}
	if count != len(buffer) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func consumeTarGzipPadding(ctx context.Context, reader io.Reader) error {
	const tarBlockSize = 512
	var buffer [32 * 1024]byte
	var trailing uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, err := reader.Read(buffer[:])
		if count > 0 {
			if trailing > archiveMaterializationMaxTrailingBytes-uint64(count) {
				return fmt.Errorf("tar.gz trailing padding exceeds core limit %d bytes", archiveMaterializationMaxTrailingBytes)
			}
			trailing += uint64(count)
			for _, value := range buffer[:count] {
				if value != 0 {
					return fmt.Errorf("tar.gz archive has nonzero trailing data")
				}
			}
		}
		if errors.Is(err, io.EOF) {
			if trailing%tarBlockSize != 0 {
				return fmt.Errorf("tar.gz trailing padding is not a whole tar block")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("validate tar.gz stream: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("validate tar.gz stream: reader made no progress")
		}
	}
}

func (materializer *archiveMaterializer) extractTar(reader io.Reader) error {
	tarReader := tar.NewReader(contextReader{ctx: materializer.ctx, reader: reader})
	for {
		if err := materializer.ctx.Err(); err != nil {
			return err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		if err := validateTarHeader(header); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return fmt.Errorf("tar directory %q has a nonzero payload", header.Name)
			}
			if err := materializer.accept(header.Name, ArchiveEntryKindDirectory, 0, nil); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 {
				return fmt.Errorf("tar regular file %q has a negative size", header.Name)
			}
			if err := materializer.accept(header.Name, ArchiveEntryKindRegular, header.Size, tarReader); err != nil {
				return err
			}
		}
	}
}

func validateTarHeader(header *tar.Header) error {
	if header == nil {
		return fmt.Errorf("tar archive returned an empty header")
	}
	if header.PAXRecords != nil && len(header.PAXRecords) != 0 {
		return fmt.Errorf("tar archive member %q contains unsupported PAX metadata", header.Name)
	}
	if header.Xattrs != nil && len(header.Xattrs) != 0 {
		return fmt.Errorf("tar archive member %q contains unsupported extended attributes", header.Name)
	}
	if header.Format == tar.FormatPAX {
		return fmt.Errorf("tar archive member %q uses unsupported PAX format", header.Name)
	}
	switch header.Typeflag {
	case tar.TypeDir, tar.TypeReg, tar.TypeRegA:
		return nil
	default:
		return fmt.Errorf("tar archive member %q has unsupported entry type %d", header.Name, header.Typeflag)
	}
}

func (materializer *archiveMaterializer) extractZip(reader *zip.Reader) error {
	for _, file := range reader.File {
		if err := materializer.ctx.Err(); err != nil {
			return err
		}
		header := &file.FileHeader
		if header.Flags&0x1 != 0 {
			return fmt.Errorf("zip archive member %q is encrypted", header.Name)
		}
		if header.NonUTF8 {
			return fmt.Errorf("zip archive member %q is not UTF-8", header.Name)
		}
		if err := validateZipMetadata(header); err != nil {
			return err
		}
		mode := header.Mode()
		if mode&fs.ModeType != 0 && !mode.IsDir() {
			return fmt.Errorf("zip archive member %q has unsupported special type", header.Name)
		}
		isDirectory := strings.HasSuffix(header.Name, "/") || mode.IsDir()
		if isDirectory {
			if header.UncompressedSize64 != 0 {
				return fmt.Errorf("zip directory %q has a nonzero payload", header.Name)
			}
			if err := materializer.accept(header.Name, ArchiveEntryKindDirectory, 0, nil); err != nil {
				return err
			}
			continue
		}
		if header.UncompressedSize64 > uint64(^uint64(0)>>1) {
			return fmt.Errorf("zip archive member %q has an unsupported size", header.Name)
		}
		content, err := file.Open()
		if err != nil {
			return fmt.Errorf("open zip archive member %q: %w", header.Name, err)
		}
		err = materializer.accept(header.Name, ArchiveEntryKindRegular, int64(header.UncompressedSize64), content)
		closeErr := content.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
	}
	return nil
}

func validateZipMetadata(header *zip.FileHeader) error {
	extra := header.Extra
	for len(extra) >= 4 {
		fieldID := uint16(extra[0]) | uint16(extra[1])<<8
		fieldSize := int(uint16(extra[2]) | uint16(extra[3])<<8)
		extra = extra[4:]
		if fieldSize > len(extra) {
			return fmt.Errorf("zip archive member %q has a malformed extra field", header.Name)
		}
		if fieldID == 0x000a || fieldID == 0x9901 {
			return fmt.Errorf("zip archive member %q contains unsupported security or encryption metadata", header.Name)
		}
		extra = extra[fieldSize:]
	}
	if len(extra) != 0 {
		return fmt.Errorf("zip archive member %q has a malformed extra field", header.Name)
	}
	return nil
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

func archivePathWithin(root string, member string) bool {
	return root == "." || member == root || strings.HasPrefix(member, root+"/")
}

func portableArchiveDestinationKey(value string) string {
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
	return key.String()
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
