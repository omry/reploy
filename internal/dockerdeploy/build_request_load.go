package dockerdeploy

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type LoadedBuildRequestV1 struct {
	State            deploy.StateV1
	Document         blueprint.Document
	PackageOverrides deploy.PackageOverrideIntentV1
	BaseImage        string
	Request          providers.ResolvedRequestV1
}

// LoadBuildRequestV1 derives the canonical provider request from the desired
// inputs stored under an already-held deployment operation lock. Source
// manifests are explicit inputs because observing local source content is a
// separate build phase; this function performs no Docker or provider work.
func LoadBuildRequestV1(
	operation *deploy.OperationLock,
	sources []providers.ResolvedSourceInput,
) (LoadedBuildRequestV1, error) {
	if sources == nil {
		return LoadedBuildRequestV1{}, fmt.Errorf("load build request sources must use an array")
	}
	state, document, err := loadBuildStateDocumentV1(operation)
	if err != nil {
		return LoadedBuildRequestV1{}, err
	}
	return loadBuildRequestWithInputsV1(
		operation, deploy.EmptyPackageOverrideIntentV1(document.Environment.ID), "", sources, state, document,
	)
}

func LoadBuildRequestWithPackageOverridesV1(
	operation *deploy.OperationLock,
	packageOverrides deploy.PackageOverrideIntentV1,
	baseImage string,
	sources []providers.ResolvedSourceInput,
) (LoadedBuildRequestV1, error) {
	if operation == nil {
		return LoadedBuildRequestV1{}, fmt.Errorf("load build request requires an operation lock")
	}
	if sources == nil {
		return LoadedBuildRequestV1{}, fmt.Errorf("load build request sources must use an array")
	}
	state, document, err := loadBuildStateDocumentV1(operation)
	if err != nil {
		return LoadedBuildRequestV1{}, err
	}
	return loadBuildRequestWithInputsV1(operation, packageOverrides, baseImage, sources, state, document)
}

func loadBuildRequestWithInputsV1(
	operation *deploy.OperationLock,
	packageOverrides deploy.PackageOverrideIntentV1,
	baseImage string,
	sources []providers.ResolvedSourceInput,
	state deploy.StateV1,
	document blueprint.Document,
) (LoadedBuildRequestV1, error) {
	request, err := BuildResolvedRequestWithOverridesV1(
		document, state.Overlay, packageOverrides, baseImage, state.Platform,
		append([]providers.ResolvedSourceInput{}, sources...),
	)
	if err != nil {
		return LoadedBuildRequestV1{}, fmt.Errorf("load build request: %w", err)
	}
	return LoadedBuildRequestV1{
		State: state, Document: document, PackageOverrides: packageOverrides,
		BaseImage: baseImage, Request: request,
	}, nil
}

func loadBuildStateDocumentV1(operation *deploy.OperationLock) (deploy.StateV1, blueprint.Document, error) {
	if operation == nil {
		return deploy.StateV1{}, blueprint.Document{}, fmt.Errorf("load build request requires an operation lock")
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return deploy.StateV1{}, blueprint.Document{}, fmt.Errorf("load build state: %w", err)
	}
	if !found {
		return deploy.StateV1{}, blueprint.Document{}, fmt.Errorf("build state is missing; stage or install the deployment first")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return deploy.StateV1{}, blueprint.Document{}, fmt.Errorf("load build blueprint: %w", err)
	}
	return state, document, nil
}
