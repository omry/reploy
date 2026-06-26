package dockerdeploy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type PortOverride struct {
	Name     string
	HostPort string
}

type dockerPortBinding struct {
	Name          string
	EnvSuffix     string
	HostBind      string
	HostPort      string
	ContainerPort string
	Named         bool
}

func dockerPortBindings(pack deploy.AppPack, service deploy.DockerServiceConfig) ([]dockerPortBinding, error) {
	if len(pack.Docker.Ports) == 0 {
		return []dockerPortBinding{{
			Name:          "default",
			EnvSuffix:     "DEFAULT",
			HostBind:      service.HostBind,
			HostPort:      service.HostPort,
			ContainerPort: service.ContainerPort,
		}}, nil
	}
	names := make([]string, 0, len(pack.Docker.Ports))
	for name := range pack.Docker.Ports {
		names = append(names, name)
	}
	sort.Strings(names)
	ports := make([]dockerPortBinding, 0, len(names))
	envSuffixes := map[string]string{}
	for _, name := range names {
		config := pack.Docker.Ports[name]
		envSuffix, err := portEnvSuffix(name)
		if err != nil {
			return nil, err
		}
		if previousName, ok := envSuffixes[envSuffix]; ok {
			return nil, fmt.Errorf("docker port names %q and %q both map to environment suffix %q", previousName, name, envSuffix)
		}
		envSuffixes[envSuffix] = name
		hostBind := defaultString(config.HostBind, service.HostBind)
		hostPort := config.HostPort
		containerPort := config.ContainerPort
		if len(names) == 1 {
			hostPort = defaultString(hostPort, service.HostPort)
			containerPort = defaultString(containerPort, service.ContainerPort)
		}
		if hostPort == "" {
			return nil, fmt.Errorf("docker.ports.%s.host_port is required when multiple ports are declared", name)
		}
		if containerPort == "" {
			return nil, fmt.Errorf("docker.ports.%s.container_port is required when multiple ports are declared", name)
		}
		ports = append(ports, dockerPortBinding{
			Name:          name,
			EnvSuffix:     envSuffix,
			HostBind:      hostBind,
			HostPort:      hostPort,
			ContainerPort: containerPort,
			Named:         true,
		})
	}
	return ports, nil
}

func applyPortOverrides(ports []dockerPortBinding, overrides []PortOverride) ([]dockerPortBinding, error) {
	resolved := append([]dockerPortBinding(nil), ports...)
	seen := map[string]bool{}
	for _, override := range overrides {
		hostPort := strings.TrimSpace(override.HostPort)
		if err := validateHostPort(hostPort); err != nil {
			return nil, err
		}
		if strings.TrimSpace(override.Name) == "" {
			if len(resolved) != 1 {
				return nil, fmt.Errorf("blueprint exposes multiple ports; use named bindings: %s", namedPortHelp(resolved))
			}
			if seen[resolved[0].Name] {
				return nil, fmt.Errorf("--port provided multiple values for %s", resolved[0].Name)
			}
			resolved[0].HostPort = hostPort
			seen[resolved[0].Name] = true
			continue
		}
		name := strings.TrimSpace(override.Name)
		index := portIndex(resolved, name)
		if index == -1 {
			return nil, fmt.Errorf("unknown port %q; declared ports: %s", name, strings.Join(portNames(resolved), ", "))
		}
		if seen[name] {
			return nil, fmt.Errorf("--port provided multiple values for %s", name)
		}
		resolved[index].HostPort = hostPort
		seen[name] = true
	}
	return resolved, nil
}

func validateHostPort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("host port must be a number from 1 to 65535: %s", value)
	}
	return nil
}

func portIndex(ports []dockerPortBinding, name string) int {
	for index, port := range ports {
		if port.Name == name {
			return index
		}
	}
	return -1
}

func portNames(ports []dockerPortBinding) []string {
	names := make([]string, 0, len(ports))
	for _, port := range ports {
		names = append(names, port.Name)
	}
	return names
}

func namedPortHelp(ports []dockerPortBinding) string {
	names := portNames(ports)
	for index, name := range names {
		names[index] = "--port " + name + "=HOST_PORT"
	}
	return strings.Join(names, ", ")
}

func portEnvSuffix(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("port name must not be empty")
	}
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range name {
		valid := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if valid {
			if r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore && builder.Len() > 0 {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "", fmt.Errorf("port name has no environment-safe characters: %s", name)
	}
	return result, nil
}

func portEnvNames(port dockerPortBinding) (string, string, string) {
	if !port.Named {
		return "REPLOY_HOST_BIND", "REPLOY_HOST_PORT", "REPLOY_CONTAINER_PORT"
	}
	prefix := "REPLOY_PORT_" + port.EnvSuffix
	return prefix + "_HOST_BIND", prefix + "_HOST_PORT", prefix + "_CONTAINER_PORT"
}

func hasNamedPortBindings(ports []dockerPortBinding) bool {
	for _, port := range ports {
		if port.Named {
			return true
		}
	}
	return false
}

func renderComposePortBindings(ports []dockerPortBinding) string {
	lines := make([]string, 0, len(ports))
	for _, port := range ports {
		hostBindEnv, hostPortEnv, containerPortEnv := portEnvNames(port)
		lines = append(lines, fmt.Sprintf(
			`      - "${%s:-%s}:${%s:-%s}:${%s:-%s}"`,
			hostBindEnv,
			port.HostBind,
			hostPortEnv,
			port.HostPort,
			containerPortEnv,
			port.ContainerPort,
		))
	}
	return strings.Join(lines, "\n")
}

func installPortState(ports []dockerPortBinding) map[string]deploy.InstallPortBinding {
	state := make(map[string]deploy.InstallPortBinding, len(ports))
	for _, port := range ports {
		state[port.Name] = deploy.InstallPortBinding{
			HostBind:      port.HostBind,
			HostPort:      port.HostPort,
			ContainerPort: port.ContainerPort,
		}
	}
	return state
}

func dockerEnvPortUpdates(ports []dockerPortBinding) map[string]string {
	updates := map[string]string{}
	if len(ports) == 0 {
		return updates
	}
	primary := ports[0]
	updates["REPLOY_HOST_BIND"] = primary.HostBind
	updates["REPLOY_HOST_PORT"] = primary.HostPort
	updates["REPLOY_CONTAINER_PORT"] = primary.ContainerPort
	for _, port := range ports {
		hostBindEnv, hostPortEnv, containerPortEnv := portEnvNames(port)
		updates[hostBindEnv] = port.HostBind
		updates[hostPortEnv] = port.HostPort
		updates[containerPortEnv] = port.ContainerPort
	}
	return updates
}
