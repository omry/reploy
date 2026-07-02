# macOS Port Plan

This document defines the work needed to make macOS an explicit Reploy support
target.

The product stance is:

- Linux is the production permanent-install target.
- macOS is a first-class development and staging host.
- macOS staging and runtime commands use Docker Desktop.
- macOS can support a Docker Desktop-backed persistent install for development
  use.
- macOS system permanent install is deferred. If it is explored later, it
  should be treated as a separate launchd design, not part of the first macOS
  support milestone.

This keeps Reploy's operator story honest. A macOS laptop is a good place to
stage, configure, test, and iterate on an app. A Linux host remains the place
where Reploy should promise server-like permanent install semantics.

## Goals

- Build and distribute `reploy` binaries for both macOS architectures:
  Apple Silicon (`darwin-arm64`) and Intel (`darwin-amd64`).
- Support the staging workflow on macOS with Docker Desktop:
  `stage`, `stage --update`, `info`, `bundle`, `app`, runtime commands, and
  `test`.
- Support persistent install/uninstall for macOS development hosts using the
  normal Reploy install command surface.
- Make macOS install reboot resistant when Docker Desktop is configured to
  start at login.
- Make Linux-only system install guarantees clearly out of scope for macOS.
- Warn during macOS install when the detected Docker runtime is Docker Desktop,
  because the security offered on macOS and Windows is weaker than Linux.
- Document which commands require Docker Desktop and which commands remain
  Linux-only.
- Keep future launchd work visible without making it part of the initial macOS
  support contract.

## Non-Goals

- Do not support macOS system permanent install in the first macOS milestone.
- Do not make `~/Library/LaunchAgents` an alternative meaning of permanent
  install. User-level execution is staging.
- Do not require a system-managed Docker VM or a native macOS Docker Engine.
- Do not promise production server behavior on macOS or Windows.
- Do not move generated state into `~/Library/Application Support` or another
  user-global install root for the first macOS milestone.
- Do not expose Docker Desktop as a user-facing install target or backend.
- Do not claim that Docker Desktop persistence provides the same isolation or
  lifecycle guarantees as Linux/systemd install.

## Assumptions

- Docker remains the first runtime backend. On macOS this means Docker Desktop
  and Linux containers.
- Docker Desktop is an appropriate dependency for macOS workflows.
- macOS install means persistent Docker/Compose resources with restart
  policies. Docker Desktop itself remains an external user-session dependency.
- Reboot resistance depends on Docker Desktop being configured to start when
  the user signs in.
- Reploy state and generated artifacts remain project-local. macOS support
  should not introduce a hidden user-level install root.
- Permanent production install remains Linux/systemd-backed.
- macOS support must be tested on real macOS with Docker Desktop; unit tests
  alone are not enough.

## Support Matrix

The detailed design should publish a command-level support matrix.

Expected first milestone:

| Command group | macOS support | Notes |
| --- | --- | --- |
| `stage`, `stage --update` | supported | CLI-only except provider fetch/build behavior. |
| `info` | supported | Should inspect staging state normally. |
| `bundle` | supported | Requires Docker Desktop for build/check. |
| `app` | supported | Requires Docker Desktop and a prepared bundle/runtime. |
| `up`, `restart`, `down`, `ps`, `status`, `logs` | supported | Docker Desktop-backed staging runtime. |
| `test` | supported | Docker Desktop-backed staging runtime. |
| `doctor` | supported | Should distinguish staging readiness from Linux install readiness. |
| `install`, `uninstall` | planned | Persistent development install using Docker/Compose restart policy; warn that macOS/Windows Docker-runtime security is weaker than Linux. |
| launchd/system install | unsupported initially | Future design topic, not exposed as the first macOS install mode. |

Unsupported operations should fail before doing partial work.

## Major Work Areas

### 1. Release Artifacts

Add Darwin targets to build and release workflows:

- `darwin-arm64`
- `darwin-amd64`

The install script and release docs should understand macOS archive names,
checksums, and platform detection. Codesigning, notarization, and Homebrew can
be separate follow-up decisions, but the plan should record whether they are
required before claiming official support.

### 2. Platform Detection

Add a small platform capability layer instead of scattering `runtime.GOOS`
checks. It should answer questions such as:

- What host OS is this?
- Is this command supported on this platform?
- Does this command require Docker Desktop?
- Should this command use staging semantics or permanent-install semantics?
- What message should unsupported install operations show?

This layer should keep staging and Docker Compose code portable while making
Linux/systemd install behavior explicit.

### 3. Docker Desktop Compatibility

Validate Docker Desktop behavior separately from Linux Docker Engine behavior:

- daemon responsiveness and timeout handling
- Docker context and socket discovery
- bind mounts from staging directories
- UID/GID and file ownership behavior inside mounted volumes
- port binding on `127.0.0.1` and `0.0.0.0`
- Compose project naming
- app command execution through generated compose files
- behavior when Docker Desktop is installed but not running

User-facing requirements should stay simple:

> macOS staging and persistent development install require Docker Desktop.

The detailed docs can explain that Docker Desktop must be running and reachable
from the user's shell.

### 4. Persistent Development Install

Define macOS install behavior that persists app containers through Docker
Desktop instead of through launchd or systemd.

Open design points:

- command shape: use the normal `install` and `uninstall` commands; do not
  expose Docker Desktop as a user-facing target or backend
- install-time warning: tell users that macOS and Windows installs have weaker
  security than Linux installs because the Docker runtime model does not provide
  the same service-user isolation. If Docker Desktop is confidently detected,
  mention it by name; otherwise do not skip the warning.
- generated Compose restart policy, likely `unless-stopped`
- reboot resistance: validate Docker Desktop login startup when possible; when
  it cannot be validated, document manual validation and remediation steps
- installed state and generated artifacts remain under the project-local
  Reploy directory
- how `uninstall` removes containers, networks, volumes, generated files, and
  installed metadata
- how control scripts and `reploy status/logs/down/restart` distinguish staged
  runtime from installed Docker Desktop runtime
- what `doctor` should require before allowing install
- what message to show when Docker Desktop is installed but not configured to
  start when the user signs in, or when that setting cannot be detected

This is a development-host persistence feature, not a production service
manager. It uses Docker Desktop as the macOS runtime, but that should be
communicated as a requirement and security caveat rather than as a separate
install target. It depends on Docker Desktop being installed, running, reachable
from the user's shell, and configured by the user for login startup if reboot
persistence is desired.

Security boundary:

- container processes are isolated inside Docker Desktop's Linux VM
- Docker access from the same macOS user remains powerful over Reploy-managed
  containers and images
- this does not provide the same protection from same-user runaway agents that
  a dedicated service user on a Linux host can provide

### 5. Filesystem and Path Audit

Audit Linux assumptions that affect macOS staging:

- absolute path handling
- symlinks
- executable bits
- `/tmp` and `$TMPDIR`
- user and group IDs in Docker bind mounts
- generated shell scripts
- assumptions about `/usr/local/bin` and `$PATH`
- default target path references in docs and dry-run output

### 6. Doctor Checks

Add macOS-specific doctor checks for development/staging:

- supported macOS version, if a minimum is needed
- Docker Desktop installed and daemon responsive
- Docker CLI and Compose available
- bind mounts work from a temporary staging directory
- expected ports are available
- control script dependencies, such as `sh` and `curl`

Doctor output should distinguish:

- "this host can stage and run apps with Docker Desktop"
- "this host can create a persistent development install"
- "Docker Desktop login startup is enabled", "manual validation is needed", or
  "enable Docker Desktop start-at-login for reboot resistance"
- "system permanent install is Linux-only in this release"

### 7. Tests and Smoke Validation

Add validation layers:

- cross-compile checks for `darwin-arm64` and `darwin-amd64`
- macOS CI tests for non-Docker command behavior
- documented manual Docker Desktop smoke tests for staging/runtime
- real macOS smoke covering stage, bundle build/check, app command, up, status,
  logs, test, and down
- real macOS smoke covering Docker Desktop-backed install, reboot/login
  expectation documentation, restart, status, logs, and uninstall

The macOS smoke should verify that generated artifacts clean up normally and
that failed Docker Desktop checks fail quickly with useful messages.

## Suggested Milestones

1. **Define support matrix and platform capability layer.**
   Add explicit macOS command support decisions and clear unsupported-command
   errors.

2. **Add macOS release artifacts.**
   Build Darwin binaries and update install/release docs.

3. **Validate macOS staging without Docker.**
   Run CLI-only staging, update, and info checks on macOS.

4. **Validate Docker Desktop staging runtime.**
   Run bundle, app, runtime, and test smoke checks with Docker Desktop.

5. **Validate persistent development install.**
   Smoke-test normal install, restart/status/logs, and uninstall against Docker
   Desktop, including the Docker Desktop security warning and reboot-resistance
   validation path.

6. **Publish macOS docs.**
   Document requirements, support matrix, known limitations, and
   troubleshooting for Docker Desktop.

## Future: System launchd Permanent Install

macOS system permanent install is intentionally outside the first support
milestone. If it becomes a goal later, the likely direction is:

- root-managed system daemon under `/Library/LaunchDaemons`
- app process running as a dedicated non-root service user
- explicit service backend abstraction beside Linux/systemd
- backend-aware installed control scripts
- Docker Desktop or another Docker endpoint usable from the service context

That future design has a hard product question: Docker Desktop is normally a
user-session development tool, while permanent install wants machine-level
service semantics. If Reploy later supports launchd install, it must either
prove a machine-usable Docker Desktop configuration or define a different
runtime requirement. Until then, macOS user-level workflows are staging and
persistent development install.

## Key Risks

- Docker Desktop behavior differs from Linux Docker Engine behavior.
- File ownership and bind mounts can differ from Linux enough to affect bundle
  builds and runtime writes.
- Users may expect `install` to work on macOS once staging works; docs and
  install output need to distinguish development-host persistence from Linux
  system install guarantees.
- Same-user Docker access limits the security value of Docker Desktop-backed
  install for untrusted local agents.
- Release artifacts may imply broader support than the support matrix actually
  promises.
