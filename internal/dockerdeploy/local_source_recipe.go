package dockerdeploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/toolrequest"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const (
	LocalSourceRecipeFilename               = ".reploy.yaml"
	LocalSourceRecipeSchemaV1               = 1
	LocalSourceRecipeIdentitySchemaV1       = "local-source-build-recipe-v1"
	PythonBuildTypePEP517                   = pythonprovider.SourceBuildTypePEP517
	PythonBuildTypeSetuptoolsLegacy         = pythonprovider.SourceBuildTypeLegacy
	localSourceRecipeMaximumBytes     int64 = 64 << 10
)

// PythonLocalSourceRecipeV1 is normalized, path-free build metadata read from
// an immutable selected local-source snapshot. Found=false means the project
// did not opt into the recipe contract.
type PythonLocalSourceRecipeV1 struct {
	Found        bool
	Project      string
	Build        string
	Requirements []toolrequest.CanonicalRequirementGroupV1
	Digest       canonical.Digest
}

type localSourceRecipeSyntaxV1 struct {
	Schema   int                    `yaml:"schema"`
	Project  string                 `yaml:"project"`
	Type     string                 `yaml:"type"`
	Build    string                 `yaml:"build"`
	Requires []toolrequest.SyntaxV1 `yaml:"requires"`
}

type localSourceRecipeIdentityV1 struct {
	Schema       string                                    `json:"schema"`
	Project      string                                    `json:"project"`
	Type         string                                    `json:"type"`
	Build        string                                    `json:"build"`
	Requirements []toolrequest.CanonicalRequirementGroupV1 `json:"requirements"`
}

// ReadPythonLocalSourceRecipeV1 reads only the selected immutable snapshot.
// It never consults an unselected override or the original live checkout.
func ReadPythonLocalSourceRecipeV1(
	sourceDir string,
	distribution string,
) (PythonLocalSourceRecipeV1, error) {
	if sourceDir == "" || !filepath.IsAbs(sourceDir) || filepath.Clean(sourceDir) != sourceDir {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf("local source recipe directory must be absolute and clean")
	}
	if pythonprovider.NormalizeDistributionName(distribution) != distribution || distribution == "" {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf("local source recipe package must be a normalized Python distribution")
	}
	filename := filepath.Join(sourceDir, LocalSourceRecipeFilename)
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return PythonLocalSourceRecipeV1{
			Found: false, Requirements: []toolrequest.CanonicalRequirementGroupV1{},
		}, nil
	}
	if err != nil {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf("inspect local source recipe for %q: %w", distribution, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe for %q must be a regular file", distribution,
		)
	}
	if info.Size() > localSourceRecipeMaximumBytes {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe for %q exceeds %d bytes", distribution, localSourceRecipeMaximumBytes,
		)
	}
	file, err := os.Open(filename)
	if err != nil {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf("open local source recipe for %q: %w", distribution, err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, localSourceRecipeMaximumBytes+1))
	if err != nil {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf("read local source recipe for %q: %w", distribution, err)
	}
	if int64(len(content)) > localSourceRecipeMaximumBytes {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe for %q exceeds %d bytes", distribution, localSourceRecipeMaximumBytes,
		)
	}
	syntax, err := decodeLocalSourceRecipeV1(content)
	if err != nil {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf("local source recipe for %q: %w", distribution, err)
	}
	recipe, err := normalizePythonLocalSourceRecipeV1(syntax, distribution)
	if err != nil {
		return PythonLocalSourceRecipeV1{}, err
	}
	if err := validatePythonLocalSourceBuildLayoutV1(sourceDir, recipe.Build); err != nil {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe for %q build %q: %w", distribution, recipe.Build, err,
		)
	}
	return recipe, nil
}

func decodeLocalSourceRecipeV1(content []byte) (localSourceRecipeSyntaxV1, error) {
	var syntax localSourceRecipeSyntaxV1
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&syntax); err != nil {
		return localSourceRecipeSyntaxV1{}, fmt.Errorf("decode: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return localSourceRecipeSyntaxV1{}, fmt.Errorf("decode: %w", err)
		}
		return localSourceRecipeSyntaxV1{}, fmt.Errorf("multiple YAML documents are not supported")
	}
	return syntax, nil
}

func normalizePythonLocalSourceRecipeV1(
	syntax localSourceRecipeSyntaxV1,
	distribution string,
) (PythonLocalSourceRecipeV1, error) {
	if syntax.Schema != LocalSourceRecipeSchemaV1 {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe for %q schema must be %d", distribution, LocalSourceRecipeSchemaV1,
		)
	}
	if syntax.Project == "" || strings.TrimSpace(syntax.Project) != syntax.Project ||
		pythonprovider.NormalizeDistributionName(syntax.Project) != syntax.Project {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe project %q must be a normalized Python distribution", syntax.Project,
		)
	}
	if syntax.Project != distribution {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe project %q does not match selected package %q", syntax.Project, distribution,
		)
	}
	if syntax.Type != "python" {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe for %q type must be %q", distribution, "python",
		)
	}
	switch syntax.Build {
	case PythonBuildTypePEP517, PythonBuildTypeSetuptoolsLegacy:
	default:
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe for %q has unsupported Python build type %q", distribution, syntax.Build,
		)
	}
	if syntax.Requires == nil {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf(
			"local source recipe for %q requires must use an array", distribution,
		)
	}
	set, err := toolrequest.NormalizeAndMergeV1(
		syntax.Requires, "source-builder:"+distribution, "build", "requires",
	)
	if err != nil {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf("local source recipe for %q: %w", distribution, err)
	}
	identity := localSourceRecipeIdentityV1{
		Schema: LocalSourceRecipeIdentitySchemaV1, Project: distribution,
		Type: "python", Build: syntax.Build, Requirements: append([]toolrequest.CanonicalRequirementGroupV1{}, set.Groups...),
	}
	digest, err := canonical.Sum("local-source-build-recipe", LocalSourceRecipeIdentitySchemaV1, identity)
	if err != nil {
		return PythonLocalSourceRecipeV1{}, fmt.Errorf("digest local source recipe for %q: %w", distribution, err)
	}
	return PythonLocalSourceRecipeV1{
		Found: true, Project: distribution, Build: syntax.Build,
		Requirements: set.Groups, Digest: digest,
	}, nil
}

func validatePythonLocalSourceBuildLayoutV1(sourceDir string, buildType string) error {
	pyproject := filepath.Join(sourceDir, "pyproject.toml")
	setupPy := filepath.Join(sourceDir, "setup.py")
	switch buildType {
	case PythonBuildTypePEP517:
		content, err := readRegularLocalBuildFileV1(pyproject)
		if err != nil {
			return fmt.Errorf("pep517 requires a regular pyproject.toml: %w", err)
		}
		var document map[string]any
		if err := toml.Unmarshal(content, &document); err != nil {
			return fmt.Errorf("pyproject.toml is invalid: %w", err)
		}
		if _, found := document["build-system"]; !found {
			return fmt.Errorf("pep517 requires pyproject.toml with [build-system]")
		}
	case PythonBuildTypeSetuptoolsLegacy:
		if _, err := readRegularLocalBuildFileV1(setupPy); err != nil {
			return fmt.Errorf("setuptools-legacy requires a regular setup.py: %w", err)
		}
		content, err := readOptionalRegularLocalBuildFileV1(pyproject)
		if err != nil {
			return err
		}
		if content != nil {
			var document map[string]any
			if err := toml.Unmarshal(content, &document); err != nil {
				return fmt.Errorf("pyproject.toml is invalid: %w", err)
			}
			if _, found := document["build-system"]; found {
				return fmt.Errorf("setuptools-legacy rejects pyproject.toml with [build-system]")
			}
		}
	default:
		return fmt.Errorf("unsupported Python build type %q", buildType)
	}
	return nil
}

func readRegularLocalBuildFileV1(filename string) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%q must be a regular file", filepath.Base(filename))
	}
	return os.ReadFile(filename)
}

func readOptionalRegularLocalBuildFileV1(filename string) ([]byte, error) {
	content, err := readRegularLocalBuildFileV1(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return content, err
}
