---
status: Active
updated: 2026-07-06
summary: Current OS and container-runtime substrate for user, system, and dedicated-user install scope shapes.
---

# Install Scope Runtime Support

This document maps what Linux, macOS, and Windows can support at the OS and
container-runtime layer for container-backed permanent installs. It is about
host and runtime capability, not about Reploy CLI implementation status.

This is a snapshot as of 2026-07-06, based on current Docker and Podman
documentation and scoped to Linux-container workloads.

## Scope Model

| Scope shape | Meaning |
| --- | --- |
| `current-user` | Runtime state, container storage, and lifecycle belong to the invoking user. No root/admin setup should be required after prerequisites are installed. |
| `system` | Runtime lifecycle is owned by the OS service manager or a privileged runtime. It can start at boot and be managed by root/admin. |
| `dedicated app user` | Root/admin creates or selects a non-root account such as `arbiter`; runtime state and app files belong to that account, isolating fallout from the invoking user's files. |

`dedicated app user` is not the same as passing `--user` to a container. The
important boundary is who owns the container runtime state, volumes, generated
service units, and bind-mounted files on the host.

`install owner` is a narrower Reploy concept. In the current Linux
Docker/systemd path, Reploy can create or resolve an app owner such as
`arbiter`, chown the installed deployment files, and run the app process inside
the container as that UID/GID. That does not make the Docker daemon, systemd
unit, Compose project lifecycle, or host runtime authority belong to the
`arbiter` account.

## Summary Matrix

| Host OS | Runtime substrate | `current-user` | `system` | `dedicated app user` | Notes |
| --- | --- | --- | --- | --- | --- |
| Linux | Docker Engine, rootful daemon | Partial | Yes | Partial | Strong system lifecycle through root/systemd and rootful Docker. Containers can run as a non-root UID/GID, but the Docker daemon and storage remain privileged/system-owned. |
| Linux | Docker rootless mode | Yes | No | Plausible | Docker supports a rootless daemon and containers as a non-root user. A dedicated app-user variant would require privileged setup of the app account, subuid/subgid ranges, and linger, then running rootless Docker as that user. |
| Linux | Podman rootful with systemd/Quadlet | Partial | Yes | Partial | Good system substrate. Can set container user IDs, but the runtime is still rootful unless the unit is installed in a rootless user path. |
| Linux | Podman rootless with user systemd/Quadlet | Yes | No | Yes | Best candidate for a dedicated non-root app-user backend. Admin can create `arbiter`, place Quadlet files under the user's rootless unit path, and enable user-session persistence. |
| macOS | Docker Desktop | Yes | No | No | Docker Desktop is a user-session/VM-backed runtime. It can need privileged setup for helper configuration, but app containers are not native macOS services or per-app macOS users. |
| macOS | Podman Machine | Yes | No | Not as a host guarantee | Containers run on the Mac inside a Podman-managed Linux VM. Per-app users inside the VM are possible, but they are not native macOS service-user isolation. |
| Windows | Docker Desktop per-user install | Yes | No | No | Per-user Docker Desktop can install/update without admin and uses WSL 2 for Linux containers. It is not a per-app Windows service-user model. |
| Windows | Docker Desktop all-users install | Partial | No | No | All-users install requires admin and can use WSL 2 or Hyper-V. It has broader host privileges but still does not provide a clean per-app user install scope for Linux containers. |
| Windows | Podman Machine | Yes | No | Not as a host guarantee | Containers run on the Windows host inside a Podman-managed Linux VM. Per-app users inside the VM do not provide native Windows service-user isolation. |

## Linux

Linux has the richest install-scope substrate.

Rootful Docker plus systemd supports a real `system` install: root/systemd owns
the service lifecycle, and Docker owns container resources through the
privileged daemon. The app process can still run with a non-root UID/GID inside
the container and write app files owned by an app service account. That is
useful fallout isolation, but the runtime authority remains rootful Docker.

This is Reploy's current leakage point. The current Linux install path should
be described as `system` scope with a non-root `install owner` and non-root
container process. It should not be described as a true `dedicated app user`
runtime, because the rootful Docker daemon and root-owned systemd unit still
own the lifecycle.

Docker rootless mode supports a real `current-user` runtime: Docker documents
running both the daemon and containers as a non-root user. It depends on host
prerequisites such as `newuidmap`, `newgidmap`, and subordinate UID/GID ranges
for the user. The rootless Docker setup creates a user systemd service and uses
`loginctl enable-linger USER` for startup without login.

Podman rootless with Quadlet is the cleanest `dedicated app user` candidate.
The model is:

```text
root/admin creates arbiter user
root/admin ensures subuid/subgid and rootless runtime prerequisites
root/admin installs Quadlet under /etc/containers/systemd/users/<uid>/
arbiter owns Podman storage, volumes, and app files
arbiter's user systemd instance runs the container
```

Podman's Quadlet docs are explicit that rootless Quadlet units do not become
another user's unit through systemd `User=`, `Group=`, or `DynamicUser=`.
Instead, the app user must exist and the Quadlet file must live in that user's
rootless unit search path.

## macOS

macOS does not currently provide the same native install-scope substrate for
Linux containers.

Docker Desktop is a user-facing app backed by a Linux-container VM. It can be
installed and operated for a user, and Docker documents privileged helper setup
options, but this does not create a native macOS service-user boundary for each
installed app. It is best modeled as `current-user` Docker-managed persistence,
with reboot resistance depending on Docker Desktop starting at login.

Podman Machine is similar from an install-scope perspective. It can provide a
local user-owned Linux VM on the Mac. Containers run on the Mac inside that VM,
not directly as macOS launchd services or macOS service users. A VM-internal
app user may still be useful, but it is not the same host guarantee as a Linux
dedicated app user.

## Windows

Windows support is also VM-backed for Linux containers.

Docker Desktop now has two Windows install modes that matter to this matrix:
per-user and all-users. Per-user installation does not require administrator
privileges and uses WSL 2 for Linux containers. All-users installation requires
administrator privileges and can use WSL 2 or Hyper-V; it also enables Windows
container support, but that is outside Reploy's Linux-container runtime model.

Neither Docker Desktop mode provides a native per-app Windows service-user
install scope for Linux containers. The practical model is `current-user`
Docker-managed persistence, with Docker Desktop as an external user-session or
machine-level dependency depending on installation mode.

Podman Machine on Windows follows the same broad shape as macOS: a local
Linux VM can host containers, but VM-internal users are not native Windows
service-user isolation.

## Reploy Implications

- Linux `system` scope can continue to use root/systemd plus Docker.
- Current Linux Docker/systemd installs provide non-root install ownership and
  container UID/GID selection, not a dedicated app-user runtime.
- Linux `current-user` scope can be implemented with Docker-managed Compose
  under the invoking user. This avoids root and systemd, but it does not
  isolate from the invoking user's files and depends on the user's Docker
  runtime/session for persistence.
- Linux `dedicated app user` scope is most promising with rootless Podman plus
  user systemd/Quadlet. It requires privileged setup, then unprivileged runtime
  under the app account.
- macOS and Windows Docker Desktop should be treated as `current-user`
  Docker-managed installs, not native system installs.
- macOS and Windows Podman Machine may improve runtime uniformity, but their
  app-user isolation is VM-backed, not native host service-user isolation.

## Source Notes

Checked on 2026-07-06:

- Docker rootless mode: <https://docs.docker.com/engine/security/rootless/>
- Docker Desktop on macOS install and privileged setup: <https://docs.docker.com/desktop/setup/install/mac-install/>
- Docker Desktop on Windows install modes: <https://docs.docker.com/desktop/setup/install/windows-install/>
- Podman Quadlet rootless unit paths and user handling: <https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html>
