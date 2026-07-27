---
sidebar_position: 1
---

# App Author

An app blueprint describes an environment: its base image, application-owned
packages, published commands, runtime mounts, workload, and install policy.
Reploy owns resolution, image construction, validation, runtime wiring, and
installation.

## Smallest Blueprint

Only format identity, supported platforms, environment identity, and a base
image are required:

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64, linux/arm64]

environment:
  id: example-app
  base:
    image: python:3.11-slim
```

This base-only form can be staged, built, inspected, installed, and opened with
`reploy shell`. Add applications and commands only when the environment needs
them.

## Minimal Runnable Python App

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64, linux/arm64]

environment:
  id: example-app
  base:
    image: python:3.11-slim
  applications:
    application:
      packages:
        python:
          requirements: [example-suite]
      executables:
        server:
          source: python
          binary: example-server
  commands:
    serve:
      executable: application.server
      argv: [serve]
  workload:
    command: serve
```

The base image may already provide Python, or the application may request it
through `packages.os`. Before creating the Python application environment,
Reploy verifies the selected interpreter and determines its version.
Executables stay with the application that provides them; commands reference
them as `application.executable`.

Add only the nodes the app needs. Common additions are:

- `environment.mounts` plus `docker.mounts` for persistent runtime data;
- `environment.allow_concurrent` for app-command and shell overlap policy;
- `environment.install` for target, system account, and success output;
- `environment.workload.endpoints` and `docker.workload.endpoints` for published
  services and readiness checks.

## Local Development Overrides

Local development choices do not belong in a published blueprint. Stage the
blueprint, then open the override editor:

```bash
reploy stage ./example.blueprint.yaml
reploy overrides
```

Use `--dir DIR` only to select a staging directory explicitly:

```bash
reploy overrides --dir ./reploy-staging
```

The editor loads or creates `overrides.yaml` beside that staged deployment.
The base image can remain **From blueprint** or use an exact image name such
as `ubuntu:24.04`. Exact names are resolved and locked during validation or
build.

Package overrides can select an exact upstream version or a local project.
The project browser starts in the directory where the editor was launched.
The workspace root is unset by default, so selected paths remain absolute.
Set an optional workspace root inside the editor to store paths beneath it
using `{{ workspace_root }}`; paths outside it remain absolute. The root may
be an absolute path or a home-relative path such as `~/src`. Reploy retains
the spelling in the sidecar and expands `~` when it uses the override:

```yaml
environment:
  id: example
  base:
    image: python:3.13-slim
  vars:
    workspace_root: ~/src
  package_overrides:
    python:
      omegaconf:
        path: "{{ workspace_root }}/OmegaConf"
      hydra-core:
        path: "{{ workspace_root }}/hydra"
```

An override is used only if the blueprint or a dependency actually requires
that package. Installation consumes the artifacts built during staging and
does not retain the sidecar or external source paths.

The editor shades explicit package dependencies and lists them before
override-only mappings. It discovers direct dependencies from static Python
project metadata and from a successful trial build. You may add an
override-only row for another transitive package, but that row is used only
when dependency resolution requires the package. Use the blueprint or
`reploy bundle add-package` when you intend to add a direct requirement.

Press `V` to save the choices and validate them with the normal build pipeline.
Success verifies that versions exist, constraints are compatible, local
sources build, and the final environment passes validation. Reploy keeps the
trial result for the next `reploy build` without replacing the current staged
image. The editor shows progress and a scrollable log; after a failure, press
`S` to save the log to a new file. Exiting unvalidated choices offers to
validate and exit, save without validation, or return to editing.

## Validate and Publish

Check syntax and semantics without staging, resolving packages, or building an
image:

```bash
reploy validate ./example.blueprint.yaml
```

For local end-to-end development:

```bash
reploy stage ./example.blueprint.yaml
reploy build
reploy up
reploy test
```

Publish the blueprint in a package or Git repository and give users its indexed
shorthand or explicit provider ref:

```bash
reploy install example-app --scope user
reploy install pypi://example-app/example_app/reploy/example.blueprint.yaml --scope user
```

For a complete working example, see
`examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml` in the
Reploy repository.
