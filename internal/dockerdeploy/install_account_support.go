package dockerdeploy

import (
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
)

type resolvedInstallOwner struct {
	Spec              string
	UID               uint32
	GID               uint32
	SupplementaryGIDs []uint32
	ContainerUser     string
}

const (
	installOwnerOnMissingCreate = "create"
	installOwnerOnMissingFail   = "fail"
)

var installLookupUser = user.Lookup
var installLookupGroup = user.LookupGroup
var installLookupUserGroupIDs = func(value *user.User) ([]string, error) { return value.GroupIds() }
var installRunCommandOutput = func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).CombinedOutput() }

func resolveInstallOwner(values map[string]string) (resolvedInstallOwner, error) {
	spec := strings.TrimSpace(values[reployInstallOwnerEnv])
	if spec == "" {
		return resolvedInstallOwner{}, fmt.Errorf("REPLOY_INSTALL_OWNER is required for system install")
	}
	uid, gid, err := parseInstallOwner(spec)
	if err != nil {
		return resolvedInstallOwner{}, err
	}
	groups, err := resolveInstallOwnerSupplementaryGIDs(spec, uid, gid)
	if err != nil {
		return resolvedInstallOwner{}, err
	}
	return resolvedInstallOwner{
		Spec: spec, UID: uid, GID: gid, SupplementaryGIDs: groups,
		ContainerUser: fmt.Sprintf("%d:%d", uid, gid),
	}, nil
}

func resolveInstallOwnerSupplementaryGIDs(spec string, uid uint32, gid uint32) ([]uint32, error) {
	userPart, _, _ := strings.Cut(spec, ":")
	if _, numeric := parseNumericInstallID(userPart); numeric {
		return []uint32{}, nil
	}
	lookedUp, err := installLookupUser(userPart)
	if err != nil {
		return nil, fmt.Errorf("resolve REPLOY_INSTALL_OWNER supplementary groups for user %q: %w", userPart, err)
	}
	values, err := installLookupUserGroupIDs(lookedUp)
	if err != nil {
		return nil, fmt.Errorf("resolve REPLOY_INSTALL_OWNER supplementary groups for user %q: %w", userPart, err)
	}
	groups := make([]uint32, 0, len(values))
	for _, value := range values {
		parsed, ok := parseNumericInstallID(value)
		if !ok {
			return nil, fmt.Errorf("resolved REPLOY_INSTALL_OWNER user has non-numeric supplementary GID %q: %s", value, spec)
		}
		groups = append(groups, parsed)
	}
	groups, err = normalizeSupplementaryGIDsV1(gid, groups)
	if err != nil {
		return nil, err
	}
	if uid != 0 {
		for _, group := range groups {
			if group == 0 {
				return nil, fmt.Errorf("REPLOY_INSTALL_OWNER non-root user must not belong to the root group: %s", spec)
			}
		}
	}
	return groups, nil
}

func installOwnerOnMissingPolicy(values map[string]string) string {
	if strings.TrimSpace(values[reployInstallOwnerOnMissing]) == installOwnerOnMissingCreate {
		return installOwnerOnMissingCreate
	}
	return installOwnerOnMissingFail
}

func installOwnerCreationSpecForResolveError(values map[string]string, resolveErr error) (string, error) {
	if installOwnerOnMissingPolicy(values) != installOwnerOnMissingCreate || !isUnknownInstallOwnerLookupError(resolveErr) {
		return "", resolveErr
	}
	return installOwnerCreationReadiness(values)
}

func installOwnerCreationReadiness(values map[string]string) (string, error) {
	userPart, groupPart, err := installOwnerNamedParts(values)
	if err != nil {
		return "", err
	}
	if _, err := installLookupUser(userPart); err != nil && !isUnknownUserError(err) {
		return "", fmt.Errorf("lookup install owner user %q: %w", userPart, err)
	}
	if _, err := installLookupGroup(groupPart); err != nil && !isUnknownGroupError(err) {
		return "", fmt.Errorf("lookup install owner group %q: %w", groupPart, err)
	}
	return userPart + ":" + groupPart, nil
}

func installOwnerNamedParts(values map[string]string) (string, string, error) {
	if installOwnerOnMissingPolicy(values) != installOwnerOnMissingCreate {
		return "", "", fmt.Errorf("%s is not %s", reployInstallOwnerOnMissing, installOwnerOnMissingCreate)
	}
	spec := strings.TrimSpace(values[reployInstallOwnerEnv])
	userPart, groupPart, hasGroup := strings.Cut(spec, ":")
	userPart, groupPart = strings.TrimSpace(userPart), strings.TrimSpace(groupPart)
	if !hasGroup {
		groupPart = userPart
	}
	if userPart == "" || groupPart == "" || strings.Contains(groupPart, ":") {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER must name both user and group for account creation: %s", spec)
	}
	if _, ok := parseNumericInstallID(userPart); ok {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER user must be named for account creation: %s", spec)
	}
	if _, ok := parseNumericInstallID(groupPart); ok {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER group must be named for account creation: %s", spec)
	}
	if userPart == "root" || groupPart == "root" || !isInstallSystemAccountName(userPart) || !isInstallSystemAccountName(groupPart) {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER must name safe non-root system accounts: %s", spec)
	}
	return userPart, groupPart, nil
}

func isInstallSystemAccountName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		switch {
		case index == 0:
			if char < 'a' || char > 'z' {
				return char == '_'
			}
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_', char == '-':
		case char == '$':
			return index == len(value)-1
		default:
			return false
		}
	}
	return true
}

func createMissingInstallOwner(values map[string]string) error {
	userPart, groupPart, err := installOwnerNamedParts(values)
	if err != nil {
		return err
	}
	if _, err := installLookupGroup(groupPart); err != nil {
		if !isUnknownGroupError(err) {
			return fmt.Errorf("lookup install owner group %q: %w", groupPart, err)
		}
		if err := runInstallAccountCommand("groupadd", "--system", groupPart); err != nil {
			return err
		}
	}
	if _, err := installLookupUser(userPart); err != nil {
		if !isUnknownUserError(err) {
			return fmt.Errorf("lookup install owner user %q: %w", userPart, err)
		}
		if err := runInstallAccountCommand("useradd", "--system", "--gid", groupPart, "--home-dir", "/nonexistent", "--no-create-home", "--shell", "/usr/sbin/nologin", userPart); err != nil {
			return err
		}
	}
	return nil
}

func isUnknownInstallOwnerLookupError(err error) bool {
	return isUnknownUserError(err) || isUnknownGroupError(err)
}
func isUnknownUserError(err error) bool {
	var unknown user.UnknownUserError
	return errors.As(err, &unknown)
}
func isUnknownGroupError(err error) bool {
	var unknown user.UnknownGroupError
	return errors.As(err, &unknown)
}

func runInstallAccountCommand(name string, args ...string) error {
	output, err := installRunCommandOutput(name, args...)
	if err == nil {
		return nil
	}
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, trimmed)
	}
	return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}

func parseInstallOwner(value string) (uint32, uint32, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, fmt.Errorf("REPLOY_INSTALL_OWNER must not be empty")
	}
	userPart, groupPart, hasGroup := strings.Cut(value, ":")
	uid, primaryGID, err := resolveInstallOwnerUser(userPart, value)
	if err != nil {
		return 0, 0, err
	}
	gid := primaryGID
	if hasGroup {
		gid, err = resolveInstallOwnerGroup(groupPart, value)
		if err != nil {
			return 0, 0, err
		}
	}
	if uid == 0 || gid == 0 {
		return 0, 0, fmt.Errorf("REPLOY_INSTALL_OWNER must not resolve to root: %s", value)
	}
	return uid, gid, nil
}

func resolveInstallOwnerUser(value string, original string) (uint32, uint32, error) {
	if id, ok := parseNumericInstallID(value); ok {
		return id, id, nil
	}
	lookedUp, err := installLookupUser(value)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve REPLOY_INSTALL_OWNER user %q: %w", value, err)
	}
	uid, uidOK := parseNumericInstallID(lookedUp.Uid)
	gid, gidOK := parseNumericInstallID(lookedUp.Gid)
	if !uidOK || !gidOK {
		return 0, 0, fmt.Errorf("resolved REPLOY_INSTALL_OWNER user has non-numeric ids: %s", original)
	}
	return uid, gid, nil
}

func resolveInstallOwnerGroup(value string, original string) (uint32, error) {
	if id, ok := parseNumericInstallID(value); ok {
		return id, nil
	}
	lookedUp, err := installLookupGroup(value)
	if err != nil {
		return 0, fmt.Errorf("resolve REPLOY_INSTALL_OWNER group %q: %w", value, err)
	}
	gid, ok := parseNumericInstallID(lookedUp.Gid)
	if !ok {
		return 0, fmt.Errorf("resolved REPLOY_INSTALL_OWNER group has non-numeric gid: %s", original)
	}
	return gid, nil
}

func parseNumericInstallID(value string) (uint32, bool) {
	id, err := strconv.ParseUint(value, 10, 32)
	return uint32(id), err == nil && id != uint64(runtimeIDUnchangedSentinelV1) && strconv.FormatUint(id, 10) == value
}
