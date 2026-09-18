package dockerdeploy

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

// portablePythonAliasClaimV1 is the immutable identity retained for one
// published alias. A destination can be reused only when both fields match;
// the target by itself is not enough to establish ownership.
type portablePythonAliasClaimV1 struct {
	Target  string
	Binding string
}

// portablePythonAliasClaimsV1 is shared by one provider export transaction.
// Its keys are the accepted, absolute host destinations. A nil map disables
// replay deduplication, but it never makes a pre-existing destination owned.
type portablePythonAliasClaimsV1 map[string]portablePythonAliasClaimV1

var errPortablePythonAliasUnownedEntryV1 = errors.New("unowned Python alias staging entry")

const portablePythonAliasEntryPrefixV1 = "reploy-python-alias-target-v1:"

// These seams keep publication failure testable without replacing filesystem
// validation. Unix hosts stage a native symlink; Windows uses a private,
// regular-file record because native symlink creation may require privilege.
// The final archive always contains a Linux symlink with the recorded target.
var (
	portablePythonAliasCreateEntryV1 = createPortablePythonAliasEntryV1
	portablePythonAliasReadEntryV1   = readPortablePythonAliasEntryV1
	// portablePythonAliasOpenRootV1 is kept as a seam so the parent walk can
	// exercise a path replacement immediately after a component is opened.
	// The production implementation is os.Root.OpenRoot, whose returned root
	// holds the opened directory handle on supported hosts.
	portablePythonAliasOpenRootV1 = func(parent *os.Root, name string) (*os.Root, error) {
		return parent.OpenRoot(name)
	}
	portablePythonAliasPublishParentV1      = publishPortablePythonAliasNoReplaceV1
	portablePythonAliasInspectCreatedTempV1 = func(parent *os.Root, name string) (os.FileInfo, error) {
		return parent.Lstat(name)
	}
	portablePythonAliasInspectPublishedParentV1 = func(parent *os.Root, name string) (os.FileInfo, error) {
		return parent.Lstat(name)
	}
	portablePythonAliasPublishTempV1 = func(parent *os.Root, temporary, destination string) error {
		return publishPortablePythonAliasNoReplaceV1(parent, temporary, destination)
	}
	portablePythonAliasRemoveV1 = func(parent *os.Root, name string) error {
		return parent.Remove(name)
	}
)

// publishPortablePythonAliasV1 publishes one application-scoped Python CLI
// alias. stagingRoot is the provider-owned host root corresponding to the
// final image filesystem; destination must already have passed the selected
// export and filesystem domains and is checked again as a root-relative path.
// target is the canonical final-image absolute generated console-script path.
// bindingIdentity is opaque, exact identity from the selected binding records.
// The caller owns a fresh private staging workspace for this transaction and
// holds the deployment operation lock through publication and workspace
// removal. No other legitimate writer shares its entries. Identity checks
// reject observed replacements, but a pathname removal cannot be atomic with
// a prior identity check against an out-of-band process running as the same OS
// account; that process is outside the workspace ownership contract.
//
// The bool is true only when this call publishes a new alias. An exact claim
// and an already matching alias entry return false, nil. Errors leave no usable
// destination. Cleanup removes temporary names and directories only while their
// identity can be verified; an ambiguous entry is retained and reported in the
// error so workspace cleanup can remove it without risking an unowned entry.
func publishPortablePythonAliasV1(
	stagingRoot string,
	destination string,
	target string,
	bindingIdentity string,
	claims portablePythonAliasClaimsV1,
) (published bool, err error) {
	root, relativeDestination, err := openPortablePythonAliasStagingRootV1(stagingRoot, destination)
	if err != nil {
		return false, err
	}
	defer root.Close()

	if err := validatePortablePythonAliasTargetV1(target); err != nil {
		return false, err
	}
	if err := validatePortablePythonAliasBindingIdentityV1(bindingIdentity); err != nil {
		return false, err
	}

	claim, claimed := claims[destination]
	wantClaim := portablePythonAliasClaimV1{Target: target, Binding: bindingIdentity}
	if claimed && claim != wantClaim {
		return false, fmt.Errorf("Python alias destination %q is claimed by a different target or binding", destination)
	}

	parentRelative := filepath.Dir(relativeDestination)
	destinationName := filepath.Base(relativeDestination)
	if destinationName == "" || destinationName == "." || destinationName == string(filepath.Separator) {
		return false, fmt.Errorf("Python alias destination %q has no name", destination)
	}
	// Check the destination before creating any missing parent. Open the
	// existing parent with the same component walk used for publication. A
	// replacement during destination inspection cannot redirect the Lstat/read
	// through a symlink, and missing parents remain unmodified until after this
	// check.
	destinationParent, parentErr := openPortablePythonAliasParentPathNoFollowV1(root, parentRelative)
	if parentErr != nil && !errors.Is(parentErr, fs.ErrNotExist) {
		return false, fmt.Errorf("inspect Python alias destination parent %q: %w", parentRelative, parentErr)
	}
	defer func() {
		if destinationParent != nil {
			_ = destinationParent.Close()
		}
	}()
	var initialEntry os.FileInfo
	var statErr error
	if destinationParent == nil {
		statErr = fs.ErrNotExist
	} else {
		initialEntry, statErr = destinationParent.Lstat(destinationName)
	}
	switch {
	case statErr == nil:
		linkTarget, readErr := portablePythonAliasReadEntryV1(destinationParent, destinationName)
		if errors.Is(readErr, errPortablePythonAliasUnownedEntryV1) {
			return false, fmt.Errorf("Python alias destination %q is pre-existing and unowned", destination)
		}
		if readErr != nil {
			return false, fmt.Errorf("inspect existing Python alias destination %q: %w", destination, readErr)
		}
		if !claimed {
			return false, fmt.Errorf("Python alias destination %q is pre-existing and unowned", destination)
		}
		linkTarget = normalizePortablePythonAliasTargetV1(linkTarget)
		if linkTarget != target {
			return false, fmt.Errorf("Python alias destination %q resolves to %q, want %q", destination, linkTarget, target)
		}
		currentEntry, currentErr := destinationParent.Lstat(destinationName)
		if currentErr != nil {
			return false, fmt.Errorf("reinspect existing Python alias destination %q: %w", destination, currentErr)
		}
		if !os.SameFile(initialEntry, currentEntry) {
			return false, fmt.Errorf("existing Python alias destination %q changed identity during replay", destination)
		}
		if verifyErr := verifyPortablePythonAliasPathIdentityV1(stagingRoot, root, parentRelative, destinationParent); verifyErr != nil {
			return false, fmt.Errorf("verify existing Python alias %q path: %w", destination, verifyErr)
		}
		return false, nil
	case !errors.Is(statErr, fs.ErrNotExist):
		return false, fmt.Errorf("inspect Python alias destination %q: %w", destination, statErr)
	case claimed:
		return false, fmt.Errorf("Python alias destination %q has an unpublished claim", destination)
	}

	parent := destinationParent
	createdParents := []portablePythonAliasCreatedParentV1(nil)
	if parent == nil {
		parent, createdParents, err = preparePortablePythonAliasParentV1(root, parentRelative)
		if err != nil {
			return false, err
		}
		destinationParent = nil
	} else {
		// Transfer ownership to the publication path. The deferred close above
		// must not close the held parent while it is still in use.
		destinationParent = nil
	}
	defer func() {
		if err == nil {
			return
		}
		for index := len(createdParents) - 1; index >= 0; index-- {
			// Remove only empty directories created by this call. Reopen the
			// parent with the same component walk so cleanup cannot resolve a
			// path through a parent replaced by a symlink.
			if removeErr := removePortablePythonAliasCreatedParentV1(root, createdParents[index]); removeErr != nil {
				err = errors.Join(err, removeErr)
			}
		}
	}()
	defer func() {
		if parent != nil {
			if closeErr := parent.Close(); closeErr != nil && err != nil {
				err = errors.Join(err, fmt.Errorf("close Python alias destination parent: %w", closeErr))
			}
		}
	}()

	temporary, err := createPortablePythonAliasTemporaryV1(parent, target)
	if err != nil {
		return false, err
	}
	var temporaryInfo os.FileInfo
	temporaryLive := true
	defer func() {
		if temporaryLive {
			if temporaryInfo == nil {
				err = errors.Join(err, fmt.Errorf("cannot safely remove temporary Python alias %q: ownership identity unavailable", temporary))
				return
			}
			if removeErr := removePortablePythonAliasOwnedEntryV1(parent, temporary, temporaryInfo); removeErr != nil {
				err = errors.Join(err, fmt.Errorf("remove temporary Python alias %q: %w", temporary, removeErr))
			}
		}
	}()
	temporaryInfo, err = portablePythonAliasInspectCreatedTempV1(parent, temporary)
	if err != nil {
		return false, fmt.Errorf("inspect created temporary Python alias %q: %w", temporary, err)
	}

	if linkTarget, readErr := portablePythonAliasReadEntryV1(parent, temporary); readErr != nil || normalizePortablePythonAliasTargetV1(linkTarget) != target {
		if readErr != nil {
			return false, fmt.Errorf("verify temporary Python alias target %q: %w", destination, readErr)
		}
		linkTarget = normalizePortablePythonAliasTargetV1(linkTarget)
		return false, fmt.Errorf("temporary Python alias target %q changed to %q", destination, linkTarget)
	}
	verifiedInfo, err := parent.Lstat(temporary)
	if err != nil {
		return false, fmt.Errorf("inspect temporary Python alias %q: %w", temporary, err)
	}
	if !os.SameFile(verifiedInfo, temporaryInfo) {
		return false, fmt.Errorf("temporary Python alias %q changed identity before publication", temporary)
	}
	if err := portablePythonAliasPublishTempV1(parent, temporary, destinationName); err != nil {
		return false, fmt.Errorf("publish Python alias %q without replacement: %w", destination, err)
	}
	temporaryLive = false

	// Re-read the alias entry from the held parent handle. The payload is deliberately
	// an absolute final-image path; resolving it through the host would add the
	// staging prefix and would be the wrong namespace to compare.
	linkTarget, readErr := portablePythonAliasReadEntryV1(parent, destinationName)
	if readErr != nil {
		removeErr := removePortablePythonAliasDestinationAfterFailureV1(parent, destinationName, temporaryInfo)
		return false, errors.Join(
			fmt.Errorf("resolve published Python alias %q: %w", destination, readErr),
			removeErr,
		)
	}
	linkTarget = normalizePortablePythonAliasTargetV1(linkTarget)
	if linkTarget != target {
		removeErr := removePortablePythonAliasDestinationAfterFailureV1(parent, destinationName, temporaryInfo)
		return false, errors.Join(
			fmt.Errorf("published Python alias %q resolves to %q, want %q", destination, linkTarget, target),
			removeErr,
		)
	}
	publishedInfo, identityErr := parent.Lstat(destinationName)
	if identityErr == nil && !os.SameFile(publishedInfo, temporaryInfo) {
		identityErr = fmt.Errorf("published destination changed identity")
	}
	if identityErr != nil {
		removeErr := removePortablePythonAliasDestinationAfterFailureV1(parent, destinationName, temporaryInfo)
		return false, errors.Join(fmt.Errorf("verify published Python alias %q identity: %w", destination, identityErr), removeErr)
	}
	if pathErr := verifyPortablePythonAliasPathIdentityV1(stagingRoot, root, parentRelative, parent); pathErr != nil {
		removeErr := removePortablePythonAliasDestinationAfterFailureV1(parent, destinationName, temporaryInfo)
		return false, errors.Join(fmt.Errorf("verify published Python alias %q path: %w", destination, pathErr), removeErr)
	}
	if claims != nil {
		claims[destination] = wantClaim
	}
	return true, nil
}

// Held roots prevent redirecting operations, while this check ensures that
// their original absolute pathname still reaches those same directories.
func verifyPortablePythonAliasPathIdentityV1(stagingRoot string, root *os.Root, parentRelative string, parent *os.Root) error {
	currentParent, err := openPortablePythonAliasParentPathNoFollowV1(root, parentRelative)
	if err != nil {
		return err
	}
	heldInfo, heldErr := parent.Stat(".")
	currentInfo, currentErr := currentParent.Stat(".")
	closeErr := currentParent.Close()
	if err := errors.Join(heldErr, currentErr, closeErr); err != nil {
		return err
	}
	if !os.SameFile(heldInfo, currentInfo) {
		return fmt.Errorf("destination parent changed identity")
	}
	openedRoot, err := root.Stat(".")
	if err != nil {
		return err
	}
	pathRoot, err := os.Lstat(stagingRoot)
	if err != nil {
		return err
	}
	if !pathRoot.IsDir() || pathRoot.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedRoot, pathRoot) {
		return fmt.Errorf("staging root changed identity")
	}
	return nil
}

// removePortablePythonAliasDestinationAfterFailureV1 attempts to remove the
// destination after publication has become unverifiable. A failed removal is
// part of the returned error because the destination may still be a usable
// alias and the caller must destroy the private staging workspace.
func removePortablePythonAliasDestinationAfterFailureV1(parent *os.Root, destination string, published os.FileInfo) error {
	if err := removePortablePythonAliasOwnedEntryV1(parent, destination, published); err != nil {
		return fmt.Errorf("remove published Python alias destination %q after publication failure: %w", destination, err)
	}
	return nil
}

func removePortablePythonAliasOwnedEntryV1(parent *os.Root, name string, published os.FileInfo) error {
	// This is an ownership check within the caller's exclusive staging
	// workspace, not an atomic conditional unlink against a rogue same-user
	// process. The transaction removes the whole workspace on failure.
	current, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Python alias entry %q before cleanup: %w", name, err)
	}
	if !os.SameFile(current, published) {
		return fmt.Errorf("Python alias entry %q changed identity before cleanup", name)
	}
	if err := portablePythonAliasRemoveV1(parent, name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func openPortablePythonAliasStagingRootV1(stagingRoot, destination string) (*os.Root, string, error) {
	if stagingRoot == "" || !filepath.IsAbs(stagingRoot) || filepath.Clean(stagingRoot) != stagingRoot {
		return nil, "", fmt.Errorf("Python alias staging root must be an absolute clean path")
	}
	rootInfo, err := os.Lstat(stagingRoot)
	if err != nil {
		return nil, "", fmt.Errorf("inspect Python alias staging root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("Python alias staging root must be a real directory: %s", stagingRoot)
	}
	if destination == "" || !utf8.ValidString(destination) || strings.ContainsRune(destination, '\x00') || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return nil, "", fmt.Errorf("Python alias destination must be an absolute clean path")
	}
	relative, err := filepath.Rel(stagingRoot, destination)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, "", fmt.Errorf("Python alias destination %q escapes staging root %q", destination, stagingRoot)
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return nil, "", fmt.Errorf("Python alias destination %q has an unsafe relative path", destination)
		}
	}
	root, err := os.OpenRoot(stagingRoot)
	if err != nil {
		return nil, "", fmt.Errorf("open Python alias staging root: %w", err)
	}
	opened, openErr := root.Stat(".")
	current, currentErr := os.Lstat(stagingRoot)
	if openErr != nil || currentErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		_ = root.Close()
		if openErr != nil {
			return nil, "", fmt.Errorf("inspect opened Python alias staging root: %w", openErr)
		}
		if currentErr != nil {
			return nil, "", fmt.Errorf("reinspect Python alias staging root: %w", currentErr)
		}
		return nil, "", fmt.Errorf("Python alias staging root changed identity while opening: %s", stagingRoot)
	}
	return root, relative, nil
}

func validatePortablePythonAliasBindingIdentityV1(identity string) error {
	if identity == "" || !utf8.ValidString(identity) || strings.ContainsRune(identity, '\x00') {
		return fmt.Errorf("Python alias binding identity must be nonempty valid text")
	}
	return nil
}

// normalizePortablePythonAliasTargetV1 converts a host representation of a
// final-image Linux path back to the canonical slash form. Windows turns the
// slash separators in a symlink payload into backslashes when it is created
// and returns those backslashes from Readlink, even though the payload is
// destined for a Linux image archive.
func normalizePortablePythonAliasTargetV1(target string) string {
	return strings.ReplaceAll(target, `\`, "/")
}

func validatePortablePythonAliasTargetV1(target string) error {
	if target == "" || !utf8.ValidString(target) || strings.ContainsRune(target, '\x00') || strings.ContainsRune(target, '\\') || !path.IsAbs(target) || path.Clean(target) != target {
		return fmt.Errorf("Python alias target must be an absolute clean final-image path")
	}
	installRoot := path.Clean(pythonprovider.InstallRoot)
	if target == installRoot || !strings.HasPrefix(target, installRoot+"/") {
		return fmt.Errorf("Python alias target %q is outside the Python provider install root", target)
	}
	suffix := strings.TrimPrefix(target, installRoot+"/")
	parts := strings.Split(suffix, "/")
	if len(parts) < 3 || parts[len(parts)-2] != "bin" || parts[len(parts)-1] == "" || parts[len(parts)-1] == "." || parts[len(parts)-1] == ".." {
		return fmt.Errorf("Python alias target %q is not a generated Python console script", target)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("Python alias target %q has an unsafe runtime path", target)
		}
	}
	// The staging root is an alias overlay and may not contain the generated
	// script itself. Final-image probing belongs to the owning materialization
	// transaction; this primitive proves the target's canonical path shape and
	// verifies the exact payload after publication.
	return nil
}

type portablePythonAliasCreatedParentV1 struct {
	path     string
	identity os.FileInfo
	parent   os.FileInfo
}

func preparePortablePythonAliasParentV1(root *os.Root, relative string) (*os.Root, []portablePythonAliasCreatedParentV1, error) {
	if relative == "" {
		return nil, nil, fmt.Errorf("Python alias destination parent is unavailable")
	}
	parentRelative := filepath.Clean(relative)
	parts := []string{}
	if parentRelative != "." {
		parts = strings.Split(filepath.ToSlash(parentRelative), "/")
	}

	// Every component is opened relative to the already-held directory for its
	// parent. A path-wide OpenRoot would follow a component replaced by a
	// symlink between destination inspection and publication.
	// Keeping each opened child root also makes the subsequent publication
	// operations independent of later renames or replacements in the path.
	type createdParent struct {
		parent   *os.Root
		name     string
		path     string
		identity os.FileInfo
	}
	created := []createdParent{}
	owned := []*os.Root{}
	current := root
	var walkErr error
	defer func() {
		if walkErr == nil {
			return
		}
		for index := len(created) - 1; index >= 0; index-- {
			// The parent handle is held from the component walk, so rollback
			// does not resolve the path through a potentially swapped parent.
			entry, statErr := created[index].parent.Lstat(created[index].name)
			if statErr == nil && os.SameFile(entry, created[index].identity) {
				_ = created[index].parent.Remove(created[index].name)
			}
		}
		if current != root {
			_ = current.Close()
		}
		for _, held := range owned {
			_ = held.Close()
		}
	}()
	if len(parts) == 0 {
		child, err := openPortablePythonAliasParentComponentV1(root, ".")
		if err != nil {
			walkErr = fmt.Errorf("open Python alias destination parent %q without following links: %w", parentRelative, err)
			return nil, nil, walkErr
		}
		return child, nil, nil
	}

	for index, name := range parts {
		candidate := filepath.FromSlash(strings.Join(parts[:index+1], "/"))
		child, err := openPortablePythonAliasParentComponentV1(current, name)
		createdHere := false
		if errors.Is(err, fs.ErrNotExist) {
			child, err = createPortablePythonAliasParentNoReplaceV1(current, name)
			createdHere = err == nil
		}
		if err != nil {
			walkErr = fmt.Errorf("open Python alias destination parent %q without following links: %w", candidate, err)
			return nil, nil, walkErr
		}
		if createdHere {
			identity, statErr := child.Stat(".")
			if statErr != nil {
				_ = child.Close()
				walkErr = fmt.Errorf("inspect created Python alias parent %q: %w", candidate, statErr)
				return nil, nil, walkErr
			}
			created = append(created, createdParent{parent: current, name: name, path: candidate, identity: identity})
		}
		if current != root {
			owned = append(owned, current)
		}
		current = child
	}

	resultCreated := make([]portablePythonAliasCreatedParentV1, len(created))
	for index := range created {
		parentInfo, statErr := created[index].parent.Stat(".")
		if statErr != nil {
			_ = current.Close()
			walkErr = fmt.Errorf("inspect Python alias parent of %q: %w", created[index].path, statErr)
			return nil, nil, walkErr
		}
		resultCreated[index] = portablePythonAliasCreatedParentV1{path: created[index].path, identity: created[index].identity, parent: parentInfo}
	}
	chain := append(append([]*os.Root(nil), owned...), current)
	if verifyErr := verifyPortablePythonAliasHeldParentChainV1(root, parts, chain); verifyErr != nil {
		walkErr = fmt.Errorf("verify Python alias destination parent %q: %w", parentRelative, verifyErr)
		return nil, nil, walkErr
	}
	for _, held := range owned {
		if closeErr := held.Close(); closeErr != nil {
			_ = current.Close()
			walkErr = fmt.Errorf("close Python alias destination parent %q: %w", parentRelative, closeErr)
			return nil, nil, walkErr
		}
	}
	owned = nil
	return current, resultCreated, nil
}

// createPortablePythonAliasParentNoReplaceV1 prepares a missing parent under a
// private unpredictable name. Its opened directory handle is the ownership
// identity: the final name is installed with a no-replace rename, then checked
// against that held handle. A directory inserted at the final name between the
// missing-parent check and publication is never accepted or overwritten.
func createPortablePythonAliasParentNoReplaceV1(parent *os.Root, name string) (*os.Root, error) {
	for attempt := 0; attempt < 32; attempt++ {
		temporary, err := portablePythonAliasTemporaryNameV1()
		if err != nil {
			return nil, err
		}
		if err := parent.Mkdir(temporary, 0o700); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return nil, fmt.Errorf("create private Python alias parent %q: %w", name, err)
		}
		published := false
		cleanup := func(child *os.Root, cause error) (*os.Root, error) {
			var owned os.FileInfo
			if child != nil {
				owned, _ = child.Stat(".")
				cause = errors.Join(cause, child.Close())
			}
			if owned != nil {
				cleanupName := temporary
				if published {
					cleanupName = name
				}
				if removeErr := removePortablePythonAliasOwnedEntryV1(parent, cleanupName, owned); removeErr != nil {
					cause = errors.Join(cause, fmt.Errorf("remove private Python alias parent %q: %w", cleanupName, removeErr))
				}
			}
			return nil, cause
		}
		child, err := openPortablePythonAliasParentComponentV1(parent, temporary)
		if err != nil {
			return cleanup(nil, fmt.Errorf("open private Python alias parent %q: %w", temporary, err))
		}
		if err := child.Chmod(".", 0o755); err != nil {
			return cleanup(child, fmt.Errorf("set Python alias parent %q mode: %w", name, err))
		}
		opened, err := child.Stat(".")
		if err != nil {
			return cleanup(child, fmt.Errorf("inspect private Python alias parent %q: %w", temporary, err))
		}
		if err := portablePythonAliasPublishParentV1(parent, temporary, name); err != nil {
			return cleanup(child, fmt.Errorf("publish Python alias parent %q without replacement: %w", name, err))
		}
		published = true
		entry, err := portablePythonAliasInspectPublishedParentV1(parent, name)
		if err != nil {
			return cleanup(child, fmt.Errorf("inspect published Python alias parent %q: %w", name, err))
		}
		if !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 || !os.SameFile(entry, opened) {
			return cleanup(child, fmt.Errorf("Python alias parent %q changed identity during publication", name))
		}
		return child, nil
	}
	return nil, fmt.Errorf("create private Python alias parent %q: exhausted private names", name)
}

// openPortablePythonAliasParentComponentV1 opens one existing directory
// component and verifies that the path entry still names that exact directory.
// Opening first is deliberate: if the entry is swapped between a pre-check and
// the open, the held child root is compared against the post-open Lstat and is
// rejected before any write can use it. Once accepted, operations use the held
// child root and are unaffected by later path replacements.
func openPortablePythonAliasParentComponentV1(parent *os.Root, name string) (*os.Root, error) {
	return openPortablePythonAliasParentComponentWithIdentityV1(parent, name, nil)
}

func openPortablePythonAliasParentComponentWithIdentityV1(parent *os.Root, name string, expected os.FileInfo) (*os.Root, error) {
	if parent == nil {
		return nil, fmt.Errorf("Python alias parent handle is unavailable")
	}
	child, err := portablePythonAliasOpenRootV1(parent, name)
	if err != nil {
		// os.Root rejects absolute links before returning a child root. Keep the
		// ownership error stable by identifying that case without following the
		// entry; no path-based operation is performed after this check.
		if entry, statErr := parent.Lstat(name); statErr == nil && entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Python alias destination parent %q must be a real directory", name)
		}
		return nil, err
	}
	closeChild := func(cause error) (*os.Root, error) {
		if closeErr := child.Close(); closeErr != nil {
			cause = errors.Join(cause, fmt.Errorf("close Python alias parent component %q: %w", name, closeErr))
		}
		return nil, cause
	}
	entry, err := parent.Lstat(name)
	if err != nil {
		return closeChild(fmt.Errorf("inspect Python alias parent component %q: %w", name, err))
	}
	opened, err := child.Stat(".")
	if err != nil {
		return closeChild(fmt.Errorf("inspect opened Python alias parent component %q: %w", name, err))
	}
	if entry.Mode()&os.ModeSymlink != 0 || !entry.IsDir() {
		return closeChild(fmt.Errorf("Python alias destination parent %q must be a real directory", name))
	}
	if !os.SameFile(entry, opened) {
		return closeChild(fmt.Errorf("Python alias destination parent %q changed identity while opening", name))
	}
	if expected != nil && !os.SameFile(expected, opened) {
		return closeChild(fmt.Errorf("created Python alias destination parent %q changed identity while opening", name))
	}
	return child, nil
}

func openPortablePythonAliasParentPathNoFollowV1(root *os.Root, relative string) (*os.Root, error) {
	if root == nil {
		return nil, fmt.Errorf("Python alias staging root handle is unavailable")
	}
	relative = filepath.Clean(relative)
	if relative == "." {
		return openPortablePythonAliasParentComponentV1(root, ".")
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	current := root
	children := make([]*os.Root, 0, len(parts))
	closeChildren := func() {
		for index := len(children) - 1; index >= 0; index-- {
			_ = children[index].Close()
		}
	}
	for _, name := range parts {
		child, err := openPortablePythonAliasParentComponentV1(current, name)
		if err != nil {
			closeChildren()
			return nil, err
		}
		children = append(children, child)
		current = child
	}
	if err := verifyPortablePythonAliasHeldParentChainV1(root, parts, children); err != nil {
		closeChildren()
		return nil, err
	}
	for _, intermediate := range children[:len(children)-1] {
		if err := intermediate.Close(); err != nil {
			_ = current.Close()
			return nil, err
		}
	}
	return current, nil
}

func verifyPortablePythonAliasHeldParentChainV1(root *os.Root, parts []string, children []*os.Root) error {
	if len(parts) != len(children) {
		return fmt.Errorf("Python alias parent chain length mismatch")
	}
	parent := root
	for index, name := range parts {
		entry, entryErr := parent.Lstat(name)
		opened, openedErr := children[index].Stat(".")
		if err := errors.Join(entryErr, openedErr); err != nil {
			return fmt.Errorf("reinspect Python alias parent component %q: %w", name, err)
		}
		if !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 || !os.SameFile(entry, opened) {
			return fmt.Errorf("Python alias parent component %q changed identity during walk", name)
		}
		parent = children[index]
	}
	return nil
}

func removePortablePythonAliasCreatedParentV1(root *os.Root, created portablePythonAliasCreatedParentV1) error {
	parentRelative := filepath.Dir(created.path)
	name := filepath.Base(created.path)
	parent, err := openPortablePythonAliasParentPathNoFollowV1(root, parentRelative)
	if err != nil {
		return err
	}
	defer parent.Close()
	parentInfo, err := parent.Stat(".")
	if err != nil {
		return err
	}
	entry, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(parentInfo, created.parent) || !os.SameFile(entry, created.identity) {
		return fmt.Errorf("created Python alias parent %q changed identity before rollback", created.path)
	}
	return parent.Remove(name)
}

func createPortablePythonAliasTemporaryV1(parent *os.Root, target string) (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		name, err := portablePythonAliasTemporaryNameV1()
		if err != nil {
			return "", err
		}
		if err := portablePythonAliasCreateEntryV1(parent, target, name); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return "", fmt.Errorf("create temporary Python alias: %w", err)
		}
		return name, nil
	}
	return "", fmt.Errorf("create temporary Python alias: exhausted private names")
}

func parsePortablePythonAliasEntryV1(data []byte) (string, error) {
	target, ok := strings.CutPrefix(string(data), portablePythonAliasEntryPrefixV1)
	if !ok || target == "" {
		return "", errPortablePythonAliasUnownedEntryV1
	}
	return target, nil
}

func portablePythonAliasTemporaryNameV1() (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate temporary Python alias name: %w", err)
	}
	return fmt.Sprintf(".reploy-python-alias-%x", bytes[:]), nil
}
