---
status: Draft
updated: 2026-07-05
summary: Draft Windows support plan for staging and Docker-managed permanent installs.
---

# Windows Port Plan

This document defines the work needed to make Windows an explicit Reploy
support target.

The product stance is:

- Linux is the production permanent-install target.
- Windows is a first-class development and staging host.
- Windows staging and runtime commands use Docker Desktop with Linux
  containers.
- Windows can support a Docker-managed permanent install using Docker Desktop.
- Windows installed control commands include a native PowerShell/Windows
  surface backed by `reploy.exe` and Docker Desktop. A matching POSIX-style
  control script may also be generated for WSL/Linux-like access, but WSL is
  not required for native Windows operation.
- Windows OS service install is deferred. If it is explored later, it should be
  treated as a separate Windows Service or service-manager design, not part of
  the first Windows support milestone.

This keeps Reploy's operator story honest. Local staging is the project-local
runtime used to configure, test, and iterate. Docker-managed permanent install
is the installed Docker/Compose runtime with restart policy, installed state,
and app control commands. Linux/systemd, future launchd, and future Windows
Service support are OS service installs with stronger service-manager
semantics.

## Goals

- Build and distribute `reploy` binaries for both Windows architectures:
  x64 (`windows-amd64`) and Arm (`windows-arm64`).
- Support the staging workflow on Windows with Docker Desktop:
  `stage`, `stage --update`, `info`, `bundle`, `app`, runtime commands, and
  `test`.
- Support install/uninstall for Windows hosts using the normal Reploy install
  command surface.
- Provide a PowerShell-native installed control surface for Windows
  Docker-managed permanent installs.
- Keep installed control script naming consistent across shells, for
  example `arbiterctl` for POSIX-like shells and `arbiterctl.ps1` for
  PowerShell.
- Make Windows install reboot resistant when Docker Desktop is configured to
  start when the user signs in.
- Make Linux/systemd OS service guarantees clearly out of scope for Windows.
- Warn during Windows install when the detected Docker runtime is Docker
  Desktop, because the security offered on macOS and Windows is weaker than
  Linux.
- Document which commands require Docker Desktop and which commands remain
  Linux-only.
- Keep future Windows Service work visible without making it part of the
  initial Windows support contract.

## Non-Goals

- Do not support Windows OS service install in the first Windows milestone.
- Do not make Windows Services, Task Scheduler, NSSM, or WSL init an
  alternative meaning of OS service install.
- Do not support Windows containers as a runtime backend in the first Windows
  milestone.
- Do not require a native Windows Docker Engine.
- Do not promise production server behavior on macOS or Windows.
- Do not move staging state into `%LOCALAPPDATA%`, `%PROGRAMDATA%`, or another
  user-global root for the first Windows milestone. Staging remains
  project-local.
- Do not expose Docker Desktop as a user-facing install target or backend.
- Do not claim that Docker Desktop persistence provides the same isolation or
  lifecycle guarantees as Linux/systemd install.
- Do not treat WSL as a native Windows backend. WSL is officially supported
  through Reploy's Linux path: the Linux binary, Linux paths, and Linux control
  script behavior inside WSL.

## Assumptions

- Docker remains the first runtime backend. On Windows this means Docker
  Desktop, the WSL 2 backend, and Linux containers.
- Docker Desktop is an appropriate dependency for Windows workflows.
- Windows install means Docker-managed permanent install: persistent
  Docker/Compose resources, restart policies, installed state, and installed
  control commands. Docker Desktop itself remains an external dependency.
- Reboot resistance depends on Docker Desktop being configured to start when
  the user signs in.
- Reploy staging state and generated artifacts remain project-local. Native
  Windows Docker-managed installs default their install target to a per-user
  app-data root, such as `%LOCALAPPDATA%\Reploy\installs\<app-id>`, with an
  explicit target override available for other install roots.
- Permanent production install remains Linux/systemd-backed.
- First milestone native Windows path support should cover normal drive-letter
  project paths, including paths with spaces. UNC paths, junction-heavy paths,
  and unusual symlink cases are not promised unless smoke tests prove them.
- Windows support must be tested on real Windows with Docker Desktop; unit
  tests alone are not enough.

## WSL Platform Boundary

Native Windows support and WSL support are not the same user promise.

WSL is officially supported through Reploy's Linux path. That means users run
the Linux `reploy` binary inside WSL, use Linux paths, and get Linux-style
control scripts such as `<app-id>ctl`. WSL is not a native Windows backend and
does not use the `reploy.exe` support contract.

For the first native Windows milestone, the primary promise is:

- users run `reploy.exe` from PowerShell or `cmd.exe`
- Reploy uses Docker Desktop from the Windows shell
- deployed apps, including Arbiter, run as Linux containers inside Docker
  Desktop
- installed host controls include a PowerShell-native surface such as
  `<app-id>ctl.ps1`
- a matching POSIX-style `<app-id>ctl` script may exist for Linux-like access
  when the same deployment directory is reached from WSL, but that script
  follows the Linux path semantics

The user-visible implications are:

- users would need to know whether they are using native Windows Reploy or
  Linux Reploy inside WSL
- install instructions would split between `reploy.exe` and a Linux `reploy`
  binary
- paths would split between Windows paths such as `C:\...` and WSL paths such
  as `/mnt/c/...`
- Docker discovery would need to cover both Windows `docker.exe` and Docker
  access from inside WSL distributions
- generated state, line endings, permissions, executable bits, and symlinks
  would need WSL-specific validation
- control commands would need separate support expectations for
  `<app-id>ctl.ps1` from Windows and `<app-id>ctl` from WSL
- native Windows and WSL access to the same installed directory would need a
  compatibility marker. Until compatibility is proven, Reploy should fail with
  a clear error that says whether to use `<app-id>ctl.ps1` from Windows or the
  Linux path from WSL.
- smoke tests and support docs would need a separate WSL matrix

The support contract should therefore say:

- Native Windows support is `reploy.exe`, PowerShell/`cmd.exe`, Docker Desktop,
  and `<app-id>ctl.ps1`.
- WSL support is the Linux path inside WSL, including Linux binaries, Linux
  paths, and `<app-id>ctl`.
- Bugs found only in WSL should be triaged against the Linux path unless they
  involve native Windows interop such as shared directories or Docker Desktop
  integration.

## Support Matrix

This matrix is the first native Windows support contract to implement. It is
implemented incrementally; automated checks and real Windows smoke evidence
are tracked separately from the support contract.

Native Windows means:

- `reploy.exe` is run from PowerShell or `cmd.exe`
- Docker work uses Docker Desktop reachable from that Windows shell
- app containers are Linux containers
- installed app controls use a PowerShell-native surface such as
  `<app-id>ctl.ps1`
- WSL remains the Linux support path, not the native Windows support path

| Command or surface | First Windows milestone | Docker Desktop required | Notes |
| --- | --- | --- | --- |
| `--help`, `help`, `--version`, `version` | supported | no | Pure CLI behavior. |
| `index update`, `index search`, `index show` | supported | no | Network/index behavior should be platform-neutral. |
| `stage APP_REF` | supported | no for file/source staging; maybe for provider workflows that build/check artifacts | Must support Windows drive-letter paths and paths with spaces. |
| `stage --update [APP_REF]` | supported | no for generated-file refresh; maybe for provider workflows that build/check artifacts | Must preserve staging as project-local state, not `%LOCALAPPDATA%`. |
| `info` | supported | no | Reads staging state and bundle metadata. |
| `bundle list`, `bundle list all`, `bundle list-options` | supported | no | Metadata-only bundle inspection. |
| `bundle add`, `bundle remove`, `bundle clean` | supported | no | Mutates staging bundle selection or generated bundle artifacts. |
| `bundle check`, `bundle build`, `bundle upgrade` | supported | yes | Uses Docker Desktop with Linux containers to prepare and validate bundle artifacts. |
| `app` summary | supported | no | Listing blueprint-declared app commands should not require Docker. |
| `app COMMAND` | supported | yes | Runs app command through the staging Docker/Compose runtime. |
| `up`, `restart`, `down` | supported | yes | Operates the staging Docker/Compose runtime through Docker Desktop. |
| `ps`, `status`, `logs` | supported | yes | Reads staging Docker/Compose state and logs through Docker Desktop. |
| `test` | supported | yes | Probes the staging app health endpoint. |
| `doctor` for staging files and generated-file drift | supported | no | Should still report Docker Desktop readiness separately when relevant. |
| `doctor --preinstall` and install-readiness checks | supported | yes | Must distinguish Docker Desktop-backed install readiness from Linux/systemd readiness. |
| `install APP_REF` direct install | supported as Docker-managed permanent install | yes | Uses a temporary staging-like workspace, Docker/Compose restart policy, and a Docker Desktop security warning. |
| `install --dir DIR` staged install | supported as Docker-managed permanent install | yes | Installs from existing staging state into a Windows install target. |
| `install --dry-run` | supported | no for plan rendering; yes if preinstall checks are requested | Must render Windows paths and Docker-managed install semantics. |
| `uninstall --from DIR` | supported for Docker-managed permanent install | yes unless `--dry-run` can render from state only | Removes installed Docker resources and installed metadata for the selected target. |
| `uninstall --service-name NAME` | deferred unless mapped to recorded Docker-managed installed state | maybe | Linux currently uses service names for systemd discovery. Windows should not invent service semantics. |
| `uninstall --list-services` | unsupported for native Windows in first milestone | no | This is Linux/systemd service discovery. Use installed state or explicit target paths for Windows. |
| generated `<app-id>ctl.ps1` | supported | yes for runtime operations | Native Windows installed control surface. Must not require `sh`, Git Bash, MSYS2, Cygwin, or WSL. |
| generated POSIX-style `<app-id>ctl` beside a Windows install | optional/deferred | yes if supported | Only for WSL/Linux-like access. Must be clearly documented as Linux-path behavior. |
| Windows Service install | unsupported | no | Future design topic; not exposed as a first-milestone backend. |
| WSL using Linux `reploy` | supported through Linux path | depends on Linux path | Not native Windows support. Uses Linux binary, Linux paths, and Linux control scripts. |

Unsupported operations should fail before doing partial work.

For implementation, "supported" means the command has explicit platform
behavior, tests or smoke coverage appropriate to the risk, and user-facing
errors for missing prerequisites. "Unsupported" means the command fails before
mutating state and explains the Windows support boundary.

## Major Work Areas

### 1. Release Artifacts

Add Windows targets to build and release workflows:

- `windows-amd64`
- `windows-arm64`

The installer and release docs should understand Windows archive names,
checksums, platform detection, and the `.exe` binary name. Windows distribution
should have a first-class native installer path, likely a PowerShell
`install.ps1` equivalent to the Linux installer. The installer downloads the
release artifact, verifies the expected checksum when available, installs
`reploy.exe`, and reports the installed path. Zip archives may still be
published as release artifacts, but they are not the primary user install
story. Add WinGet as the managed install/upgrade channel once artifacts are
stable. Shell-only Linux installer instructions are not sufficient for native
Windows users.

The current POSIX installer installs `reploy` into `$HOME/.local/bin` by
default, prints a PATH hint when needed, and does not edit shell profile files
or system PATH. The native Windows installer should make the same policy
explicit: install the CLI into a chosen user-writable directory such as
`%LOCALAPPDATA%\Programs\Reploy\bin` by default, offer an opt-in update to the
current user's PATH, and avoid silently changing machine-wide PATH. In
interactive mode the installer may ask whether to add the install directory to
the user PATH. In non-interactive mode this should be controlled by explicit
flags such as `-AddToPath` and `-NoPathUpdate`.

The user PATH update should use the Windows user environment, not an
administrator-owned machine PATH. It should preserve existing entries, avoid
duplicates, handle paths with spaces, and tell the user that already-open
shells may need to restart or have `$env:Path` updated for the current
session.

The first Windows milestone may ship unsigned artifacts, but that limitation
must be documented because users may see SmartScreen or enterprise endpoint
protection friction. Authenticode signing is a follow-up release-hardening
milestone.

PowerShell control docs should mention execution-policy friction for local
`.ps1` scripts. Reploy should generate the script and document the expected
invocation, but it should not change machine execution policy.

### 2. Platform Detection

Use the same platform capability layer needed for macOS instead of scattering
`runtime.GOOS` checks. It should answer questions such as:

- What host OS is this?
- Is this command supported on this platform?
- Does this command require Docker Desktop?
- Should this command use staging semantics or permanent-install semantics?
- What message should unsupported install operations show?
- Does this host need Windows-specific path, shell, or executable-name
  handling?

This layer should keep staging and Docker Compose code portable while making
Linux/systemd install behavior explicit.

### 3. Docker Desktop Compatibility

Validate Docker Desktop behavior separately from Linux Docker Engine behavior:

- daemon responsiveness and timeout handling
- Docker context and socket discovery from PowerShell and `cmd.exe`
- Docker Desktop WSL 2 backend and Linux-container mode
- bind mounts from Windows staging directories
- drive-letter, backslash, space, and Unicode path handling
- UID/GID and file ownership behavior inside mounted volumes
- line-ending behavior for generated files consumed inside containers
- port binding on `127.0.0.1` and `0.0.0.0`, including Windows Firewall
  prompts
- Compose project naming
- app command execution through generated compose files
- behavior when Docker Desktop is installed but not running

User-facing requirements should stay simple:

> Windows staging and Docker-managed permanent install require Docker Desktop
> using Linux containers.

The detailed docs can explain that Docker Desktop must be running and
reachable from the user's shell.

### 4. Docker-Managed Permanent Install

Define Windows install behavior that persists app containers through Docker
Desktop instead of through Windows Services, Task Scheduler, WSL init, or
systemd.

Open design points:

- command shape: use the normal `install` and `uninstall` commands; do not
  expose Docker Desktop as a user-facing target or backend
- install-time warning: tell users that macOS and Windows installs have weaker
  security than Linux installs because the Docker runtime model does not
  provide the same service-user isolation. If Docker Desktop is confidently
  detected, mention it by name; otherwise do not skip the warning.
- generated Compose restart policy, likely `unless-stopped`
- reboot resistance: set a Compose restart policy and document that Docker
  Desktop must be configured by the user to start at login. Do not make the
  first milestone depend on automatic Docker Desktop login-start detection.
- default install target: `%LOCALAPPDATA%\Reploy\installs\<app-id>` for native
  Windows user installs. Admin-owned paths such as `%ProgramFiles%\<app-id>`
  remain explicit target choices, not the default.
- installed Reploy metadata and generated runtime files live under the install
  target directory
- installed Docker identity reuses the existing installed identity model:
  service name plus canonical target path derive the instance id, compose
  project, container name, and network name recorded in installed state
- how `uninstall` removes containers, networks, volumes, generated files, and
  installed metadata
- how control scripts and `reploy status/logs/down/restart` distinguish staged
  runtime from installed Docker Desktop runtime
- installed control commands include a native Windows/PowerShell surface. They
  may be generated PowerShell wrappers, direct `reploy.exe` commands, or both.
  They must not require `sh`, Git Bash, MSYS2, Cygwin, or WSL for native
  Windows operation.
- the installed control surface may also include a matching POSIX-style
  `<app-id>ctl` script for WSL/Linux-like access to the same install target
  directory. The PowerShell wrapper should use the matching `<app-id>ctl.ps1`
  name.
- if a native Windows install is accessed from WSL and the installed state is
  not known to be compatible with the Linux path, the control command should
  fail before touching Docker resources and explain which control surface to
  use.
- what `doctor` should require before allowing install
- what message to show when Docker Desktop is installed but not configured to
  start when the user signs in, or when that setting cannot be detected
- how docs handle PowerShell execution policy for `<app-id>ctl.ps1`

This is a Docker-managed permanent install, not an OS service install. If
Docker Desktop is installed and configured machine-wide, the installed
containers may have machine-level persistence through Docker. Reploy still
does not install a Windows Service in this milestone. It uses Docker Desktop as
the Windows runtime, but that should be communicated as a requirement and
security caveat rather than as a separate install target. It depends on Docker
Desktop being installed, running, reachable from the user's shell, using Linux
containers, and configured to start automatically if reboot persistence is
desired.

Security boundary:

- container processes are isolated inside Docker Desktop's Linux VM
- Docker access from the same Windows user remains powerful over Reploy-managed
  containers and images
- this does not provide the same protection from same-user runaway agents that
  a dedicated service user on a Linux host can provide

### 5. Filesystem, Path, and Shell Audit

Audit Linux assumptions that affect Windows staging:

- absolute path handling, including drive letters and UNC paths
- backslash versus slash normalization
- spaces and shell metacharacters in project paths
- symlinks and junctions
- executable bits and generated scripts
- CRLF versus LF line endings
- `%TEMP%`, `%TMP%`, and paths mounted into Linux containers
- user and group IDs in Docker bind mounts
- generated PowerShell wrappers, matching POSIX-style wrappers, and direct CLI
  behavior
- assumptions about `/usr/local/bin`, `$PATH`, and POSIX tools
- default target path references in docs and dry-run output

For the first milestone, prefer native Go command behavior over generated host
scripts whenever possible. When installed control wrappers are useful, provide
PowerShell-native wrappers for Windows host use. A matching POSIX-style wrapper
can be generated for WSL/Linux-like use, but native Windows workflows must not
require Git Bash, MSYS2, Cygwin, or WSL.

### 6. Doctor Checks

Add Windows-specific doctor checks for development/staging:

- supported Windows version, likely Windows 10/11 with WSL 2 support
- Docker Desktop installed and daemon responsive
- Docker Desktop using Linux containers
- Docker CLI and Compose available from the current shell
- bind mounts work from a temporary staging directory
- expected ports are available, with useful handling for Windows Firewall
  prompts or blocked bindings
- Windows Firewall prompts or blocked bindings are explained clearly; Reploy
  does not change firewall rules automatically in the first milestone
- control command dependencies, avoiding POSIX shell dependencies for native
  Windows workflows
- system/all-users Docker Desktop or Docker Engine detection where practical.
  When Docker appears to be machine-wide and the requested target is
  admin-owned, Reploy should suggest an elevated/admin install instead of
  silently changing the install scope.

Doctor output should distinguish:

- "this host can stage and run apps with Docker Desktop"
- "this host can create a Docker-managed permanent install"
- "Docker Desktop login startup is enabled", "manual validation is needed", or
  "enable Docker Desktop start-at-login for reboot resistance"
- "OS service install is Linux/systemd-only in this release"

### 7. Tests and Smoke Validation

Add validation layers:

- cross-compile checks for `windows-amd64`
- cross-compile checks for `windows-arm64`
- Windows CI tests for non-Docker command behavior
- GitHub Actions Windows CI matrix matching the host-check pattern used for
  macOS: `windows-amd64` on `windows-2025` and `windows-arm64` on
  `windows-11-arm`, running Go tests plus the CLI smoke with Docker optional.
- documented manual Docker Desktop smoke tests for staging/runtime
- real Windows smoke covering stage, bundle build/check, app command, up,
  status, logs, test, and down from PowerShell
- real Windows smoke covering Docker Desktop-backed install, reboot/login
  expectation documentation, restart, status, logs, and uninstall

The Windows smoke should verify that generated artifacts clean up normally,
that failed Docker Desktop checks fail quickly with useful messages, and that
paths with spaces do not break generated Compose or runtime behavior.

Until Windows CI or a real Windows validation host is available,
implementation may continue using Linux automation, cross-compilation, and
release artifact checks. Those checks do not satisfy the real Windows smoke
requirements. Record the Windows staging, Docker Desktop runtime, and
persistent-install smokes as deferred, and complete them before claiming
release readiness.

## Suggested Milestones

1. **Define support matrix and platform capability layer.**
   Add explicit Windows command support decisions and clear
   unsupported-command errors.

2. **Add Windows release artifacts.**
   Build Windows binaries and update install/release docs.

3. **Validate Windows staging without Docker.**
   Run CLI-only staging, update, and info checks on Windows.

4. **Validate Docker Desktop staging runtime.**
   Run bundle, app, runtime, and test smoke checks with Docker Desktop using
   Linux containers.

5. **Validate Docker-managed permanent install.**
   Smoke-test normal install, restart/status/logs, and uninstall against
   Docker Desktop, including the Docker Desktop security warning and
   reboot-resistance validation path.

6. **Publish Windows docs.**
   Document requirements, support matrix, known limitations, and
   troubleshooting for Docker Desktop.

## Detailed AWD Execution Plan

The high-level milestones above are not detailed enough for implementation on
their own. Use this AWD plan as the executable port shape:

```text
(inventory Windows port plan and current Reploy platform assumptions
-> record agreed Windows support contract and command matrix
-> (review support contract against settled decisions !> support contract matches settled decisions)
-> design platform capability layer and Docker Desktop runtime detection
-> implement platform support decisions and unsupported-command errors
-> ((run platform unit tests + run CLI smoke for Linux behavior) !> platform checks pass)
-> add Windows release artifacts and installer platform detection
-> ((run windows-amd64 cross compile + run windows-arm64 cross compile + verify release archive naming, .exe handling, and checksums) !> release artifact checks pass)
-> implement Windows Docker Desktop preflight and doctor checks
-> ((run doctor unit tests + run Docker timeout and responsiveness tests) !> doctor checks pass)
-> (validate CLI-only Windows staging on real Windows !> CLI-only Windows staging smoke passes)
-> (validate Docker Desktop staging runtime on real Windows !> Docker Desktop staging smoke passes)
-> record agreed Docker-managed permanent install semantics for Windows
-> (review persistent install design against settled decisions !> persistent install design matches settled decisions)
-> implement Docker Desktop-backed Docker-managed install and uninstall
-> ((run focused install tests + run Windows control command tests + run uninstall cleanup tests) !> Docker-managed install automated checks pass)
-> (validate Docker-managed permanent install on real Windows with Docker Desktop !> Docker-managed install Windows smoke passes)
-> publish Windows user docs and troubleshooting
-> (review Windows Service future-design boundary remains explicit !> docs and scope review pass)
-> (release readiness review with evidence bundle ?> approve Windows support milestone))
```

### Expanded Planning Steps

Before implementation, expand the following chunky steps into small design
artifacts and checks.

The main product decisions are already settled:

- Windows is a development and staging host, not a production
  permanent-install target.
- Windows uses Docker Desktop, the WSL 2 backend, and Linux containers.
- The normal `install` command is allowed on Windows as a Docker
  Desktop-backed Docker-managed permanent install.
- Windows OS service install through Windows Services, Task Scheduler, NSSM, WSL
  init, or another service manager is out of scope for the first milestone.
- Reploy staging state and generated artifacts remain project-local.
- Native Windows Docker-managed installs default to
  `%LOCALAPPDATA%\Reploy\installs\<app-id>` for user installs.
- Docker Desktop-backed install must warn about weaker isolation than
  Linux/systemd install.
- Installed control commands include a native Windows/PowerShell surface backed
  by `reploy.exe` and Docker Desktop. A matching POSIX-style control script may
  also exist for WSL/Linux-like access, but WSL is not required for native
  Windows operation.
- Reboot resistance means Docker Desktop starts when the user signs in and
  Reploy-managed containers use a restart policy.
- WSL is officially supported through Reploy's Linux path. It uses the Linux
  binary, Linux paths, and Linux control script behavior inside WSL rather than
  the native Windows `reploy.exe` support contract.

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
- Windows executable-name, path, shell, and line-ending handling

The output should be a decision table and a small internal API. The API should
keep staging and Docker Compose code portable while making Linux system install
behavior explicit.

#### Docker Desktop Detection And Preflight

Split Docker Desktop compatibility into separately testable checks:

- Docker CLI exists
- Docker Compose is available
- daemon responds within the configured timeout
- runtime appears to be Docker Desktop or an unknown Docker runtime
- Docker context/socket is reachable from PowerShell and `cmd.exe`
- Docker Desktop is using Linux containers
- bind mount smoke works from a project-local staging directory, including a
  path with spaces
- generated files consumed inside containers use usable line endings
- port binding smoke works for `127.0.0.1` and any supported public bind
- installed-but-not-running failure message is quick and useful
- unsupported or surprising Docker context failure message is quick and useful

Docker Desktop detection should trigger the macOS/Windows weaker-security
warning. Failure to prove Docker Desktop should not skip the warning when the
host platform still has weaker Docker-runtime isolation than Linux.

#### Docker-Managed Permanent Install

Record the settled product contract before coding install behavior:

- normal `install` and `uninstall` command surface remains the user interface
- no Docker Desktop backend name is exposed as a target
- staging state and generated artifacts remain project-local
- installed state and generated runtime files live under the install target
  directory
- native Windows Docker-managed installs default to
  `%LOCALAPPDATA%\Reploy\installs\<app-id>` for user installs, while
  admin-owned targets such as `%ProgramFiles%\<app-id>` require an explicit
  target choice
- installed Docker identity reuses the existing installed identity model:
  service name plus canonical target path derive the instance id, compose
  project, container name, and network name recorded in installed state
- Compose restart policy is explicit, likely `unless-stopped`
- installed Docker-backed control command behavior is distinct from staging
  behavior
- installed control commands include a native Windows/PowerShell surface. They
  may be generated PowerShell wrappers, direct `reploy.exe` commands, or both.
  They must not require `sh`, Git Bash, MSYS2, Cygwin, or WSL for native
  Windows operation.
- if a matching POSIX-style `<app-id>ctl` script is generated for WSL/Linux-like
  access, it must be documented as Linux-path behavior inside WSL, not as a
  native Windows backend.
- `status`, `logs`, `down`, `restart`, and `uninstall` operate on the
  installed Docker/Compose project
- uninstall cleanup covers containers, networks, volumes, generated files, and
  installed metadata
- reboot resistance means containers can restart after Docker Desktop starts at
  user login
- Docker Desktop login startup is documented as a user-managed prerequisite for
  reboot resistance; docs provide manual validation and remediation
- install output warns that Docker Desktop-backed install is a Docker-managed
  permanent install and does not provide Linux service-user isolation

This design should be reviewed before implementation because it records the
settled meaning of `install` on Windows. The review should check consistency
with the agreed product contract rather than reopen the direction.

#### Release Artifacts

Expand release work if the existing release automation does not already model
new target triples cleanly:

- add `windows-amd64`
- add `windows-arm64`
- define archive names
- preserve the `.exe` suffix through packaging, install, and smoke checks
- generate and publish checksums
- add a PowerShell-native installer, likely `install.ps1`, rather than relying
  on the POSIX installer for native Windows users
- update installer platform mapping
- add a PowerShell-friendly install path
- document that installer flows install the CLI to a user-writable location,
  such as `%LOCALAPPDATA%\Programs\Reploy\bin`
- implement an explicit user PATH update option for the Windows installer,
  such as an interactive prompt or `-AddToPath`, while avoiding silent
  machine-wide PATH edits
- update release documentation
- document that first Windows artifacts may be unsigned
- leave Authenticode signing, certificate ownership, and SmartScreen
  hardening as a follow-up release-hardening milestone

#### Real Windows Smoke Checklists

Create runnable smoke checklists with expected output for each manual or
machine-backed validation pass:

- CLI-only staging smoke: `stage`, `stage --update`, `info`, and unsupported
  Linux-only command failures from PowerShell
- Docker Desktop staging smoke: `bundle build`, `bundle check`, app command,
  `up`, `status`, `logs`, `test`, and `down`
- Docker Desktop failure smoke: Docker installed but not running, daemon
  timeout, Linux-container mode missing, bind mount failure, and port conflict
- persistent install smoke: `install`, warning output, restart/status/logs,
  reboot/login persistence expectation, and `uninstall`
- path smoke: project directory with spaces, Windows drive-letter path, and any
  supported UNC or symlink/junction behavior
- PowerShell control smoke: `<app-id>ctl.ps1` invocation, execution-policy
  guidance, and parity with the POSIX-style `<app-id>ctl` where both exist
- cleanup smoke: generated artifacts, containers, networks, and volumes are
  removed or preserved according to the install contract

Each smoke should record host architecture, Windows version, Docker Desktop
version, Docker context, Docker Desktop backend, exact commands, expected
outputs, and cleanup steps.

### Per-Phase Commit Loop

Use a small commit loop for each implementation phase:

```text
(select one Windows port phase
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

## Future: Windows Service Permanent Install

Windows Service install is intentionally outside the first support milestone.
If it becomes a goal later, the likely direction is:

- a Windows Service backend beside Linux/systemd
- app process identity and permissions defined explicitly, not inherited from
  an interactive development shell
- backend-aware installed control commands
- Docker Desktop or another Docker endpoint usable from the service context
- clear install, upgrade, restart, logs, and uninstall behavior for the service
  backend

That future design has a hard product question: Docker Desktop can be installed
for all users, but Reploy still needs to prove the Docker endpoint and app
containers behave correctly from a service context before promising Windows
Service semantics. Until then, Windows native workflows are staging and
Docker-managed permanent install.

## Key Risks

- Docker Desktop behavior differs from Linux Docker Engine behavior.
- Windows path, shell, line-ending, and executable-name behavior can affect
  generated artifacts and Compose invocations.
- File ownership and bind mounts can differ from Linux enough to affect bundle
  builds and runtime writes.
- Users may expect `install` to work on Windows once staging works; docs and
  install output need to distinguish Docker-managed permanent install from
  Linux/systemd service guarantees.
- Same-user Docker access limits the security value of Docker Desktop-backed
  install for untrusted local agents.
- Release artifacts may imply broader support than the support matrix actually
  promises.
