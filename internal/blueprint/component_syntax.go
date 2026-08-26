package blueprint

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func (base *BaseSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "base", map[string]bool{
		"image": true, "exports": true,
	}); err != nil {
		return err
	}
	type plain BaseSyntax
	return node.Decode((*plain)(base))
}

func (packages *EnvironmentPackagesSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "environment packages", map[string]bool{"os": true}); err != nil {
		return err
	}
	type plain EnvironmentPackagesSyntax
	return node.Decode((*plain)(packages))
}

func (application *ApplicationSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "application", map[string]bool{
		"packages": true, "options": true, "executables": true,
	}); err != nil {
		return err
	}
	type plain ApplicationSyntax
	return node.Decode((*plain)(application))
}

func (packages *ApplicationPackagesSyntax) UnmarshalYAML(node *yaml.Node) error {
	present, err := validateSyntaxMapping(node, "application packages", map[string]bool{
		"os": true, "python": true, "tools": true,
	})
	if err != nil {
		return err
	}
	type plain ApplicationPackagesSyntax
	if err := node.Decode((*plain)(packages)); err != nil {
		return err
	}
	packages.ToolsFieldPresent = present["tools"]
	return nil
}

func (packages *PythonPackagesSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "Python packages", map[string]bool{
		"interpreter": true, "requirements": true,
	}); err != nil {
		return err
	}
	type plain PythonPackagesSyntax
	return node.Decode((*plain)(packages))
}

func (option *ApplicationOptionSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "application option", map[string]bool{
		"description": true, "packages": true,
	}); err != nil {
		return err
	}
	type plain ApplicationOptionSyntax
	return node.Decode((*plain)(option))
}

func (export *ExecutableExportSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "executable export", map[string]bool{"executable": true}); err != nil {
		return err
	}
	type plain ExecutableExportSyntax
	return node.Decode((*plain)(export))
}

func (executable *ExecutableSyntax) UnmarshalYAML(node *yaml.Node) error {
	if _, err := validateSyntaxMapping(node, "executable profile", map[string]bool{
		"source": true, "binary": true, "order": true, "argv_prefix": true, "argv_suffix": true,
	}); err != nil {
		return err
	}
	type plain ExecutableSyntax
	return node.Decode((*plain)(executable))
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
