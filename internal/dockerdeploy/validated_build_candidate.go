package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ValidatedBuildInputsV1 struct {
	BlueprintDigest        canonical.Digest
	OverlayDigest          canonical.Digest
	PackageOverridesDigest canonical.Digest
	PackageOverrides       deploy.PackageOverrideIntentV1
	BaseImage              string
	Platform               blueprint.Platform
}

type ValidatedBuildCandidateV1 struct {
	Record  deploy.ValidatedBuildV1
	Current CurrentBuild
}

type OverrideDiscoveredPackageV1 struct {
	Provider string
	Package  string
}

type StagedOverrideValidationV1 struct {
	Validated bool
	Packages  []OverrideDiscoveredPackageV1
	Unused    []OverrideDiscoveredPackageV1
}

func ValidatedBuildInputs(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	overrides deploy.PackageOverridesV1,
	deploymentDir string,
	platform blueprint.Platform,
) (ValidatedBuildInputsV1, error) {
	blueprintDigest, err := blueprint.DocumentDigestV1(document)
	if err != nil {
		return ValidatedBuildInputsV1{}, err
	}
	overlayDigest, err := deploy.RequestOverlayDigestV1(overlay)
	if err != nil {
		return ValidatedBuildInputsV1{}, err
	}
	overridesDigest, err := deploy.PackageOverridesDigestV1(overrides)
	if err != nil {
		return ValidatedBuildInputsV1{}, err
	}
	resolvedOverrides, err := deploy.ResolvePackageOverridesV1(
		overrides, deploymentDir, registry.NormalizePackageOverrideV1,
	)
	if err != nil {
		return ValidatedBuildInputsV1{}, err
	}
	overrideIntent, err := resolvedOverrides.Intent()
	if err != nil {
		return ValidatedBuildInputsV1{}, err
	}
	if err := platform.Validate(); err != nil {
		return ValidatedBuildInputsV1{}, err
	}
	baseImage, err := deploy.EffectiveBaseImageV1(document, overrides)
	if err != nil {
		return ValidatedBuildInputsV1{}, err
	}
	return ValidatedBuildInputsV1{
		BlueprintDigest: blueprintDigest, OverlayDigest: overlayDigest,
		PackageOverridesDigest: overridesDigest, PackageOverrides: overrideIntent,
		BaseImage: baseImage, Platform: platform,
	}, nil
}

func ValidatedBuildRecordMatchesInputs(record deploy.ValidatedBuildV1, input ValidatedBuildInputsV1) bool {
	return !record.Discarded &&
		record.BlueprintDigest == input.BlueprintDigest &&
		record.OverlayDigest == input.OverlayDigest &&
		record.PackageOverridesDigest == input.PackageOverridesDigest &&
		record.Platform == input.Platform
}

func validateBuildLockMatchesValidatedInputs(lock deploy.BuildLockV1, input ValidatedBuildInputsV1) error {
	if lock.BlueprintDigest != input.BlueprintDigest {
		return fmt.Errorf("validated build lock blueprint does not match its input")
	}
	overlayDigest, err := deploy.RequestOverlayDigestV1(lock.Overlay)
	if err != nil {
		return err
	}
	if overlayDigest != input.OverlayDigest {
		return fmt.Errorf("validated build lock request overlay does not match its input")
	}
	if !reflect.DeepEqual(lock.PackageOverrides, input.PackageOverrides) {
		return fmt.Errorf("validated build lock package overrides do not match its input")
	}
	if lock.Base.AuthorReference != input.BaseImage {
		return fmt.Errorf("validated build lock base image does not match its input")
	}
	if lock.Platform != input.Platform {
		return fmt.Errorf("validated build lock platform does not match its input")
	}
	return nil
}

// LoadValidatedBuildCandidate validates a recorded trial build without making
// it current. verifyImage is intentionally selectable: the editor can report
// the durable input-bound result while offline, whereas promotion verifies the
// Docker reference before trusting it.
func LoadValidatedBuildCandidate(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	document blueprint.Document,
	state deploy.StateV1,
	overrides deploy.PackageOverridesV1,
	deploymentDir string,
	verifyCache bool,
	verifyImage bool,
) (ValidatedBuildCandidateV1, bool, error) {
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found {
		return ValidatedBuildCandidateV1{}, false, err
	}
	if record.Discarded {
		return ValidatedBuildCandidateV1{}, false, nil
	}
	inputs, err := ValidatedBuildInputs(document, state.Overlay, overrides, deploymentDir, state.Platform)
	if err != nil {
		return ValidatedBuildCandidateV1{}, false, err
	}
	stateDocument, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return ValidatedBuildCandidateV1{}, false, fmt.Errorf("validated build state blueprint: %w", err)
	}
	stateBlueprintDigest, err := blueprint.DocumentDigestV1(stateDocument)
	if err != nil {
		return ValidatedBuildCandidateV1{}, false, err
	}
	if stateBlueprintDigest != inputs.BlueprintDigest {
		return ValidatedBuildCandidateV1{}, false, fmt.Errorf("validated build document does not match deployment state")
	}
	if !ValidatedBuildRecordMatchesInputs(record, inputs) {
		return ValidatedBuildCandidateV1{}, false, nil
	}
	lock, found, err := operation.ReadBuildLock(record.BuildLockDigest, registry.ValidateRequirementProfileV1)
	if err != nil {
		return ValidatedBuildCandidateV1{}, false, err
	}
	if !found {
		return ValidatedBuildCandidateV1{}, false, fmt.Errorf("validated build lock %s is missing", record.BuildLockDigest)
	}
	digest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		return ValidatedBuildCandidateV1{}, false, err
	}
	if digest != record.BuildLockDigest {
		return ValidatedBuildCandidateV1{}, false, fmt.Errorf("validated build lock identity changed")
	}
	if err := validateBuildLockMatchesValidatedInputs(lock, inputs); err != nil {
		return ValidatedBuildCandidateV1{}, false, err
	}
	if !reflect.DeepEqual(lock.FinalImage, record.Image) {
		return ValidatedBuildCandidateV1{}, false, fmt.Errorf("validated build image does not match its build lock")
	}
	if verifyCache {
		if _, err := deploy.ReusableBuildLockStoreClosure(
			lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
		); err != nil {
			return ValidatedBuildCandidateV1{}, false, fmt.Errorf("validated build cache: %w", err)
		}
	}
	if verifyImage {
		if err := VerifyEnvironmentGenerationReference(
			ctx, lock.FinalImage, record.ImageReference, document.Environment.ID, deploymentDir,
		); err != nil {
			return ValidatedBuildCandidateV1{}, false, fmt.Errorf("validated build image: %w", err)
		}
	}
	policyDigest, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		return ValidatedBuildCandidateV1{}, false, err
	}
	generation := deploy.EnvironmentGenerationState{
		Reference: record.ImageReference, ImageDigest: lock.FinalImage.Digest,
		RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: record.BuildLockDigest,
		Platform: lock.Platform, RuntimePolicyDigest: policyDigest,
	}
	synthetic := state
	synthetic.Platform = lock.Platform
	synthetic.Overlay = lock.Overlay
	synthetic.Current = &generation
	current := CurrentBuild{State: synthetic, Generation: generation, Lock: lock}
	if err := deploy.ValidateStateV1(synthetic); err != nil {
		return ValidatedBuildCandidateV1{}, false, fmt.Errorf("validated build synthetic state: %w", err)
	}
	return ValidatedBuildCandidateV1{Record: record, Current: current}, true, nil
}

func InspectStagedOverrideValidation(ctx context.Context, deploymentDir string) (result StagedOverrideValidationV1, resultErr error) {
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return StagedOverrideValidationV1{}, fmt.Errorf("resolve validated build directory: %w", err)
	}
	deploymentDir = filepath.Clean(absoluteDir)
	operation, err := deploy.AcquireExistingOperationLock(ctx, deploymentDir)
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, operation.Unlock())
	}()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	if !found {
		return StagedOverrideValidationV1{}, fmt.Errorf("package override validation requires an existing staged deployment")
	}
	if state.Staging == nil {
		return StagedOverrideValidationV1{}, fmt.Errorf("package override validation requires a staged deployment")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	overrides, found, err := deploy.ReadPackageOverridesV1(deploymentDir)
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	if !found {
		overrides = deploy.EmptyPackageOverridesV1(document.Environment.ID)
	}
	store, err := providerstore.NewStore(deploymentDir)
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	cacheExists, err := store.Exists()
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	if !cacheExists {
		return StagedOverrideValidationV1{}, nil
	}
	candidate, found, err := LoadValidatedBuildCandidate(
		ctx, operation, store, document, state, overrides, deploymentDir, false, false,
	)
	if err != nil || !found {
		return StagedOverrideValidationV1{}, err
	}
	dependencies, err := DirectPythonDependenciesFromBuildLock(store, candidate.Current.Lock)
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	packages := make([]OverrideDiscoveredPackageV1, 0, len(dependencies))
	for _, dependency := range dependencies {
		packages = append(packages, OverrideDiscoveredPackageV1{
			Provider: string(blueprint.ComponentTypePython), Package: dependency,
		})
	}
	resolved, err := deploy.ResolvePackageOverridesV1(overrides, deploymentDir, registry.NormalizePackageOverrideV1)
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	intent, err := resolved.Intent()
	if err != nil {
		return StagedOverrideValidationV1{}, err
	}
	used := map[string]struct{}{}
	for _, choice := range candidate.Current.Lock.PackageOverrides.Choices {
		used[choice.Provider+"\x00"+choice.Package] = struct{}{}
	}
	unused := []OverrideDiscoveredPackageV1{}
	for _, choice := range intent.Choices {
		if _, found := used[choice.Provider+"\x00"+choice.Package]; !found {
			unused = append(unused, OverrideDiscoveredPackageV1{Provider: choice.Provider, Package: choice.Package})
		}
	}
	return StagedOverrideValidationV1{Validated: true, Packages: packages, Unused: unused}, nil
}

func DirectPythonDependenciesFromBuildLock(store providerstore.Store, lock deploy.BuildLockV1) ([]string, error) {
	seen := map[string]struct{}{}
	for _, node := range lock.Nodes {
		if node.Provider != blueprint.ComponentTypePython {
			continue
		}
		bundleRecord, err := providers.LoadResolvedBundleManifest(
			store, node.BundleManifest, registry.ValidateResolvedBundlePayloadV1,
		)
		if err != nil {
			return nil, fmt.Errorf("load Python bundle %q for dependency discovery: %w", node.NodeID, err)
		}
		component, ok := bundleRecord.Payload.Request.Value["component"].(string)
		if !ok {
			return nil, fmt.Errorf("Python bundle %q has no component", node.NodeID)
		}
		bundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component, bundleRecord.Payload.ProviderPayload)
		if err != nil {
			return nil, err
		}
		roots, err := pythonprovider.ProviderRequestDistributionsV1(bundleRecord.Payload.Request)
		if err != nil {
			return nil, err
		}
		wheels := make(map[string]providerstore.ArtifactDescriptor, len(bundle.Wheels))
		resolvedDistributions := make([]string, 0, len(bundle.Wheels))
		for _, wheel := range bundle.Wheels {
			wheels[wheel.Distribution] = wheel.Artifact
			resolvedDistributions = append(resolvedDistributions, wheel.Distribution)
		}
		sort.Strings(resolvedDistributions)
		for _, root := range roots {
			artifact, found := wheels[root]
			if !found {
				return nil, fmt.Errorf("Python bundle %q is missing root wheel %q", node.NodeID, root)
			}
			file, err := store.OpenVerifiedArtifact(artifact)
			if err != nil {
				return nil, err
			}
			info, infoErr := file.Stat()
			var dependencies []string
			var inspectErr error
			if infoErr == nil {
				dependencies, inspectErr = pythonprovider.InspectWheelDeclaredDependenciesReaderV1(
					file, info.Size(), resolvedDistributions,
				)
			}
			verifyErr := providerstore.VerifyOpenArtifact(file, artifact)
			closeErr := file.Close()
			if err := errors.Join(infoErr, inspectErr, verifyErr, closeErr); err != nil {
				return nil, fmt.Errorf("inspect declared dependencies of %q: %w", root, err)
			}
			for _, dependency := range dependencies {
				seen[dependency] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for dependency := range seen {
		result = append(result, dependency)
	}
	sort.Strings(result)
	return result, nil
}

func DiscardValidatedBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	environment string,
	deploymentDir string,
	progress io.Writer,
) error {
	store, err := providerstore.NewStore(deploymentDir)
	if err != nil {
		return err
	}
	pending, err := discardValidatedBuild(
		ctx, operation, store, environment, deploymentDir, RemoveEnvironmentGenerationReference,
	)
	if err != nil {
		return err
	}
	if pending {
		writeProviderBuildProgress(
			progress,
			"warning: validated build was discarded, but cleanup of its storage is pending; Reploy will retry automatically",
		)
	}
	return nil
}

func discardValidatedBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	environment string,
	deploymentDir string,
	removeReference func(context.Context, providers.RealizedImageV1, string, string, string) error,
) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("discard validated build requires a context")
	}
	if operation == nil || removeReference == nil {
		return false, fmt.Errorf("discard validated build requires a complete backend")
	}
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found {
		return false, err
	}
	if !record.Discarded {
		record, cleanupErrors := cleanupPendingValidatedBuildReferences(
			context.WithoutCancel(ctx), operation, record, environment, deploymentDir,
			removeReference,
		)
		if err := removeReference(
			context.WithoutCancel(ctx), record.Image, record.ImageReference, environment, deploymentDir,
		); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf(
				"remove current validated image reference %q: %w", record.ImageReference, err,
			))
		}
		if len(cleanupErrors) != 0 {
			return false, errors.Join(cleanupErrors...)
		}
		record.PendingCleanup = nil
		record.PendingStorageCleanup = true
		record.Discarded = true
		if err := operation.CommitValidatedBuildV1(record); err != nil {
			return false, err
		}
	}
	if err := cleanupValidatedBuildStorage(operation, store, nil); err != nil {
		return true, nil
	}
	if err := operation.RemoveValidatedBuildV1(); err != nil {
		return true, nil
	}
	return false, nil
}

type publishValidatedBuildBackendV1 struct {
	newReferences   func(string, string) (EnvironmentImageReferences, error)
	createReference func(context.Context, providers.RealizedImageV1, EnvironmentImageReferences, EnvironmentReferenceKind, string, string) error
	removeReference func(context.Context, providers.RealizedImageV1, string, string, string) error
}

// RetryValidatedBuildCleanup retries superseded Docker references, build
// locks, and provider-store objects retained by a successful validation.
// Cleanup failures remain durable and do not invalidate the current candidate.
func RetryValidatedBuildCleanup(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	environment string,
	deploymentDir string,
) (deploy.ValidatedBuildV1, bool, error) {
	if ctx == nil {
		return deploy.ValidatedBuildV1{}, false, fmt.Errorf("retry validated build cleanup requires a context")
	}
	if operation == nil {
		return deploy.ValidatedBuildV1{}, false, fmt.Errorf("retry validated build cleanup requires an operation lock")
	}
	record, found, err := operation.ReadValidatedBuildV1()
	if err != nil || !found {
		return deploy.ValidatedBuildV1{}, found, err
	}
	if !record.Discarded {
		record, _ = cleanupPendingValidatedBuildReferences(
			context.WithoutCancel(ctx), operation, record, environment, deploymentDir,
			RemoveEnvironmentGenerationReference,
		)
	}
	if record.PendingStorageCleanup {
		var candidate *deploy.BuildLockV1
		if !record.Discarded {
			build, found, err := operation.ReadBuildLock(
				record.BuildLockDigest, registry.ValidateRequirementProfileV1,
			)
			if err != nil {
				return deploy.ValidatedBuildV1{}, false, err
			}
			if !found {
				return deploy.ValidatedBuildV1{}, false, fmt.Errorf(
					"validated build lock %s is missing during cleanup retry",
					record.BuildLockDigest,
				)
			}
			candidate = &build
		}
		if err := cleanupValidatedBuildStorage(operation, store, candidate); err == nil {
			if record.Discarded {
				if err := operation.RemoveValidatedBuildV1(); err == nil {
					return deploy.ValidatedBuildV1{}, false, nil
				}
			} else {
				updated := record
				updated.PendingStorageCleanup = false
				if err := operation.CommitValidatedBuildV1(updated); err == nil {
					record = updated
				}
			}
		}
	}
	return record, true, nil
}

func PublishValidatedBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	environment string,
	deploymentDir string,
	lock deploy.BuildLockV1,
	inputs ValidatedBuildInputsV1,
) (deploy.ValidatedBuildV1, error) {
	return publishValidatedBuild(ctx, operation, store, environment, deploymentDir, lock, inputs, publishValidatedBuildBackendV1{
		newReferences:   NewEnvironmentImageReferences,
		createReference: CreateEnvironmentImageReference,
		removeReference: RemoveEnvironmentGenerationReference,
	})
}

// publishValidatedBuild returns success once the new candidate is committed.
// A nonempty PendingCleanup list means superseded Docker references could not
// yet be removed; callers should warn and a later validation or discard should
// retry them.
func publishValidatedBuild(
	ctx context.Context,
	operation *deploy.OperationLock,
	store providerstore.Store,
	environment string,
	deploymentDir string,
	lock deploy.BuildLockV1,
	inputs ValidatedBuildInputsV1,
	backend publishValidatedBuildBackendV1,
) (result deploy.ValidatedBuildV1, resultErr error) {
	if ctx == nil {
		return deploy.ValidatedBuildV1{}, fmt.Errorf("publish validated build requires a context")
	}
	if operation == nil || backend.newReferences == nil || backend.createReference == nil || backend.removeReference == nil {
		return deploy.ValidatedBuildV1{}, fmt.Errorf("publish validated build requires a complete backend")
	}
	if err := validatePublicationDeployment(operation, store, deploymentDir); err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	lockDigest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	if _, err := deploy.BuildLockStoreClosure(
		lock, store, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	); err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	if err := validateBuildLockMatchesValidatedInputs(lock, inputs); err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	old, oldFound, err := operation.ReadValidatedBuildV1()
	if err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	references, err := backend.newReferences(environment, deploymentDir)
	if err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	pendingCleanup := []deploy.ValidatedBuildReferenceV1(nil)
	if oldFound && !old.Discarded {
		pendingCleanup, err = mergeValidatedBuildReferences(
			deploy.ValidatedBuildReferenceV1{
				Image: lock.FinalImage, ImageReference: references.Generation,
			},
			old.PendingCleanup,
			[]deploy.ValidatedBuildReferenceV1{{
				Image: old.Image, ImageReference: old.ImageReference,
			}},
		)
		if err != nil {
			return deploy.ValidatedBuildV1{}, err
		}
	}
	if err := backend.createReference(
		ctx, lock.FinalImage, references, EnvironmentReferenceGeneration, environment, deploymentDir,
	); err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	created := true
	defer func() {
		if resultErr != nil && created {
			cleanupErr := backend.removeReference(
				context.WithoutCancel(ctx), lock.FinalImage, references.Generation, environment, deploymentDir,
			)
			if cleanupErr != nil {
				resultErr = errors.Join(
					resultErr,
					fmt.Errorf("cleanup newly created validated image reference: %w", cleanupErr),
				)
			}
		}
	}()
	publishedDigest, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	if publishedDigest != lockDigest {
		return deploy.ValidatedBuildV1{}, fmt.Errorf("published validated build lock identity changed")
	}
	record := deploy.ValidatedBuildV1{
		Schema:          deploy.ValidatedBuildSchemaV1,
		BlueprintDigest: inputs.BlueprintDigest, OverlayDigest: inputs.OverlayDigest,
		PackageOverridesDigest: inputs.PackageOverridesDigest, Platform: inputs.Platform,
		BuildLockDigest: lockDigest, Image: lock.FinalImage, ImageReference: references.Generation,
		PendingCleanup: pendingCleanup, PendingStorageCleanup: true,
	}
	if err := operation.CommitValidatedBuildV1(record); err != nil {
		return deploy.ValidatedBuildV1{}, err
	}
	created = false
	record, _ = cleanupPendingValidatedBuildReferences(
		context.WithoutCancel(ctx), operation, record, environment, deploymentDir, backend.removeReference,
	)
	if err := cleanupValidatedBuildStorage(operation, store, &lock); err == nil {
		updated := record
		updated.PendingStorageCleanup = false
		if err := operation.CommitValidatedBuildV1(updated); err == nil {
			record = updated
		}
	}
	return record, nil
}

func cleanupValidatedBuildStorage(
	operation *deploy.OperationLock,
	store providerstore.Store,
	candidate *deploy.BuildLockV1,
) error {
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return err
	}
	roots := make([]deploy.BuildLockV1, 0, 2)
	digests := make([]canonical.Digest, 0, 2)
	if found && state.Current != nil {
		current, found, err := operation.ReadBuildLock(
			state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1,
		)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("current build lock %s is missing during validated build cleanup", state.Current.BuildLockDigest)
		}
		if err := validateGenerationBuildLock(
			*state.Current, current, registry.ValidateRequirementProfileV1,
		); err != nil {
			return err
		}
		roots = append(roots, current)
		digests = append(digests, state.Current.BuildLockDigest)
	}
	if candidate != nil {
		digest, err := deploy.BuildLockDigestV1(*candidate, registry.ValidateRequirementProfileV1)
		if err != nil {
			return err
		}
		duplicate := false
		for _, retained := range digests {
			if retained == digest {
				duplicate = true
				break
			}
		}
		if !duplicate {
			roots = append(roots, *candidate)
			digests = append(digests, digest)
		}
	}
	if err := operation.RemoveUnreachableBuildObjectsForBuilds(
		store, roots, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
	); err != nil {
		return err
	}
	return operation.RemoveBuildLocksExcept(digests, registry.ValidateRequirementProfileV1)
}

func mergeValidatedBuildReferences(
	current deploy.ValidatedBuildReferenceV1,
	groups ...[]deploy.ValidatedBuildReferenceV1,
) ([]deploy.ValidatedBuildReferenceV1, error) {
	byReference := map[string]deploy.ValidatedBuildReferenceV1{}
	for _, group := range groups {
		for _, reference := range group {
			if reference.ImageReference == current.ImageReference {
				if !reflect.DeepEqual(reference.Image, current.Image) {
					return nil, fmt.Errorf(
						"new validated build reference %q conflicts with a retained image identity",
						current.ImageReference,
					)
				}
				continue
			}
			if existing, found := byReference[reference.ImageReference]; found &&
				!reflect.DeepEqual(existing.Image, reference.Image) {
				return nil, fmt.Errorf(
					"validated build cleanup reference %q has conflicting image identities",
					reference.ImageReference,
				)
			}
			byReference[reference.ImageReference] = reference
		}
	}
	result := make([]deploy.ValidatedBuildReferenceV1, 0, len(byReference))
	for _, reference := range byReference {
		result = append(result, reference)
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].ImageReference < result[right].ImageReference
	})
	return result, nil
}

func cleanupPendingValidatedBuildReferences(
	ctx context.Context,
	operation *deploy.OperationLock,
	record deploy.ValidatedBuildV1,
	environment string,
	deploymentDir string,
	removeReference func(context.Context, providers.RealizedImageV1, string, string, string) error,
) (deploy.ValidatedBuildV1, []error) {
	if len(record.PendingCleanup) == 0 {
		return record, nil
	}
	var remaining []deploy.ValidatedBuildReferenceV1
	cleanupErrors := []error{}
	for _, pending := range record.PendingCleanup {
		if err := removeReference(
			ctx, pending.Image, pending.ImageReference, environment, deploymentDir,
		); err != nil {
			remaining = append(remaining, pending)
			cleanupErrors = append(cleanupErrors, fmt.Errorf(
				"remove superseded validated image reference %q: %w", pending.ImageReference, err,
			))
		}
	}
	updated := record
	updated.PendingCleanup = remaining
	if reflect.DeepEqual(updated.PendingCleanup, record.PendingCleanup) {
		return record, cleanupErrors
	}
	if err := operation.CommitValidatedBuildV1(updated); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("record validated image cleanup progress: %w", err))
		return record, cleanupErrors
	}
	return updated, cleanupErrors
}
