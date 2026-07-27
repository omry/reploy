package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type RuntimeHostSourceV1 struct {
	Destination string
	HostPath    string
	SourceKind  string
	ReadOnly    bool
}

func RuntimeHostSourcesV1(plan DockerExecutionPlan, output *transientOutputMount) ([]RuntimeHostSourceV1, error) {
	sources := []RuntimeHostSourceV1{}
	for _, mount := range plan.Mounts {
		switch mount.Mode {
		case blueprint.MountManagedBind, blueprint.MountBind:
			sourceKind, err := runtimeMountSourceKindV1(mount)
			if err != nil {
				return nil, fmt.Errorf("runtime mount %q: %w", mount.Name, err)
			}
			sources = append(sources, RuntimeHostSourceV1{
				Destination: mount.Target, HostPath: mount.Source, SourceKind: sourceKind, ReadOnly: mount.ReadOnly,
			})
		case blueprint.MountVolume, blueprint.MountTmpfs:
		default:
			return nil, fmt.Errorf("runtime mount %q has unsupported mode %q", mount.Name, mount.Mode)
		}
	}
	if output != nil {
		sources = append(sources, RuntimeHostSourceV1{
			Destination: runtimeOutputRoot, HostPath: output.HostDirectory,
			SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: false,
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

func ValidateRuntimeHostSourcesV1(policy deploy.RuntimePolicyV1, planID string, sources []RuntimeHostSourceV1) error {
	if err := deploy.ValidateRuntimePolicyV1(policy); err != nil {
		return err
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
		if source.SourceKind != mount.SourceKind || source.ReadOnly != mount.ReadOnly {
			return fmt.Errorf("runtime plan %q mount %q host kind or access policy changed", planID, mount.Destination)
		}
		info, err := os.Stat(source.HostPath)
		if err != nil {
			return fmt.Errorf("runtime plan %q mount %q host source: %w", planID, mount.Destination, err)
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
