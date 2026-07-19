package dockerdeploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

type FinalizationBuildRequest struct {
	Source              InspectedImageCandidate
	Validation          deploy.PrefixValidationV1
	ValidationReference providerstore.StoreObjectRef
	Platform            blueprint.Platform
}

var runFinalizationBuildCommand = runCommand
var runFinalizationBuildReferenceDocker = runDockerOutput

func FinalizationDockerfile(request FinalizationBuildRequest) ([]byte, error) {
	if err := validateFinalizationBuildRequest(request); err != nil {
		return nil, err
	}
	labels, err := deploy.PrefixValidationLabels(request.Source.Image.RootFSSubject, request.ValidationReference)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# syntax=%s\n", MaterializationDockerfileSyntax)
	output.WriteString("ARG REPLOY_BASE_IMAGE\n")
	output.WriteString("FROM ${REPLOY_BASE_IMAGE}\n")
	for _, label := range labels {
		name, err := quoteDockerfileWord(label.Name)
		if err != nil {
			return nil, fmt.Errorf("render final validation label name: %w", err)
		}
		value, err := quoteDockerfileWord(label.Value)
		if err != nil {
			return nil, fmt.Errorf("render final validation label %q: %w", label.Name, err)
		}
		fmt.Fprintf(&output, "LABEL %s=%s\n", name, value)
	}
	return output.Bytes(), nil
}

func BuildFinalizedImageCandidate(store providerstore.Store, request FinalizationBuildRequest, options RunOptions) (result BuiltImageCandidate, resultErr error) {
	dockerfile, err := FinalizationDockerfile(request)
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	workspace, err := store.NewWorkspace("finalize-*")
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	defer os.RemoveAll(workspace)
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	baseReference, cleanupBaseReference, err := prepareTemporaryBuildBaseReference(
		ctx, store.Root(), workspace, request.Source.Image, runFinalizationBuildReferenceDocker,
	)
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	defer func() {
		if cleanupErr := cleanupTemporaryBuildBaseReferenceAfterBuild(
			context.WithoutCancel(ctx), cleanupBaseReference, result, runFinalizationBuildReferenceDocker,
		); cleanupErr != nil {
			if resultErr != nil {
				resultErr = fmt.Errorf("%w; cleanup temporary build base reference: %v", resultErr, cleanupErr)
			} else {
				result = BuiltImageCandidate{}
				resultErr = fmt.Errorf("cleanup temporary build base reference: %w", cleanupErr)
			}
		}
	}()
	contextDir := filepath.Join(workspace, "context")
	if err := os.Mkdir(contextDir, 0o700); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("create finalization build context: %w", err)
	}
	dockerfilePath := filepath.Join(workspace, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, dockerfile, 0o600); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("write finalization Dockerfile: %w", err)
	}
	iidPath := filepath.Join(workspace, "result.iid")
	command, err := MaterializationBuildCommand(MaterializationBuildPlan{
		BaseReference: baseReference, Platform: request.Platform,
		DockerfilePath: dockerfilePath, ContextDir: contextDir, IIDFile: iidPath,
	})
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	if err := runFinalizationBuildCommand(command, options); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("build finalized validation-label candidate: %w", err)
	}
	content, err := os.ReadFile(iidPath)
	if err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("read finalized image ID: %w", err)
	}
	imageID := canonical.Digest(strings.TrimSpace(string(content)))
	if err := imageID.Validate(); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("finalized image ID: %w", err)
	}
	return BuiltImageCandidate{ImageID: imageID}, nil
}

func validateFinalizationBuildRequest(request FinalizationBuildRequest) error {
	if err := ValidateInspectedImageCandidateIdentity(request.Source); err != nil {
		return fmt.Errorf("finalization source image: %w", err)
	}
	if err := request.Platform.Validate(); err != nil {
		return fmt.Errorf("finalization platform: %w", err)
	}
	if request.Source.Descriptor.Platform != request.Platform {
		return fmt.Errorf("finalization platform does not match the source image")
	}
	digest, err := deploy.PrefixValidationDigest(request.Validation)
	if err != nil {
		return fmt.Errorf("finalization validation record: %w", err)
	}
	if err := request.ValidationReference.Validate(); err != nil {
		return fmt.Errorf("finalization validation reference: %w", err)
	}
	if request.ValidationReference.Kind != providerstore.ValidationRecordKind || request.ValidationReference.Digest != digest {
		return fmt.Errorf("finalization validation reference does not match its record")
	}
	if request.Validation.SubjectRootFS != request.Source.Image.RootFSSubject {
		return fmt.Errorf("finalization validation record does not bind the source rootfs subject")
	}
	return nil
}
