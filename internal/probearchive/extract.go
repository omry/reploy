package probearchive

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/omry/reploy/internal/canonical"
)

const ExtractedFileName = "reploy-probe"

type ExtractedProbe struct {
	Platform string
	Path     string
	Size     string
	SHA256   canonical.Digest
}

// Extract writes only the selected helper into an existing private workspace.
// The caller owns the workspace and its complete cleanup lifecycle.
func Extract(ctx context.Context, executable string, platform string, workspace string) (ExtractedProbe, error) {
	if ctx == nil {
		return ExtractedProbe{}, fmt.Errorf("probe extraction context is required")
	}
	if !Supports(platform) {
		return ExtractedProbe{}, fmt.Errorf("no embedded probe supports platform %q", platform)
	}
	if workspace == "" || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return ExtractedProbe{}, fmt.Errorf("probe extraction workspace must be an absolute clean path")
	}
	workspaceInfo, err := os.Lstat(workspace)
	if err != nil {
		return ExtractedProbe{}, fmt.Errorf("inspect probe extraction workspace: %w", err)
	}
	if !workspaceInfo.IsDir() || workspaceInfo.Mode()&os.ModeSymlink != 0 {
		return ExtractedProbe{}, fmt.Errorf("probe extraction workspace must be a real directory: %s", workspace)
	}
	if err := ctx.Err(); err != nil {
		return ExtractedProbe{}, fmt.Errorf("extract embedded probe %s: %w", platform, err)
	}

	archive, err := open(executable)
	if err != nil {
		return ExtractedProbe{}, err
	}
	defer archive.close()
	var manifestEntry EntryV1
	for _, candidate := range archive.manifest.Entries {
		if candidate.Platform == platform {
			manifestEntry = candidate
			break
		}
	}
	if manifestEntry.Platform == "" {
		return ExtractedProbe{}, fmt.Errorf("embedded probe archive omits platform %q", platform)
	}
	reader, err := archive.entries[manifestEntry.ArchivePath].Open()
	if err != nil {
		return ExtractedProbe{}, fmt.Errorf("open embedded probe %s: %w", platform, err)
	}
	defer reader.Close()
	return writeExtracted(ctx, workspace, manifestEntry, reader)
}

// Supports reports whether the release archive has a native helper for the
// canonical OCI platform.
func Supports(platform string) bool {
	return helperArchivePath(platform) != ""
}

func writeExtracted(ctx context.Context, workspace string, entry EntryV1, reader io.Reader) (result ExtractedProbe, resultErr error) {
	targetPath := filepath.Join(workspace, ExtractedFileName)
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o500)
	if err != nil {
		return ExtractedProbe{}, fmt.Errorf("create extracted probe: %w", err)
	}
	committed := false
	closed := false
	defer func() {
		if !closed {
			if err := target.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("close extracted probe: %w", err))
			}
		}
		if !committed {
			if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove incomplete extracted probe: %w", err))
			}
		}
	}()
	hash := sha256.New()
	size, err := copyContext(ctx, io.MultiWriter(target, hash), reader)
	if err != nil {
		return ExtractedProbe{}, fmt.Errorf("extract embedded probe %s: %w", entry.Platform, err)
	}
	if strconv.FormatInt(size, 10) != entry.Size {
		return ExtractedProbe{}, fmt.Errorf("extracted probe %s size does not match its manifest", entry.Platform)
	}
	digest := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
	if digest != entry.SHA256 {
		return ExtractedProbe{}, fmt.Errorf("extracted probe %s digest does not match its manifest", entry.Platform)
	}
	if err := target.Chmod(0o555); err != nil {
		return ExtractedProbe{}, fmt.Errorf("protect extracted probe: %w", err)
	}
	if err := target.Sync(); err != nil {
		return ExtractedProbe{}, fmt.Errorf("sync extracted probe: %w", err)
	}
	if err := target.Close(); err != nil {
		return ExtractedProbe{}, fmt.Errorf("close extracted probe: %w", err)
	}
	closed = true
	committed = true
	return ExtractedProbe{Platform: entry.Platform, Path: targetPath, Size: entry.Size, SHA256: entry.SHA256}, nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	return io.Copy(destination, contextReader{ctx: ctx, reader: source})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
