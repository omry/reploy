package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type controlScriptMode string

const (
	controlScriptModeStaged   controlScriptMode = "staged"
	controlScriptModeDeployed controlScriptMode = "deployed"
)

type controlScriptSpec struct {
	Mode             controlScriptMode
	TargetDir        string
	Service          string
	ComposeProject   string
	ComposeOverride  bool
	ControlScript    string
	ConfigDir        string
	Health           deploy.DockerHealthConfig
	DeployedCommands []deploy.DockerCommandConfig
}

func stagingControlScriptContent(pack deploy.AppPack, deployedCommands []deploy.DockerCommandConfig) string {
	return renderControlScript(controlScriptSpec{
		Mode:             controlScriptModeStaged,
		ControlScript:    controlScriptName(pack.AppID),
		ConfigDir:        pack.Docker.DeploymentDirs.Config,
		Health:           pack.Docker.Health,
		DeployedCommands: deployedCommands,
	})
}

func controlScriptContent(plan installPlan) string {
	return renderControlScript(controlScriptSpec{
		Mode:             controlScriptModeDeployed,
		TargetDir:        plan.TargetDir,
		Service:          plan.Service,
		ComposeProject:   plan.ComposeProject,
		ComposeOverride:  plan.ComposeOverride,
		ControlScript:    plan.ControlScript,
		ConfigDir:        plan.ConfigDir,
		Health:           plan.Health,
		DeployedCommands: plan.DeployedCommands,
	})
}

func renderControlScript(spec controlScriptSpec) string {
	health := spec.Health
	insecureFlag := ""
	wgetInsecureFlag := ""
	if !healthTLSVerify(health) {
		insecureFlag = "--insecure"
		wgetInsecureFlag = "--no-check-certificate"
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

run_app_command() {
  command_name="$1"
  shift
  forwarded_count="$#"
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
  append_shell_arg "REPLOY_CONFIG_CONTAINER_DIR=/config"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_CONFIG_DISPLAY_DIR=$config_display_dir"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_INCLUDE_RUNTIME_OVERRIDES=0"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_CONFIG_MOUNT=rw"
  append_shell_arg "-e"
  append_shell_arg "REPLOY_APP_COMMAND_PREFIX=%s"
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
      if [ -n "$curl_insecure_flag" ]; then
        exec curl -fsS "$curl_insecure_flag" "$url"
      fi
      exec curl -fsS "$url"
    fi
    if command -v wget >/dev/null 2>&1; then
      if [ -n "$wget_insecure_flag" ]; then
        exec wget -qO- "$wget_insecure_flag" "$url"
      fi
      exec wget -qO- "$url"
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
`, controlScriptAssignments(spec), health.SchemeEnv, health.HostEnv, health.PortEnv, defaultString(health.DefaultScheme, "https"), defaultString(health.DefaultHost, "127.0.0.1"), health.DefaultPort, health.Path, insecureFlag, wgetInsecureFlag, spec.ControlScript, controlScriptServiceUsage(spec), controlScriptUsageCommands(spec.DeployedCommands), controlScriptComposeFunctions(spec), spec.ControlScript, controlScriptAppCommandRunner(spec), controlScriptLifecycleCases(spec), controlScriptAppCommandCases(spec.DeployedCommands))
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
	if spec.Mode == controlScriptModeStaged {
		runCompose = `

run_compose() {
  append_compose_base
  for compose_arg in "$@"; do
    append_shell_arg "$compose_arg"
  done
  exec sh -c "$shell_command"
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
}%s%s`, projectLines, runCompose, controlScriptRunAsOwnerFunction(spec))
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
    exec sh -c "$command"
  fi
  owner_user="${owner%%:*}"
  owner_group="$owner_user"
  case "$owner" in
    *:*) owner_group="${owner#*:}" ;;
  esac
  case "$owner_user:$owner_group" in
    *[!0123456789:]*)
      if command -v runuser >/dev/null 2>&1; then
        exec runuser -u "$owner_user" -- sh -c "$command"
      fi
      ;;
    *)
      if command -v setpriv >/dev/null 2>&1; then
        exec setpriv --reuid "$owner_user" --regid "$owner_group" --clear-groups -- sh -c "$command"
      fi
      ;;
  esac
  echo "setpriv or runuser is required to run deployed app commands as $owner" >&2
  exit 1
}`
}

func controlScriptAppCommandRunner(spec controlScriptSpec) string {
	if spec.Mode == controlScriptModeDeployed {
		return `run_as_install_owner "$shell_command"`
	}
	return `exec sh -c "$shell_command"`
}

func controlScriptLifecycleCases(spec controlScriptSpec) string {
	if spec.Mode == controlScriptModeStaged {
		return `  up|start)
    run_compose up -d
    ;;
  down|stop)
    run_compose down --remove-orphans
    ;;
  restart)
    run_compose up -d --force-recreate
    ;;
  status|ps)
    run_compose ps
    ;;
  logs)
    run_compose logs --timestamps "$@"
    ;;
`
	}
	return `  up|start)
    exec systemctl start "$service"
    ;;
  down|stop)
    exec systemctl stop "$service"
    ;;
  restart)
    exec systemctl restart "$service"
    ;;
  status)
    exec systemctl status "$service"
    ;;
  logs)
    exec journalctl -u "$service" "$@"
    ;;
  enable)
    exec systemctl enable "$service"
    ;;
  disable)
    exec systemctl disable "$service"
    ;;
`
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
		builder.WriteString("    fi\n")
	}
	builder.WriteString("    usage\n")
	builder.WriteString("    echo \"unknown command: $cmd\" >&2\n")
	builder.WriteString("    exit 2\n")
	builder.WriteString("    ;;\n")
	return builder.String()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
