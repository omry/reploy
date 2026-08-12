// Package probearchive owns the release-binary archive that carries the
// container-native reploy-probe variants and controlled-session client
// executables. The archive is appended to the main executable, which keeps
// ordinary Go builds independent of generated assets.
package probearchive

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/omry/reploy/internal/canonical"
)

const (
	ManifestSchemaV1 = "reploy-runtime-archive-v1"
	manifestPath     = "reploy-runtime/manifest.json"
)

var ErrNotEmbedded = errors.New("Reploy executable has no embedded runtime archive")

var supportedPlatforms = [...]string{"linux/amd64", "linux/arm/v7", "linux/arm64"}
var supportedSessionClientPlatforms = [...]string{"linux/amd64", "linux/arm64"}

type ReleaseV1 struct {
	Version        string `json:"version"`
	BuildCommit    string `json:"build_commit"`
	BuildDirty     string `json:"build_dirty"`
	BuildTimestamp string `json:"build_timestamp"`
}

type HelperInput struct {
	Platform string
	Path     string
}

type SessionClientInput struct {
	Platform string
	Path     string
}

type ManifestV1 struct {
	Schema         string    `json:"schema"`
	Release        ReleaseV1 `json:"release"`
	Entries        []EntryV1 `json:"entries"`
	SessionClients []EntryV1 `json:"session_clients"`
}

type EntryV1 struct {
	Platform    string           `json:"platform"`
	ArchivePath string           `json:"archive_path"`
	Size        string           `json:"size"`
	SHA256      canonical.Digest `json:"sha256"`
}

type preparedHelper struct {
	entry   EntryV1
	content []byte
}

type openedArchive struct {
	file     *os.File
	manifest ManifestV1
	entries  map[string]*zip.File
}

// Append adds one exact, self-describing runtime archive to executable. It
// restores the original executable length if any write or verification fails.
func Append(executable string, release ReleaseV1, inputs []HelperInput, sessionClients []SessionClientInput) error {
	return appendWithVerifier(executable, release, inputs, sessionClients, func(path string) error {
		_, err := Verify(path)
		return err
	})
}

func appendWithVerifier(executable string, release ReleaseV1, inputs []HelperInput, sessionClients []SessionClientInput, verify func(string) error) (resultErr error) {
	info, err := os.Lstat(executable)
	if err != nil {
		return fmt.Errorf("inspect Reploy executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Reploy executable must be a regular file: %s", executable)
	}
	if archive, err := open(executable); err == nil {
		_ = archive.close()
		return fmt.Errorf("Reploy executable already has an embedded runtime archive")
	} else if !errors.Is(err, ErrNotEmbedded) {
		return err
	}

	helpers, manifest, err := prepare(release, inputs, sessionClients)
	if err != nil {
		return err
	}
	manifestContent, err := canonical.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode runtime archive manifest: %w", err)
	}

	file, err := os.OpenFile(executable, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open Reploy executable for runtime archive: %w", err)
	}
	originalSize := info.Size()
	committed := false
	defer func() {
		if !committed {
			if err := file.Truncate(originalSize); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("roll back Reploy executable runtime archive: %w", err))
			} else if err := file.Sync(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("sync rolled-back Reploy executable: %w", err))
			}
		}
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close Reploy executable runtime archive: %w", err))
		}
	}()
	if _, err := file.Seek(originalSize, io.SeekStart); err != nil {
		return fmt.Errorf("seek Reploy executable runtime archive: %w", err)
	}
	archive := zip.NewWriter(file)
	archive.SetOffset(originalSize)
	if err := writeEntry(archive, manifestPath, 0o444, manifestContent); err != nil {
		_ = archive.Close()
		return err
	}
	for _, helper := range helpers {
		if err := writeEntry(archive, helper.entry.ArchivePath, 0o555, helper.content); err != nil {
			_ = archive.Close()
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close runtime archive: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync Reploy executable runtime archive: %w", err)
	}
	if err := verify(executable); err != nil {
		return fmt.Errorf("verify appended runtime archive: %w", err)
	}
	committed = true
	return nil
}

// Verify validates the closed archive layout and reads and hashes every
// embedded executable. It returns the canonical manifest only after all bytes
// pass.
func Verify(executable string) (ManifestV1, error) {
	archive, err := open(executable)
	if err != nil {
		return ManifestV1{}, err
	}
	defer archive.close()
	for _, entry := range archive.manifest.Entries {
		file := archive.entries[entry.ArchivePath]
		reader, err := file.Open()
		if err != nil {
			return ManifestV1{}, fmt.Errorf("open embedded probe %s: %w", entry.Platform, err)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, reader)
		closeErr := reader.Close()
		if copyErr != nil {
			return ManifestV1{}, fmt.Errorf("read embedded probe %s: %w", entry.Platform, copyErr)
		}
		if closeErr != nil {
			return ManifestV1{}, fmt.Errorf("close embedded probe %s: %w", entry.Platform, closeErr)
		}
		if strconv.FormatInt(size, 10) != entry.Size {
			return ManifestV1{}, fmt.Errorf("embedded probe %s size does not match its manifest", entry.Platform)
		}
		actual := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
		if actual != entry.SHA256 {
			return ManifestV1{}, fmt.Errorf("embedded probe %s digest does not match its manifest", entry.Platform)
		}
	}
	for _, entry := range archive.manifest.SessionClients {
		file := archive.entries[entry.ArchivePath]
		reader, err := file.Open()
		if err != nil {
			return ManifestV1{}, fmt.Errorf("open embedded session client %s: %w", entry.Platform, err)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, reader)
		closeErr := reader.Close()
		if copyErr != nil {
			return ManifestV1{}, fmt.Errorf("read embedded session client %s: %w", entry.Platform, copyErr)
		}
		if closeErr != nil {
			return ManifestV1{}, fmt.Errorf("close embedded session client %s: %w", entry.Platform, closeErr)
		}
		if strconv.FormatInt(size, 10) != entry.Size {
			return ManifestV1{}, fmt.Errorf("embedded session client %s size does not match its manifest", entry.Platform)
		}
		actual := canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
		if actual != entry.SHA256 {
			return ManifestV1{}, fmt.Errorf("embedded session client %s digest does not match its manifest", entry.Platform)
		}
	}
	return archive.manifest, nil
}

func prepare(release ReleaseV1, inputs []HelperInput, sessionClients []SessionClientInput) ([]preparedHelper, ManifestV1, error) {
	if err := validateRelease(release); err != nil {
		return nil, ManifestV1{}, err
	}
	if len(inputs) != len(supportedPlatforms) {
		return nil, ManifestV1{}, fmt.Errorf("probe archive requires exactly %d helpers", len(supportedPlatforms))
	}
	byPlatform := make(map[string]HelperInput, len(inputs))
	for _, input := range inputs {
		if input.Platform == "" || input.Path == "" {
			return nil, ManifestV1{}, fmt.Errorf("probe archive helper platform and path are required")
		}
		if _, exists := byPlatform[input.Platform]; exists {
			return nil, ManifestV1{}, fmt.Errorf("probe archive repeats platform %q", input.Platform)
		}
		byPlatform[input.Platform] = input
	}
	helpers := make([]preparedHelper, 0, len(supportedPlatforms)+len(supportedSessionClientPlatforms))
	manifest := ManifestV1{
		Schema: ManifestSchemaV1, Release: release,
		Entries:        make([]EntryV1, 0, len(supportedPlatforms)),
		SessionClients: make([]EntryV1, 0, len(supportedSessionClientPlatforms)),
	}
	for _, platform := range supportedPlatforms {
		input, exists := byPlatform[platform]
		if !exists {
			return nil, ManifestV1{}, fmt.Errorf("probe archive is missing platform %q", platform)
		}
		info, err := os.Lstat(input.Path)
		if err != nil {
			return nil, ManifestV1{}, fmt.Errorf("inspect probe helper %s: %w", platform, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ManifestV1{}, fmt.Errorf("probe helper %s must be a regular file", platform)
		}
		content, err := os.ReadFile(input.Path)
		if err != nil {
			return nil, ManifestV1{}, fmt.Errorf("read probe helper %s: %w", platform, err)
		}
		if len(content) == 0 {
			return nil, ManifestV1{}, fmt.Errorf("probe helper %s must not be empty", platform)
		}
		digest := sha256.Sum256(content)
		entry := EntryV1{
			Platform: platform, ArchivePath: helperArchivePath(platform),
			Size: strconv.Itoa(len(content)), SHA256: canonical.Digest(fmt.Sprintf("sha256:%x", digest)),
		}
		helpers = append(helpers, preparedHelper{entry: entry, content: content})
		manifest.Entries = append(manifest.Entries, entry)
	}
	if len(byPlatform) != len(supportedPlatforms) {
		return nil, ManifestV1{}, fmt.Errorf("probe archive contains an unsupported platform")
	}
	if len(sessionClients) != len(supportedSessionClientPlatforms) {
		return nil, ManifestV1{}, fmt.Errorf("runtime archive requires exactly %d session clients", len(supportedSessionClientPlatforms))
	}
	clientsByPlatform := make(map[string]SessionClientInput, len(sessionClients))
	for _, input := range sessionClients {
		if input.Platform == "" || input.Path == "" {
			return nil, ManifestV1{}, fmt.Errorf("session client platform and path are required")
		}
		if _, exists := clientsByPlatform[input.Platform]; exists {
			return nil, ManifestV1{}, fmt.Errorf("runtime archive repeats session client platform %q", input.Platform)
		}
		clientsByPlatform[input.Platform] = input
	}
	for _, platform := range supportedSessionClientPlatforms {
		input, exists := clientsByPlatform[platform]
		if !exists {
			return nil, ManifestV1{}, fmt.Errorf("runtime archive is missing session client platform %q", platform)
		}
		info, err := os.Lstat(input.Path)
		if err != nil {
			return nil, ManifestV1{}, fmt.Errorf("inspect session client %s: %w", platform, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ManifestV1{}, fmt.Errorf("session client %s must be a regular file", platform)
		}
		content, err := os.ReadFile(input.Path)
		if err != nil {
			return nil, ManifestV1{}, fmt.Errorf("read session client %s: %w", platform, err)
		}
		if len(content) == 0 {
			return nil, ManifestV1{}, fmt.Errorf("session client %s must not be empty", platform)
		}
		digest := sha256.Sum256(content)
		entry := EntryV1{
			Platform: platform, ArchivePath: sessionClientArchivePath(platform),
			Size: strconv.Itoa(len(content)), SHA256: canonical.Digest(fmt.Sprintf("sha256:%x", digest)),
		}
		helpers = append(helpers, preparedHelper{entry: entry, content: content})
		manifest.SessionClients = append(manifest.SessionClients, entry)
	}
	if len(clientsByPlatform) != len(supportedSessionClientPlatforms) {
		return nil, ManifestV1{}, fmt.Errorf("runtime archive contains an unsupported session client platform")
	}
	return helpers, manifest, nil
}

func open(executable string) (*openedArchive, error) {
	file, err := os.Open(executable)
	if err != nil {
		return nil, fmt.Errorf("open Reploy executable runtime archive: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect Reploy executable runtime archive: %w", err)
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		_ = file.Close()
		return nil, ErrNotEmbedded
	}
	entries := make(map[string]*zip.File, len(reader.File))
	for _, entry := range reader.File {
		if _, exists := entries[entry.Name]; exists {
			_ = file.Close()
			return nil, fmt.Errorf("embedded probe archive repeats path %q", entry.Name)
		}
		entries[entry.Name] = entry
	}
	manifestFile, exists := entries[manifestPath]
	if !exists {
		_ = file.Close()
		return nil, ErrNotEmbedded
	}
	manifestContent, err := readZipFile(manifestFile)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("read embedded runtime archive manifest: %w", err)
	}
	manifest, err := decodeManifest(manifestContent)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := validateLayout(manifest, entries); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &openedArchive{file: file, manifest: manifest, entries: entries}, nil
}

func decodeManifest(content []byte) (ManifestV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var manifest ManifestV1
	if err := decoder.Decode(&manifest); err != nil {
		return ManifestV1{}, fmt.Errorf("decode embedded runtime archive manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ManifestV1{}, fmt.Errorf("embedded runtime archive manifest contains trailing JSON")
		}
		return ManifestV1{}, fmt.Errorf("decode embedded runtime archive manifest trailer: %w", err)
	}
	canonicalContent, err := canonical.Marshal(manifest)
	if err != nil {
		return ManifestV1{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return ManifestV1{}, fmt.Errorf("embedded runtime archive manifest is not canonical JSON")
	}
	return manifest, nil
}

func validateLayout(manifest ManifestV1, entries map[string]*zip.File) error {
	if manifest.Schema != ManifestSchemaV1 {
		return fmt.Errorf("embedded runtime archive schema must be %q", ManifestSchemaV1)
	}
	if err := validateRelease(manifest.Release); err != nil {
		return err
	}
	if len(manifest.Entries) != len(supportedPlatforms) || len(manifest.SessionClients) != len(supportedSessionClientPlatforms) || len(entries) != len(supportedPlatforms)+len(supportedSessionClientPlatforms)+1 {
		return fmt.Errorf("embedded runtime archive must contain exactly its manifest, three probes, and two session clients")
	}
	manifestFile := entries[manifestPath]
	if err := validateZipEntry(manifestFile, 0o444); err != nil {
		return fmt.Errorf("embedded probe archive manifest: %w", err)
	}
	for index, platform := range supportedPlatforms {
		entry := manifest.Entries[index]
		if entry.Platform != platform || entry.ArchivePath != helperArchivePath(platform) {
			return fmt.Errorf("embedded probe archive entries must use the complete sorted platform matrix")
		}
		if !canonicalPositive(entry.Size) {
			return fmt.Errorf("embedded probe %s has a noncanonical size", platform)
		}
		if err := entry.SHA256.Validate(); err != nil {
			return fmt.Errorf("embedded probe %s digest: %w", platform, err)
		}
		file, exists := entries[entry.ArchivePath]
		if !exists {
			return fmt.Errorf("embedded probe archive omits %s", platform)
		}
		if err := validateZipEntry(file, 0o555); err != nil {
			return fmt.Errorf("embedded probe %s: %w", platform, err)
		}
		if strconv.FormatUint(file.UncompressedSize64, 10) != entry.Size {
			return fmt.Errorf("embedded probe %s ZIP size does not match its manifest", platform)
		}
	}
	for index, platform := range supportedSessionClientPlatforms {
		entry := manifest.SessionClients[index]
		if entry.Platform != platform || entry.ArchivePath != sessionClientArchivePath(platform) {
			return fmt.Errorf("embedded session client entries must use the complete sorted platform matrix")
		}
		if !canonicalPositive(entry.Size) {
			return fmt.Errorf("embedded session client %s has a noncanonical size", platform)
		}
		if err := entry.SHA256.Validate(); err != nil {
			return fmt.Errorf("embedded session client %s digest: %w", platform, err)
		}
		file, exists := entries[entry.ArchivePath]
		if !exists {
			return fmt.Errorf("embedded runtime archive omits session client %s", platform)
		}
		if err := validateZipEntry(file, 0o555); err != nil {
			return fmt.Errorf("embedded session client %s: %w", platform, err)
		}
		if strconv.FormatUint(file.UncompressedSize64, 10) != entry.Size {
			return fmt.Errorf("embedded session client %s ZIP size does not match its manifest", platform)
		}
	}
	return nil
}

func validateZipEntry(file *zip.File, mode os.FileMode) error {
	if file == nil || file.FileInfo().IsDir() || !file.Mode().IsRegular() {
		return fmt.Errorf("entry must be a regular file")
	}
	if file.Method != zip.Deflate {
		return fmt.Errorf("entry must use deflate compression")
	}
	if file.Mode().Perm() != mode {
		return fmt.Errorf("entry mode must be %04o", mode)
	}
	return nil
}

func writeEntry(writer *zip.Writer, name string, mode os.FileMode, content []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create probe archive entry %s: %w", name, err)
	}
	if _, err := entry.Write(content); err != nil {
		return fmt.Errorf("write probe archive entry %s: %w", name, err)
	}
	return nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return content, nil
}

func helperArchivePath(platform string) string {
	switch platform {
	case "linux/amd64":
		return "reploy-probe/linux-amd64"
	case "linux/arm/v7":
		return "reploy-probe/linux-arm-v7"
	case "linux/arm64":
		return "reploy-probe/linux-arm64"
	default:
		return ""
	}
}

func sessionClientArchivePath(platform string) string {
	switch platform {
	case "linux/amd64":
		return "reploy-session-client/linux-amd64"
	case "linux/arm64":
		return "reploy-session-client/linux-arm64"
	default:
		return ""
	}
}

func validateRelease(release ReleaseV1) error {
	if strings.TrimSpace(release.Version) == "" || strings.TrimSpace(release.Version) != release.Version || strings.ContainsAny(release.Version, "\r\n\t") {
		return fmt.Errorf("runtime archive release version is missing or unsafe")
	}
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "build commit", value: release.BuildCommit},
		{name: "build dirty", value: release.BuildDirty},
		{name: "build timestamp", value: release.BuildTimestamp},
	} {
		if strings.TrimSpace(value.value) != value.value || strings.ContainsAny(value.value, "\r\n\t") {
			return fmt.Errorf("runtime archive release %s is unsafe", value.name)
		}
	}
	if release.BuildDirty != "" && release.BuildDirty != "0" && release.BuildDirty != "1" &&
		!strings.EqualFold(release.BuildDirty, "true") && !strings.EqualFold(release.BuildDirty, "false") {
		return fmt.Errorf("runtime archive release build dirty must be empty, 0, 1, true, or false")
	}
	return nil
}

// ValidateReleaseV1 validates release metadata carried by the private runtime
// archive without exposing its wire layout.
func ValidateReleaseV1(release ReleaseV1) error {
	return validateRelease(release)
}

func canonicalPositive(value string) bool {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for _, char := range value[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (archive *openedArchive) close() error {
	return archive.file.Close()
}
