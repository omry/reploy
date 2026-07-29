package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// MaterializationEvidenceInput gives the validation runner the exact inspected
// image and the closed transaction and bundle that produced it.
type MaterializationEvidenceInput struct {
	Candidate   InspectedMaterializationLayerCandidate
	Transaction providers.MaterializationTransaction
	Bundle      providers.ResolvedBundle
}

type MaterializationEvidenceRunner func(
	context.Context,
	MaterializationEvidenceInput,
) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error)

type materializationLayerBuilder func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error)
type materializationLayerInspector func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error)
type materializationCandidateRetainer func(context.Context, providers.RealizedImageV1) error
type materializationCandidateRemover func(context.Context, BuiltImageCandidate) error

// BuildAndAcceptMaterializationLayer builds one provider layer and returns a
// graph result only after immutable inspection and exact generated/public
// executable validation have succeeded.
func BuildAndAcceptMaterializationLayer(
	ctx context.Context,
	store providerstore.Store,
	transaction providers.MaterializationTransaction,
	bundle providers.ResolvedBundle,
	platform blueprint.Platform,
	runEvidence MaterializationEvidenceRunner,
	options RunOptions,
) (providers.GraphNodeMaterializeResult, error) {
	return buildAndAcceptMaterializationLayerWithVerifiedArtifacts(
		ctx, store, transaction, bundle, platform, runEvidence, nil,
		RetainVerifiedProviderLayer, options,
	)
}

// buildAndAcceptMaterializationLayerWithVerifiedArtifacts uses operation-local
// staged files that were already hashed during resolution. All other bundle
// artifacts retain the normal store verification path.
func buildAndAcceptMaterializationLayerWithVerifiedArtifacts(
	ctx context.Context,
	store providerstore.Store,
	transaction providers.MaterializationTransaction,
	bundle providers.ResolvedBundle,
	platform blueprint.Platform,
	runEvidence MaterializationEvidenceRunner,
	verifiedArtifacts map[canonical.Digest]string,
	retain materializationCandidateRetainer,
	options RunOptions,
) (providers.GraphNodeMaterializeResult, error) {
	return buildAndAcceptMaterializationLayer(
		ctx, store, transaction, bundle, platform, runEvidence, verifiedArtifacts, options,
		BuildMaterializationLayer, InspectMaterializationLayerCandidate,
		retain, RemoveBuiltImageCandidate,
	)
}

func buildAndAcceptMaterializationLayer(
	ctx context.Context,
	store providerstore.Store,
	transaction providers.MaterializationTransaction,
	bundle providers.ResolvedBundle,
	platform blueprint.Platform,
	runEvidence MaterializationEvidenceRunner,
	verifiedArtifacts map[canonical.Digest]string,
	options RunOptions,
	build materializationLayerBuilder,
	inspect materializationLayerInspector,
	retain materializationCandidateRetainer,
	remove materializationCandidateRemover,
) (providers.GraphNodeMaterializeResult, error) {
	if ctx == nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("materialization acceptance requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphNodeMaterializeResult{}, err
	}
	if build == nil || inspect == nil || retain == nil || remove == nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("materialization acceptance requires build, inspection, retention, and cleanup backends")
	}
	if err := validateMaterializationBuildBinding(transaction, bundle, platform); err != nil {
		return providers.GraphNodeMaterializeResult{}, err
	}
	mountInputs, err := MaterializationMountInputs(bundle, transaction)
	if err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("prepare materialization layer inputs: %w", err)
	}
	if err := bindVerifiedMaterializationArtifacts(mountInputs, verifiedArtifacts); err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("bind verified materialization artifacts: %w", err)
	}
	request := MaterializationLayerRequest{Transaction: transaction, MountInputs: mountInputs, Platform: platform}
	buildCtx, endBuild := buildprofile.Start(ctx, "Build provider layer image")
	options.Context = buildCtx
	built, err := build(store, request, options)
	endBuild(err)
	if err != nil {
		return providers.GraphNodeMaterializeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return rejectMaterializationCandidate(ctx, built.Built, err, remove)
	}
	inspectCtx, endInspect := buildprofile.Start(ctx, "Inspect provider layer image")
	inspected, err := inspect(inspectCtx, built, request)
	endInspect(err)
	if err != nil {
		return rejectMaterializationCandidate(ctx, built.Built, err, remove)
	}
	validateCtx, endValidate := buildprofile.Start(ctx, "Validate provider layer")
	accepted, err := AcceptMaterializationLayer(validateCtx, MaterializationEvidenceInput{
		Candidate: inspected, Transaction: transaction, Bundle: bundle,
	}, runEvidence)
	endValidate(err)
	if err != nil {
		return rejectMaterializationCandidate(ctx, built.Built, err, remove)
	}
	retainCtx, endRetain := buildprofile.Start(ctx, "Retain provider layer")
	err = retain(retainCtx, accepted.Image)
	endRetain(err)
	if err != nil {
		return rejectMaterializationCandidate(
			ctx,
			built.Built,
			fmt.Errorf("retain verified provider layer: %w", err),
			remove,
		)
	}
	return accepted, nil
}

func rejectMaterializationCandidate(
	ctx context.Context,
	candidate BuiltImageCandidate,
	rejection error,
	remove materializationCandidateRemover,
) (providers.GraphNodeMaterializeResult, error) {
	cleanupContext := context.WithoutCancel(ctx)
	if err := remove(cleanupContext, candidate); err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("%w; remove rejected materialization candidate %s: %v", rejection, candidate.ImageID, err)
	}
	return providers.GraphNodeMaterializeResult{}, rejection
}

// RemoveBuiltImageCandidate removes exactly the untagged immutable image ID
// produced by the current materialization build. It never invokes a Docker
// prune operation or discovers other images.
func RemoveBuiltImageCandidate(ctx context.Context, candidate BuiltImageCandidate) error {
	return removeBuiltImageCandidate(ctx, candidate, runDockerOutput)
}

func removeBuiltImageCandidate(ctx context.Context, candidate BuiltImageCandidate, run dockerOutputRunner) error {
	if ctx == nil {
		return fmt.Errorf("remove materialization candidate requires a context")
	}
	if err := candidate.ImageID.Validate(); err != nil {
		return fmt.Errorf("remove materialization candidate image ID: %w", err)
	}
	if run == nil {
		return fmt.Errorf("remove materialization candidate requires a Docker runner")
	}
	if _, err := run(ctx, "image", "rm", string(candidate.ImageID)); err != nil {
		return fmt.Errorf("remove materialization candidate %s: %w", candidate.ImageID, err)
	}
	return nil
}

// AcceptMaterializationLayer is the publication boundary for one provider
// prefix. An inspected Docker image remains only a candidate until this
// function validates complete evidence for the transaction's generated tools
// and the bundle's public outputs.
func AcceptMaterializationLayer(
	ctx context.Context,
	input MaterializationEvidenceInput,
	run MaterializationEvidenceRunner,
) (providers.GraphNodeMaterializeResult, error) {
	if ctx == nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("materialization acceptance requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphNodeMaterializeResult{}, err
	}
	if err := validateMaterializationEvidenceInput(input); err != nil {
		return providers.GraphNodeMaterializeResult{}, err
	}
	if run == nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("materialization acceptance requires an evidence runner")
	}
	generated, outputs, err := run(ctx, input)
	if err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("validate materialization layer for %q: %w", input.Transaction.NodeID, err)
	}
	if err := ctx.Err(); err != nil {
		return providers.GraphNodeMaterializeResult{}, err
	}
	generated = append([]providers.RealizedGeneratedExecutable{}, generated...)
	outputs = append([]providers.RealizedOutput{}, outputs...)
	sort.Slice(generated, func(left int, right int) bool {
		return generated[left].Declaration.ID < generated[right].Declaration.ID
	})
	sort.Slice(outputs, func(left int, right int) bool {
		if outputs[left].SupplierComponent != outputs[right].SupplierComponent {
			return outputs[left].SupplierComponent < outputs[right].SupplierComponent
		}
		return outputs[left].Name < outputs[right].Name
	})
	if err := providers.ValidateMaterializationGeneratedExecutables(input.Transaction, generated); err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("accept materialization generated executables: %w", err)
	}
	if err := providers.ValidateRealizedBundleOutputs(input.Bundle, outputs); err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("accept materialization public outputs: %w", err)
	}
	transactionDigest, err := providers.MaterializationTransactionDigest(input.Transaction)
	if err != nil {
		return providers.GraphNodeMaterializeResult{}, fmt.Errorf("accept materialization transaction identity: %w", err)
	}
	return providers.GraphNodeMaterializeResult{
		Image: input.Candidate.Image.Image, TransactionDigest: transactionDigest,
		GeneratedExecutables: generated, Outputs: outputs,
	}, nil
}

func validateMaterializationEvidenceInput(input MaterializationEvidenceInput) error {
	if err := validateMaterializationBuildBinding(input.Transaction, input.Bundle, input.Candidate.AssemblyKey.Platform); err != nil {
		return err
	}
	expectedKey, expectedDigest, err := MaterializationAssemblyKey(input.Transaction, input.Bundle.Payload.Platform)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(input.Candidate.AssemblyKey, expectedKey) || input.Candidate.AssemblyKeyDigest != expectedDigest {
		return fmt.Errorf("materialization acceptance candidate assembly identity does not match the transaction")
	}
	if err := ValidateInspectedMaterializationCandidate(input.Transaction, input.Candidate.Image); err != nil {
		return fmt.Errorf("materialization acceptance candidate: %w", err)
	}
	return nil
}

func validateMaterializationBuildBinding(transaction providers.MaterializationTransaction, bundle providers.ResolvedBundle, platform blueprint.Platform) error {
	if err := providers.ValidateMaterializationTransaction(transaction); err != nil {
		return fmt.Errorf("materialization acceptance transaction: %w", err)
	}
	if err := platform.Validate(); err != nil {
		return fmt.Errorf("materialization acceptance platform: %w", err)
	}
	if err := bundle.Identity.Validate(); err != nil {
		return fmt.Errorf("materialization acceptance bundle identity: %w", err)
	}
	if bundle.Payload.NodeID != transaction.NodeID {
		return fmt.Errorf("materialization acceptance bundle node does not match the transaction")
	}
	if bundle.Payload.Upstream != transaction.Upstream {
		return fmt.Errorf("materialization acceptance bundle upstream does not match the transaction")
	}
	if bundle.Payload.Platform != platform {
		return fmt.Errorf("materialization acceptance bundle platform does not match the assembly")
	}
	return nil
}
