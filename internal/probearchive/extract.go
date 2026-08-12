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

const (
	ExtractedFileName              = "reploy-probe"
	ExtractedSessionClientFileName = "reploy-session-client"
)

type ExtractedProbe struct {
	Platform string
	Path     string
	Size     string
	SHA256   canonical.Digest
}

type ExtractedSessionClient struct {
	Platform string
	Path     string
	Size     string
	SHA256   canonical.Digest
	Release  ReleaseV1
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
	extracted, err := writeExtracted(ctx, workspace, ExtractedFileName, "probe", manifestEntry, reader)
	if err != nil {
		return ExtractedProbe{}, err
	}
	return ExtractedProbe{Platform: extracted.Platform, Path: extracted.Path, Size: extracted.Size, SHA256: extracted.SHA256}, nil
}

// Supports reports whether the release archive has a native helper for the
// canonical OCI platform.
func Supports(platform string) bool {
	return helperArchivePath(platform) != ""
}

// ExtractSessionClient writes the matching controller-side session client into
// an existing private workspace.
func ExtractSessionClient(ctx context.Context, executable string, platform string, workspace string) (ExtractedSessionClient, error) {
	if ctx == nil {
		return ExtractedSessionClient{}, fmt.Errorf("session client extraction context is required")
	}
	if !SupportsSessionClient(platform) {
		return ExtractedSessionClient{}, fmt.Errorf("no embedded session client supports platform %q", platform)
	}
	if workspace == "" || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return ExtractedSessionClient{}, fmt.Errorf("session client extraction workspace must be an absolute clean path")
	}
	workspaceInfo, err := os.Lstat(workspace)
	if err != nil {
		return ExtractedSessionClient{}, fmt.Errorf("inspect session client extraction workspace: %w", err)
	}
	if !workspaceInfo.IsDir() || workspaceInfo.Mode()&os.ModeSymlink != 0 {
		return ExtractedSessionClient{}, fmt.Errorf("session client extraction workspace must be a real directory: %s", workspace)
	}
	if err := ctx.Err(); err != nil {
		return ExtractedSessionClient{}, fmt.Errorf("extract embedded session client %s: %w", platform, err)
	}
	archive, err := open(executable)
	if err != nil {
		return ExtractedSessionClient{}, err
	}
	defer archive.close()
	var manifestEntry EntryV1
	for _, candidate := range archive.manifest.SessionClients {
		if candidate.Platform == platform {
			manifestEntry = candidate
			break
		}
	}
	if manifestEntry.Platform == "" {
		return ExtractedSessionClient{}, fmt.Errorf("embedded runtime archive omits session client platform %q", platform)
	}
	reader, err := archive.entries[manifestEntry.ArchivePath].Open()
	if err != nil {
		return ExtractedSessionClient{}, fmt.Errorf("open embedded session client %s: %w", platform, err)
	}
	defer reader.Close()
	extracted, err := writeExtracted(ctx, workspace, ExtractedSessionClientFileName, "session client", manifestEntry, reader)
	if err != nil {
		return ExtractedSessionClient{}, err
	}
	return ExtractedSessionClient{
		Platform: extracted.Platform, Path: extracted.Path, Size: extracted.Size,
		SHA256: extracted.SHA256, Release: archive.manifest.Release,
	}, nil
}

func SupportsSessionClient(platform string) bool {
	return sessionClientArchivePath(platform) != ""
}

type extractedExecutable struct {
	Platform string
	Path     string
	Size     string
	SHA256   canonical.Digest
}

func writeExtracted(ctx context.Context, workspace string, filename string, kind string, entry EntryV1, reader io.Reader) (result extractedExecutable, resultErr error) {
	targetPath := filepath.Join(workspace, filename)
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o500)
	if err != nil {
		return extractedExecutable{}, fmt.Errorf("create extracted %s: %w", kind, err)
	}
	committed := false
	closed := false
	defer func() {
		if !closed {
			if err := target.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("close extracted %s: %w", kind, err))
			}
		}
		if !committed {
			if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove incomplete extracted %s: %w", kind, err))
			}
		}
	}()
	hash := sha256.New()
	size, err := copyContext(ctx, io.MultiWriter(target, hash), reader)
	if err != nil {
		return extractedExecutable{}, fmt.Errorf("extract embedded %s %s: %w", kind, entry.Platform, err)
	}
	if strconv.FormatInt(size, 10) != entry.Size {
		return extractedExecutable{}, fmt.Errorf("extracted %s %s size does not match its manifest", kind, entry.Platform)
	}
	digest := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
	if digest != entry.SHA256 {
		return extractedExecutable{}, fmt.Errorf("extracted %s %s digest does not match its manifest", kind, entry.Platform)
	}
	if err := target.Chmod(0o555); err != nil {
		return extractedExecutable{}, fmt.Errorf("protect extracted %s: %w", kind, err)
	}
	if err := target.Sync(); err != nil {
		return extractedExecutable{}, fmt.Errorf("sync extracted %s: %w", kind, err)
	}
	if err := target.Close(); err != nil {
		return extractedExecutable{}, fmt.Errorf("close extracted %s: %w", kind, err)
	}
	closed = true
	committed = true
	return extractedExecutable{Platform: entry.Platform, Path: targetPath, Size: entry.Size, SHA256: entry.SHA256}, nil
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
