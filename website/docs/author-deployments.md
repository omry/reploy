---
sidebar_position: 1
---

# App Author

This page is for app authors who want app users to install their app through
Reploy.

Your job is to publish a blueprint that describes the app-specific pieces.
Reploy handles the generic lifecycle around that blueprint: initializing a
deployment directory, preparing bundles, generating Docker runtime files,
running health checks, and installing or uninstalling the service.

## What the Blueprint Owns

A blueprint answers these questions:

- Which app backend provides the deployable packages?
- Which bundle options can users select?
- Which runtime commands should Reploy expose?
- Which Docker image, ports, and directories should the app use?
- Which health check proves the service is up?
- Which app-specific hooks should run during install?

## Current Backend and Runtime

The first supported app backend is Python. The first supported runtime is
Docker. The first permanent install target is Linux with systemd.

That means the current authoring path is strongest for apps that can publish
Python packages and run inside a Docker container.

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

docker:
  service:
    image: python:3.11-slim
    install_owner: "example:example"
    host_bind: 127.0.0.1
    host_port: "18075"
    container_port: "8080"
  health:
    path: /_health_
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - example-server
          - serve
```

For a working reference, see the fixture under
`tests/e2e/python/packages/smoke-blueprint/`.

## Publishing

Blueprint refs can be local files while developing:

```bash
reploy init --blueprint file:./example.blueprint.yaml
```

For users, publish the blueprint inside the app package and give them the
package ref:

```bash
reploy init --blueprint pypi:example-app
```

If the blueprint is not stored in the package's conventional `example_app/reploy`
path, include the explicit path:

```bash
reploy init --blueprint pypi:example-app//custom/path
```
