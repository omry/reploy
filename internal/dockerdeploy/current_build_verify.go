package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type CurrentBuildVerificationInputV1 struct {
	Store         providerstore.Store
	Current       CurrentBuild
	Runtime       CurrentRuntimePlanV1
	RunValidation FullImageValidationRunner
}

type CurrentBuildVerificationResultV1 struct {
	StoreObjects int
	Images       int
	Commands     int
}

type CurrentBuildImageMissingErrorV1 struct {
	Subject string
	ImageID canonical.Digest
	cause   error
}

func (err *CurrentBuildImageMissingErrorV1) Error() string {
	return fmt.Sprintf(
		"%s %s is missing from Docker",
		err.Subject,
		err.ImageID,
	)
}

func (err *CurrentBuildImageMissingErrorV1) Unwrap() error {
	return err.cause
}

type currentBuildVerificationBackendV1 struct {
	verifyClosure func(
		deploy.BuildLockV1,
		providerstore.Store,
		providers.RequirementProfileOwnerValidator,
		providers.ResolvedBundleOwnerValidator,
	) ([]providerstore.StoreObjectRef, error)
	inspectImage providerGraphImageInspector
}

// VerifyLoadedCurrentBuildV1 audits an already loaded current generation. It
// fully hashes the provider-store closure, re-inspects every immutable image,
// reruns cumulative provider validation, and proves the recorded runtime
// policy and command catalog. It does not publish, repair, or replace state.
func VerifyLoadedCurrentBuildV1(
	ctx context.Context,
	input CurrentBuildVerificationInputV1,
) (CurrentBuildVerificationResultV1, error) {
	if input.RunValidation == nil {
		runner := ProviderFullImageValidationRunner{Store: input.Store}
		input.RunValidation = runner.Run
	}
	return verifyLoadedCurrentBuildV1(ctx, input, currentBuildVerificationBackendV1{
		verifyClosure: deploy.BuildLockStoreClosure,
		inspectImage:  InspectBuiltImageCandidate,
	})
}

func verifyLoadedCurrentBuildV1(
	ctx context.Context,
	input CurrentBuildVerificationInputV1,
	backend currentBuildVerificationBackendV1,
) (CurrentBuildVerificationResultV1, error) {
	if ctx == nil {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("verify current build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return CurrentBuildVerificationResultV1{}, err
	}
	if backend.verifyClosure == nil || backend.inspectImage == nil || input.RunValidation == nil {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("verify current build requires complete verification backends")
	}
	if err := deploy.ValidateStateV1(input.Current.State); err != nil {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("verify current build state: %w", err)
	}
	if input.Current.State.Current == nil ||
		!reflect.DeepEqual(*input.Current.State.Current, input.Current.Generation) {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("verify current build state does not name the supplied generation")
	}
	if err := validateGenerationBuildLock(
		input.Current.Generation,
		input.Current.Lock,
		registry.ValidateRequirementProfileV1,
	); err != nil {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("verify current build lock: %w", err)
	}
	document, err := blueprint.DecodeResolvedDocumentV1(input.Current.State.Blueprint)
	if err != nil {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("verify current build blueprint: %w", err)
	}
	if !reflect.DeepEqual(document, input.Runtime.Document) {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("verify current runtime plan was not derived from the recorded blueprint")
	}
	if err := verifyLockedRuntimeV1(input.Current.Lock, input.Runtime); err != nil {
		return CurrentBuildVerificationResultV1{}, err
	}

	closure, err := backend.verifyClosure(
		input.Current.Lock,
		input.Store,
		registry.ValidateRequirementProfileV1,
		registry.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("verify current build provider store: %w", err)
	}
	storedValidation, err := deploy.LoadPrefixValidation(
		input.Store,
		input.Current.Lock.ValidationRecord,
	)
	if err != nil {
		return CurrentBuildVerificationResultV1{}, fmt.Errorf("load current build validation record: %w", err)
	}
	images, err := verifyLockedImagesV1(
		ctx,
		input.Current.Lock,
		storedValidation,
		input.RunValidation,
		backend.inspectImage,
	)
	if err != nil {
		return CurrentBuildVerificationResultV1{}, err
	}
	return CurrentBuildVerificationResultV1{
		StoreObjects: len(closure),
		Images:       images,
		Commands:     len(document.Environment.Commands),
	}, nil
}

func verifyLockedRuntimeV1(lock deploy.BuildLockV1, runtime CurrentRuntimePlanV1) error {
	plans, err := RuntimePlansV1(runtime.Document, runtime.Docker)
	if err != nil {
		return fmt.Errorf("verify current runtime plans: %w", err)
	}
	policy, err := CompileRuntimePolicyFromLockV1(runtime.Document, lock, plans)
	if err != nil {
		return fmt.Errorf("verify current runtime policy: %w", err)
	}
	if !reflect.DeepEqual(policy, lock.RuntimePolicy) {
		return fmt.Errorf("current runtime policy does not match the recorded build")
	}
	names := make([]string, 0, len(runtime.Document.Environment.Commands))
	for name := range runtime.Document.Environment.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		command := runtime.Document.Environment.Commands[name]
		if _, err := resolveLockedEnvironmentCommandForPlanV1(
			runtime.Document,
			lock.Catalog,
			runtime.Docker,
			name,
			nil,
		); err != nil {
			return fmt.Errorf("verify current command %q: %w", name, err)
		}
		if len(command.Trigger) == 0 {
			continue
		}
		if command.NativeCommand {
			matched, forwarded, err := MatchEnvironmentCommand(
				runtime.Document,
				command.Trigger,
				false,
			)
			if err != nil || matched != name || len(forwarded) != 0 {
				if err != nil {
					return fmt.Errorf("verify current command %q trigger: %w", name, err)
				}
				return fmt.Errorf("verify current command %q trigger resolves to %q", name, matched)
			}
		}
		if command.DeployedCommand {
			matched, forwarded, err := MatchEnvironmentCommand(
				runtime.Document,
				command.Trigger,
				true,
			)
			if err != nil || matched != name || len(forwarded) != 0 {
				if err != nil {
					return fmt.Errorf("verify deployed command %q trigger: %w", name, err)
				}
				return fmt.Errorf("verify deployed command %q trigger resolves to %q", name, matched)
			}
		}
	}
	return nil
}

func verifyLockedImagesV1(
	ctx context.Context,
	lock deploy.BuildLockV1,
	storedValidation deploy.PrefixValidationV1,
	run FullImageValidationRunner,
	inspect providerGraphImageInspector,
) (int, error) {
	base, err := inspect(
		ctx,
		BuiltImageCandidate{ImageID: lock.Base.ConfigDigest},
		lock.Platform,
	)
	if err != nil {
		return 0, currentBuildImageInspectionError(
			"current base image",
			lock.Base.ConfigDigest,
			err,
		)
	}
	expectedBase, err := realizedImageFromDescriptor(lock.Base)
	if err != nil {
		return 0, fmt.Errorf("verify current base descriptor: %w", err)
	}
	if base.Image.ConfigDigest != expectedBase.ConfigDigest ||
		base.Image.RootFSSubject != expectedBase.RootFSSubject ||
		base.Descriptor.Platform != lock.Base.Platform ||
		!reflect.DeepEqual(base.Descriptor.RootFSDiffIDs, lock.Base.RootFSDiffIDs) {
		return 0, fmt.Errorf("current base image no longer matches the locked descriptor")
	}

	baseOutputs := make([]providers.RealizedOutput, 0, len(lock.Catalog))
	for _, output := range lock.Catalog {
		if output.SupplierNode == "base" {
			baseOutputs = append(baseOutputs, output)
		}
	}
	profiles := []providers.RequirementProfile{}
	outputs := append([]providers.RealizedOutput{}, baseOutputs...)
	source := base
	inspectedImages := 1

	order, err := providers.StableProviderNodeOrder(lock.Graph.Nodes, lock.Graph.Edges)
	if err != nil {
		return 0, fmt.Errorf("verify current provider graph order: %w", err)
	}
	nodes := make(map[providers.NodeID]deploy.NodeLockV1, len(lock.Nodes))
	for _, node := range lock.Nodes {
		nodes[node.NodeID] = node
	}
	for _, nodeID := range order {
		if nodeID == "base" {
			continue
		}
		node := nodes[nodeID]
		profiles = append(profiles, node.RequirementProfile)
		outputs = append(outputs, node.Outputs...)
		layer, err := inspect(
			ctx,
			BuiltImageCandidate{ImageID: node.Result.ConfigDigest},
			lock.Platform,
		)
		if err != nil {
			return 0, currentBuildImageInspectionError(
				fmt.Sprintf("cached %s layer image", providerDisplayName(node.Provider)),
				node.Result.ConfigDigest,
				err,
			)
		}
		if layer.Image != node.Result {
			return 0, fmt.Errorf(
				"cached %s layer image no longer matches its locked identity",
				providerDisplayName(node.Provider),
			)
		}
		record, err := ValidateImage(ctx, FullImageValidationInput{
			Image:         layer,
			Profiles:      append([]providers.RequirementProfile{}, profiles...),
			Outputs:       append([]providers.RealizedOutput{}, outputs...),
			RuntimePolicy: lock.RuntimePolicy,
		}, registry.ValidateRequirementProfileV1, run)
		if err != nil {
			return 0, fmt.Errorf(
				"verify cached %s layer contents: %w",
				providerDisplayName(node.Provider),
				err,
			)
		}
		source = layer
		inspectedImages++
		if nodeID == lastNonBaseNodeID(order) && !reflect.DeepEqual(record, storedValidation) {
			return 0, fmt.Errorf("current provider validation evidence does not match the recorded final-prefix evidence")
		}
	}
	if len(lock.Nodes) == 0 {
		record, err := ValidateImage(ctx, FullImageValidationInput{
			Image:         base,
			Profiles:      []providers.RequirementProfile{},
			Outputs:       append([]providers.RealizedOutput{}, baseOutputs...),
			RuntimePolicy: lock.RuntimePolicy,
		}, registry.ValidateRequirementProfileV1, run)
		if err != nil {
			return 0, fmt.Errorf("verify current base image contents: %w", err)
		}
		if !reflect.DeepEqual(record, storedValidation) {
			return 0, fmt.Errorf("current base validation evidence does not match the recorded final-prefix evidence")
		}
	}

	final, err := inspect(
		ctx,
		BuiltImageCandidate{ImageID: lock.FinalImage.ConfigDigest},
		lock.Platform,
	)
	if err != nil {
		return 0, currentBuildImageInspectionError(
			"current environment image",
			lock.FinalImage.ConfigDigest,
			err,
		)
	}
	if err := validateInspectedFinalizedImageCandidate(final, FinalizationBuildRequest{
		Source:              source,
		Validation:          storedValidation,
		ValidationReference: lock.ValidationRecord,
		Platform:            lock.Platform,
	}); err != nil {
		return 0, fmt.Errorf("verify current final image: %w", err)
	}
	if final.Image != lock.FinalImage {
		return 0, fmt.Errorf("current final image no longer matches the locked image")
	}
	return inspectedImages + 1, nil
}

func currentBuildImageInspectionError(
	subject string,
	imageID canonical.Digest,
	err error,
) error {
	var missing *dockerImageNotFoundError
	if errors.As(err, &missing) {
		return &CurrentBuildImageMissingErrorV1{
			Subject: subject,
			ImageID: imageID,
			cause:   err,
		}
	}
	return fmt.Errorf("verify %s: %w", subject, err)
}

func lastNonBaseNodeID(order []providers.NodeID) providers.NodeID {
	for index := len(order) - 1; index >= 0; index-- {
		if order[index] != "base" {
			return order[index]
		}
	}
	return ""
}
