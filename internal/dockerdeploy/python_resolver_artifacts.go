package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

func validatePreparedPythonResolverArtifacts(prepared PreparedPythonResolverArtifacts) error {
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
	entries, err := os.ReadDir(prepared.OutputHostDir)
	if err != nil {
		return fmt.Errorf("read Python resolver artifact output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("Python resolver artifact output directory must be initially empty")
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
