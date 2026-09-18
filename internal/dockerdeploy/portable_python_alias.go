package dockerdeploy

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
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
//
// The bool is true only when this call publishes a new alias. An exact claim
// and an already matching alias entry return false, nil. Errors leave no usable
// destination and remove temporary names and directories created by this call.
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
	if err := validatePortablePythonAliasExistingParentsV1(root, parentRelative); err != nil {
		return false, err
	}

	// Check the destination before creating any missing parent. This keeps a
	// rejected export from changing the provider-owned staging tree.
	_, statErr := root.Lstat(relativeDestination)
	switch {
	case statErr == nil:
		linkTarget, readErr := portablePythonAliasReadEntryV1(root, relativeDestination)
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
		return false, nil
	case !errors.Is(statErr, fs.ErrNotExist):
		return false, fmt.Errorf("inspect Python alias destination %q: %w", destination, statErr)
	case claimed:
		return false, fmt.Errorf("Python alias destination %q has an unpublished claim", destination)
	}

	parent, createdParents, err := preparePortablePythonAliasParentV1(root, parentRelative)
	if err != nil {
		return false, err
	}
	defer func() {
		if err == nil {
			return
		}
		for index := len(createdParents) - 1; index >= 0; index-- {
			// Remove only empty directories created by this call. Root.Remove is
			// relative to the held staging root and cannot follow an alias.
			_ = root.Remove(createdParents[index])
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
	temporaryLive := true
	defer func() {
		if temporaryLive {
			if removeErr := portablePythonAliasRemoveV1(parent, temporary); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove temporary Python alias %q: %w", temporary, removeErr))
			}
		}
	}()

	if linkTarget, readErr := portablePythonAliasReadEntryV1(parent, temporary); readErr != nil || normalizePortablePythonAliasTargetV1(linkTarget) != target {
		if readErr != nil {
			return false, fmt.Errorf("verify temporary Python alias target %q: %w", destination, readErr)
		}
		linkTarget = normalizePortablePythonAliasTargetV1(linkTarget)
		return false, fmt.Errorf("temporary Python alias target %q changed to %q", destination, linkTarget)
	}
	if err := portablePythonAliasPublishTempV1(parent, temporary, destinationName); err != nil {
		return false, fmt.Errorf("publish Python alias %q without replacement: %w", destination, err)
	}
	temporaryLive = false

	// The destination now owns the private alias entry. If cleanup of the temporary
	// name fails, remove the destination again before returning an error so a
	// failed publication cannot expose a usable alias.
	if err := portablePythonAliasRemoveV1(parent, temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
		removeErr := portablePythonAliasRemoveV1(parent, destinationName)
		return false, errors.Join(
			fmt.Errorf("remove temporary Python alias %q: %w", destination, err),
			removeErr,
		)
	}

	// Re-read the alias entry from the held parent handle. The payload is deliberately
	// an absolute final-image path; resolving it through the host would add the
	// staging prefix and would be the wrong namespace to compare.
	linkTarget, err := portablePythonAliasReadEntryV1(parent, destinationName)
	if err != nil {
		_ = portablePythonAliasRemoveV1(parent, destinationName)
		return false, fmt.Errorf("resolve published Python alias %q: %w", destination, err)
	}
	linkTarget = normalizePortablePythonAliasTargetV1(linkTarget)
	if linkTarget != target {
		_ = portablePythonAliasRemoveV1(parent, destinationName)
		return false, fmt.Errorf("published Python alias %q resolves to %q, want %q", destination, linkTarget, target)
	}
	if claims != nil {
		claims[destination] = wantClaim
	}
	return true, nil
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

func validatePortablePythonAliasExistingParentsV1(root *os.Root, relative string) error {
	if relative == "." {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for index := range parts {
		candidate := filepath.FromSlash(strings.Join(parts[:index+1], "/"))
		info, err := root.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect Python alias destination parent %q: %w", candidate, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Python alias destination parent %q must be a real directory", candidate)
		}
	}
	return nil
}

func preparePortablePythonAliasParentV1(root *os.Root, relative string) (*os.Root, []string, error) {
	if relative == "" {
		return nil, nil, fmt.Errorf("Python alias destination parent is unavailable")
	}
	parentRelative := filepath.Clean(relative)
	parts := []string{}
	if parentRelative != "." {
		parts = strings.Split(filepath.ToSlash(parentRelative), "/")
	}
	created := []string{}
	rollbackParents := func() {
		for index := len(created) - 1; index >= 0; index-- {
			_ = root.Remove(created[index])
		}
	}
	for index := range parts {
		candidate := filepath.FromSlash(strings.Join(parts[:index+1], "/"))
		info, err := root.Lstat(candidate)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				rollbackParents()
				return nil, nil, fmt.Errorf("Python alias destination parent %q must be a real directory", candidate)
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			rollbackParents()
			return nil, nil, fmt.Errorf("inspect Python alias destination parent %q: %w", candidate, err)
		}
		mkdirErr := root.Mkdir(candidate, 0o755)
		if mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
			rollbackParents()
			return nil, nil, fmt.Errorf("create Python alias destination parent %q: %w", candidate, mkdirErr)
		}
		createdHere := mkdirErr == nil
		if createdHere {
			created = append(created, candidate)
			if err := root.Chmod(candidate, 0o755); err != nil {
				rollbackParents()
				return nil, nil, fmt.Errorf("set Python alias destination parent %q mode: %w", candidate, err)
			}
		}
		createdInfo, statErr := root.Lstat(candidate)
		if statErr != nil {
			rollbackParents()
			return nil, nil, fmt.Errorf("inspect created Python alias destination parent %q: %w", candidate, statErr)
		}
		if !createdInfo.IsDir() || createdInfo.Mode()&os.ModeSymlink != 0 {
			rollbackParents()
			return nil, nil, fmt.Errorf("created Python alias destination parent %q is not a real directory", candidate)
		}
		if createdHere && runtime.GOOS != "windows" && createdInfo.Mode().Perm() != 0o755 {
			rollbackParents()
			return nil, nil, fmt.Errorf("created Python alias destination parent %q does not have mode 0755", candidate)
		}
	}
	parent, err := root.OpenRoot(parentRelative)
	if err != nil {
		rollbackParents()
		return nil, nil, fmt.Errorf("open Python alias destination parent %q: %w", parentRelative, err)
	}
	return parent, created, nil
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
