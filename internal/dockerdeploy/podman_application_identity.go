package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Docker's ordinary single-user contract uses the host account's numeric IDs.
// Podman 6.1.4 instead maps that account to container root by default. Keep the
// common contract through keep-id, not ownership changes to the user's files.
// Explicit isolated service mappings belong to the multi-identity policy;
// they must never fall through to this same-account default.
func (client *podmanHealthcheckClient) applicationUserNamespace(ctx context.Context, command []string) (string, error) {
	uid, gid, err := podmanApplicationIdentity(command)
	if err != nil {
		return "", err
	}
	data, err := client.request(ctx, http.MethodGet, "/v"+client.version+"/libpod/info", nil)
	if err != nil {
		return "", fmt.Errorf("identify Podman host-file identity: %w", err)
	}
	namespace, err := podmanSameIDNamespace(data, uid, gid, os.Geteuid(), os.Getegid())
	if err == nil && namespace != "" && !supportsNativePodmanBasePull(client.version) {
		return "", fmt.Errorf("same-ID application creation requires the qualified Podman 6.1.4 or newer API")
	}
	return namespace, err
}

func podmanApplicationIdentity(command []string) (uint32, uint32, error) {
	if len(command) < 2 || (command[0] != "sandbox-exec" && command[0] != "restricted-exec") {
		return 0, 0, fmt.Errorf("Podman identity requires the trusted application launcher")
	}
	separator := slices.Index(command, "--")
	if separator < 1 || separator == len(command)-1 {
		return 0, 0, fmt.Errorf("Podman identity requires a complete application command")
	}
	ids := map[string]uint32{}
	for i := 1; i < separator; i++ {
		if command[i] == podmanPrepareTemporaryHome || command[i] == "--private-environment" {
			continue
		}
		if i+1 >= separator {
			return 0, 0, fmt.Errorf("incomplete trusted application identity option")
		}
		key, value := command[i], command[i+1]
		i++
		if key != "--uid" && key != "--gid" {
			continue
		}
		n, err := strconv.ParseUint(value, 10, 32)
		_, duplicate := ids[key]
		if err != nil || n == 0xffffffff || strconv.FormatUint(n, 10) != value || duplicate {
			return 0, 0, fmt.Errorf("invalid trusted application identity")
		}
		ids[key] = uint32(n)
	}
	if len(ids) != 2 {
		return 0, 0, fmt.Errorf("trusted application UID/GID are required")
	}
	return ids["--uid"], ids["--gid"], nil
}

func podmanSameIDNamespace(data []byte, uid, gid uint32, callerUID, callerGID int) (string, error) {
	type mapping struct {
		Container uint32 `json:"container_id"`
		Host      uint32 `json:"host_id"`
		Size      uint32 `json:"size"`
	}
	var info struct {
		Host struct {
			Security   struct{ Rootless *bool }
			IDMappings struct{ UIDMap, GIDMap []mapping }
		}
	}
	if json.Unmarshal(data, &info) != nil || info.Host.Security.Rootless == nil {
		return "", fmt.Errorf("Podman did not establish its rootless/rootful identity mode")
	}
	if !*info.Host.Security.Rootless {
		return "", nil // Retain the existing rootful path; never disable admin remapping.
	}
	matches := func(entries []mapping, id uint32) bool {
		count := 0
		for _, entry := range entries {
			if entry.Size == 0 {
				return false
			}
			if entry.Container == 0 {
				if entry.Host != id || entry.Size != 1 {
					return false
				}
				count++
			}
		}
		return count == 1
	}
	if callerUID <= 0 || callerGID < 0 || uint64(uid) != uint64(callerUID) || uint64(gid) != uint64(callerGID) ||
		!matches(info.Host.IDMappings.UIDMap, uid) || !matches(info.Host.IDMappings.GIDMap, gid) {
		return "", fmt.Errorf("rootless Podman host-file access requires the engine owner and application to use the invoking account's UID/GID; select that account's engine, or use a rootful engine for a separate service account")
	}
	return "keep-id:uid=" + strconv.FormatUint(uint64(uid), 10) + ",gid=" + strconv.FormatUint(uint64(gid), 10), nil
}

func podmanApplicationCommand(spec CommandSpec) ([]string, error) {
	index := slices.Index(spec.Args, "--entrypoint")
	if index < 1 || index+4 >= len(spec.Args) || spec.Args[index+1] != "/reploy-probe" {
		return nil, fmt.Errorf("Podman identity requires the fixed Reploy application entrypoint")
	}
	// Everything after this boundary belongs to the trusted launcher or its
	// payload. Never interpret forwarded application flags as engine options.
	return slices.Clone(spec.Args[index+3:]), nil
}

func podmanNamespaceConflict(value string) error {
	return fmt.Errorf("explicit Podman user namespace %q cannot use the ordinary same-ID application path", strings.TrimSpace(value))
}
