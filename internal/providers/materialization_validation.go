package providers

import (
	"fmt"
	"path"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func MaterializationTransactionDigest(transaction MaterializationTransaction) (canonical.Digest, error) {
	if err := ValidateMaterializationTransaction(transaction); err != nil {
		return "", err
	}
	return canonical.Sum("materialization-transaction", MaterializationTransactionSchemaV1, transaction)
}

func ValidateMaterializationTransaction(transaction MaterializationTransaction) error {
	if transaction.Schema != MaterializationTransactionSchemaV1 {
		return fmt.Errorf("materialization transaction schema must be %q", MaterializationTransactionSchemaV1)
	}
	if err := validateResolvableNodeID(transaction.NodeID); err != nil {
		return fmt.Errorf("materialization transaction: %w", err)
	}
	if !isNonemptyPlainString(transaction.RecipeVersion) {
		return fmt.Errorf("materialization transaction recipe version must be nonempty valid text")
	}
	if err := transaction.Upstream.Validate(); err != nil {
		return fmt.Errorf("materialization transaction upstream: %w", err)
	}
	if err := ValidateValidatedExecutableInput(transaction.Carrier); err != nil {
		return fmt.Errorf("materialization carrier: %w", err)
	}
	if transaction.Carrier.Role != ExecutableRoleCarrier {
		return fmt.Errorf("materialization carrier role must be %q", ExecutableRoleCarrier)
	}
	if err := ValidateValidatedExecutableInput(transaction.EnvironmentLauncher); err != nil {
		return fmt.Errorf("materialization environment launcher: %w", err)
	}
	if transaction.EnvironmentLauncher.Role != ExecutableRoleEnvironmentLauncher {
		return fmt.Errorf("materialization environment launcher role must be %q", ExecutableRoleEnvironmentLauncher)
	}
	inputs := map[string]ValidatedExecutableInput{}
	for _, input := range []ValidatedExecutableInput{transaction.Carrier, transaction.EnvironmentLauncher} {
		if _, exists := inputs[input.ID]; exists {
			return fmt.Errorf("materialization executable input ID %q is duplicated", input.ID)
		}
		inputs[input.ID] = input
	}
	if transaction.Prerequisites == nil {
		return fmt.Errorf("materialization prerequisites must use an array")
	}
	for index, input := range transaction.Prerequisites {
		if err := ValidateValidatedExecutableInput(input); err != nil {
			return fmt.Errorf("materialization prerequisite %d: %w", index, err)
		}
		if input.Role != ExecutableRoleProviderPrerequisite && input.Role != ExecutableRoleSelectedOutput {
			return fmt.Errorf("materialization prerequisite %q role must be provider-prerequisite or selected-output", input.ID)
		}
		if index > 0 && transaction.Prerequisites[index-1].ID >= input.ID {
			return fmt.Errorf("materialization prerequisites must be unique and sorted by ID")
		}
		if _, exists := inputs[input.ID]; exists {
			return fmt.Errorf("materialization executable input ID %q is duplicated", input.ID)
		}
		inputs[input.ID] = input
	}
	if err := transaction.Script.Validate(); err != nil {
		return fmt.Errorf("materialization script: %w", err)
	}
	if transaction.Script.Kind != BuildMountSourceScript {
		return fmt.Errorf("materialization script artifact kind must be %q", BuildMountSourceScript)
	}
	if transaction.Argv == nil || len(transaction.Argv) == 0 {
		return fmt.Errorf("materialization argv must use a nonempty array")
	}
	if transaction.Argv[0].Kind != TypedArgumentValidatedExecutable {
		return fmt.Errorf("materialization command position must be a validated executable")
	}
	if transaction.Argv[0].ExecutableID != transaction.Carrier.ID {
		return fmt.Errorf("materialization command position must reference the carrier executable")
	}
	if err := ValidateChildEnvironmentProfile(transaction.ChildEnvironment); err != nil {
		return fmt.Errorf("materialization child environment: %w", err)
	}
	if err := validateAbsoluteLinuxPath("materialization working directory", transaction.WorkingDirectory); err != nil {
		return err
	}
	if err := ValidateContainerIdentity(transaction.BuildIdentity); err != nil {
		return fmt.Errorf("materialization build identity: %w", err)
	}
	if err := ValidateNetworkPolicy(transaction.Network); err != nil {
		return err
	}
	if err := ValidateBuildMounts(transaction.Mounts); err != nil {
		return err
	}
	mounts := make(map[string]BuildMount, len(transaction.Mounts))
	scriptMounts := 0
	for _, mount := range transaction.Mounts {
		mounts[mount.ID] = mount
		if mount.SourceKind == BuildMountSourceScript {
			scriptMounts++
			if mount.SourceDigest != transaction.Script.SHA256 {
				return fmt.Errorf("materialization script mount %q does not match the script artifact digest", mount.ID)
			}
		}
	}
	if scriptMounts != 1 {
		return fmt.Errorf("materialization transaction must contain exactly one script mount")
	}
	generated := make(map[string]GeneratedExecutableDeclaration, len(transaction.GeneratedExecutables))
	if transaction.GeneratedExecutables == nil {
		return fmt.Errorf("materialization generated executables must use an array")
	}
	for index, declaration := range transaction.GeneratedExecutables {
		if err := ValidateGeneratedExecutableDeclaration(declaration); err != nil {
			return fmt.Errorf("materialization generated executable %d: %w", index, err)
		}
		if index > 0 && transaction.GeneratedExecutables[index-1].ID >= declaration.ID {
			return fmt.Errorf("materialization generated executables must be unique and sorted by ID")
		}
		generated[declaration.ID] = declaration
	}
	scriptReferenced := false
	for index, argument := range transaction.Argv {
		if err := ValidateTypedArgument(argument); err != nil {
			return fmt.Errorf("materialization argv %d: %w", index, err)
		}
		switch argument.Kind {
		case TypedArgumentValidatedExecutable:
			if _, exists := inputs[argument.ExecutableID]; !exists {
				return fmt.Errorf("materialization argv %d references unknown executable input %q", index, argument.ExecutableID)
			}
		case TypedArgumentGeneratedExecutable:
			if _, exists := generated[argument.GeneratedID]; !exists {
				return fmt.Errorf("materialization argv %d references unknown generated executable %q", index, argument.GeneratedID)
			}
		case TypedArgumentMountedArtifact:
			mount, exists := mounts[argument.MountID]
			if !exists {
				return fmt.Errorf("materialization argv %d references unknown mount %q", index, argument.MountID)
			}
			if mount.SourceKind == BuildMountSourceScript {
				scriptReferenced = true
			}
		}
	}
	if !scriptReferenced {
		return fmt.Errorf("materialization argv must reference the trusted script mount")
	}
	if err := ValidateImageConfigPolicy(transaction.FinalImageConfig); err != nil {
		return fmt.Errorf("materialization final image config: %w", err)
	}
	return nil
}

func ValidateTypedArgument(argument TypedArgument) error {
	otherEmpty := func(fields ...string) bool {
		for _, field := range fields {
			if field != "" {
				return false
			}
		}
		return true
	}
	switch argument.Kind {
	case TypedArgumentLiteral:
		if !otherEmpty(argument.ExecutableID, argument.GeneratedID, argument.MountID, argument.RelativePath) {
			return fmt.Errorf("literal argument may set only literal")
		}
	case TypedArgumentValidatedExecutable:
		if argument.ExecutableID == "" || !otherEmpty(argument.Literal, argument.GeneratedID, argument.MountID, argument.RelativePath) {
			return fmt.Errorf("validated-executable argument must set only executable_id")
		}
		if err := blueprint.ValidateProviderIdentifier("validated executable argument ID", argument.ExecutableID); err != nil {
			return err
		}
	case TypedArgumentGeneratedExecutable:
		if argument.GeneratedID == "" || !otherEmpty(argument.Literal, argument.ExecutableID, argument.MountID, argument.RelativePath) {
			return fmt.Errorf("generated-executable argument must set only generated_id")
		}
		if err := blueprint.ValidateProviderIdentifier("generated executable argument ID", argument.GeneratedID); err != nil {
			return err
		}
	case TypedArgumentMountedArtifact:
		if argument.MountID == "" || argument.RelativePath == "" || !otherEmpty(argument.Literal, argument.ExecutableID, argument.GeneratedID) {
			return fmt.Errorf("mounted-artifact argument must set only mount_id and relative_path")
		}
		if err := blueprint.ValidateProviderIdentifier("mounted artifact argument ID", argument.MountID); err != nil {
			return err
		}
		if err := validateRelativeLinuxPath("mounted artifact relative path", argument.RelativePath); err != nil {
			return err
		}
	default:
		return fmt.Errorf("typed argument kind must be literal, validated-executable, generated-executable, or mounted-artifact")
	}
	return nil
}

func ValidateValidatedExecutableInput(input ValidatedExecutableInput) error {
	if err := blueprint.ValidateProviderIdentifier("validated executable input ID", input.ID); err != nil {
		return err
	}
	switch input.Role {
	case ExecutableRoleCarrier, ExecutableRoleEnvironmentLauncher, ExecutableRoleProviderPrerequisite, ExecutableRoleSelectedOutput:
	default:
		return fmt.Errorf("validated executable input role must be carrier, environment-launcher, provider-prerequisite, or selected-output")
	}
	if err := validateValidationPolicy(input.Policy); err != nil {
		return fmt.Errorf("validated executable input: %w", err)
	}
	if input.Evidence.RequirementID != input.ID {
		return fmt.Errorf("validated executable input evidence requirement ID %q does not match %q", input.Evidence.RequirementID, input.ID)
	}
	if err := validateExecutableEvidenceStructure(input.Evidence, false); err != nil {
		return fmt.Errorf("validated executable input %q: %w", input.ID, err)
	}
	return nil
}

func ValidateChildEnvironmentProfile(profile ChildEnvironmentProfile) error {
	if profile.Schema != ChildEnvironmentSchemaV1 {
		return fmt.Errorf("child environment schema must be %q", ChildEnvironmentSchemaV1)
	}
	if !isNonemptyPlainString(profile.Name) {
		return fmt.Errorf("child environment name must be nonempty valid text")
	}
	if !profile.InheritNone {
		return fmt.Errorf("child environment must inherit no variables")
	}
	if len(profile.Umask) != 4 {
		return fmt.Errorf("child environment umask must contain four octal digits")
	}
	for _, digit := range profile.Umask {
		if digit < '0' || digit > '7' {
			return fmt.Errorf("child environment umask must contain four octal digits")
		}
	}
	if profile.Variables == nil {
		return fmt.Errorf("child environment variables must use an array")
	}
	for index, variable := range profile.Variables {
		if !validEnvironmentName(variable.Name) {
			return fmt.Errorf("child environment variable name %q is invalid", variable.Name)
		}
		if index > 0 && profile.Variables[index-1].Name >= variable.Name {
			return fmt.Errorf("child environment variables must be unique and sorted")
		}
	}
	return nil
}

func ValidateContainerIdentity(identity ContainerIdentity) error {
	if !isCanonicalUnsigned(identity.UID) || !isCanonicalUnsigned(identity.GID) {
		return fmt.Errorf("container UID and GID must be canonical unsigned decimal integers")
	}
	if identity.SupplementaryGIDs == nil {
		return fmt.Errorf("supplementary GIDs must use an array")
	}
	for index, gid := range identity.SupplementaryGIDs {
		if !isCanonicalUnsigned(gid) {
			return fmt.Errorf("supplementary GID %q must be a canonical unsigned decimal integer", gid)
		}
		if index > 0 && compareUnsigned(identity.SupplementaryGIDs[index-1], gid) >= 0 {
			return fmt.Errorf("supplementary GIDs must be unique and numerically sorted")
		}
	}
	return nil
}

func ValidateNetworkPolicy(policy NetworkPolicy) error {
	if policy != NetworkPolicyNone {
		return fmt.Errorf("materialization network policy must be %q", NetworkPolicyNone)
	}
	return nil
}

func ValidateBuildMounts(mounts []BuildMount) error {
	if mounts == nil {
		return fmt.Errorf("build mounts must use an array")
	}
	for index, mount := range mounts {
		if err := blueprint.ValidateProviderIdentifier("build mount ID", mount.ID); err != nil {
			return err
		}
		if index > 0 && mounts[index-1].ID >= mount.ID {
			return fmt.Errorf("build mounts must be unique and sorted by ID")
		}
		if err := validateAbsoluteLinuxPath("build mount destination", mount.Destination); err != nil {
			return err
		}
		if mount.Destination == "/.reploy-build" || !strings.HasPrefix(mount.Destination, "/.reploy-build/") {
			return fmt.Errorf("build mount destination must be a strict descendant of /.reploy-build")
		}
		if !isNonemptyPlainString(mount.ExpectedKind) {
			return fmt.Errorf("build mount expected kind must be nonempty valid text")
		}
		switch mount.SourceKind {
		case BuildMountSourceArtifact, BuildMountSourceScript:
			if !mount.ReadOnly {
				return fmt.Errorf("%s build mount must be read-only", mount.SourceKind)
			}
			if err := mount.SourceDigest.Validate(); err != nil {
				return fmt.Errorf("build mount source digest: %w", err)
			}
		case BuildMountSourcePrivateOutput:
			if mount.ReadOnly || mount.SourceDigest != "" {
				return fmt.Errorf("private-output build mount must be writable and have no source digest")
			}
		default:
			return fmt.Errorf("build mount source kind must be artifact, script, or private-output")
		}
		for previous := 0; previous < index; previous++ {
			left := mounts[previous].Destination
			right := mount.Destination
			if strings.HasPrefix(left+"/", right+"/") || strings.HasPrefix(right+"/", left+"/") {
				return fmt.Errorf("build mount destinations must not overlap: %s and %s", left, right)
			}
		}
	}
	return nil
}

func ValidateGeneratedExecutableDeclaration(declaration GeneratedExecutableDeclaration) error {
	if err := blueprint.ValidateProviderIdentifier("generated executable ID", declaration.ID); err != nil {
		return err
	}
	if err := validateAbsoluteLinuxPath("generated executable path", declaration.Path); err != nil {
		return err
	}
	if err := validateAbsoluteLinuxPath("generated executable exclusive root", declaration.ExclusiveRoot); err != nil {
		return err
	}
	if declaration.Path == declaration.ExclusiveRoot || !strings.HasPrefix(declaration.Path, declaration.ExclusiveRoot+"/") {
		return fmt.Errorf("generated executable path must be a strict descendant of its exclusive root")
	}
	if err := validateValidationPolicy(declaration.ValidationPolicy); err != nil {
		return fmt.Errorf("generated executable: %w", err)
	}
	return nil
}

func ValidateImageConfigPolicy(policy ImageConfigPolicy) error {
	if !isNonemptyPlainString(policy.User) {
		return fmt.Errorf("image config user must be nonempty valid text")
	}
	if err := validateAbsoluteLinuxPath("image config working directory", policy.WorkingDir); err != nil {
		return err
	}
	if policy.Environment == nil {
		return fmt.Errorf("image config environment must use an array")
	}
	for index, variable := range policy.Environment {
		if !validEnvironmentName(variable.Name) {
			return fmt.Errorf("image config environment variable name %q is invalid", variable.Name)
		}
		if index > 0 && policy.Environment[index-1].Name >= variable.Name {
			return fmt.Errorf("image config environment variables must be unique and sorted")
		}
	}
	if policy.Entrypoint == nil || policy.Command == nil {
		return fmt.Errorf("image config entrypoint and command must use arrays")
	}
	if policy.Healthcheck != ImageHealthcheckNone {
		return fmt.Errorf("image config healthcheck must be %q", ImageHealthcheckNone)
	}
	if !isNonemptyPlainString(policy.StopSignal) {
		return fmt.Errorf("image config stop signal must be nonempty valid text")
	}
	if policy.Labels == nil {
		return fmt.Errorf("image config labels must use an array")
	}
	for index, label := range policy.Labels {
		if !isNonemptyPlainString(label.Name) {
			return fmt.Errorf("image config label name must be nonempty valid text")
		}
		if strings.HasPrefix(label.Name, "io.reploy.validation.") {
			return fmt.Errorf("image config label %q uses the reserved validation namespace", label.Name)
		}
		if index > 0 && policy.Labels[index-1].Name >= label.Name {
			return fmt.Errorf("image config labels must be unique and sorted")
		}
	}
	return nil
}

func validateRelativeLinuxPath(field string, value string) error {
	if value == "" || path.IsAbs(value) || path.Clean(value) != value || value == "." || strings.Contains(value, `\`) {
		return fmt.Errorf("%s %q must be a normalized nonempty relative Linux path", field, value)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("%s %q contains an invalid component", field, value)
		}
	}
	return nil
}

func validEnvironmentName(name string) bool {
	if name == "" || !asciiLetter(name[0]) && name[0] != '_' {
		return false
	}
	for index := 1; index < len(name); index++ {
		char := name[index]
		if !asciiLetter(char) && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func asciiLetter(char byte) bool {
	return char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z'
}

func isCanonicalUnsigned(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func compareUnsigned(left string, right string) int {
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return strings.Compare(left, right)
}
