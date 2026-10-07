package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// ValidateApplicationBuildCaseV1 executes an ordinary prepared application
// build and observes its exact finalized image before publication. Observations
// are returned only after execution and all owned cleanup succeed; they are
// in-process results, not external support evidence.
func ValidateApplicationBuildCaseV1(
	ctx context.Context, input LockedProviderBuildExecutionInputV1,
	caseV1 toolcatalog.IntegrationCaseV1, scope string,
) ([]providers.ValidationEvidence, error) {
	return validateApplicationBuildCaseV1(ctx, input, caseV1, scope,
		ExecuteLockedProviderBuildV1, ValidatePortableToolMaterializationV1)
}

func validateApplicationBuildCaseV1(
	ctx context.Context, input LockedProviderBuildExecutionInputV1,
	caseV1 toolcatalog.IntegrationCaseV1, scope string,
	execute func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error),
	validate func(context.Context, providerstore.Store, PortableToolMaterializationValidationInputV1) ([]providers.ValidationEvidence, error),
) ([]providers.ValidationEvidence, error) {
	if ctx == nil || execute == nil || validate == nil {
		return nil, fmt.Errorf("application integration case requires a context and execution boundary")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := requireApplicationBuildCasePreparationV1(input.Preparation, caseV1, scope); err != nil {
		return nil, err
	}
	var observations []providers.ValidationEvidence
	var observedLock deploy.BuildLockV1
	var observationErr error
	called := false
	input.observeFinalImage = func(ctx context.Context, image InspectedImageCandidate, lock deploy.BuildLockV1) (callbackErr error) {
		defer func() { observationErr = callbackErr }()
		if called {
			return fmt.Errorf("application integration callback invoked more than once")
		}
		called = true
		observedLock = lock
		selected, err := PortableToolApplicationCaseValidationInputFromBuildLockV1(
			image, lock, input.Preparation.ApplicationTools.Closures, caseV1, scope)
		if err != nil {
			return err
		}
		observations, err = validate(ctx, input.Preparation.Store, selected)
		if err != nil {
			return err
		}
		return requirePortableToolValidationEvidenceForInputV1(selected, observations)
	}
	result, err := execute(ctx, input)
	if err != nil {
		return nil, err
	}
	if observationErr != nil {
		return nil, observationErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !called || len(observations) == 0 || result.Reused || !reflect.DeepEqual(result.Lock, observedLock) {
		return nil, fmt.Errorf("application integration case did not observe its materialized final image")
	}
	return append([]providers.ValidationEvidence{}, observations...), nil
}

func requireApplicationBuildCasePreparationV1(
	preparation LockedProviderBuildPreparationV1, caseV1 toolcatalog.IntegrationCaseV1, scope string,
) error {
	expected, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, scope)
	if err != nil {
		return err
	}
	selected := preparation.ApplicationTools
	if selected == nil || selected.sealed == nil || preparation.PreparedBase == nil || preparation.Reused ||
		!strings.HasPrefix(scope, "application:") || strings.TrimPrefix(scope, "application:") == "" ||
		caseV1.Support.Context != "runtime" || caseV1.Fixture.Context != "runtime" {
		return fmt.Errorf("application integration case requires a materialized application-scoped runtime selection")
	}
	base := preparation.PreparedBase.Descriptor
	if selected.Target != caseV1.Fixture.Target || caseV1.Target.Target != selected.Target ||
		base.Platform.Canonical != caseV1.Fixture.Target.Platform ||
		base.ManifestDigest != caseV1.Fixture.BaseImageDigest ||
		!reflect.DeepEqual(preparation.SelectedBase.Descriptor, base) {
		return fmt.Errorf("application integration case target or immutable base differs from ordinary build inputs")
	}
	digest, err := blueprint.DocumentDigestV1(preparation.Loaded.Document)
	if err != nil || digest != selected.sealed.documentDigest || digest != preparation.BlueprintDigest ||
		!reflect.DeepEqual(selected.Closures, selected.sealed.closures) {
		return fmt.Errorf("application integration case differs from sealed ordinary build selection")
	}
	matched := false
	for _, closure := range selected.Closures {
		if closure.Scope != scope || closure.Provenance.Tool != caseV1.Manifest.Tool {
			continue
		}
		if matched || !reflect.DeepEqual(closure, expected) {
			return fmt.Errorf("application integration case selects a different or duplicate closure")
		}
		matched = true
	}
	if !matched {
		return fmt.Errorf("application integration case has no matching selected closure")
	}
	return nil
}

// PortableToolApplicationCaseValidationInputFromBuildLockV1 combines exact
// final-image authority with the catalog-derived case's selected scope.
func PortableToolApplicationCaseValidationInputFromBuildLockV1(
	image InspectedImageCandidate, lock deploy.BuildLockV1,
	closures []toolcatalog.SelectedClosureV1, caseV1 toolcatalog.IntegrationCaseV1, scope string,
) (PortableToolMaterializationValidationInputV1, error) {
	if _, err := PortableToolApplicationValidationInputFromBuildLockV1(image, lock, scope); err != nil {
		return PortableToolMaterializationValidationInputV1{}, err
	}
	return portableToolCaseValidationInputFromLockV1(image, *lock.PortableTools, closures, caseV1, scope, "runtime")
}
