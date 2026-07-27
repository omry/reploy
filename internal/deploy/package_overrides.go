package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omry/reploy/internal/blueprint"
	"gopkg.in/yaml.v3"
)

const PackageOverridesFilename = "package-overrides.yaml"
const PackageOverrideIntentSchemaV1 = "package-override-intent-v1"

// PackageOverridesV1 is the explicit, staging-only developer intent stored
// beside a staged deployment. It is not part of the published blueprint.
type PackageOverridesV1 struct {
	Environment PackageOverridesEnvironmentV1 `yaml:"environment"`
}

type PackageOverridesEnvironmentV1 struct {
	ID               string                                        `yaml:"id"`
	Vars             map[string]any                                `yaml:"vars,omitempty"`
	PackageOverrides map[string]map[string]PackageOverrideChoiceV1 `yaml:"package_overrides"`
}

// PackageOverrideChoiceV1 selects exactly one local source or exact upstream
// version. A mapping never requests installation by itself.
type PackageOverrideChoiceV1 struct {
	Path    string `yaml:"path,omitempty"`
	Version string `yaml:"version,omitempty"`
}

// ResolvedPackageOverridesV1 contains interpolated, normalized lookup keys and
// lexical absolute local paths. Resolving it performs no filesystem access so
// unused mappings remain completely unobserved.
type ResolvedPackageOverridesV1 struct {
	EnvironmentID string
	Providers     map[string]map[string]ResolvedPackageOverrideChoiceV1
}

type ResolvedPackageOverrideChoiceV1 struct {
	Path    string
	Version string
}

// PackageOverrideIntentV1 is the path-free build input retained in the lock.
// Local paths are deliberately represented only as kind=local; selected
// content is bound separately by source-manifest and wheel digests.
type PackageOverrideIntentV1 struct {
	Schema        string                          `json:"schema"`
	EnvironmentID string                          `json:"environment_id"`
	Choices       []PackageOverrideIntentChoiceV1 `json:"choices"`
}

type PackageOverrideIntentChoiceV1 struct {
	Provider string `json:"provider"`
	Package  string `json:"package"`
	Kind     string `json:"kind"`
	Version  string `json:"version"`
}

func EmptyPackageOverridesV1(environmentID string) PackageOverridesV1 {
	return PackageOverridesV1{Environment: PackageOverridesEnvironmentV1{
		ID:               environmentID,
		Vars:             map[string]any{},
		PackageOverrides: map[string]map[string]PackageOverrideChoiceV1{},
	}}
}

// DecodePackageOverridesV1 strictly decodes one YAML document. Provider-owned
// package identities are normalized later, when the provider is known.
func DecodePackageOverridesV1(content []byte) (PackageOverridesV1, error) {
	var overrides PackageOverridesV1
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&overrides); err != nil {
		return PackageOverridesV1{}, fmt.Errorf("decode package overrides: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return PackageOverridesV1{}, fmt.Errorf("decode package overrides: %w", err)
		}
		return PackageOverridesV1{}, fmt.Errorf("decode package overrides: multiple YAML documents are not supported")
	}
	if err := ValidatePackageOverridesV1(overrides); err != nil {
		return PackageOverridesV1{}, err
	}
	return overrides, nil
}

func EncodePackageOverridesV1(overrides PackageOverridesV1) ([]byte, error) {
	if err := ValidatePackageOverridesV1(overrides); err != nil {
		return nil, err
	}
	content, err := yaml.Marshal(overrides)
	if err != nil {
		return nil, fmt.Errorf("encode package overrides: %w", err)
	}
	return content, nil
}

func ValidatePackageOverridesV1(overrides PackageOverridesV1) error {
	environment := overrides.Environment
	if err := blueprint.ValidateEnvironmentID("package overrides environment.id", environment.ID); err != nil {
		return err
	}
	if _, err := blueprint.ResolveEnvironmentVariables(environment.Vars); err != nil {
		return fmt.Errorf("package overrides variables: %w", err)
	}
	if environment.PackageOverrides == nil {
		return fmt.Errorf("package overrides environment.package_overrides must use a mapping")
	}
	providers := sortedKeys(environment.PackageOverrides)
	for _, provider := range providers {
		if provider != strings.TrimSpace(provider) {
			return fmt.Errorf("package override provider %q must not contain surrounding whitespace", provider)
		}
		if err := blueprint.ValidateProviderIdentifier("package override provider", provider); err != nil {
			return err
		}
		packages := environment.PackageOverrides[provider]
		if packages == nil {
			return fmt.Errorf("package overrides for provider %q must use a mapping", provider)
		}
		for _, packageID := range sortedKeys(packages) {
			if err := validatePackageOverrideIdentifier(provider, packageID); err != nil {
				return err
			}
			choice := packages[packageID]
			pathValue := strings.TrimSpace(choice.Path)
			version := strings.TrimSpace(choice.Version)
			if (pathValue == "") == (version == "") {
				return fmt.Errorf("package override %s.%s must select exactly one of path or version", provider, packageID)
			}
			if choice.Path != pathValue {
				return fmt.Errorf("package override %s.%s path must not contain surrounding whitespace", provider, packageID)
			}
			if choice.Version != version {
				return fmt.Errorf("package override %s.%s version must not contain surrounding whitespace", provider, packageID)
			}
			if pathValue != "" && containsControl(pathValue) {
				return fmt.Errorf("package override %s.%s path must not contain control characters", provider, packageID)
			}
			if version != "" && (strings.HasPrefix(version, "-") || containsControl(version)) {
				return fmt.Errorf("package override %s.%s version must be plain version text", provider, packageID)
			}
		}
	}
	return nil
}

// ResolvePackageOverridesV1 interpolates sidecar variables and creates stable
// provider/package lookup maps. normalizePackage owns ecosystem-specific
// package-name canonicalization and supported-choice validation.
func ResolvePackageOverridesV1(
	overrides PackageOverridesV1,
	sidecarDir string,
	normalizePackage func(provider string, packageID string, choice PackageOverrideChoiceV1) (string, error),
) (ResolvedPackageOverridesV1, error) {
	if err := ValidatePackageOverridesV1(overrides); err != nil {
		return ResolvedPackageOverridesV1{}, err
	}
	if sidecarDir == "" || !filepath.IsAbs(sidecarDir) || filepath.Clean(sidecarDir) != sidecarDir {
		return ResolvedPackageOverridesV1{}, fmt.Errorf("package overrides directory must be an absolute clean path")
	}
	if normalizePackage == nil && len(overrides.Environment.PackageOverrides) != 0 {
		return ResolvedPackageOverridesV1{}, fmt.Errorf("package override provider validation is unavailable")
	}
	variables, err := blueprint.ResolveEnvironmentVariables(overrides.Environment.Vars)
	if err != nil {
		return ResolvedPackageOverridesV1{}, fmt.Errorf("package overrides variables: %w", err)
	}
	resolved := ResolvedPackageOverridesV1{
		EnvironmentID: overrides.Environment.ID,
		Providers:     map[string]map[string]ResolvedPackageOverrideChoiceV1{},
	}
	for _, provider := range sortedKeys(overrides.Environment.PackageOverrides) {
		resolvedPackages := map[string]ResolvedPackageOverrideChoiceV1{}
		owners := map[string]string{}
		for _, packageID := range sortedKeys(overrides.Environment.PackageOverrides[provider]) {
			choice := overrides.Environment.PackageOverrides[provider][packageID]
			normalized, err := normalizePackage(provider, packageID, choice)
			if err != nil {
				return ResolvedPackageOverridesV1{}, fmt.Errorf("package override %s.%s: %w", provider, packageID, err)
			}
			if normalized == "" {
				return ResolvedPackageOverridesV1{}, fmt.Errorf("package override %s.%s normalized to an empty package identifier", provider, packageID)
			}
			if prior, found := owners[normalized]; found {
				return ResolvedPackageOverridesV1{}, fmt.Errorf(
					"package overrides %s.%s and %s.%s both normalize to %q",
					provider, prior, provider, packageID, normalized,
				)
			}
			owners[normalized] = packageID

			resolvedChoice := ResolvedPackageOverrideChoiceV1{Version: choice.Version}
			if choice.Path != "" {
				interpolated, err := blueprint.ResolveEnvironmentVariableString(choice.Path, variables)
				if err != nil {
					return ResolvedPackageOverridesV1{}, fmt.Errorf("package override %s.%s path: %w", provider, packageID, err)
				}
				if interpolated == "" || containsControl(interpolated) {
					return ResolvedPackageOverridesV1{}, fmt.Errorf("package override %s.%s path resolved to invalid text", provider, packageID)
				}
				if !filepath.IsAbs(interpolated) {
					interpolated = filepath.Join(sidecarDir, filepath.FromSlash(interpolated))
				}
				absolute, err := filepath.Abs(interpolated)
				if err != nil {
					return ResolvedPackageOverridesV1{}, fmt.Errorf("package override %s.%s path: %w", provider, packageID, err)
				}
				resolvedChoice.Path = filepath.Clean(absolute)
			}
			resolvedPackages[normalized] = resolvedChoice
		}
		resolved.Providers[provider] = resolvedPackages
	}
	return resolved, nil
}

func (overrides ResolvedPackageOverridesV1) Intent() (PackageOverrideIntentV1, error) {
	intent := PackageOverrideIntentV1{
		Schema: PackageOverrideIntentSchemaV1, EnvironmentID: overrides.EnvironmentID,
		Choices: []PackageOverrideIntentChoiceV1{},
	}
	for _, provider := range sortedKeys(overrides.Providers) {
		for _, packageID := range sortedKeys(overrides.Providers[provider]) {
			choice := overrides.Providers[provider][packageID]
			item := PackageOverrideIntentChoiceV1{Provider: provider, Package: packageID}
			switch {
			case choice.Path != "" && choice.Version == "":
				item.Kind = "local"
			case choice.Path == "" && choice.Version != "":
				item.Kind = "version"
				item.Version = choice.Version
			default:
				return PackageOverrideIntentV1{}, fmt.Errorf("resolved package override %s.%s must select exactly one source", provider, packageID)
			}
			intent.Choices = append(intent.Choices, item)
		}
	}
	if err := ValidatePackageOverrideIntentV1(intent); err != nil {
		return PackageOverrideIntentV1{}, err
	}
	return intent, nil
}

func EmptyPackageOverrideIntentV1(environmentID string) PackageOverrideIntentV1 {
	return PackageOverrideIntentV1{
		Schema: PackageOverrideIntentSchemaV1, EnvironmentID: environmentID,
		Choices: []PackageOverrideIntentChoiceV1{},
	}
}

func ValidatePackageOverrideIntentV1(intent PackageOverrideIntentV1) error {
	if intent.Schema != PackageOverrideIntentSchemaV1 {
		return fmt.Errorf("package override intent schema must be %q", PackageOverrideIntentSchemaV1)
	}
	if err := blueprint.ValidateEnvironmentID("package override intent environment", intent.EnvironmentID); err != nil {
		return err
	}
	if intent.Choices == nil {
		return fmt.Errorf("package override intent choices must use an array")
	}
	for index, choice := range intent.Choices {
		if err := blueprint.ValidateProviderIdentifier("package override intent provider", choice.Provider); err != nil {
			return err
		}
		if err := validatePackageOverrideIdentifier(choice.Provider, choice.Package); err != nil {
			return err
		}
		if index > 0 {
			prior := intent.Choices[index-1]
			if prior.Provider > choice.Provider || prior.Provider == choice.Provider && prior.Package >= choice.Package {
				return fmt.Errorf("package override intent choices must be unique and sorted by provider and package")
			}
		}
		switch choice.Kind {
		case "local":
			if choice.Version != "" {
				return fmt.Errorf("local package override intent %s.%s must not contain a version", choice.Provider, choice.Package)
			}
		case "version":
			if choice.Version == "" || strings.HasPrefix(choice.Version, "-") || containsControl(choice.Version) {
				return fmt.Errorf("version package override intent %s.%s must contain plain version text", choice.Provider, choice.Package)
			}
		default:
			return fmt.Errorf("package override intent %s.%s has unsupported kind %q", choice.Provider, choice.Package, choice.Kind)
		}
	}
	return nil
}

func (intent PackageOverrideIntentV1) ChoicesForProvider(provider string) []PackageOverrideIntentChoiceV1 {
	result := []PackageOverrideIntentChoiceV1{}
	for _, choice := range intent.Choices {
		if choice.Provider == provider {
			result = append(result, choice)
		}
	}
	return result
}

func PackageOverridesPath(deploymentDir string) (string, error) {
	absolute, err := filepath.Abs(deploymentDir)
	if err != nil {
		return "", fmt.Errorf("resolve package overrides directory: %w", err)
	}
	return filepath.Join(absolute, PackageOverridesFilename), nil
}

// ReadPackageOverridesV1 reads one complete sidecar snapshot. Missing files
// are normal and do not create any deployment state.
func ReadPackageOverridesV1(deploymentDir string) (PackageOverridesV1, bool, error) {
	path, err := PackageOverridesPath(deploymentDir)
	if err != nil {
		return PackageOverridesV1{}, false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return PackageOverridesV1{}, false, nil
	}
	if err != nil {
		return PackageOverridesV1{}, false, fmt.Errorf("inspect package overrides: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return PackageOverridesV1{}, false, fmt.Errorf("package overrides path must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return PackageOverridesV1{}, false, fmt.Errorf("read package overrides: %w", err)
	}
	overrides, err := DecodePackageOverridesV1(content)
	if err != nil {
		return PackageOverridesV1{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return overrides, true, nil
}

// CommitPackageOverridesV1 atomically replaces the sidecar while the
// deployment operation lock is held.
func (lock *OperationLock) CommitPackageOverridesV1(overrides PackageOverridesV1) error {
	if lock == nil {
		return fmt.Errorf("commit package overrides requires an operation lock")
	}
	content, err := EncodePackageOverridesV1(overrides)
	if err != nil {
		return err
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if lock.released || lock.file == nil || lock.path == "" {
		return fmt.Errorf("operation lock is not held")
	}
	path := filepath.Join(filepath.Dir(filepath.Dir(lock.path)), PackageOverridesFilename)
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("package overrides path must be a regular file: %s", path)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("inspect package overrides: %w", statErr)
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("commit package overrides: %w", err)
	}
	return nil
}

func validatePackageOverrideIdentifier(provider string, packageID string) error {
	if packageID == "" || packageID != strings.TrimSpace(packageID) || !utf8.ValidString(packageID) || containsControl(packageID) {
		return fmt.Errorf("package override %s package identifier %q must be nonempty plain text without surrounding whitespace", provider, packageID)
	}
	return nil
}

func containsControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
