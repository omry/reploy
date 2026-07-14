package blueprint

import (
	"fmt"
	"sort"
	"strings"
)

const environmentPathReferencePrefix = "environment.paths."
const environmentEndpointReferencePrefix = "environment.workload.endpoints."

type extendedSyntax struct {
	Mounts    map[string]extendedMountSyntax
	Endpoints map[string]extendedEndpointSyntax
}

type extendedMountSyntax struct {
	Path   PathSyntax
	Docker DockerMountSyntax
}

type extendedEndpointSyntax struct {
	Endpoint EndpointSyntax
	Docker   DockerEndpointSyntax
}

func resolveExtends(source Syntax) (extendedSyntax, error) {
	resolved := extendedSyntax{
		Mounts:    map[string]extendedMountSyntax{},
		Endpoints: map[string]extendedEndpointSyntax{},
	}

	mountNames := sortedKeys(source.Docker.Mounts)
	for _, name := range mountNames {
		mount := source.Docker.Mounts[name]
		reference, err := referencedName("docker.mounts."+name+".extends", mount.Extends, environmentPathReferencePrefix)
		if err != nil {
			return extendedSyntax{}, err
		}
		path, ok := source.Environment.Paths[reference]
		if !ok {
			return extendedSyntax{}, fmt.Errorf("docker.mounts.%s.extends references missing environment path %q", name, reference)
		}
		resolved.Mounts[name] = extendedMountSyntax{Path: path, Docker: mount}
	}

	if source.Docker.Workload == nil {
		return resolved, nil
	}
	if source.Environment.Workload == nil && len(source.Docker.Workload.Endpoints) > 0 {
		return extendedSyntax{}, fmt.Errorf("docker.workload.endpoints require environment.workload")
	}
	endpointNames := sortedKeys(source.Docker.Workload.Endpoints)
	for _, name := range endpointNames {
		endpoint := source.Docker.Workload.Endpoints[name]
		reference, err := referencedName("docker.workload.endpoints."+name+".extends", endpoint.Extends, environmentEndpointReferencePrefix)
		if err != nil {
			return extendedSyntax{}, err
		}
		inherited, ok := source.Environment.Workload.Endpoints[reference]
		if !ok {
			return extendedSyntax{}, fmt.Errorf("docker.workload.endpoints.%s.extends references missing environment endpoint %q", name, reference)
		}
		resolved.Endpoints[name] = extendedEndpointSyntax{Endpoint: inherited, Docker: endpoint}
	}
	return resolved, nil
}

func referencedName(field string, reference string, prefix string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if !strings.HasPrefix(reference, prefix) {
		return "", fmt.Errorf("%s must reference %s<name>", field, prefix)
	}
	name := strings.TrimPrefix(reference, prefix)
	if name == "" || strings.Contains(name, ".") {
		return "", fmt.Errorf("%s must reference one named object", field)
	}
	return name, nil
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
