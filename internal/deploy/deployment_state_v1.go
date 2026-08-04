package deploy

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
)

const (
	DeploymentStateSchemaV1       = "deployment-v1"
	InstallationSchemaV1          = "installation-v1"
	InstallationStatusConfiguring = "configuring"
	InstallationStatusReady       = "ready"
)

// DeploymentStateV1 contains machine-local operational facts. It is not an
// input to environment resolution, provider identity, or build reuse.
type DeploymentStateV1 struct {
	Schema       string              `json:"schema"`
	Installation InstallationStateV1 `json:"installation"`
}

type InstallationStateV1 struct {
	Schema         string                      `json:"schema"`
	Status         string                      `json:"status"`
	TargetDir      string                      `json:"target_dir"`
	Scope          string                      `json:"scope"`
	Service        string                      `json:"service"`
	UnitPath       string                      `json:"unit_path"`
	InstanceID     string                      `json:"instance_id"`
	ComposeProject string                      `json:"compose_project"`
	ContainerName  string                      `json:"container_name"`
	NetworkName    string                      `json:"network_name"`
	Ports          []InstallationPortBindingV1 `json:"ports"`
}

type InstallationPortBindingV1 struct {
	Name          string `json:"name"`
	HostBind      string `json:"host_bind"`
	HostPort      string `json:"host_port"`
	ContainerPort string `json:"container_port"`
}

func ValidateDeploymentStateV1(state DeploymentStateV1) error {
	if state.Schema != DeploymentStateSchemaV1 {
		return fmt.Errorf("deployment state schema must be %q", DeploymentStateSchemaV1)
	}
	if err := ValidateInstallationStateV1(state.Installation); err != nil {
		return fmt.Errorf("deployment installation: %w", err)
	}
	return nil
}

func ValidateInstallationStateV1(state InstallationStateV1) error {
	if state.Schema != InstallationSchemaV1 {
		return fmt.Errorf("installation schema must be %q", InstallationSchemaV1)
	}
	if state.Status != InstallationStatusConfiguring && state.Status != InstallationStatusReady {
		return fmt.Errorf("installation status must be %q or %q", InstallationStatusConfiguring, InstallationStatusReady)
	}
	if !safeDeploymentText(state.TargetDir) || !filepath.IsAbs(state.TargetDir) || filepath.Clean(state.TargetDir) != state.TargetDir {
		return fmt.Errorf("installation target directory must be an absolute clean path")
	}
	if state.Scope != "user" && state.Scope != "system" {
		return fmt.Errorf("installation scope must be user or system")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "service", value: state.Service},
		{name: "instance ID", value: state.InstanceID},
		{name: "Compose project", value: state.ComposeProject},
		{name: "container name", value: state.ContainerName},
		{name: "network name", value: state.NetworkName},
	} {
		if !safeDeploymentText(field.value) {
			return fmt.Errorf("installation %s must be nonempty safe text", field.name)
		}
	}
	if state.UnitPath != "" && (!safeDeploymentText(state.UnitPath) || !filepath.IsAbs(state.UnitPath) || filepath.Clean(state.UnitPath) != state.UnitPath) {
		return fmt.Errorf("installation unit path must be empty or an absolute clean path")
	}
	if state.Ports == nil {
		return fmt.Errorf("installation ports must use an array")
	}
	for index, port := range state.Ports {
		if !safeDeploymentText(port.Name) || !safeDeploymentText(port.HostBind) || !safeDeploymentText(port.HostPort) || !safeDeploymentText(port.ContainerPort) {
			return fmt.Errorf("installation port %d fields must be nonempty safe text", index)
		}
		if !validInstallationPort(port.HostPort) || !validInstallationPort(port.ContainerPort) {
			return fmt.Errorf("installation port %q host and container ports must be between 1 and 65535", port.Name)
		}
		if index > 0 && state.Ports[index-1].Name >= port.Name {
			return fmt.Errorf("installation ports must be unique and sorted by name")
		}
	}
	return nil
}

// MarkInstallationReadyV1 completes the only supported installation-state
// transition. The ready record must describe the exact configuring install;
// changing any other installation fact requires a new install publication.
func (lock *OperationLock) MarkInstallationReadyV1(ready InstallationStateV1) (StateV1, bool, error) {
	if lock == nil {
		return StateV1{}, false, fmt.Errorf("mark installation ready requires an operation lock")
	}
	if err := ValidateInstallationStateV1(ready); err != nil {
		return StateV1{}, false, err
	}
	if ready.Status != InstallationStatusReady {
		return StateV1{}, false, fmt.Errorf("mark installation ready requires status %q", InstallationStatusReady)
	}

	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.statePathV1Locked()
	if err != nil {
		return StateV1{}, false, err
	}
	state, found, err := readStateV1Path(path)
	if err != nil {
		return StateV1{}, false, err
	}
	if !found || state.Current == nil || state.Deployment == nil {
		return StateV1{}, false, fmt.Errorf("mark installation ready requires a committed installation")
	}
	targetDir := filepath.Dir(filepath.Dir(path))
	if ready.TargetDir != targetDir {
		return StateV1{}, false, fmt.Errorf("installation target directory %q does not match locked deployment %q", ready.TargetDir, targetDir)
	}
	if reflect.DeepEqual(state.Deployment.Installation, ready) {
		return state, false, nil
	}
	configuring := ready
	configuring.Status = InstallationStatusConfiguring
	if !reflect.DeepEqual(state.Deployment.Installation, configuring) {
		return StateV1{}, false, fmt.Errorf("committed configuring installation does not match the ready installation")
	}
	state.Deployment = &DeploymentStateV1{Schema: DeploymentStateSchemaV1, Installation: ready}
	if err := writeInstalledStateV1(path, state); err != nil {
		return StateV1{}, false, err
	}
	return state, true, nil
}

func safeDeploymentText(value string) bool {
	return safeRecoveryIdentity(value)
}

func validInstallationPort(value string) bool {
	port, err := strconv.ParseUint(value, 10, 16)
	return err == nil && port != 0 && strconv.FormatUint(port, 10) == value
}

// SetInstallationStateV1 records where the current built environment was
// installed. The caller retains the deployment operation lock across any
// filesystem, service, image-reference, and provider-store work that precedes
// this final state publication.
func (lock *OperationLock) SetInstallationStateV1(installation InstallationStateV1) (StateV1, bool, error) {
	if lock == nil {
		return StateV1{}, false, fmt.Errorf("set installation state requires an operation lock")
	}
	if err := ValidateInstallationStateV1(installation); err != nil {
		return StateV1{}, false, err
	}

	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.statePathV1Locked()
	if err != nil {
		return StateV1{}, false, err
	}
	state, found, err := readStateV1Path(path)
	if err != nil {
		return StateV1{}, false, err
	}
	if !found {
		return StateV1{}, false, fmt.Errorf("set installation state requires deployment state")
	}
	if state.Current == nil {
		return StateV1{}, false, fmt.Errorf("set installation state requires a current build")
	}
	targetDir := filepath.Dir(filepath.Dir(path))
	if installation.TargetDir != targetDir {
		return StateV1{}, false, fmt.Errorf("installation target directory %q does not match locked deployment %q", installation.TargetDir, targetDir)
	}

	deployment := &DeploymentStateV1{Schema: DeploymentStateSchemaV1, Installation: installation}
	if reflect.DeepEqual(state.Deployment, deployment) {
		return state, false, nil
	}
	state.Deployment = deployment
	if err := writeInstalledStateV1(path, state); err != nil {
		return StateV1{}, false, err
	}
	return state, true, nil
}

// ClearInstallationStateV1 removes the exact recorded installation facts
// after host uninstall succeeds, returning the deployment to staged state
// without changing its current build.
func (lock *OperationLock) ClearInstallationStateV1(expected InstallationStateV1) (StateV1, bool, error) {
	if lock == nil {
		return StateV1{}, false, fmt.Errorf("clear installation state requires an operation lock")
	}
	if err := ValidateInstallationStateV1(expected); err != nil {
		return StateV1{}, false, err
	}

	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.statePathV1Locked()
	if err != nil {
		return StateV1{}, false, err
	}
	state, found, err := readStateV1Path(path)
	if err != nil {
		return StateV1{}, false, err
	}
	if !found || state.Current == nil || state.Deployment == nil {
		return StateV1{}, false, fmt.Errorf("clear installation state requires a committed installation")
	}
	if !reflect.DeepEqual(state.Deployment.Installation, expected) {
		return StateV1{}, false, fmt.Errorf("installed facts changed before clearing installation state")
	}
	state.Deployment = nil
	if err := writeInstalledStateV1(path, state); err != nil {
		return StateV1{}, false, err
	}
	return state, true, nil
}

// CommitInstalledStateV1 is the destination state commit point for install.
// Artifact transfer, image-reference creation, and host-file candidate
// preparation must succeed first. The caller commits status configuring before
// replacing live host configuration, then uses MarkInstallationReadyV1.
func (lock *OperationLock) CommitInstalledStateV1(
	expected *EnvironmentGenerationState,
	sourceState StateV1,
	destinationGeneration EnvironmentGenerationState,
	installation InstallationStateV1,
) (StateV1, bool, error) {
	if lock == nil {
		return StateV1{}, false, fmt.Errorf("commit installed state requires an operation lock")
	}
	if err := ValidateStateV1(sourceState); err != nil {
		return StateV1{}, false, fmt.Errorf("commit installed source state: %w", err)
	}
	if sourceState.Current == nil {
		return StateV1{}, false, fmt.Errorf("commit installed state requires a current source build")
	}
	if sourceState.Deployment != nil {
		return StateV1{}, false, fmt.Errorf("commit installed state requires staged source state without deployment-local facts")
	}
	if err := validateInstalledDestinationGeneration(*sourceState.Current, destinationGeneration); err != nil {
		return StateV1{}, false, err
	}
	if err := ValidateInstallationStateV1(installation); err != nil {
		return StateV1{}, false, err
	}

	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.statePathV1Locked()
	if err != nil {
		return StateV1{}, false, err
	}
	targetDir := filepath.Dir(filepath.Dir(path))
	if installation.TargetDir != targetDir {
		return StateV1{}, false, fmt.Errorf("installation target directory %q does not match locked deployment %q", installation.TargetDir, targetDir)
	}
	current, found, err := readStateV1Path(path)
	if err != nil {
		return StateV1{}, false, err
	}
	var observed *EnvironmentGenerationState
	if found {
		observed = current.Current
	}
	if !reflect.DeepEqual(observed, expected) {
		return StateV1{}, false, fmt.Errorf("destination current generation changed before installed state commit")
	}

	candidate := sourceState
	candidate.Current = &destinationGeneration
	candidate.Staging = nil
	candidate.Deployment = &DeploymentStateV1{Schema: DeploymentStateSchemaV1, Installation: installation}
	if found && reflect.DeepEqual(current, candidate) {
		return candidate, false, nil
	}
	if err := writeInstalledStateV1(path, candidate); err != nil {
		return StateV1{}, false, err
	}
	return candidate, true, nil
}

func validateInstalledDestinationGeneration(source EnvironmentGenerationState, destination EnvironmentGenerationState) error {
	if err := ValidateEnvironmentGenerationState(destination); err != nil {
		return fmt.Errorf("installed destination generation: %w", err)
	}
	if destination.Reference == source.Reference {
		return fmt.Errorf("installed destination generation requires a new destination-local reference")
	}
	if destination.Platform != source.Platform || destination.RuntimePolicyDigest != source.RuntimePolicyDigest {
		return fmt.Errorf("installed destination generation must preserve the source platform and runtime policy")
	}
	return nil
}

func writeInstalledStateV1(path string, state StateV1) error {
	content, err := EncodeStateV1(state)
	if err != nil {
		return err
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("commit installation state: %w", err)
	}
	return nil
}
