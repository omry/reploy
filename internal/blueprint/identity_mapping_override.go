package blueprint

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// IdentityMappingOverrideV1 is sparse deployment input, never host authority.
// A supplied array replaces the complete corresponding declaration array.
type IdentityMappingOverrideV1 struct {
	Schema  *int                 `yaml:"schema,omitempty"`
	Profile *MappingScalarV1     `yaml:"profile,omitempty"`
	UIDs    *[]MappingIntervalV1 `yaml:"uids,omitempty"`
	GIDs    *[]MappingIntervalV1 `yaml:"gids,omitempty"`
}

func (value MappingScalarV1) MarshalYAML() (any, error) {
	if _, ok := value.integer(); ok {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: value.Value}, nil
	}
	if value.Kind == "string" || value.Kind == "missing" || value.Kind == "expression" {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value.Value}, nil
	}
	return nil, fmt.Errorf("%s cannot encode an invalid scalar", identityMappingPathV1)
}

func (override *IdentityMappingOverrideV1) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, identityMappingPathV1, map[string]bool{
		"schema": true, "profile": true, "uids": true, "gids": true,
	}); err != nil {
		return err
	}
	for i := 0; i < len(node.Content); i += 2 {
		name, value := node.Content[i].Value, node.Content[i+1]
		if value.Tag == "!!null" || name == "schema" && value.Tag != "!!int" {
			return fmt.Errorf("%s.%s has an invalid type", identityMappingPathV1, name)
		}
		if (name == "uids" || name == "gids") && value.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s.%s must be an array", identityMappingPathV1, name)
		}
	}
	type plain IdentityMappingOverrideV1
	if err := node.Decode((*plain)(override)); err != nil {
		return err
	}
	return override.Validate()
}

func (override *IdentityMappingOverrideV1) Validate() error {
	if override == nil {
		return nil
	}
	missing := MappingScalarV1{Kind: "missing", Value: "???"}
	placeholder := []MappingIntervalV1{{missing, missing, missing}}
	_, err := (&IdentityMappingV1{Schema: 1, Profile: missing, UIDs: placeholder, GIDs: placeholder}).WithOverride(override)
	return err
}

// WithOverride validates portable intent without requiring effective values.
// It does not modify either input or resolve a leaf that an override replaces.
func (mapping *IdentityMappingV1) WithOverride(override *IdentityMappingOverrideV1) (*IdentityMappingV1, error) {
	if mapping == nil {
		if override != nil {
			return nil, fmt.Errorf("%s override requires a blueprint declaration", identityMappingPathV1)
		}
		return nil, nil
	}
	if err := mapping.validate(); err != nil {
		return nil, err
	}
	result := *mapping
	if override != nil {
		if override.Schema != nil {
			result.Schema = *override.Schema
		}
		if override.Profile != nil {
			result.Profile = *override.Profile
		}
		if override.UIDs != nil {
			result.UIDs = *override.UIDs
		}
		if override.GIDs != nil {
			result.GIDs = *override.GIDs
		}
	}
	if err := result.validate(); err != nil {
		return nil, err
	}
	result.UIDs = slices.Clone(result.UIDs)
	result.GIDs = slices.Clone(result.GIDs)
	return &result, nil
}

// ResolveIdentityMappingV1 supplies a pure operation-specific candidate. It
// grants no reservation or execution admission and leaves portable intent intact.
func ResolveIdentityMappingV1(document Document, override *IdentityMappingOverrideV1) (*IdentityMappingV1, error) {
	mapping := document.Environment.Runtime.IdentityMapping
	if err := mapping.validateVariables(document.Environment.Vars); err != nil {
		return nil, fmt.Errorf("environment %q: %w", document.Environment.ID, err)
	}
	result, err := mapping.WithOverride(override)
	if err != nil {
		return nil, fmt.Errorf("environment %q: %w", document.Environment.ID, err)
	}
	if result == nil {
		return nil, nil
	}
	var missing []string
	resolve := func(value *MappingScalarV1, path string) {
		if value.Kind == "expression" {
			if match := wholeInterpolationPattern.FindStringSubmatch(value.Value); match != nil {
				binding, found := result.Variables[match[1]]
				if !found {
					binding = MappingScalarV1{Kind: "missing", Value: "???"}
				}
				*value = binding
			} else {
				unresolved := false
				invalid := false
				text := interpolationPattern.ReplaceAllStringFunc(value.Value, func(token string) string {
					name := interpolationPattern.FindStringSubmatch(token)[1]
					binding, found := result.Variables[name]
					if !found || binding.Kind == "missing" || binding.Kind == "expression" {
						unresolved = true
					}
					invalid = invalid || binding.Kind == "invalid" || binding.Kind == "null"
					return binding.Value
				})
				*value = MappingScalarV1{Kind: "string", Value: text}
				if invalid {
					value.Kind = "invalid"
				}
				if unresolved {
					value.Kind = "missing"
				}
			}
		}
		if value.Kind == "missing" || value.Kind == "expression" {
			missing = append(missing, path)
		}
	}
	resolve(&result.Profile, identityMappingPathV1+".profile")
	for i, values := range [][]MappingIntervalV1{result.UIDs, result.GIDs} {
		for j := range values {
			prefix := fmt.Sprintf("%s.%s[%d].", identityMappingPathV1, []string{"uids", "gids"}[i], j)
			resolve(&values[j].ContainerStart, prefix+"container_start")
			resolve(&values[j].HostStart, prefix+"host_start")
			resolve(&values[j].Count, prefix+"count")
		}
	}
	if len(missing) != 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("environment %q: required mapping values unresolved at %s; supply deployment overrides", document.Environment.ID, strings.Join(missing, ", "))
	}
	result.Variables = nil
	if err := result.validate(); err != nil {
		return nil, fmt.Errorf("environment %q: %w", document.Environment.ID, err)
	}
	return canonicalIdentityMappingV1(result), nil
}
