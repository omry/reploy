package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"strings"
)

type controlScriptMode string

const (
	controlScriptModeStaged        controlScriptMode = "staged"
	controlScriptModeDeployed      controlScriptMode = "deployed"
	controlScriptModeDockerDesktop controlScriptMode = "docker-desktop"
)

type controlScriptSpec struct {
	Mode          controlScriptMode
	TargetDir     string
	AppID         string
	ControlScript string
}

func powerShellControlScriptName(appID string) string {
	return controlScriptName(appID) + ".ps1"
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
