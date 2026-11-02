package blueprint

import (
	"fmt"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/runtimeidentity"
)

// MountMetadata describes creation intent, never repair of an existing tree.
type MountMetadata struct {
	Owner       string `yaml:"owner" json:",omitempty"`
	Group       string `yaml:"group" json:",omitempty"`
	Permissions string `yaml:"permissions" json:",omitempty"`
}

type MountMetadataIdentity struct {
	UID         uint32
	GID         uint32
	Permissions *uint32
}

func (mount EnvironmentMount) HasMetadata() bool {
	return mount.MountMetadata != (MountMetadata{}) || len(mount.Directories) != 0 || len(mount.Files) != 0
}

func (environment Environment) ImageRootWritable() bool {
	root := environment.Mounts["root"]
	return root.Target == "/" && root.Writable
}

func (metadata MountMetadata) validate(policy *IdentityPolicyV1) error {
	for field, name := range map[string]string{"owner": metadata.Owner, "group": metadata.Group} {
		if name == "" {
			continue
		}
		if err := runtimeidentity.ValidateUserName(name); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		if policy != nil {
			kind := "user"
			_, known := policy.Users[name]
			if field == "group" {
				kind = "group"
				_, known = policy.Groups[name]
			}
			if !known {
				return fmt.Errorf("%s %q must name a declared %s, not a role", field, name, kind)
			}
		}
	}
	if metadata.Permissions != "" {
		if len(metadata.Permissions) != 4 {
			return fmt.Errorf("permissions must contain four octal digits")
		}
		for _, digit := range metadata.Permissions {
			if digit < '0' || digit > '7' {
				return fmt.Errorf("permissions must contain four octal digits")
			}
		}
	}
	return nil
}

// ResolveMountMetadata uses the already resolved container entry identity.
// It performs no NSS lookup and cannot turn an image role into a user.
func ResolveMountMetadata(metadata MountMetadata, policy *IdentityPolicyV1, context runtimeidentity.IdentityV1) (MountMetadataIdentity, error) {
	if err := policy.Validate(); err != nil {
		return MountMetadataIdentity{}, err
	}
	if err := metadata.validate(policy); err != nil {
		return MountMetadataIdentity{}, err
	}
	if err := runtimeidentity.ValidateIdentityV1(context); err != nil {
		return MountMetadataIdentity{}, fmt.Errorf("mount runtime context: %w", err)
	}
	uid, gid := context.UID, context.GID
	owner := metadata.Owner
	if owner == "" {
		owner = context.Username
	}
	if policy != nil {
		user, found := policy.Users[owner]
		if !found {
			return MountMetadataIdentity{}, fmt.Errorf("mount owner %q is not a declared user", owner)
		}
		uid, gid = user.UID, policy.Groups[user.PrimaryGroup]
		if metadata.Owner == "" && (uid != context.UID || gid != context.GID) {
			return MountMetadataIdentity{}, fmt.Errorf("mount default owner differs from the resolved runtime context")
		}
	} else if owner != context.Username {
		return MountMetadataIdentity{}, fmt.Errorf("mount owner %q requires a declared identity policy", owner)
	}
	if metadata.Group != "" {
		if policy == nil {
			return MountMetadataIdentity{}, fmt.Errorf("explicit mount group requires a declared identity policy")
		}
		gid = policy.Groups[metadata.Group]
	}
	u, _ := strconv.ParseUint(uid, 10, 32)
	g, _ := strconv.ParseUint(gid, 10, 32)
	result := MountMetadataIdentity{UID: uint32(u), GID: uint32(g)}
	if metadata.Permissions != "" {
		mode, _ := strconv.ParseUint(metadata.Permissions, 8, 12)
		value := uint32(mode)
		result.Permissions = &value
	}
	return result, nil
}

func validateMountChildren(mount EnvironmentMount, policy *IdentityPolicyV1) error {
	if err := mount.MountMetadata.validate(policy); err != nil {
		return err
	}
	for _, children := range []map[string]MountMetadata{mount.Directories, mount.Files} {
		for _, name := range sortedKeys(children) {
			if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n\t") || name == ".." || strings.HasPrefix(name, "../") {
				return fmt.Errorf("object %q must be a normalized relative Linux path", name)
			}
			if err := children[name].validate(policy); err != nil {
				return fmt.Errorf("object %q: %w", name, err)
			}
			if _, directory := mount.Directories[name]; directory {
				if _, file := mount.Files[name]; file {
					return fmt.Errorf("object %q cannot be both a directory and a file", name)
				}
			}
			for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
				if _, file := mount.Files[parent]; file {
					return fmt.Errorf("object %q has file ancestor %q", name, parent)
				}
			}
		}
	}
	return nil
}

func validateMetadataTarget(target string) error {
	if !path.IsAbs(target) || path.Clean(target) != target || strings.Contains(target, `\`) {
		return fmt.Errorf("managed metadata requires a normalized absolute Linux mount target")
	}
	return validateEnvironmentMountTarget(target)
}

func (document Document) ValidateMountDeclarations() error {
	policy := document.Environment.Runtime.IdentityPolicy
	for _, name := range sortedKeys(document.Environment.Mounts) {
		mount := document.Environment.Mounts[name]
		if mount.Target == "/" {
			if name != "root" || mount.UpdatePolicy != "" || mount.HasMetadata() {
				return fmt.Errorf("intrinsic root requires name root and cannot have managed metadata or update_policy")
			}
		}
		if mount.HasMetadata() {
			if err := validateMetadataTarget(mount.Target); err != nil {
				return err
			}
		}
		if err := validateMountChildren(mount, policy); err != nil {
			return fmt.Errorf("environment.mounts.%s: %w", name, err)
		}
		bindings := 0
		for _, binding := range document.Docker.Mounts {
			if binding.Extends != environmentMountReferencePrefix+name {
				continue
			}
			bindings++
			if mount.Target == "/" {
				return fmt.Errorf("intrinsic root cannot have a Docker mount binding")
			}
			if mount.HasMetadata() || binding.Contract.HasMetadata() {
				if !reflect.DeepEqual(mount, binding.Contract) || binding.Mode == MountBind {
					return fmt.Errorf("managed metadata requires an identical managed mount contract")
				}
				if err := validateMount("managed metadata", binding); err != nil {
					return err
				}
			}
		}
		if mount.HasMetadata() && bindings != 1 {
			return fmt.Errorf("managed metadata requires exactly one Docker mount binding")
		}
	}
	for _, binding := range document.Docker.Mounts {
		if err := validateMountChildren(binding.Contract, policy); err != nil {
			return err
		}
		if binding.Contract.HasMetadata() {
			name := strings.TrimPrefix(binding.Extends, environmentMountReferencePrefix)
			mount, exists := document.Environment.Mounts[name]
			if !strings.HasPrefix(binding.Extends, environmentMountReferencePrefix) || !exists || !reflect.DeepEqual(mount, binding.Contract) {
				return fmt.Errorf("managed metadata requires an exact environment mount contract")
			}
		}
		if binding.Contract.Target == "/" {
			return fmt.Errorf("intrinsic root cannot be an external Docker mount")
		}
	}
	for _, command := range document.Environment.Commands {
		for name := range command.Mounts {
			if document.Environment.Mounts[name].Target == "/" {
				return fmt.Errorf("command cannot override the intrinsic root policy")
			}
		}
	}
	return nil
}

type ManagedMountObject struct {
	Path      string
	Directory bool
	Identity  MountMetadataIdentity
}

// ResolveManagedMountObjects freezes declarations only. It opens no paths.
func ResolveManagedMountObjects(mount EnvironmentMount, policy *IdentityPolicyV1, context runtimeidentity.IdentityV1) ([]ManagedMountObject, error) {
	if err := validateMountChildren(mount, policy); err != nil {
		return nil, err
	}
	if !mount.HasMetadata() {
		return nil, nil
	}
	if err := validateMetadataTarget(mount.Target); err != nil {
		return nil, err
	}
	root, err := ResolveMountMetadata(mount.MountMetadata, policy, context)
	if err != nil {
		return nil, err
	}
	result := []ManagedMountObject{{Path: ".", Directory: true, Identity: root}}
	for index, children := range []map[string]MountMetadata{mount.Directories, mount.Files} {
		names := sortedKeys(children)
		if index == 0 {
			sort.SliceStable(names, func(i, j int) bool { return strings.Count(names[i], "/") < strings.Count(names[j], "/") })
		}
		for _, name := range names {
			identity, err := ResolveMountMetadata(children[name], policy, context)
			if err != nil {
				return nil, fmt.Errorf("object %q: %w", name, err)
			}
			result = append(result, ManagedMountObject{Path: name, Directory: index == 0, Identity: identity})
		}
	}
	return result, nil
}
