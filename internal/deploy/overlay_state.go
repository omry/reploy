package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

type RequestOverlayMutation func(blueprint.Document, RequestOverlayV1) (RequestOverlayV1, error)

type RequestOverlayMutationResult struct {
	Overlay RequestOverlayV1
	Digest  canonical.Digest
	Changed bool
}

// MutateRequestOverlayV1 performs one complete directory-scoped state
// transaction. It never resolves packages, prepares a bundle, or builds an
// image. A changed desired overlay retains the current generation, which then
// fails exact reuse as stale until an explicit build publishes its replacement.
func MutateRequestOverlayV1(
	ctx context.Context,
	deploymentDir string,
	validatePackage PackageRequestValidator,
	mutate RequestOverlayMutation,
) (result RequestOverlayMutationResult, err error) {
	return mutateRequestOverlayV1(ctx, deploymentDir, validatePackage, mutate)
}

func mutateRequestOverlayV1(
	ctx context.Context,
	deploymentDir string,
	validatePackage PackageRequestValidator,
	mutate RequestOverlayMutation,
) (result RequestOverlayMutationResult, err error) {
	if mutate == nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("request overlay mutation is required")
	}
	lock, err := AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		return RequestOverlayMutationResult{}, err
	}
	defer func() {
		if unlockErr := lock.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()

	state, found, err := lock.ReadStateV1()
	if err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("read deployment state: %w", err)
	}
	if !found {
		return RequestOverlayMutationResult{}, fmt.Errorf("deployment state is missing; stage or install the deployment first")
	}
	if state.Deployment != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("request overlay cannot be changed on an installed deployment; change the staging source and install it again")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("load resolved blueprint: %w", err)
	}
	current, err := NormalizeRequestOverlayV1(document, state.Overlay, validatePackage)
	if err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("validate current request overlay: %w", err)
	}
	candidate, err := cloneRequestOverlayV1(current)
	if err != nil {
		return RequestOverlayMutationResult{}, err
	}
	candidate, err = mutate(document, candidate)
	if err != nil {
		return RequestOverlayMutationResult{}, err
	}
	candidate, err = NormalizeRequestOverlayV1(document, candidate, validatePackage)
	if err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("validate proposed request overlay: %w", err)
	}
	digest, err := RequestOverlayDigestV1(candidate)
	if err != nil {
		return RequestOverlayMutationResult{}, err
	}
	currentBytes, err := canonical.Marshal(current)
	if err != nil {
		return RequestOverlayMutationResult{}, err
	}
	candidateBytes, err := canonical.Marshal(candidate)
	if err != nil {
		return RequestOverlayMutationResult{}, err
	}
	result = RequestOverlayMutationResult{Overlay: candidate, Digest: digest, Changed: !bytes.Equal(currentBytes, candidateBytes)}
	if !result.Changed {
		return result, nil
	}

	state.Overlay = candidate
	if err := lock.CommitStateV1(state.Current, state); err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("write deployment state: %w", err)
	}
	return result, nil
}

func cloneRequestOverlayV1(overlay RequestOverlayV1) (RequestOverlayV1, error) {
	content, err := json.Marshal(overlay)
	if err != nil {
		return RequestOverlayV1{}, fmt.Errorf("clone request overlay: %w", err)
	}
	var clone RequestOverlayV1
	if err := json.Unmarshal(content, &clone); err != nil {
		return RequestOverlayV1{}, fmt.Errorf("clone request overlay: %w", err)
	}
	return clone, nil
}
