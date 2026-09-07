package providerstore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const archiveMaterializationMaxTrailingBytes uint64 = 64 << 10

func (materializer *archiveMaterializer) extractTarGz(file *os.File) error {
	tarBudget, err := archiveTarDecompressedByteBudget(materializer.request)
	if err != nil {
		return err
	}
	gzipReader, err := gzip.NewReader(contextReader{ctx: materializer.ctx, reader: file})
	if err != nil {
		return fmt.Errorf("open gzip archive: %w", err)
	}
	err = materializer.extractTar(&archiveTarDecompressedByteBudgetReader{
		ctx:       materializer.ctx,
		reader:    gzipReader,
		remaining: tarBudget,
	})
	if err == nil {
		err = consumeTarGzipPadding(materializer.ctx, gzipReader)
	}
	closeErr := gzipReader.Close()
	return errors.Join(err, closeErr)
}

var errArchiveTarDecompressedByteBudget = errors.New("tar archive decompressed byte budget exceeded")
var errArchiveTarMissingEndMarker = errors.New("tar archive missing complete two-block end marker")

// archiveTarDecompressedByteBudget accounts for every byte archive/tar may
// consume while processing the expected entries: member headers and padding,
// one maximum-length GNU/PAX metadata record per entry, regular-file content,
// content padding, and the two end blocks. The checked arithmetic keeps this
// budget safe even if the request contract changes to wider limits later.
func archiveTarDecompressedByteBudget(request validatedArchiveMaterializationRequest) (uint64, error) {
	const (
		tarBlockBytes        uint64 = 512
		tarPaddingBytes      uint64 = tarBlockBytes - 1
		metadataPayloadBytes uint64 = archiveMaterializationMaxPathBytes + 1
		endBytes             uint64 = tarBlockBytes * 2
	)
	metadataRecordBytes, ok := addArchiveMaterializationBudget(tarBlockBytes, metadataPayloadBytes)
	if !ok {
		return 0, fmt.Errorf("tar archive decompressed byte budget overflow")
	}
	metadataRecordBytes, ok = addArchiveMaterializationBudget(metadataRecordBytes, tarPaddingBytes)
	if !ok {
		return 0, fmt.Errorf("tar archive decompressed byte budget overflow")
	}
	entryOverhead, ok := addArchiveMaterializationBudget(tarBlockBytes, tarPaddingBytes)
	if !ok {
		return 0, fmt.Errorf("tar archive decompressed byte budget overflow")
	}
	entryOverhead, ok = addArchiveMaterializationBudget(entryOverhead, metadataRecordBytes)
	if !ok {
		return 0, fmt.Errorf("tar archive decompressed byte budget overflow")
	}
	overhead, ok := multiplyArchiveMaterializationBudget(request.entryLimit, entryOverhead)
	if !ok {
		return 0, fmt.Errorf("tar archive decompressed byte budget overflow")
	}
	budget, ok := addArchiveMaterializationBudget(request.sizeLimit, overhead)
	if !ok {
		return 0, fmt.Errorf("tar archive decompressed byte budget overflow")
	}
	budget, ok = addArchiveMaterializationBudget(budget, endBytes)
	if !ok {
		return 0, fmt.Errorf("tar archive decompressed byte budget overflow")
	}
	return budget, nil
}

func addArchiveMaterializationBudget(left, right uint64) (uint64, bool) {
	if right > ^uint64(0)-left {
		return 0, false
	}
	return left + right, true
}

func multiplyArchiveMaterializationBudget(left, right uint64) (uint64, bool) {
	if left != 0 && right > ^uint64(0)/left {
		return 0, false
	}
	return left * right, true
}

type archiveTarDecompressedByteBudgetReader struct {
	ctx       context.Context
	reader    io.Reader
	remaining uint64
}

func (reader *archiveTarDecompressedByteBudgetReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if reader.remaining == 0 {
		return 0, errArchiveTarDecompressedByteBudget
	}
	if uint64(len(buffer)) > reader.remaining {
		buffer = buffer[:int(reader.remaining)]
	}
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		reader.remaining -= uint64(count)
	}
	return count, err
}

type archiveTarEndMarkerReader struct {
	reader    io.Reader
	lastBytes [2 * 512]byte
	readBytes uint64
	lastCount int
}

func (reader *archiveTarEndMarkerReader) reset() {
	reader.readBytes = 0
	reader.lastCount = 0
}

func (reader *archiveTarEndMarkerReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		reader.readBytes += uint64(count)
		reader.record(buffer[:count])
	}
	return count, err
}

func (reader *archiveTarEndMarkerReader) record(buffer []byte) {
	if len(buffer) >= len(reader.lastBytes) {
		copy(reader.lastBytes[:], buffer[len(buffer)-len(reader.lastBytes):])
		reader.lastCount = len(reader.lastBytes)
		return
	}
	if reader.lastCount+len(buffer) > len(reader.lastBytes) {
		shift := reader.lastCount + len(buffer) - len(reader.lastBytes)
		copy(reader.lastBytes[:], reader.lastBytes[shift:reader.lastCount])
		reader.lastCount -= shift
	}
	copy(reader.lastBytes[reader.lastCount:], buffer)
	reader.lastCount += len(buffer)
}

func (reader *archiveTarEndMarkerReader) hasCompleteEndMarker(expectedPrefix uint64) bool {
	if reader.readBytes != expectedPrefix+uint64(len(reader.lastBytes)) || reader.lastCount < len(reader.lastBytes) {
		return false
	}
	for _, value := range reader.lastBytes {
		if value != 0 {
			return false
		}
	}
	return true
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
	endMarkerReader := &archiveTarEndMarkerReader{reader: reader}
	tarReader := tar.NewReader(contextReader{ctx: materializer.ctx, reader: endMarkerReader})
	var expectedPreviousMemberPadding uint64
	for {
		if err := materializer.ctx.Err(); err != nil {
			return err
		}
		endMarkerReader.reset()
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			if !endMarkerReader.hasCompleteEndMarker(expectedPreviousMemberPadding) {
				return errArchiveTarMissingEndMarker
			}
			return materializer.materializeSymbolicLinks()
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		if err := validateTarHeader(header, materializer.request.SymbolicLinkPolicy); err != nil {
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
		case tar.TypeSymlink:
			if header.Size != 0 {
				return fmt.Errorf("tar symbolic link %q has a nonzero payload", header.Name)
			}
			if err := materializer.acceptSymbolicLink(header.Name, header.Linkname); err != nil {
				return err
			}
		}
		if header.Size > 0 {
			const tarBlockBytes int64 = 512
			expectedPreviousMemberPadding = uint64((tarBlockBytes - header.Size%tarBlockBytes) % tarBlockBytes)
		} else {
			expectedPreviousMemberPadding = 0
		}
	}
}

func validateTarHeader(header *tar.Header, symbolicLinkPolicy ArchiveSymbolicLinkPolicy) error {
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
	case tar.TypeSymlink:
		if symbolicLinkPolicy == ArchiveSymbolicLinkPolicyMaterializeRegularTarget {
			return nil
		}
		return fmt.Errorf("tar archive member %q has unsupported entry type %d", header.Name, header.Typeflag)
	default:
		return fmt.Errorf("tar archive member %q has unsupported entry type %d", header.Name, header.Typeflag)
	}
}

func (materializer *archiveMaterializer) acceptSymbolicLink(rawPath string, rawTarget string) error {
	archivePath, destinationPath, err := materializer.acceptPath(rawPath, false)
	if err != nil {
		return err
	}
	if destinationPath == "." {
		return fmt.Errorf("archive symbolic link cannot materialize at the install root")
	}
	targetArchivePath, err := normalizeArchiveSymbolicLinkTarget(archivePath, rawTarget)
	if err != nil {
		return fmt.Errorf("archive symbolic link %q target %q: %w", archivePath, rawTarget, err)
	}
	if !archivePathWithin(materializer.request.archiveRoot, targetArchivePath) {
		return fmt.Errorf("archive symbolic link %q target %q is outside archive root %q", archivePath, targetArchivePath, materializer.request.ArchiveRoot)
	}
	targetDestinationPath, err := materializer.destinationPath(targetArchivePath)
	if err != nil {
		return err
	}
	if node, exists := materializer.nodes[destinationPath]; exists {
		return fmt.Errorf("archive symbolic link %q collides with %s", destinationPath, node.kind)
	}
	if err := materializer.reservePortableDestination(destinationPath); err != nil {
		return err
	}
	if err := materializer.ensureParent(destinationPath); err != nil {
		return err
	}
	_, declaredExecutable := materializer.executablePaths[archivePath]
	materializer.nodes[destinationPath] = archiveMaterializedNode{kind: ArchiveEntryKindSymbolicLink, explicit: true}
	materializer.symbolicLinks = append(materializer.symbolicLinks, archiveSymbolicLink{
		archivePath: archivePath, destinationPath: destinationPath,
		targetArchivePath: targetArchivePath, targetDestinationPath: targetDestinationPath,
		executable: declaredExecutable,
	})
	materializer.entries = append(materializer.entries, ArchiveMaterializationEntry{
		ArchivePath: archivePath, DestinationPath: destinationPath, Kind: ArchiveEntryKindSymbolicLink, Size: "0",
	})
	return nil
}

func normalizeArchiveSymbolicLinkTarget(archivePath string, rawTarget string) (string, error) {
	if rawTarget == "" || !utf8.ValidString(rawTarget) || strings.ContainsRune(rawTarget, 0) || strings.Contains(rawTarget, `\`) || path.IsAbs(rawTarget) || hasWindowsVolumePrefix(rawTarget) {
		return "", fmt.Errorf("target must be a relative UTF-8 slash path")
	}
	if uint64(len(rawTarget)) > archiveMaterializationMaxPathBytes {
		return "", fmt.Errorf("target exceeds %d UTF-8 bytes", archiveMaterializationMaxPathBytes)
	}
	for _, component := range strings.Split(rawTarget, "/") {
		if component == "" || component == "." {
			return "", fmt.Errorf("target contains an invalid component")
		}
		if component == ".." {
			continue
		}
		if err := validateArchivePathComponentBytes(component); err != nil {
			return "", err
		}
		if err := validateWindowsSafeArchivePath(component); err != nil {
			return "", err
		}
	}
	resolved := path.Clean(path.Join(path.Dir(archivePath), rawTarget))
	normalized, err := normalizeArchivePath(resolved, false)
	if err != nil {
		return "", fmt.Errorf("target does not resolve to a contained archive path: %w", err)
	}
	return normalized, nil
}

func (materializer *archiveMaterializer) materializeSymbolicLinks() error {
	for _, link := range materializer.symbolicLinks {
		if err := materializer.materializeSymbolicLink(link); err != nil {
			return err
		}
	}
	return nil
}

func (materializer *archiveMaterializer) materializeSymbolicLink(link archiveSymbolicLink) error {
	targetNode, exists := materializer.nodes[link.targetDestinationPath]
	if !exists || targetNode.kind != ArchiveEntryKindRegular {
		return fmt.Errorf("archive symbolic link %q target %q is not a direct regular-file archive member", link.archivePath, link.targetArchivePath)
	}
	targetPath := filepath.FromSlash(link.targetDestinationPath)
	targetInfo, err := materializer.stageRoot.Lstat(targetPath)
	if err != nil {
		return fmt.Errorf("inspect archive symbolic link %q target %q: %w", link.archivePath, link.targetArchivePath, err)
	}
	if !targetInfo.Mode().IsRegular() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archive symbolic link %q target %q is not a regular file", link.archivePath, link.targetArchivePath)
	}
	if targetInfo.Size() < 0 || uint64(targetInfo.Size()) > archiveMaterializationMaxUnpackedBytes-materializer.materializedSize {
		return fmt.Errorf("archive materialized size exceeds core limit %d", archiveMaterializationMaxUnpackedBytes)
	}
	target, err := materializer.stageRoot.Open(targetPath)
	if err != nil {
		return fmt.Errorf("open archive symbolic link %q target %q: %w", link.archivePath, link.targetArchivePath, err)
	}
	openedInfo, statErr := target.Stat()
	if statErr == nil && !os.SameFile(targetInfo, openedInfo) {
		statErr = fmt.Errorf("target changed identity")
	}
	if statErr != nil {
		_ = target.Close()
		return fmt.Errorf("validate archive symbolic link %q target %q: %w", link.archivePath, link.targetArchivePath, statErr)
	}
	destinationPath := filepath.FromSlash(link.destinationPath)
	destination, err := materializer.stageRoot.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = target.Close()
		return fmt.Errorf("create archive symbolic link materialization %q: %w", link.destinationPath, err)
	}
	copyErr := copyArchiveMember(materializer.ctx, destination, target, targetInfo.Size())
	mode := os.FileMode(0o444)
	if link.executable {
		mode = 0o555
	}
	chmodErr := error(nil)
	syncErr := error(nil)
	if copyErr == nil {
		if err := destination.Chmod(mode); err != nil {
			chmodErr = fmt.Errorf("normalize archive symbolic link materialization %q mode: %w", link.destinationPath, err)
		} else if err := destination.Sync(); err != nil {
			syncErr = fmt.Errorf("sync archive symbolic link materialization %q: %w", link.destinationPath, err)
		}
	}
	closeErr := errors.Join(destination.Close(), target.Close())
	if copyErr != nil || chmodErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(copyErr, chmodErr, syncErr, closeErr)
	}
	materializer.materializedSize += uint64(targetInfo.Size())
	if link.executable {
		materializer.executablePaths[link.archivePath] = link.destinationPath
	}
	return nil
}
