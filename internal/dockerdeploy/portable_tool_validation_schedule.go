package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// scheduledPortableToolProfile is constructed before any probe runs. It keeps
// one decoded profile and an owned copy of its selected runtime projection.
type scheduledPortableToolProfile struct {
	entry   providers.PortableToolScheduledValidationV1
	profile toolcatalog.ValidationProfileRecordV1
}

var runScheduledPortableToolValidationProfile = RunPortableToolValidationProfile
var preparePortableToolValidationWorkspace = PrepareProbeWorkspace

// PortableToolMaterializationValidationInputV1 is an in-process,
// construction-controlled handoff from a validated lock to one concrete image.
// It does not infer image placement from the tool or runtime metadata.
type PortableToolMaterializationValidationInputV1 struct {
	Image    InspectedImageCandidate
	selected []scheduledPortableToolProfile
}

func PortableToolMaterializationValidationInputFromLockV1(
	image InspectedImageCandidate,
	lock providers.PortableToolLockV1,
) (PortableToolMaterializationValidationInputV1, error) {
	schedule, err := providers.PortableToolValidationScheduleFromLockV1(lock)
	if err != nil {
		return PortableToolMaterializationValidationInputV1{}, err
	}
	selected, err := constructScheduledPortableToolProfiles(schedule)
	if err != nil {
		return PortableToolMaterializationValidationInputV1{}, err
	}
	return PortableToolMaterializationValidationInputV1{Image: image, selected: selected}, nil
}

// PortableToolBuildCaseValidationInputFromLockV1 binds one catalog-derived
// build case to the exact resolution scope in the materialized lock. The
// caller owns the scope: neither tool identity nor runtime metadata chooses
// which image or lock entries belong to the case.
func PortableToolBuildCaseValidationInputFromLockV1(
	image InspectedImageCandidate,
	lock providers.PortableToolLockV1,
	closures []toolcatalog.SelectedClosureV1,
	caseV1 toolcatalog.IntegrationCaseV1,
	scope string,
) (PortableToolMaterializationValidationInputV1, error) {
	if err := requireCatalogDerivedBuildCaseV1(caseV1); err != nil {
		return PortableToolMaterializationValidationInputV1{}, err
	}
	if caseV1.Support.Context != "build" || caseV1.Fixture.Context != "build" ||
		caseV1.Manifest.Tool == "" || caseV1.ManifestReference.Digest == "" {
		return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case requires a derived build-context case")
	}
	if err := caseV1.ID.Validate(); err != nil {
		return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case identity: %w", err)
	}
	schedule, err := providers.PortableToolValidationScheduleFromLockV1(lock)
	if err != nil {
		return PortableToolMaterializationValidationInputV1{}, err
	}
	scoped, err := providers.PortableToolValidationScheduleForScopeV1(schedule, scope)
	if err != nil {
		return PortableToolMaterializationValidationInputV1{}, err
	}
	if len(scoped.Entries) == 0 || len(scoped.Entries) != len(caseV1.Fixture.ValidationProfiles) ||
		len(caseV1.Profiles) != len(caseV1.Fixture.ValidationProfiles) {
		return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case scope %q has no exact selected profile set", scope)
	}
	var locked *providers.PortableToolPlanEntryV1
	for _, entry := range lock.Plan.PortableToolPlan.Tools {
		if entry.Scope == scope && entry.Provenance.Tool == caseV1.Manifest.Tool &&
			entry.Provenance.ManifestDigest == caseV1.ManifestReference.Digest {
			if locked != nil {
				return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case scope %q selects duplicate releases", scope)
			}
			copy := entry
			locked = &copy
		}
	}
	if locked == nil {
		return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case scope %q selects a different release", scope)
	}
	matchedClosure := false
	for _, closure := range closures {
		if closure.Scope != scope || closure.Provenance.Tool != caseV1.Manifest.Tool {
			continue
		}
		if matchedClosure {
			return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case scope %q has duplicate selected closures", scope)
		}
		matchedClosure = true
		if closure.Identity != locked.SelectedClosureDigest ||
			closure.Provenance.ManifestDigest != caseV1.ManifestReference.Digest ||
			closure.Target.Identity != caseV1.Target.Target ||
			closure.Contract.Context != caseV1.Support.Context ||
			!slices.Equal(closure.Contract.Bindings, caseV1.Support.Bindings) ||
			!sameBuildCaseSelectionsV1(closure.Contract.Selections, caseV1.Support.Selections) ||
			!reflect.DeepEqual(closure.Fixture, caseV1.Fixture) {
			return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case scope %q selects a different support tuple or closure", scope)
		}
	}
	if !matchedClosure {
		return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case scope %q has no selected closure", scope)
	}
	for index, entry := range scoped.Entries {
		if entry.Tool != caseV1.Manifest.Tool || entry.Profile.Reference != caseV1.Fixture.ValidationProfiles[index] ||
			caseV1.Profiles[index].ID != entry.Profile.Reference.ID {
			return PortableToolMaterializationValidationInputV1{}, fmt.Errorf("portable-tool build case scope %q selects a different validation profile", scope)
		}
	}
	selected, err := constructScheduledPortableToolProfiles(scoped)
	if err != nil {
		return PortableToolMaterializationValidationInputV1{}, err
	}
	return PortableToolMaterializationValidationInputV1{Image: image, selected: selected}, nil
}

func requireCatalogDerivedBuildCaseV1(caseV1 toolcatalog.IntegrationCaseV1) error {
	cases, err := toolcatalog.EmbeddedIntegrationCasesV1()
	if err != nil {
		return fmt.Errorf("derive portable-tool build cases: %w", err)
	}
	for _, derived := range cases {
		if derived.ID == caseV1.ID {
			if reflect.DeepEqual(derived, caseV1) {
				return nil
			}
			return fmt.Errorf("portable-tool build case differs from its catalog-derived case")
		}
	}
	return fmt.Errorf("portable-tool build case is not catalog-derived")
}

func sameBuildCaseSelectionsV1(left, right map[string][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for dimension, values := range left {
		other, ok := right[dimension]
		if !ok || !slices.Equal(values, other) {
			return false
		}
	}
	return true
}

// requirePortableToolValidationEvidenceForInputV1 refuses a successful
// handoff unless the callback returned exactly the evidence implied by its
// locked schedule and inspected image. Identical selected profiles coalesce.
func requirePortableToolValidationEvidenceForInputV1(
	input PortableToolMaterializationValidationInputV1,
	evidence []providers.ValidationEvidence,
) error {
	subject, err := deploy.RootFSSubject(input.Image.Descriptor.RootFSDiffIDs)
	if err != nil {
		return err
	}
	expected := make(map[providers.ValidationEvidence]struct{}, len(input.selected))
	for _, selected := range input.selected {
		item, err := providers.NewPortableToolValidationEvidence(subject, selected.entry.Profile.Reference, selected.entry.Runtime)
		if err != nil {
			return err
		}
		expected[item] = struct{}{}
	}
	if len(evidence) != len(expected) {
		return fmt.Errorf("portable-tool validation callback returned %d observations, want %d", len(evidence), len(expected))
	}
	for _, item := range evidence {
		if _, ok := expected[item]; !ok {
			return fmt.Errorf("portable-tool validation callback returned an observation for a different image or profile")
		}
		delete(expected, item)
	}
	return nil
}

// ValidatePortableToolMaterializationV1 is the production acceptance boundary
// for a materialized portable-tool closure. The usage owner supplies both the
// exact image produced by materialization and the lock-derived selected view.
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
	if input.selected == nil {
		return nil, fmt.Errorf("portable-tool materialization validation requires a lock-derived schedule")
	}
	if len(input.selected) == 0 {
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
	return runScheduledPortableToolProfiles(ctx, input.Image.Descriptor, workspace, input.selected)
}

// RunPortableToolValidationScheduleV1 is the direct scheduling entry point for
// an already-selected schedule. Production materialization uses the lock-only
// constructor above, then executes its construction-controlled profile view.
// Neither path infers image placement from tool or runtime metadata.
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
) ([]providers.ValidationEvidence, error) {
	if ctx == nil {
		return nil, fmt.Errorf("portable-tool validation schedule requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selected, err := constructScheduledPortableToolProfiles(schedule)
	if err != nil {
		return nil, err
	}
	return runScheduledPortableToolProfiles(ctx, descriptor, workspace, selected)
}

func runScheduledPortableToolProfiles(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	workspace PreparedProbeWorkspace,
	selected []scheduledPortableToolProfile,
) ([]providers.ValidationEvidence, error) {
	subject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		return nil, err
	}
	evidence := make([]providers.ValidationEvidence, 0, len(selected))
	seen := make(map[providers.ValidationEvidence]struct{}, len(selected))
	for _, selectedProfile := range selected {
		entry := selectedProfile.entry
		profileCtx, endProfile := buildprofile.Start(ctx, "Validate portable tool "+entry.Tool)
		observed, err := runScheduledPortableToolValidationProfile(profileCtx, descriptor, workspace, selectedProfile.profile, entry.Runtime)
		endProfile(err)
		if err != nil {
			return nil, fmt.Errorf("run portable-tool validation profile %s: %w", entry.Profile.Reference.ID, err)
		}
		if err := requireAttributedPortableToolObservationsV1(entry, descriptor, observed); err != nil {
			return nil, err
		}
		value, err := providers.NewPortableToolValidationEvidence(subject, entry.Profile.Reference, entry.Runtime)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[value]; !duplicate {
			seen[value] = struct{}{}
			evidence = append(evidence, value)
		}
	}
	sort.Slice(evidence, func(left, right int) bool {
		if evidence[left].ProfileDigest != evidence[right].ProfileDigest {
			return evidence[left].ProfileDigest < evidence[right].ProfileDigest
		}
		if evidence[left].PortableToolProfileID != evidence[right].PortableToolProfileID {
			return evidence[left].PortableToolProfileID < evidence[right].PortableToolProfileID
		}
		return evidence[left].PortableToolRuntimeDigest < evidence[right].PortableToolRuntimeDigest
	})
	return evidence, nil
}

func constructScheduledPortableToolProfiles(
	schedule providers.PortableToolValidationScheduleV1,
) ([]scheduledPortableToolProfile, error) {
	if err := providers.ValidatePortableToolValidationScheduleV1(schedule); err != nil {
		return nil, err
	}
	selected := make([]scheduledPortableToolProfile, 0, len(schedule.Entries))
	for _, original := range schedule.Entries {
		profile, err := toolcatalog.DecodePortableToolValidationProfileV1(original.Profile.Reference, original.Profile.Record)
		if err != nil {
			return nil, fmt.Errorf("schedule portable-tool validation for %s/%s: %w", original.Scope, original.Tool, err)
		}
		entry := original
		if original.Runtime != nil {
			runtime := *original.Runtime
			runtime.Environment = append([]providers.PortableToolEnvironmentVariableV1{}, original.Runtime.Environment...)
			entry.Runtime = &runtime
		}
		selected = append(selected, scheduledPortableToolProfile{entry: entry, profile: profile})
	}
	return selected, nil
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
	descriptor deploy.ImageDescriptor,
	observed PortableToolProbeEvidenceV1,
) error {
	if observed.Profile.ID != entry.Profile.Reference.ID ||
		observed.Profile.Digest != entry.Profile.Reference.Digest {
		return fmt.Errorf(
			"portable-tool validation evidence is attributed to profile %s, but %s was scheduled",
			observed.Profile.ID, entry.Profile.Reference.ID,
		)
	}
	if _, err := PortableToolProbeEvidenceDigestV1(observed); err != nil {
		return fmt.Errorf("portable-tool validation profile %s returned invalid evidence: %w", entry.Profile.Reference.ID, err)
	}
	subject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		return err
	}
	if observed.SubjectRootFS != subject || !reflect.DeepEqual(observed.Platform, descriptor.Platform) {
		return fmt.Errorf("portable-tool validation profile %s observed a different image subject", entry.Profile.Reference.ID)
	}
	policy, _, err := portableToolProbePolicyV1(entry.Runtime)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(observed.Policy, policy) {
		return fmt.Errorf("portable-tool validation profile %s observed a different runtime projection", entry.Profile.Reference.ID)
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
