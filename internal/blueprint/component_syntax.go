package blueprint

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func (component *ComponentSyntax) UnmarshalYAML(node *yaml.Node) error {
	allowed := map[string]bool{
		"type": true, "image": true, "exports": true, "interpreter": true,
		"requirements": true, "packages": true, "options": true,
	}
	present, err := validateSyntaxMapping(node, "component", allowed)
	if err != nil {
		return err
	}
	type plain ComponentSyntax
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*component = ComponentSyntax(decoded)
	component.Present = present
	return nil
}

func (export *ExecutableExportSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "executable export", map[string]bool{"executable": true}); err != nil {
		return err
	}
	type plain ExecutableExportSyntax
	return node.Decode((*plain)(export))
}

func (requirement *CommandRequirementSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "command requirement", map[string]bool{
		"command": true, "version": true, "supplier": true,
	}); err != nil {
		return err
	}
	type plain CommandRequirementSyntax
	return node.Decode((*plain)(requirement))
}

func (option *ComponentOptionSyntax) UnmarshalYAML(node *yaml.Node) error {
	present, err := validateSyntaxMapping(node, "component option", map[string]bool{
		"description": true, "requirements": true, "packages": true,
	})
	if err != nil {
		return err
	}
	type plain ComponentOptionSyntax
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*option = ComponentOptionSyntax(decoded)
	option.Present = present
	return nil
}

func (request *APTPackageRequestSyntax) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if node.Tag != "!!str" {
			return fmt.Errorf("APT package entry must be a string or mapping")
		}
		request.Package = node.Value
		request.Exports = map[string]ExecutableExportSyntax{}
		return nil
	}
	if _, err := validateSyntaxMapping(node, "APT package entry", map[string]bool{
		"package": true, "exports": true,
	}); err != nil {
		return err
	}
	type plain APTPackageRequestSyntax
	if err := node.Decode((*plain)(request)); err != nil {
		return err
	}
	if request.Exports == nil {
		request.Exports = map[string]ExecutableExportSyntax{}
	}
	return nil
}

func validateSyntaxMapping(node *yaml.Node, subject string, allowed map[string]bool) (map[string]bool, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping", subject)
	}
	present := make(map[string]bool, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("%s field name must be a string", subject)
		}
		if !allowed[key.Value] {
			return nil, fmt.Errorf("field %s not found in type blueprint.%s", key.Value, subject)
		}
		if present[key.Value] {
			return nil, fmt.Errorf("%s contains duplicate field %q", subject, key.Value)
		}
		present[key.Value] = true
	}
	return present, nil
}
