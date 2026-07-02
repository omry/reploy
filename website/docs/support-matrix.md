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
| Kubernetes | Not yet supported | Out of scope for the current release line. |

## Host Operating System Support

The host OS determines which binaries are published and which permanent service
manager Reploy can use.

| Host | CLI binary | Staging Docker lifecycle | Permanent install/uninstall |
| --- | --- | --- | --- |
| Linux | Supported | Supported | Supported with systemd |
| macOS | Release artifacts for `darwin-arm64` and `darwin-amd64` | Docker Desktop support is being validated | Initial Docker Desktop-backed development persistence implementation; not a production system install |
| Windows | Deferred | Planned | Deferred; Windows service support is undecided |

## Current Supported Path

The production permanent-install path is:

```text
Python app backend + Docker runtime + Linux host with systemd
```

macOS support is being validated as a development and staging host with Docker
Desktop. Docker Desktop-backed macOS installs use Docker Compose restart policy
and depend on Docker Desktop being configured by the user to start at login for
reboot resistance. They do not provide the same service-user isolation as
Linux/systemd installs.

Formal Windows behavior is tracked in the Reploy backlog.
