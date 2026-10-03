package dockerdeploy

import (
	"context"
	"fmt"
	"reflect"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func requireNoPendingValidatedBuildV1(operation *deploy.OperationLock) error {
	if _, found, err := operation.ReadPendingValidatedBuildV1(); err != nil {
		return err
	} else if found {
		return fmt.Errorf("pending validated publication requires recovery; ownership was preserved")
	}
	return nil
}

func requireValidatedPruningBoundaryV1(operation *deploy.OperationLock) error {
	if err := requireNoPendingValidatedBuildV1(operation); err != nil {
		return err
	}
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found {
		return err
	}
	if len(record.PendingCleanup) != 0 {
		return fmt.Errorf("validated cleanup inventory requires retirement before pruning; ownership was preserved")
	}
	return nil
}

func requireCurrentPublicationValidatedSeparationV1(operation *deploy.OperationLock, pending deploy.PendingBuildV1, environment, dir string) error {
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || record.Discarded {
		return err
	}
	validated, err := validatedRecordReferencesV1(record, environment, dir)
	if err != nil {
		return err
	}
	references := []string{pending.Candidate.TemporaryReference, pending.Candidate.GenerationReference}
	if pending.Candidate.Companion != nil {
		references = append(references, pending.Candidate.Companion.Reference)
	}
	if pending.Old != nil {
		references = append(references, pending.Old.Reference)
	}
	for _, pair := range pending.OldReferences {
		references = append(references, pair.Reference)
	}
	for _, item := range pending.Cleanup {
		if item.Kind == deploy.CleanupKindTemporaryImageReference || item.Kind == deploy.CleanupKindGenerationReference {
			references = append(references, item.Identity)
		}
	}
	for _, reference := range references {
		for _, pair := range validated {
			if reference == pair.ImageReference {
				return fmt.Errorf("current publication overlaps independently validated ownership; ownership was preserved")
			}
		}
	}
	return nil
}

func requireValidatedConsumerBoundaryV1(operation *deploy.OperationLock, boundary string) error {
	if err := requireNoPendingValidatedBuildV1(operation); err != nil {
		return err
	}
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found {
		return err
	}
	// This shared boundary inspects lock identity and ownership shape without
	// interpreting provider-owned profile schemas. The cutover validator keeps
	// older retained profiles readable while still checking the canonical lock
	// and its content digest.
	lock, lockFound, err := operation.ReadBuildLock(record.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		return fmt.Errorf("validate retained build lock before %s; ownership was preserved: %w", boundary, err)
	}
	if !lockFound {
		// A discarded ordinary record can outlive its lock when storage cleanup
		// is retried. A live record needs the lock to establish its owner shape.
		if record.Discarded && record.Owner == nil && record.Companion == nil {
			return requireValidatedRecordBoundaryV1(record, boundary)
		}
		return fmt.Errorf("validated build lock %s is missing before %s; ownership was preserved", record.BuildLockDigest, boundary)
	}
	recordPortable := record.Owner != nil && record.Companion != nil
	lockPortable := lock.PortableRuntimeLayer != nil
	if recordPortable != lockPortable {
		return fmt.Errorf("validated record and retained build lock disagree about portable ownership; ownership was preserved")
	}
	if lockPortable {
		return fmt.Errorf("portable validated ownership requires %s; owner was preserved", boundary)
	}
	return requireValidatedRecordBoundaryV1(record, boundary)
}

func requireValidatedRecordBoundaryV1(record deploy.ValidatedBuildV1, boundary string) error {
	if record.Companion != nil {
		return fmt.Errorf("portable validated ownership requires %s; owner was preserved", boundary)
	}
	for _, pending := range record.PendingCleanup {
		if pending.CompanionOwner != nil {
			return fmt.Errorf("portable validated cleanup requires %s; inventory was preserved", boundary)
		}
	}
	return nil
}

func validatedRecordReferencesV1(record deploy.ValidatedBuildV1, environment, dir string) ([]deploy.ValidatedBuildReferenceV1, error) {
	if err := deploy.ValidateValidatedBuildV1(record); err != nil {
		return nil, err
	}
	if err := ValidateEnvironmentGenerationReference(record.ImageReference, environment, dir); err != nil {
		return nil, err
	}
	pairs := []deploy.ValidatedBuildReferenceV1{{Image: record.Image, ImageReference: record.ImageReference}}
	if record.Companion != nil {
		if err := validatePortableOwnedReferenceV1(*record.Companion, *record.Owner, environment, dir); err != nil {
			return nil, err
		}
		pairs = append(pairs, deploy.ValidatedBuildReferenceV1{Image: record.Companion.Image, ImageReference: record.Companion.Reference, CompanionOwner: record.Owner})
	}
	for _, pair := range record.PendingCleanup {
		if pair.CompanionOwner == nil {
			if err := ValidateEnvironmentGenerationReference(pair.ImageReference, environment, dir); err != nil {
				return nil, err
			}
		} else if err := validatePortableOwnedReferenceV1(OwnedImageReferenceV1{Image: pair.Image, Reference: pair.ImageReference}, *pair.CompanionOwner, environment, dir); err != nil {
			return nil, err
		}
	}
	return pairs, nil
}

func validatedPublicationOwnerV1(record deploy.ValidatedBuildV1, lock deploy.BuildLockV1) (deploy.EnvironmentGenerationState, error) {
	policy, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		return deploy.EnvironmentGenerationState{}, err
	}
	return deploy.EnvironmentGenerationState{Reference: record.ImageReference, ImageDigest: record.Image.Digest, RootFSSubject: record.Image.RootFSSubject, BuildLockDigest: record.BuildLockDigest, Platform: record.Platform, RuntimePolicyDigest: policy}, nil
}

func validateValidatedRecordLockV1(operation *deploy.OperationLock, store providerstore.Store, record deploy.ValidatedBuildV1, environment, dir string) (deploy.BuildLockV1, error) {
	lock, found, err := operation.ReadBuildLock(record.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		return lock, err
	}
	if !found {
		return lock, fmt.Errorf("pending validated build lock %s is missing", record.BuildLockDigest)
	}
	owner, err := validatedPublicationOwnerV1(record, lock)
	if err != nil {
		return lock, err
	}
	pairs, err := ProjectEnvironmentOwnedReferencesV1(owner, lock, environment, dir)
	if err != nil {
		return lock, err
	}
	if lock.BlueprintDigest != record.BlueprintDigest || lock.FinalImage != record.Image || (len(pairs) == 2) != (record.Companion != nil) {
		return lock, fmt.Errorf("pending validated record does not match its complete lock")
	}
	overlay, err := deploy.RequestOverlayDigestV1(lock.Overlay)
	if err != nil {
		return lock, err
	}
	if overlay != record.OverlayDigest {
		return lock, fmt.Errorf("pending validated overlay does not match its lock")
	}
	if len(pairs) == 2 && (*record.Owner != owner || *record.Companion != pairs[1]) {
		return lock, fmt.Errorf("pending validated companion does not match its lock")
	}
	if _, err := deploy.BuildLockStoreClosure(lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
		return lock, err
	}
	return lock, nil
}

func pendingValidatedCleanupV1(previous *deploy.ValidatedBuildV1, candidate deploy.ValidatedBuildV1, environment, dir string) ([]deploy.ValidatedBuildReferenceV1, error) {
	if previous == nil {
		return nil, nil
	}
	pairs, err := validatedRecordReferencesV1(*previous, environment, dir)
	if err != nil {
		return nil, err
	}
	if previous.Discarded {
		// Discard closes reuse before retirement completes. Carry its exact
		// remaining inventory without resurrecting successfully removed aliases.
		if len(previous.PendingCleanup) == 0 {
			return nil, nil
		}
		pairs = nil
	}
	return mergeValidatedBuildReferences(deploy.ValidatedBuildReferenceV1{Image: candidate.Image, ImageReference: candidate.ImageReference}, previous.PendingCleanup, pairs)
}

func requireDistinctValidatedCandidateV1(operation *deploy.OperationLock, candidate deploy.ValidatedBuildV1, environment, dir string) error {
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Current == nil {
		return err
	}
	lock, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("current build lock is missing before validated publication")
	}
	pairs, err := ProjectEnvironmentOwnedReferencesV1(*state.Current, lock, environment, dir)
	if err != nil {
		return err
	}
	for _, pair := range pairs {
		if pair.Reference == candidate.ImageReference || (candidate.Companion != nil && pair.Reference == candidate.Companion.Reference) {
			return fmt.Errorf("validated candidate conflicts with independently current ownership")
		}
	}
	return nil
}

// RecoverPendingValidatedPublicationV1 uses only the persisted lock/record and
// exact Docker reference primitives. It never rebuilds or acquires provider data.
func RecoverPendingValidatedPublicationV1(ctx context.Context, operation *deploy.OperationLock, store providerstore.Store, environment, dir string) (bool, error) {
	return recoverPendingValidatedPublicationV1(ctx, operation, store, environment, dir, newValidatedPublicationBackendV1())
}

func recoverPendingValidatedPublicationV1(ctx context.Context, operation *deploy.OperationLock, store providerstore.Store, environment, dir string, backend publishValidatedBuildBackendV1) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("pending validated recovery requires a context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validatePublicationDeployment(operation, store, dir); err != nil {
		return false, err
	}
	intent, found, err := operation.ReadPendingValidatedBuildV1()
	if err != nil || !found {
		return false, err
	}
	if _, pending, err := operation.ReadPendingBuild(); err != nil {
		return false, err
	} else if pending {
		return false, fmt.Errorf("current and validated publication intents conflict; ownership was preserved")
	}
	if _, err := validatedRecordReferencesV1(intent.Candidate, environment, dir); err != nil {
		return false, err
	}
	cleanup, err := pendingValidatedCleanupV1(intent.Previous, intent.Candidate, environment, dir)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(cleanup, intent.Candidate.PendingCleanup) {
		return false, fmt.Errorf("pending validated cleanup differs from previous ownership")
	}
	if _, err := validateValidatedRecordLockV1(operation, store, intent.Candidate, environment, dir); err != nil {
		return false, err
	}
	if err := requireDistinctValidatedCandidateV1(operation, intent.Candidate, environment, dir); err != nil {
		return false, err
	}
	selected, found, err := operation.ReadValidatedBuildV1()
	if err != nil {
		return false, err
	}
	committed := found && reflect.DeepEqual(selected, intent.Candidate)
	if !committed && !sameValidatedRecordV1(selected, found, intent.Previous) {
		return false, fmt.Errorf("pending validated state conflict; ownership was preserved")
	}
	if !committed && intent.Previous != nil && !intent.Previous.Discarded {
		if _, err := validateValidatedRecordLockV1(operation, store, *intent.Previous, environment, dir); err != nil {
			return false, err
		}
	}
	if backend.removeReference == nil || backend.verifyReference == nil || (intent.Candidate.Companion != nil && (backend.removeCompanion == nil || backend.verifyCompanion == nil)) {
		return false, fmt.Errorf("pending validated recovery requires complete reference operations")
	}
	observed, present, err := operation.ReadPendingValidatedBuildV1()
	if err != nil {
		return false, err
	}
	if !present || !reflect.DeepEqual(observed, intent) {
		return false, fmt.Errorf("pending validated intent changed before recovery")
	}
	current, present, err := operation.ReadValidatedBuildV1()
	if err != nil {
		return false, err
	}
	if present != found || !reflect.DeepEqual(current, selected) {
		return false, fmt.Errorf("validated record changed before recovery")
	}
	if committed {
		if err := backend.verifyReference(ctx, selected.Image, selected.ImageReference, environment, dir); err != nil {
			return false, err
		}
		if selected.Companion != nil {
			if err := backend.verifyCompanion(ctx, operation, *selected.Companion, *selected.Owner, environment, dir); err != nil {
				return false, err
			}
		}
	} else {
		if err := backend.removeReference(ctx, intent.Candidate.Image, intent.Candidate.ImageReference, environment, dir); err != nil {
			return false, err
		}
		if intent.Candidate.Companion != nil {
			if err := backend.removeCompanion(ctx, operation, *intent.Candidate.Companion, *intent.Candidate.Owner, environment, dir); err != nil {
				return false, err
			}
		}
	}
	removeIntent := backend.removeIntent
	if removeIntent == nil {
		removeIntent = operation.RemovePendingValidatedBuildV1
	}
	return true, removeIntent()
}

func requireExactValidatedIntentV1(operation *deploy.OperationLock, intent deploy.PendingValidatedBuildV1) error {
	observed, found, err := operation.ReadPendingValidatedBuildV1()
	if err != nil {
		return err
	}
	if !found || !reflect.DeepEqual(observed, intent) {
		return fmt.Errorf("validated publication intent changed; ownership was preserved")
	}
	return nil
}

// Existing retirement consumers cannot remove a reference still selected by
// independently current ownership, even when its image bytes are identical.
func requireValidatedReferencesNotCurrentV1(operation *deploy.OperationLock, references []deploy.ValidatedBuildReferenceV1, environment, dir string) error {
	state, found, err := operation.ReadStateV1()
	if err != nil || !found || state.Current == nil || len(references) == 0 {
		return err
	}
	lock, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("current build lock is missing before validated cleanup")
	}
	pairs, err := ProjectEnvironmentOwnedReferencesV1(*state.Current, lock, environment, dir)
	if err != nil {
		return err
	}
	for _, reference := range references {
		for _, pair := range pairs {
			if reference.ImageReference == pair.Reference {
				return fmt.Errorf("validated cleanup overlaps current ownership; inventory was preserved")
			}
		}
	}
	return nil
}

func sameValidatedRecordV1(record deploy.ValidatedBuildV1, found bool, expected *deploy.ValidatedBuildV1) bool {
	return (!found && expected == nil) || (found && expected != nil && reflect.DeepEqual(record, *expected))
}
