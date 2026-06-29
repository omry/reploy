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
- Which install target, owner, staging ports, and deployed ports should be the
  defaults?
- Which Docker image and directories should the app use?
- Which health check proves the service is up?
- Which app-specific hooks should run during install?

## Current Backend and Runtime

The first supported app backend is Python. The first supported runtime is
Docker. The first permanent install target is Linux with systemd.

That means the current authoring path is strongest for apps that can publish
Python packages and run inside a Docker container.

Reploy exposes `REPLOY_DEPLOYMENT_SCOPE` inside the app container. The value is
`staging` while running from a staging directory and `deployed` after a
permanent install. Apps can use this for logs, diagnostics, and support output
without hard-coding their own staging/deployed marker in the blueprint.

## Minimal Shape

The local end-to-end fixture in this repository is a good starting point:

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

bundle:
  options:
    imap:
      identifier: example-imap
      group: plugins
      description: Install the example IMAP plugin.

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
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - example-server
          - serve
    config_check:
      trigger:
        - config
        - check
      app_command: true
      deployed_command: true
      forward_flags:
        - --live
      container:
        argv:
          - example-server
          - config
          - check
```

For a working reference, see the fixture under
`tests/e2e/python/packages/smoke-blueprint/`.

## Publishing

Blueprint refs can be local files while developing:

```bash
reploy stage file:./example.blueprint.yaml
reploy install file:./example.blueprint.yaml --dry-run
```

For users, publish the blueprint inside the app package and give them an indexed
shortcut:

```bash
reploy stage example-app
reploy install example-app
```

Direct PyPI refs remain available when an exact package path is useful:

```bash
reploy stage pypi://example-app/example_app/reploy/example.blueprint.yaml
reploy install pypi://example-app/example_app/reploy/example.blueprint.yaml
```
