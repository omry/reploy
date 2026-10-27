package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/omry/reploy/internal/canonical"
)

const imageIdentityLayerLimitV1 = 128
const imageIdentityHeaderLimitV1 = 100000
const imageIdentityNameLimitV1 = 8 << 20
const imageIdentityContentLimitV1 = 16 << 20
const imageIdentityFileLimitV1 = 1 << 20

var imageIdentityAccountPathsV1 = [...]string{"etc/passwd", "etc/group", "etc/nsswitch.conf"}

type imageIdentityLayerV1 struct {
	digest canonical.Digest
	files  map[string][]byte
}

type imageIdentityLayersV1 struct {
	layers       map[string]imageIdentityLayerV1
	contentBytes int64
}

func invalidImageIdentityArchiveV1() error {
	return fmt.Errorf("image identity archive: invalid or unsupported immutable source")
}

// Keep literal Linux path bytes. Normalize only the two approved tar spellings.
func imageIdentityLayerPathV1(name string, kind byte) (string, error) {
	if kind == tar.TypeDir {
		name = strings.TrimSuffix(name, "/")
	}
	name = strings.TrimPrefix(name, "./")
	if name == "" || len(name) > 4096 || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || (name == "." && kind != tar.TypeDir) {
		return "", invalidImageIdentityArchiveV1()
	}
	return name, nil
}

func imageIdentityProtectedPathV1(name string) bool {
	if name == "." || name == "etc" {
		return true
	}
	for _, selected := range imageIdentityAccountPathsV1 {
		if name == selected || strings.HasPrefix(name, selected+"/") {
			return true
		}
	}
	return false
}

func imageIdentityUnsupportedPathV1(name string, header *tar.Header) bool {
	if name == "." || name == "etc" {
		return header.Typeflag != tar.TypeDir
	}
	for _, selected := range imageIdentityAccountPathsV1 {
		if name == selected {
			return header.Typeflag != tar.TypeReg
		}
		if strings.HasPrefix(name, selected+"/") {
			return true
		}
	}
	switch name {
	case ".wh.etc", ".wh..wh..opq", "etc/.wh..wh..opq", "etc/.wh.passwd", "etc/.wh.group", "etc/.wh.nsswitch.conf":
		return true
	}
	if header.Typeflag == tar.TypeLink {
		target, err := imageIdentityLayerPathV1(header.Linkname, tar.TypeReg)
		return err != nil || imageIdentityProtectedPathV1(target)
	}
	return false
}

func (layers *imageIdentityLayersV1) add(ctx context.Context, name string, input io.Reader) error {
	if len(layers.layers) >= imageIdentityLayerLimitV1 {
		return invalidImageIdentityArchiveV1()
	}
	hash := sha256.New()
	tracked := &imageHealthArchiveReader{ctx: ctx, input: io.TeeReader(input, hash)}
	limited := &io.LimitedReader{R: tracked, N: imageHealthArchiveLimit + 1}
	metadata := &io.LimitedReader{R: limited}
	reader := tar.NewReader(metadata)
	names, files := map[string]bool{}, map[string][]byte{}
	nameBytes := 0
	headerBytes, padding := int64(imageIdentityHeaderLimitV1*512), int64(0)
	for {
		// Next hides PAX/GNU headers. Bound the read itself and charge their
		// payload blocks too, excluding the previous regular file's padding.
		metadata.N = headerBytes + padding + 1024
		before := tracked.count
		header, err := reader.Next()
		consumed := tracked.count - before - padding
		if err == io.EOF {
			// Require the actual terminator here. Exhausting the metadata
			// budget, or dangling extension records, cannot manufacture EOF.
			if consumed != 1024 || !bytes.Equal(tracked.tail, make([]byte, 1024)) {
				return invalidImageIdentityArchiveV1()
			}
			break
		}
		if err != nil || consumed < 512 || consumed%512 != 0 || consumed > headerBytes || len(names) >= imageIdentityHeaderLimitV1 || isSparseImageHealthHeader(header) {
			return invalidImageIdentityArchiveV1()
		}
		headerBytes -= consumed
		metadata.N = limited.N
		dataStart := tracked.count
		member, err := imageIdentityLayerPathV1(header.Name, header.Typeflag)
		nameBytes += len(header.Name)
		if err != nil || names[member] || nameBytes > imageIdentityNameLimitV1 || imageIdentityUnsupportedPathV1(member, header) {
			return invalidImageIdentityArchiveV1()
		}
		names[member] = true
		selected := false
		for _, account := range imageIdentityAccountPathsV1 {
			selected = selected || member == account
		}
		if selected {
			layers.contentBytes += header.Size
			if header.Size < 1 || header.Size > imageIdentityFileLimitV1 || layers.contentBytes > imageIdentityContentLimitV1 {
				return invalidImageIdentityArchiveV1()
			}
			files[member], err = io.ReadAll(reader)
			for line := range bytes.SplitSeq(files[member], []byte{'\n'}) {
				if len(line) > 64<<10 {
					return invalidImageIdentityArchiveV1()
				}
			}
		} else {
			_, err = io.Copy(io.Discard, reader)
		}
		if err != nil {
			return invalidImageIdentityArchiveV1()
		}
		padding = (512 - (tracked.count-dataStart)%512) % 512
	}
	if err := finishImageArchiveV1(ctx, limited, tracked); err != nil {
		return invalidImageIdentityArchiveV1()
	}
	if layers.layers == nil {
		layers.layers = map[string]imageIdentityLayerV1{}
	}
	layers.layers[name] = imageIdentityLayerV1{digest: canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))), files: files}
	return nil
}
