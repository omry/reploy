package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

const portableToolCaseEvidenceSchemaV1 = "portable-tool-case-evidence-v1"
const PortableToolCaseValidatorVersionV1 = "reploy-portable-tool-case-v2"

// PortableToolCaseObservationV1 can only be constructed by a successful case
// lifecycle. Its bytes are owned, immutable and include the original executor
// output. It is not an authenticated attestation: persisted evidence relies
// on the project-controlled workflow and provider store.
type PortableToolCaseObservationV1 struct {
	payload []byte
}

// PortableToolCaseEvidenceRequestV1 selects an exact derived case and its
// ordinary resolution scope for current-evidence matching.
type PortableToolCaseEvidenceRequestV1 struct {
	Case  toolcatalog.IntegrationCaseV1
	Scope string
}

type portableToolCaseEvidenceBundleV1 struct {
	Schema       string                           `json:"schema"`
	CaseID       canonical.Digest                 `json:"case_id"`
	Scope        string                           `json:"scope"`
	Image        deploy.ImageDescriptor           `json:"image"`
	Evidence     toolcatalog.ValidationEvidenceV1 `json:"evidence"`
	Observations []PortableToolProbeEvidenceV1    `json:"observations"`
}

type portableToolCaseCaptureV1 struct {
	image        deploy.ImageDescriptor
	observations []PortableToolProbeEvidenceV1
}

func (capture *portableToolCaseCaptureV1) attach(input *PortableToolMaterializationValidationInputV1) {
	capture.image = input.Image.Descriptor
	input.observe = func(observed PortableToolProbeEvidenceV1) {
		capture.observations = append(capture.observations, observed)
	}
}

// ObserveApplicationBuildCaseV1 retains original executor observations only
// after the ordinary application build, publication and cleanup all succeed.
func ObserveApplicationBuildCaseV1(
	ctx context.Context, input LockedProviderBuildExecutionInputV1,
	caseV1 toolcatalog.IntegrationCaseV1, scope string,
) (PortableToolCaseObservationV1, error) {
	return observeApplicationBuildCaseV1(ctx, input, caseV1, scope, ExecuteLockedProviderBuildV1)
}

func observeApplicationBuildCaseV1(
	ctx context.Context, input LockedProviderBuildExecutionInputV1,
	caseV1 toolcatalog.IntegrationCaseV1, scope string,
	execute func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error),
) (PortableToolCaseObservationV1, error) {
	var capture portableToolCaseCaptureV1
	_, err := validateApplicationBuildCaseV1(ctx, input, caseV1, scope,
		execute,
		func(ctx context.Context, store providerstore.Store, selected PortableToolMaterializationValidationInputV1) ([]providers.ValidationEvidence, error) {
			capture.attach(&selected)
			return ValidatePortableToolMaterializationV1(ctx, store, selected)
		})
	if err != nil {
		return PortableToolCaseObservationV1{}, err
	}
	return completePortableToolCaseObservationV1(ctx, caseV1, scope, capture)
}

// ObserveSourceBuilderBuildCaseV1 uses the ordinary builder case validator,
// retaining output only after the exact inspected image is removed successfully.
func ObserveSourceBuilderBuildCaseV1(
	ctx context.Context, store providerstore.Store, tools *SourceBuilderPortableToolsV1,
	upstream deploy.ImageDescriptor, options RunOptions,
	caseV1 toolcatalog.IntegrationCaseV1, scope string,
) (PortableToolCaseObservationV1, error) {
	var capture portableToolCaseCaptureV1
	_, err := validateSourceBuilderBuildCaseV1(ctx, store, tools, upstream, options, caseV1, scope, capture.attach)
	if err != nil {
		return PortableToolCaseObservationV1{}, err
	}
	return completePortableToolCaseObservationV1(ctx, caseV1, scope, capture)
}

func completePortableToolCaseObservationV1(
	ctx context.Context, caseV1 toolcatalog.IntegrationCaseV1, scope string, capture portableToolCaseCaptureV1,
) (PortableToolCaseObservationV1, error) {
	if ctx == nil {
		return PortableToolCaseObservationV1{}, fmt.Errorf("case observation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return PortableToolCaseObservationV1{}, err
	}
	record, err := expectedPortableToolCaseEvidenceV1(caseV1, scope, capture.observations)
	if err != nil {
		return PortableToolCaseObservationV1{}, err
	}
	bundle := portableToolCaseEvidenceBundleV1{
		Schema: portableToolCaseEvidenceSchemaV1, CaseID: caseV1.ID, Scope: scope,
		Image: capture.image, Evidence: record, Observations: capture.observations,
	}
	if err := requireCurrentPortableToolCaseBundleV1(bundle, caseV1, scope); err != nil {
		return PortableToolCaseObservationV1{}, err
	}
	payload, err := canonical.Marshal(bundle)
	if err != nil {
		return PortableToolCaseObservationV1{}, err
	}
	return PortableToolCaseObservationV1{payload: payload}, nil
}

func expectedPortableToolCaseEvidenceV1(
	caseV1 toolcatalog.IntegrationCaseV1, scope string, observations []PortableToolProbeEvidenceV1,
) (toolcatalog.ValidationEvidenceV1, error) {
	closure, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, scope)
	if err != nil {
		return toolcatalog.ValidationEvidenceV1{}, err
	}
	output, err := canonical.Sum("portable-tool-case-output", portableToolCaseEvidenceSchemaV1, observations)
	if err != nil {
		return toolcatalog.ValidationEvidenceV1{}, err
	}
	return toolcatalog.ValidationEvidenceV1{
		Schema: toolcatalog.ValidationEvidenceSchemaV1,
		Tool:   caseV1.Manifest.Tool, Version: caseV1.Manifest.Version, Revision: caseV1.Manifest.Revision,
		ManifestDigest: caseV1.ManifestReference.Digest, SelectedClosureDigest: closure.Identity,
		Context: caseV1.Support.Context, Target: caseV1.Target.Target,
		BaseImageDigest: caseV1.Fixture.BaseImageDigest,
		Bindings:        caseV1.Support.Bindings, Selections: caseV1.Support.Selections,
		Fixture: caseV1.Fixture.ID, ValidatorVersion: PortableToolCaseValidatorVersionV1,
		Result: "pass", ValidatorOutputDigest: output,
	}, nil
}

func requireCurrentPortableToolCaseBundleV1(
	bundle portableToolCaseEvidenceBundleV1, caseV1 toolcatalog.IntegrationCaseV1, scope string,
) error {
	if bundle.Schema != portableToolCaseEvidenceSchemaV1 || bundle.CaseID != caseV1.ID || bundle.Scope != scope {
		return fmt.Errorf("external evidence is not for this exact derived case and scope")
	}
	expected, err := expectedPortableToolCaseEvidenceV1(caseV1, scope, bundle.Observations)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(bundle.Evidence, expected) {
		return fmt.Errorf("external evidence differs from current case inputs or original output")
	}
	encoded, err := canonical.Marshal(bundle.Evidence)
	if err != nil {
		return err
	}
	if _, err := toolcatalog.DecodePortableToolValidationEvidenceV1(encoded); err != nil {
		return err
	}
	if err := bundle.Image.Validate(); err != nil {
		return err
	}
	if bundle.Image.Platform.Canonical != caseV1.Fixture.Target.Platform || len(bundle.Observations) != len(caseV1.Profiles) || len(bundle.Observations) == 0 {
		return fmt.Errorf("external evidence lacks exact image platform or complete profile observations")
	}
	closure, err := toolcatalog.EmbeddedSelectedClosureForIntegrationCaseV1(caseV1, scope)
	if err != nil {
		return err
	}
	plan, err := toolcatalog.CompilePortableToolPlanV1([]toolcatalog.SelectedClosureV1{closure})
	if err != nil {
		return err
	}
	if len(plan.Tools) != 1 || len(plan.Tools[0].ValidationProfiles) != len(bundle.Observations) {
		return fmt.Errorf("external evidence does not match current selected profiles")
	}
	for index, observed := range bundle.Observations {
		entry := providers.PortableToolScheduledValidationV1{
			Scope: scope, Tool: caseV1.Manifest.Tool,
			Profile: plan.Tools[0].ValidationProfiles[index], Runtime: plan.Tools[0].Runtime,
		}
		if err := requireAttributedPortableToolObservationsV1(entry, bundle.Image, observed); err != nil {
			return err
		}
	}
	return nil
}

// PersistPortableToolCaseEvidenceV1 publishes one complete external bundle
// atomically through the existing immutable validation-record store. A zero
// observation or a failed case cannot publish passing support evidence.
func PersistPortableToolCaseEvidenceV1(
	ctx context.Context, store providerstore.Store, observation PortableToolCaseObservationV1,
) (providerstore.StoreObjectRef, error) {
	if ctx == nil || len(observation.payload) == 0 {
		return providerstore.StoreObjectRef{}, fmt.Errorf("external evidence requires a completed harness observation and context")
	}
	var bundle portableToolCaseEvidenceBundleV1
	if err := json.Unmarshal(observation.payload, &bundle); err != nil {
		return providerstore.StoreObjectRef{}, err
	}
	digest, err := canonical.Sum("portable-tool-case-evidence", portableToolCaseEvidenceSchemaV1, bundle)
	if err != nil {
		return providerstore.StoreObjectRef{}, err
	}
	reference := providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: digest}
	if err := store.PublishValidationRecord(ctx, reference, observation.payload); err != nil {
		return providerstore.StoreObjectRef{}, err
	}
	return reference, nil
}

// MatchPortableToolCaseEvidenceV1 requires original successful fixed-executor
// output and every current derived-case binding. Record presence alone, or an
// independently asserted v1 record, cannot pass. Store ownership is the trust
// boundary; this matcher does not authenticate records from an untrusted store.
func MatchPortableToolCaseEvidenceV1(
	store providerstore.Store, caseV1 toolcatalog.IntegrationCaseV1, scope string,
	reference providerstore.StoreObjectRef,
) (toolcatalog.ValidationEvidenceV1, error) {
	payload, err := store.LoadValidationRecord(reference)
	if err != nil {
		return toolcatalog.ValidationEvidenceV1{}, err
	}
	var bundle portableToolCaseEvidenceBundleV1
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return toolcatalog.ValidationEvidenceV1{}, err
	}
	encoded, err := canonical.Marshal(bundle)
	if err != nil || !bytes.Equal(encoded, payload) {
		return toolcatalog.ValidationEvidenceV1{}, fmt.Errorf("external evidence must be the exact canonical workflow bundle")
	}
	digest, err := canonical.Sum("portable-tool-case-evidence", portableToolCaseEvidenceSchemaV1, bundle)
	if err != nil || digest != reference.Digest {
		return toolcatalog.ValidationEvidenceV1{}, fmt.Errorf("external evidence content differs from its workflow reference")
	}
	if err := requireCurrentPortableToolCaseBundleV1(bundle, caseV1, scope); err != nil {
		return toolcatalog.ValidationEvidenceV1{}, err
	}
	return bundle.Evidence, nil
}

// RequireCurrentPortableToolCaseEvidenceV1 applies the same exact matcher to
// a nonempty current case set. Callers select representative cases or derive
// the complete advertised set; this function makes no unexecuted support claim.
func RequireCurrentPortableToolCaseEvidenceV1(
	store providerstore.Store, requests []PortableToolCaseEvidenceRequestV1,
	references map[canonical.Digest]providerstore.StoreObjectRef,
) error {
	if len(requests) == 0 || len(requests) != len(references) {
		return fmt.Errorf("external evidence must cover exactly the requested derived cases")
	}
	seen := make(map[canonical.Digest]bool, len(requests))
	for _, request := range requests {
		if seen[request.Case.ID] {
			return fmt.Errorf("external evidence case set contains a duplicate")
		}
		seen[request.Case.ID] = true
		reference, ok := references[request.Case.ID]
		if !ok {
			return fmt.Errorf("external evidence is missing case %s", request.Case.ID)
		}
		if _, err := MatchPortableToolCaseEvidenceV1(store, request.Case, request.Scope, reference); err != nil {
			return fmt.Errorf("match external evidence for case %s: %w", request.Case.ID, err)
		}
	}
	return nil
}
