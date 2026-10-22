package deploy

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"gopkg.in/yaml.v3"
)

type PackageOverridesRuntimeV1 struct {
	IdentityMapping *blueprint.IdentityMappingOverrideV1 `yaml:"identity_mapping,omitempty"`
}

func (overrides PackageOverridesV1) IdentityMappingOverride() *blueprint.IdentityMappingOverrideV1 {
	if overrides.Environment.Runtime == nil {
		return nil
	}
	return overrides.Environment.Runtime.IdentityMapping
}

// CheckIdentityMappingDeclaration preserves unresolved portable inputs. It
// neither resolves effective values nor grants reservations or admission.
func (overrides PackageOverridesV1) CheckIdentityMappingDeclaration(document blueprint.Document) error {
	if overrides.Environment.ID != document.Environment.ID {
		return fmt.Errorf("mapping overrides target environment %q, want %q", overrides.Environment.ID, document.Environment.ID)
	}
	_, err := document.Environment.Runtime.IdentityMapping.WithOverride(overrides.IdentityMappingOverride())
	return err
}

func (environment PackageOverridesEnvironmentV1) MarshalYAML() (any, error) {
	type plain PackageOverridesEnvironmentV1
	var node yaml.Node
	if err := node.Encode(plain(environment)); err != nil {
		return nil, err
	}
	if environment.Runtime != nil && environment.Runtime.IdentityMapping != nil && len(environment.PackageOverrides) == 0 {
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == "package_overrides" {
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				break
			}
		}
	}
	return &node, nil
}

// Inspect presence over the same captured bytes as the strict decoder. YAML
// pointer fields alone cannot distinguish an omitted field from explicit null.
func validateMappingOverridePresenceV1(content []byte, mappingInput bool) error {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return err
	}
	var inspect func(*yaml.Node, string) error
	type visit struct {
		node *yaml.Node
		path string
	}
	seen := map[visit]bool{}
	inspect = func(node *yaml.Node, path string) error {
		key := visit{node, path}
		if seen[key] {
			return nil
		}
		seen[key] = true
		if node.Kind == yaml.AliasNode {
			return inspect(node.Alias, path)
		}
		if node.Kind == yaml.SequenceNode {
			for _, source := range node.Content {
				if err := inspect(source, path); err != nil {
					return err
				}
			}
			return nil
		}
		if node.Kind != yaml.MappingNode {
			return nil // The existing strict decoder owns the ordinary schema.
		}
		for i := 0; i < len(node.Content); i += 2 {
			name, value := node.Content[i].Value, node.Content[i+1]
			if node.Content[i].Tag == "!!merge" {
				if err := inspect(value, path); err != nil {
					return err
				}
				continue
			}
			for value.Kind == yaml.AliasNode {
				value = value.Alias
			}
			child := path + "." + name
			if path == "" {
				child = name
			}
			if mappingInput && child == "environment.package_overrides" && value.Tag == "!!null" {
				return fmt.Errorf("overrides %s must use a mapping", child)
			}
			if child == "environment.runtime" || child == "environment.runtime.identity_mapping" {
				if value.Kind != yaml.MappingNode {
					return fmt.Errorf("overrides %s must use a mapping", child)
				}
			}
			if child == "environment" || child == "environment.runtime" {
				if err := inspect(value, child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if len(root.Content) == 0 {
		return nil
	}
	return inspect(root.Content[0], "")
}
