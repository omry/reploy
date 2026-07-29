package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type MaterializationLayerRequest struct {
	Transaction providers.MaterializationTransaction
	MountInputs []MaterializationMountInput
	Platform    blueprint.Platform
}

// BuiltImageCandidate is deliberately not providers.RealizedImageV1. It has
// not yet passed image inspection or final validation and cannot be published.
type BuiltImageCandidate struct {
	ImageID canonical.Digest
}

type dockerImageNotFoundError struct {
	ImageID canonical.Digest
	cause   error
}

func (err *dockerImageNotFoundError) Error() string {
	return fmt.Sprintf("Docker image %s is not available locally", err.ImageID)
}

func (err *dockerImageNotFoundError) Unwrap() error {
	return err.cause
}

// MaterializationLayerCandidate binds the predicted assembly identity to an
// uninspected Docker result. It is not validated or publishable state.
type MaterializationLayerCandidate struct {
	Built             BuiltImageCandidate
	AssemblyKey       providers.AssemblyKeyV1
	AssemblyKeyDigest canonical.Digest
}

// InspectedMaterializationLayerCandidate has passed immutable Docker identity
// and controlled config inspection, but not final prefix validation.
type InspectedMaterializationLayerCandidate struct {
	AssemblyKey       providers.AssemblyKeyV1
	AssemblyKeyDigest canonical.Digest
	Image             InspectedImageCandidate
}

// InspectedImageCandidate records Docker's immutable observation of a built
// candidate. It is not final validation evidence and is not publishable state.
type InspectedImageCandidate struct {
	Descriptor deploy.ImageDescriptor
	Config     deploy.BaseConfig
	Labels     map[string]string
	Image      providers.RealizedImageV1
}

var runMaterializationBuildCommand = runCommand
var runMaterializationBuildReferenceDocker = runDockerOutput

func BuildMaterializationLayer(store providerstore.Store, request MaterializationLayerRequest, options RunOptions) (result MaterializationLayerCandidate, resultErr error) {
	assemblyKey, assemblyKeyDigest, err := MaterializationAssemblyKey(request.Transaction, request.Platform)
	if err != nil {
		return MaterializationLayerCandidate{}, err
	}
	prepared, cleanup, err := PrepareMaterializationContext(store, request.Transaction, request.MountInputs)
	if err != nil {
		return MaterializationLayerCandidate{}, err
	}
	defer cleanup()
	dockerfile, err := MaterializationDockerfile(request.Transaction, prepared.Sources)
	if err != nil {
		return MaterializationLayerCandidate{}, err
	}
	workspace := filepath.Dir(prepared.Dir)
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	baseReference, cleanupBaseReference, err := prepareTemporaryBuildBaseReference(
		ctx, store.Root(), workspace, request.Transaction.Upstream, runMaterializationBuildReferenceDocker,
	)
	if err != nil {
		return MaterializationLayerCandidate{}, err
	}
	defer func() {
		if cleanupErr := cleanupTemporaryBuildBaseReferenceAfterBuild(
			context.WithoutCancel(ctx), cleanupBaseReference, result.Built, runMaterializationBuildReferenceDocker,
		); cleanupErr != nil {
			if resultErr != nil {
				resultErr = fmt.Errorf("%w; cleanup temporary build base reference: %v", resultErr, cleanupErr)
			} else {
				result = MaterializationLayerCandidate{}
				resultErr = fmt.Errorf("cleanup temporary build base reference: %w", cleanupErr)
			}
		}
	}()
	dockerfilePath := filepath.Join(workspace, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, dockerfile, 0o600); err != nil {
		return MaterializationLayerCandidate{}, fmt.Errorf("write materialization Dockerfile: %w", err)
	}
	iidPath := filepath.Join(workspace, "result.iid")
	command, err := MaterializationBuildCommand(MaterializationBuildPlan{
		BaseReference: baseReference, Platform: request.Platform,
		DockerfilePath: dockerfilePath, ContextDir: prepared.Dir, IIDFile: iidPath, NoCache: options.NoCache,
	})
	if err != nil {
		return MaterializationLayerCandidate{}, err
	}
	if err := runMaterializationBuildCommand(command, options); err != nil {
		platform := request.Platform
		failure := providers.NewBuildErrorV1(providers.BuildErrorV1{
			Code: "materialization.failed", Phase: "materialize", Platform: &platform,
			BaseDigest: request.Transaction.Upstream.ConfigDigest, NodeID: request.Transaction.NodeID, CauseKind: "docker.build",
			Correction: &providers.CorrectionV1{Kind: "retry-materialization"},
		}, err)
		return MaterializationLayerCandidate{}, failure
	}
	content, err := os.ReadFile(iidPath)
	if err != nil {
		return MaterializationLayerCandidate{}, fmt.Errorf("read materialization image ID: %w", err)
	}
	imageDigest := canonical.Digest(strings.TrimSpace(string(content)))
	if err := imageDigest.Validate(); err != nil {
		return MaterializationLayerCandidate{}, fmt.Errorf("materialization image ID: %w", err)
	}
	return MaterializationLayerCandidate{
		Built: BuiltImageCandidate{ImageID: imageDigest}, AssemblyKey: assemblyKey, AssemblyKeyDigest: assemblyKeyDigest,
	}, nil
}

func InspectBuiltImageCandidate(ctx context.Context, candidate BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
	return inspectBuiltImageCandidate(ctx, candidate, platform, runDockerOutput)
}

func InspectMaterializationLayerCandidate(ctx context.Context, candidate MaterializationLayerCandidate, request MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
	return inspectMaterializationLayerCandidate(ctx, candidate, request, runDockerOutput)
}

func inspectMaterializationLayerCandidate(ctx context.Context, candidate MaterializationLayerCandidate, request MaterializationLayerRequest, run dockerOutputRunner) (InspectedMaterializationLayerCandidate, error) {
	expectedKey, expectedDigest, err := MaterializationAssemblyKey(request.Transaction, request.Platform)
	if err != nil {
		return InspectedMaterializationLayerCandidate{}, err
	}
	if !reflect.DeepEqual(candidate.AssemblyKey, expectedKey) || candidate.AssemblyKeyDigest != expectedDigest {
		return InspectedMaterializationLayerCandidate{}, fmt.Errorf("materialization candidate assembly identity does not match its request")
	}
	image, err := inspectBuiltImageCandidate(ctx, candidate.Built, request.Platform, run)
	if err != nil {
		return InspectedMaterializationLayerCandidate{}, err
	}
	if err := ValidateInspectedMaterializationCandidate(request.Transaction, image); err != nil {
		return InspectedMaterializationLayerCandidate{}, err
	}
	return InspectedMaterializationLayerCandidate{
		AssemblyKey: candidate.AssemblyKey, AssemblyKeyDigest: candidate.AssemblyKeyDigest, Image: image,
	}, nil
}

func inspectBuiltImageCandidate(ctx context.Context, candidate BuiltImageCandidate, platform blueprint.Platform, run dockerOutputRunner) (InspectedImageCandidate, error) {
	if err := candidate.ImageID.Validate(); err != nil {
		return InspectedImageCandidate{}, fmt.Errorf("inspect materialization candidate image ID: %w", err)
	}
	if err := platform.Validate(); err != nil {
		return InspectedImageCandidate{}, fmt.Errorf("inspect materialization candidate platform: %w", err)
	}
	output, err := run(ctx, "image", "inspect", string(candidate.ImageID))
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return InspectedImageCandidate{}, contextErr
		}
		if dockerImageInspectReportsMissing(err) {
			return InspectedImageCandidate{}, &dockerImageNotFoundError{
				ImageID: candidate.ImageID,
				cause:   err,
			}
		}
		return InspectedImageCandidate{}, fmt.Errorf("inspect materialization candidate %s: %w", candidate.ImageID, err)
	}
	inspection, err := parseDockerImageInspectionDetails(string(candidate.ImageID), platform, []byte(output))
	if err != nil {
		return InspectedImageCandidate{}, fmt.Errorf("inspect materialization candidate %s: %w", candidate.ImageID, err)
	}
	descriptor := inspection.Descriptor
	rootFSSubject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		return InspectedImageCandidate{}, fmt.Errorf("inspect materialization candidate rootfs: %w", err)
	}
	image := providers.RealizedImageV1{
		Digest: descriptor.ConfigDigest, ConfigDigest: descriptor.ConfigDigest, RootFSSubject: rootFSSubject,
	}
	if err := image.Validate(); err != nil {
		return InspectedImageCandidate{}, fmt.Errorf("inspect materialization candidate realized image: %w", err)
	}
	return InspectedImageCandidate{Descriptor: descriptor, Config: inspection.Config, Labels: inspection.Labels, Image: image}, nil
}

func dockerImageInspectReportsMissing(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such image") ||
		strings.Contains(message, "no such object")
}

// ValidateInspectedMaterializationCandidate checks the configuration values
// Reploy explicitly controls. Unrelated metadata inherited from the trusted
// base image is intentionally outside this policy.
func ValidateInspectedMaterializationCandidate(transaction providers.MaterializationTransaction, candidate InspectedImageCandidate) error {
	if err := providers.ValidateImageConfigPolicy(transaction.FinalImageConfig); err != nil {
		return fmt.Errorf("validate materialization image policy: %w", err)
	}
	if err := ValidateInspectedImageCandidateIdentity(candidate); err != nil {
		return err
	}
	if err := candidate.Config.Validate(); err != nil {
		return fmt.Errorf("validate inspected materialization config: %w", err)
	}
	expected := transaction.FinalImageConfig
	actual := candidate.Config
	if actual.User != expected.User {
		return fmt.Errorf("materialization image user is %q, want %q", actual.User, expected.User)
	}
	if actual.WorkingDir != expected.WorkingDir {
		return fmt.Errorf("materialization image working directory is %q, want %q", actual.WorkingDir, expected.WorkingDir)
	}
	actualEnvironment := make(map[string]string, len(actual.Environment))
	for _, variable := range actual.Environment {
		actualEnvironment[variable.Name] = variable.Value
	}
	for _, variable := range expected.Environment {
		if value, found := actualEnvironment[variable.Name]; !found || value != variable.Value {
			return fmt.Errorf("materialization image environment %q is %q, want %q", variable.Name, value, variable.Value)
		}
	}
	if !reflect.DeepEqual(actual.Entrypoint, expected.Entrypoint) {
		return fmt.Errorf("materialization image entrypoint does not match policy")
	}
	if !reflect.DeepEqual(actual.Command, expected.Command) {
		return fmt.Errorf("materialization image command does not match policy")
	}
	disabledHealthcheck, err := canonicalizeDockerHealthcheck(dockerHealthcheck{Test: []string{"NONE"}})
	if err != nil {
		return err
	}
	if actual.Healthcheck != disabledHealthcheck {
		return fmt.Errorf("materialization image healthcheck is not disabled")
	}
	if actual.StopSignal != expected.StopSignal {
		return fmt.Errorf("materialization image stop signal is %q, want %q", actual.StopSignal, expected.StopSignal)
	}
	for _, label := range expected.Labels {
		if value, found := candidate.Labels[label.Name]; !found || value != label.Value {
			return fmt.Errorf("materialization image label %q is %q, want %q", label.Name, value, label.Value)
		}
	}
	return nil
}

func ValidateInspectedImageCandidateIdentity(candidate InspectedImageCandidate) error {
	if err := candidate.Image.Validate(); err != nil {
		return fmt.Errorf("validate inspected materialization image: %w", err)
	}
	if err := candidate.Descriptor.Validate(); err != nil {
		return fmt.Errorf("validate inspected materialization descriptor: %w", err)
	}
	rootFSSubject, err := deploy.RootFSSubject(candidate.Descriptor.RootFSDiffIDs)
	if err != nil {
		return fmt.Errorf("validate inspected materialization rootfs: %w", err)
	}
	if candidate.Image.Digest != candidate.Descriptor.ConfigDigest || candidate.Image.ConfigDigest != candidate.Descriptor.ConfigDigest || candidate.Image.RootFSSubject != rootFSSubject {
		return fmt.Errorf("inspected materialization image identity does not match its Docker descriptor")
	}
	return nil
}
