---
sidebar_position: 1
---

# App Author

An app blueprint describes an environment: its base image, software
components, published commands, runtime mounts, workload, and install policy.
Reploy owns resolution, image construction, validation, runtime wiring, and
installation.

## Minimal Blueprint

Start with the smallest environment that expresses the app:

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64, linux/arm64]

environment:
  id: example-app
  components:
    base:
      image: python:3.11-slim
    application:
      type: python
      requirements: [example-suite]
      executables:
        server:
          binary: example-server
  commands:
    serve:
      executable: application.server
      argv: [serve]
  workload:
    command: serve

docker:
  workload: {}
```

The base image may already provide Python, or an earlier APT component may
provide it. Before creating the Python component environment, Reploy verifies
that the selected interpreter is Python and determines its version. Component
executables stay with the component that provides them; commands reference them
as `component.profile`.

Add only the nodes the app needs. Common additions are:

- `environment.workspace` for explicitly named local development package
  checkouts;
- `environment.mounts` plus `docker.mounts` for persistent runtime data;
- `environment.allow_concurrent` for app-command and shell overlap policy;
- `environment.install` for target, system account, and success output;
- `environment.workload.endpoints` and `docker.workload.endpoints` for published
  services and readiness checks.

## Local Workspace Overrides

Local checkouts can supersede published Python distributions during staging:

```yaml
environment:
  workspace:
    root: ..
    packages:
      python:
        omegaconf: OmegaConf
        hydra-core: hydra
```

`root` may be relative to the blueprint. Package paths are relative to `root`
and must stay inside it. A caller may override the root while staging:

```bash
reploy stage ./example.blueprint.yaml --workspace-root /home/me/src/project
```

Reploy records the effective root in staging state. Installation consumes the
artifacts built during staging and does not retain an external workspace path.

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
