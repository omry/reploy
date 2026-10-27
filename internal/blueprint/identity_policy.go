package blueprint

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/runtimeidentity"
	"gopkg.in/yaml.v3"
)

const identityPolicyPathV1 = "environment.runtime.identity_policy"
const IdentityPolicySchemaV1 = "identity-policy-v1"

// IdentityPolicyV1 is portable application intent. It does not admit execution.
type IdentityPolicyV1 struct {
	Schema string                          `json:"schema"`
	Users  map[string]IdentityPolicyUserV1 `json:"users"`
	Groups map[string]string               `json:"groups"`
	Roles  map[string]IdentityPolicyRoleV1 `json:"roles"`
}

type IdentityPolicyUserV1 struct {
	UID                 string   `json:"uid"`
	FullName            *string  `json:"full_name,omitempty"`
	PrimaryGroup        string   `json:"primary_group"`
	SupplementaryGroups []string `json:"supplementary_groups"`
}

type IdentityPolicyRoleV1 struct {
	User         string   `json:"user"`
	Capabilities []string `json:"capabilities"`
}

// policyFields checks raw presence and duplicates before constructing maps.
func policyFields(node *yaml.Node, path string, allowed ...string) (map[string]*yaml.Node, error) {
	names := map[string]bool{}
	for _, name := range allowed {
		names[name] = true
	}
	if _, err := validateSyntaxMapping(node, path, names); err != nil {
		return nil, err
	}
	fields := map[string]*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		fields[node.Content[i].Value] = node.Content[i+1]
	}
	return fields, nil
}

func policyNamedFields(node *yaml.Node, path string) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping", path)
	}
	names := []string{}
	for i := 0; i < len(node.Content); i += 2 {
		if err := runtimeidentity.ValidateUserName(node.Content[i].Value); err != nil {
			return nil, fmt.Errorf("%s name: %w", path, err)
		}
		names = append(names, node.Content[i].Value)
	}
	return policyFields(node, path, names...)
}

func policyID(value string) bool {
	n, err := strconv.ParseUint(value, 10, 32)
	return err == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == value
}

func policyYAMLID(node *yaml.Node, path string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 || !policyID(node.Value) {
		return "", fmt.Errorf("%s must be a literal canonical decimal integer from 0 through 4294967294", path)
	}
	return node.Value, nil
}

func policyYAMLText(node *yaml.Node, path string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("%s must be a literal string", path)
	}
	return node.Value, nil
}

func policyYAMLArray(node *yaml.Node, path string) ([]string, error) {
	result := []string{}
	if node == nil {
		return result, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s must be an array", path)
	}
	for _, item := range node.Content {
		value, err := policyYAMLText(item, path)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return result, nil
}

func (policy *IdentityPolicyV1) UnmarshalYAML(node *yaml.Node) error {
	fields, err := policyFields(node, identityPolicyPathV1, "schema", "users", "groups", "roles")
	if err != nil {
		return err
	}
	for _, name := range []string{"schema", "users", "groups", "roles"} {
		if fields[name] == nil {
			return fmt.Errorf("%s.%s is required", identityPolicyPathV1, name)
		}
	}
	schema, err := policyYAMLID(fields["schema"], identityPolicyPathV1+".schema")
	if err != nil || schema != "1" {
		return fmt.Errorf("%s.schema must be the integer 1", identityPolicyPathV1)
	}
	result := IdentityPolicyV1{Schema: IdentityPolicySchemaV1, Users: map[string]IdentityPolicyUserV1{}, Groups: map[string]string{}, Roles: map[string]IdentityPolicyRoleV1{}}
	groups, err := policyNamedFields(fields["groups"], identityPolicyPathV1+".groups")
	if err != nil {
		return err
	}
	for name, node := range groups {
		id, err := policyYAMLID(node, identityPolicyPathV1+".groups."+name)
		if err != nil {
			return err
		}
		result.Groups[name] = id
	}
	users, err := policyNamedFields(fields["users"], identityPolicyPathV1+".users")
	if err != nil {
		return err
	}
	for name, node := range users {
		path := identityPolicyPathV1 + ".users." + name
		user := IdentityPolicyUserV1{PrimaryGroup: name, SupplementaryGroups: []string{}}
		if node.Kind == yaml.ScalarNode {
			user.UID, err = policyYAMLID(node, path)
		} else {
			var f map[string]*yaml.Node
			f, err = policyFields(node, path, "uid", "full_name", "primary_group", "supplementary_groups")
			if err != nil {
				return err
			}
			user.UID, err = policyYAMLID(f["uid"], path+".uid")
			if err != nil {
				return err
			}
			if n := f["full_name"]; n != nil {
				var text string
				text, err = policyYAMLText(n, path+".full_name")
				user.FullName = &text
				if err != nil {
					return err
				}
			}
			if n := f["primary_group"]; n != nil {
				user.PrimaryGroup, err = policyYAMLText(n, path+".primary_group")
				if err != nil {
					return err
				}
			}
			user.SupplementaryGroups, err = policyYAMLArray(f["supplementary_groups"], path+".supplementary_groups")
		}
		if err != nil {
			return err
		}
		result.Users[name] = user
	}
	roles, err := policyNamedFields(fields["roles"], identityPolicyPathV1+".roles")
	if err != nil {
		return err
	}
	for name, node := range roles {
		path := identityPolicyPathV1 + ".roles." + name
		f, err := policyFields(node, path, "user", "capabilities")
		if err != nil {
			return err
		}
		user, err := policyYAMLText(f["user"], path+".user")
		if err != nil {
			return err
		}
		caps, err := policyYAMLArray(f["capabilities"], path+".capabilities")
		if err != nil {
			return err
		}
		result.Roles[name] = IdentityPolicyRoleV1{User: user, Capabilities: caps}
	}
	if err := result.Validate(); err != nil {
		return err
	}
	*policy = result
	return nil
}

// Validate protects resolution, persisted policy and image-evidence boundaries.
func (policy *IdentityPolicyV1) Validate() error {
	if policy == nil {
		return nil
	}
	fail := func(reason string) error { return fmt.Errorf("%s: %s", identityPolicyPathV1, reason) }
	if policy.Schema != IdentityPolicySchemaV1 {
		return fail("unsupported schema")
	}
	if len(policy.Users) == 0 || len(policy.Groups) == 0 || len(policy.Roles) == 0 || len(policy.Users)+len(policy.Groups)+len(policy.Roles) > 1024 {
		return fail("require nonempty users, groups and roles, at most 1024 combined")
	}
	gids := map[string]bool{}
	for name, id := range policy.Groups {
		if runtimeidentity.ValidateUserName(name) != nil || !policyID(id) || gids[id] {
			return fail("invalid or duplicate group name/GID")
		}
		gids[id] = true
	}
	uids := map[string]bool{}
	for name, user := range policy.Users {
		if uids[user.UID] {
			return fail("duplicate user UID")
		}
		uids[user.UID] = true
		gid, ok := policy.Groups[user.PrimaryGroup]
		if !ok {
			return fail("user " + name + " primary_group must select a declared group")
		}
		if user.FullName != nil && (!utf8.ValidString(*user.FullName) || len(*user.FullName) > 256 || strings.ContainsAny(*user.FullName, "\x00\r\n")) {
			return fail("user " + name + " full_name must be UTF-8 text of at most 256 bytes without NUL, CR or LF")
		}
		if user.SupplementaryGroups == nil || len(user.SupplementaryGroups) > 64 || !slices.IsSorted(user.SupplementaryGroups) {
			return fail("user " + name + " supplementary_groups must be a sorted array of at most 64 additional groups")
		}
		extra := []string{}
		for i, group := range user.SupplementaryGroups {
			id, ok := policy.Groups[group]
			if !ok || group == user.PrimaryGroup || i > 0 && group == user.SupplementaryGroups[i-1] {
				return fail("user " + name + " supplementary_groups must be unique declared additional groups")
			}
			extra = append(extra, id)
		}
		slices.SortFunc(extra, func(a, b string) int {
			x, _ := strconv.ParseUint(a, 10, 32)
			y, _ := strconv.ParseUint(b, 10, 32)
			return cmp.Compare(x, y)
		})
		if err := runtimeidentity.ValidateIdentityV1(runtimeidentity.IdentityV1{Username: name, UID: user.UID, GID: gid, SupplementaryGIDs: extra}); err != nil {
			return fail("user " + name + ": " + err.Error())
		}
	}
	for name, role := range policy.Roles {
		if runtimeidentity.ValidateUserName(name) != nil {
			return fail("invalid role name")
		}
		if _, ok := policy.Users[role.User]; !ok {
			return fail("role " + name + " must select a declared user")
		}
		if role.Capabilities == nil || !slices.IsSorted(role.Capabilities) {
			return fail("role " + name + " capabilities must be a sorted array")
		}
		for i, capability := range role.Capabilities {
			if !linuxCapabilityNameV1(capability) || i > 0 && capability == role.Capabilities[i-1] {
				return fail("role " + name + " capabilities must be unique recognized literal Linux CAP_ names")
			}
		}
	}
	return nil
}
