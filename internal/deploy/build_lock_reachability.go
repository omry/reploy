package deploy

import (
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type buildLockClosureArtifact struct {
	NodeID     providers.NodeID
	Descriptor providerstore.ArtifactDescriptor
}

type buildLockStoreClosurePlan struct {
	References       []providerstore.StoreObjectRef
	Artifacts        map[providerstore.StoreObjectRef]providerstore.ArtifactDescriptor
	OrderedArtifacts []buildLockClosureArtifact
	Manifests        map[providerstore.StoreObjectRef][]byte
	Validation       []byte
}

func BuildLockStoreClosure(
	lock BuildLockV1,
	store providerstore.Store,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) ([]providerstore.StoreObjectRef, error) {
	plan, err := loadBuildLockStoreClosure(lock, store, validateProfileOwner, validateBundleOwner)
	if err != nil {
		return nil, err
	}
	for _, artifact := range plan.OrderedArtifacts {
		if err := store.VerifyArtifact(artifact.Descriptor); err != nil {
			return nil, fmt.Errorf("verify build lock node %q artifact %q: %w", artifact.NodeID, artifact.Descriptor.LogicalPath, err)
		}
	}
	return plan.References, nil
}

// ReusableBuildLockStoreClosure verifies the closure for deployment-local
// cache reuse. Debian archives use deployment-local verification stamps so an
// unchanged size and modification time avoid rereading their bodies. All other
// artifacts continue to receive full content hashing.
func ReusableBuildLockStoreClosure(
	lock BuildLockV1,
	store providerstore.Store,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) ([]providerstore.StoreObjectRef, error) {
	plan, err := loadBuildLockStoreClosure(lock, store, validateProfileOwner, validateBundleOwner)
	if err != nil {
		return nil, err
	}
	for _, artifact := range plan.OrderedArtifacts {
		if artifact.Descriptor.Kind == "deb" {
			if err := store.VerifyCachedDeb(artifact.Descriptor); err == nil {
				continue
			} else {
				return nil, fmt.Errorf(
					"verify reusable build lock node %q artifact %q: %w",
					artifact.NodeID, artifact.Descriptor.LogicalPath, err,
				)
			}
		}
		if err := store.VerifyArtifact(artifact.Descriptor); err != nil {
			return nil, fmt.Errorf(
				"verify reusable build lock node %q artifact %q: %w",
				artifact.NodeID, artifact.Descriptor.LogicalPath, err,
			)
		}
	}
	return plan.References, nil
}

// BuildLockStoreClosureBytes returns the logical bytes written when every
// object in the locked closure is copied. It validates record content and blob
// layout and size, but deliberately does not hash blob bodies; transfer does
// that while streaming each object into the destination store.
func BuildLockStoreClosureBytes(
	lock BuildLockV1,
	store providerstore.Store,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) (uint64, error) {
	_, total, err := InspectBuildLockStoreClosure(lock, store, validateProfileOwner, validateBundleOwner)
	return total, err
}

// InspectBuildLockStoreClosure returns the ordered closure and its logical
// transfer size. It validates records plus blob layout and size without
// hashing blob bodies; transfer remains responsible for streaming verification.
func InspectBuildLockStoreClosure(
	lock BuildLockV1,
	store providerstore.Store,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) ([]providerstore.StoreObjectRef, uint64, error) {
	plan, err := loadBuildLockStoreClosure(lock, store, validateProfileOwner, validateBundleOwner)
	if err != nil {
		return nil, 0, err
	}
	total := uint64(len(plan.Validation))
	for _, content := range plan.Manifests {
		if err := addBuildClosureBytes(&total, uint64(len(content))); err != nil {
			return nil, 0, err
		}
	}
	for reference, descriptor := range plan.Artifacts {
		if _, err := store.InspectArtifactPath(descriptor); err != nil {
			return nil, 0, fmt.Errorf("inspect build lock blob %s: %w", reference.Digest, err)
		}
		size, err := strconv.ParseUint(descriptor.Size, 10, 64)
		if err != nil {
			return nil, 0, fmt.Errorf("parse build lock blob %s size: %w", reference.Digest, err)
		}
		if err := addBuildClosureBytes(&total, size); err != nil {
			return nil, 0, err
		}
	}
	return append([]providerstore.StoreObjectRef(nil), plan.References...), total, nil
}

func addBuildClosureBytes(total *uint64, size uint64) error {
	if math.MaxUint64-*total < size {
		return fmt.Errorf("build lock store closure byte size overflows uint64")
	}
	*total += size
	return nil
}

func loadBuildLockStoreClosure(
	lock BuildLockV1,
	store providerstore.Store,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) (buildLockStoreClosurePlan, error) {
	if err := ValidateBuildLockV1(lock, validateProfileOwner); err != nil {
		return buildLockStoreClosurePlan{}, fmt.Errorf("build lock store closure: %w", err)
	}
	policyDigest, err := RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		return buildLockStoreClosurePlan{}, err
	}
	validationContent, err := store.LoadValidationRecord(lock.ValidationRecord)
	if err != nil {
		return buildLockStoreClosurePlan{}, fmt.Errorf("load build lock validation record: %w", err)
	}
	validation, err := DecodePrefixValidation(validationContent, lock.ValidationRecord)
	if err != nil {
		return buildLockStoreClosurePlan{}, fmt.Errorf("load build lock validation record: %w", err)
	}
	if validation.SubjectRootFS != lock.FinalImage.RootFSSubject || validation.RuntimePolicy != policyDigest {
		return buildLockStoreClosurePlan{}, fmt.Errorf("build lock validation record does not match its final image and runtime policy")
	}

	objects := map[string]providerstore.StoreObjectRef{}
	artifacts := map[providerstore.StoreObjectRef]providerstore.ArtifactDescriptor{}
	orderedArtifacts := []buildLockClosureArtifact{}
	manifests := map[providerstore.StoreObjectRef][]byte{}
	addStoreClosureReference(objects, lock.ValidationRecord)
	for _, node := range lock.Nodes {
		manifestContent, err := store.LoadManifest(node.BundleManifest)
		if err != nil {
			return buildLockStoreClosurePlan{}, fmt.Errorf("load build lock node %q manifest: %w", node.NodeID, err)
		}
		bundle, err := providers.DecodeResolvedBundleManifest(manifestContent, node.BundleManifest, validateBundleOwner)
		if err != nil {
			return buildLockStoreClosurePlan{}, fmt.Errorf("load build lock node %q manifest: %w", node.NodeID, err)
		}
		profileDigest, err := providers.RequirementProfileDigest(node.RequirementProfile, validateProfileOwner)
		if err != nil {
			return buildLockStoreClosurePlan{}, err
		}
		payload := bundle.Payload
		if payload.NodeID != node.NodeID || payload.Provider != node.Provider || payload.RequirementProfileDigest != profileDigest || payload.Platform != lock.Platform || payload.Upstream != node.Upstream {
			return buildLockStoreClosurePlan{}, fmt.Errorf("build lock node %q manifest does not match its locked resolution inputs", node.NodeID)
		}
		if len(payload.Outputs) != len(node.Outputs) {
			return buildLockStoreClosurePlan{}, fmt.Errorf("build lock node %q manifest outputs do not match locked realized outputs", node.NodeID)
		}
		for index, resolved := range payload.Outputs {
			realized := node.Outputs[index]
			if resolved.SupplierComponent != realized.SupplierComponent || resolved.SupplierNode != realized.SupplierNode || resolved.Name != realized.Name || !reflect.DeepEqual(resolved.Candidate, realized.Candidate) {
				return buildLockStoreClosurePlan{}, fmt.Errorf("build lock node %q output %d changed after resolution", node.NodeID, index)
			}
		}
		addStoreClosureReference(objects, node.BundleManifest)
		manifests[node.BundleManifest] = manifestContent
		for _, artifact := range payload.Artifacts {
			reference, err := artifact.StoreObjectRef()
			if err != nil {
				return buildLockStoreClosurePlan{}, err
			}
			addStoreClosureReference(objects, reference)
			if previous, found := artifacts[reference]; found && previous.Size != artifact.Size {
				return buildLockStoreClosurePlan{}, fmt.Errorf("build lock blob %s has conflicting artifact sizes %s and %s", reference.Digest, previous.Size, artifact.Size)
			}
			if _, found := artifacts[reference]; !found {
				artifacts[reference] = artifact
			}
			orderedArtifacts = append(orderedArtifacts, buildLockClosureArtifact{NodeID: node.NodeID, Descriptor: artifact})
		}
	}
	if lock.PortableTools != nil {
		for _, acquisition := range lock.PortableTools.Acquisitions {
			reference, err := acquisition.Descriptor.StoreObjectRef()
			if err != nil {
				return buildLockStoreClosurePlan{}, fmt.Errorf("portable tool artifact %q: %w", acquisition.Artifact.ID, err)
			}
			addStoreClosureReference(objects, reference)
			if previous, found := artifacts[reference]; found && previous.Size != acquisition.Descriptor.Size {
				return buildLockStoreClosurePlan{}, fmt.Errorf("build lock blob %s has conflicting artifact sizes %s and %s", reference.Digest, previous.Size, acquisition.Descriptor.Size)
			}
			if _, found := artifacts[reference]; !found {
				artifacts[reference] = acquisition.Descriptor
			}
			orderedArtifacts = append(orderedArtifacts, buildLockClosureArtifact{
				NodeID:     providers.NodeID("portable-tool:" + acquisition.Scope + "/" + acquisition.Tool),
				Descriptor: acquisition.Descriptor,
			})
		}
	}
	result := make([]providerstore.StoreObjectRef, 0, len(objects))
	for _, reference := range objects {
		result = append(result, reference)
	}
	sort.Slice(result, func(left int, right int) bool {
		if result[left].Kind != result[right].Kind {
			return result[left].Kind < result[right].Kind
		}
		return result[left].Digest < result[right].Digest
	})
	return buildLockStoreClosurePlan{
		References: result, Artifacts: artifacts, OrderedArtifacts: orderedArtifacts,
		Manifests: manifests, Validation: validationContent,
	}, nil
}

func (lock *OperationLock) RemoveUnreachableBuildObjects(
	store providerstore.Store,
	build BuildLockV1,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) error {
	return lock.RemoveUnreachableBuildObjectsForBuilds(
		store, []BuildLockV1{build}, validateProfileOwner, validateBundleOwner,
	)
}

// RemoveUnreachableBuildObjectsForBuilds retains the union of the selected
// build closures. An empty root set removes every provider-store object.
func (lock *OperationLock) RemoveUnreachableBuildObjectsForBuilds(
	store providerstore.Store,
	builds []BuildLockV1,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) error {
	if lock == nil {
		return fmt.Errorf("clean build objects requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if lock.released || lock.file == nil || lock.path == "" {
		return fmt.Errorf("operation lock is not held")
	}
	if err := lock.validateProviderStoreLocked(store); err != nil {
		return err
	}
	byIdentity := map[string]providerstore.StoreObjectRef{}
	for _, build := range builds {
		reachable, err := BuildLockStoreClosure(build, store, validateProfileOwner, validateBundleOwner)
		if err != nil {
			return err
		}
		for _, reference := range reachable {
			key := reference.Kind + "\x00" + string(reference.Digest)
			byIdentity[key] = reference
		}
	}
	reachable := make([]providerstore.StoreObjectRef, 0, len(byIdentity))
	for _, reference := range byIdentity {
		reachable = append(reachable, reference)
	}
	sort.Slice(reachable, func(left int, right int) bool {
		if reachable[left].Kind != reachable[right].Kind {
			return reachable[left].Kind < reachable[right].Kind
		}
		return reachable[left].Digest < reachable[right].Digest
	})
	return store.RemoveUnreachable(reachable)
}

func (lock *OperationLock) ValidateProviderStore(store providerstore.Store) error {
	if lock == nil {
		return fmt.Errorf("validate provider store requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	return lock.validateProviderStoreLocked(store)
}

func (lock *OperationLock) RemoveAllBuildObjects(store providerstore.Store) error {
	_, err := lock.RemoveProviderStore(store)
	return err
}

// RemoveProviderStore deletes the complete deployment-owned provider cache
// while retaining state, build locks, and the operation lock itself.
func (lock *OperationLock) RemoveProviderStore(store providerstore.Store) (bool, error) {
	if lock == nil {
		return false, fmt.Errorf("clean provider store requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.validateProviderStoreLocked(store); err != nil {
		return false, err
	}
	return store.Remove()
}

func (lock *OperationLock) validateProviderStoreLocked(store providerstore.Store) error {
	if lock.released || lock.file == nil || lock.path == "" {
		return fmt.Errorf("operation lock is not held")
	}
	deploymentRoot := filepath.Dir(filepath.Dir(lock.path))
	expectedStoreRoot := filepath.Join(deploymentRoot, ".reploy", providerstore.StoreDirName)
	if filepath.Clean(store.Root()) != expectedStoreRoot {
		return fmt.Errorf("provider store does not belong to the locked deployment")
	}
	return nil
}

func addStoreClosureReference(objects map[string]providerstore.StoreObjectRef, reference providerstore.StoreObjectRef) {
	key := reference.Kind + "\x00" + string(reference.Digest)
	objects[key] = reference
}
