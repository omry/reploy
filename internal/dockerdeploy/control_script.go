package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
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
	ControlScript string
}

func renderPowerShellControlScript(spec controlScriptSpec) string {
	targetDir := "$PSScriptRoot"
	if spec.Mode != controlScriptModeStaged {
		targetDir = powerShellSingleQuote(spec.TargetDir)
	}
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
`, targetDir, powerShellSingleQuote(spec.ControlScript), powerShellSingleQuote(filepath.FromSlash(embeddedRuntimeFileName())))
}

func renderControlScript(spec controlScriptSpec) string {
	return fmt.Sprintf(`#!/usr/bin/env sh
set -eu

%s

exec "$reploy_bin" _control --dir "$target_dir" --script-name "$control_script" "$@"
`, controlScriptWrapperAssignments(spec))
}

func controlScriptWrapperAssignments(spec controlScriptSpec) string {
	controlScript := defaultString(spec.ControlScript, blueprint.DefaultControlScriptName)
	if spec.Mode == controlScriptModeStaged {
		return fmt.Sprintf(`target_dir="${REPLOY_DEPLOY_DIR:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)}"
control_script=%s
reploy_bin="$target_dir"/%s`, posixShellSingleQuote(controlScript), embeddedRuntimeFileName())
	}
	return fmt.Sprintf(`target_dir=%s
control_script=%s
reploy_bin=%s`,
		posixShellSingleQuote(spec.TargetDir),
		posixShellSingleQuote(controlScript),
		posixShellSingleQuote(filepath.Join(spec.TargetDir, embeddedRuntimeFileName())),
	)
}

func posixShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func powerShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
