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
		ControlScript:      controlScriptName(pack.AppID),
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
	if plan.Backend == installBackendDockerDesktop {
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
	configContainerDir := spec.ConfigContainerDir
	if configContainerDir == "" {
		configContainerDir = "/config"
	}
	return fmt.Sprintf(`[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Command = 'help',
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$RemainingArgs
)

$ErrorActionPreference = 'Stop'

$TargetDir = %s
$ComposeProject = %s
$ComposeFile = Join-Path $TargetDir %s
$DockerEnv = Join-Path $TargetDir %s
$ConfigDisplayDir = Join-Path $TargetDir %s
$ConfigContainerDir = %s
$ManagedFiles = @(%s)
$ReployControlLabel = %s
$ReployControlColor = %s

function Write-Usage {
    Write-Error @'
usage: %s COMMAND [ARGS...]
commands:
  up
  down
  restart
  status
  logs
%s
  help
'@
}

function Get-ReployControlOutputPrefix {
    $ColorMode = [Environment]::GetEnvironmentVariable('REPLOY_COLOR')
    if ([string]::IsNullOrWhiteSpace($ColorMode)) {
        $ColorMode = 'auto'
    }
    $ColorMode = $ColorMode.ToLowerInvariant()

    $UseColor = $false
    if ($ColorMode -eq 'always') {
        $UseColor = $true
    } elseif ($ColorMode -ne 'never' -and -not (Test-Path Env:NO_COLOR)) {
        $OutputIsTerminal = -not [Console]::IsOutputRedirected
        $SupportsVT = $false
        if ($Host -and $Host.UI) {
            $SupportsVT = [bool]$Host.UI.SupportsVirtualTerminal
        }
        $UseColor = $OutputIsTerminal -and $SupportsVT
    }

    if ($UseColor) {
        $Esc = [char]27
        return "${Esc}[38;5;${ReployControlColor}m${ReployControlLabel}${Esc}[0m"
    }
    return $ReployControlLabel
}

function Test-ManagedFiles {
    foreach ($ManagedFile in $ManagedFiles) {
        $Path = Join-Path $TargetDir $ManagedFile
        if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
            throw "managed file is missing: $ManagedFile"
        }
    }
}

function Invoke-ReployCompose {
    param(
        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$ComposeArgs
    )
    $Prefix = Get-ReployControlOutputPrefix
    $PreviousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & docker compose --project-name $ComposeProject --project-directory $TargetDir --env-file $DockerEnv -f $ComposeFile @ComposeArgs 2>&1 | ForEach-Object {
            if ($null -eq $_) {
                $Line = ''
            } else {
                $Line = $_.ToString()
            }
            if ($Line.StartsWith(' ')) {
                Write-Output "$Prefix$Line"
            } else {
                Write-Output "$Prefix $Line"
            }
        }
        $ExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $PreviousErrorActionPreference
    }
    if ($ExitCode -ne 0) {
        exit $ExitCode
    }
}

function Test-ForwardedArgs {
    param(
        [string]$Mode,
        [string[]]$AllowedFlags,
        [string[]]$Args
    )
    if ($Mode -eq 'args') {
        return
    }
    foreach ($Arg in $Args) {
        if ($Arg -match '^(--[^=]+)=') {
            $Flag = $Matches[1]
        } elseif ($Arg -match '^(--.+)$') {
            $Flag = $Matches[1]
        } else {
            throw "unexpected positional argument after app command trigger: $Arg"
        }
        if ($AllowedFlags -notcontains $Flag) {
            throw "unknown forwarded flag: $Flag"
        }
    }
}

function Invoke-AppCommand {
    param(
        [string]$CommandName,
        [string[]]$ForwardedArgs
    )
    Test-ManagedFiles
    $ComposeArgs = @(
        'run',
        '--rm',
        '--no-deps',
        '-e',
        "REPLOY_CONTAINER_COMMAND=$CommandName",
        '-e',
        "REPLOY_FORWARDED_ARGC=$($ForwardedArgs.Count)"
    )
    for ($Index = 0; $Index -lt $ForwardedArgs.Count; $Index++) {
        $ComposeArgs += '-e'
        $ComposeArgs += "REPLOY_FORWARDED_ARG_$Index=$($ForwardedArgs[$Index])"
    }
    $ComposeArgs += @(
        '-e',
        "REPLOY_CONFIG_CONTAINER_DIR=$ConfigContainerDir",
        '-e',
        "REPLOY_CONFIG_DISPLAY_DIR=$ConfigDisplayDir",
        '-e',
        'REPLOY_INCLUDE_RUNTIME_OVERRIDES=0',
        '-e',
        'REPLOY_CONFIG_MOUNT=rw',
        '-e',
        'REPLOY_APP_COMMAND_PREFIX=reploy app'
    )
    if ($env:COLUMNS -match '^\d+$') {
        $Columns = [int]$env:COLUMNS
        if ($Columns -ge 20 -and $Columns -le 1000) {
            $ComposeArgs += '-e'
            $ComposeArgs += "COLUMNS=$Columns"
        }
    }
%s
    $ComposeArgs += 'app'
    Invoke-ReployCompose @ComposeArgs
}

function Select-ForwardedArgs {
    param(
        [string[]]$Args,
        [int]$Skip
    )
    if ($Args.Count -le $Skip) {
        return @()
    }
    return @($Args | Select-Object -Skip $Skip)
}

switch ($Command) {
    'up' {
        Test-ManagedFiles
        Invoke-ReployCompose up -d @RemainingArgs
    }
    'down' {
        Invoke-ReployCompose down @RemainingArgs
    }
    'restart' {
        Test-ManagedFiles
        Invoke-ReployCompose restart @RemainingArgs
    }
    'status' {
        Invoke-ReployCompose ps @RemainingArgs
    }
    'logs' {
        Invoke-ReployCompose logs @RemainingArgs
    }
    { $_ -in @('', '-h', '--help', 'help') } {
        Write-Usage
    }
%s
    default {
        Write-Usage
        throw "unknown command: $Command"
    }
}
`, powerShellSingleQuote(spec.TargetDir), powerShellSingleQuote(spec.ComposeProject), powerShellSingleQuote(filepath.FromSlash(ComposeFileName)), powerShellSingleQuote(filepath.FromSlash(DockerEnvFileName)), powerShellSingleQuote(filepath.FromSlash(spec.ConfigDir)), powerShellSingleQuote(configContainerDir), powerShellArrayLiteral(spec.ManagedFiles), powerShellSingleQuote(controlScriptOutputLabel(spec.AppID)), powerShellSingleQuote(deployedOutputColor), spec.ControlScript, powerShellUsageCommands(spec.DeployedCommands), powerShellTerminalEnv(spec.Terminal), powerShellAppCommandCases(spec.DeployedCommands))
}

func renderControlScript(spec controlScriptSpec) string {
	health := spec.Health
	insecureFlag := ""
	wgetInsecureFlag := ""
	if !healthTLSVerify(health) {
		insecureFlag = "--insecure"
		wgetInsecureFlag = "--no-check-certificate"
	}
	configContainerDir := spec.ConfigContainerDir
	if configContainerDir == "" {
		configContainerDir = "/config"
	}
	return fmt.Sprintf(`#!/usr/bin/env sh
set -eu

%s
health_scheme_env=%q
health_host_env=%q
health_port_env=%q
health_default_scheme=%q
health_default_host=%q
health_default_port=%q
health_path=%q
curl_insecure_flag=%q
wget_insecure_flag=%q

usage() {
  echo "usage: %s COMMAND [ARGS...]" >&2
  echo "commands:" >&2
  echo "  up" >&2
  echo "  down" >&2
  echo "  restart" >&2
  echo "  status" >&2
  echo "  logs" >&2
%s  echo "  health" >&2
%s
}

env_value() {
  key="$1"
  default="$2"
  value="$(awk -v key="$key" -F= '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$docker_env" 2>/dev/null || true)"
  if [ -n "$value" ]; then
    printf '%%s\n' "$value"
  else
    printf '%%s\n' "$default"
  fi
}

quote_for_shell() {
  printf "'"
  printf '%%s' "$1" | sed "s/'/'\\\\''/g"
  printf "'"
}

append_shell_arg() {
  shell_command="${shell_command} $(quote_for_shell "$1")"
}

%s

%s

%s

run_app_command() {
  command_name="$1"
  shift
  forwarded_count="$#"
  ensure_managed_files
  append_compose_base
  append_shell_arg "run"
  append_shell_arg "--rm"
  append_shell_arg "--no-deps"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_CONTAINER_COMMAND=$command_name"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_FORWARDED_ARGC=$forwarded_count"
  forwarded_index=0
  for forwarded_arg in "$@"; do
    append_shell_arg "-e"
    append_shell_arg "REPLOY_FORWARDED_ARG_${forwarded_index}=$forwarded_arg"
    forwarded_index=$((forwarded_index + 1))
  done
  append_shell_arg "-e"
  append_shell_arg "REPLOY_CONFIG_CONTAINER_DIR=%s"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_CONFIG_DISPLAY_DIR=$config_display_dir"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_INCLUDE_RUNTIME_OVERRIDES=0"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_CONFIG_MOUNT=rw"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_APP_COMMAND_PREFIX=%s"
  append_terminal_env
  append_shell_arg "app"
  %s
}

validate_forwarded_args() {
  mode="$1"
  allowed_flags="$2"
  shift 2
  if [ "$mode" = "args" ]; then
    return 0
  fi
  while [ "$#" -gt 0 ]; do
    arg="$1"
    shift
    case "$arg" in
      --*=*) flag="${arg%%%%=*}" ;;
      --?*) flag="$arg" ;;
      *)
        echo "unexpected positional argument after app command trigger: $arg" >&2
        return 2
        ;;
    esac
    found=0
    for allowed_flag in $allowed_flags; do
      if [ "$flag" = "$allowed_flag" ]; then
        found=1
      fi
    done
    if [ "$found" -ne 1 ]; then
      echo "unknown forwarded flag: $flag" >&2
      return 2
    fi
  done
}

health_url() {
  if [ -z "$health_path" ] || [ -z "$health_scheme_env" ] || [ -z "$health_host_env" ] || [ -z "$health_port_env" ]; then
    echo "health check is not declared by this blueprint" >&2
    exit 1
  fi
  scheme="$(env_value "$health_scheme_env" "$health_default_scheme")"
  host="$(env_value "$health_host_env" "$health_default_host")"
  port="$(env_value "$health_port_env" "$health_default_port")"
  if [ -z "$port" ]; then
    echo "health check port is not configured" >&2
    exit 1
  fi
  if [ "$host" = "0.0.0.0" ]; then
    host="127.0.0.1"
  fi
  printf '%%s://%%s:%%s%%s\n' "$scheme" "$host" "$port" "$health_path"
}

cmd="${1:-}"
if [ "$#" -gt 0 ]; then
  shift
fi

case "$cmd" in
%s
  health)
    url="$(health_url)"
    if command -v curl >/dev/null 2>&1; then
      shell_command=""
      append_shell_arg "curl"
      append_shell_arg "-fsS"
      if [ -n "$curl_insecure_flag" ]; then
        append_shell_arg "$curl_insecure_flag"
      fi
      append_shell_arg "$url"
      %s
      exit $?
    fi
    if command -v wget >/dev/null 2>&1; then
      shell_command=""
      append_shell_arg "wget"
      append_shell_arg "-qO-"
      if [ -n "$wget_insecure_flag" ]; then
        append_shell_arg "$wget_insecure_flag"
      fi
      append_shell_arg "$url"
      %s
      exit $?
    fi
    echo "curl or wget is required for health checks" >&2
    exit 1
    ;;
  ""|-h|--help|help)
    usage
    exit 0
    ;;
%s
  *)
    usage
    echo "unknown command: $cmd" >&2
    exit 2
    ;;
esac
`, controlScriptAssignments(spec), health.SchemeEnv, health.HostEnv, health.PortEnv, defaultString(health.DefaultScheme, "https"), defaultString(health.DefaultHost, "127.0.0.1"), health.DefaultPort, health.Path, insecureFlag, wgetInsecureFlag, spec.ControlScript, controlScriptServiceUsage(spec), controlScriptUsageCommands(spec.DeployedCommands), controlScriptOutputPrefixFunctions(spec), controlScriptManagedFileValidation(spec), controlScriptComposeFunctions(spec), configContainerDir, spec.ControlScript, controlScriptAppCommandRunner(spec), controlScriptLifecycleCases(spec), controlScriptHealthCommandRunner(spec), controlScriptHealthCommandRunner(spec), controlScriptAppCommandCases(spec.DeployedCommands))
}

func controlScriptAssignments(spec controlScriptSpec) string {
	if spec.Mode == controlScriptModeStaged {
		configDisplayDir := `config_display_dir="$target_dir"`
		if spec.ConfigDir != "" {
			configDisplayDir = fmt.Sprintf(`config_display_dir="$target_dir"/%s`, shellSingleQuote(spec.ConfigDir))
		}
		return fmt.Sprintf(`target_dir="${REPLOY_DEPLOY_DIR:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)}"
compose_file="$target_dir/%s"
compose_override_file=""
if [ -f "$target_dir/%s" ]; then
  compose_override_file="$target_dir/%s"
fi
docker_env="$target_dir/%s"
%s`, ComposeFileName, ComposeOverrideFileName, ComposeOverrideFileName, DockerEnvFileName, configDisplayDir)
	}
	composeOverrideFile := ""
	if spec.ComposeOverride {
		composeOverrideFile = filepath.Join(spec.TargetDir, ComposeOverrideFileName)
	}
	return fmt.Sprintf(`target_dir=%q
service=%q
compose_project=%q
compose_file=%q
compose_override_file=%q
docker_env=%q
config_display_dir=%q`, spec.TargetDir, spec.Service+".service", spec.ComposeProject, filepath.Join(spec.TargetDir, ComposeFileName), composeOverrideFile, filepath.Join(spec.TargetDir, DockerEnvFileName), filepath.Join(spec.TargetDir, spec.ConfigDir))
}

func controlScriptServiceUsage(spec controlScriptSpec) string {
	if spec.Mode != controlScriptModeDeployed {
		return ""
	}
	return `  echo "  enable" >&2
  echo "  disable" >&2
`
}

func controlScriptOutputPrefixFunctions(spec controlScriptSpec) string {
	color, label := controlScriptOutputStyle(spec)
	return fmt.Sprintf(`
reploy_control_stdout_is_tty=0
if [ -t 1 ]; then
  reploy_control_stdout_is_tty=1
fi

control_output_prefix() {
  case "${REPLOY_COLOR:-auto}" in
    always)
      printf '\033[38;5;%sm%s\033[0m'
      ;;
    never)
      printf '%s'
      ;;
    *)
      if [ -n "${NO_COLOR:-}" ] || [ "$reploy_control_stdout_is_tty" != 1 ]; then
        printf '%s'
      else
        printf '\033[38;5;%sm%s\033[0m'
      fi
      ;;
  esac
}

run_control_shell_command_recorded() {
  reploy_control_command="$1"
  reploy_control_prefix="$(control_output_prefix)"
  reploy_control_status_file="$(mktemp "${TMPDIR:-/tmp}/reploy-output-prefix.XXXXXX")" || return 1
  reploy_control_output_file="$(mktemp "${TMPDIR:-/tmp}/reploy-output-prefix.XXXXXX")" || {
    rm -f "$reploy_control_status_file"
    return 1
  }
  (
    set +e
    sh -c "$reploy_control_command"
    reploy_control_status="$?"
    printf '%%s\n' "$reploy_control_status" > "$reploy_control_status_file"
    exit 0
  ) 2>&1 | while IFS= read -r reploy_control_line || [ -n "$reploy_control_line" ]; do
    printf '%%s\n' "$reploy_control_line" >> "$reploy_control_output_file"
    printf '%%s %%s\n' "$reploy_control_prefix" "$reploy_control_line"
  done
  if [ -r "$reploy_control_status_file" ]; then
    IFS= read -r reploy_control_status < "$reploy_control_status_file"
    rm -f "$reploy_control_status_file"
    return "$reploy_control_status"
  fi
  return 1
}

run_control_shell_command() {
  reploy_control_output_file=""
  if run_control_shell_command_recorded "$1"; then
    reploy_control_result=0
  else
    reploy_control_result="$?"
  fi
  if [ -n "$reploy_control_output_file" ]; then
    rm -f "$reploy_control_output_file"
  fi
  return "$reploy_control_result"
}`, color, label, label, label, color, label)
}

func controlScriptOutputStyle(spec controlScriptSpec) (string, string) {
	if spec.Mode == controlScriptModeStaged {
		return stagingOutputColor, deploymentOutputLabel(stagingOutputPhase, spec.AppID)
	}
	return deployedOutputColor, controlScriptOutputLabel(spec.AppID)
}

func controlScriptOutputLabel(appID string) string {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "[reploy]"
	}
	return "[" + appID + "]"
}

func controlScriptManagedFileValidation(spec controlScriptSpec) string {
	if len(spec.ManagedFiles) == 0 {
		return `validate_managed_files() {
  return 0
}

ensure_managed_files() {
  return 0
}`
	}
	lines := []string{`validate_managed_files() {`}
	for _, relativePath := range spec.ManagedFiles {
		lines = append(lines,
			fmt.Sprintf(`  if [ ! -f "$target_dir"/%s ]; then`, shellSingleQuote(relativePath)),
			fmt.Sprintf(`    printf '%%s%%s\n' "managed file is missing: $target_dir/" %s >&2`, shellSingleQuote(relativePath)),
			`    return 1`,
			`  fi`,
		)
	}
	lines = append(lines, `}`)
	lines = append(lines, ``, `ensure_managed_files() {`)
	for _, relativePath := range spec.ManagedFiles {
		quotedPath := shellSingleQuote(relativePath)
		lines = append(lines,
			fmt.Sprintf(`  if [ -e "$target_dir"/%s ] || [ -L "$target_dir"/%s ]; then`, quotedPath, quotedPath),
			fmt.Sprintf(`    if [ ! -f "$target_dir"/%s ]; then`, quotedPath),
			fmt.Sprintf(`      printf '%%s%%s\n' "managed path must be a file: $target_dir/" %s >&2`, quotedPath),
			`      return 1`,
			`    fi`,
			`  else`,
			fmt.Sprintf(`    mkdir -p "$(dirname "$target_dir"/%s)"`, quotedPath),
			fmt.Sprintf(`    : > "$target_dir"/%s`, quotedPath),
			fmt.Sprintf(`    chmod 600 "$target_dir"/%s 2>/dev/null || true`, quotedPath),
			`    container_user="$(env_value REPLOY_CONTAINER_USER "")"`,
			fmt.Sprintf(`    if [ "$(id -u)" = "0" ] && [ -n "$container_user" ]; then chown "$container_user" "$target_dir"/%s; fi`, quotedPath),
			`  fi`,
		)
	}
	lines = append(lines, `}`)
	return strings.Join(lines, "\n")
}

func controlScriptComposeFunctions(spec controlScriptSpec) string {
	projectLines := `  if [ -n "$compose_project" ]; then
    append_shell_arg "--project-name"
    append_shell_arg "$compose_project"
  fi`
	if spec.Mode == controlScriptModeStaged {
		projectLines = `  compose_project="$(env_value REPLOY_CONTAINER_NAME "")"
  if [ -z "$compose_project" ]; then
    compose_project="$(env_value REPLOY_DOCKER_NETWORK_NAME "")"
  fi
  if [ -n "$compose_project" ]; then
    append_shell_arg "--project-name"
    append_shell_arg "$compose_project"
  fi`
	}
	runCompose := ""
	if spec.Mode == controlScriptModeStaged || spec.Mode == controlScriptModeDockerDesktop {
		runCompose = `

run_compose() {
  append_compose_base
  for compose_arg in "$@"; do
    append_shell_arg "$compose_arg"
  done
  run_control_shell_command "$shell_command"
}

run_compose_up() {
  validate_managed_files
  shell_command=""
  append_compose_base
  append_shell_arg "up"
  append_shell_arg "-d"
  for compose_arg in "$@"; do
    append_shell_arg "$compose_arg"
  done
  if run_control_shell_command_recorded "$shell_command"; then
    reploy_up_status=0
  else
    reploy_up_status="$?"
  fi
  reploy_up_output_file="$reploy_control_output_file"
  if [ "$reploy_up_status" -eq 0 ]; then
    rm -f "$reploy_up_output_file"
    return 0
  fi
  if [ -r "$reploy_up_output_file" ] && grep -E 'network .+ not found' "$reploy_up_output_file" >/dev/null 2>&1; then
    rm -f "$reploy_up_output_file"
    printf '%s detected stale Docker network state; running down --remove-orphans and retrying up\n' "$(control_output_prefix)"
    shell_command=""
    append_compose_base
    append_shell_arg "down"
    append_shell_arg "--remove-orphans"
    if ! run_control_shell_command "$shell_command"; then
      return 1
    fi
    shell_command=""
    append_compose_base
    append_shell_arg "up"
    append_shell_arg "-d"
    for compose_arg in "$@"; do
      append_shell_arg "$compose_arg"
    done
    if run_control_shell_command "$shell_command"; then
      return 0
    else
      return $?
    fi
  fi
  rm -f "$reploy_up_output_file"
  return "$reploy_up_status"
}`
	}
	return fmt.Sprintf(`append_compose_base() {
  shell_command="COMPOSE_PROGRESS=quiet COMPOSE_ANSI=never docker compose"
%s
  append_shell_arg "--project-directory"
  append_shell_arg "$target_dir"
  append_shell_arg "--env-file"
  append_shell_arg "$docker_env"
  append_shell_arg "-f"
  append_shell_arg "$compose_file"
  if [ -n "$compose_override_file" ]; then
    append_shell_arg "-f"
    append_shell_arg "$compose_override_file"
  fi
}%s%s%s`, projectLines, controlScriptTerminalEnvFunctions(spec), runCompose, controlScriptRunAsOwnerFunction(spec))
}

func controlScriptTerminalEnvFunctions(spec controlScriptSpec) string {
	colorEnv := strings.TrimSpace(spec.Terminal.ColorEnv)
	colorBlock := ""
	if colorEnv != "" {
		colorBlock = fmt.Sprintf(`
  reploy_terminal_color_set=""
  reploy_terminal_color_value=""
  eval "reploy_terminal_color_set=\${%s+set}"
  if [ -n "$reploy_terminal_color_set" ]; then
    eval "reploy_terminal_color_value=\${%s-}"
  else
    reploy_terminal_color_value="$(env_value %s "")"
  fi
  if [ -z "$reploy_terminal_color_value" ] && [ -z "$reploy_terminal_color_set" ]; then
    reploy_color_mode="$(printf '%%s' "${REPLOY_COLOR:-auto}" | tr '[:upper:]' '[:lower:]')"
    case "$reploy_color_mode" in
      always)
        reploy_terminal_color_value="always"
        ;;
      never)
        reploy_terminal_color_value="never"
        ;;
      ""|auto)
        if [ -n "${NO_COLOR:-}" ]; then
          reploy_terminal_color_value="never"
        elif [ "$reploy_control_stdout_is_tty" = 1 ]; then
          case "${TERM:-}" in
            ""|dumb) ;;
            *) reploy_terminal_color_value="always" ;;
          esac
        fi
        ;;
      *) ;;
    esac
  fi
  if [ -n "$reploy_terminal_color_value" ] || [ -n "$reploy_terminal_color_set" ]; then
    append_shell_arg "-e"
    append_shell_arg "%s=$reploy_terminal_color_value"
  fi`, colorEnv, colorEnv, shellSingleQuote(colorEnv), colorEnv)
	}
	return fmt.Sprintf(`
append_terminal_env() {%s
  case "${COLUMNS:-}" in
    ""|*[!0123456789]*) ;;
    *)
      if [ "$COLUMNS" -ge 20 ] && [ "$COLUMNS" -le 1000 ]; then
        append_shell_arg "-e"
        append_shell_arg "COLUMNS=$COLUMNS"
      fi
      ;;
  esac
}`, colorBlock)
}

func controlScriptRunAsOwnerFunction(spec controlScriptSpec) string {
	if spec.Mode != controlScriptModeDeployed {
		return ""
	}
	return `

run_as_install_owner() {
  command="$1"
  owner="$(env_value REPLOY_INSTALL_OWNER "")"
  if [ "$(id -u)" != "0" ] || [ -z "$owner" ]; then
    sh -c "$command"
    return $?
  fi
  owner_user="${owner%%:*}"
  owner_group="$owner_user"
  case "$owner" in
    *:*) owner_group="${owner#*:}" ;;
  esac
  case "$owner_user:$owner_group" in
    *[!0123456789:]*)
      if command -v runuser >/dev/null 2>&1; then
        runuser -u "$owner_user" -- sh -c "$command"
        return $?
      fi
      ;;
    *)
      if command -v setpriv >/dev/null 2>&1; then
        setpriv --reuid "$owner_user" --regid "$owner_group" --clear-groups -- sh -c "$command"
        return $?
      fi
      ;;
  esac
  echo "setpriv or runuser is required to run deployed app commands as $owner" >&2
  return 1
}

run_control_owner_command() {
  reploy_control_command="$1"
  reploy_control_prefix="$(control_output_prefix)"
  reploy_control_status_file="$(mktemp "${TMPDIR:-/tmp}/reploy-output-prefix.XXXXXX")" || return 1
  (
    set +e
    run_as_install_owner "$reploy_control_command"
    reploy_control_status="$?"
    printf '%s\n' "$reploy_control_status" > "$reploy_control_status_file"
    exit 0
  ) 2>&1 | while IFS= read -r reploy_control_line || [ -n "$reploy_control_line" ]; do
    printf '%s %s\n' "$reploy_control_prefix" "$reploy_control_line"
  done
  if [ -r "$reploy_control_status_file" ]; then
    IFS= read -r reploy_control_status < "$reploy_control_status_file"
    rm -f "$reploy_control_status_file"
    return "$reploy_control_status"
  fi
  return 1
}`
}

func controlScriptAppCommandRunner(spec controlScriptSpec) string {
	if spec.Mode == controlScriptModeDeployed {
		return `run_control_owner_command "$shell_command"`
	}
	return `run_control_shell_command "$shell_command"`
}

func controlScriptHealthCommandRunner(spec controlScriptSpec) string {
	return `run_control_shell_command "$shell_command"`
}

func controlScriptLifecycleCases(spec controlScriptSpec) string {
	if spec.Mode == controlScriptModeStaged || spec.Mode == controlScriptModeDockerDesktop {
		return fmt.Sprintf(`  up|start)
    run_compose_up
    ;;
  down|stop)
    run_compose down --remove-orphans
    ;;
  restart)
    run_compose_up --force-recreate
    ;;
  status|ps)
    run_compose ps
    ;;
  logs)
    shell_command=""
    append_compose_base
    append_shell_arg "logs"
    append_shell_arg "--timestamps"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --follow|-f)
          append_shell_arg "-f"
          shift
          ;;
        --tail)
          shift
          if [ "$#" -eq 0 ]; then
            echo "logs: --tail requires a value" >&2
            exit 2
          fi
          append_shell_arg "--tail"
          append_shell_arg "$1"
          shift
          ;;
        --tail=*)
          tail_value="${1#--tail=}"
          if [ -z "$tail_value" ]; then
            echo "logs: --tail requires a value" >&2
            exit 2
          fi
          append_shell_arg "--tail"
          append_shell_arg "$tail_value"
          shift
          ;;
        --help|-h)
          echo "usage: %s logs [--tail N] [--follow]" >&2
          exit 0
          ;;
        *)
          echo "logs: unknown option: $1" >&2
          exit 2
          ;;
      esac
    done
    run_control_shell_command "$shell_command"
    ;;
`, spec.ControlScript)
	}
	return fmt.Sprintf(`  up|start)
    shell_command=""
    append_shell_arg "systemctl"
    append_shell_arg "start"
    append_shell_arg "$service"
    run_control_shell_command "$shell_command"
    ;;
  down|stop)
    shell_command=""
    append_shell_arg "systemctl"
    append_shell_arg "stop"
    append_shell_arg "$service"
    run_control_shell_command "$shell_command"
    ;;
  restart)
    shell_command=""
    append_shell_arg "systemctl"
    append_shell_arg "restart"
    append_shell_arg "$service"
    run_control_shell_command "$shell_command"
    ;;
  status)
    shell_command=""
    append_shell_arg "systemctl"
    append_shell_arg "status"
    append_shell_arg "$service"
    run_control_shell_command "$shell_command"
    ;;
  logs)
    shell_command=""
    append_shell_arg "journalctl"
    append_shell_arg "-u"
    append_shell_arg "$service"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --follow|-f)
          append_shell_arg "-f"
          shift
          ;;
        --tail)
          shift
          if [ "$#" -eq 0 ]; then
            echo "logs: --tail requires a value" >&2
            exit 2
          fi
          append_shell_arg "-n"
          append_shell_arg "$1"
          shift
          ;;
        --tail=*)
          tail_value="${1#--tail=}"
          if [ -z "$tail_value" ]; then
            echo "logs: --tail requires a value" >&2
            exit 2
          fi
          append_shell_arg "-n"
          append_shell_arg "$tail_value"
          shift
          ;;
        --help|-h)
          echo "usage: %s logs [--tail N] [--follow]" >&2
          exit 0
          ;;
        *)
          echo "logs: unknown option: $1" >&2
          exit 2
          ;;
      esac
    done
    run_control_shell_command "$shell_command"
    ;;
  enable)
    shell_command=""
    append_shell_arg "systemctl"
    append_shell_arg "enable"
    append_shell_arg "$service"
    run_control_shell_command "$shell_command"
    ;;
  disable)
    shell_command=""
    append_shell_arg "systemctl"
    append_shell_arg "disable"
    append_shell_arg "$service"
    run_control_shell_command "$shell_command"
    ;;
`, spec.ControlScript)
}

func controlScriptUsageCommands(commands []deploy.DockerCommandConfig) string {
	if len(commands) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, command := range commands {
		builder.WriteString("  printf '%s\\n' ")
		builder.WriteString(shellSingleQuote("  " + strings.Join(command.Trigger, " ")))
		builder.WriteString(" >&2\n")
	}
	return builder.String()
}

func controlScriptAppCommandCases(commands []deploy.DockerCommandConfig) string {
	if len(commands) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("  *)\n")
	for _, command := range commands {
		builder.WriteString("    if ")
		for index, part := range command.Trigger {
			if index > 0 {
				builder.WriteString(" && ")
			}
			if index == 0 {
				builder.WriteString("[ \"$cmd\" = ")
			} else {
				builder.WriteString(fmt.Sprintf("[ \"${%d:-}\" = ", index))
			}
			builder.WriteString(shellSingleQuote(part))
			builder.WriteString(" ]")
		}
		builder.WriteString("; then\n")
		for index := 1; index < len(command.Trigger); index++ {
			_ = index
			builder.WriteString("      shift\n")
		}
		mode := "flags"
		allowedFlags := strings.Join(command.ForwardFlags, " ")
		if command.ForwardArgs {
			mode = "args"
			allowedFlags = ""
		}
		builder.WriteString("      if ! validate_forwarded_args ")
		builder.WriteString(shellSingleQuote(mode))
		builder.WriteString(" ")
		builder.WriteString(shellSingleQuote(allowedFlags))
		builder.WriteString(" \"$@\"; then\n")
		builder.WriteString("        exit 2\n")
		builder.WriteString("      fi\n")
		builder.WriteString("      run_app_command ")
		builder.WriteString(shellSingleQuote(command.Name))
		builder.WriteString(" \"$@\"\n")
		builder.WriteString("      exit $?\n")
		builder.WriteString("    fi\n")
	}
	builder.WriteString("    usage\n")
	builder.WriteString("    echo \"unknown command: $cmd\" >&2\n")
	builder.WriteString("    exit 2\n")
	builder.WriteString("    ;;\n")
	return builder.String()
}

func powerShellUsageCommands(commands []deploy.DockerCommandConfig) string {
	if len(commands) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, command := range commands {
		builder.WriteString("  ")
		builder.WriteString(strings.Join(command.Trigger, " "))
		builder.WriteByte('\n')
	}
	return strings.TrimRight(builder.String(), "\n")
}

func powerShellTerminalEnv(terminal deploy.AppTerminalConfig) string {
	colorEnv := strings.TrimSpace(terminal.ColorEnv)
	if colorEnv == "" {
		return ""
	}
	return fmt.Sprintf(`    $ColorEnvName = %s
    $ColorValue = [Environment]::GetEnvironmentVariable($ColorEnvName)
    $ColorWasSet = Test-Path ("Env:" + $ColorEnvName)
    if (-not $ColorWasSet) {
        $ReployColorMode = [Environment]::GetEnvironmentVariable('REPLOY_COLOR')
        if ([string]::IsNullOrWhiteSpace($ReployColorMode)) {
            $ReployColorMode = 'auto'
        }
        switch ($ReployColorMode.Trim().ToLowerInvariant()) {
            'always' {
                $ColorValue = 'always'
            }
            'never' {
                $ColorValue = 'never'
            }
            'auto' {
                if (Test-Path Env:NO_COLOR) {
                    $ColorValue = 'never'
                } else {
                    $OutputIsTerminal = $true
                    try {
                        $OutputIsTerminal = -not [Console]::IsOutputRedirected
                    } catch {
                        $OutputIsTerminal = $true
                    }
                    $SupportsVirtualTerminal = $false
                    try {
                        $SupportsVirtualTerminal = [bool]$Host.UI.SupportsVirtualTerminal
                    } catch {
                        $SupportsVirtualTerminal = $false
                    }
                    if ($OutputIsTerminal -and $SupportsVirtualTerminal) {
                        $ColorValue = 'always'
                    }
                }
            }
        }
    }
    if ($ColorWasSet -or -not [string]::IsNullOrEmpty($ColorValue)) {
        $ComposeArgs += '-e'
        $ComposeArgs += "$ColorEnvName=$ColorValue"
    }
`, powerShellSingleQuote(colorEnv))
}

func powerShellAppCommandCases(commands []deploy.DockerCommandConfig) string {
	if len(commands) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, command := range commands {
		builder.WriteString("    { ")
		for index, part := range command.Trigger {
			if index > 0 {
				builder.WriteString(" -and ")
			}
			if index == 0 {
				builder.WriteString("$Command -eq ")
			} else {
				builder.WriteString(fmt.Sprintf("$RemainingArgs.Count -ge %d -and $RemainingArgs[%d] -eq ", index, index-1))
			}
			builder.WriteString(powerShellSingleQuote(part))
		}
		builder.WriteString(" } {\n")
		mode := "flags"
		allowedFlags := command.ForwardFlags
		if command.ForwardArgs {
			mode = "args"
			allowedFlags = nil
		}
		builder.WriteString(fmt.Sprintf("        $ForwardedArgs = Select-ForwardedArgs -Args $RemainingArgs -Skip %d\n", len(command.Trigger)-1))
		builder.WriteString("        Test-ForwardedArgs -Mode ")
		builder.WriteString(powerShellSingleQuote(mode))
		builder.WriteString(" -AllowedFlags @(")
		builder.WriteString(powerShellArrayLiteral(allowedFlags))
		builder.WriteString(") -Args $ForwardedArgs\n")
		builder.WriteString("        Invoke-AppCommand -CommandName ")
		builder.WriteString(powerShellSingleQuote(command.Name))
		builder.WriteString(" -ForwardedArgs $ForwardedArgs\n")
		builder.WriteString("    }\n")
	}
	return builder.String()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func powerShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func powerShellArrayLiteral(values []string) string {
	if len(values) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, powerShellSingleQuote(value))
	}
	return strings.Join(quoted, ", ")
}
