---
sidebar_position: 2
---

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
  target:
    default_path: /opt/{{ app.id }}
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
  upgrade:
    artifacts:
      config:
        default: preserve
        paths:
          - conf/

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

`install` declares host install defaults: target path, non-root installed
owner, whether Reploy creates that system owner when missing, deployed and
staging port defaults, and app-owned artifact upgrade policy.

`bundle` declares optional package selections that an app user can add to the
deployment bundle.

`docker` declares the runtime shape: image, ports, deployment directories,
health checks, app commands, and install hooks.

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
    deployed_command: true
    container:
      argv_prefix: [example-server, --config-dir, "${REPLOY_CONFIG_CONTAINER_DIR}"]
  commands:
    serve:
      container:
        argv_suffix: [serve]
    config_check:
      forward_flags: [--live]
      container:
        argv_suffix: [config, check]
    external_status:
      trigger: [status, external]
      deployed_command: false
      container:
        argv: [example-status-tool, inspect]
```

`trigger` is the command path after `reploy app`. When omitted, Reploy derives
it from the command key by splitting underscores, so `config_check` becomes
`reploy app config check`. The `docker.default_command` command remains
internal unless it declares an explicit trigger.

Use `command_defaults` for repeated command settings. `app_command` exposes a
command through `reploy app`; `deployed_command` also exposes it through the
installed app control script. Individual commands can override these defaults.
Set `deployed_command: true` only for app commands that are safe to expose
there, such as live validation.

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
