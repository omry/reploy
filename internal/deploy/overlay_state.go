package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const deploymentStateRelativePath = ".reploy/state.json"

type RequestOverlayMutation func(blueprint.Document, RequestOverlayV1) (RequestOverlayV1, error)

type RequestOverlayDocumentResolver func(DeploymentState) (blueprint.Document, error)

type RequestOverlayMutationResult struct {
	Overlay RequestOverlayV1
	Digest  canonical.Digest
	Changed bool
}

// MutateRequestOverlayV1 performs one complete directory-scoped state
// transaction. It never resolves packages, prepares a bundle, or builds an
// image; changed intent only invalidates the retained prototype build facts.
func MutateRequestOverlayV1(
	ctx context.Context,
	deploymentDir string,
	validatePackage PackageRequestValidator,
	mutate RequestOverlayMutation,
) (result RequestOverlayMutationResult, err error) {
	return mutateRequestOverlayV1(ctx, deploymentDir, resolveRequestOverlayDocument, validatePackage, mutate)
}

func mutateRequestOverlayV1(
	ctx context.Context,
	deploymentDir string,
	resolveDocument RequestOverlayDocumentResolver,
	validatePackage PackageRequestValidator,
	mutate RequestOverlayMutation,
) (result RequestOverlayMutationResult, err error) {
	if mutate == nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("request overlay mutation is required")
	}
	if resolveDocument == nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("request overlay document resolver is required")
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

	statePath := filepath.Join(deploymentDir, filepath.FromSlash(deploymentStateRelativePath))
	info, err := os.Lstat(statePath)
	if err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("inspect deployment state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return RequestOverlayMutationResult{}, fmt.Errorf("deployment state path must be a regular file: %s", statePath)
	}
	content, err := os.ReadFile(statePath)
	if err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("read deployment state: %w", err)
	}
	state, err := ParseDeploymentState(content)
	if err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("parse deployment state: %w", err)
	}
	document, err := resolveDocument(state)
	if err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("resolve request overlay blueprint: %w", err)
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
	state.Bundle.PreparedFingerprint = ""
	state.Materialization = nil
	nextContent, err := MarshalDeploymentState(state)
	if err != nil {
		return RequestOverlayMutationResult{}, err
	}
	if err := writeAtomicStateFile(statePath, nextContent, info.Mode().Perm()); err != nil {
		return RequestOverlayMutationResult{}, fmt.Errorf("write deployment state: %w", err)
	}
	return result, nil
}

func resolveRequestOverlayDocument(state DeploymentState) (blueprint.Document, error) {
	pack, err := LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return blueprint.Document{}, err
	}
	if pack.Environment == nil {
		return blueprint.Document{}, fmt.Errorf("blueprint does not use the environment model")
	}
	return *pack.Environment, nil
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
