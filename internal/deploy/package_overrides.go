package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	dockerreference "github.com/distribution/reference"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"gopkg.in/yaml.v3"
)

const PackageOverridesFilename = "overrides.yaml"
const PackageOverrideIntentSchemaV1 = "package-override-intent-v1"

// PackageOverridesV1 is the explicit, staging-only developer intent stored
// beside a staged deployment. It is not part of the published blueprint.
type PackageOverridesV1 struct {
	Environment PackageOverridesEnvironmentV1 `yaml:"environment"`
}

type PackageOverridesEnvironmentV1 struct {
	ID               string                                        `yaml:"id"`
	Vars             map[string]any                                `yaml:"vars,omitempty"`
	Base             *BaseImageOverrideV1                          `yaml:"base,omitempty"`
	PackageAdditions map[string][]string                           `yaml:"package_additions,omitempty"`
	PackageOverrides map[string]map[string]PackageOverrideChoiceV1 `yaml:"package_overrides"`
}

type BaseImageOverrideV1 struct {
	Image string `yaml:"image"`
}

// PackageOverrideChoiceV1 selects exactly one local source or exact upstream
// version. A mapping never requests installation by itself.
type PackageOverrideChoiceV1 struct {
	Path    string   `yaml:"path,omitempty"`
	Version string   `yaml:"version,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
}

func (choice PackageOverrideChoiceV1) Empty() bool {
	return choice.Path == "" && choice.Version == "" && len(choice.Exclude) == 0
}

// ResolvedPackageOverridesV1 contains interpolated, normalized lookup keys and
// lexical absolute local paths. Resolving it performs no filesystem access so
// unused mappings remain completely unobserved.
type ResolvedPackageOverridesV1 struct {
	EnvironmentID string
	Additions     map[string][]string
	Providers     map[string]map[string]ResolvedPackageOverrideChoiceV1
}

type ResolvedPackageOverrideChoiceV1 struct {
	Path    string
	Version string
	Exclude []string
}

// PackageOverrideIntentV1 is the path-free build input retained in the lock.
// Local paths are deliberately represented only as kind=local; selected
// content is bound separately by source-input, retained source-artifact,
// build-environment, and output-artifact digests.
type PackageOverrideIntentV1 struct {
	Schema        string                          `json:"schema"`
	EnvironmentID string                          `json:"environment_id"`
	Additions     []PackageAdditionIntentV1       `json:"additions,omitempty"`
	Choices       []PackageOverrideIntentChoiceV1 `json:"choices"`
}

type PackageAdditionIntentV1 struct {
	Provider    string `json:"provider"`
	Requirement string `json:"requirement"`
}

type PackageOverrideIntentChoiceV1 struct {
	Provider string   `json:"provider"`
	Package  string   `json:"package"`
	Kind     string   `json:"kind"`
	Version  string   `json:"version"`
	Exclude  []string `json:"exclude,omitempty"`
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
	overrides = canonicalPackageOverridesV1(overrides)
	content, err := yaml.Marshal(overrides)
	if err != nil {
		return nil, fmt.Errorf("encode package overrides: %w", err)
	}
	return content, nil
}

// canonicalPackageOverridesV1 returns an encoding copy whose semantically
// unordered exclusion lists use their stable lexical order. Validation must
// run first; copying the nested mappings avoids mutating caller-owned values.
func canonicalPackageOverridesV1(overrides PackageOverridesV1) PackageOverridesV1 {
	canonicalOverrides := make(map[string]map[string]PackageOverrideChoiceV1, len(overrides.Environment.PackageOverrides))
	for provider, packages := range overrides.Environment.PackageOverrides {
		canonicalPackages := make(map[string]PackageOverrideChoiceV1, len(packages))
		for packageID, choice := range packages {
			exclusions, _ := NormalizePackageOverrideExclusionsV1(choice.Exclude)
			choice.Exclude = exclusions
			canonicalPackages[packageID] = choice
		}
		canonicalOverrides[provider] = canonicalPackages
	}
	overrides.Environment.PackageOverrides = canonicalOverrides
	return overrides
}

func ValidatePackageOverridesV1(overrides PackageOverridesV1) error {
	environment := overrides.Environment
	if err := blueprint.ValidateEnvironmentID("package overrides environment.id", environment.ID); err != nil {
		return err
	}
	variables, err := blueprint.ResolveEnvironmentVariables(environment.Vars)
	if err != nil {
		return fmt.Errorf("package overrides variables: %w", err)
	}
	if _, declared := environment.Vars["workspace_root"]; declared {
		workspace, ok := variables["workspace_root"].(string)
		if !ok {
			return fmt.Errorf("package overrides workspace_root must resolve to a string")
		}
		if containsControl(workspace) {
			return fmt.Errorf("package overrides workspace_root must not contain control characters")
		}
	}
	if environment.Base != nil {
		if err := ValidateBaseImageReferenceV1(environment.Base.Image); err != nil {
			return fmt.Errorf("overrides environment.base.image: %w", err)
		}
	}
	if environment.PackageOverrides == nil {
		return fmt.Errorf("package overrides environment.package_overrides must use a mapping")
	}
	for _, provider := range sortedKeys(environment.PackageAdditions) {
		requirements := environment.PackageAdditions[provider]
		if requirements == nil {
			return fmt.Errorf("package additions for provider %q must use an array", provider)
		}
		seen := map[string]bool{}
		for _, requirement := range requirements {
			normalized, err := NormalizePackageAdditionV1(provider, requirement)
			if err != nil {
				return err
			}
			if seen[normalized] {
				return fmt.Errorf("package additions for provider %q contain duplicate requirement %q", provider, normalized)
			}
			seen[normalized] = true
		}
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
			exclusions, err := NormalizePackageOverrideExclusionsV1(choice.Exclude)
			if err != nil {
				return fmt.Errorf("package override %s.%s exclude: %w", provider, packageID, err)
			}
			if len(exclusions) != 0 && pathValue == "" {
				return fmt.Errorf("package override %s.%s exclude requires a local path", provider, packageID)
			}
		}
	}
	return nil
}

// NormalizePackageOverrideExclusionsV1 validates exact source-relative paths
// and returns their stable lexical order. Entries select the named path and
// its descendants; they are deliberately not glob or ignore-file patterns.
func NormalizePackageOverrideExclusionsV1(exclusions []string) ([]string, error) {
	normalized := append([]string{}, exclusions...)
	for index, exclusion := range normalized {
		if exclusion == "" || strings.TrimSpace(exclusion) != exclusion ||
			!utf8.ValidString(exclusion) || containsControl(exclusion) ||
			path.IsAbs(exclusion) || path.Clean(exclusion) != exclusion ||
			strings.ContainsAny(exclusion, `\:*?[]`) || exclusion == "." ||
			exclusion == ".." || strings.HasPrefix(exclusion, "../") {
			return nil, fmt.Errorf("entry %d must be a canonical relative path using forward slashes", index)
		}
		for _, component := range strings.Split(exclusion, "/") {
			if component == "" || component == "." || component == ".." {
				return nil, fmt.Errorf("entry %d must be a canonical relative path using forward slashes", index)
			}
		}
	}
	sort.Strings(normalized)
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1] == normalized[index] {
			return nil, fmt.Errorf("contains duplicate path %q", normalized[index])
		}
	}
	return normalized, nil
}

// NormalizePackageAdditionV1 validates a provider-native development package
// addition without translating its package name. The os provider currently
// selects the Debian/Ubuntu APT implementation at build time.
func NormalizePackageAdditionV1(provider string, requirement string) (string, error) {
	if provider != "os" {
		return "", fmt.Errorf("package addition provider %q is unsupported; use os", provider)
	}
	if requirement == "" || strings.TrimSpace(requirement) != requirement {
		return "", fmt.Errorf("package addition %s requirement must be nonempty and have no surrounding whitespace", provider)
	}
	request, err := blueprint.ParseAPTPackageRequest(requirement)
	if err != nil {
		return "", fmt.Errorf("package addition %s:%s: %w", provider, requirement, err)
	}
	normalized := request.Name
	if request.Version != "" {
		normalized += "=" + request.Version
	}
	if normalized != requirement {
		return "", fmt.Errorf("package addition %s:%s must use its exact native package spelling %q", provider, requirement, normalized)
	}
	return requirement, nil
}

// ValidateBaseImageReferenceV1 validates one Docker author reference without
// resolving or pulling it. A bare sha256 digest is a full local image ID;
// repository digests remain valid remote references.
func ValidateBaseImageReferenceV1(reference string) error {
	if reference == "" || strings.TrimSpace(reference) != reference ||
		containsControl(reference) || strings.Contains(reference, "://") {
		return fmt.Errorf("Docker image reference is missing or unsafe")
	}
	if strings.HasPrefix(reference, "sha256:") {
		if err := canonical.Digest(reference).Validate(); err != nil {
			return fmt.Errorf("Docker local image ID: %w", err)
		}
		return nil
	}
	if repository, digest, found := strings.Cut(reference, "@"); found {
		if repository == "" || strings.Contains(digest, "@") {
			return fmt.Errorf("Docker image reference is malformed")
		}
		if err := canonical.Digest(digest).Validate(); err != nil {
			return fmt.Errorf("Docker image reference digest: %w", err)
		}
	}
	if _, err := dockerreference.ParseNormalizedNamed(reference); err != nil {
		return fmt.Errorf("Docker image reference %q is invalid: %w", reference, err)
	}
	return nil
}

func EffectiveBaseImageV1(document blueprint.Document, overrides PackageOverridesV1) (string, error) {
	if err := ValidatePackageOverridesV1(overrides); err != nil {
		return "", err
	}
	if document.Environment.Base.Image == "" {
		return "", fmt.Errorf("resolved blueprint has no base image")
	}
	if overrides.Environment.ID != document.Environment.ID {
		return "", fmt.Errorf(
			"overrides target environment %q, want %q",
			overrides.Environment.ID, document.Environment.ID,
		)
	}
	if overrides.Environment.Base != nil {
		return overrides.Environment.Base.Image, nil
	}
	return document.Environment.Base.Image, nil
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
	if _, declared := overrides.Environment.Vars["workspace_root"]; declared {
		workspace, ok := variables["workspace_root"].(string)
		if !ok {
			return ResolvedPackageOverridesV1{}, fmt.Errorf("package overrides workspace_root must resolve to a string")
		}
		workspace, err = ResolvePackageOverrideWorkspaceRootV1(workspace)
		if err != nil {
			return ResolvedPackageOverridesV1{}, fmt.Errorf("package overrides workspace_root: %w", err)
		}
		source := make(map[string]any, len(overrides.Environment.Vars))
		for name, value := range overrides.Environment.Vars {
			source[name] = value
		}
		source["workspace_root"] = workspace
		variables, err = blueprint.ResolveEnvironmentVariables(source)
		if err != nil {
			return ResolvedPackageOverridesV1{}, fmt.Errorf("package overrides variables: %w", err)
		}
	}
	resolved := ResolvedPackageOverridesV1{
		EnvironmentID: overrides.Environment.ID,
		Providers:     map[string]map[string]ResolvedPackageOverrideChoiceV1{},
	}
	if len(overrides.Environment.PackageAdditions) != 0 {
		resolved.Additions = map[string][]string{}
	}
	for _, provider := range sortedKeys(overrides.Environment.PackageAdditions) {
		for _, requirement := range overrides.Environment.PackageAdditions[provider] {
			normalized, err := NormalizePackageAdditionV1(provider, requirement)
			if err != nil {
				return ResolvedPackageOverridesV1{}, err
			}
			resolved.Additions[provider] = append(resolved.Additions[provider], normalized)
		}
		sort.Strings(resolved.Additions[provider])
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

			exclusions, err := NormalizePackageOverrideExclusionsV1(choice.Exclude)
			if err != nil {
				return ResolvedPackageOverridesV1{}, fmt.Errorf("package override %s.%s exclude: %w", provider, packageID, err)
			}
			resolvedChoice := ResolvedPackageOverrideChoiceV1{
				Version: choice.Version,
				Exclude: exclusions,
			}
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

// ResolvePackageOverrideWorkspaceRootV1 expands a current-user home shorthand
// only when the workspace is used. The sidecar retains the user's original
// spelling.
func ResolvePackageOverrideWorkspaceRootV1(value string) (string, error) {
	expanded := value
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve current user's home directory: %w", err)
		}
		expanded = home
		if value != "~" {
			expanded = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("must be an absolute path or ~/path")
	}
	return filepath.Clean(expanded), nil
}

func (overrides ResolvedPackageOverridesV1) Intent() (PackageOverrideIntentV1, error) {
	intent := PackageOverrideIntentV1{
		Schema: PackageOverrideIntentSchemaV1, EnvironmentID: overrides.EnvironmentID,
		Choices: []PackageOverrideIntentChoiceV1{},
	}
	for _, provider := range sortedKeys(overrides.Additions) {
		for _, requirement := range overrides.Additions[provider] {
			intent.Additions = append(intent.Additions, PackageAdditionIntentV1{
				Provider: provider, Requirement: requirement,
			})
		}
	}
	for _, provider := range sortedKeys(overrides.Providers) {
		for _, packageID := range sortedKeys(overrides.Providers[provider]) {
			choice := overrides.Providers[provider][packageID]
			item := PackageOverrideIntentChoiceV1{Provider: provider, Package: packageID}
			switch {
			case choice.Path != "" && choice.Version == "":
				item.Kind = "local"
				item.Exclude = append([]string{}, choice.Exclude...)
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
	for index, addition := range intent.Additions {
		normalized, err := NormalizePackageAdditionV1(addition.Provider, addition.Requirement)
		if err != nil {
			return err
		}
		if normalized != addition.Requirement {
			return fmt.Errorf("package override intent addition must be normalized")
		}
		if index > 0 {
			prior := intent.Additions[index-1]
			if prior.Provider > addition.Provider ||
				prior.Provider == addition.Provider && prior.Requirement >= addition.Requirement {
				return fmt.Errorf("package override intent additions must be unique and sorted by provider and requirement")
			}
		}
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
			exclusions, err := NormalizePackageOverrideExclusionsV1(choice.Exclude)
			if err != nil {
				return fmt.Errorf("local package override intent %s.%s exclude: %w", choice.Provider, choice.Package, err)
			}
			if !slices.Equal(exclusions, choice.Exclude) {
				return fmt.Errorf("local package override intent %s.%s exclusions must be unique and sorted", choice.Provider, choice.Package)
			}
		case "version":
			if choice.Version == "" || strings.HasPrefix(choice.Version, "-") || containsControl(choice.Version) {
				return fmt.Errorf("version package override intent %s.%s must contain plain version text", choice.Provider, choice.Package)
			}
			if len(choice.Exclude) != 0 {
				return fmt.Errorf("version package override intent %s.%s must not contain exclusions", choice.Provider, choice.Package)
			}
		default:
			return fmt.Errorf("package override intent %s.%s has unsupported kind %q", choice.Provider, choice.Package, choice.Kind)
		}
	}
	return nil
}

func (intent PackageOverrideIntentV1) AdditionsForProvider(provider string) []PackageAdditionIntentV1 {
	result := []PackageAdditionIntentV1{}
	for _, addition := range intent.Additions {
		if addition.Provider == provider {
			result = append(result, addition)
		}
	}
	return result
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

// removePackageOverridesV1 removes a sidecar created during the same locked
// operation when publication of the corresponding new staged state fails.
func (lock *OperationLock) removePackageOverridesV1() error {
	if lock == nil {
		return fmt.Errorf("remove package overrides requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if lock.released || lock.file == nil || lock.path == "" {
		return fmt.Errorf("operation lock is not held")
	}
	path := filepath.Join(filepath.Dir(filepath.Dir(lock.path)), PackageOverridesFilename)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove package overrides after failed state publication: %w", err)
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
