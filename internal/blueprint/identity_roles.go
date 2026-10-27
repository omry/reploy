package blueprint

import (
	"fmt"

	"github.com/omry/reploy/internal/runtimeidentity"
)

func validateIdentityRoleReference(policy *IdentityPolicyV1, role, field string) error {
	if policy == nil {
		if role != "" {
			return fmt.Errorf("%s requires %s", field, identityPolicyPathV1)
		}
		return nil
	}
	if err := runtimeidentity.ValidateUserName(role); err != nil {
		return fmt.Errorf("%s must name a declared role: %w", field, err)
	}
	if _, found := policy.Roles[role]; !found {
		return fmt.Errorf("%s references undeclared role %q", field, role)
	}
	return nil
}

// The application profiles and qualified command references are the complete
// inventory. Commands inherit the profile's role; no second selector is stored.
func (environment Environment) validateIdentityRoleInventory() error {
	policy := environment.Runtime.IdentityPolicy
	for _, application := range sortedKeys(environment.Applications) {
		for _, name := range sortedKeys(environment.Applications[application].Executables) {
			field := "environment.applications." + application + ".executables." + name + ".role"
			if err := validateIdentityRoleReference(policy, environment.Applications[application].Executables[name].Role, field); err != nil {
				return err
			}
		}
	}
	if policy != nil {
		for _, name := range sortedKeys(environment.Commands) {
			command := environment.Commands[name]
			if _, _, found := environment.ResolveExecutableProfile(command.Executable); !found {
				return fmt.Errorf("environment.commands.%s references missing qualified executable %q", name, command.Executable)
			}
		}
	}
	return nil
}
