package deploy

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const defaultPyPIBaseURL = "https://pypi.org"

var packageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var packageNameNormalizePattern = regexp.MustCompile(`[-_.]+`)
var pyPIHTTPClient = &http.Client{Timeout: 30 * time.Second}

type pyPIProject struct {
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
	Releases map[string][]pyPIFile `json:"releases"`
	URLs     []pyPIFile            `json:"urls"`
}

type pyPIFile struct {
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	PackageType string `json:"packagetype"`
	Digests     struct {
		SHA256 string `json:"sha256"`
	} `json:"digests"`
}

func resolvePyPIBlueprint(ref PackRef) (PackRef, string, []byte, *ResolvedPackArtifact, error) {
	packageName, requestedVersion, err := parsePyPISource(ref.Source)
	if err != nil {
		return PackRef{}, "", nil, nil, err
	}
	project, err := fetchPyPIProject(ref, packageName)
	if err != nil {
		return PackRef{}, "", nil, nil, err
	}
	version := requestedVersion
	if version == "" {
		version = project.Info.Version
	}
	if version == "" {
		return PackRef{}, "", nil, nil, fmt.Errorf("pypi project metadata is missing latest version: %s", packageName)
	}
	file, err := selectPyPIWheel(project, version)
	if err != nil {
		return PackRef{}, "", nil, nil, err
	}
	cacheRoot, err := reployCacheDir()
	if err != nil {
		return PackRef{}, "", nil, nil, err
	}
	wheelPath, sha256, err := cachePyPIWheel(cacheRoot, packageName, version, file)
	if err != nil {
		return PackRef{}, "", nil, nil, err
	}
	blueprintPath, err := extractPackFromWheel(cacheRoot, packageName, version, sha256, wheelPath, ref.Subdir)
	if err != nil {
		return PackRef{}, "", nil, nil, err
	}
	blueprintContent, err := readBlueprintContentFromWheel(wheelPath, ref.Subdir)
	if err != nil {
		return PackRef{}, "", nil, nil, fmt.Errorf("read verified PyPI blueprint: %w", err)
	}
	resolvedRef := ref
	resolvedRef.Source = packageName + "==" + version
	resolvedRef.Raw = formatPyPIRef(packageName, version, ref.Subdir)
	resolvedRef.IsPinned = true
	resolvedRef.Query = nil

	artifact := &ResolvedPackArtifact{
		Scheme:        "pypi",
		Package:       packageName,
		Version:       version,
		Filename:      file.Filename,
		SHA256:        sha256,
		Subdir:        ref.Subdir,
		CachePath:     wheelPath,
		BlueprintPath: blueprintPath,
	}
	return resolvedRef, blueprintPath, blueprintContent, artifact, nil
}

func formatPyPIRef(packageName string, version string, blueprintPath string) string {
	ref := url.URL{
		Scheme: "pypi",
		Host:   packageName,
		Path:   "/" + blueprintPath,
	}
	query := ref.Query()
	query.Set("version", version)
	ref.RawQuery = query.Encode()
	return ref.String()
}

func parsePyPISource(source string) (string, string, error) {
	packageName, version, hasVersion := strings.Cut(source, "==")
	if packageName == "" || !packageNamePattern.MatchString(packageName) {
		return "", "", fmt.Errorf("invalid pypi package name: %s", source)
	}
	if strings.Contains(packageName, " ") {
		return "", "", fmt.Errorf("invalid pypi package name: %s", source)
	}
	if hasVersion && strings.TrimSpace(version) == "" {
		return "", "", fmt.Errorf("invalid pypi package version: %s", source)
	}
	if strings.Contains(version, "==") {
		return "", "", fmt.Errorf("invalid pypi package version: %s", source)
	}
	return packageName, version, nil
}

func fetchPyPIProject(ref PackRef, packageName string) (pyPIProject, error) {
	baseURL := strings.TrimRight(ref.Query.Get("index-url"), "/")
	if baseURL == "" {
		baseURL = defaultPyPIBaseURL
	}
	url := baseURL + "/pypi/" + normalizePackageName(packageName) + "/json"
	response, err := pyPIHTTPClient.Get(url)
	if err != nil {
		return pyPIProject{}, fmt.Errorf("fetch pypi metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return pyPIProject{}, fmt.Errorf("fetch pypi metadata: %s", response.Status)
	}
	var project pyPIProject
	if err := json.NewDecoder(response.Body).Decode(&project); err != nil {
		return pyPIProject{}, fmt.Errorf("parse pypi metadata: %w", err)
	}
	return project, nil
}

func selectPyPIWheel(project pyPIProject, version string) (pyPIFile, error) {
	files := project.Releases[version]
	if len(files) == 0 && project.Info.Version == version {
		files = project.URLs
	}
	for _, file := range files {
		if file.PackageType == "bdist_wheel" && strings.HasSuffix(file.Filename, ".whl") && file.URL != "" {
			return file, nil
		}
	}
	return pyPIFile{}, fmt.Errorf("no wheel artifact found for pypi version: %s", version)
}

func reployCacheDir() (string, error) {
	if dir := os.Getenv("REPLOY_CACHE_DIR"); dir != "" {
		return dir, nil
	}
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userCacheDir, "reploy"), nil
}

func cachePyPIWheel(cacheRoot string, packageName string, version string, file pyPIFile) (string, string, error) {
	if file.Filename == "" || filepath.Base(file.Filename) != file.Filename {
		return "", "", fmt.Errorf("invalid wheel filename: %s", file.Filename)
	}
	expectedSHA256 := strings.ToLower(file.Digests.SHA256)
	if expectedSHA256 != "" {
		path := pypiWheelCachePath(cacheRoot, packageName, version, expectedSHA256, file.Filename)
		if current, err := HashFile(path); err == nil && current == expectedSHA256 {
			return path, expectedSHA256, nil
		}
	}

	response, err := pyPIHTTPClient.Get(file.URL)
	if err != nil {
		return "", "", fmt.Errorf("download pypi wheel: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download pypi wheel: %s", response.Status)
	}
	tmpRoot := filepath.Join(cacheRoot, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", "", err
	}
	tempDir, err := os.MkdirTemp(tmpRoot, "wheel-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tempDir)
	tempPath := filepath.Join(tempDir, file.Filename)
	out, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", "", err
	}
	if _, err := io.Copy(out, response.Body); err != nil {
		out.Close()
		return "", "", err
	}
	if err := out.Close(); err != nil {
		return "", "", err
	}
	actualSHA256, err := HashFile(tempPath)
	if err != nil {
		return "", "", err
	}
	if expectedSHA256 != "" && actualSHA256 != expectedSHA256 {
		return "", "", fmt.Errorf("downloaded wheel sha256 mismatch for %s", file.Filename)
	}
	finalPath := pypiWheelCachePath(cacheRoot, packageName, version, actualSHA256, file.Filename)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return "", "", err
	}
	if current, err := HashFile(finalPath); err == nil && current == actualSHA256 {
		return finalPath, actualSHA256, nil
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", "", err
	}
	return finalPath, actualSHA256, nil
}

func pypiWheelCachePath(cacheRoot string, packageName string, version string, sha256 string, filename string) string {
	return filepath.Join(cacheRoot, "pypi", normalizePackageName(packageName), pypiVersionCacheKey(version), sha256, filename)
}

func pypiVersionCacheKey(version string) string {
	return "version-" + HashBytes([]byte(version))
}

func pypiBlueprintCacheDir(cacheRoot string, packageName string, version string, sha256 string, archiveDir string) string {
	return filepath.Join(
		cacheRoot,
		"blueprints",
		"pypi",
		normalizePackageName(packageName),
		pypiVersionCacheKey(version),
		sha256,
		filepath.FromSlash(archiveDir),
	)
}

func extractPackFromWheel(cacheRoot string, packageName string, version string, sha256 string, wheelPath string, blueprintPath string) (string, error) {
	cleanBlueprintPath, err := cleanArchiveSubdir(blueprintPath)
	if err != nil {
		return "", err
	}
	if !isBlueprintManifestPath(cleanBlueprintPath) {
		return "", fmt.Errorf("pypi blueprint path must point to a %s file: %s", BlueprintManifestGlob, blueprintPath)
	}
	archiveDir := filepath.ToSlash(filepath.Dir(cleanBlueprintPath))
	cacheDir := archiveDir
	if cacheDir == "." {
		cacheDir = "_root"
	}
	blueprintFilename := filepath.Base(cleanBlueprintPath)
	targetDir := pypiBlueprintCacheDir(cacheRoot, packageName, version, sha256, cacheDir)
	targetBlueprintPath := filepath.Join(targetDir, blueprintFilename)
	reader, err := zip.OpenReader(wheelPath)
	if err != nil {
		return "", fmt.Errorf("open wheel archive: %w", err)
	}
	defer reader.Close()
	blueprintContent, blueprintMode, err := readBlueprintFromWheel(reader.File, cleanBlueprintPath)
	if err != nil {
		return "", fmt.Errorf(
			"read blueprint from PyPI wheel %s==%s (%s): %w",
			packageName, version, filepath.Base(wheelPath), err,
		)
	}
	if info, err := os.Lstat(targetDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("cached PyPI blueprint path is not a directory: %s", targetDir)
		}
		if err := ensureCachedPyPIBlueprint(targetBlueprintPath, blueprintContent, blueprintMode); err != nil {
			return "", fmt.Errorf("validate cached PyPI blueprint %s: %w", targetBlueprintPath, err)
		}
		return targetBlueprintPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tmpRoot := filepath.Join(cacheRoot, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", err
	}
	tempDir, err := os.MkdirTemp(tmpRoot, "pack-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	foundBlueprint := false
	prefix := ""
	if archiveDir != "." {
		prefix = archiveDir + "/"
	}
	for _, file := range reader.File {
		if file.Name == archiveDir {
			continue
		}
		if prefix != "" && file.Name != archiveDir && !strings.HasPrefix(file.Name, prefix) {
			continue
		}
		relativePath := file.Name
		if prefix != "" {
			relativePath = strings.TrimPrefix(file.Name, prefix)
		}
		if relativePath == "" {
			continue
		}
		if file.Name == cleanBlueprintPath {
			foundBlueprint = true
		}
		cleanRelativePath, err := cleanArchiveSubdir(relativePath)
		if err != nil {
			return "", err
		}
		targetPath := filepath.Join(tempDir, filepath.FromSlash(cleanRelativePath))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return "", err
		}
		in, err := file.Open()
		if err != nil {
			return "", err
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, file.FileInfo().Mode().Perm())
		if err != nil {
			in.Close()
			return "", err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			out.Close()
			return "", err
		}
		if err := in.Close(); err != nil {
			out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
	}
	if !foundBlueprint {
		return "", fmt.Errorf("blueprint path not found in PyPI wheel %s==%s (%s): %s", packageName, version, filepath.Base(wheelPath), blueprintPath)
	}
	tempBlueprintPath := filepath.Join(tempDir, blueprintFilename)
	if _, err := os.Stat(tempBlueprintPath); err != nil {
		return "", fmt.Errorf("blueprint path not found in PyPI wheel %s==%s (%s): %s", packageName, version, filepath.Base(wheelPath), blueprintPath)
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tempDir, targetDir); err != nil {
		info, statErr := os.Lstat(targetDir)
		if statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if validationErr := ensureCachedPyPIBlueprint(targetBlueprintPath, blueprintContent, blueprintMode); validationErr == nil {
				return targetBlueprintPath, nil
			}
		}
		if statErr == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", fmt.Errorf("cached PyPI blueprint path is not a directory: %s", targetDir)
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", statErr
		}
		return "", err
	}
	if err := ensureCachedPyPIBlueprint(targetBlueprintPath, blueprintContent, blueprintMode); err != nil {
		return "", fmt.Errorf("validate extracted PyPI blueprint %s: %w", targetBlueprintPath, err)
	}
	return targetBlueprintPath, nil
}

func readBlueprintContentFromWheel(wheelPath string, blueprintPath string) ([]byte, error) {
	cleanBlueprintPath, err := cleanArchiveSubdir(blueprintPath)
	if err != nil {
		return nil, err
	}
	reader, err := zip.OpenReader(wheelPath)
	if err != nil {
		return nil, fmt.Errorf("open wheel archive: %w", err)
	}
	defer reader.Close()
	content, _, err := readBlueprintFromWheel(reader.File, cleanBlueprintPath)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func readBlueprintFromWheel(files []*zip.File, blueprintPath string) ([]byte, os.FileMode, error) {
	var blueprint *zip.File
	for _, file := range files {
		if file.Name != blueprintPath {
			continue
		}
		if blueprint != nil {
			return nil, 0, fmt.Errorf("wheel contains duplicate blueprint path: %s", blueprintPath)
		}
		blueprint = file
	}
	if blueprint == nil || blueprint.FileInfo().IsDir() {
		return nil, 0, fmt.Errorf("blueprint path not found: %s", blueprintPath)
	}
	in, err := blueprint.Open()
	if err != nil {
		return nil, 0, err
	}
	defer in.Close()
	content, err := io.ReadAll(in)
	if err != nil {
		return nil, 0, err
	}
	return content, blueprint.FileInfo().Mode().Perm(), nil
}

func ensureCachedPyPIBlueprint(path string, expected []byte, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err == nil && info.Mode().IsRegular() {
		current, hashErr := HashFile(path)
		if hashErr == nil && current == HashBytes(expected) {
			return nil
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := writeAtomicStateFile(path, expected, mode); err != nil {
		return err
	}
	info, err = os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("repaired cache entry is not a regular file")
	}
	current, err := HashFile(path)
	if err != nil {
		return err
	}
	if current != HashBytes(expected) {
		return fmt.Errorf("repaired cache entry does not match its verified wheel")
	}
	return nil
}

func cleanArchiveSubdir(path string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." || filepath.IsAbs(clean) {
		return "", fmt.Errorf("archive path must be relative: %s", path)
	}
	return clean, nil
}

func normalizePackageName(name string) string {
	return strings.ToLower(packageNameNormalizePattern.ReplaceAllString(name, "-"))
}
