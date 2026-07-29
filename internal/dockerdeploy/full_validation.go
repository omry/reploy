package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type FullImageValidationInput struct {
	Image         InspectedImageCandidate
	Profiles      []providers.RequirementProfile
	Outputs       []providers.RealizedOutput
	RuntimePolicy deploy.RuntimePolicyV1
}

type FullImageValidationRunner func(
	context.Context,
	FullImageValidationInput,
) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error)

type PublishedImageValidation struct {
	Record    deploy.PrefixValidationV1
	Reference providerstore.StoreObjectRef
}

type BuildValidationResult struct {
	Layers []PublishedImageValidation
	Final  PublishedImageValidation
}

// ValidateImage performs complete image validation without publishing the
// resulting evidence. Callers that only audit existing state must use this
// path so verification cannot mutate the provider store.
func ValidateImage(
	ctx context.Context,
	input FullImageValidationInput,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	run FullImageValidationRunner,
) (deploy.PrefixValidationV1, error) {
	if ctx == nil {
		return deploy.PrefixValidationV1{}, fmt.Errorf("full image validation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return deploy.PrefixValidationV1{}, err
	}
	if err := validateFullImageValidationInput(input, validateProfileOwner); err != nil {
		return deploy.PrefixValidationV1{}, err
	}
	if run == nil {
		return deploy.PrefixValidationV1{}, fmt.Errorf("full image validation requires a runner")
	}
	profiles, outputs, err := run(ctx, input)
	if err != nil {
		return deploy.PrefixValidationV1{}, fmt.Errorf("validate image %s: %w", input.Image.Image.Digest, err)
	}
	profiles = append([]providers.ValidationEvidence{}, profiles...)
	outputs = append([]providers.ExecutableEvidence{}, outputs...)
	sort.Slice(profiles, func(left int, right int) bool { return profiles[left].ProfileDigest < profiles[right].ProfileDigest })
	sort.Slice(outputs, func(left int, right int) bool {
		if outputs[left].Output.Component != outputs[right].Output.Component {
			return outputs[left].Output.Component < outputs[right].Output.Component
		}
		return outputs[left].Output.Name < outputs[right].Output.Name
	})
	if err := validateFullImageEvidence(input, profiles, outputs, validateProfileOwner); err != nil {
		return deploy.PrefixValidationV1{}, err
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(input.RuntimePolicy)
	if err != nil {
		return deploy.PrefixValidationV1{}, err
	}
	record := deploy.PrefixValidationV1{
		Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: input.Image.Image.RootFSSubject,
		Profiles: profiles, RuntimePolicy: policyDigest, ExposedOutputs: outputs,
	}
	if err := deploy.ValidatePrefixValidation(record); err != nil {
		return deploy.PrefixValidationV1{}, fmt.Errorf("validate full image evidence record: %w", err)
	}
	return record, nil
}

func ValidateAndPublishImage(
	ctx context.Context,
	store providerstore.Store,
	input FullImageValidationInput,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	run FullImageValidationRunner,
) (PublishedImageValidation, error) {
	validateCtx, endValidate := buildprofile.Start(ctx, "Probe and validate image")
	record, err := ValidateImage(validateCtx, input, validateProfileOwner, run)
	endValidate(err)
	if err != nil {
		return PublishedImageValidation{}, err
	}
	publishCtx, endPublish := buildprofile.Start(ctx, "Publish validation evidence")
	reference, err := deploy.PublishPrefixValidation(publishCtx, store, record)
	endPublish(err)
	if err != nil {
		return PublishedImageValidation{}, fmt.Errorf("publish full image validation: %w", err)
	}
	return PublishedImageValidation{Record: record, Reference: reference}, nil
}

func ValidateBuildImages(
	ctx context.Context,
	store providerstore.Store,
	layers []FullImageValidationInput,
	final FullImageValidationInput,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	run FullImageValidationRunner,
) (BuildValidationResult, error) {
	result := BuildValidationResult{Layers: []PublishedImageValidation{}}
	if len(layers) != 0 && !reflect.DeepEqual(final, layers[len(layers)-1]) {
		return BuildValidationResult{}, fmt.Errorf("final image validation does not match the last component layer")
	}
	for index, layer := range layers {
		validated, err := ValidateAndPublishImage(ctx, store, layer, validateProfileOwner, run)
		if err != nil {
			return BuildValidationResult{}, fmt.Errorf("validate component layer %d: %w", index+1, err)
		}
		result.Layers = append(result.Layers, validated)
	}
	if len(layers) != 0 {
		result.Final = result.Layers[len(result.Layers)-1]
		return result, nil
	}
	validated, err := ValidateAndPublishImage(ctx, store, final, validateProfileOwner, run)
	if err != nil {
		return BuildValidationResult{}, fmt.Errorf("validate final image: %w", err)
	}
	result.Final = validated
	return result, nil
}

func validateFullImageValidationInput(input FullImageValidationInput, validateProfileOwner providers.RequirementProfileOwnerValidator) error {
	if err := ValidateInspectedImageCandidateIdentity(input.Image); err != nil {
		return fmt.Errorf("full validation image: %w", err)
	}
	if input.Profiles == nil || input.Outputs == nil {
		return fmt.Errorf("full validation profiles and outputs must use arrays")
	}
	if validateProfileOwner == nil {
		return fmt.Errorf("full validation profile owner validator is required")
	}
	profileDigests := map[string]bool{}
	for index, profile := range input.Profiles {
		digest, err := providers.RequirementProfileDigest(profile, validateProfileOwner)
		if err != nil {
			return fmt.Errorf("full validation profile %d: %w", index, err)
		}
		if profile.Platform != input.Image.Descriptor.Platform {
			return fmt.Errorf("full validation profile %s platform does not match the image", digest)
		}
		if profileDigests[string(digest)] {
			return fmt.Errorf("full validation contains duplicate requirement profile %s", digest)
		}
		profileDigests[string(digest)] = true
	}
	qualifiedOutputs := map[string]bool{}
	for index, output := range input.Outputs {
		if err := providers.ValidateRealizedOutput(output); err != nil {
			return fmt.Errorf("full validation output %d: %w", index, err)
		}
		key := output.SupplierComponent + "\x00" + output.Name
		if qualifiedOutputs[key] {
			return fmt.Errorf("full validation contains duplicate output %s.%s", output.SupplierComponent, output.Name)
		}
		qualifiedOutputs[key] = true
	}
	if err := deploy.ValidateRuntimePolicyV1(input.RuntimePolicy); err != nil {
		return fmt.Errorf("full validation runtime policy: %w", err)
	}
	return nil
}

func validateFullImageEvidence(
	input FullImageValidationInput,
	profiles []providers.ValidationEvidence,
	outputs []providers.ExecutableEvidence,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
) error {
	expectedProfiles := make(map[string]bool, len(input.Profiles))
	for _, profile := range input.Profiles {
		digest, err := providers.RequirementProfileDigest(profile, validateProfileOwner)
		if err != nil {
			return err
		}
		expectedProfiles[string(digest)] = true
	}
	if len(profiles) != len(expectedProfiles) {
		return fmt.Errorf("full validation returned %d profile records, want %d", len(profiles), len(expectedProfiles))
	}
	for index, evidence := range profiles {
		if err := evidence.Validate(); err != nil {
			return fmt.Errorf("full validation profile evidence %d: %w", index, err)
		}
		if evidence.SubjectRootFS != input.Image.Image.RootFSSubject || !expectedProfiles[string(evidence.ProfileDigest)] {
			return fmt.Errorf("full validation profile evidence %s does not match the requested image and profiles", evidence.ProfileDigest)
		}
		if index > 0 && profiles[index-1].ProfileDigest == evidence.ProfileDigest {
			return fmt.Errorf("full validation returned duplicate profile evidence %s", evidence.ProfileDigest)
		}
	}
	expectedOutputs := make(map[string]providers.RealizedOutput, len(input.Outputs))
	for _, output := range input.Outputs {
		expectedOutputs[output.SupplierComponent+"\x00"+output.Name] = output
	}
	if len(outputs) != len(expectedOutputs) {
		return fmt.Errorf("full validation returned %d output records, want %d", len(outputs), len(expectedOutputs))
	}
	for index, evidence := range outputs {
		if err := providers.ValidateFinalExecutableEvidence(evidence); err != nil {
			return fmt.Errorf("full validation output evidence %d: %w", index, err)
		}
		key := evidence.Output.Component + "\x00" + evidence.Output.Name
		expected, found := expectedOutputs[key]
		if !found || evidence.InvocationPath != expected.Candidate.InvocationPath {
			return fmt.Errorf("full validation output evidence %s.%s does not match the requested output", evidence.Output.Component, evidence.Output.Name)
		}
		if index > 0 && outputs[index-1].Output == evidence.Output {
			return fmt.Errorf("full validation returned duplicate output evidence %s.%s", evidence.Output.Component, evidence.Output.Name)
		}
	}
	return nil
}
