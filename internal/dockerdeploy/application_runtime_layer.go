package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probearchive"
	"github.com/omry/reploy/internal/providerstore"
)

type ApplicationRuntimeLayerBuildRequest struct {
	Source   InspectedImageCandidate
	Verifier deploy.ApplicationStartupVerifierV1
	Account  deploy.ApplicationLocalAccountV1
	Platform blueprint.Platform
}

var locateApplicationRuntimeExecutable = os.Executable
var runApplicationRuntimeBuildCommand = runCommand
var runApplicationRuntimeBuildDocker = runDockerOutput

func LoadApplicationStartupVerifierV1(platform blueprint.Platform) (deploy.ApplicationStartupVerifierV1, error) {
	if err := platform.Validate(); err != nil {
		return deploy.ApplicationStartupVerifierV1{}, fmt.Errorf("load application startup verifier platform: %w", err)
	}
	if platform.OS != "linux" || !probearchive.Supports(platform.Canonical) {
		return deploy.ApplicationStartupVerifierV1{}, fmt.Errorf("application startup verifier does not support platform %q", platform.Canonical)
	}
	executable, err := locateApplicationRuntimeExecutable()
	if err != nil {
		return deploy.ApplicationStartupVerifierV1{}, fmt.Errorf("locate Reploy probe archive: %w", err)
	}
	manifest, err := probearchive.Verify(executable)
	if err != nil {
		return deploy.ApplicationStartupVerifierV1{}, fmt.Errorf("load Reploy probe archive: %w", err)
	}
	for _, entry := range manifest.Entries {
		if entry.Platform != platform.Canonical {
			continue
		}
		verifier := deploy.ApplicationStartupVerifierContractV1()
		verifier.Artifact = entry.SHA256
		verifier.Size = entry.Size
		if err := deploy.ValidateApplicationStartupVerifierV1(verifier, true); err != nil {
			return deploy.ApplicationStartupVerifierV1{}, err
		}
		return verifier, nil
	}
	return deploy.ApplicationStartupVerifierV1{}, fmt.Errorf("Reploy probe archive omits platform %q", platform.Canonical)
}

func ApplicationRuntimeLayerDockerfile(request ApplicationRuntimeLayerBuildRequest) ([]byte, error) {
	if err := validateApplicationRuntimeLayerBuildRequest(request); err != nil {
		return nil, err
	}
	originalUser := ""
	if request.Source.Config.User != "" {
		var err error
		originalUser, err = quoteDockerfileWord(request.Source.Config.User)
		if err != nil {
			return nil, fmt.Errorf("render application runtime source user: %w", err)
		}
	}
	installAccount, err := json.Marshal([]string{
		request.Verifier.Path, "install-local-account",
		request.Account.Name, request.Account.UID, request.Account.GID, request.Account.Home,
	})
	if err != nil {
		return nil, fmt.Errorf("render application local account command: %w", err)
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# syntax=%s\n", MaterializationDockerfileSyntax)
	output.WriteString("ARG REPLOY_BASE_IMAGE=scratch\n")
	output.WriteString("FROM ${REPLOY_BASE_IMAGE} AS reploy-runtime-account\n")
	output.WriteString("USER 0:0\n")
	fmt.Fprintf(
		&output,
		"RUN --mount=type=bind,source=%s,target=/reploy-build-probe,readonly [\"/reploy-build-probe\", \"install-runtime-verifier\", %s]\n",
		probearchive.ExtractedFileName,
		strconv.Quote(request.Verifier.Path),
	)
	fmt.Fprintf(&output, "RUN %s\n", installAccount)
	output.WriteString("FROM ${REPLOY_BASE_IMAGE}\n")
	if originalUser != "" {
		output.WriteString("USER 0:0\n")
	}
	output.WriteString("COPY --from=reploy-runtime-account /etc/passwd /etc/group /etc/\n")
	fmt.Fprintf(
		&output,
		"RUN --mount=type=bind,source=%s,target=/reploy-build-probe,readonly [\"/reploy-build-probe\", \"install-runtime-verifier\", %s]\n",
		probearchive.ExtractedFileName,
		strconv.Quote(request.Verifier.Path),
	)
	if originalUser != "" {
		fmt.Fprintf(&output, "USER %s\n", originalUser)
	}
	return output.Bytes(), nil
}

func BuildApplicationRuntimeLayerCandidate(
	store providerstore.Store,
	request ApplicationRuntimeLayerBuildRequest,
	options RunOptions,
) (result BuiltImageCandidate, resultErr error) {
	dockerfile, err := ApplicationRuntimeLayerDockerfile(request)
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	preserveWorkspace := false
	defer func() {
		if !preserveWorkspace {
			_ = os.RemoveAll(workspace)
		}
	}()
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	contextDir := filepath.Join(workspace, "context")
	if err := os.Mkdir(contextDir, 0o700); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("create application runtime build context: %w", err)
	}
	executable, err := locateApplicationRuntimeExecutable()
	if err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("locate Reploy probe archive: %w", err)
	}
	extracted, err := probearchive.Extract(ctx, executable, request.Platform.Canonical, contextDir)
	if err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("extract application startup verifier: %w", err)
	}
	if extracted.SHA256 != request.Verifier.Artifact || extracted.Size != request.Verifier.Size || filepath.Base(extracted.Path) != probearchive.ExtractedFileName {
		return BuiltImageCandidate{}, fmt.Errorf("extracted application startup verifier does not match the build request")
	}
	baseReference, cleanupBaseReference, err := prepareTemporaryBuildBaseReference(
		ctx, store.Root(), workspace, request.Source.Image, runApplicationRuntimeBuildDocker,
	)
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	defer func() {
		if cleanupErr := cleanupTemporaryBuildBaseReferenceAfterBuild(
			context.WithoutCancel(ctx), cleanupBaseReference, result, runApplicationRuntimeBuildDocker,
		); cleanupErr != nil {
			preserveWorkspace = true
			if resultErr != nil {
				resultErr = fmt.Errorf("%w; cleanup temporary application runtime base reference: %v", resultErr, cleanupErr)
			} else {
				result = BuiltImageCandidate{}
				resultErr = fmt.Errorf("cleanup temporary application runtime base reference: %w", cleanupErr)
			}
		}
	}()
	outputReference, err := prepareTemporaryBuildOutputReference(ctx, store.Root(), workspace, runApplicationRuntimeBuildDocker)
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if cleanupErr := removeTemporaryBuildReference(context.WithoutCancel(ctx), outputReference, "", runApplicationRuntimeBuildDocker); cleanupErr != nil {
			preserveWorkspace = true
			resultErr = fmt.Errorf("%w; cleanup temporary application runtime output reference: %v", resultErr, cleanupErr)
		}
	}()
	dockerfilePath := filepath.Join(workspace, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, dockerfile, 0o600); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("write application runtime Dockerfile: %w", err)
	}
	iidPath := filepath.Join(workspace, "result.iid")
	command, err := MaterializationBuildCommand(MaterializationBuildPlan{
		BaseReference: baseReference, OutputReference: outputReference, Platform: request.Platform,
		DockerfilePath: dockerfilePath, ContextDir: contextDir, IIDFile: iidPath, NoCache: options.NoCache,
	})
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	if err := runApplicationRuntimeBuildCommand(command, options); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("build application runtime layer: %w", err)
	}
	content, err := os.ReadFile(iidPath)
	if err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("read application runtime image ID: %w", err)
	}
	imageID := canonical.Digest(strings.TrimSpace(string(content)))
	if err := imageID.Validate(); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("application runtime image ID: %w", err)
	}
	preserveWorkspace = true
	return BuiltImageCandidate{ImageID: imageID, TemporaryReference: outputReference, Workspace: workspace}, nil
}

func InspectApplicationRuntimeLayerCandidate(
	ctx context.Context,
	built BuiltImageCandidate,
	request ApplicationRuntimeLayerBuildRequest,
) (InspectedImageCandidate, error) {
	if err := validateApplicationRuntimeLayerBuildRequest(request); err != nil {
		return InspectedImageCandidate{}, err
	}
	candidate, err := inspectBuiltImageCandidate(ctx, built, request.Platform, runDockerOutput)
	if err != nil {
		return InspectedImageCandidate{}, err
	}
	if err := ValidateInspectedApplicationRuntimeLayerCandidate(request, candidate); err != nil {
		return InspectedImageCandidate{}, err
	}
	return candidate, nil
}

func ValidateInspectedApplicationRuntimeLayerCandidate(
	request ApplicationRuntimeLayerBuildRequest,
	candidate InspectedImageCandidate,
) error {
	if err := validateApplicationRuntimeLayerBuildRequest(request); err != nil {
		return err
	}
	if err := ValidateInspectedImageCandidateIdentity(candidate); err != nil {
		return err
	}
	if !reflect.DeepEqual(candidate.Config, request.Source.Config) || !reflect.DeepEqual(candidate.Labels, request.Source.Labels) {
		return fmt.Errorf("application runtime layer changed inherited image configuration")
	}
	wantPrefix := request.Source.Descriptor.RootFSDiffIDs
	got := candidate.Descriptor.RootFSDiffIDs
	if len(got) != len(wantPrefix)+2 || !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		return fmt.Errorf("application runtime layer must add exactly two filesystem layers to its source")
	}
	return nil
}

func validateApplicationRuntimeLayerBuildRequest(request ApplicationRuntimeLayerBuildRequest) error {
	if err := ValidateInspectedImageCandidateIdentity(request.Source); err != nil {
		return fmt.Errorf("application runtime layer source: %w", err)
	}
	if err := deploy.ValidateApplicationStartupVerifierV1(request.Verifier, true); err != nil {
		return err
	}
	if err := deploy.ValidateApplicationLocalAccountV1(request.Account); err != nil {
		return err
	}
	if err := request.Platform.Validate(); err != nil {
		return fmt.Errorf("application runtime layer platform: %w", err)
	}
	if request.Platform.OS != "linux" || request.Source.Descriptor.Platform != request.Platform {
		return fmt.Errorf("application runtime layer requires its exact Linux source platform")
	}
	return nil
}
