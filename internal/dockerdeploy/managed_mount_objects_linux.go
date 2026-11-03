//go:build linux

package dockerdeploy

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"golang.org/x/sys/unix"
)

type managedMountObjectIdentityV1 struct {
	Device, Inode, Mount uint64
	UID, GID, Mode       uint32
}

type preparedManagedMountObjectV1 struct {
	FD       int
	Identity managedMountObjectIdentityV1
}

// prepareManagedMountObjectsV1 is an inert Linux primitive, not admission.
// The caller must hold exclusive, unattached managed-storage authority and run
// inside the selected setup mapping. root, staging and recorded descriptors are borrowed;
// recorded descriptors must come from that resource's retained provenance.
// staging must be a distinct, private, setup-owned directory on the same mount,
// inaccessible to workload writers for the entire call. F1.3/F2 supply and fence
// that authority; matching UID alone does not establish its provenance.
// Success transfers ownership of every returned descriptor to the caller.
// Failure closes owned descriptors, leaves any newly created inert objects in
// place (including private staging entries) and requires the caller to retain
// inhibition for explicit recovery. Staging's default ACL is temporary setup state.
// Existing objects are never chowned, chmodded, truncated or adopted by path.
func prepareManagedMountObjectsV1(ctx context.Context, root, staging int, objects []blueprint.ManagedMountObject, recorded map[string]int) (result map[string]preparedManagedMountObjectV1, err error) {
	ordered, err := orderManagedMountObjectsV1(objects)
	if err != nil {
		return nil, err
	}
	rootIdentity, err := managedMountObjectIdentityForFDV1(root)
	if err != nil || rootIdentity.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, fmt.Errorf("managed root must be a held directory: %v", err)
	}
	stagingIdentity, err := managedMountObjectIdentityForFDV1(staging)
	if err != nil || stagingIdentity.Mode&unix.S_IFMT != unix.S_IFDIR || stagingIdentity.Mode&0o7777 != 0o700 ||
		stagingIdentity.UID != uint32(os.Geteuid()) || stagingIdentity.Mount != rootIdentity.Mount ||
		stagingIdentity.Device == rootIdentity.Device && stagingIdentity.Inode == rootIdentity.Inode {
		return nil, fmt.Errorf("managed staging must be a distinct private setup-owned directory on the managed mount: %v", err)
	}
	prepared := make(map[string]preparedManagedMountObjectV1, len(ordered))
	defer func() {
		if err != nil {
			for _, object := range prepared {
				unix.Close(object.FD)
			}
		}
	}()
	for _, declaration := range ordered {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		object, prepareErr := prepareManagedMountObjectV1(root, staging, rootIdentity.Mount, declaration, recorded, prepared)
		if prepareErr != nil {
			return nil, fmt.Errorf("prepare managed object %q: %w", declaration.Path, prepareErr)
		}
		prepared[declaration.Path] = object
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return prepared, nil
}

func orderManagedMountObjectsV1(objects []blueprint.ManagedMountObject) ([]blueprint.ManagedMountObject, error) {
	if len(objects) == 0 {
		return nil, fmt.Errorf("managed preparation requires an explicit root declaration")
	}
	ordered := append([]blueprint.ManagedMountObject(nil), objects...)
	seen := make(map[string]bool, len(objects))
	for _, object := range ordered {
		name := object.Path
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00\r\n\t") {
			return nil, fmt.Errorf("managed object requires a normalized relative Linux path")
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate managed object %q", name)
		}
		if name == "." && !object.Directory || object.Identity.Permissions != nil && *object.Identity.Permissions > 0o7777 {
			return nil, fmt.Errorf("invalid managed object declaration %q", name)
		}
		seen[name] = object.Directory
	}
	if directory, exists := seen["."]; !exists || !directory {
		return nil, fmt.Errorf("managed preparation requires a directory root")
	}
	for _, object := range ordered {
		for parent := path.Dir(object.Path); parent != "."; parent = path.Dir(parent) {
			if directory, declared := seen[parent]; declared && !directory {
				return nil, fmt.Errorf("managed object %q has a file ancestor", object.Path)
			}
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Path == "." || b.Path == "." {
			return a.Path == "."
		}
		if a.Directory != b.Directory {
			return a.Directory
		}
		if depthA, depthB := strings.Count(a.Path, "/"), strings.Count(b.Path, "/"); depthA != depthB {
			return depthA < depthB
		}
		return a.Path < b.Path
	})
	return ordered, nil
}

func openManagedMountObjectV1(root int, name string, flags int) (int, error) {
	return unix.Openat2(root, name, &unix.OpenHow{
		Flags: uint64(flags | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS |
			unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
}

func managedMountObjectIdentityForFDV1(fd int) (managedMountObjectIdentityV1, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return managedMountObjectIdentityV1{}, err
	}
	if kind := stat.Mode & unix.S_IFMT; kind != unix.S_IFDIR && (kind != unix.S_IFREG || stat.Nlink != 1) {
		return managedMountObjectIdentityV1{}, fmt.Errorf("managed object must be a directory or unaliased regular file")
	}
	var mount unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &mount); err != nil {
		return managedMountObjectIdentityV1{}, err
	}
	if mount.Mask&unix.STATX_MNT_ID == 0 {
		return managedMountObjectIdentityV1{}, fmt.Errorf("kernel did not report the held object's mount identity")
	}
	return managedMountObjectIdentityV1{Device: uint64(stat.Dev), Inode: stat.Ino, Mount: mount.Mnt_id, UID: stat.Uid, GID: stat.Gid, Mode: stat.Mode}, nil
}

func verifyManagedMountObjectV1(fd int, mount uint64, declaration blueprint.ManagedMountObject, expected int) (managedMountObjectIdentityV1, error) {
	actual, err := managedMountObjectIdentityForFDV1(fd)
	if err != nil {
		return actual, err
	}
	retained, err := managedMountObjectIdentityForFDV1(expected)
	if err != nil {
		return actual, fmt.Errorf("recorded descriptor: %w", err)
	}
	kind := uint32(unix.S_IFREG)
	if declaration.Directory {
		kind = unix.S_IFDIR
	}
	identity := declaration.Identity
	if actual != retained || actual.Mount != mount || actual.Mode&unix.S_IFMT != kind || actual.UID != identity.UID || actual.GID != identity.GID ||
		identity.Permissions != nil && actual.Mode&0o7777 != *identity.Permissions {
		return actual, fmt.Errorf("managed object identity, type or declared metadata differs from recorded authority")
	}
	return actual, nil
}

func prepareManagedMountObjectV1(root, staging int, mount uint64, declaration blueprint.ManagedMountObject, recorded map[string]int, prepared map[string]preparedManagedMountObjectV1) (preparedManagedMountObjectV1, error) {
	name := declaration.Path
	fd, err := openManagedMountObjectV1(root, name, unix.O_PATH)
	if err == nil {
		expected, exists := recorded[name]
		if !exists {
			unix.Close(fd)
			return preparedManagedMountObjectV1{}, fmt.Errorf("existing object has no retained descriptor provenance")
		}
		identity, err := verifyManagedMountObjectV1(fd, mount, declaration, expected)
		if err != nil {
			unix.Close(fd)
			return preparedManagedMountObjectV1{}, err
		}
		return preparedManagedMountObjectV1{FD: fd, Identity: identity}, nil
	}
	if !errors.Is(err, unix.ENOENT) || name == "." {
		return preparedManagedMountObjectV1{}, err
	}
	if _, exists := recorded[name]; exists {
		return preparedManagedMountObjectV1{}, fmt.Errorf("recorded managed object is missing")
	}
	parentName := path.Dir(name)
	parent, err := openManagedMountObjectV1(root, parentName, unix.O_PATH|unix.O_DIRECTORY)
	if err != nil {
		return preparedManagedMountObjectV1{}, err
	}
	defer unix.Close(parent)
	expected, exists := recorded[parentName]
	if object, ready := prepared[parentName]; ready {
		expected, exists = object.FD, true
	}
	if !exists {
		return preparedManagedMountObjectV1{}, fmt.Errorf("parent has no retained descriptor provenance")
	}
	parentIdentity, err := managedMountObjectIdentityForFDV1(parent)
	if err != nil {
		return preparedManagedMountObjectV1{}, err
	}
	retained, err := managedMountObjectIdentityForFDV1(expected)
	if err != nil || parentIdentity != retained || parentIdentity.Mount != mount {
		return preparedManagedMountObjectV1{}, fmt.Errorf("managed parent was substituted")
	}
	return createManagedMountObjectV1(parent, staging, path.Base(name), mount, declaration)
}

// Reuse kernel ACL inheritance rather than interpreting or rebuilding ACLs.
// Only the private staging directory's default ACL changes, never the parent.
func inheritManagedMountDirectoryACLV1(parent, staging int) error {
	fd, err := openManagedMountObjectV1(parent, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	acl := make([]byte, 65536) // Linux's maximum extended-attribute value size.
	n, err := unix.Fgetxattr(fd, "system.posix_acl_default", acl)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EOPNOTSUPP) {
		err = unix.Fremovexattr(staging, "system.posix_acl_default")
		if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EOPNOTSUPP) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}
	return unix.Fsetxattr(staging, "system.posix_acl_default", acl[:n], 0)
}

func createManagedMountObjectV1(parent, staging int, leaf string, mount uint64, declaration blueprint.ManagedMountObject) (result preparedManagedMountObjectV1, err error) {
	mode := uint32(0o644)
	if declaration.Directory {
		mode = 0o755
	}
	// Files return their creation FD atomically. Directories must be pinned and
	// receive metadata privately before their destination name becomes visible.
	var fd int
	var inheritedMode uint32
	var stagingName string
	var stagingFD int
	if declaration.Directory {
		parentIdentity, statErr := managedMountObjectIdentityForFDV1(parent)
		if statErr != nil {
			return result, statErr
		}
		inheritedMode = parentIdentity.Mode & unix.S_ISGID
		stagingFD, err = openManagedMountObjectV1(staging, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK)
		if err != nil {
			return result, err
		}
		defer unix.Close(stagingFD)
		if err = inheritManagedMountDirectoryACLV1(parent, stagingFD); err != nil {
			return result, err
		}
		stagingName = "reploy-dir-" + rand.Text()
		if err = unix.Mkdirat(stagingFD, stagingName, mode); err != nil {
			return result, err
		}
		fd, err = openManagedMountObjectV1(stagingFD, stagingName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK)
	} else {
		fd, err = unix.Openat(parent, leaf, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, mode)
	}
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			unix.Close(fd)
		}
	}()
	identity, err := managedMountObjectIdentityForFDV1(fd)
	if err != nil {
		return result, err
	}
	if identity.Mount != mount || identity.UID != uint32(os.Geteuid()) {
		return result, fmt.Errorf("new object differs from exclusive setup creation")
	}
	// Preserve the creation mode after umask when no mode was requested.
	mode = identity.Mode&0o7777 | inheritedMode
	if declaration.Identity.Permissions != nil {
		mode = *declaration.Identity.Permissions
	}
	if err = unix.Fchown(fd, int(declaration.Identity.UID), int(declaration.Identity.GID)); err != nil {
		return result, err
	}
	if err = unix.Fchmod(fd, mode); err != nil {
		return result, err
	}
	if declaration.Directory {
		if err = unix.Renameat2(stagingFD, stagingName, parent, leaf, unix.RENAME_NOREPLACE); err != nil {
			return result, err
		}
	}
	identity, err = managedMountObjectIdentityForFDV1(fd)
	if err != nil {
		return result, err
	}
	observed, err := openManagedMountObjectV1(parent, leaf, unix.O_PATH)
	if err != nil {
		return result, err
	}
	defer unix.Close(observed)
	declared := declaration
	declared.Identity.Permissions = &mode
	if _, err = verifyManagedMountObjectV1(observed, mount, declared, fd); err != nil {
		return result, err
	}
	return preparedManagedMountObjectV1{FD: fd, Identity: identity}, nil
}
