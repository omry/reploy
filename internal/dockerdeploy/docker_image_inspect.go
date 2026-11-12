package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

type dockerImageInspectRecord struct {
	ID           string                 `json:"Id"`
	RepoDigests  []string               `json:"RepoDigests"`
	Descriptor   *dockerImageDescriptor `json:"Descriptor"`
	OS           string                 `json:"Os"`
	Architecture string                 `json:"Architecture"`
	Variant      string                 `json:"Variant"`
	RootFS       struct {
		Layers []string `json:"Layers"`
	} `json:"RootFS"`
	Config *dockerImageConfig `json:"Config"`
}

// Docker's containerd-backed image store reports the selected child manifest
// in this optional descriptor. Classic Docker stores and native Podman do not
// reliably report it, so an absent or non-image descriptor remains a no-op.
type dockerImageDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}

type dockerImageConfig struct {
	Env         []string                   `json:"Env"`
	User        string                     `json:"User"`
	WorkingDir  string                     `json:"WorkingDir"`
	Entrypoint  []string                   `json:"Entrypoint"`
	Cmd         []string                   `json:"Cmd"`
	Healthcheck *dockerHealthcheck         `json:"Healthcheck"`
	StopSignal  string                     `json:"StopSignal"`
	OnBuild     []string                   `json:"OnBuild"`
	Volumes     map[string]json.RawMessage `json:"Volumes"`
	Labels      map[string]string          `json:"Labels"`
}

type dockerHealthcheck struct {
	Test          []string `json:"Test"`
	Interval      int64    `json:"Interval"`
	Timeout       int64    `json:"Timeout"`
	StartPeriod   int64    `json:"StartPeriod"`
	StartInterval int64    `json:"StartInterval"`
	Retries       int      `json:"Retries"`
}

type canonicalDockerHealthcheck struct {
	Test          []string `json:"test"`
	Interval      string   `json:"interval"`
	Timeout       string   `json:"timeout"`
	StartPeriod   string   `json:"start_period"`
	StartInterval string   `json:"start_interval"`
	Retries       string   `json:"retries"`
}

type parsedDockerImageInspection struct {
	Descriptor             deploy.ImageDescriptor
	Config                 deploy.BaseConfig
	Labels                 map[string]string
	SelectedManifestDigest canonical.Digest
}

// ResolveBase pulls the requested platform, captures its immutable identity,
// and normalizes the Docker image configuration used by later build stages.
func ResolveBase(ctx context.Context, authorReference string, platform blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	return resolveBaseWithNativePull(ctx, authorReference, platform, runDockerOutput, resolveNativePodmanBase)
}

// InspectCachedBase resolves only Docker's current local author reference. A
// missing or unusable local reference is a cache miss; it never contacts a
// registry. Callers use this only to test an already-published build before
// falling back to normal base resolution.
func InspectCachedBase(
	ctx context.Context,
	authorReference string,
	platform blueprint.Platform,
) (deploy.ImageDescriptor, deploy.BaseConfig, bool, error) {
	return inspectCachedBase(ctx, authorReference, platform, runDockerOutput, nil)
}

func inspectCachedBase(
	ctx context.Context,
	authorReference string,
	platform blueprint.Platform,
	run dockerOutputRunner,
	expected *deploy.ImageDescriptor,
) (deploy.ImageDescriptor, deploy.BaseConfig, bool, error) {
	if ctx == nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, fmt.Errorf("inspect cached Docker base requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, err
	}
	if err := validateDockerAuthorReference(authorReference); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, err
	}
	if err := platform.Validate(); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, fmt.Errorf("inspect cached Docker base platform: %w", err)
	}
	output, err := inspectDockerBaseForPlatform(ctx, authorReference, platform, run)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, contextErr
		}
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, nil
	}
	// Prefer the complete locked identity only when this author reference still
	// names the same image bytes. Native Podman pulls use config IDs, which do
	// not require RepoDigests; Docker locks may instead name an index/manifest.
	if expected != nil && expected.AuthorReference == authorReference &&
		(expected.ManifestDigest != "" || isDockerLocalImageID(expected.ImmutableReference)) {
		locked, lockedErr := parseResolvedDockerBaseDetails(expected.ImmutableReference, platform, []byte(output), "")
		if lockedErr == nil {
			locked.Descriptor.AuthorReference = authorReference
			if reflect.DeepEqual(locked.Descriptor, *expected) {
				if locked.SelectedManifestDigest != "" {
					registered, registrationErr := inspectDockerSelectedImage(ctx, locked.Descriptor, locked.Config, run)
					if registrationErr != nil {
						return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, registrationErr
					}
					if !registered {
						return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, nil
					}
				}
				return locked.Descriptor, locked.Config, true, nil
			}
		}
	}
	locked, err := parseResolvedDockerBaseDetails(authorReference, platform, []byte(output), "")
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, nil
	}
	if locked.SelectedManifestDigest != "" {
		registered, registrationErr := inspectDockerSelectedImage(ctx, locked.Descriptor, locked.Config, run)
		if registrationErr != nil {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, registrationErr
		}
		if !registered {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, nil
		}
	}
	return locked.Descriptor, locked.Config, true, nil
}

func resolveBase(ctx context.Context, authorReference string, platform blueprint.Platform, run dockerOutputRunner) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	return resolveBaseWithNativePull(ctx, authorReference, platform, run, nil)
}

type nativeBaseResolver func(context.Context, string, blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, bool, error)

func resolveBaseWithNativePull(ctx context.Context, authorReference string, platform blueprint.Platform, run dockerOutputRunner, native nativeBaseResolver) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	if ctx == nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("resolve Docker base requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	if err := validateDockerAuthorReference(authorReference); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	if err := platform.Validate(); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("resolve Docker base platform: %w", err)
	}
	if platform.OS != "linux" {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("resolve Docker base requires a Linux platform")
	}
	pulledDigest := canonical.Digest("")
	inspectionReference := authorReference
	if !isDockerLocalImageID(authorReference) {
		if pinnedDigest, pinned := dockerPinnedManifestDigest(authorReference); pinned {
			if output, inspectErr := inspectDockerBaseForPlatform(ctx, inspectionReference, platform, run); inspectErr == nil {
				if inspection, parseErr := parseResolvedDockerBaseDetails(
					authorReference,
					platform,
					[]byte(output),
					pinnedDigest,
				); parseErr == nil {
					if registerErr := ensureDockerSelectedImageRegistered(ctx, inspection, run); registerErr != nil {
						return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("register Docker base image %s for %s: %w", authorReference, platform.Canonical, registerErr)
					}
					return inspection.Descriptor, inspection.Config, nil
				}
			}
		}
		if native != nil {
			descriptor, config, handled, err := native(ctx, authorReference, platform)
			if handled || err != nil {
				return descriptor, config, err
			}
		}
		pullOutput, err := pullDockerBaseWithRetry(ctx, run, platform.Canonical, authorReference)
		if err != nil {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("pull Docker base image %s for %s: %w", authorReference, platform.Canonical, err)
		}
		pulledDigest, err = dockerPullManifestDigest(pullOutput)
		if err != nil {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("pull Docker base image %s for %s: %w", authorReference, platform.Canonical, err)
		}
		if pulledDigest == "" {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("pull Docker base image %s for %s: Docker did not report an immutable manifest digest", authorReference, platform.Canonical)
		}
	}
	output, err := inspectDockerBaseForPlatform(ctx, inspectionReference, platform, run)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("inspect Docker base image %s: %w", authorReference, err)
	}
	inspection, err := parseResolvedDockerBaseDetails(authorReference, platform, []byte(output), pulledDigest)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	if err := ensureDockerSelectedImageRegistered(ctx, inspection, run); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("register Docker base image %s for %s: %w", authorReference, platform.Canonical, err)
	}
	return inspection.Descriptor, inspection.Config, nil
}

// inspectDockerBaseForPlatform selects the concrete image in a containerd-backed
// multi-platform reference. Older clients/APIs cannot select during inspection;
// keep their previous inspection path and validate its actual metadata as usual.
func inspectDockerBaseForPlatform(ctx context.Context, reference string, platform blueprint.Platform, run dockerOutputRunner) (string, error) {
	output, err := run(ctx, "image", "inspect", "--platform", platform.Canonical, reference)
	if err == nil {
		return output, nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	message := err.Error()
	if !strings.Contains(message, "unknown flag: --platform") &&
		!strings.Contains(message, `"--platform" requires API version 1.49, but the Docker daemon API version is`) {
		return output, err
	}
	return run(ctx, "image", "inspect", reference)
}

func parseResolvedDockerBase(
	authorReference string,
	platform blueprint.Platform,
	output []byte,
	pulledDigest canonical.Digest,
) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	inspection, err := parseResolvedDockerBaseDetails(authorReference, platform, output, pulledDigest)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	return inspection.Descriptor, inspection.Config, nil
}

func parseResolvedDockerBaseDetails(
	authorReference string,
	platform blueprint.Platform,
	output []byte,
	pulledDigest canonical.Digest,
) (parsedDockerImageInspection, error) {
	inspection, err := parseDockerImageInspectionDetailsWithPullDigest(authorReference, platform, output, pulledDigest)
	if err != nil {
		return parsedDockerImageInspection{}, err
	}
	if len(inspection.Config.OnBuild) != 0 {
		return parsedDockerImageInspection{}, fmt.Errorf("Docker base image %s declares unsupported OnBuild instructions", authorReference)
	}
	if len(inspection.Config.Volumes) != 0 {
		return parsedDockerImageInspection{}, fmt.Errorf("Docker base image %s declares unsupported volumes", authorReference)
	}
	return inspection, nil
}

func dockerPinnedManifestDigest(reference string) (canonical.Digest, bool) {
	_, rawDigest, found := strings.Cut(reference, "@")
	if !found {
		return "", false
	}
	digest := canonical.Digest(rawDigest)
	return digest, digest.Validate() == nil
}

func parseDockerImageInspection(authorReference string, expected blueprint.Platform, data []byte) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	return parseDockerImageInspectionWithPullDigest(authorReference, expected, data, "")
}

func parseDockerImageInspectionWithPullDigest(authorReference string, expected blueprint.Platform, data []byte, pulledDigest canonical.Digest) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	inspection, err := parseDockerImageInspectionDetailsWithPullDigest(authorReference, expected, data, pulledDigest)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	return inspection.Descriptor, inspection.Config, nil
}

func parseDockerImageInspectionDetails(authorReference string, expected blueprint.Platform, data []byte) (parsedDockerImageInspection, error) {
	return parseDockerImageInspectionDetailsWithPullDigest(authorReference, expected, data, "")
}

func parseDockerImageInspectionDetailsWithPullDigest(authorReference string, expected blueprint.Platform, data []byte, pulledDigest canonical.Digest) (parsedDockerImageInspection, error) {
	if err := validateDockerAuthorReference(authorReference); err != nil {
		return parsedDockerImageInspection{}, err
	}
	var records []dockerImageInspectRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return parsedDockerImageInspection{}, fmt.Errorf("decode Docker image inspection: %w", err)
	}
	if len(records) != 1 {
		return parsedDockerImageInspection{}, fmt.Errorf("Docker image inspection returned %d records; expected exactly one", len(records))
	}
	record := records[0]
	if pulledDigest != "" {
		if err := pulledDigest.Validate(); err != nil {
			return parsedDockerImageInspection{}, fmt.Errorf("Docker pull manifest digest: %w", err)
		}
		requestedDigest := ""
		if _, digest, found := strings.Cut(authorReference, "@"); found {
			requestedDigest = digest
		}
		if requestedDigest != "" && requestedDigest != string(pulledDigest) {
			return parsedDockerImageInspection{}, fmt.Errorf("Docker pull manifest digest %s does not match requested digest %s", pulledDigest, requestedDigest)
		}
	}
	actual, err := blueprint.ParsePlatform(dockerPlatform(record.OS, record.Architecture, record.Variant))
	if err != nil {
		return parsedDockerImageInspection{}, fmt.Errorf("Docker image inspection platform: %w", err)
	}
	if err := expected.Validate(); err != nil {
		return parsedDockerImageInspection{}, fmt.Errorf("expected Docker image platform: %w", err)
	}
	if actual.OS != expected.OS || actual.Architecture != expected.Architecture || expected.Variant != "" && actual.Variant != expected.Variant {
		return parsedDockerImageInspection{}, fmt.Errorf("Docker image platform %s does not match selected platform %s", actual.Canonical, expected.Canonical)
	}
	// A variantless selected architecture intentionally covers the concrete
	// variant reported by Docker. Keep the selected identity so the base
	// descriptor continues to match the graph and build-lock platform.
	if expected.Variant == "" {
		actual = expected
	}
	configDigest := canonical.Digest(record.ID)
	if err := configDigest.Validate(); err != nil {
		return parsedDockerImageInspection{}, fmt.Errorf("Docker image config ID: %w", err)
	}
	diffIDs := make([]canonical.Digest, len(record.RootFS.Layers))
	for index, value := range record.RootFS.Layers {
		diffIDs[index] = canonical.Digest(value)
	}

	immutableReference, manifestDigest, err := dockerImmutableReference(authorReference, record.RepoDigests, configDigest, pulledDigest)
	if err != nil {
		return parsedDockerImageInspection{}, err
	}
	descriptor := deploy.ImageDescriptor{
		Schema: deploy.ImageDescriptorSchemaV1, Platform: actual, AuthorReference: authorReference,
		ImmutableReference: immutableReference, ManifestDigest: manifestDigest,
		ConfigDigest: configDigest, RootFSDiffIDs: diffIDs,
	}
	if err := descriptor.Validate(); err != nil {
		return parsedDockerImageInspection{}, fmt.Errorf("Docker image descriptor: %w", err)
	}
	config, err := normalizeDockerBaseConfig(record.Config)
	if err != nil {
		return parsedDockerImageInspection{}, err
	}
	labels := map[string]string{}
	for name, value := range record.Config.Labels {
		labels[name] = value
	}
	selectedManifestDigest := canonical.Digest("")
	if record.Descriptor != nil && dockerImageManifestMediaType(record.Descriptor.MediaType) {
		candidate := canonical.Digest(record.Descriptor.Digest)
		if candidate.Validate() == nil && candidate == configDigest {
			selectedManifestDigest = candidate
		}
	}
	return parsedDockerImageInspection{
		Descriptor: descriptor, Config: config, Labels: labels,
		SelectedManifestDigest: selectedManifestDigest,
	}, nil
}

func dockerImageManifestMediaType(mediaType string) bool {
	switch mediaType {
	case "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json":
		return true
	default:
		return false
	}
}

// ensureDockerSelectedImageRegistered makes a platform-selected manifest ID
// runnable in Docker's local image store. The descriptor is intentionally
// optional: classic Docker and native Podman commonly expose only a config ID,
// and those paths retain their historical behavior.
func ensureDockerSelectedImageRegistered(
	ctx context.Context,
	inspection parsedDockerImageInspection,
	run dockerOutputRunner,
) error {
	if inspection.SelectedManifestDigest == "" {
		return nil
	}
	selected := inspection.Descriptor.ConfigDigest
	if selected != inspection.SelectedManifestDigest {
		return fmt.Errorf("selected manifest descriptor %s does not match inspected ID %s", inspection.SelectedManifestDigest, selected)
	}
	registered, err := inspectDockerSelectedImage(ctx, inspection.Descriptor, inspection.Config, run)
	if err != nil {
		return err
	}
	if registered {
		return nil
	}
	repository := dockerRepositoryName(inspection.Descriptor.ImmutableReference)
	if repository == "" || isDockerLocalImageID(inspection.Descriptor.ImmutableReference) {
		repository = dockerRepositoryName(inspection.Descriptor.AuthorReference)
	}
	if repository == "" || isDockerLocalImageID(repository) {
		return fmt.Errorf("selected image has no repository identity")
	}
	childReference := repository + "@" + string(selected)
	pullOutput, err := pullDockerBaseWithRetry(ctx, run, inspection.Descriptor.Platform.Canonical, childReference)
	if err != nil {
		return fmt.Errorf("pull selected image %s: %w", childReference, err)
	}
	reportedDigest, err := dockerPullManifestDigest(pullOutput)
	if err != nil {
		return fmt.Errorf("pull selected image %s: %w", childReference, err)
	}
	if reportedDigest != selected {
		return fmt.Errorf("pull selected image %s reported digest %s, want %s", childReference, reportedDigest, selected)
	}
	output, err := inspectDockerBaseForPlatform(ctx, string(selected), inspection.Descriptor.Platform, run)
	if err != nil {
		return fmt.Errorf("inspect registered selected image %s: %w", selected, err)
	}
	verified, err := parseResolvedDockerBaseDetails(string(selected), inspection.Descriptor.Platform, []byte(output), "")
	if err != nil {
		return fmt.Errorf("parse registered selected image %s: %w", selected, err)
	}
	if err := verifyDockerSelectedImage(inspection, verified); err != nil {
		return fmt.Errorf("verify registered selected image %s: %w", selected, err)
	}
	return nil
}

// inspectDockerSelectedImage is deliberately read-only. A missing or
// mismatched selected child is a normal resolution miss; daemon failures are
// preserved for the caller instead of being mistaken for cache absence.
func inspectDockerSelectedImage(
	ctx context.Context,
	want deploy.ImageDescriptor,
	wantConfig deploy.BaseConfig,
	run dockerOutputRunner,
) (bool, error) {
	output, err := inspectDockerBaseForPlatform(ctx, string(want.ConfigDigest), want.Platform, run)
	if err != nil {
		if isDockerMissingImageError(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect selected image %s: %w", want.ConfigDigest, err)
	}
	got, err := parseResolvedDockerBaseDetails(string(want.ConfigDigest), want.Platform, []byte(output), "")
	if err != nil {
		return false, nil
	}
	if err := verifyDockerSelectedImage(
		parsedDockerImageInspection{Descriptor: want, Config: wantConfig}, got,
	); err != nil {
		return false, nil
	}
	return true, nil
}

func verifyDockerSelectedImage(want, got parsedDockerImageInspection) error {
	if got.Descriptor.ConfigDigest != want.Descriptor.ConfigDigest {
		return fmt.Errorf("config ID %s does not match selected ID %s", got.Descriptor.ConfigDigest, want.Descriptor.ConfigDigest)
	}
	if got.Descriptor.Platform != want.Descriptor.Platform {
		return fmt.Errorf("platform %s does not match selected platform %s", got.Descriptor.Platform.Canonical, want.Descriptor.Platform.Canonical)
	}
	if !reflect.DeepEqual(got.Descriptor.RootFSDiffIDs, want.Descriptor.RootFSDiffIDs) {
		return fmt.Errorf("rootfs layers do not match selected image")
	}
	if !reflect.DeepEqual(got.Config, want.Config) {
		return fmt.Errorf("normalized config does not match selected image")
	}
	return nil
}

func isDockerMissingImageError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such image") || strings.Contains(message, "image does not exist")
}

func dockerPullManifestDigest(output string) (canonical.Digest, error) {
	digest := canonical.Digest("")
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Digest:") {
			continue
		}
		candidate := canonical.Digest(strings.TrimSpace(strings.TrimPrefix(line, "Digest:")))
		if err := candidate.Validate(); err != nil {
			return "", fmt.Errorf("invalid manifest digest in Docker pull output: %w", err)
		}
		if digest != "" && digest != candidate {
			return "", fmt.Errorf("Docker pull output contains conflicting manifest digests")
		}
		digest = candidate
	}
	return digest, nil
}

func dockerPlatform(osName string, architecture string, variant string) string {
	value := osName + "/" + architecture
	if variant != "" {
		value += "/" + variant
	}
	return value
}

func dockerImmutableReference(authorReference string, repoDigests []string, configDigest canonical.Digest, pulledDigest canonical.Digest) (string, canonical.Digest, error) {
	if isDockerLocalImageID(authorReference) {
		if authorReference != string(configDigest) {
			return "", "", fmt.Errorf("Docker local image ID %s resolved to different config ID %s", authorReference, configDigest)
		}
		return authorReference, "", nil
	}
	if pulledDigest != "" {
		repository := canonicalDockerRepository(authorReference)
		for _, candidate := range repoDigests {
			candidateRepository, candidateDigest, found := strings.Cut(candidate, "@")
			if found && canonicalDockerRepository(candidateRepository) == repository && candidateDigest == string(pulledDigest) {
				return candidate, pulledDigest, nil
			}
		}
		// Some Docker image stores omit RepoDigests after an explicit-platform
		// pull. The inspected config digest is still an immutable local image
		// reference and keeps all later operations bound to the exact bytes that
		// were selected for this platform.
		return string(configDigest), "", nil
	}
	if strings.Contains(authorReference, "@") {
		for _, candidate := range repoDigests {
			if candidate == authorReference {
				_, digest, _ := strings.Cut(candidate, "@")
				return candidate, canonical.Digest(digest), nil
			}
		}
		return "", "", fmt.Errorf("Docker image inspection does not contain requested repo digest %s", authorReference)
	}
	repository := canonicalDockerRepository(authorReference)
	matches := make([]string, 0, len(repoDigests))
	for _, candidate := range repoDigests {
		candidateRepository, _, found := strings.Cut(candidate, "@")
		if found && canonicalDockerRepository(candidateRepository) == repository {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return "", "", fmt.Errorf("Docker image inspection contains no repo digest for %s", authorReference)
	}
	sort.Strings(matches)
	_, digest, _ := strings.Cut(matches[0], "@")
	return matches[0], canonical.Digest(digest), nil
}

func isDockerLocalImageID(reference string) bool {
	return canonical.Digest(reference).Validate() == nil
}

func dockerRepositoryWithoutTag(reference string) string {
	lastSlash := strings.LastIndexByte(reference, '/')
	lastColon := strings.LastIndexByte(reference, ':')
	if lastColon > lastSlash {
		return reference[:lastColon]
	}
	return reference
}

func dockerRepositoryName(reference string) string {
	if repository, _, found := strings.Cut(reference, "@"); found {
		return repository
	}
	return dockerRepositoryWithoutTag(reference)
}

func canonicalDockerRepository(reference string) string {
	repository := dockerRepositoryWithoutTag(dockerRepositoryName(reference))
	repository = strings.TrimPrefix(repository, "docker.io/")
	repository = strings.TrimPrefix(repository, "index.docker.io/")
	if strings.HasPrefix(repository, "library/") && strings.Count(repository, "/") == 1 {
		repository = strings.TrimPrefix(repository, "library/")
	}
	return repository
}

func validateDockerAuthorReference(reference string) error {
	if err := deploy.ValidateBaseImageReferenceV1(reference); err != nil {
		return fmt.Errorf("Docker image author reference: %w", err)
	}
	return nil
}

func normalizeDockerBaseConfig(source *dockerImageConfig) (deploy.BaseConfig, error) {
	if source == nil {
		return deploy.BaseConfig{}, fmt.Errorf("Docker image inspection has no config")
	}
	environmentByName := make(map[string]string, len(source.Env))
	for _, entry := range source.Env {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" {
			return deploy.BaseConfig{}, fmt.Errorf("Docker image environment entry %q is not name=value", entry)
		}
		environmentByName[name] = value
	}
	names := make([]string, 0, len(environmentByName))
	for name := range environmentByName {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]deploy.ConfigEnvironmentVariable, 0, len(names))
	for _, name := range names {
		environment = append(environment, deploy.ConfigEnvironmentVariable{Name: name, Value: environmentByName[name]})
	}
	volumes := make([]string, 0, len(source.Volumes))
	for volume := range source.Volumes {
		volumes = append(volumes, volume)
	}
	sort.Strings(volumes)
	healthcheck := ""
	if source.Healthcheck != nil {
		encoded, err := canonicalizeDockerHealthcheck(*source.Healthcheck)
		if err != nil {
			return deploy.BaseConfig{}, err
		}
		healthcheck = encoded
	}
	config := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: environment,
		User: source.User, WorkingDir: source.WorkingDir,
		Entrypoint: append([]string{}, source.Entrypoint...), Command: append([]string{}, source.Cmd...),
		Healthcheck: healthcheck, StopSignal: source.StopSignal,
		OnBuild: append([]string{}, source.OnBuild...), Volumes: volumes,
	}
	if config.Entrypoint == nil {
		config.Entrypoint = []string{}
	}
	if config.Command == nil {
		config.Command = []string{}
	}
	if config.OnBuild == nil {
		config.OnBuild = []string{}
	}
	if err := config.Validate(); err != nil {
		return deploy.BaseConfig{}, fmt.Errorf("Docker image base config: %w", err)
	}
	return config, nil
}

func canonicalizeDockerHealthcheck(healthcheck dockerHealthcheck) (string, error) {
	test := append([]string{}, healthcheck.Test...)
	if test == nil {
		test = []string{}
	}
	encoded, err := canonical.Marshal(canonicalDockerHealthcheck{
		Test: test, Interval: strconv.FormatInt(healthcheck.Interval, 10),
		Timeout:       strconv.FormatInt(healthcheck.Timeout, 10),
		StartPeriod:   strconv.FormatInt(healthcheck.StartPeriod, 10),
		StartInterval: strconv.FormatInt(healthcheck.StartInterval, 10),
		Retries:       strconv.Itoa(healthcheck.Retries),
	})
	if err != nil {
		return "", fmt.Errorf("canonicalize Docker image healthcheck: %w", err)
	}
	return string(encoded), nil
}
