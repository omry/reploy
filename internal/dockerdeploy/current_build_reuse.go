package dockerdeploy

import (
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

type CurrentBuildReuseInput struct {
	ResolvedRequest  providers.ResolvedRequestV1
	Overlay          deploy.RequestOverlayV1
	PackageOverrides deploy.PackageOverrideIntentV1
	Base             deploy.ImageDescriptor
	Document         blueprint.Document
	DockerPlan       DockerExecutionPlan
	StartupVerifier  deploy.ApplicationStartupVerifierV1
}

// CurrentBuildMatches returns false for a valid but changed build input. It
// performs no I/O; malformed current state or candidate inputs remain errors.
func CurrentBuildMatches(current CurrentBuild, input CurrentBuildReuseInput) (bool, error) {
	if err := deploy.ValidateStateV1(current.State); err != nil {
		return false, fmt.Errorf("current build reuse state: %w", err)
	}
	if current.State.Current == nil || !reflect.DeepEqual(*current.State.Current, current.Generation) {
		return false, fmt.Errorf("current build reuse state does not name the supplied generation")
	}
	blueprintPayload, err := blueprint.EncodeResolvedDocumentV1(input.Document)
	if err != nil {
		return false, err
	}
	if blueprintPayload != current.State.Blueprint {
		return false, nil
	}
	if current.State.Platform != input.ResolvedRequest.Platform {
		return false, nil
	}
	if err := validateGenerationBuildLock(current.Generation, current.Lock, registry.ValidateRequirementProfileV1); err != nil {
		return false, fmt.Errorf("current build reuse: %w", err)
	}
	requestDigest, err := providers.ResolvedRequestDigest(input.ResolvedRequest, registry.ValidateResolvedRequestOwnersV1)
	if err != nil {
		return false, err
	}
	overlayDigest, err := deploy.RequestOverlayDigestV1(input.Overlay)
	if err != nil {
		return false, err
	}
	if overlayDigest != input.ResolvedRequest.OverlayDigest {
		return false, fmt.Errorf("current build reuse overlay does not match the resolved request")
	}
	stateOverlayDigest, err := deploy.RequestOverlayDigestV1(current.State.Overlay)
	if err != nil {
		return false, err
	}
	if stateOverlayDigest != overlayDigest {
		return false, nil
	}
	if err := input.Base.Validate(); err != nil {
		return false, fmt.Errorf("current build reuse base: %w", err)
	}
	if input.Base.Platform != input.ResolvedRequest.Platform {
		return false, fmt.Errorf("current build reuse base platform does not match the resolved request")
	}
	if err := deploy.ValidateApplicationStartupVerifierV1(input.StartupVerifier, true); err != nil {
		return false, fmt.Errorf("current build reuse startup verifier: %w", err)
	}
	baseReference, err := resolvedRequestBaseReference(input.ResolvedRequest)
	if err != nil {
		return false, err
	}
	if input.Base.AuthorReference != baseReference {
		return false, fmt.Errorf("current build reuse base descriptor does not match the resolved base request")
	}
	plans, err := RuntimePlansV1(input.Document, input.DockerPlan)
	if err != nil {
		return false, err
	}
	policy, err := CompileRuntimePolicyFromLockV1(input.Document, current.Lock, plans)
	if err != nil {
		return false, err
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(policy)
	if err != nil {
		return false, err
	}
	lockedOverlayDigest, err := deploy.RequestOverlayDigestV1(current.Lock.Overlay)
	if err != nil {
		return false, err
	}
	lockedPolicyDigest, err := deploy.RuntimePolicyDigestV1(current.Lock.RuntimePolicy)
	if err != nil {
		return false, err
	}
	if current.Lock.ResolvedRequestDigest != requestDigest ||
		lockedOverlayDigest != overlayDigest ||
		!reflect.DeepEqual(current.Lock.PackageOverrides, input.PackageOverrides) ||
		current.Lock.Platform != input.ResolvedRequest.Platform ||
		!reflect.DeepEqual(current.Lock.Base, input.Base) ||
		lockedPolicyDigest != policyDigest ||
		!reflect.DeepEqual(current.Lock.RuntimeLayer.Verifier, input.StartupVerifier) {
		return false, nil
	}
	return true, nil
}

// rebindCurrentBuildLockV1 returns a lock for the desired resolved document
// while preserving the exact validated image, provider closure, and runtime
// policy of a build that already passed CurrentBuildMatches.
func rebindCurrentBuildLockV1(lock deploy.BuildLockV1, document blueprint.Document) (deploy.BuildLockV1, error) {
	blueprintPayload, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		return deploy.BuildLockV1{}, err
	}
	blueprintDigest, err := blueprint.ResolvedDocumentDigestV1(blueprintPayload)
	if err != nil {
		return deploy.BuildLockV1{}, err
	}
	rebound := lock
	rebound.BlueprintDigest = blueprintDigest
	if err := deploy.ValidateBuildLockV1(rebound, registry.ValidateRequirementProfileV1); err != nil {
		return deploy.BuildLockV1{}, fmt.Errorf("rebind current build lock: %w", err)
	}
	return rebound, nil
}
