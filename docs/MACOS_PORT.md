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
| `install`, `uninstall` | supported for persistent development install | Uses Docker/Compose restart policy; warns that macOS/Windows Docker-runtime security is weaker than Linux. |
| launchd/system install | unsupported initially | Future design topic, not exposed as the first macOS install mode. |

Unsupported operations should fail before doing partial work.

## Major Work Areas

### 1. Release Artifacts

Add Darwin targets to build and release workflows:

- `darwin-arm64`
- `darwin-amd64`

The install script and release docs should understand macOS archive names,
checksums, and platform detection. The first macOS milestone may ship unsigned
and unnotarized artifacts, but that limitation must be documented because users
may see Gatekeeper friction. Apple-native distribution, using Developer ID
signing and notarization, is a follow-up release-hardening milestone.

Developer ID signing requires Apple Developer Program membership. Apple offers
fee waivers for eligible legal entities such as nonprofit organizations,
accredited educational institutions, and government entities; it does not offer
a general open-source-project waiver for individual maintainers.

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
- reboot resistance: set a Compose restart policy and document that Docker
  Desktop must be configured by the user to start at login. Do not make the
  first milestone depend on automatic Docker Desktop login-start detection.
- installed state and generated artifacts remain under the project-local
  Reploy directory
- installed Docker identity reuses the existing installed identity model:
  service name plus canonical target path derive the instance id, compose
  project, container name, and network name recorded in installed state.
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

Until macOS CI or a real macOS validation host is available, implementation may
continue using Linux automation, cross-compilation, and release artifact checks.
Those checks do not satisfy the real macOS smoke requirements. Record the
macOS staging, Docker Desktop runtime, and persistent-install smokes as
deferred, and complete them before claiming release readiness.

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

## Detailed AWD Execution Plan

The high-level milestones above are not detailed enough for implementation on
their own. Use this AWD plan as the executable port shape:

```text
(inventory macOS port plan and current Reploy platform assumptions
-> record agreed macOS support contract and command matrix
-> (review support contract against settled decisions !> support contract matches settled decisions)
-> design platform capability layer and Docker Desktop runtime detection
-> implement platform support decisions and unsupported-command errors
-> ((run platform unit tests + run CLI smoke for Linux behavior) !> platform checks pass)
-> add Darwin release artifacts and installer platform detection
-> ((run darwin-arm64 cross compile + run darwin-amd64 cross compile + verify release archive naming and checksums) !> release artifact checks pass)
-> implement macOS Docker Desktop preflight and doctor checks
-> ((run doctor unit tests + run Docker timeout and responsiveness tests) !> doctor checks pass)
-> (validate CLI-only macOS staging on real macOS !> CLI-only macOS staging smoke passes)
-> (validate Docker Desktop staging runtime on real macOS !> Docker Desktop staging smoke passes)
-> record agreed persistent development install semantics for macOS
-> (review persistent install design against settled decisions !> persistent install design matches settled decisions)
-> implement Docker Desktop-backed persistent development install and uninstall
-> ((run focused install tests + run generated control script tests + run uninstall cleanup tests) !> persistent install automated checks pass)
-> (validate persistent development install on real macOS with Docker Desktop !> persistent install macOS smoke passes)
-> publish macOS user docs and troubleshooting
-> (review launchd future-design boundary remains explicit !> docs and scope review pass)
-> (release readiness review with evidence bundle ?> approve macOS support milestone))
```

### Expanded Planning Steps

Before implementation, expand the following chunky steps into small design
artifacts and checks.

The main product decisions are already settled:

- macOS is a development and staging host, not a production permanent-install
  target.
- macOS uses Docker Desktop and Linux containers.
- The normal `install` command is allowed on macOS as a Docker Desktop-backed
  persistent development install.
- macOS system install through launchd is out of scope for the first milestone.
- Reploy state and generated artifacts remain project-local.
- Docker Desktop-backed install must warn about weaker isolation than
  Linux/systemd install.
- Reboot resistance means Docker Desktop starts when the user signs in and
  Reploy-managed containers use a restart policy.

The review gates in the AWD plan are therefore consistency gates. They should
verify that design artifacts and implementation match these decisions, not
reopen the product direction.

#### Platform Capability Layer

Define this API before coding platform branches:

- host OS and architecture detection
- command support by OS
- command groups that require Docker
- command groups that require Docker Desktop specifically
- install semantics by OS
- unsupported-command error messages
- Linux/systemd-only support boundaries
- Docker Desktop security warning policy

The output should be a decision table and a small internal API. The API should
keep staging and Docker Compose code portable while making Linux system install
behavior explicit.

#### Docker Desktop Detection And Preflight

Split Docker Desktop compatibility into separately testable checks:

- Docker CLI exists
- Docker Compose is available
- daemon responds within the configured timeout
- runtime appears to be Docker Desktop or an unknown Docker runtime
- Docker context/socket is reachable from the user's shell
- bind mount smoke works from a project-local staging directory
- port binding smoke works for `127.0.0.1` and any supported public bind
- installed-but-not-running failure message is quick and useful
- unsupported or surprising Docker context failure message is quick and useful

Docker Desktop detection should trigger the macOS/Windows weaker-security
warning. Failure to prove Docker Desktop should not skip the warning when the
host platform still has weaker Docker-runtime isolation than Linux.

#### Persistent Development Install

Record the settled product contract before coding install behavior:

- normal `install` and `uninstall` command surface remains the user interface
- no Docker Desktop backend name is exposed as a target
- generated state and metadata remain project-local
- installed Docker identity reuses the existing installed identity model:
  service name plus canonical target path derive the instance id, compose
  project, container name, and network name recorded in installed state
- Compose restart policy is explicit, likely `unless-stopped`
- installed Docker-backed control script behavior is distinct from staging
  behavior
- `status`, `logs`, `down`, `restart`, and `uninstall` operate on the
  installed Docker/Compose project
- uninstall cleanup covers containers, networks, volumes, generated files, and
  installed metadata
- reboot resistance means containers can restart after Docker Desktop starts at
  user login
- Docker Desktop login startup is documented as a user-managed prerequisite for
  reboot resistance; docs provide manual validation and remediation
- install output warns that Docker Desktop-backed install is for development
  persistence and does not provide Linux service-user isolation

This design should be reviewed before implementation because it records the
settled meaning of `install` on macOS. The review should check consistency with
the agreed product contract rather than reopen the direction.

#### Release Artifacts

Expand release work if the existing release automation does not already model
new target triples cleanly:

- add `darwin-arm64`
- add `darwin-amd64`
- define archive names
- generate and publish checksums
- update install script platform mapping
- update release documentation
- document that first macOS artifacts may be unsigned and unnotarized
- leave Developer ID signing, notarization, and related Apple Developer Program
  setup as a follow-up release-hardening milestone

#### Real macOS Smoke Checklists

Create runnable smoke checklists with expected output for each manual or
machine-backed validation pass:

- CLI-only staging smoke: `stage`, `stage --update`, `info`, and unsupported
  Linux-only command failures
- Docker Desktop staging smoke: `bundle build`, `bundle check`, app command,
  `up`, `status`, `logs`, `test`, and `down`
- Docker Desktop failure smoke: Docker installed but not running, daemon
  timeout, bind mount failure, and port conflict
- persistent install smoke: `install`, warning output, restart/status/logs,
  reboot/login persistence expectation, and `uninstall`
- cleanup smoke: generated artifacts, containers, networks, and volumes are
  removed or preserved according to the install contract

Each smoke should record host architecture, macOS version, Docker Desktop
version, Docker context, exact commands, expected outputs, and cleanup steps.

### Per-Phase Commit Loop

Use a small commit loop for each implementation phase:

```text
(select one macOS port phase
-> review selected phase scope ?>
   (((implement phase changes -> run focused tests + run relevant validation)
     *3 until pass: phase checks pass)
    !> compose phase commit message
    -> commit selected phase
    -> verify no unintended changes remain))
```

This keeps platform support, release artifacts, Docker Desktop behavior,
persistent install semantics, and documentation as reviewable slices instead of
one large port commit.

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
