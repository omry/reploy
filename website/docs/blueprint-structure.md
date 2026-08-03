---
sidebar_position: 2
---

# Blueprint Structure

A schema-1 blueprint has two required top-level nodes and one optional backend
node:

```yaml
blueprint:   # Required format version, blueprint version, and platforms.
environment: # Required identity and base image; other portable intent is optional.
docker:      # Optional Docker realization of mounts, endpoints, and restart policy.
```

Unknown fields are errors. Reploy does not accept aliases for the removed
prototype schema.

The smallest valid blueprint is:

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]

environment:
  id: example
  base:
    image: debian:13
```

## Environment Nodes

```yaml
environment:
  id: example                 # Required stable environment name.
  control_script: example     # Optional generated command name; defaults to id.
  vars: {}                    # Values used by blueprint interpolation.
  base:                       # Required base image root.
    image: debian:13          # Required OCI image reference.
  packages: {}                # Environment-owned packages.
  applications: {}            # Application packages, options, and executables.
  allow_concurrent: auto      # App-command and shell overlap policy.
  terminal: {}                # Terminal and color integration.
  install: {}                 # Target, system account, hooks, success output.
  mounts: {}                  # Portable runtime filesystem contracts.
  commands: {}                # Public commands using application executables.
  workload: {}                # Optional persistent primary workload.
```

Optional empty nodes should be omitted.

## Applications and Executables

Every environment has a base root. Applications can contribute OS and Python
packages while keeping one application identity:

```yaml
environment:
  base:
    image: debian:13
  applications:
    application:
      packages:
        os:
          - package: python3
            exports:
              python:
                executable: /usr/bin/python3
          - ca-certificates
        python:
          interpreter:
            command: python
            version: ">=3.11"
            supplier: os
          requirements: [example-suite]
      executables:
        server:
          source: python
          binary: example-server
```

The interpreter requirement is optional when Reploy can identify the single
supported Python supplied by the base or the application's OS contribution. If discovery fails or the
binary is not Python, the error guides the author to the explicit form.

Executable profiles belong to their application. Environment commands reference
them with a qualified name:

```yaml
environment:
  commands:
    serve:
      executable: application.server
      argv: [serve]
    config_check:
      executable: application.server
      trigger: [config, check]
      native_command: true
      deployed_command: true
      forward_flags: [--live]
      argv: [config, check]
  workload:
    command: serve
```

## Mounts

Portable mount contracts are declared under `environment.mounts`; Docker maps
them to a backend mode and source:

```yaml
environment:
  mounts:
    config:
      target: /conf
      writable: true
      update_policy: preserve
    data:
      target: /data
      writable: true
      update_policy: preserve

docker:
  mounts:
    config:
      extends: environment.mounts.config
      mode: managed-bind
      source: conf
    data:
      extends: environment.mounts.data
      mode: managed-bind
      source: data
```

`update_policy` is `preserve`, `replace`, or `unmanaged`. `writable` controls
runtime access; read-only mounts also determine the default `auto` concurrency
policy.

## Concurrency

`allow_concurrent` accepts `yes`, `no`, or `auto` and defaults to `auto`. In
automatic mode, concurrent app commands and shell sessions are allowed only
when all of their mounts are read-only. A blocked caller may use `--wait` to
queue in FIFO order. `reploy runs list` and `reploy runs stop RUN_ID` inspect or
stop active and waiting runs.

## Installation

Installation settings remain part of the portable environment:

```yaml
environment:
  install:
    target:
      default_path: "{{ reploy.install_root }}/{{ environment.id }}"
    system:
      account:
        user: example
        group: example
        on_missing: create
    success:
      lines:
        - "installed {{ environment.id }}"
```

`on_missing: create` allows Reploy to create the declared system account during
a system-scope install. User-scope installs run as the invoking user.

## Endpoints and Readiness

The environment owns the portable endpoint; Docker supplies bind and published
addresses and ports:

```yaml
environment:
  workload:
    command: serve
    endpoints:
      http:
        scheme: http
        port: 8076
        readiness:
          path: /_health_

docker:
  workload:
    endpoints:
      http:
        extends: environment.workload.endpoints.http
        bind:
          address: 0.0.0.0
        publish:
          address: 127.0.0.1
          staging: 18076
          deployed: 19076
```

Use `reploy validate BLUEPRINT_REF` for syntax and semantic checks. Use
`reploy build` after staging to resolve packages, build the image, and perform
full image validation.
