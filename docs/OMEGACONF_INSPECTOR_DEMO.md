---
status: Draft
updated: 2026-07-07
summary: Design for a neutral OmegaConf Inspector demo service blueprint.
---

# OmegaConf Inspector Demo

Reploy needs a neutral demo app that shows what Reploy does without tying the
story to Arbiter or any other domain-specific application. The demo should be a
small but realistic Python web service with dependencies, configuration,
persistent data, ports, health checks, app commands, and an install lifecycle.

The proposed demo is an OmegaConf Inspector: a browser-based workspace for
loading multiple YAML configuration layers, merging them with OmegaConf, and
inspecting the unresolved and resolved results.

## Product Shape

A user opens the web UI and creates or opens a project. A project contains
user-provided config files, merge order, and UI state.

Within a project, the user can:

- paste or type YAML config files
- load YAML files through the browser
- name, reorder, enable, and disable config layers
- merge the active layers with `OmegaConf.merge()`
- compare unresolved and resolved output
- inspect interpolation behavior
- look up a value by path
- see parse, merge, and resolve errors clearly

The app-owned configuration remains separate from user-provided project data.
Reploy-managed `conf/inspector.yaml` configures the service itself. Runtime
project state lives under `/data`.

## Reploy Story

The demo should exercise the Reploy surfaces that matter to an app operator and
an app author:

- stage a blueprint
- prepare a bundle with real Python dependencies
- warm up the runtime environment
- mount service config from `conf`
- persist user projects in writable `data`
- publish a local HTTP port
- run health checks before and after install
- expose useful app commands
- inspect status and logs
- update without losing project data
- uninstall with clear runtime cleanup behavior

The end-user storyline should stay simple:

```text
Install a config-debugging web app.
Open it in the browser.
Paste, type, or load config layers.
See the merged OmegaConf result.
Save projects.
Operate it with Reploy.
```

This demo also creates a useful future video pattern for OmegaFlow-style
walkthroughs: the story naturally spans terminal actions and browser
interaction. A good demo recording should be able to show Reploy commands in
the terminal, then switch to the installed web UI for project creation, config
merge inspection, and troubleshooting. That need is broader than this demo, so
recording support should be treated as a future documentation/video workflow
concern rather than part of the app implementation.

The public Reploy blueprint index should include `omegaconf-inspector-demo` and
point at the canonical GitHub-hosted blueprint. The in-repo demo walkthrough
should stage from the local blueprint path so development and documentation can
be tested before publishing:

```text
reploy stage file:examples/omegaconf-inspector/reploy
```

## Web Stack

Use a real but modest Python web stack:

- FastAPI for routing, request validation, and JSON error behavior
- Uvicorn as the service process
- OmegaConf as the core config merge engine
- SQLite from the Python standard library for project persistence
- packaged static HTML, CSS, and JavaScript for the frontend

The frontend should be packaged inside the Python wheel, not built as a
separate Node artifact. This keeps the app installable from one Python package
while still exercising static package data in the bundle.

Initial frontend scope:

- project picker and project editor
- layer list with add, remove, reorder, enable, and disable controls
- text editor panes based on plain textareas
- file loading via browser `FileReader`
- merge actions through JSON APIs
- read-only output panes with syntax highlighting

Avoid a separate React, Vite, or Monaco/CodeMirror build for v1. Those can be
added later if the demo needs to showcase non-Python asset build support.

## OmegaConf-Aware Output

Rendered config output should use custom OmegaConf-aware syntax highlighting,
not only generic YAML highlighting.

The highlighted read-only output should distinguish:

- YAML keys, values, comments, anchors, and aliases
- interpolation expressions such as `${paths.cache}`
- resolver expressions such as `${oc.env:HOME,/tmp}`
- resolver names and interpolation paths
- missing mandatory values such as `???`
- invalid interpolation regions when an error can be localized

The UI should show at least these output panes:

- merged unresolved YAML
- resolved YAML
- errors

Input panes may remain plain textareas for v1. The value of the demo is in the
OmegaConf merge and inspection behavior, not in building a full editor.

## Persistence Model

Runtime data is organized around projects:

```text
Project
  id
  name
  created_at
  updated_at
  ui_state
  files[]

ProjectFile
  id
  project_id
  name
  content
  order
  enabled
```

The initial storage backend should be SQLite at `/data/projects.sqlite`.

`conf/inspector.yaml` configures the service. User projects and UI state do not
belong in `conf`; they belong in writable runtime data.

## API Shape

Initial JSON API:

```text
GET  /                         web UI
GET  /_health_                 health check
GET  /api/projects             list projects
POST /api/projects             create project
GET  /api/projects/{id}        load project
PUT  /api/projects/{id}        update project metadata and UI state
GET  /api/projects/{id}/files  list project files
POST /api/projects/{id}/files  create or replace a project file
PUT  /api/projects/{id}/files/{file_id}
DELETE /api/projects/{id}/files/{file_id}
POST /api/projects/{id}/merge
```

The health endpoint should verify that service config loaded successfully and
that the SQLite data store is reachable and writable. It should fail when the
`conf` or `data` contract is broken, not only when the HTTP process is alive.

The merge request should use the active stored layer order, optional enabled
file ids, and an optional path lookup. When a path is supplied, the output panes
show the selected OmegaConf value directly.

The merge response should include:

- success or failure
- merged unresolved YAML
- resolved YAML when possible
- clear merge errors

## Package Layout

The demo should live as a first-class example, not under the e2e fixtures:

```text
examples/omegaconf-inspector/
  README.md
  pyproject.toml
  src/omegaconf_inspector/
    __init__.py
    __main__.py
    cli.py
    config.py
    merge.py
    server.py
    storage.py
    static/
      index.html
      app.js
      styles.css
      omegaconf-highlight.js
  reploy/
    omegaconf-inspector.blueprint.yaml
```

The `pyproject.toml` should include package data for
`omegaconf_inspector/static/*` so the frontend is present in the wheel and in a
prepared Reploy bundle.

The local blueprint should use `app.provider.local_sources` to point from the
blueprint directory back to the package root. This is the same relative-source
pattern used by Arbiter's in-repo blueprint. For the proposed layout, the local
source entry should resolve the `omegaconf-inspector` package from `..`:

```yaml
app:
  provider:
    type: python
    identifier: omegaconf-inspector
    local_sources:
      omegaconf-inspector: ..
```

The GitHub-backed blueprint index entry should point at the same blueprint
inside the Reploy repository. When Reploy stages that GitHub ref, the relative
local source should resolve within the checked-out repository just as it does
for local development.

## Service Configuration

Example app-owned config:

```yaml
server:
  host: 0.0.0.0
  port: 8076
  title: OmegaConf Inspector

storage:
  database: /data/projects.sqlite
```

This config is part of the installed app contract. It is different from the
YAML files users upload or type into projects.

The app should provide a small config bootstrap flow. `config init` writes a
template `conf/inspector.yaml` when it is missing, and should refuse to
overwrite an existing config unless the user explicitly asks for that behavior.
The user can edit the generated template before running `config check`,
starting the service, or installing the app.

## App Commands

Initial app commands:

```text
serve
config init
config check
config show
project list
project show <id>
version
```

Installed command exposure should be conservative:

- expose `config check`, `config show`, project list/show, and version
- use `serve` as the runtime command, not as an operator command
- keep `config init` as a staging/bootstrap command unless a clear installed
  use case appears

## Blueprint Shape

The blueprint should demonstrate:

- Python package root for the demo service
- preserved `conf` and `data` managed paths
- read-only runtime config mount
- writable runtime data mount
- HTTP port publication
- health check against `/_health_`
- install hooks that check config before start and health after start
- success output with the service URL
- app commands for service config and persisted project inspection
- localhost-only host port binding, because users may paste real configs or
  secrets into the inspector

Managed paths should express the config/data split explicitly:

```yaml
install:
  managed_paths:
    dirs:
      - path: conf
        update: preserve
        mount: /conf
      - path: data
        update: preserve
        mount: /data
        writeable: true
```

The container can listen on `0.0.0.0`, but the host binding should be
`127.0.0.1` for both staging and deployed ports. This demo is intentionally a
local operator tool, not a network service.

## Acceptance Checks

The first implementation should be considered good enough when:

- a user can stage and bundle the demo from `examples/omegaconf-inspector`
- the Reploy blueprint index can expose `omegaconf-inspector-demo` through a
  GitHub-backed blueprint reference
- the bundle includes FastAPI, Uvicorn, OmegaConf, and packaged static assets
- the service starts under Reploy and passes `/_health_`
- `config init` creates an editable service config template, and
  `config check` validates it before service start
- the UI can create a project, edit config layers, and merge them
- `data` survives update/reinstall flows that preserve managed paths
- `conf` remains app-owned service config, not user project input
- the installed control surface can show config and project state
- docs can use the demo to explain Reploy without referencing Arbiter

## Non-Goals For V1

- Do not build a general-purpose hosted config management product.
- Do not add authentication or multi-user authorization.
- Do not add a separate frontend build pipeline.
- Do not depend on a CDN at runtime.
- Do not make Hydra part of v1.
- Do not make uploaded project configs part of Reploy-managed `conf`.

## Open Questions

- Whether syntax highlighting should later use a tiny custom highlighter or a
  vendored library such as Prism with an OmegaConf grammar.
- Whether this demo should later become an optional e2e smoke target.
