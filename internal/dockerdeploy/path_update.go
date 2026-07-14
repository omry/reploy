package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

type PathUpdateActionKind string

const (
	PathPreserveManagedBind PathUpdateActionKind = "preserve-managed-bind"
	PathReplaceManagedBind  PathUpdateActionKind = "replace-managed-bind"
	PathPreserveVolume      PathUpdateActionKind = "preserve-volume"
	PathReplaceVolume       PathUpdateActionKind = "replace-volume"
	PathValidateUnmanaged   PathUpdateActionKind = "validate-unmanaged"
	PathTmpfsNoop           PathUpdateActionKind = "tmpfs-noop"
)

type PathUpdateOptions struct {
	ReplaceAll bool
	Clean      bool
	Replace    []string
}

type PathUpdateAction struct {
	Name   string
	Kind   PathUpdateActionKind
	Source string
	Target string
}

func PlanPathUpdates(staging DockerExecutionPlan, installed DockerExecutionPlan, installTarget string, options PathUpdateOptions) ([]PathUpdateAction, error) {
	stagingByName := mountPlansByName(staging.Mounts)
	installedByName := mountPlansByName(installed.Mounts)
	replace := map[string]bool{}
	for _, name := range options.Replace {
		if _, exists := installedByName[name]; !exists {
			return nil, fmt.Errorf("replace override references unknown path %q", name)
		}
		replace[name] = true
	}
	actions := []PathUpdateAction{}
	for _, name := range sortedMountPlanNames(installedByName) {
		target := installedByName[name]
		source, exists := stagingByName[name]
		if !exists {
			return nil, fmt.Errorf("installed path %q has no staging source", name)
		}
		policy := target.Update
		if (options.ReplaceAll || options.Clean || replace[name]) && policy != blueprint.UpdateUnmanaged {
			policy = blueprint.UpdateReplace
		}
		action := PathUpdateAction{Name: name, Source: source.Source, Target: target.Source}
		switch target.Mode {
		case blueprint.MountManagedBind:
			if err := requirePathWithinInstallTarget(target.Source, installTarget); err != nil {
				return nil, fmt.Errorf("managed path %q: %w", name, err)
			}
			if policy == blueprint.UpdateReplace && filepath.Clean(source.Source) == filepath.Clean(target.Source) {
				return nil, fmt.Errorf("managed path %q replacement source and target must differ", name)
			}
			if policy == blueprint.UpdateReplace {
				action.Kind = PathReplaceManagedBind
			} else {
				action.Kind = PathPreserveManagedBind
			}
		case blueprint.MountVolume:
			if source.Source == target.Source {
				return nil, fmt.Errorf("volume path %q replacement source and target must differ", name)
			}
			if policy == blueprint.UpdateReplace {
				action.Kind = PathReplaceVolume
			} else {
				action.Kind = PathPreserveVolume
			}
		case blueprint.MountBind:
			if policy != blueprint.UpdateUnmanaged {
				return nil, fmt.Errorf("external bind %q must remain unmanaged", name)
			}
			action.Kind = PathValidateUnmanaged
		case blueprint.MountTmpfs:
			action.Kind = PathTmpfsNoop
		default:
			return nil, fmt.Errorf("unsupported path mode %q", target.Mode)
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func requirePathWithinInstallTarget(candidate string, installTarget string) error {
	if installTarget == "" {
		return fmt.Errorf("install target is required")
	}
	relative, err := filepath.Rel(installTarget, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("target escapes install directory: %s", candidate)
	}
	return nil
}

func mountPlansByName(mounts []MountExecutionPlan) map[string]MountExecutionPlan {
	result := make(map[string]MountExecutionPlan, len(mounts))
	for _, mount := range mounts {
		result[mount.Name] = mount
	}
	return result
}

func sortedMountPlanNames(mounts map[string]MountExecutionPlan) []string {
	names := make([]string, 0, len(mounts))
	for name := range mounts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
