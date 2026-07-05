---
status: Draft
updated: 2026-07-05
summary: Mini design for platform-aware install target defaults and semantic install-root variables.
---

# Cross-Platform Install Locations

Reploy is a cross-platform app installer, not only a wrapper around one host
package manager. A blueprint should describe app install intent, while Reploy
maps that intent to host-appropriate paths and lifecycle behavior.

The immediate design problem is `install.target.default_path`. A path such as
`/opt/{{ app.id }}` is a Linux system-install default, not a portable app
install concept. Windows and macOS Docker-managed installs need host-native
defaults, and app authors may still need to override those defaults globally or
per OS.

## Goals

- Let Reploy hard-code sensible default install roots for each supported OS.
- Let blueprints override the install target globally or for a specific OS.
- Let blueprints reference semantic host locations for choosing the single app
  install directory without hard-coding platform paths.
- Validate the active host path strictly while allowing inactive per-OS paths
  to remain platform-specific.
- Keep installed app state localized under one install directory for now.
- Keep this design ready to document later in the blueprint authoring docs.

## Non-Goals

- Do not treat `/opt/...` as a portable path.
- Do not require every blueprint to spell out Linux, macOS, and Windows paths.
- Do not expose Docker Desktop as a user-facing install target.
- Do not decide future Windows Service or macOS launchd semantics here.
- Do not add separate host-global config, data, cache, or state placement for
  installed apps in this design. Those remain inside the install target.

## Built-In Defaults

If a blueprint does not provide an install target default, Reploy chooses the
default for the current host and install backend.

Initial defaults:

| Host/backend | Default install root |
| --- | --- |
| Linux systemd install | `/opt/{{ app.id }}` |
| macOS Docker-managed install | `{{ user.data }}/Reploy/installs/{{ app.id }}` |
| Windows Docker-managed install | `{{ user.local_data }}/Reploy/installs/{{ app.id }}` |

The concrete resolved examples are:

| Host | Resolved example |
| --- | --- |
| Linux | `/opt/arbiter` |
| macOS | `~/Library/Application Support/Reploy/installs/arbiter` |
| Windows | `%LOCALAPPDATA%\Reploy\installs\arbiter` |

## Blueprint Shape

The shortest portable form is to omit the target default:

```yaml
install:
  target: {}
```

Blueprints may provide one global default:

```yaml
install:
  target:
    default_path: "{{ reploy.install_root }}/{{ app.id }}"
```

Blueprints may provide per-OS defaults:

```yaml
install:
  target:
    default_paths:
      linux: /opt/{{ app.id }}
      macos: "{{ user.data }}/Acme/{{ app.id }}"
      windows: "{{ user.local_data }}/Acme/{{ app.id }}"
```

Blueprints may combine both. The per-OS default wins on matching hosts:

```yaml
install:
  target:
    default_path: "{{ user.data }}/Acme/{{ app.id }}"
    default_paths:
      linux: /opt/{{ app.id }}
      windows: "{{ user.local_data }}/Acme/{{ app.id }}"
```

## Precedence

Install target resolution should use:

1. CLI `--to`
2. `install.target.default_paths.<host_os>`
3. `install.target.default_path`
4. Reploy built-in default for the host and install backend

`default_paths` keys should use product-facing OS names:

- `linux`
- `macos`
- `windows`

## Semantic Install-Root Variables

Reploy should support a small set of semantic variables for computing the
single install target. These variables are resolved for the current host before
validation and rendering.

Core variables:

| Variable | Meaning |
| --- | --- |
| `{{ app.id }}` | Blueprint app id |
| `{{ user.home }}` | Current user's home directory |
| `{{ user.data }}` | Per-user application data root suitable for durable app installs |
| `{{ user.local_data }}` | Per-user local data root suitable for machine-local app installs |
| `{{ system.data }}` | System-wide application data root |
| `{{ reploy.install_root }}` | Reploy's default install root for this host/backend |

Initial mappings:

| Variable | Linux | macOS | Windows |
| --- | --- | --- | --- |
| `user.data` | `~/.local/share` | `~/Library/Application Support` | `%APPDATA%` |
| `user.local_data` | `~/.local/share` | `~/Library/Application Support` | `%LOCALAPPDATA%` |
| `system.data` | `/var/lib` | `/Library/Application Support` | `%ProgramData%` |

These variables choose where the app install directory lives. They do not mean
that Reploy will place app config, data, cache, or runtime files directly in
those host roots. For now, installed apps remain localized under the resolved
install target, and managed paths such as `conf`, `data`, and bundle/runtime
state stay inside that tree.

## Validation

- `default_path` and the active `default_paths.<host_os>` must resolve to an
  absolute path for the current host.
- Inactive `default_paths` entries should be syntax-checked for known variables
  and template safety, but should not be rejected because they use another
  platform's absolute-path syntax.
- Unknown OS keys should fail clearly.
- Unknown template variables should fail clearly.
- Newlines, tabs, and unsafe path traversal in app-derived path components
  should fail clearly.
- On Windows, Reploy may accept forward slashes in blueprint path templates and
  render native separators after resolution.

## UX Consequences

With this design, Windows staging should not fail merely because a blueprint
contains a Linux-specific `default_paths.linux`. It should fail only when the
active Windows target cannot be resolved or validated.

If a legacy blueprint has only:

```yaml
install:
  target:
    default_path: /opt/{{ app.id }}
```

Windows should treat that as the global active default and reject it with a
clear author-facing error. The fix is to omit the default and rely on Reploy's
built-in host default, or move the Linux path under `default_paths.linux`.

## Documentation Follow-Up

When implemented, the blueprint docs should cover:

- built-in install target defaults
- target resolution precedence
- `default_path` versus `default_paths`
- semantic install-root variables
- examples for portable defaults and app/vendor overrides
- the difference between staging directories and permanent install targets
- the current constraint that installed app state stays localized under the
  install target
