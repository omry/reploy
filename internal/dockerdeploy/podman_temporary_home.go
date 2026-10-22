package dockerdeploy

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const podmanPrepareTemporaryHome = "--prepare-temporary-home"

// Podman 6.1.4 and the tested #29726 engine reject Docker tmpfs uid/gid
// options: https://github.com/podman-container-tools/podman/issues/29979.
// Retain every mount restriction and delegate only
// ownership of the fixed mount root to the trusted startup verifier.
func podmanTemporaryHome(mount string, command []string) (string, []string, error) {
	if len(command) < 2 || (command[0] != "sandbox-exec" && command[0] != "restricted-exec") {
		return "", nil, fmt.Errorf("Podman private home requires the Reploy startup verifier; rebuild the runtime image")
	}
	separator := slices.Index(command, "--")
	if separator < 1 || separator == len(command)-1 {
		return "", nil, fmt.Errorf("invalid trusted startup arguments")
	}
	identity := map[string]string{}
	privateEnvironment := false
	for index := 1; index < separator; index++ {
		if command[index] == "--private-environment" {
			if privateEnvironment {
				return "", nil, fmt.Errorf("duplicate private workload environment option")
			}
			privateEnvironment = true
			continue
		}
		if index+1 >= separator || command[index] == podmanPrepareTemporaryHome {
			return "", nil, fmt.Errorf("invalid canonical trusted startup options")
		}
		if command[index] == "--uid" || command[index] == "--gid" {
			key, value := command[index], command[index+1]
			number, err := strconv.ParseUint(value, 10, 32)
			if err != nil || number == 0xffffffff || strconv.FormatUint(number, 10) != value || identity[key] != "" {
				return "", nil, fmt.Errorf("invalid trusted startup identity")
			}
			identity[key] = value
		}
		index++
	}
	base := "/mnt/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=0700"
	if identity["--uid"] == "" || identity["--gid"] == "" || mount != base+",uid="+identity["--uid"]+",gid="+identity["--gid"] {
		return "", nil, fmt.Errorf("Podman private home does not match the canonical mount and startup identity")
	}
	translated := append([]string{command[0], podmanPrepareTemporaryHome}, command[1:]...)
	return base, translated, nil
}

func preparePodmanTemporaryHomeCommand(spec CommandSpec) (CommandSpec, error) {
	// The seccomp marker is set only by application renderers. The entrypoint
	// terminates their engine options; nothing after the application separator
	// is interpreted as an engine option.
	entrypoint := slices.Index(spec.Args, "--entrypoint")
	if entrypoint < 1 || entrypoint+4 >= len(spec.Args) || spec.Args[entrypoint+1] != "/reploy-probe" {
		return spec, fmt.Errorf("Podman private home requires the fixed Reploy startup verifier")
	}
	home := -1
	for index := 1; index+1 < entrypoint; index++ {
		if spec.Args[index] == "--tmpfs" && strings.HasPrefix(spec.Args[index+1], "/mnt/reploy-home:") {
			if home >= 0 {
				return spec, fmt.Errorf("duplicate private home mount")
			}
			home = index + 1
		}
	}
	if home < 0 {
		return spec, fmt.Errorf("Podman private home mount is missing")
	}
	mount, command, err := podmanTemporaryHome(spec.Args[home], spec.Args[entrypoint+3:])
	if err != nil {
		return spec, err
	}
	args := append([]string{}, spec.Args[:entrypoint+3]...)
	args[home] = mount
	for index := 1; index+1 < entrypoint; index++ {
		if args[index] == "--tmpfs" {
			args[index+1] = podmanPrivateDirectoryMaskV1(args[index+1])
		}
	}
	// Insert setup authority among engine options, never into application argv.
	args = append(args[:entrypoint], append([]string{"--cap-add", "CHOWN"}, args[entrypoint:]...)...)
	spec.Args = append(args, command...)
	return spec, nil
}

func composePodmanField(node *yaml.Node, key string) (*yaml.Node, error) {
	var found *yaml.Node
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected Compose mapping")
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			if found != nil {
				return nil, fmt.Errorf("duplicate Compose field %q", key)
			}
			found = node.Content[index+1]
		}
	}
	return found, nil
}

func preparePodmanTemporaryHomeCompose(service *yaml.Node) error {
	fields := map[string]*yaml.Node{}
	for _, key := range []string{"entrypoint", "command", "tmpfs", "cap_add", "user"} {
		node, err := composePodmanField(service, key)
		if err != nil {
			return err
		}
		fields[key] = node
	}
	entrypoint := fields["entrypoint"]
	if entrypoint == nil {
		return nil // Base-only models have no workload and are never started.
	}
	if entrypoint.Kind != yaml.SequenceNode || len(entrypoint.Content) != 1 || entrypoint.Content[0].Value != "/reploy-probe" ||
		fields["user"] == nil || fields["user"].Value != "0:0" {
		return fmt.Errorf("Podman private home requires the fixed root-run Reploy startup verifier")
	}
	for _, key := range []string{"command", "tmpfs", "cap_add"} {
		if fields[key] == nil || fields[key].Kind != yaml.SequenceNode {
			return fmt.Errorf("Podman private home requires canonical Compose %s", key)
		}
		for _, value := range fields[key].Content {
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return fmt.Errorf("Compose %s requires string values", key)
			}
		}
	}
	var home *yaml.Node
	for _, mount := range fields["tmpfs"].Content {
		mount.Value = podmanPrivateDirectoryMaskV1(mount.Value)
		if strings.HasPrefix(mount.Value, "/mnt/reploy-home:") {
			if home != nil {
				return fmt.Errorf("duplicate Compose private home mount")
			}
			home = mount
		}
	}
	if home == nil {
		return fmt.Errorf("Compose private home mount is missing")
	}
	var command []string
	for _, value := range fields["command"].Content {
		command = append(command, value.Value)
	}
	mount, _, err := podmanTemporaryHome(home.Value, command)
	if err != nil {
		return err
	}
	home.Value = mount
	argument := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: podmanPrepareTemporaryHome}
	fields["command"].Content = slices.Insert(fields["command"].Content, 1, argument)
	fields["cap_add"].Content = append(fields["cap_add"].Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "CHOWN"})
	return nil
}

// The qualified Podman 6.1.4+ path must explicitly disable its default
// copy-up for Reploy's empty metadata shadow mounts. Keeping copied host
// files can also exhaust the deliberately small mask filesystem.
// https://docs.podman.io/en/stable/markdown/podman-run.1.html
func podmanPrivateDirectoryMaskV1(mount string) string {
	if strings.HasSuffix(mount, ":"+privateRuntimeDirectoryMaskOptionsV1) {
		return mount + ",notmpcopyup"
	}
	return mount
}
