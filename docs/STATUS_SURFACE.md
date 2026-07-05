---
status: Draft
updated: 2026-07-06
summary: Mini design for blueprint-driven app status output.
---

# Status Surface

Reploy's app control script should show app-facing status, not raw runtime
tables. Docker, systemd, launchd, or Windows Service details are implementation
details unless the operator explicitly asks for runtime/debug output.

The default `status` command should therefore be blueprint-driven:

```yaml
status: |
  State: {{ reploy.state }}
  URL: {{ reploy.url }}
  Health: {{ reploy.health }}
  Config: {{ commands.config_check }}
```

This is a presentation template. Reploy owns variable resolution, timeouts,
formatting, color, terminal behavior, and fallback output in one place.

## Goals

- Make `<app>ctl status` useful to app operators without exposing raw Docker
  status by default.
- Let blueprints define concise app-specific status output.
- Keep control scripts as thin wrappers around the embedded Reploy runtime.
- Keep runtime-specific details available through an explicit debug/runtime
  command surface.
- Support future non-service blueprints where container/service status is not
  the main thing the user needs to see.

## Non-Goals

- Do not make the generated control script interpret templates.
- Do not expose every Docker/systemd field as a stable blueprint variable.
- Do not run arbitrary shell snippets from the status template.
- Do not make status a replacement for logs, health checks, or diagnostics.

## Command Shape

The installed control script keeps the normal user-facing command:

```bash
arbiterctl status
```

Internally, the thin wrapper delegates to embedded Reploy. Reploy loads the
installed deployment state, resolves the status template, and renders it.

When no blueprint status template is declared, Reploy should render a small
built-in status summary instead of a raw runtime table.

Runtime detail can remain available through an explicit lower-level command,
for example:

```bash
arbiterctl runtime status
```

The exact debug command name is not part of this design.

## Template Variables

Variables are read-only. Missing optional values render as `unknown` unless the
variable is structurally invalid, in which case Reploy should fail with a clear
template error.

### Core Variables

| Variable | Meaning |
| --- | --- |
| `{{ app.id }}` | Blueprint app id. |
| `{{ app.name }}` | Human-facing app name when known; otherwise the app id. |
| `{{ reploy.phase }}` | Deployment phase, such as `staged` or `deployed`. |
| `{{ reploy.state }}` | App lifecycle state: `running`, `stopped`, `starting`, `unhealthy`, `unknown`, or `unsupported`. |
| `{{ reploy.health }}` | Health summary: `pass`, `fail`, `unknown`, `not_declared`, or `unsupported`. |
| `{{ reploy.url }}` | Primary app URL when Reploy can derive one from installed state or blueprint metadata. |
| `{{ reploy.install_dir }}` | Installed deployment directory. |
| `{{ reploy.control_name }}` | Generated app control command name, such as `arbiterctl`. |

### Runtime Variables

Runtime variables expose normalized Reploy concepts, not backend-native table
columns.

| Variable | Meaning |
| --- | --- |
| `{{ runtime.backend }}` | Runtime backend, such as `docker`, `systemd`, `launchd`, or `windows-service`. |
| `{{ runtime.service }}` | App service name when the deployment has a primary service. |
| `{{ runtime.status }}` | Backend-normalized service status string. |
| `{{ runtime.started_at }}` | Best-effort start time when available. |
| `{{ runtime.ports }}` | Best-effort published port summary when available. |

These variables are deliberately small. Raw backend output belongs in a debug
surface, not in the stable status template contract.

### Command Result Variables

Status templates may reference deployed app commands through Reploy-managed
variables. Command-backed variables must point at commands that are safe in the
installed control surface.

Initial form:

```yaml
status_values:
  commands.config_check:
    command: ["config", "check", "--live"]
  commands.env_check:
    command: ["env", "check"]
```

The template can then use:

```yaml
status: |
  Config: {{ commands.config_check }}
  Env: {{ commands.env_check }}
```

Rules:

- referenced commands must be deployed commands
- status command execution must have a short timeout
- output is captured and trimmed for single-value display
- non-zero exit status renders `fail` plus a concise reason
- verbose output should be hidden unless the user asks for diagnostics

### App-Specific Aliases

Blueprints may define app-specific names over core or command-backed values:

```yaml
status_values:
  arbiter.config.url:
    value: "{{ reploy.url }}"
  arbiter.config.check:
    command: ["config", "check", "--live"]
```

This allows templates like:

```yaml
status: |
  State: {{ reploy.state }}
  URL: {{ arbiter.config.url }}
  Health: {{ reploy.health }}
  Config: {{ arbiter.config.check }}
```

Alias names are blueprint-local. Reploy should validate that aliases do not
shadow built-in namespaces such as `app`, `reploy`, or `runtime`.

## Output Formats

Default output is rendered text.

Future machine-readable output can use the same resolved status model:

```bash
arbiterctl status --format json
arbiterctl status --format yaml
```

For machine output, Reploy should return structured resolved values rather than
the rendered text block.

## Fallback Behavior

If a blueprint does not declare a status template, Reploy should print a compact
default:

```text
State: running
Health: unknown
URL: https://127.0.0.1:8075
```

If a runtime backend cannot provide a value, Reploy should show `unknown` or
`unsupported` rather than leaking backend-specific output.

## Open Questions

- Exact YAML placement: top-level `status` versus `app.status`.
- Exact name for command-backed values: `status_values` versus a more general
  metadata/query section.
- Whether status command results should be cached during one status render.
- Exact debug command name for raw backend status.
