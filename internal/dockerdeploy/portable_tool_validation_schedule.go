package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
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

// PortableToolFinalImageScheduleFromBuildLockV1 projects the locked profiles
// that belong to the shipped final image: those whose selected closure
// declares a contract runtime projection.
//
// A build-context tool declares no runtime projection because it is
// materialized into the isolated source-builder image, not the final image.
// Probing it here would execute a path the shipped image does not contain, and
// silently skipping it would let a build succeed having validated nothing, so
// a locked build-context profile is refused outright. The source-builder
// validation path that executes them is not part of this slice.
//
// A build lock with no portable tools yields the zero value, which schedules
// nothing. Every final-image validation entry point uses this, so a locked
// build is revalidated against exactly the profiles it locked for that image.
func PortableToolFinalImageScheduleFromBuildLockV1(
	lock *providers.PortableToolLockV1,
) (providers.PortableToolValidationScheduleV1, error) {
	if lock == nil {
		return providers.PortableToolValidationScheduleV1{}, nil
	}
	complete, err := providers.PortableToolValidationScheduleFromLockV1(*lock)
	if err != nil {
		return providers.PortableToolValidationScheduleV1{}, fmt.Errorf(
			"portable tool validation schedule from build lock: %w", err,
		)
	}
	schedule := providers.PortableToolValidationScheduleV1{
		Schema:  complete.Schema,
		Entries: []providers.PortableToolScheduledValidationV1{},
	}
	for _, entry := range complete.Entries {
		if entry.Runtime == nil {
			// Dropping these would let a build succeed having run none of its
			// locked probes, so refuse until a source-builder validation path
			// exists to execute them against the image that materializes them.
			return providers.PortableToolValidationScheduleV1{}, fmt.Errorf(
				"portable tool %s/%s locks a build-context validation profile that requires source-builder validation",
				entry.Scope, entry.Tool,
			)
		}
		schedule.Entries = append(schedule.Entries, entry)
	}
	if len(schedule.Entries) == 0 {
		return providers.PortableToolValidationScheduleV1{}, nil
	}
	return schedule, nil
}

var runScheduledPortableToolValidationProfile = RunPortableToolValidationProfile

// RunPortableToolValidationScheduleV1 is the production scheduling boundary
// for portable-tool validation. It carries every selected validation-profile
// reference from the compiled plan into the PTD-20 fixed executor, invoking it
// once per scheduled profile with that closure's contract install root and
// environment projection.
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
