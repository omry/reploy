---
sidebar_position: 8
---

# Support

Reploy support has several separate dimensions. A platform can support one
dimension without supporting all of them.

## App Backend Support

The backend is how Reploy prepares app-provided bundle artifacts.

| Backend | Status | Notes |
| --- | --- | --- |
| Python | Supported | Builds and installs Python wheel bundles, including optional package roots. |
| Other backends | Not yet supported | Future backends should plug into the same bundle lifecycle. |

## Runtime Support

The runtime is where the deployed app runs.

| Runtime | Status | Notes |
| --- | --- | --- |
| Docker | Supported | Reploy generates Docker Compose state and controls the app container lifecycle. |
| Native process | Not yet supported | No non-Docker runtime backend exists yet. |
| Kubernetes | Not supported | Out of scope for the current release line. |

## Host Operating System Support

The host OS determines which binaries are published, how Docker is reached, and
which permanent-install semantics Reploy can promise.

| Host | CLI binary | Staging Docker lifecycle | Permanent install/uninstall |
| --- | --- | --- | --- |
| Linux | Supported | Supported | Supported as user-scope Docker-managed install or system-scope systemd install |
| Windows | Supported with `windows-amd64` and `windows-arm64` release artifacts | Supported with Docker Desktop and Linux containers | Supported as a Docker-managed install; not a Windows Service install |
| macOS | Supported with `darwin-amd64` and `darwin-arm64` release artifacts | Supported with Docker Desktop | Supported as a Docker-managed install; not a launchd or Linux/systemd OS service install |

WSL follows the Linux support path: use the Linux Reploy binary inside WSL with
Linux paths and Linux-style control scripts. Native Windows support means
`reploy.exe` from PowerShell or `cmd.exe`, Docker Desktop, and PowerShell-native
installed control scripts.

## Current Supported Path

The strongest production permanent-install path is:

```text
Python app backend + Docker runtime + Linux host with systemd
```

Linux user-scope installs, macOS installs, and Windows installs are
Docker-managed permanent installs. They use Docker Compose restart policy and
depend on the user's Docker runtime or Docker Desktop being started for reboot
resistance. They do not provide the same service-user isolation as
Linux/systemd OS service installs.

The supported ways to install the Reploy command itself are the release install
scripts and the platform-specific PyPI package. Package-manager formulas such
as Homebrew, Chocolatey, apt, and yum are not supported yet.
