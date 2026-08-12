package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	reploy "github.com/omry/reploy"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probearchive"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	ControlledSessionControllerPackageSchemaV1 = "controlled-session-controller-package-v1"
	controlledSessionControllerExecutableV1    = "/usr/local/bin/reploy-session-client"
	controlledSessionControllerPackageLabelV1  = "io.reploy.controlled-session.package"
	controlledSessionControllerArtifactLabelV1 = "io.reploy.controlled-session.artifact"
	controlledSessionControllerVersionLabelV1  = "io.reploy.controlled-session.version"
	controlledSessionControllerPathPrefixV1    = "/usr/local/bin"
	controlledSessionDefaultPathV1             = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

type ControlledSessionControllerPackageV1 struct {
	Schema      string                    `json:"schema"`
	Platform    blueprint.Platform        `json:"platform"`
	Release     probearchive.ReleaseV1    `json:"release"`
	Artifact    canonical.Digest          `json:"artifact"`
	SourceImage providers.RealizedImageV1 `json:"source_image"`
	Image       providers.RealizedImageV1 `json:"image"`
	Executable  string                    `json:"executable"`
}

type preparedControlledSessionControllerV1 struct {
	Package   ControlledSessionControllerPackageV1
	Candidate BuiltImageCandidate
}

type controlledSessionControllerPackageBackendV1 struct {
	locateExecutable func() (string, error)
	buildCommand     func(CommandSpec, RunOptions) error
	docker           dockerOutputRunner
	hostRelease      probearchive.ReleaseV1
}

var locateControlledSessionControllerExecutableV1 = os.Executable

func prepareControlledSessionControllerPackageV1(
	ctx context.Context,
	deploymentDir string,
	current CurrentBuild,
) (*preparedControlledSessionControllerV1, error) {
	store, err := providerstore.NewStore(deploymentDir)
	if err != nil {
		return nil, err
	}
	return buildControlledSessionControllerPackageV1(ctx, store, current, controlledSessionControllerPackageBackendV1{
		locateExecutable: locateControlledSessionControllerExecutableV1,
		buildCommand:     runCommand,
		docker:           runDockerOutput,
		hostRelease:      currentControllerReleaseV1(),
	})
}

func buildControlledSessionControllerPackageV1(
	ctx context.Context,
	store providerstore.Store,
	current CurrentBuild,
	backend controlledSessionControllerPackageBackendV1,
) (result *preparedControlledSessionControllerV1, resultErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("prepare controlled-session controller package requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	platform := current.Lock.Platform
	if err := platform.Validate(); err != nil {
		return nil, fmt.Errorf("controlled-session controller platform: %w", err)
	}
	if platform.OS != "linux" || !probearchive.SupportsSessionClient(platform.Canonical) {
		return nil, fmt.Errorf("controlled sessions require a Linux controller image on a supported architecture; controller platform %q is unsupported", platform.Canonical)
	}
	if backend.locateExecutable == nil || backend.buildCommand == nil || backend.docker == nil {
		return nil, fmt.Errorf("prepare controlled-session controller package requires a complete backend")
	}
	if err := probearchive.ValidateReleaseV1(backend.hostRelease); err != nil {
		return nil, fmt.Errorf("prepare controlled-session controller package host release: %w", err)
	}
	source, err := inspectControlledSessionControllerSourceV1(ctx, current, backend.docker)
	if err != nil {
		return nil, err
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		return nil, err
	}
	preserveWorkspace := false
	defer func() {
		if !preserveWorkspace {
			resultErr = errors.Join(resultErr, os.RemoveAll(workspace))
		}
	}()
	contextDir := filepath.Join(workspace, "context")
	if err := os.Mkdir(contextDir, 0o700); err != nil {
		return nil, fmt.Errorf("create controlled-session controller package context: %w", err)
	}
	hostExecutable, err := backend.locateExecutable()
	if err != nil {
		return nil, fmt.Errorf("locate Reploy runtime archive: %w", err)
	}
	extracted, err := probearchive.ExtractSessionClient(ctx, hostExecutable, platform.Canonical, contextDir)
	if err != nil {
		return nil, fmt.Errorf("extract matching session client: %w", err)
	}
	if err := validateControllerReleaseV1(extracted.Release, backend.hostRelease); err != nil {
		return nil, err
	}
	dockerfile, expectedConfig, expectedLabels, err := controlledSessionControllerPackageDockerfileV1(source, extracted)
	if err != nil {
		return nil, err
	}
	dockerfilePath := filepath.Join(workspace, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, dockerfile, 0o600); err != nil {
		return nil, fmt.Errorf("write controlled-session controller package Dockerfile: %w", err)
	}
	baseReference, cleanupBaseReference, err := prepareTemporaryBuildBaseReference(
		ctx, store.Root(), workspace, source.Image, backend.docker,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := cleanupBaseReference(context.WithoutCancel(ctx)); cleanupErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("cleanup controlled-session controller package base: %w", cleanupErr))
		}
	}()
	outputReference, err := prepareTemporaryBuildOutputReference(ctx, store.Root(), workspace, backend.docker)
	if err != nil {
		return nil, err
	}
	candidateEstablished := false
	defer func() {
		if resultErr == nil || candidateEstablished {
			return
		}
		if cleanupErr := removeTemporaryBuildReference(context.WithoutCancel(ctx), outputReference, "", backend.docker); cleanupErr != nil {
			preserveWorkspace = true
			resultErr = errors.Join(resultErr, fmt.Errorf("cleanup controlled-session controller package output reference: %w", cleanupErr))
		}
	}()
	iidPath := filepath.Join(workspace, "result.iid")
	command, err := MaterializationBuildCommand(MaterializationBuildPlan{
		BaseReference: baseReference, OutputReference: outputReference, Platform: platform,
		DockerfilePath: dockerfilePath, ContextDir: contextDir, IIDFile: iidPath,
	})
	if err != nil {
		return nil, err
	}
	if err := backend.buildCommand(command, RunOptions{Context: ctx}); err != nil {
		return nil, fmt.Errorf("build controlled-session controller package: %w", err)
	}
	content, err := os.ReadFile(iidPath)
	if err != nil {
		return nil, fmt.Errorf("read controlled-session controller package image ID: %w", err)
	}
	imageID := canonical.Digest(strings.TrimSpace(string(content)))
	if err := imageID.Validate(); err != nil {
		return nil, fmt.Errorf("controlled-session controller package image ID: %w", err)
	}
	candidate := BuiltImageCandidate{ImageID: imageID, TemporaryReference: outputReference, Workspace: workspace}
	candidateEstablished = true
	accepted := false
	defer func() {
		if accepted {
			return
		}
		if cleanupErr := removeBuiltImageCandidate(context.WithoutCancel(ctx), candidate, backend.docker); cleanupErr != nil {
			preserveWorkspace = true
			resultErr = errors.Join(resultErr, fmt.Errorf("cleanup rejected controlled-session controller package: %w", cleanupErr))
		}
	}()
	inspected, err := inspectBuiltImageCandidate(ctx, candidate, platform, backend.docker)
	if err != nil {
		return nil, err
	}
	if err := validateControlledSessionControllerPackageImageV1(source, inspected, expectedConfig, expectedLabels); err != nil {
		return nil, err
	}
	packagePlan := ControlledSessionControllerPackageV1{
		Schema: ControlledSessionControllerPackageSchemaV1, Platform: platform,
		Release: extracted.Release, Artifact: extracted.SHA256,
		SourceImage: source.Image, Image: inspected.Image,
		Executable: controlledSessionControllerExecutableV1,
	}
	if err := validateControlledSessionControllerPackageForReleaseV1(packagePlan, backend.hostRelease); err != nil {
		return nil, err
	}
	preserveWorkspace = true
	accepted = true
	return &preparedControlledSessionControllerV1{Package: packagePlan, Candidate: candidate}, nil
}

func cleanupControlledSessionControllerPackageV1(ctx context.Context, prepared *preparedControlledSessionControllerV1) error {
	if prepared == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return removeBuiltImageCandidate(ctx, prepared.Candidate, runDockerOutput)
}

func inspectControlledSessionControllerSourceV1(ctx context.Context, current CurrentBuild, run dockerOutputRunner) (InspectedImageCandidate, error) {
	if run == nil {
		return InspectedImageCandidate{}, fmt.Errorf("inspect controlled-session controller source requires a Docker runner")
	}
	output, err := run(ctx, "image", "inspect", current.Generation.Reference)
	if err != nil {
		return InspectedImageCandidate{}, fmt.Errorf("inspect controlled-session controller generation %q: %w", current.Generation.Reference, err)
	}
	inspection, err := parseDockerImageInspectionDetails(current.Generation.Reference, current.Lock.Platform, []byte(output))
	if err != nil {
		return InspectedImageCandidate{}, fmt.Errorf("inspect controlled-session controller generation %q: %w", current.Generation.Reference, err)
	}
	rootFSSubject, err := deploy.RootFSSubject(inspection.Descriptor.RootFSDiffIDs)
	if err != nil {
		return InspectedImageCandidate{}, err
	}
	image := providers.RealizedImageV1{
		Digest: inspection.Descriptor.ConfigDigest, ConfigDigest: inspection.Descriptor.ConfigDigest,
		RootFSSubject: rootFSSubject,
	}
	if !reflect.DeepEqual(image, current.Lock.FinalImage) {
		return InspectedImageCandidate{}, fmt.Errorf("controlled-session controller generation does not match its locked final image")
	}
	return InspectedImageCandidate{Descriptor: inspection.Descriptor, Config: inspection.Config, Labels: inspection.Labels, Image: image}, nil
}

func controlledSessionControllerPackageDockerfileV1(source InspectedImageCandidate, extracted probearchive.ExtractedSessionClient) ([]byte, deploy.BaseConfig, map[string]string, error) {
	if err := ValidateInspectedImageCandidateIdentity(source); err != nil {
		return nil, deploy.BaseConfig{}, nil, fmt.Errorf("controlled-session controller package source: %w", err)
	}
	if !probearchive.SupportsSessionClient(extracted.Platform) || extracted.Path == "" || extracted.Size == "" {
		return nil, deploy.BaseConfig{}, nil, fmt.Errorf("controlled-session controller package requires an extracted supported session client")
	}
	if err := extracted.SHA256.Validate(); err != nil {
		return nil, deploy.BaseConfig{}, nil, fmt.Errorf("controlled-session session client: %w", err)
	}
	expectedConfig := source.Config
	expectedConfig.Environment = append([]deploy.ConfigEnvironmentVariable{}, source.Config.Environment...)
	pathValue := controlledSessionDefaultPathV1
	foundPath := false
	for index := range expectedConfig.Environment {
		if expectedConfig.Environment[index].Name == "PATH" {
			pathValue = expectedConfig.Environment[index].Value
			expectedConfig.Environment[index].Value = controlledSessionControllerPathPrefixV1 + ":" + pathValue
			foundPath = true
			break
		}
	}
	if !foundPath {
		expectedConfig.Environment = append(expectedConfig.Environment, deploy.ConfigEnvironmentVariable{
			Name: "PATH", Value: controlledSessionControllerPathPrefixV1 + ":" + pathValue,
		})
		sort.Slice(expectedConfig.Environment, func(left int, right int) bool {
			return expectedConfig.Environment[left].Name < expectedConfig.Environment[right].Name
		})
	}
	expectedLabels := make(map[string]string, len(source.Labels)+3)
	for name, value := range source.Labels {
		expectedLabels[name] = value
	}
	expectedLabels[controlledSessionControllerPackageLabelV1] = ControlledSessionControllerPackageSchemaV1
	expectedLabels[controlledSessionControllerArtifactLabelV1] = string(extracted.SHA256)
	expectedLabels[controlledSessionControllerVersionLabelV1] = extracted.Release.Version

	pathWord, err := quoteDockerfileWord(expectedConfig.Environment[pathEnvironmentIndexV1(expectedConfig.Environment)].Value)
	if err != nil {
		return nil, deploy.BaseConfig{}, nil, fmt.Errorf("render controlled-session controller PATH: %w", err)
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# syntax=%s\n", MaterializationDockerfileSyntax)
	output.WriteString("ARG REPLOY_BASE_IMAGE=scratch\n")
	output.WriteString("FROM ${REPLOY_BASE_IMAGE}\n")
	output.WriteString("COPY --chmod=0555 reploy-session-client /usr/local/bin/reploy-session-client\n")
	fmt.Fprintf(&output, "ENV PATH=%s\n", pathWord)
	for _, name := range []string{
		controlledSessionControllerPackageLabelV1,
		controlledSessionControllerArtifactLabelV1,
		controlledSessionControllerVersionLabelV1,
	} {
		labelName, err := quoteDockerfileWord(name)
		if err != nil {
			return nil, deploy.BaseConfig{}, nil, err
		}
		labelValue, err := quoteDockerfileWord(expectedLabels[name])
		if err != nil {
			return nil, deploy.BaseConfig{}, nil, err
		}
		fmt.Fprintf(&output, "LABEL %s=%s\n", labelName, labelValue)
	}
	return output.Bytes(), expectedConfig, expectedLabels, nil
}

func pathEnvironmentIndexV1(environment []deploy.ConfigEnvironmentVariable) int {
	for index, variable := range environment {
		if variable.Name == "PATH" {
			return index
		}
	}
	return -1
}

func validateControlledSessionControllerPackageImageV1(source InspectedImageCandidate, inspected InspectedImageCandidate, expectedConfig deploy.BaseConfig, expectedLabels map[string]string) error {
	if err := ValidateInspectedImageCandidateIdentity(inspected); err != nil {
		return fmt.Errorf("controlled-session controller package image: %w", err)
	}
	if !reflect.DeepEqual(inspected.Config, expectedConfig) {
		return fmt.Errorf("controlled-session controller package changed image configuration beyond PATH")
	}
	if !reflect.DeepEqual(inspected.Labels, expectedLabels) {
		return fmt.Errorf("controlled-session controller package labels do not match the exact embedded session client")
	}
	wantPrefix := source.Descriptor.RootFSDiffIDs
	got := inspected.Descriptor.RootFSDiffIDs
	if len(got) != len(wantPrefix)+1 || !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		return fmt.Errorf("controlled-session controller package must add exactly one filesystem layer to its source")
	}
	return nil
}

func validateMatchingControllerReleaseV1(release probearchive.ReleaseV1) error {
	return validateControllerReleaseV1(release, currentControllerReleaseV1())
}

func validateControllerReleaseV1(release probearchive.ReleaseV1, want probearchive.ReleaseV1) error {
	if release != want {
		return fmt.Errorf("embedded session client release does not match host Reploy: session client %#v, host %#v", release, want)
	}
	return nil
}

func currentControllerReleaseV1() probearchive.ReleaseV1 {
	return probearchive.ReleaseV1{
		Version: reploy.Version, BuildCommit: reploy.BuildCommit,
		BuildDirty: reploy.BuildDirty, BuildTimestamp: reploy.BuildTimestamp,
	}
}

func ValidateControlledSessionControllerPackageV1(packagePlan ControlledSessionControllerPackageV1) error {
	return validateControlledSessionControllerPackageForReleaseV1(packagePlan, currentControllerReleaseV1())
}

func validateControlledSessionControllerPackageForReleaseV1(packagePlan ControlledSessionControllerPackageV1, hostRelease probearchive.ReleaseV1) error {
	if packagePlan.Schema != ControlledSessionControllerPackageSchemaV1 {
		return fmt.Errorf("controlled-session controller package schema must be %q", ControlledSessionControllerPackageSchemaV1)
	}
	if err := packagePlan.Platform.Validate(); err != nil {
		return fmt.Errorf("controlled-session controller package platform: %w", err)
	}
	if packagePlan.Platform.OS != "linux" || !probearchive.SupportsSessionClient(packagePlan.Platform.Canonical) {
		return fmt.Errorf("controlled-session controller package platform %q is unsupported", packagePlan.Platform.Canonical)
	}
	if err := validateControllerReleaseV1(packagePlan.Release, hostRelease); err != nil {
		return err
	}
	if err := packagePlan.Artifact.Validate(); err != nil {
		return fmt.Errorf("controlled-session controller package artifact: %w", err)
	}
	if err := packagePlan.SourceImage.Validate(); err != nil {
		return fmt.Errorf("controlled-session controller package source image: %w", err)
	}
	if err := packagePlan.Image.Validate(); err != nil {
		return fmt.Errorf("controlled-session controller package image: %w", err)
	}
	if packagePlan.SourceImage == packagePlan.Image {
		return fmt.Errorf("controlled-session controller package image must differ from its source")
	}
	if packagePlan.Executable != controlledSessionControllerExecutableV1 {
		return fmt.Errorf("controlled-session controller package executable must be %q", controlledSessionControllerExecutableV1)
	}
	return nil
}
