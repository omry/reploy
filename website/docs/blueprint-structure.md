---
sidebar_position: 2
---

import PlatformTabs from '@site/src/components/PlatformTabs';
import TabItem from '@theme/TabItem';

# Blueprint Structure

A blueprint is a YAML manifest owned by the app author. It describes the
app-specific pieces that Reploy needs in order to build bundles, generate Docker
runtime files, expose app commands, and install the service.

## Top-Level Sections

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: example-app
  provider:
    type: python
    identifier: example-suite

install:
  target: {}
  owner:
    user: example
    group: example
    on_missing: create
  ports:
    deployed:
      https:
        host_bind: 127.0.0.1
        host_port: 8075
    staging:
      https:
        host_bind: 127.0.0.1
        host_port: 18075
  managed_paths:
    files:
      - path: .example.env
        update: preserve
        mount: /{{ path }}
    dirs:
      - path: conf
        update: preserve
        mount: /{{ path }}
      - path: data
        update: preserve
        mount: /{{ path }}

bundle:
  options: {}

docker:
  service: {}
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_HOST_BIND
    port_env: REPLOY_HOST_PORT
    default_scheme: https
    default_host: 127.0.0.1
    default_port: "18075"
    path: /_health_
  default_command: serve
  commands: {}
```

`blueprint` identifies the manifest format, the blueprint version, and the
minimum Reploy version expected by the app.

`app` names the deployment and declares the app provider. The first supported
provider is `python`, where `identifier` is the required root package.

`install` declares host install defaults: target path selection, non-root
installed owner, whether Reploy creates that system owner when missing,
deployed and staging port defaults, and managed app-owned paths with update
policy and optional runtime mounts.

Managed path mounts may use the blueprint-time `{{ path }}` placeholder, which
expands to the entry's normalized relative path. For example,
`mount: /{{ path }}` mounts `conf` at `/conf` and `.example.env` at
`/.example.env`. The compact `{{path}}` form is accepted, but `{{ path }}` is
the canonical style. Use `${...}` placeholders only for container/runtime
environment values.

`bundle` declares optional package selections that an app user can add to the
deployment bundle.

`docker` declares the runtime shape: image, ports, deployment directories,
health checks, app commands, and install hooks.

## Install Target

`install.target` controls the default permanent install directory. If the
blueprint omits target defaults, Reploy chooses a built-in host default:

<PlatformTabs>
  <TabItem value="linux">

| Field | Value |
| --- | --- |
| Host/backend | Linux systemd |
| Built-in default | `/opt/{{ app.id }}` |
| For `app.id: example-app` | `/opt/example-app` |

  </TabItem>
  <TabItem value="windows">

| Field | Value |
| --- | --- |
| Host/backend | Windows Docker Desktop |
| Built-in default | `{{ user.local_data }}/Reploy/installs/{{ app.id }}` |
| For `app.id: example-app` | `%LOCALAPPDATA%\Reploy\installs\example-app` |

  </TabItem>
  <TabItem value="macos">

| Field | Value |
| --- | --- |
| Host/backend | Mac Docker Desktop |
| Built-in default | `{{ user.data }}/Reploy/installs/{{ app.id }}` |
| For `app.id: example-app` | `$HOME/Library/Application Support/Reploy/installs/example-app` |

  </TabItem>
</PlatformTabs>

Users can always override the resolved target with `reploy install --to DIR`.

Blueprints may provide one global default:

```yaml
install:
  target:
    default_path: "{{ reploy.install_root }}/{{ app.id }}"
```

Blueprints may also provide per-OS defaults:

```yaml
install:
  target:
    default_paths:
      linux: /opt/{{ app.id }}
      macos: "{{ user.data }}/Acme/{{ app.id }}"
      windows: "{{ user.local_data }}/Acme/{{ app.id }}"
```

Resolution order is:

1. `reploy install --to DIR`
2. `install.target.default_paths.<host_os>`
3. `install.target.default_path`
4. Reploy's built-in default for the host/backend

Supported `default_paths` OS keys are `linux`, `macos`, and `windows`.
Inactive per-OS paths may use that OS's path syntax. For example,
`default_paths.linux: /opt/{{ app.id }}` is valid in a blueprint used on
Windows because it is not the active Windows default.

Supported install-target template variables and default root values are:

<PlatformTabs>
  <TabItem value="linux">

| Variable | Meaning | Linux default |
| --- | --- | --- |
| `{{ app.id }}` | Blueprint app id | App-specific |
| `{{ user.home }}` | Current user's home directory | `$HOME` |
| `{{ user.data }}` | Per-user application data root | `$HOME/.local/share` |
| `{{ user.local_data }}` | Per-user local data root | `$HOME/.local/share` |
| `{{ system.data }}` | System-wide application data root | `/var/lib` |
| `{{ reploy.install_root }}` | Reploy's default install root for this host/backend | `/opt` |
| Built-in install target | Target used when the blueprint omits target defaults | `/opt/{{ app.id }}` |

  </TabItem>
  <TabItem value="windows">

| Variable | Meaning | Windows default |
| --- | --- | --- |
| `{{ app.id }}` | Blueprint app id | App-specific |
| `{{ user.home }}` | Current user's home directory | `%USERPROFILE%` |
| `{{ user.data }}` | Per-user application data root | `%APPDATA%` |
| `{{ user.local_data }}` | Per-user local data root | `%LOCALAPPDATA%` |
| `{{ system.data }}` | System-wide application data root | `%ProgramData%` |
| `{{ reploy.install_root }}` | Reploy's default install root for this host/backend | `%LOCALAPPDATA%\Reploy\installs` |
| Built-in install target | Target used when the blueprint omits target defaults | `%LOCALAPPDATA%\Reploy\installs\{{ app.id }}` |

  </TabItem>
  <TabItem value="macos">

| Variable | Meaning | Mac default |
| --- | --- | --- |
| `{{ app.id }}` | Blueprint app id | App-specific |
| `{{ user.home }}` | Current user's home directory | `$HOME` |
| `{{ user.data }}` | Per-user application data root | `$HOME/Library/Application Support` |
| `{{ user.local_data }}` | Per-user local data root | `$HOME/Library/Application Support` |
| `{{ system.data }}` | System-wide application data root | `/Library/Application Support` |
| `{{ reploy.install_root }}` | Reploy's default install root for this host/backend | `$HOME/Library/Application Support/Reploy/installs` |
| Built-in install target | Target used when the blueprint omits target defaults | `$HOME/Library/Application Support/Reploy/installs/{{ app.id }}` |

  </TabItem>
</PlatformTabs>

On Windows, `{{ user.data }}` falls back to `%LOCALAPPDATA%` if `%APPDATA%`
is not set.

These variables choose the one install directory for the app. Reploy keeps
managed paths such as `conf`, `data`, bundle state, and runtime state localized
under that install directory.

## Bundle Options

Bundle options declare additional choices an app user can include in a
deployment bundle, such as plugins or related artifacts. For Python app
providers, each option points to a package identifier that Reploy can resolve
when the user selects it:

```yaml
bundle:
  options:
    imap:
      identifier: example-imap
      group: plugins
      description: Enable the example IMAP plugin.
```

App users can list and select these options with `reploy bundle list-options`
and `reploy bundle add`.

## Docker Service

The service section defines the default container runtime. Host install
defaults live in `install`, not in Docker-specific fields.

```yaml
docker:
  service:
    image: python:3.11-slim
```

Use `install.ports.deployed` and `install.ports.staging` when the app exposes
more than one named public port.

## App Commands

Commands expose app-specific operations through `reploy app`:

```yaml
docker:
  default_command: serve
  command_defaults:
    app_command: true
    container:
      argv_prefix: [example-server, --config-dir, "${REPLOY_CONFIG_CONTAINER_DIR}"]
  commands:
    serve:
      container:
        argv_suffix: [serve]
    config_check:
      deployed_command: true
      forward_flags: [--live]
      container:
        argv_suffix: [config, check]
    external_status:
      trigger: [status, external]
      container:
        argv: [example-status-tool, inspect]
```

`trigger` is the command path after `reploy app`. When omitted, Reploy derives
it from the command key by splitting underscores, so `config_check` becomes
`reploy app config check`. The `docker.default_command` command remains
internal unless it declares an explicit trigger.

Use `command_defaults` for repeated command settings. `app_command` exposes a
command through `reploy app`. Set `deployed_command: true` on individual app
commands that are safe to expose through the installed app control script, such
as live validation.

Tools can inspect the deployed app-command surface with:

```bash
reploy app --commands --deployed-only --format json --dir DIR
```

For container arguments, `argv_prefix` plus `argv_suffix` produces the final
command. A command-level `container.argv` is a full override and does not use
the prefix. Quote `${...}` placeholders inside flow-style YAML lists.
`forward_flags` and `forward_args` control what user input is passed through to
the container command.

## Install Hooks

Install hooks let the app run checks before or after the service starts:

```yaml
docker:
  install:
    hooks:
      before_start:
        - app:
            - config
            - check
      after_start:
        - health_check:
            wait: true
```

Use app hooks for app-owned validation, and health-check hooks for service
readiness.

For a working reference, see
`tests/e2e/python/packages/smoke-blueprint/smoke.blueprint.yaml`.
