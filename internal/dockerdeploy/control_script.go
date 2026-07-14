package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type controlScriptMode string

const (
	controlScriptModeStaged        controlScriptMode = "staged"
	controlScriptModeDeployed      controlScriptMode = "deployed"
	controlScriptModeDockerDesktop controlScriptMode = "docker-desktop"
)

type controlScriptSpec struct {
	Mode               controlScriptMode
	TargetDir          string
	AppID              string
	Service            string
	ComposeProject     string
	ComposeOverride    bool
	ControlScript      string
	ConfigDir          string
	Health             deploy.DockerHealthConfig
	Terminal           deploy.AppTerminalConfig
	DeployedCommands   []deploy.DockerCommandConfig
	ConfigContainerDir string
	ManagedFiles       []string
}

func stagingControlScriptContent(pack deploy.AppPack, deployedCommands []deploy.DockerCommandConfig) string {
	configLayout := configMountLayoutForPack(pack)
	return renderControlScript(controlScriptSpec{
		Mode:               controlScriptModeStaged,
		AppID:              pack.AppID,
		ControlScript:      controlScriptNameForPack(pack),
		ConfigDir:          pack.Docker.DeploymentDirs.Config,
		Health:             pack.Docker.Health,
		Terminal:           pack.App.Terminal,
		DeployedCommands:   deployedCommands,
		ConfigContainerDir: configLayout.ContainerConfigDir,
		ManagedFiles:       append([]string(nil), configLayout.FileMounts...),
	})
}

func controlScriptContent(plan installPlan) string {
	mode := controlScriptModeDeployed
	if isDockerManagedInstallBackend(plan.Backend) {
		mode = controlScriptModeDockerDesktop
	}
	return renderControlScript(controlScriptSpec{
		Mode:               mode,
		TargetDir:          plan.TargetDir,
		AppID:              plan.AppID,
		Service:            plan.Service,
		ComposeProject:     plan.ComposeProject,
		ComposeOverride:    plan.ComposeOverride,
		ControlScript:      plan.ControlScript,
		ConfigDir:          plan.ConfigDir,
		Health:             plan.Health,
		Terminal:           plan.Terminal,
		DeployedCommands:   plan.DeployedCommands,
		ConfigContainerDir: plan.ConfigContainerDir,
		ManagedFiles:       append([]string(nil), plan.ManagedFiles...),
	})
}

func powerShellControlScriptName(appID string) string {
	return controlScriptName(appID) + ".ps1"
}

func powerShellDockerDesktopControlScriptContent(plan installPlan) string {
	return renderPowerShellDockerDesktopControlScript(controlScriptSpec{
		Mode:               controlScriptModeDockerDesktop,
		TargetDir:          plan.TargetDir,
		AppID:              plan.AppID,
		ComposeProject:     plan.ComposeProject,
		ControlScript:      powerShellControlScriptName(plan.AppID),
		ConfigDir:          plan.ConfigDir,
		Terminal:           plan.Terminal,
		DeployedCommands:   plan.DeployedCommands,
		ConfigContainerDir: plan.ConfigContainerDir,
		ManagedFiles:       append([]string(nil), plan.ManagedFiles...),
	})
}

func renderPowerShellDockerDesktopControlScript(spec controlScriptSpec) string {
	return fmt.Sprintf(`[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$RemainingArgs
)

$ErrorActionPreference = 'Stop'

$TargetDir = %s
$ControlScript = %s
$ReployBin = Join-Path $TargetDir %s

& $ReployBin _control --dir $TargetDir --script-name $ControlScript @RemainingArgs
exit $LASTEXITCODE
`, powerShellSingleQuote(spec.TargetDir), powerShellSingleQuote(spec.ControlScript), powerShellSingleQuote(filepath.FromSlash(embeddedRuntimeFileName())))
}

func renderControlScript(spec controlScriptSpec) string {
	return fmt.Sprintf(`#!/usr/bin/env sh
set -eu

%s

exec "$reploy_bin" _control --dir "$target_dir" --script-name "$control_script" "$@"
`, controlScriptWrapperAssignments(spec))
}

func controlScriptWrapperAssignments(spec controlScriptSpec) string {
	controlScript := defaultString(spec.ControlScript, controlScriptName(spec.AppID))
	if spec.Mode == controlScriptModeStaged {
		return fmt.Sprintf(`target_dir="${REPLOY_DEPLOY_DIR:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)}"
control_script=%q
reploy_bin="$target_dir"/%s`, controlScript, embeddedRuntimeFileName())
	}
	return fmt.Sprintf(`target_dir=%q
control_script=%q
reploy_bin=%q`, spec.TargetDir, controlScript, filepath.Join(spec.TargetDir, embeddedRuntimeFileName()))
}

func powerShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
