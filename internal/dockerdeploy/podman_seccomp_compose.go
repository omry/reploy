package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Translate the generated workload's sandbox settings, retaining the
// original project directory for all relative mounts and environment files.
func preparePodmanSeccompCompose(spec CommandSpec, action int) (CommandSpec, func(), bool, error) {
	noop := func() {}
	if !spec.applicationCompose {
		return spec, noop, false, nil
	}
	fileIndex, directory := -1, false
	for index := 1; index < action; index++ {
		switch spec.Args[index] {
		case "--project-directory":
			directory = true
			index++
		case "-f", "--file":
			if fileIndex >= 0 || index+1 >= action {
				return spec, noop, false, fmt.Errorf("Podman seccomp requires one explicit Compose file")
			}
			fileIndex = index + 1
			index++
		case "--project-name", "-p", "--env-file":
			index++
		}
	}
	if fileIndex < 0 {
		return spec, noop, false, fmt.Errorf("Podman seccomp requires an explicit Compose file")
	}
	path := spec.Args[fileIndex]
	if !filepath.IsAbs(path) {
		path = filepath.Join(spec.Dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return spec, noop, false, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return spec, noop, false, err
	}
	if len(document.Content) != 1 {
		return spec, noop, false, fmt.Errorf("invalid Compose document")
	}
	node := document.Content[0]
	var service *yaml.Node
	for _, key := range []string{"services", composeWorkloadService, "security_opt"} {
		var next *yaml.Node
		if node.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(node.Content); index += 2 {
				if node.Content[index].Value == key {
					if next != nil {
						return spec, noop, false, fmt.Errorf("duplicate Compose security path key %q", key)
					}
					next = node.Content[index+1]
				}
			}
		}
		if next == nil {
			return spec, noop, false, nil
		}
		node = next
		if key == composeWorkloadService {
			service = node
		}
	}
	if node.Kind != yaml.SequenceNode {
		return spec, noop, false, fmt.Errorf("Compose security_opt must be an explicit sequence")
	}
	var builtin *yaml.Node
	seccompCount := 0
	for _, option := range node.Content {
		if option.Kind != yaml.ScalarNode {
			return spec, noop, false, fmt.Errorf("Compose security_opt must contain scalar options")
		}
		if strings.HasPrefix(option.Value, "seccomp=") || strings.HasPrefix(option.Value, "seccomp:") {
			seccompCount++
		}
		if option.Value == "seccomp=builtin" {
			builtin = option
		}
	}
	if builtin == nil {
		return spec, noop, false, nil
	}
	if seccompCount != 1 {
		return spec, noop, false, fmt.Errorf("ambiguous Compose seccomp options")
	}
	profile, err := materializePodmanDockerSeccomp()
	if err != nil {
		return spec, noop, false, err
	}
	if err := preparePodmanTemporaryHomeCompose(service); err != nil {
		return spec, noop, false, err
	}
	builtin.Value = "seccomp=" + strings.ReplaceAll(profile, "$", "$$")
	data, err = yaml.Marshal(&document)
	if err != nil {
		return spec, noop, false, err
	}
	dir, err := os.MkdirTemp("", "reploy-podman-compose-")
	if err != nil {
		return spec, noop, false, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	translated := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(translated, data, 0o600); err != nil {
		cleanup()
		return spec, noop, false, err
	}
	spec.Args = append([]string(nil), spec.Args...)
	spec.Args[fileIndex] = translated
	if !directory {
		// This changes prefix length, so the caller must resolve the action again.
		prefix := append([]string{}, spec.Args[:action]...)
		prefix = append(prefix, "--project-directory", filepath.Dir(path))
		spec.Args = append(prefix, spec.Args[action:]...)
	}
	return spec, cleanup, true, nil
}
