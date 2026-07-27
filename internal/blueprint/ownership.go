package blueprint

import (
	"fmt"
	"strings"
)

const (
	ContributionProviderOS     = "os"
	ContributionProviderPython = "python"
)

func EnvironmentContributionID(provider string) string {
	return "environment/" + provider
}

func ApplicationID(application string) string {
	return "application/" + application
}

func ApplicationContributionID(application string, provider string) string {
	return ApplicationID(application) + "/" + provider
}

func ApplicationExecutableID(application string, executable string) string {
	return ApplicationID(application) + "/executable/" + executable
}

func ApplicationContributionOwner(id string, provider string) (string, bool) {
	parts := strings.Split(id, "/")
	if len(parts) != 3 || parts[0] != "application" || parts[2] != provider {
		return "", false
	}
	if validateProviderIdentifier("application contribution", parts[1]) != nil {
		return "", false
	}
	return parts[1], true
}

func ContributionRuntimeOwner(id string, provider string) string {
	if application, ok := ApplicationContributionOwner(id, provider); ok {
		return ApplicationID(application)
	}
	return id
}

func ValidateContributionID(field string, id string) error {
	parts := strings.Split(id, "/")
	switch {
	case len(parts) == 1 && parts[0] == "base":
		return nil
	case len(parts) == 2 && parts[0] == "environment":
		return validateContributionProvider(field, parts[1])
	case len(parts) == 3 && parts[0] == "application":
		if err := validateProviderIdentifier(field+" application", parts[1]); err != nil {
			return err
		}
		return validateContributionProvider(field, parts[2])
	default:
		return fmt.Errorf("%s must be base, environment/<provider>, or application/<application>/<provider>", field)
	}
}

// ValidateContributionReference accepts canonical ownership identities and the
// provider-local identifiers still used by low-level provider APIs. Blueprint
// resolution emits only canonical contribution IDs.
func ValidateContributionReference(field string, id string) error {
	if ValidateContributionID(field, id) == nil {
		return nil
	}
	return validateProviderIdentifier(field, id)
}

func (environment *Environment) RebuildProviderContributions() error {
	if environment == nil {
		return fmt.Errorf("environment is required")
	}
	components := map[string]Component{
		"base": {
			Type: ComponentTypeBase, Base: &environment.Base,
			Options: map[string]ComponentOption{}, Executables: map[string]Executable{},
		},
	}
	if len(environment.Packages.OS) != 0 {
		components[EnvironmentContributionID(ContributionProviderOS)] = Component{
			Type: ComponentTypeAPT, APT: &APTComponent{Packages: environment.Packages.OS},
			Options: map[string]ComponentOption{}, Executables: map[string]Executable{},
		}
	}
	for applicationName, application := range environment.Applications {
		if err := validateProviderIdentifier("environment application", applicationName); err != nil {
			return err
		}
		osOptions := map[string]ComponentOption{}
		pythonOptions := map[string]ComponentOption{}
		for optionName, option := range application.Options {
			if err := validateProviderIdentifier("application option", optionName); err != nil {
				return err
			}
			if len(option.Packages.OS) != 0 {
				osOptions[optionName] = ComponentOption{
					Description: option.Description, APTPackages: option.Packages.OS,
				}
			}
			if option.Packages.Python != nil {
				pythonOptions[optionName] = ComponentOption{
					Description:        option.Description,
					PythonRequirements: option.Packages.Python.Requirements,
				}
			}
		}
		if len(application.Packages.OS) != 0 || len(osOptions) != 0 {
			id := ApplicationContributionID(applicationName, ContributionProviderOS)
			components[id] = Component{
				Type: ComponentTypeAPT, APT: &APTComponent{Packages: application.Packages.OS},
				Options: osOptions, Executables: map[string]Executable{},
			}
		}
		if application.Packages.Python != nil || len(pythonOptions) != 0 {
			pythonPackages := application.Packages.Python
			if pythonPackages == nil {
				pythonPackages = &PythonComponent{Interpreter: CommandRequirement{Command: "python"}}
			}
			id := ApplicationContributionID(applicationName, ContributionProviderPython)
			components[id] = Component{
				Type: ComponentTypePython, Python: pythonPackages,
				Options: pythonOptions, Executables: map[string]Executable{},
			}
		}
		for executableName, executable := range application.Executables {
			if err := validateProviderIdentifier("application executable", executableName); err != nil {
				return err
			}
			if err := validateContributionProvider("application executable source", executable.Source); err != nil {
				return err
			}
			id := ApplicationContributionID(applicationName, executable.Source)
			contribution, found := components[id]
			if !found {
				return fmt.Errorf("application executable %s references missing contribution %q", ApplicationExecutableID(applicationName, executableName), id)
			}
			contribution.Executables[executableName] = executable
			components[id] = contribution
		}
	}
	environment.Components = components
	return nil
}

func validateContributionProvider(field string, provider string) error {
	switch provider {
	case ContributionProviderOS, ContributionProviderPython:
		return nil
	default:
		return fmt.Errorf("%s uses unsupported contribution provider %q", field, provider)
	}
}
