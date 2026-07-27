package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/deploy"
)

func InspectFinalizedImageCandidate(ctx context.Context, built BuiltImageCandidate, request FinalizationBuildRequest) (InspectedImageCandidate, error) {
	return inspectFinalizedImageCandidate(ctx, built, request, runDockerOutput)
}

func inspectFinalizedImageCandidate(ctx context.Context, built BuiltImageCandidate, request FinalizationBuildRequest, run dockerOutputRunner) (InspectedImageCandidate, error) {
	if err := validateFinalizationBuildRequest(request); err != nil {
		return InspectedImageCandidate{}, err
	}
	finalized, err := inspectBuiltImageCandidate(ctx, built, request.Platform, run)
	if err != nil {
		return InspectedImageCandidate{}, err
	}
	if finalized.Image.RootFSSubject != request.Source.Image.RootFSSubject {
		return InspectedImageCandidate{}, fmt.Errorf("finalized image changed the validated rootfs subject")
	}
	if err := request.Source.Config.Validate(); err != nil {
		return InspectedImageCandidate{}, fmt.Errorf("finalization source config: %w", err)
	}
	if !reflect.DeepEqual(finalized.Config, request.Source.Config) {
		return InspectedImageCandidate{}, fmt.Errorf("finalized image changed non-label image configuration")
	}
	if err := deploy.ValidatePrefixValidationLabels(finalized.Labels, finalized.Image, request.ValidationReference); err != nil {
		return InspectedImageCandidate{}, err
	}
	expectedLabels := make(map[string]string, len(request.Source.Labels)+3)
	for name, value := range request.Source.Labels {
		expectedLabels[name] = value
	}
	validationLabels, err := deploy.PrefixValidationLabels(finalized.Image.RootFSSubject, request.ValidationReference)
	if err != nil {
		return InspectedImageCandidate{}, err
	}
	for _, label := range validationLabels {
		expectedLabels[label.Name] = label.Value
	}
	if !reflect.DeepEqual(finalized.Labels, expectedLabels) {
		return InspectedImageCandidate{}, fmt.Errorf("finalized image changed labels outside the validation contract")
	}
	return finalized, nil
}
