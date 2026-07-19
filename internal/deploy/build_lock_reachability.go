package deploy

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func BuildLockStoreClosure(
	lock BuildLockV1,
	store providerstore.Store,
	validateProfileOwner providers.RequirementProfileOwnerValidator,
	validateBundleOwner providers.ResolvedBundleOwnerValidator,
) ([]providerstore.StoreObjectRef, error) {
	if err := ValidateBuildLockV1(lock, validateProfileOwner); err != nil {
		return nil, fmt.Errorf("build lock store closure: %w", err)
	}
	policyDigest, err := RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		return nil, err
	}
	validation, err := LoadPrefixValidation(store, lock.ValidationRecord)
	if err != nil {
		return nil, fmt.Errorf("load build lock validation record: %w", err)
	}
	if validation.SubjectRootFS != lock.FinalImage.RootFSSubject || validation.RuntimePolicy != policyDigest {
		return nil, fmt.Errorf("build lock validation record does not match its final image and runtime policy")
	}

	objects := map[string]providerstore.StoreObjectRef{}
	addStoreClosureReference(objects, lock.ValidationRecord)
	for _, node := range lock.Nodes {
		bundle, err := providers.LoadResolvedBundleManifest(store, node.BundleManifest, validateBundleOwner)
		if err != nil {
			return nil, fmt.Errorf("load build lock node %q manifest: %w", node.NodeID, err)
		}
		profileDigest, err := providers.RequirementProfileDigest(node.RequirementProfile, validateProfileOwner)
		if err != nil {
			return nil, err
		}
		payload := bundle.Payload
		if payload.NodeID != node.NodeID || payload.Provider != node.Provider || payload.RequirementProfileDigest != profileDigest || payload.Platform != lock.Platform || payload.Upstream != node.Upstream {
			return nil, fmt.Errorf("build lock node %q manifest does not match its locked resolution inputs", node.NodeID)
		}
		if len(payload.Outputs) != len(node.Outputs) {
			return nil, fmt.Errorf("build lock node %q manifest outputs do not match locked realized outputs", node.NodeID)
		}
		for index, resolved := range payload.Outputs {
			realized := node.Outputs[index]
			if resolved.SupplierComponent != realized.SupplierComponent || resolved.SupplierNode != realized.SupplierNode || resolved.Name != realized.Name || !reflect.DeepEqual(resolved.Candidate, realized.Candidate) {
				return nil, fmt.Errorf("build lock node %q output %d changed after resolution", node.NodeID, index)
			}
		}
		addStoreClosureReference(objects, node.BundleManifest)
		for _, artifact := range payload.Artifacts {
			if err := store.VerifyArtifact(artifact); err != nil {
				return nil, fmt.Errorf("verify build lock node %q artifact %q: %w", node.NodeID, artifact.LogicalPath, err)
			}
			reference, err := artifact.StoreObjectRef()
			if err != nil {
				return nil, err
			}
			addStoreClosureReference(objects, reference)
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
	return result, nil
}

func (lock *OperationLock) RemoveUnreachableBuildObjects(
	store providerstore.Store,
	build BuildLockV1,
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
	reachable, err := BuildLockStoreClosure(build, store, validateProfileOwner, validateBundleOwner)
	if err != nil {
		return err
	}
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
	if lock == nil {
		return fmt.Errorf("clean all build objects requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	if err := lock.validateProviderStoreLocked(store); err != nil {
		return err
	}
	return store.RemoveUnreachable([]providerstore.StoreObjectRef{})
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
