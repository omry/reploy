package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

type dockerImageInspectRecord struct {
	ID           string   `json:"Id"`
	RepoDigests  []string `json:"RepoDigests"`
	OS           string   `json:"Os"`
	Architecture string   `json:"Architecture"`
	Variant      string   `json:"Variant"`
	RootFS       struct {
		Layers []string `json:"Layers"`
	} `json:"RootFS"`
	Config *dockerImageConfig `json:"Config"`
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
	Descriptor deploy.ImageDescriptor
	Config     deploy.BaseConfig
	Labels     map[string]string
}

// ResolveBase pulls the requested platform, captures its immutable identity,
// and normalizes the Docker image configuration used by later build stages.
func ResolveBase(ctx context.Context, authorReference string, platform blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	return resolveBase(ctx, authorReference, platform, runDockerOutput)
}

func resolveBase(ctx context.Context, authorReference string, platform blueprint.Platform, run dockerOutputRunner) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
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
		pullOutput, err := run(ctx, "pull", "--platform", platform.Canonical, authorReference)
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
		inspectionReference = dockerRepositoryName(authorReference) + "@" + string(pulledDigest)
	}
	output, err := run(ctx, "image", "inspect", inspectionReference)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("inspect Docker base image %s: %w", authorReference, err)
	}
	descriptor, config, err := parseDockerImageInspectionWithPullDigest(authorReference, platform, []byte(output), pulledDigest)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	if len(config.OnBuild) != 0 {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("Docker base image %s declares unsupported OnBuild instructions", authorReference)
	}
	if len(config.Volumes) != 0 {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("Docker base image %s declares unsupported volumes", authorReference)
	}
	return descriptor, config, nil
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
	return parsedDockerImageInspection{Descriptor: descriptor, Config: config, Labels: labels}, nil
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
		return dockerRepositoryName(authorReference) + "@" + string(pulledDigest), pulledDigest, nil
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
	repository := dockerRepositoryWithoutTag(reference)
	repository = strings.TrimPrefix(repository, "docker.io/")
	repository = strings.TrimPrefix(repository, "index.docker.io/")
	if strings.HasPrefix(repository, "library/") && strings.Count(repository, "/") == 1 {
		repository = strings.TrimPrefix(repository, "library/")
	}
	return repository
}

func validateDockerAuthorReference(reference string) error {
	if reference == "" || strings.TrimSpace(reference) != reference || strings.ContainsAny(reference, "\r\n\t") || strings.Contains(reference, "://") {
		return fmt.Errorf("Docker image author reference is missing or unsafe")
	}
	if repository, digest, found := strings.Cut(reference, "@"); found {
		if repository == "" {
			return fmt.Errorf("Docker image author reference is malformed")
		}
		if strings.Contains(digest, "@") {
			return fmt.Errorf("Docker image author reference is malformed")
		}
		if err := canonical.Digest(digest).Validate(); err != nil {
			return fmt.Errorf("Docker image author reference digest: %w", err)
		}
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
