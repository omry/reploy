package providerstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
)

type storedObject struct {
	reference StoreObjectRef
	path      string
}

type objectLayout struct {
	kind      string
	directory string
	extension string
}

var providerStoreObjectLayouts = []objectLayout{
	{kind: BlobKind, directory: "blobs"},
	{kind: BundleManifestKind, directory: "manifests", extension: ".json"},
	{kind: ValidationRecordKind, directory: "validation", extension: ".json"},
}

var artifactVerificationLayout = objectLayout{
	kind: BlobKind, directory: artifactVerificationDir, extension: ".json",
}

// RemoveTemporaryEntries empties only the deployment-owned scratch directory.
// Callers hold the deployment operation lock and establish that no active or
// recoverable publication can still own these entries.
func (store Store) RemoveTemporaryEntries() error {
	if _, err := os.Lstat(store.root); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect provider store root: %w", err)
	}
	if err := requireRealDirectory(filepath.Dir(store.root)); err != nil {
		return err
	}
	if err := requireRealDirectory(store.root); err != nil {
		return err
	}
	temporaryRoot := filepath.Join(store.root, "tmp")
	if _, err := os.Lstat(temporaryRoot); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect provider store temporary directory: %w", err)
	}
	if err := requireRealDirectory(temporaryRoot); err != nil {
		return err
	}
	entries, err := os.ReadDir(temporaryRoot)
	if err != nil {
		return fmt.Errorf("read provider store temporary directory: %w", err)
	}
	temporary, err := os.OpenRoot(temporaryRoot)
	if err != nil {
		return fmt.Errorf("open provider store temporary directory: %w", err)
	}
	defer temporary.Close()
	sort.Slice(entries, func(left int, right int) bool { return entries[left].Name() < entries[right].Name() })
	for _, entry := range entries {
		if err := makeTemporaryEntryRemovable(temporary, entry.Name()); err != nil {
			if errors.Is(err, fs.ErrPermission) {
				path := filepath.Join(temporaryRoot, entry.Name())
				return temporaryWorkspacePermissionError(path, err)
			}
			return fmt.Errorf("prepare abandoned provider store temporary entry %q for removal: %w", entry.Name(), err)
		}
		if err := temporary.RemoveAll(entry.Name()); err != nil {
			return fmt.Errorf("remove abandoned provider store temporary entry %q: %w", entry.Name(), err)
		}
	}
	if len(entries) != 0 {
		return syncStoreDirectory(temporaryRoot)
	}
	return nil
}

func temporaryWorkspacePermissionError(path string, err error) error {
	return fmt.Errorf(
		"abandoned provider workspace %q cannot be removed by the current host user: %w\n"+
			"this can follow an interrupted Docker operation\n"+
			"next: restore ownership of that workspace and its contents to the current user, or remove it, then rerun the command",
		path, err,
	)
}

func makeTemporaryEntryRemovable(temporary *os.Root, name string) error {
	return fs.WalkDir(temporary.FS(), name, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if err := temporary.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("make temporary directory %q removable: %w", path, err)
		}
		return nil
	})
}

// RemoveUnreachable removes only recognized immutable objects not present in
// the supplied closure. Callers hold the deployment operation lock and derive
// reachable from the selected build lock before invoking this method.
func (store Store) RemoveUnreachable(reachable []StoreObjectRef) error {
	if err := validateReachableObjectClosure(reachable); err != nil {
		return err
	}
	if _, err := os.Lstat(store.root); errors.Is(err, fs.ErrNotExist) {
		if len(reachable) == 0 {
			return nil
		}
		return fmt.Errorf("provider store is missing reachable objects")
	} else if err != nil {
		return fmt.Errorf("inspect provider store root: %w", err)
	}
	if err := requireRealDirectory(filepath.Dir(store.root)); err != nil {
		return err
	}
	if err := requireRealDirectory(store.root); err != nil {
		return err
	}

	stored := []storedObject{}
	for _, layout := range providerStoreObjectLayouts {
		objects, err := store.collectLayoutObjects(layout)
		if err != nil {
			return err
		}
		stored = append(stored, objects...)
	}
	verifications, err := store.collectLayoutObjects(artifactVerificationLayout)
	if err != nil {
		return err
	}
	storedByKey := make(map[string]storedObject, len(stored))
	for _, object := range stored {
		storedByKey[storeObjectKey(object.reference)] = object
	}
	for _, reference := range reachable {
		if _, found := storedByKey[storeObjectKey(reference)]; !found {
			return fmt.Errorf("reachable provider store object %s %s is missing", reference.Kind, reference.Digest)
		}
	}
	reachableKeys := make(map[string]bool, len(reachable))
	for _, reference := range reachable {
		reachableKeys[storeObjectKey(reference)] = true
	}
	for _, object := range stored {
		if reachableKeys[storeObjectKey(object.reference)] {
			continue
		}
		if err := os.Remove(object.path); err != nil {
			return fmt.Errorf("remove unreachable provider store object: %w", err)
		}
		if err := syncStoreDirectory(filepath.Dir(object.path)); err != nil {
			return err
		}
	}
	if err := store.removeUnreachableArtifactVerifications(verifications, reachableKeys); err != nil {
		return err
	}
	return store.removeEmptyObjectDirectories()
}

func (store Store) removeUnreachableArtifactVerifications(
	verifications []storedObject,
	reachableKeys map[string]bool,
) error {
	for _, verification := range verifications {
		if reachableKeys[storeObjectKey(verification.reference)] {
			continue
		}
		if err := os.Remove(verification.path); err != nil {
			return fmt.Errorf("remove unreachable artifact verification: %w", err)
		}
		if err := syncStoreDirectory(filepath.Dir(verification.path)); err != nil {
			return err
		}
	}
	return nil
}

func validateReachableObjectClosure(reachable []StoreObjectRef) error {
	if reachable == nil {
		return fmt.Errorf("reachable provider store objects must use an array")
	}
	for index, reference := range reachable {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("reachable provider store object %d: %w", index, err)
		}
		if index > 0 && compareStoreObjectReferences(reachable[index-1], reference) >= 0 {
			return fmt.Errorf("reachable provider store objects must be unique and sorted by kind and digest")
		}
	}
	return nil
}

func compareStoreObjectReferences(left StoreObjectRef, right StoreObjectRef) int {
	if left.Kind != right.Kind {
		return strings.Compare(left.Kind, right.Kind)
	}
	return strings.Compare(string(left.Digest), string(right.Digest))
}

func storeObjectKey(reference StoreObjectRef) string {
	return reference.Kind + "\x00" + string(reference.Digest)
}

func (store Store) collectLayoutObjects(layout objectLayout) ([]storedObject, error) {
	category := filepath.Join(store.root, layout.directory)
	if _, err := os.Lstat(category); errors.Is(err, fs.ErrNotExist) {
		return []storedObject{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect provider store %s directory: %w", layout.kind, err)
	}
	if err := requireRealDirectory(category); err != nil {
		return nil, err
	}
	categoryEntries, err := os.ReadDir(category)
	if err != nil {
		return nil, fmt.Errorf("read provider store %s directory: %w", layout.kind, err)
	}
	if len(categoryEntries) != 1 || categoryEntries[0].Name() != "sha256" || !categoryEntries[0].IsDir() || categoryEntries[0].Type()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("provider store %s directory has an unrecognized layout", layout.kind)
	}
	algorithm := filepath.Join(category, "sha256")
	if err := requireRealDirectory(algorithm); err != nil {
		return nil, err
	}
	prefixEntries, err := os.ReadDir(algorithm)
	if err != nil {
		return nil, fmt.Errorf("read provider store %s prefixes: %w", layout.kind, err)
	}
	objects := []storedObject{}
	for _, prefixEntry := range prefixEntries {
		prefix := prefixEntry.Name()
		if len(prefix) != 2 || !lowerHex(prefix) || !prefixEntry.IsDir() || prefixEntry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("provider store %s has unrecognized prefix %q", layout.kind, prefix)
		}
		prefixPath := filepath.Join(algorithm, prefix)
		if err := requireRealDirectory(prefixPath); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(prefixPath)
		if err != nil {
			return nil, fmt.Errorf("read provider store %s prefix %q: %w", layout.kind, prefix, err)
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() {
				return nil, fmt.Errorf("provider store %s object must be a regular file: %s", layout.kind, filepath.Join(prefixPath, entry.Name()))
			}
			hex := strings.TrimSuffix(entry.Name(), layout.extension)
			if (layout.extension != "" && hex == entry.Name()) || len(hex) != 64 || !lowerHex(hex) || !strings.HasPrefix(hex, prefix) {
				return nil, fmt.Errorf("provider store %s has unrecognized object name %q", layout.kind, entry.Name())
			}
			digest := canonical.Digest("sha256:" + hex)
			objects = append(objects, storedObject{
				reference: StoreObjectRef{Kind: layout.kind, Digest: digest},
				path:      filepath.Join(prefixPath, entry.Name()),
			})
		}
	}
	return objects, nil
}

func lowerHex(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return value != ""
}

func (store Store) removeEmptyObjectDirectories() error {
	layouts := append(append([]objectLayout{}, providerStoreObjectLayouts...), artifactVerificationLayout)
	for _, layout := range layouts {
		algorithm := filepath.Join(store.root, layout.directory, "sha256")
		entries, err := os.ReadDir(algorithm)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read provider store cleanup directory: %w", err)
		}
		sort.Slice(entries, func(left int, right int) bool { return entries[left].Name() < entries[right].Name() })
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(algorithm, entry.Name())
			children, err := os.ReadDir(path)
			if err != nil {
				return fmt.Errorf("read provider store prefix during cleanup: %w", err)
			}
			if len(children) != 0 {
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove empty provider store prefix: %w", err)
			}
			if err := syncStoreDirectory(algorithm); err != nil {
				return err
			}
		}
	}
	return nil
}
