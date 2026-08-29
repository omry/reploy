package providerstore

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

const (
	archiveMaterializationZipDirectoryHeaderBytes uint64 = 46
	archiveMaterializationMaxZipDirectoryBytes    uint64 = archiveMaterializationMaxEntries * (archiveMaterializationZipDirectoryHeaderBytes + archiveMaterializationMaxPathBytes)
)

func (materializer *archiveMaterializer) extractZipFile(file *os.File) error {
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
	// These are archive/zip's ZIP64 sentinel values. A missing or invalid
	// locator is not an error: archive/zip keeps the classic EOCD values.
	if declaredRecords == 0xffff || directorySize == 0xffff || directoryOffset == 0xffffffff {
		found, zip64Offset, zip64Records, zip64Size, zip64DirectoryOffset, err := readZip64DirectoryEnd(file, size, eocdOffset, directory64LocLen, directory64LocSignature, directory64EndLen, directory64EndSignature)
		if err != nil {
			return err
		}
		if found {
			declaredRecords = zip64Records
			directorySize = zip64Size
			directoryOffset = zip64DirectoryOffset
			directoryEndOffset = zip64Offset
		}
	}
	if declaredRecords > archiveMaterializationMaxEntries {
		return fmt.Errorf("ZIP central directory declares %d entries, exceeds core limit %d", declaredRecords, archiveMaterializationMaxEntries)
	}
	if directorySize > archiveMaterializationMaxZipDirectoryBytes {
		return fmt.Errorf("ZIP central directory byte budget exceeds core limit %d bytes", archiveMaterializationMaxZipDirectoryBytes)
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
	// archive/zip accepts a non-zero prepended-archive base offset, but
	// deliberately falls back to offset zero when a valid central header is
	// also present at the raw directory offset. Reject that ambiguous case so
	// the bounded scan below covers the same directory that zip.NewReader uses.
	if directoryOffset < uint64(directoryStart) {
		valid, err := zipDirectoryHeaderAt(file, size, int64(directoryOffset))
		if err != nil {
			return err
		}
		if valid {
			return fmt.Errorf("ZIP central directory has an ambiguous raw offset")
		}
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
		metadataSize := uint64(binary.LittleEndian.Uint16(header[28:30])) +
			uint64(binary.LittleEndian.Uint16(header[30:32])) +
			uint64(binary.LittleEndian.Uint16(header[32:34]))
		if metadataSize > archiveMaterializationMaxPathBytes {
			return fmt.Errorf("ZIP central directory record metadata path exceeds core limit %d bytes", archiveMaterializationMaxPathBytes)
		}
		entrySize := uint64(directoryHeaderLen) + metadataSize
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

func zipDirectoryHeaderAt(file *os.File, size int64, offset int64) (bool, error) {
	const (
		directoryHeaderLen = 46
		zip64ExtraID       = 0x0001
	)
	if offset < 0 || offset > size-int64(directoryHeaderLen) {
		return false, nil
	}
	var header [directoryHeaderLen]byte
	if err := readZipPreflightAt(file, offset, header[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	if binary.LittleEndian.Uint32(header[:4]) != 0x02014b50 {
		return false, nil
	}
	nameSize := int64(binary.LittleEndian.Uint16(header[28:30]))
	extraSize := int64(binary.LittleEndian.Uint16(header[30:32]))
	commentSize := int64(binary.LittleEndian.Uint16(header[32:34]))
	metadataSize := nameSize + extraSize + commentSize
	if metadataSize > size-offset-int64(directoryHeaderLen) {
		return false, nil
	}
	extra := make([]byte, int(extraSize))
	if err := readZipPreflightAt(file, offset+directoryHeaderLen+nameSize, extra); err != nil {
		return false, err
	}
	needUncompressedSize := binary.LittleEndian.Uint32(header[24:28]) == 0xffffffff
	needCompressedSize := binary.LittleEndian.Uint32(header[20:24]) == 0xffffffff
	needHeaderOffset := binary.LittleEndian.Uint32(header[42:46]) == 0xffffffff
	for len(extra) >= 4 {
		fieldID := binary.LittleEndian.Uint16(extra[:2])
		fieldSize := int(binary.LittleEndian.Uint16(extra[2:4]))
		extra = extra[4:]
		if fieldSize > len(extra) {
			break
		}
		field := extra[:fieldSize]
		extra = extra[fieldSize:]
		if fieldID != zip64ExtraID {
			continue
		}
		for _, needed := range []*bool{&needUncompressedSize, &needCompressedSize, &needHeaderOffset} {
			if !*needed {
				continue
			}
			if len(field) < 8 {
				return false, nil
			}
			field = field[8:]
			*needed = false
		}
	}
	// archive/zip tolerates an unresolved uncompressed-size sentinel, but
	// requires ZIP64 values for the compressed size and local-header offset.
	return !needCompressedSize && !needHeaderOffset, nil
}

func readZip64DirectoryEnd(file *os.File, size int64, eocdOffset int64, locatorLen int, locatorSignature uint32, recordLen int, recordSignature uint32) (bool, int64, uint64, uint64, uint64, error) {
	if eocdOffset < int64(locatorLen) {
		return false, 0, 0, 0, 0, nil
	}
	var locator [20]byte
	locatorOffset := eocdOffset - int64(locatorLen)
	if err := readZipPreflightAt(file, locatorOffset, locator[:]); err != nil {
		return false, 0, 0, 0, 0, err
	}
	if binary.LittleEndian.Uint32(locator[:4]) != locatorSignature || binary.LittleEndian.Uint32(locator[4:8]) != 0 || binary.LittleEndian.Uint32(locator[16:20]) != 1 {
		return false, 0, 0, 0, 0, nil
	}
	zip64Offset := binary.LittleEndian.Uint64(locator[8:16])
	if size < int64(recordLen) || zip64Offset >= uint64(eocdOffset) || zip64Offset > uint64(1<<63-1) || zip64Offset > uint64(size-int64(recordLen)) {
		return false, 0, 0, 0, 0, zip.ErrFormat
	}
	var record [56]byte
	if err := readZipPreflightAt(file, int64(zip64Offset), record[:]); err != nil {
		return false, 0, 0, 0, 0, err
	}
	if binary.LittleEndian.Uint32(record[:4]) != recordSignature || binary.LittleEndian.Uint64(record[4:12]) < 44 {
		return false, 0, 0, 0, 0, zip.ErrFormat
	}
	recordSize := binary.LittleEndian.Uint64(record[4:12])
	if recordSize > uint64(size)-zip64Offset-12 {
		return false, 0, 0, 0, 0, zip.ErrFormat
	}
	return true, int64(zip64Offset), binary.LittleEndian.Uint64(record[32:40]), binary.LittleEndian.Uint64(record[40:48]), binary.LittleEndian.Uint64(record[48:56]), nil
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
