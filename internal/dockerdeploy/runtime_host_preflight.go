package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type RuntimeHostSourceV1 struct {
	Destination string
	HostPath    string
	SourceKind  string
	Authority   string
	ReadOnly    bool
}

const (
	runtimeHostAuthorityInputV1       = "host-input"
	runtimeHostAuthoritySharedStateV1 = "shared-state"
	runtimeHostAuthorityOutputV1      = "explicit-output"
)

func RuntimeHostSourcesV1(plan DockerExecutionPlan, output *transientOutputMount) ([]RuntimeHostSourceV1, error) {
	sources := []RuntimeHostSourceV1{}
	for _, mount := range plan.Mounts {
		switch mount.Mode {
		case blueprint.MountManagedBind, blueprint.MountBind:
			sourceKind, err := runtimeMountSourceKindV1(mount)
			if err != nil {
				return nil, fmt.Errorf("runtime mount %q: %w", mount.Name, err)
			}
			authority := runtimeHostAuthoritySharedStateV1
			if mount.ReadOnly {
				authority = runtimeHostAuthorityInputV1
			}
			sources = append(sources, RuntimeHostSourceV1{
				Destination: mount.Target, HostPath: mount.Source, SourceKind: sourceKind,
				Authority: authority, ReadOnly: mount.ReadOnly,
			})
		case blueprint.MountVolume, blueprint.MountTmpfs:
		default:
			return nil, fmt.Errorf("runtime mount %q has unsupported mode %q", mount.Name, mount.Mode)
		}
	}
	if output != nil {
		sources = append(sources, RuntimeHostSourceV1{
			Destination: runtimeOutputRoot, HostPath: output.HostDirectory,
			SourceKind: deploy.RuntimeMountSourceDirectory, Authority: runtimeHostAuthorityOutputV1, ReadOnly: false,
		})
	}
	sort.Slice(sources, func(left int, right int) bool { return sources[left].Destination < sources[right].Destination })
	for index := 1; index < len(sources); index++ {
		if sources[index-1].Destination == sources[index].Destination {
			return nil, fmt.Errorf("runtime host sources duplicate destination %q", sources[index].Destination)
		}
	}
	return sources, nil
}

func ValidateRuntimeHostSourcesV1(policy deploy.RuntimePolicyV1, planID string, runtimeUID int, sources []RuntimeHostSourceV1) error {
	if err := deploy.ValidateRuntimePolicyV1(policy); err != nil {
		return err
	}
	if runtimeUID < 0 {
		return fmt.Errorf("runtime UID must be non-negative")
	}
	var selected *deploy.RuntimePlanV1
	for index := range policy.Plans {
		if policy.Plans[index].ID == planID {
			selected = &policy.Plans[index]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("runtime plan %q is absent from the current build", planID)
	}
	byDestination := make(map[string]RuntimeHostSourceV1, len(sources))
	for _, source := range sources {
		if source.Destination == "" || !filepath.IsAbs(source.HostPath) {
			return fmt.Errorf("runtime host source for %q requires an absolute host path", source.Destination)
		}
		if _, found := byDestination[source.Destination]; found {
			return fmt.Errorf("runtime host sources duplicate destination %q", source.Destination)
		}
		byDestination[source.Destination] = source
	}
	for _, mount := range selected.Mounts {
		source, found := byDestination[mount.Destination]
		if mount.SourceKind == deploy.RuntimeMountSourceGenerated {
			if found {
				return fmt.Errorf("runtime plan %q generated mount %q unexpectedly has a host source", planID, mount.Destination)
			}
			continue
		}
		if !found {
			return fmt.Errorf("runtime plan %q mount %q is missing its host source", planID, mount.Destination)
		}
		delete(byDestination, mount.Destination)
		wantAuthority := runtimeHostAuthoritySharedStateV1
		if mount.ReadOnly {
			wantAuthority = runtimeHostAuthorityInputV1
		} else if mount.Destination == runtimeOutputRoot {
			wantAuthority = runtimeHostAuthorityOutputV1
		}
		if source.SourceKind != mount.SourceKind || source.ReadOnly != mount.ReadOnly || source.Authority != wantAuthority {
			return fmt.Errorf("runtime plan %q mount %q host kind or access policy changed", planID, mount.Destination)
		}
		if runtimeUID == 0 {
			if source.Authority == runtimeHostAuthorityOutputV1 {
				return fmt.Errorf("root application runtime cannot use explicit output mounts until the root-safe output contract is implemented")
			}
			kind := "host shared-state"
			if source.Authority == runtimeHostAuthorityInputV1 {
				kind = "host input"
			}
			return fmt.Errorf("root application runtime cannot use %s mount %q; use image content, a Docker-managed volume, or tmpfs instead", kind, mount.Destination)
		}
		info, err := os.Stat(source.HostPath)
		if err != nil {
			return fmt.Errorf("runtime plan %q mount %q host source: %w", planID, mount.Destination, err)
		}
		protected, err := protectedRuntimeHostTreeV1(source.HostPath)
		if err != nil {
			return fmt.Errorf("runtime plan %q mount %q host source: %w", planID, mount.Destination, err)
		}
		if protected != "" {
			return fmt.Errorf(
				"runtime plan %q mount %q host source resolves to protected host system source %q; ordinary host binds cannot expose the host filesystem root or protected kernel filesystems, including /proc, /dev, and /sys",
				planID, mount.Destination, protected,
			)
		}
		switch mount.SourceKind {
		case deploy.RuntimeMountSourceDirectory:
			if !info.IsDir() {
				return fmt.Errorf("runtime plan %q mount %q host source is not a directory", planID, mount.Destination)
			}
		case deploy.RuntimeMountSourceFile:
			if !info.Mode().IsRegular() {
				return fmt.Errorf("runtime plan %q mount %q host source is not a regular file", planID, mount.Destination)
			}
		default:
			return fmt.Errorf("runtime plan %q mount %q has unsupported host source kind %q", planID, mount.Destination, mount.SourceKind)
		}
	}
	if len(byDestination) != 0 {
		destinations := make([]string, 0, len(byDestination))
		for destination := range byDestination {
			destinations = append(destinations, destination)
		}
		sort.Strings(destinations)
		return fmt.Errorf("runtime plan %q has unexpected host source for %q", planID, destinations[0])
	}
	return nil
}

func protectedRuntimeHostTreeV1(hostPath string) (string, error) {
	if runtime.GOOS != "windows" {
		original := filepath.Clean(hostPath)
		for _, candidate := range []string{"/proc", "/dev", "/sys"} {
			if pathWithinV1(original, candidate) {
				return candidate, nil
			}
		}
	}
	protected, err := protectedRuntimeHostPathV1(hostPath)
	if err != nil {
		return "", fmt.Errorf("validate host path resolution: %w", err)
	}
	if protected != "" {
		return protected, nil
	}

	resolved, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		return "", fmt.Errorf("resolve canonical path: %w", err)
	}
	resolved = filepath.Clean(resolved)
	filesystem, err := protectedRuntimeHostFilesystemV1(resolved)
	if err != nil {
		return "", fmt.Errorf("identify host filesystem: %w", err)
	}
	if filesystem != "" {
		return filesystem, nil
	}

	volumeRoot := filepath.VolumeName(resolved) + string(filepath.Separator)
	if filepath.Clean(volumeRoot) == resolved {
		return volumeRoot, nil
	}
	if runtime.GOOS == "windows" {
		return "", nil
	}

	for _, candidate := range []string{"/proc", "/dev", "/sys"} {
		canonical, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("resolve protected host system tree %q: %w", candidate, err)
		}
		if pathWithinV1(resolved, canonical) {
			return candidate, nil
		}
	}
	return "", nil
}

func pathWithinV1(path string, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ValidateRootRuntimeHostAuthorityV1(policy deploy.RuntimePolicyV1, plan DockerExecutionPlan) error {
	if plan.Sandbox.RuntimeUser.UID != 0 {
		return nil
	}
	invocation, err := ShellRuntimeInvocationV1(plan)
	if err != nil {
		return err
	}
	return ValidateRuntimeHostSourcesV1(policy, invocation.PlanID, 0, invocation.Sources)
}
