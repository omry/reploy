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

The editor loads or creates `package-overrides.yaml` beside that staged
deployment. It can select an exact upstream version or a local project for a
package. The project browser starts in the directory where the editor was
launched. The workspace root is unset by default, so selected paths remain
absolute. Set an optional workspace root inside the editor to store paths
beneath it using `{{ workspace_root }}`; paths outside it remain absolute:

```yaml
environment:
  id: example
  vars:
    workspace_root: /home/me/src
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

The editor shades explicit component dependencies and lists them before
override-only mappings. In v1, a package available only from a local project
must be an explicit component requirement if it would otherwise appear only
transitively; use the blueprint or `reploy bundle add-package` to add that
discovery root.

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
