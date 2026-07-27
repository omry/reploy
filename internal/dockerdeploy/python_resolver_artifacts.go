package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	pythonResolverInputContainerDir  = pythonprovider.ResolverInputDirectory
	pythonResolverOutputContainerDir = pythonprovider.ResolverOutputDirectory
)

// PreparedPythonResolverArtifacts separates immutable reusable wheels from
// the initially empty writable output of one disposable resolver.
type PreparedPythonResolverArtifacts struct {
	HostDir            string
	InputHostDir       string
	OutputHostDir      string
	InputContainerDir  string
	OutputContainerDir string
}

// StagePythonResolverWheelArtifact exposes one verified store wheel only after
// a retained sdist identity has selected it for reuse.
func StagePythonResolverWheelArtifact(
	store providerstore.Store,
	prepared PreparedPythonResolverArtifacts,
	artifact providerstore.ArtifactDescriptor,
) (err error) {
	if err := validatePreparedPythonResolverArtifactLayout(prepared); err != nil {
		return err
	}
	if err := artifact.Validate(); err != nil {
		return fmt.Errorf("stage Python resolver wheel: %w", err)
	}
	filename := filepath.Base(filepath.FromSlash(artifact.LogicalPath))
	if artifact.Kind != "wheel" || !strings.HasSuffix(strings.ToLower(filename), ".whl") {
		return fmt.Errorf("stage Python resolver artifact %q must be a wheel", artifact.LogicalPath)
	}
	blob, err := store.InspectArtifactPath(artifact)
	if err != nil {
		return fmt.Errorf("inspect staged Python resolver wheel %q: %w", artifact.LogicalPath, err)
	}
	if err := os.Chmod(prepared.InputHostDir, 0o700); err != nil {
		return fmt.Errorf("make Python resolver input writable for source wheel reuse: %w", err)
	}
	defer func() {
		if protectErr := os.Chmod(prepared.InputHostDir, 0o500); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("restore Python resolver input protection: %w", protectErr))
		}
	}()
	destination := filepath.Join(prepared.InputHostDir, filename)
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace staged Python resolver wheel %q: %w", filename, err)
	}
	if err := os.Link(blob, destination); err != nil {
		return fmt.Errorf("stage Python resolver wheel %q: %w", filename, err)
	}
	return nil
}

func replacePythonWheelByFilename(
	artifacts []providerstore.ArtifactDescriptor,
	replacement providerstore.ArtifactDescriptor,
) []providerstore.ArtifactDescriptor {
	byFilename := make(map[string]providerstore.ArtifactDescriptor, len(artifacts)+1)
	for _, artifact := range artifacts {
		byFilename[filepath.Base(filepath.FromSlash(artifact.LogicalPath))] = artifact
	}
	byFilename[filepath.Base(filepath.FromSlash(replacement.LogicalPath))] = replacement
	result := make([]providerstore.ArtifactDescriptor, 0, len(byFilename))
	for _, artifact := range byFilename {
		result = append(result, artifact)
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].LogicalPath < result[right].LogicalPath
	})
	return result
}

// StagePythonResolverSourceConstraints places the provider-owned constraints
// beside the verified wheels before the one pip invocation consumes them.
func StagePythonResolverSourceConstraints(
	prepared PreparedPythonResolverArtifacts,
	request providers.CanonicalProviderRequest,
	sources []providers.ResolvedSourceInput,
	reusable []providerstore.ArtifactDescriptor,
) error {
	if err := validatePreparedPythonResolverArtifacts(prepared); err != nil {
		return err
	}
	content, err := pythonprovider.WheelResolverSourceConstraints(request, sources, reusable)
	if err != nil {
		return err
	}
	if len(content) == 0 {
		return nil
	}
	if err := os.Chmod(prepared.InputHostDir, 0o700); err != nil {
		return fmt.Errorf("make Python resolver input writable for source constraints: %w", err)
	}
	constraintPath := filepath.Join(prepared.InputHostDir, filepath.Base(pythonprovider.ResolverSourceConstraintsPath))
	removeErr := os.Remove(constraintPath)
	if removeErr != nil && !os.IsNotExist(removeErr) {
		protectErr := os.Chmod(prepared.InputHostDir, 0o500)
		return errors.Join(fmt.Errorf("replace Python resolver source constraints: %w", removeErr), protectErr)
	}
	writeErr := os.WriteFile(constraintPath, content, 0o400)
	protectErr := os.Chmod(prepared.InputHostDir, 0o500)
	if writeErr != nil {
		return errors.Join(fmt.Errorf("write Python resolver source constraints: %w", writeErr), protectErr)
	}
	if protectErr != nil {
		return fmt.Errorf("restore Python resolver input protection: %w", protectErr)
	}
	return nil
}

// ResetPythonResolverOutput removes one provisional resolver result before a
// second pass or local source build. It accepts only regular files in the
// private output directory and leaves the directory itself in place.
func ResetPythonResolverOutput(prepared PreparedPythonResolverArtifacts) error {
	if err := validatePreparedPythonResolverArtifactLayout(prepared); err != nil {
		return err
	}
	entries, err := os.ReadDir(prepared.OutputHostDir)
	if err != nil {
		return fmt.Errorf("read Python resolver output for reset: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Python resolver output contains unsafe entry %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect Python resolver output %q for reset: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Python resolver output %q must be a regular file", entry.Name())
		}
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(prepared.OutputHostDir, entry.Name())); err != nil {
			return fmt.Errorf("reset Python resolver output %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func validatePreparedPythonResolverArtifacts(prepared PreparedPythonResolverArtifacts) error {
	if err := validatePreparedPythonResolverArtifactLayout(prepared); err != nil {
		return err
	}
	entries, err := os.ReadDir(prepared.OutputHostDir)
	if err != nil {
		return fmt.Errorf("read Python resolver artifact output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("Python resolver artifact output directory must be initially empty")
	}
	return nil
}

func validatePreparedPythonResolverArtifactLayout(prepared PreparedPythonResolverArtifacts) error {
	paths := []struct {
		name  string
		value string
	}{
		{name: "workspace", value: prepared.HostDir},
		{name: "input", value: prepared.InputHostDir},
		{name: "output", value: prepared.OutputHostDir},
	}
	for _, item := range paths {
		if item.value == "" || !filepath.IsAbs(item.value) || filepath.Clean(item.value) != item.value {
			return fmt.Errorf("Python resolver artifact %s path must be absolute and clean", item.name)
		}
		info, err := os.Lstat(item.value)
		if err != nil {
			return fmt.Errorf("inspect Python resolver artifact %s directory: %w", item.name, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Python resolver artifact %s must be a real directory", item.name)
		}
	}
	if filepath.Dir(prepared.InputHostDir) != prepared.HostDir || filepath.Base(prepared.InputHostDir) != "input" ||
		filepath.Dir(prepared.OutputHostDir) != prepared.HostDir || filepath.Base(prepared.OutputHostDir) != "output" {
		return fmt.Errorf("Python resolver artifact input and output must be direct workspace children")
	}
	if prepared.InputContainerDir != pythonResolverInputContainerDir || prepared.OutputContainerDir != pythonResolverOutputContainerDir {
		return fmt.Errorf("Python resolver artifact container paths do not match the provider-owned layout")
	}
	inputInfo, _ := os.Stat(prepared.InputHostDir)
	if inputInfo.Mode().Perm()&0o222 != 0 {
		return fmt.Errorf("Python resolver artifact input directory must not be writable")
	}
	outputInfo, _ := os.Stat(prepared.OutputHostDir)
	if outputInfo.Mode().Perm()&0o200 == 0 {
		return fmt.Errorf("Python resolver artifact output directory must be owner-writable")
	}
	return nil
}

// PreparePythonResolverArtifacts exposes verified store wheels as hardlinks
// in a read-only flat find-links directory. Resolver output is a separate,
// initially empty directory beneath the same deployment-owned store.
func PreparePythonResolverArtifacts(
	store providerstore.Store,
	reusable []providerstore.ArtifactDescriptor,
) (PreparedPythonResolverArtifacts, func(), error) {
	workspace, err := store.NewWorkspace("python-resolve-*")
	if err != nil {
		return PreparedPythonResolverArtifacts{}, func() {}, err
	}
	inputDir := filepath.Join(workspace, "input")
	outputDir := filepath.Join(workspace, "output")
	cleanup := func() {
		_ = os.Chmod(inputDir, 0o700)
		makePythonResolverWorkspaceRemovable(workspace)
		_ = os.RemoveAll(workspace)
	}
	for _, directory := range []string{inputDir, outputDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			cleanup()
			return PreparedPythonResolverArtifacts{}, func() {}, fmt.Errorf("create Python resolver artifact directory: %w", err)
		}
	}

	artifacts := append([]providerstore.ArtifactDescriptor{}, reusable...)
	sort.Slice(artifacts, func(left int, right int) bool {
		return artifacts[left].LogicalPath < artifacts[right].LogicalPath
	})
	filenames := map[string]string{}
	for _, artifact := range artifacts {
		if err := artifact.Validate(); err != nil {
			cleanup()
			return PreparedPythonResolverArtifacts{}, func() {}, fmt.Errorf("Python resolver reusable artifact: %w", err)
		}
		filename := filepath.Base(filepath.FromSlash(artifact.LogicalPath))
		if artifact.Kind != "wheel" || !strings.HasSuffix(strings.ToLower(filename), ".whl") {
			cleanup()
			return PreparedPythonResolverArtifacts{}, func() {}, fmt.Errorf("Python resolver reusable artifact %q must be a wheel", artifact.LogicalPath)
		}
		if prior, found := filenames[filename]; found {
			cleanup()
			return PreparedPythonResolverArtifacts{}, func() {}, fmt.Errorf("Python resolver reusable artifacts %q and %q have the same wheel filename", prior, artifact.LogicalPath)
		}
		source, err := store.InspectArtifactPath(artifact)
		if err != nil {
			cleanup()
			return PreparedPythonResolverArtifacts{}, func() {}, fmt.Errorf("inspect Python resolver reusable wheel %q: %w", artifact.LogicalPath, err)
		}
		if err := os.Link(source, filepath.Join(inputDir, filename)); err != nil {
			cleanup()
			return PreparedPythonResolverArtifacts{}, func() {}, fmt.Errorf("stage Python resolver reusable wheel %q: %w", artifact.LogicalPath, err)
		}
		filenames[filename] = artifact.LogicalPath
	}
	if err := os.Chmod(inputDir, 0o500); err != nil {
		cleanup()
		return PreparedPythonResolverArtifacts{}, func() {}, fmt.Errorf("protect Python resolver input directory: %w", err)
	}
	return PreparedPythonResolverArtifacts{
		HostDir: workspace, InputHostDir: inputDir, OutputHostDir: outputDir,
		InputContainerDir: pythonResolverInputContainerDir, OutputContainerDir: pythonResolverOutputContainerDir,
	}, cleanup, nil
}

// VerifyPythonResolverArtifacts performs the content check only when the
// resolver will actually consume its staged inputs.
func VerifyPythonResolverArtifacts(prepared PreparedPythonResolverArtifacts, reusable []providerstore.ArtifactDescriptor) error {
	if err := validatePreparedPythonResolverArtifacts(prepared); err != nil {
		return err
	}
	for _, artifact := range reusable {
		path := filepath.Join(prepared.InputHostDir, filepath.Base(filepath.FromSlash(artifact.LogicalPath)))
		if err := providerstore.VerifyArtifactFile(path, artifact); err != nil {
			return fmt.Errorf("verify Python resolver reusable wheel %q: %w", artifact.LogicalPath, err)
		}
	}
	return nil
}

// FilterVerifiedPythonResolverArtifacts removes corrupt staged candidates so
// the fresh resolver cannot select them through find-links. Missing or corrupt
// reusable content is a cache miss, not a resolver failure.
func FilterVerifiedPythonResolverArtifacts(
	prepared PreparedPythonResolverArtifacts,
	reusable []providerstore.ArtifactDescriptor,
) ([]providerstore.ArtifactDescriptor, error) {
	if err := validatePreparedPythonResolverArtifacts(prepared); err != nil {
		return nil, err
	}
	verified := make([]providerstore.ArtifactDescriptor, 0, len(reusable))
	invalid := make([]string, 0)
	for _, artifact := range reusable {
		filename := filepath.Base(filepath.FromSlash(artifact.LogicalPath))
		path := filepath.Join(prepared.InputHostDir, filename)
		if err := providerstore.VerifyArtifactFile(path, artifact); err != nil {
			invalid = append(invalid, path)
			continue
		}
		verified = append(verified, artifact)
	}
	if len(invalid) == 0 {
		return verified, nil
	}
	if err := os.Chmod(prepared.InputHostDir, 0o700); err != nil {
		return nil, fmt.Errorf("make Python resolver input writable for invalid-candidate removal: %w", err)
	}
	var removalErr error
	for _, path := range invalid {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			removalErr = errors.Join(removalErr, fmt.Errorf("remove invalid Python resolver input %q: %w", filepath.Base(path), err))
		}
	}
	if err := os.Chmod(prepared.InputHostDir, 0o500); err != nil {
		removalErr = errors.Join(removalErr, fmt.Errorf("restore Python resolver input protection: %w", err))
	}
	if removalErr != nil {
		return nil, removalErr
	}
	return verified, nil
}
