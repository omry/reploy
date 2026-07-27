package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
)

var ErrDesiredStateAlreadyExists = errors.New("deployment desired state already exists")

// DesiredStateUpdateResult reports the complete desired state after staging.
// Changed is false when the deployment already contains the same canonical
// blueprint, platform, and overlay.
type DesiredStateUpdateResult struct {
	State   StateV1
	Changed bool
}

// SetDesiredStateV1 atomically stages a resolved blueprint and selected target
// platform. Existing overlay intent and the last successful generation are
// retained. If that overlay is invalid for the new blueprint, no state is
// changed. Staging never resolves providers or builds an image.
func SetDesiredStateV1(
	ctx context.Context,
	deploymentDir string,
	document blueprint.Document,
	platform blueprint.Platform,
	validatePackage PackageRequestValidator,
) (result DesiredStateUpdateResult, err error) {
	return setDesiredStateV1(ctx, deploymentDir, document, platform, validatePackage, nil, "", false, nil)
}

// CreateDesiredStateV1 atomically stages a new deployment and refuses to
// replace any existing state. It shares SetDesiredStateV1's validation and
// publication rules.
func CreateDesiredStateV1(
	ctx context.Context,
	deploymentDir string,
	document blueprint.Document,
	platform blueprint.Platform,
	validatePackage PackageRequestValidator,
) (result DesiredStateUpdateResult, err error) {
	return setDesiredStateV1(ctx, deploymentDir, document, platform, validatePackage, nil, "", true, nil)
}

// SetStagedDesiredStateV1 records the author-provided blueprint text alongside
// the resolved document.
func SetStagedDesiredStateV1(
	ctx context.Context,
	deploymentDir string,
	document blueprint.Document,
	platform blueprint.Platform,
	validatePackage PackageRequestValidator,
	blueprintSource string,
	requireMissing bool,
) (result DesiredStateUpdateResult, err error) {
	staging := &StagingStateV1{Schema: StagingStateSchemaV1}
	return setDesiredStateV1(ctx, deploymentDir, document, platform, validatePackage, staging, blueprintSource, requireMissing, nil)
}

// CreateStagedDesiredStateWithPackageOverridesV1 records a new staged
// deployment and its local-development package overrides under one operation
// lock. It refuses to replace either existing deployment state or an existing
// sidecar.
func CreateStagedDesiredStateWithPackageOverridesV1(
	ctx context.Context,
	deploymentDir string,
	document blueprint.Document,
	platform blueprint.Platform,
	validatePackage PackageRequestValidator,
	blueprintSource string,
	overrides PackageOverridesV1,
) (result DesiredStateUpdateResult, err error) {
	staging := &StagingStateV1{Schema: StagingStateSchemaV1}
	return setDesiredStateV1(
		ctx, deploymentDir, document, platform, validatePackage,
		staging, blueprintSource, true, &overrides,
	)
}

func setDesiredStateV1(
	ctx context.Context,
	deploymentDir string,
	document blueprint.Document,
	platform blueprint.Platform,
	validatePackage PackageRequestValidator,
	staging *StagingStateV1,
	blueprintSource string,
	requireMissing bool,
	initialOverrides *PackageOverridesV1,
) (result DesiredStateUpdateResult, err error) {
	lock, err := AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	defer func() {
		if unlockErr := lock.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()

	current, found, err := lock.ReadStateV1()
	if err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("read deployment state: %w", err)
	}
	if requireMissing && found {
		return DesiredStateUpdateResult{}, ErrDesiredStateAlreadyExists
	}
	if initialOverrides != nil {
		if !requireMissing {
			return DesiredStateUpdateResult{}, fmt.Errorf("initial package overrides require a new staged deployment")
		}
		if initialOverrides.Environment.ID != document.Environment.ID {
			return DesiredStateUpdateResult{}, fmt.Errorf(
				"package overrides target environment %q, want %q",
				initialOverrides.Environment.ID,
				document.Environment.ID,
			)
		}
		if _, sidecarFound, readErr := ReadPackageOverridesV1(deploymentDir); readErr != nil {
			return DesiredStateUpdateResult{}, readErr
		} else if sidecarFound {
			return DesiredStateUpdateResult{}, fmt.Errorf(
				"%s already exists in the staging directory",
				PackageOverridesFilename,
			)
		}
	}
	overlay := EmptyRequestOverlayV1()
	var generation *EnvironmentGenerationState
	var deployment *DeploymentStateV1
	if found {
		currentDocument, decodeErr := blueprint.DecodeResolvedDocumentV1(current.Blueprint)
		if decodeErr != nil {
			return DesiredStateUpdateResult{}, fmt.Errorf("decode staged blueprint: %w", decodeErr)
		}
		if currentDocument.Environment.ID != document.Environment.ID {
			return DesiredStateUpdateResult{}, fmt.Errorf(
				"staging directory belongs to environment %q, not %q; use a different staging directory",
				currentDocument.Environment.ID,
				document.Environment.ID,
			)
		}
		overlay = current.Overlay
		generation = current.Current
		deployment = current.Deployment
		if staging == nil {
			staging = current.Staging
			blueprintSource = current.BlueprintSource
		}
	}

	payload, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	if err := blueprint.ValidateSelectedPlatform(document, platform); err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("stage selected platform: %w", err)
	}
	overlay, err = NormalizeRequestOverlayV1(document, overlay, validatePackage)
	if err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("retain request overlay for staged blueprint: %w", err)
	}

	candidate := StateV1{
		Schema: StateSchemaV1, Blueprint: payload, BlueprintSource: blueprintSource,
		Platform: platform, Overlay: overlay, Current: generation,
		Staging: staging, Deployment: deployment,
	}
	candidateBytes, err := EncodeStateV1(candidate)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	result = DesiredStateUpdateResult{State: candidate, Changed: true}
	if found {
		currentBytes, encodeErr := EncodeStateV1(current)
		if encodeErr != nil {
			return DesiredStateUpdateResult{}, encodeErr
		}
		if bytes.Equal(currentBytes, candidateBytes) {
			result.Changed = false
			return result, nil
		}
	}

	if initialOverrides != nil {
		if err := lock.CommitPackageOverridesV1(*initialOverrides); err != nil {
			return DesiredStateUpdateResult{}, err
		}
	}
	if err := lock.CommitStateV1(generation, candidate); err != nil {
		if initialOverrides != nil {
			err = errors.Join(err, lock.removePackageOverridesV1())
		}
		return DesiredStateUpdateResult{}, fmt.Errorf("write deployment state: %w", err)
	}
	return result, nil
}

// SetDesiredPlatformV1 atomically updates only the selected target of an
// existing deployment. The resolved blueprint, request overlay, and current
// generation are retained exactly.
func SetDesiredPlatformV1(
	ctx context.Context,
	deploymentDir string,
	platform blueprint.Platform,
	validatePackage PackageRequestValidator,
) (result DesiredStateUpdateResult, err error) {
	return SelectDesiredPlatformV1(ctx, deploymentDir, func(blueprint.Document) (blueprint.Platform, error) {
		return platform, nil
	}, validatePackage)
}

// DesiredPlatformSelector chooses one backend-supported target from the exact
// resolved blueprint protected by the deployment operation lock.
type DesiredPlatformSelector func(blueprint.Document) (blueprint.Platform, error)

// SelectDesiredPlatformV1 selects and commits a target without allowing the
// stored blueprint to change between those operations.
func SelectDesiredPlatformV1(
	ctx context.Context,
	deploymentDir string,
	selectPlatform DesiredPlatformSelector,
	validatePackage PackageRequestValidator,
) (result DesiredStateUpdateResult, err error) {
	if selectPlatform == nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("select desired platform requires a selector")
	}
	lock, err := AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	defer func() {
		if unlockErr := lock.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()

	current, found, err := lock.ReadStateV1()
	if err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("read deployment state: %w", err)
	}
	if !found {
		return DesiredStateUpdateResult{}, fmt.Errorf("deployment desired state is missing")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(current.Blueprint)
	if err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("decode staged blueprint: %w", err)
	}
	platform, err := selectPlatform(document)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	if err := blueprint.ValidateSelectedPlatform(document, platform); err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("stage selected platform: %w", err)
	}
	overlay, err := NormalizeRequestOverlayV1(document, current.Overlay, validatePackage)
	if err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("retain request overlay for selected platform: %w", err)
	}

	candidate := current
	candidate.Platform = platform
	candidate.Overlay = overlay
	candidateBytes, err := EncodeStateV1(candidate)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	currentBytes, err := EncodeStateV1(current)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	result = DesiredStateUpdateResult{State: candidate, Changed: !bytes.Equal(currentBytes, candidateBytes)}
	if !result.Changed {
		return result, nil
	}
	if err := lock.CommitStateV1(current.Current, candidate); err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf("write deployment state: %w", err)
	}
	return result, nil
}
