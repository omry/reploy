---
sidebar_position: 1
---

# App Author

This page is for app authors who want app users to install their app through
Reploy.

Your job is to publish a blueprint that describes the app-specific pieces.
Reploy handles the generic lifecycle around that blueprint: creating a staging
deployment directory, preparing bundles, generating Docker runtime files,
running health checks, installing or updating the service, and generating the
local app control script for installed deployments.

## What the Blueprint Owns

A blueprint answers these questions:

- Which app backend provides the deployable packages?
- Which bundle options can users select?
- Which runtime commands should Reploy expose?
- Which install target overrides, system run-as account, staging ports, and
  deployed ports should be the defaults?
- Which Docker image and directories should the app use?
- Which health check proves the service is up?
- Which app-specific hooks should run during install?

## Current Backend and Runtime

The first supported app backend is Python. The first supported runtime is
Docker. Linux supports current-user Docker-managed installs and system-scope
systemd installs. macOS and Windows support development, staging, and
Docker-managed user-scope permanent installs with Docker Desktop.

That means the current authoring path is strongest for apps that can publish
Python packages and run inside a Docker container.

Reploy exposes `REPLOY_DEPLOYMENT_SCOPE` inside the app container. The value is
`staging` while running from a staging directory and `deployed` after a
permanent install. Apps can use this for logs, diagnostics, and support output
without hard-coding their own staging/deployed marker in the blueprint.

## Minimal Shape

This example shape is a good starting point:

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.5.1.dev1"

app:
  id: example-app
  provider:
    type: python
    identifier: example-suite

bundle:
  options:
    imap:
      identifier: example-imap
      group: plugins
      description: Install the example IMAP plugin.

install:
  target: {}
  system:
    run_as:
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
    dirs:
      - path: conf
        update: preserve
        mount: /{{ path }}
      - path: data
        update: preserve
        mount: /{{ path }}
        writeable: true

docker:
  service:
    image: python:3.11-slim
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_HOST_BIND
    port_env: REPLOY_HOST_PORT
    default_scheme: https
    default_host: 127.0.0.1
    default_port: "18075"
    path: /_health_
  runtime:
    hooks:
      after_start:
        - health_check:
            wait: true
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
```

For a working reference, see the fixture under
`tests/e2e/python/packages/smoke-blueprint/`.

## Publishing

Blueprint refs can be local files while developing:

```bash
reploy stage file:./example.blueprint.yaml
reploy install file:./example.blueprint.yaml --scope <user|system> --dry-run
```

For users, publish the blueprint inside the app package and give them an indexed
shortcut:

```bash
reploy stage example-app
reploy install example-app --scope <user|system>
```

Direct PyPI refs remain available when an exact package path is useful:

```bash
reploy stage pypi://example-app/example_app/reploy/example.blueprint.yaml
reploy install pypi://example-app/example_app/reploy/example.blueprint.yaml --scope <user|system>
```
