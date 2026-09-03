package dockerdeploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// PortableToolScheduledEvidenceV1 pairs one scheduled profile with the
// observations the fixed executor produced for it.
type PortableToolScheduledEvidenceV1 struct {
	Scope    string                                  `json:"scope"`
	Tool     string                                  `json:"tool"`
	Profile  providers.PortableToolRecordReferenceV1 `json:"profile"`
	Observed PortableToolProbeEvidenceV1             `json:"observed"`
}

var runScheduledPortableToolValidationProfile = RunPortableToolValidationProfile
var preparePortableToolValidationWorkspace = PrepareProbeWorkspace

// PortableToolMaterializationValidationInputV1 binds a usage owner's selected
// validation schedule to the concrete image that contains that selected
// closure. The image may be temporary or retained; validation does not classify
// it as a build or runtime image and does not infer scope from image placement.
type PortableToolMaterializationValidationInputV1 struct {
	Image    InspectedImageCandidate
	Schedule providers.PortableToolValidationScheduleV1
}

// ValidatePortableToolMaterializationV1 is the production acceptance boundary
// for a materialized portable-tool closure. The usage owner supplies both the
// exact image produced by materialization and the already-selected schedule.
// This function validates that image identity, runs every selected profile,
// and binds the resulting evidence to the image's observed root filesystem.
//
// The dedicated workspace matters when the usage owner is concurrently holding
// another validation session: each fixed-executor probe owns a fresh container
// whose name is derived from this workspace.
func ValidatePortableToolMaterializationV1(
	ctx context.Context,
	store providerstore.Store,
	input PortableToolMaterializationValidationInputV1,
) (evidence []providers.ValidationEvidence, resultErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("portable-tool materialization validation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateInspectedImageCandidateIdentity(input.Image); err != nil {
		return nil, fmt.Errorf("portable-tool materialization validation image: %w", err)
	}
	if err := providers.ValidatePortableToolValidationScheduleV1(input.Schedule); err != nil {
		return nil, fmt.Errorf("portable-tool materialization validation schedule: %w", err)
	}
	if len(input.Schedule.Entries) == 0 {
		return []providers.ValidationEvidence{}, nil
	}
	workspaceCtx, endWorkspace := buildprofile.Start(ctx, "Prepare portable tool validation workspace")
	workspace, cleanup, err := preparePortableToolValidationWorkspace(
		workspaceCtx, store, input.Image.Descriptor.Platform,
	)
	endWorkspace(err)
	if err != nil {
		return nil, err
	}
	defer func() {
		if providerHelperCleanupFailed(resultErr) {
			// The probe container may still exist, so its workspace mount
			// must be retained for abandoned-helper recovery.
			return
		}
		if cleanupErr := cleanup(); cleanupErr != nil {
			evidence, resultErr = nil, errors.Join(resultErr, cleanupErr)
		}
	}()
	scheduled, err := RunPortableToolValidationScheduleV1(
		ctx, input.Image.Descriptor, workspace, input.Schedule,
	)
	if err != nil {
		return nil, err
	}
	return PortableToolValidationEvidenceV1(input.Image.Image.RootFSSubject, scheduled)
}

// RunPortableToolValidationScheduleV1 is the production scheduling boundary
// for portable-tool validation. It carries the usage owner's preselected
// validation-profile references into the PTD-20 fixed executor, invoking it
// once per scheduled profile with that closure's contract install root and
// environment projection. The scheduler does not decide which resolution
// scopes belong to the image descriptor supplied by the caller.
//
// Scheduling decides only what runs and in what order. Executor policy stays
// executor-owned, and a probe result is an observation: a non-passing outcome
// fails the schedule rather than becoming validation evidence.
//
// The workspace must not belong to a validation session the caller still holds
// open. Each probe runs in its own container whose name is derived from the
// workspace directory, so a shared workspace collides with that held
// container.
func RunPortableToolValidationScheduleV1(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	schedule providers.PortableToolValidationScheduleV1,
) ([]PortableToolScheduledEvidenceV1, error) {
	if ctx == nil {
		return nil, fmt.Errorf("portable-tool validation schedule requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := providers.ValidatePortableToolValidationScheduleV1(schedule); err != nil {
		return nil, err
	}
	results := make([]PortableToolScheduledEvidenceV1, 0, len(schedule.Entries))
	for _, entry := range schedule.Entries {
		profile, err := toolcatalog.DecodePortableToolValidationProfileV1(entry.Profile.Reference, entry.Profile.Record)
		if err != nil {
			return nil, fmt.Errorf("schedule portable-tool validation for %s/%s: %w", entry.Scope, entry.Tool, err)
		}
		profileCtx, endProfile := buildprofile.Start(ctx, "Validate portable tool "+entry.Tool)
		observed, err := runScheduledPortableToolValidationProfile(profileCtx, descriptor, workspace, profile, entry.Runtime)
		endProfile(err)
		if err != nil {
			return nil, fmt.Errorf("run portable-tool validation profile %s: %w", entry.Profile.Reference.ID, err)
		}
		if err := requireAttributedPortableToolObservationsV1(entry, observed); err != nil {
			return nil, err
		}
		results = append(results, PortableToolScheduledEvidenceV1{
			Scope: entry.Scope, Tool: entry.Tool, Profile: entry.Profile.Reference, Observed: observed,
		})
	}
	return results, nil
}

// requireAttributedPortableToolObservationsV1 keeps observation and support
// distinct. It refuses to convert a failing, timed-out, or truncated probe
// into scheduling success; it does not decide what a passing probe advertises.
//
// Attribution is checked here rather than trusted from the executor's own
// report: the schedule asked for one exact locked profile, and evidence for a
// different profile must not be bound to that profile's identity.
func requireAttributedPortableToolObservationsV1(
	entry providers.PortableToolScheduledValidationV1,
	observed PortableToolProbeEvidenceV1,
) error {
	if observed.Profile.ID != entry.Profile.Reference.ID ||
		observed.Profile.Digest != entry.Profile.Reference.Digest {
		return fmt.Errorf(
			"portable-tool validation evidence is attributed to profile %s, but %s was scheduled",
			observed.Profile.ID, entry.Profile.Reference.ID,
		)
	}
	if len(observed.Results) != len(observed.ProfileDefinition.Probes) {
		return fmt.Errorf(
			"portable-tool validation profile %s observed %d of %d declared probes",
			entry.Profile.Reference.ID, len(observed.Results), len(observed.ProfileDefinition.Probes),
		)
	}
	for _, result := range observed.Results {
		if result.Outcome != PortableToolProbeOutcomePassV1 {
			return fmt.Errorf(
				"portable-tool validation probe %s in profile %s reported %s",
				result.Probe.Path, entry.Profile.Reference.ID, result.Outcome,
			)
		}
	}
	return nil
}

// PortableToolValidationEvidenceV1 converts scheduled observations into the
// generic provider validation evidence recorded for the validated image. The
// profile digest is the scheduled reference, so evidence stays bound to the
// exact locked profile and remains outside selected-closure identity.
// Evidence identity is the pair (rootfs subject, profile digest), so two
// closures that select the same profile contribute one record. Both probe runs
// still happen, because their contract runtime projections may differ; only
// the resulting evidence collapses.
func PortableToolValidationEvidenceV1(
	subjectRootFS canonical.Digest,
	scheduled []PortableToolScheduledEvidenceV1,
) ([]providers.ValidationEvidence, error) {
	evidence := make([]providers.ValidationEvidence, 0, len(scheduled))
	seen := make(map[canonical.Digest]struct{}, len(scheduled))
	for _, entry := range scheduled {
		if _, duplicate := seen[entry.Profile.Digest]; duplicate {
			continue
		}
		value, err := providers.NewValidationEvidence(subjectRootFS, entry.Profile.Digest)
		if err != nil {
			return nil, fmt.Errorf("portable-tool validation evidence for %s: %w", entry.Profile.ID, err)
		}
		seen[entry.Profile.Digest] = struct{}{}
		evidence = append(evidence, value)
	}
	return evidence, nil
}
