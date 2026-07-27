package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

const (
	stagedControlManifestSchemaV1 = "staged-control-v1"
	stagedControlManifestPathV1   = ".reploy/staged-control.json"
)

type stagedControlManifestFileV1 struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type stagedControlManifestV1 struct {
	Schema string                        `json:"schema"`
	Files  []stagedControlManifestFileV1 `json:"files"`
}

type stagedControlFileV1 struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
	Digest  string
}

type stagedControlPlanV1 struct {
	DeploymentDir string
	Files         []stagedControlFileV1
	Remove        []stagedControlManifestFileV1
	Manifest      []byte
}

func planStagedControlSurfaceV1(deploymentDir string, document blueprint.Document) (stagedControlPlanV1, error) {
	if strings.TrimSpace(deploymentDir) == "" {
		return stagedControlPlanV1{}, fmt.Errorf("stage control surface requires a deployment directory")
	}
	controlScript := strings.TrimSpace(document.Environment.ControlScript)
	if controlScript == "" {
		return stagedControlPlanV1{}, fmt.Errorf("stage control surface requires a control script name")
	}
	if filepath.Base(controlScript) != controlScript || controlScript == "." || controlScript == ".." {
		return stagedControlPlanV1{}, fmt.Errorf("stage control script name %q must be a file name", controlScript)
	}
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return stagedControlPlanV1{}, fmt.Errorf("resolve staging directory for control surface: %w", err)
	}
	runtimePath, err := embeddedRuntimeExecutable()
	if err != nil {
		return stagedControlPlanV1{}, fmt.Errorf("locate staged Reploy runtime: %w", err)
	}
	runtimeContent, err := os.ReadFile(runtimePath)
	if err != nil {
		return stagedControlPlanV1{}, fmt.Errorf("read staged Reploy runtime: %w", err)
	}
	spec := controlScriptSpec{
		Mode:          controlScriptModeStaged,
		TargetDir:     absoluteDir,
		AppID:         document.Environment.ID,
		ControlScript: controlScript,
	}
	files := []stagedControlFileV1{
		newStagedControlFileV1(filepath.ToSlash(embeddedRuntimeFileName()), runtimeContent, 0o755),
		newStagedControlFileV1(controlScript, []byte(renderControlScript(spec)), 0o755),
	}
	if currentHostPlatform().GOOS == "windows" {
		powerShellName := controlScript + ".ps1"
		spec.ControlScript = powerShellName
		files = append(files, newStagedControlFileV1(
			powerShellName, []byte(renderPowerShellControlScript(spec)), 0o755,
		))
	}
	sort.Slice(files, func(left int, right int) bool { return files[left].Path < files[right].Path })
	for index, file := range files {
		if err := validateStagedControlRelativePathV1(file.Path); err != nil {
			return stagedControlPlanV1{}, err
		}
		if index > 0 && files[index-1].Path == file.Path {
			return stagedControlPlanV1{}, fmt.Errorf("stage control files repeat path %q", file.Path)
		}
	}
	previous, found, err := readStagedControlManifestV1(absoluteDir)
	if err != nil {
		return stagedControlPlanV1{}, err
	}
	previousFiles := map[string]stagedControlManifestFileV1{}
	if found {
		for _, file := range previous.Files {
			previousFiles[file.Path] = file
		}
	}
	adoptInstalled, err := stagedControlSurfaceMatchesInstalledV1(absoluteDir, document)
	if err != nil {
		return stagedControlPlanV1{}, err
	}
	nextFiles := make(map[string]stagedControlFileV1, len(files))
	for _, file := range files {
		nextFiles[file.Path] = file
		path := filepath.Join(absoluteDir, filepath.FromSlash(file.Path))
		if _, managed := previousFiles[file.Path]; !managed && !adoptInstalled {
			if err := requireMissingOrMatchingStagedControlFileV1(path, file); err != nil {
				return stagedControlPlanV1{}, err
			}
		}
	}
	var remove []stagedControlManifestFileV1
	for path, previousFile := range previousFiles {
		if _, retained := nextFiles[path]; retained {
			continue
		}
		absolutePath := filepath.Join(absoluteDir, filepath.FromSlash(path))
		if err := requireMissingOrDigestStagedControlFileV1(absolutePath, previousFile.Digest); err != nil {
			return stagedControlPlanV1{}, fmt.Errorf("remove obsolete staged control file %q: %w", path, err)
		}
		remove = append(remove, previousFile)
	}
	sort.Slice(remove, func(left int, right int) bool { return remove[left].Path < remove[right].Path })
	manifest := stagedControlManifestV1{Schema: stagedControlManifestSchemaV1}
	for _, file := range files {
		manifest.Files = append(manifest.Files, stagedControlManifestFileV1{Path: file.Path, Digest: file.Digest})
	}
	manifestContent, err := canonical.Marshal(manifest)
	if err != nil {
		return stagedControlPlanV1{}, fmt.Errorf("encode staged control manifest: %w", err)
	}
	return stagedControlPlanV1{
		DeploymentDir: absoluteDir,
		Files:         files,
		Remove:        remove,
		Manifest:      manifestContent,
	}, nil
}

func applyStagedControlSurfaceV1(plan stagedControlPlanV1) (bool, error) {
	changed := false
	for _, file := range plan.Files {
		path := filepath.Join(plan.DeploymentDir, filepath.FromSlash(file.Path))
		fileChanged, err := replaceStagedControlFileV1(path, file.Content, file.Mode)
		if err != nil {
			return changed, fmt.Errorf("write staged control file %q: %w", file.Path, err)
		}
		changed = changed || fileChanged
	}
	for _, file := range plan.Remove {
		path := filepath.Join(plan.DeploymentDir, filepath.FromSlash(file.Path))
		if err := requireMissingOrDigestStagedControlFileV1(path, file.Digest); err != nil {
			return changed, fmt.Errorf("recheck obsolete staged control file %q: %w", file.Path, err)
		}
		if err := os.Remove(path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return changed, fmt.Errorf("remove obsolete staged control file %q: %w", file.Path, err)
			}
		} else {
			changed = true
			if err := syncProviderInstallDirectory(filepath.Dir(path)); err != nil {
				return changed, fmt.Errorf("sync obsolete staged control file removal %q: %w", file.Path, err)
			}
		}
	}
	manifestPath := filepath.Join(plan.DeploymentDir, filepath.FromSlash(stagedControlManifestPathV1))
	manifestChanged, err := replaceStagedControlFileV1(manifestPath, plan.Manifest, 0o600)
	if err != nil {
		return changed, fmt.Errorf("write staged control manifest: %w", err)
	}
	return changed || manifestChanged, nil
}

func syncStagedControlSurfaceV1(deploymentDir string, document blueprint.Document) (bool, error) {
	plan, err := planStagedControlSurfaceV1(deploymentDir, document)
	if err != nil {
		return false, err
	}
	return applyStagedControlSurfaceV1(plan)
}

func stagedControlSurfaceMatchesInstalledV1(deploymentDir string, document blueprint.Document) (bool, error) {
	controlScript := document.Environment.ControlScript
	spec := controlScriptSpec{
		Mode:          controlScriptModeDeployed,
		TargetDir:     deploymentDir,
		AppID:         document.Environment.ID,
		ControlScript: controlScript,
	}
	expected := map[string][]byte{
		controlScript: []byte(renderControlScript(spec)),
	}
	if currentHostPlatform().GOOS == "windows" {
		powerShellName := controlScript + ".ps1"
		spec.ControlScript = powerShellName
		expected[powerShellName] = []byte(renderPowerShellControlScript(spec))
	}
	for relative, content := range expected {
		path := filepath.Join(deploymentDir, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect installed control file %q: %w", relative, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("read installed control file %q: %w", relative, err)
		}
		if !bytes.Equal(current, content) {
			return false, nil
		}
	}
	runtimePath := filepath.Join(deploymentDir, filepath.FromSlash(embeddedRuntimeFileName()))
	info, err := os.Lstat(runtimePath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect installed control runtime: %w", err)
	}
	return info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0, nil
}

func syncCurrentStagedControlSurfaceV1(ctx context.Context, deploymentDir string) (changed bool, err error) {
	if ctx == nil {
		return false, fmt.Errorf("sync current staged control surface requires a context")
	}
	operation, err := deploy.AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		return false, err
	}
	defer func() {
		err = errors.Join(err, operation.Unlock())
	}()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return false, fmt.Errorf("read staged state for control surface: %w", err)
	}
	if !found || state.Deployment != nil {
		return false, fmt.Errorf("sync control surface requires a staged state-v1 deployment")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return false, fmt.Errorf("decode staged blueprint for control surface: %w", err)
	}
	return syncStagedControlSurfaceV1(deploymentDir, document)
}

func newStagedControlFileV1(path string, content []byte, mode fs.FileMode) stagedControlFileV1 {
	return stagedControlFileV1{
		Path: path, Content: append([]byte(nil), content...), Mode: mode,
		Digest: stagedControlDigestV1(content),
	}
}

func readStagedControlManifestV1(deploymentDir string) (stagedControlManifestV1, bool, error) {
	path := filepath.Join(deploymentDir, filepath.FromSlash(stagedControlManifestPathV1))
	if err := validateProviderInstallDirectoryAncestorsV1(filepath.Dir(path)); err != nil {
		return stagedControlManifestV1{}, false, fmt.Errorf("validate staged control manifest path: %w", err)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return stagedControlManifestV1{}, false, nil
	}
	if err != nil {
		return stagedControlManifestV1{}, false, fmt.Errorf("inspect staged control manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return stagedControlManifestV1{}, false, fmt.Errorf("staged control manifest must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return stagedControlManifestV1{}, false, fmt.Errorf("read staged control manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var manifest stagedControlManifestV1
	if err := decoder.Decode(&manifest); err != nil {
		return stagedControlManifestV1{}, false, fmt.Errorf("decode staged control manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return stagedControlManifestV1{}, false, fmt.Errorf("staged control manifest contains trailing JSON")
		}
		return stagedControlManifestV1{}, false, fmt.Errorf("decode staged control manifest trailer: %w", err)
	}
	if manifest.Schema != stagedControlManifestSchemaV1 {
		return stagedControlManifestV1{}, false, fmt.Errorf(
			"staged control manifest schema %q is unsupported; expected %q",
			manifest.Schema, stagedControlManifestSchemaV1,
		)
	}
	for index, file := range manifest.Files {
		if err := validateStagedControlRelativePathV1(file.Path); err != nil {
			return stagedControlManifestV1{}, false, fmt.Errorf("staged control manifest file %d: %w", index, err)
		}
		if !validStagedControlDigestV1(file.Digest) {
			return stagedControlManifestV1{}, false, fmt.Errorf(
				"staged control manifest file %q has invalid digest %q", file.Path, file.Digest,
			)
		}
		if index > 0 && manifest.Files[index-1].Path >= file.Path {
			return stagedControlManifestV1{}, false, fmt.Errorf("staged control manifest files must be unique and sorted by path")
		}
	}
	canonicalContent, err := canonical.Marshal(manifest)
	if err != nil {
		return stagedControlManifestV1{}, false, fmt.Errorf("encode staged control manifest: %w", err)
	}
	if !bytes.Equal(content, canonicalContent) {
		return stagedControlManifestV1{}, false, fmt.Errorf("staged control manifest is not canonical JSON")
	}
	return manifest, true, nil
}

func validateStagedControlRelativePathV1(path string) error {
	if path == "" || filepath.IsAbs(filepath.FromSlash(path)) || filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path {
		return fmt.Errorf("staged control path %q must be a clean relative path", path)
	}
	if path == ".." || strings.HasPrefix(path, "../") {
		return fmt.Errorf("staged control path %q escapes the staging directory", path)
	}
	return nil
}

func requireMissingOrMatchingStagedControlFileV1(path string, expected stagedControlFileV1) error {
	if err := validateProviderInstallDirectoryAncestorsV1(filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect unmanaged staged control path %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace unmanaged staged control path %q", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read unmanaged staged control path %q: %w", path, err)
	}
	if !bytes.Equal(content, expected.Content) {
		return fmt.Errorf("refusing to replace unmanaged staged control file %q", path)
	}
	return nil
}

func requireMissingOrDigestStagedControlFileV1(path string, expectedDigest string) error {
	if err := validateProviderInstallDirectoryAncestorsV1(filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed path is no longer a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if stagedControlDigestV1(content) != expectedDigest {
		return fmt.Errorf("managed file was modified; refusing to remove it")
	}
	return nil
}

func replaceStagedControlFileV1(path string, content []byte, mode fs.FileMode) (bool, error) {
	if err := validateProviderInstallDirectoryAncestorsV1(filepath.Dir(path)); err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("destination must be a regular file: %s", path)
		}
		current, readErr := os.ReadFile(path)
		if readErr != nil {
			return false, readErr
		}
		if bytes.Equal(current, content) && providerInstallFileModeMatches(info.Mode(), mode) {
			return false, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	prepared, err := prepareProviderInstallFileCandidatesV1([]providerInstallFileCandidateV1{{
		Path: path, Content: content, Mode: mode,
	}})
	if err != nil {
		return false, err
	}
	if err := prepared.Publish(); err != nil {
		return false, errors.Join(err, prepared.Cleanup())
	}
	if err := prepared.Cleanup(); err != nil {
		return true, err
	}
	return true, nil
}

func stagedControlDigestV1(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validStagedControlDigestV1(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && strings.ToLower(value) == value
}
