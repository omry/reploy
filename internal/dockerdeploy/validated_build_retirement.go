package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

type validatedRetirementBackendV1 struct {
	removeReference func(context.Context, providers.RealizedImageV1, string, string, string) error
	removeCompanion func(context.Context, *deploy.OperationLock, OwnedImageReferenceV1, deploy.EnvironmentGenerationState, string, string) error
	commitRecord    func(deploy.ValidatedBuildV1) error
}

func validatedRetirementBackend(remove func(context.Context, providers.RealizedImageV1, string, string, string) error) validatedRetirementBackendV1 {
	return validatedRetirementBackendV1{removeReference: remove, removeCompanion: RemovePortableEnvironmentReferenceV1}
}

// Retirement needs canonical ownership, but must work when cache objects have
// disappeared. A completed discard retains its exact record after lock pruning.
func validateValidatedRetirementV1(operation *deploy.OperationLock, record deploy.ValidatedBuildV1, environment, dir string) ([]deploy.ValidatedBuildReferenceV1, error) {
	if err := requireNoPendingValidatedBuildV1(operation); err != nil {
		return nil, err
	}
	current, found, err := operation.ReadValidatedBuildV1()
	if err != nil {
		return nil, err
	}
	if !found || !reflect.DeepEqual(current, record) {
		return nil, fmt.Errorf("validated retirement record changed; ownership was preserved")
	}
	pairs, err := validatedRecordReferencesV1(record, environment, dir)
	if err != nil {
		return nil, err
	}
	if record.Discarded {
		if err := requireValidatedRetirementSeparationV1(operation, record.PendingCleanup, environment, dir); err != nil {
			return nil, err
		}
		return nil, nil
	}
	lock, found, err := operation.ReadBuildLock(record.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("validated build lock %s is missing before retirement; ownership was preserved", record.BuildLockDigest)
	}
	if (record.Companion != nil) != (lock.PortableRuntimeLayer != nil) {
		return nil, fmt.Errorf("validated record and retained build lock disagree about portable ownership; ownership was preserved")
	}
	owner, err := validatedPublicationOwnerV1(record, lock)
	if err != nil {
		return nil, err
	}
	projected, err := projectEnvironmentOwnedReferencesV1(owner, lock, environment, dir, acceptProviderProfileOwnerForCutoverV1)
	if err != nil {
		return nil, err
	}
	if lock.FinalImage != record.Image || lock.BlueprintDigest != record.BlueprintDigest {
		return nil, fmt.Errorf("validated retirement record differs from its canonical lock")
	}
	if len(projected) == 2 && (*record.Owner != owner || *record.Companion != projected[1]) {
		return nil, fmt.Errorf("validated retirement companion differs from its canonical lock")
	}
	all := append(append([]deploy.ValidatedBuildReferenceV1{}, pairs...), record.PendingCleanup...)
	if err := requireValidatedRetirementSeparationV1(operation, all, environment, dir); err != nil {
		return nil, err
	}
	return pairs, nil
}

// The current owner and an interrupted promotion own independent aliases even
// when their images are byte-identical to a validated candidate.
func requireValidatedRetirementSeparationV1(operation *deploy.OperationLock, retiring []deploy.ValidatedBuildReferenceV1, environment, dir string) error {
	protected := map[string]bool{}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	if found && state.Current != nil {
		lock, found, err := operation.ReadBuildLock(state.Current.BuildLockDigest, acceptProviderProfileOwnerForCutoverV1)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("current build lock is missing before validated retirement")
		}
		pairs, err := projectEnvironmentOwnedReferencesV1(*state.Current, lock, environment, dir, acceptProviderProfileOwnerForCutoverV1)
		if err != nil {
			return err
		}
		for _, pair := range pairs {
			protected[pair.Reference] = true
		}
	}
	pending, found, err := operation.ReadPendingBuild()
	if err != nil {
		return err
	}
	if found {
		if err := validatePendingOwnedReferencesV1(pending, environment, dir); err != nil {
			return err
		}
		protected[pending.Candidate.TemporaryReference] = true
		protected[pending.Candidate.GenerationReference] = true
		if pending.Candidate.Companion != nil {
			protected[pending.Candidate.Companion.Reference] = true
		}
		if pending.Old != nil {
			protected[pending.Old.Reference] = true
		}
		for _, pair := range pending.OldReferences {
			protected[pair.Reference] = true
		}
		for _, item := range pending.Cleanup {
			if item.Kind == deploy.CleanupKindTemporaryImageReference || item.Kind == deploy.CleanupKindGenerationReference {
				protected[item.Identity] = true
			}
		}
	}
	for _, pair := range retiring {
		if protected[pair.ImageReference] {
			return fmt.Errorf("validated retirement overlaps current publication ownership; inventory was preserved")
		}
	}
	return nil
}

func removeValidatedRetirementReferenceV1(ctx context.Context, operation *deploy.OperationLock, pair deploy.ValidatedBuildReferenceV1, environment, dir string, backend validatedRetirementBackendV1) error {
	if pair.CompanionOwner != nil {
		if backend.removeCompanion == nil {
			return fmt.Errorf("validated companion retirement requires a complete backend")
		}
		return backend.removeCompanion(ctx, operation, OwnedImageReferenceV1{Reference: pair.ImageReference, Image: pair.Image}, *pair.CompanionOwner, environment, dir)
	}
	if backend.removeReference == nil {
		return fmt.Errorf("validated retirement requires a complete backend")
	}
	return backend.removeReference(ctx, pair.Image, pair.ImageReference, environment, dir)
}

func cleanupPendingValidatedReferencesV1(ctx context.Context, operation *deploy.OperationLock, record deploy.ValidatedBuildV1, environment, dir string, backend validatedRetirementBackendV1) (deploy.ValidatedBuildV1, []error) {
	if err := operation.RequireWritable(); err != nil {
		return record, []error{err}
	}
	if ctx == nil {
		return record, []error{fmt.Errorf("validated retirement requires a context")}
	}
	if _, err := validateValidatedRetirementV1(operation, record, environment, dir); err != nil {
		return record, []error{err}
	}
	if len(record.PendingCleanup) == 0 {
		return record, nil
	}
	for _, pair := range record.PendingCleanup {
		if pair.CompanionOwner != nil && backend.removeCompanion == nil || pair.CompanionOwner == nil && backend.removeReference == nil {
			return record, []error{fmt.Errorf("validated retirement requires a complete backend")}
		}
	}
	var remaining []deploy.ValidatedBuildReferenceV1
	cleanupErrors := []error{}
	for _, pair := range record.PendingCleanup {
		if err := removeValidatedRetirementReferenceV1(ctx, operation, pair, environment, dir, backend); err != nil {
			remaining = append(remaining, pair)
			cleanupErrors = append(cleanupErrors, fmt.Errorf("retire superseded validated reference %q: %w", pair.ImageReference, err))
		}
	}
	if reflect.DeepEqual(remaining, record.PendingCleanup) {
		return record, cleanupErrors
	}
	updated := record
	updated.PendingCleanup = remaining
	current, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(current, record) {
		cleanupErrors = append(cleanupErrors, errors.Join(fmt.Errorf("validated record changed during retirement; inventory was preserved"), err))
		return record, cleanupErrors
	}
	commit := backend.commitRecord
	if commit == nil {
		commit = operation.CommitValidatedBuildV1
	}
	if err := commit(updated); err != nil {
		return record, append(cleanupErrors, fmt.Errorf("record validated retirement progress: %w", err))
	}
	current, found, err = operation.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(current, updated) {
		return record, append(cleanupErrors, errors.Join(fmt.Errorf("validated retirement progress was not durably recorded"), err))
	}
	return current, cleanupErrors
}

func discardValidatedBuildWithBackendV1(ctx context.Context, operation *deploy.OperationLock, environment, dir string, backend validatedRetirementBackendV1) (deploy.ValidatedBuildV1, bool, error) {
	if err := operation.RequireWritable(); err != nil {
		return deploy.ValidatedBuildV1{}, false, err
	}
	if ctx == nil {
		return deploy.ValidatedBuildV1{}, false, fmt.Errorf("discard validated build requires a context")
	}
	if operation == nil || backend.removeReference == nil {
		return deploy.ValidatedBuildV1{}, false, fmt.Errorf("discard validated build requires a complete backend")
	}
	if err := requireNoPendingValidatedBuildV1(operation); err != nil {
		return deploy.ValidatedBuildV1{}, false, err
	}
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found {
		return record, found, err
	}
	pairs, err := validateValidatedRetirementV1(operation, record, environment, dir)
	if err != nil {
		return record, true, err
	}
	inventory := append(append([]deploy.ValidatedBuildReferenceV1{}, record.PendingCleanup...), pairs...)
	for _, pair := range inventory {
		if pair.CompanionOwner != nil && backend.removeCompanion == nil {
			return record, true, fmt.Errorf("validated companion retirement requires a complete backend")
		}
	}
	if !record.Discarded {
		sort.Slice(inventory, func(left, right int) bool {
			return inventory[left].ImageReference < inventory[right].ImageReference
		})
		updated := record
		updated.PendingCleanup = inventory
		updated.PendingStorageCleanup = true
		updated.Discarded = true
		commit := backend.commitRecord
		if commit == nil {
			commit = operation.CommitValidatedBuildV1
		}
		commitErr := commit(updated)
		current, found, readErr := operation.ReadValidatedBuildV1()
		if commitErr != nil || readErr != nil || !found || !reflect.DeepEqual(current, updated) {
			// Even an applied-then-error commit authorizes no alias mutation in
			// this attempt. The actual durable record remains the retry authority.
			if readErr == nil && found {
				return current, true, errors.Join(fmt.Errorf("validated discard guard was not confirmed"), commitErr)
			}
			return record, true, errors.Join(fmt.Errorf("validated discard guard was not confirmed"), commitErr, readErr)
		}
		record = current
	}
	record, cleanupErrors := cleanupPendingValidatedReferencesV1(context.WithoutCancel(ctx), operation, record, environment, dir, backend)
	return record, true, errors.Join(cleanupErrors...)
}
