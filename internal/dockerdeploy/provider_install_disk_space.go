package dockerdeploy

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

type providerInstallDiskRequirementV1 struct {
	Path  string
	Bytes uint64
}

type providerInstallFilesystemSpaceV1 struct {
	Key       string
	Available uint64
}

type providerInstallFilesystemSpaceLookupV1 func(string) (providerInstallFilesystemSpaceV1, error)

// preflightProviderInstallDiskSpaceV1 checks logical bytes without adding a
// policy margin. Requirements are grouped by their actual destination
// filesystem so split /opt and /etc mounts are accounted for independently.
func preflightProviderInstallDiskSpaceV1(requirements []providerInstallDiskRequirementV1) error {
	return preflightProviderInstallDiskSpaceWithV1(requirements, providerInstallFilesystemSpace)
}

func preflightProviderInstallDiskSpaceWithV1(requirements []providerInstallDiskRequirementV1, lookup providerInstallFilesystemSpaceLookupV1) error {
	if requirements == nil {
		return fmt.Errorf("install disk-space preflight requires an array")
	}
	if lookup == nil {
		return fmt.Errorf("install disk-space preflight requires a filesystem lookup")
	}
	type filesystemRequirement struct {
		bytes     uint64
		available uint64
		paths     []string
	}
	filesystems := map[string]*filesystemRequirement{}
	for index, requirement := range requirements {
		if requirement.Path == "" || !filepath.IsAbs(requirement.Path) || filepath.Clean(requirement.Path) != requirement.Path {
			return fmt.Errorf("install disk-space requirement %d must name an absolute clean path", index)
		}
		if requirement.Bytes == 0 {
			continue
		}
		directory, err := nearestExistingInstallDirectoryV1(requirement.Path)
		if err != nil {
			return err
		}
		space, err := lookup(directory)
		if err != nil {
			return fmt.Errorf("inspect install disk space for %q: %w", requirement.Path, err)
		}
		if strings.TrimSpace(space.Key) == "" {
			return fmt.Errorf("inspect install disk space for %q: filesystem key is empty", requirement.Path)
		}
		group := filesystems[space.Key]
		if group == nil {
			group = &filesystemRequirement{available: space.Available}
			filesystems[space.Key] = group
		} else if space.Available < group.available {
			// No files are written during the preflight, but use the lower value
			// if the host reports a changing amount of free space.
			group.available = space.Available
		}
		if math.MaxUint64-group.bytes < requirement.Bytes {
			return fmt.Errorf("install disk-space requirements overflow uint64")
		}
		group.bytes += requirement.Bytes
		group.paths = append(group.paths, requirement.Path)
	}

	keys := make([]string, 0, len(filesystems))
	for key := range filesystems {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := filesystems[key]
		if group.available < group.bytes {
			sort.Strings(group.paths)
			return fmt.Errorf(
				"insufficient disk space for install paths %s: need %d bytes, have %d bytes",
				strings.Join(group.paths, ", "), group.bytes, group.available,
			)
		}
	}
	return nil
}
