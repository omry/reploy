package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
)

// validateAPTProfileObservation reproduces one APT capability profile from
// the combined image probe. The fixed command checks run in the same held
// networkless session; no second filesystem probe is issued.
func validateAPTProfileObservation(
	ctx context.Context,
	session *ImageValidationSession,
	profile providers.RequirementProfile,
	toolInspection map[string]string,
	observations map[string]probe.ExecutableObservationV1,
) (APTBaseValidation, error) {
	if ctx == nil {
		return APTBaseValidation{}, fmt.Errorf("validate APT image profile requires a context")
	}
	if err := ctx.Err(); err != nil {
		return APTBaseValidation{}, err
	}
	if session == nil || session.closed || session.aptWorkspace == nil {
		return APTBaseValidation{}, fmt.Errorf("APT image validation session is not open")
	}
	if err := providers.ValidateRequirementProfile(profile, aptprovider.ValidateRequirementProfileV1); err != nil {
		return APTBaseValidation{}, fmt.Errorf("validate APT image profile: %w", err)
	}
	lockedBase, _, err := aptprovider.DecodeProfileFactsV1(profile.Facts)
	if err != nil {
		return APTBaseValidation{}, err
	}
	requiredTools := aptprovider.RequiredBaseToolsV1()
	if len(toolInspection) != len(requiredTools) {
		return APTBaseValidation{}, fmt.Errorf("APT image profile tool observations do not match the required tool set")
	}
	for _, tool := range requiredTools {
		if toolInspection[tool.Name] == "" {
			return APTBaseValidation{}, fmt.Errorf("APT image profile is missing observation mapping for %q", tool.Name)
		}
	}

	fresh, err := observeAPTBaseProfile(
		ctx,
		session.descriptor.Platform,
		func(_ context.Context, request probe.RequestV1) (probe.ResponseV1, error) {
			response := probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: make([]probe.ExecutableObservationV1, 0, len(request.Inspections))}
			for _, inspection := range request.Inspections {
				batchID := toolInspection[inspection.ID]
				observation, found := observations[batchID]
				if !found {
					return probe.ResponseV1{}, fmt.Errorf("APT image profile has no combined observation for %q", inspection.ID)
				}
				if observation.InvocationPath != inspection.InvocationPath {
					return probe.ResponseV1{}, fmt.Errorf("APT image profile observation for %q has path %q, want %q", inspection.ID, observation.InvocationPath, inspection.InvocationPath)
				}
				observation.ID = inspection.ID
				response.Observations = append(response.Observations, observation)
			}
			if err := probe.ValidateResponseV1(request, response); err != nil {
				return probe.ResponseV1{}, err
			}
			return response, nil
		},
		session.runAPTProfileCommand,
	)
	if err != nil {
		return APTBaseValidation{}, err
	}
	if fresh.Profile.Profile != lockedBase.Profile || fresh.Profile.Platform != lockedBase.Platform || fresh.Profile.NativeArchitecture != lockedBase.NativeArchitecture {
		return APTBaseValidation{}, fmt.Errorf("APT image capability profile no longer matches the locked platform family")
	}
	return fresh, nil
}
