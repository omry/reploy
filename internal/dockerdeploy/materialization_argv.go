package dockerdeploy

import (
	"fmt"
	"path"

	"github.com/omry/reploy/internal/providers"
)

const materializationChildEnvironmentProgram = `exec </dev/null; umask "$1"; shift; exec "$@"`

// RenderMaterializationArgv resolves a validated transaction's typed operands
// to their exact in-container strings. It deliberately returns an argv array:
// callers must not join or reinterpret the result as shell source.
func RenderMaterializationArgv(transaction providers.MaterializationTransaction) ([]string, error) {
	if err := providers.ValidateMaterializationTransaction(transaction); err != nil {
		return nil, err
	}
	executables := make(map[string]string, len(transaction.Prerequisites)+2)
	executables[transaction.Carrier.ID] = transaction.Carrier.Evidence.InvocationPath
	executables[transaction.EnvironmentLauncher.ID] = transaction.EnvironmentLauncher.Evidence.InvocationPath
	for _, input := range transaction.Prerequisites {
		executables[input.ID] = input.Evidence.InvocationPath
	}
	generated := make(map[string]string, len(transaction.GeneratedExecutables))
	for _, declaration := range transaction.GeneratedExecutables {
		generated[declaration.ID] = declaration.Path
	}
	mounts := make(map[string]string, len(transaction.Mounts))
	for _, mount := range transaction.Mounts {
		mounts[mount.ID] = mount.Destination
	}

	providerArgv := make([]string, 0, len(transaction.Argv))
	for index, argument := range transaction.Argv {
		switch argument.Kind {
		case providers.TypedArgumentLiteral:
			providerArgv = append(providerArgv, argument.Literal)
		case providers.TypedArgumentValidatedExecutable:
			value, exists := executables[argument.ExecutableID]
			if !exists {
				return nil, fmt.Errorf("materialization argv %d references unknown executable input %q", index, argument.ExecutableID)
			}
			providerArgv = append(providerArgv, value)
		case providers.TypedArgumentGeneratedExecutable:
			value, exists := generated[argument.GeneratedID]
			if !exists {
				return nil, fmt.Errorf("materialization argv %d references unknown generated executable %q", index, argument.GeneratedID)
			}
			providerArgv = append(providerArgv, value)
		case providers.TypedArgumentMountedArtifact:
			destination, exists := mounts[argument.MountID]
			if !exists {
				return nil, fmt.Errorf("materialization argv %d references unknown mount %q", index, argument.MountID)
			}
			providerArgv = append(providerArgv, path.Join(destination, argument.RelativePath))
		default:
			return nil, fmt.Errorf("materialization argv %d has unsupported kind %q", index, argument.Kind)
		}
	}

	profile := transaction.ChildEnvironment
	argv := make([]string, 0, 7+len(profile.Variables)+len(providerArgv))
	argv = append(argv, transaction.EnvironmentLauncher.Evidence.InvocationPath, "-i")
	for _, variable := range profile.Variables {
		argv = append(argv, variable.Name+"="+variable.Value)
	}
	argv = append(argv,
		transaction.Carrier.Evidence.InvocationPath,
		"-c", materializationChildEnvironmentProgram,
		profile.Name, profile.Umask,
	)
	argv = append(argv, providerArgv...)
	return argv, nil
}
