---
status: Complete
updated: 2026-07-27
summary: Implementation and fresh follow-up review complete; all discovered P1 findings are resolved.
---

# Reploy Redesign Evaluation

This is a temporary evidence and continuation surface for observations made
during the redesign walkthrough. The implementation slices below define the
primary owner and verification boundary for every finding. Completed findings
remain as historical acceptance evidence through the fresh review; this
document can be removed after that review is summarized.

## Implementation triage

The reviewed implementation stack completed slices 1-15. Their original scope
and verification gates remain below as completion evidence. A fresh
end-to-end review follows this implementation sequence.

The broader post-v1 design for explicit local build commands and cross-run
caching remains related but separate.

Each implementation slice was developed test-first, passed its focused tests
and `go test ./...`, included a Changie fragment when user-visible, and was
kept independently reviewable. Docker-facing behavior also received focused
integration testing against a real Docker daemon.

### Slice 1: Make transient home writable

**State:** complete; implemented and reviewed.

Fix operation-local home initialization for non-root transient containers.
The solution must apply consistently to shells, app commands, and lifecycle
actions; preserve the read-only image filesystem; retain anonymous-volume
cleanup; and avoid host-owned persistent state.

**Primary finding:** `Transient shell home is not writable by the runtime user`.

**Verification gate:**

- a transient container running as a numeric non-root UID/GID can create,
  modify, and remove files under `$HOME` and `$TMPDIR`;
- its image filesystem remains read-only;
- its anonymous home volume is removed after normal exit and forced stop; and
- shell, app-command, and lifecycle integration tests exercise the shared path.

### Slice 2: Stop app commands from refreshing the environment

**State:** complete; implemented and reviewed.

Remove the automatic provider-build call from staged app commands. An app
command must load and cheaply validate the exact current generation, fail with
`run reploy build` when none exists, join the live-run queue for that
generation, and fail for explicit retry if the generation changes while it is
waiting. Preserve the already-approved automatic build behavior of `reploy up`.

**Primary finding:** `Every staged app command enters the automatic-build path`.

**Verification gate:**

- a staged app command with a current build never invokes source observation,
  base selection, a resolver, or image construction;
- an absent current build gives the explicit recovery command;
- a waiting app command does not silently switch generations;
- installed app commands retain their current behavior; and
- `reploy up` still builds when its staged generation is missing or stale.

### Slice 3: Make completed uninstall idempotent

**State:** complete; implemented and reviewed.

Distinguish an absent explicit installation directory from unreadable or
corrupt installation state. Repeating an uninstall after `--remove-dir` must
return a successful no-op without asking for root. Missing-directory service
recovery remains explicit through `--service-name`, and only the recovered
scope determines whether elevation is required.

**Primary finding:** `Repeated completed uninstall incorrectly requires root`.

**Verification gate:**

- repeating a completed user uninstall exits successfully with the approved
  no-installation message;
- an unreadable or corrupt state is an error, not an idempotent no-op;
- missing-directory recovery with `--service-name` still works;
- recovered system-scope cleanup still requires root; and
- the successful-uninstall result is emitted after progress completion.

### Slice 4: Establish one operation-presentation framework

**State:** complete; implemented and reviewed.

Replace the current mixture of spinner output, successful child-process output,
and backend progress with one Reploy-owned operation presenter. It must support
named step transitions, one final result boundary, captured child diagnostics,
and explicit success/failure. Interactive default output uses a step-updating
spinner. `--verbose`, redirected output, and `TERM=dumb` use durable plain
Reploy-level lines. `NO_COLOR` disables color without changing content.

**Primary finding:** `Install and uninstall need one shared progress model`.

**Verification gate:**

- interactive output updates one spinner and prints one final result;
- verbose output has stable per-step lines and no animation;
- redirected and dumb-terminal output contains no ANSI or cursor controls;
- successful child output is capturable without leaking into normal output;
- failures retain the named failed step plus translated diagnostics; and
- completion messages cannot appear before the progress presenter stops.

### Slice 5: Give build a Reploy-owned progress and result contract

**State:** complete; implemented and reviewed.

Emit immediate component-oriented build progress, capture all Docker/BuildKit
output, deduplicate warnings, translate or classify those warnings in Reploy
terms, and finish fresh builds and exact-reuse no-ops with explicit results.
Raw backend transcripts require a diagnostic interface; `--verbose` remains
Reploy-level detail.

**Primary findings:**

- `Build progress starts too late`;
- `Aggregate build warnings at command completion`;
- `Build has no explicit final Reploy outcome`; and
- `Default build output must use Reploy concepts`.

**Verification gate:**

- progress begins before local-source observation or provider work;
- fresh, reused, failed, and warning-bearing builds have golden CLI tests;
- repeated backend warnings appear once at command completion;
- normal and verbose output contain no Docker-generated instructions; and
- a real Docker build shows only Reploy concepts and an unmistakable result.

### Slice 6: Normalize install, update, and uninstall presentation

**State:** complete; implemented and reviewed.

Apply the shared presenter to first install, reinstall/update, and uninstall.
Suppress successful lifecycle JSON and Docker network/container progress.
Report the deployed location, control command, service state, endpoints, and
blueprint success lines. Update and uninstall results must classify what was
preserved, replaced, reused, removed, and retained using actual resolved host
paths and generation decisions.

**Primary findings:**

- `Successful install leaks internals and omits the installed result`; and
- `Reinstall is not presented as an update`.

**Verification gate:**

- first install, unchanged reinstall, changed-generation reinstall,
  `--replace`, `--clean`, retained-directory uninstall, and `--remove-dir`
  have exact result tests;
- lifecycle stdout is hidden on success and retained for failure diagnostics;
- final labels use the deployed phase;
- blueprint-defined success lines appear exactly once; and
- a user-scope Docker integration verifies preserved data and replaced config.

### Slice 7: Normalize status and log observation

**State:** complete; implemented and reviewed.

Replace raw Compose status tables with a shared staged/deployed Reploy status
model. Preserve application log lines by default instead of forcing Docker
timestamps. Add explicit `--timestamps`; render its date, time, and timezone
fields with restrained terminal-only color while keeping redirected output
byte-stable and plain. Do not infer application severity.

**Primary findings:**

- `Status exposes raw Docker Compose output`; and
- `Logs have no Reploy record format or presentation`.

**Verification gate:**

- staging CLI and installed control script render the same logical status
  fields with phase-appropriate values;
- default logs contain the original application message without a prepended
  Docker timestamp;
- `--timestamps` prints one exact runtime timestamp;
- TTY, `NO_COLOR`, redirected, and `TERM=dumb` timestamp rendering is tested;
  and
- following logs remains pinned to the selected immutable container.

### Slice 8: Improve live-run and shell behavior

**State:** complete; implemented and reviewed.

Print an immediate reason before any `--wait` operation blocks, including queue
depth, the active run, conflicting writable paths, and Ctrl-C behavior. Add
`reploy shell --read-only`, which downgrades shared mounts to read-only while
retaining private writable home. Translate an acknowledged `runs stop` into a
Reploy shell-stopped result instead of Docker exit status 137.

**Primary findings:**

- “`--wait` blocks without explaining why”;
- `Add a read-only shell mode`; and
- `An intentionally stopped shell reports a Docker failure`.

**Verification gate:**

- one and multiple waiters receive accurate FIFO wait notices;
- Ctrl-C removes only the caller's waiter;
- a read-only shell overlaps with non-exclusive runs but waits behind an
  existing writer or lifecycle operation;
- all shared mounts are actually read-only in that mode; and
- intentional stop and unexpected container death produce different results.

### Slice 9: Clean up the top-level CLI vocabulary and references

**State:** complete; implemented and reviewed.

Adopt `runtime`, `bundle`, and `image` in `reploy info`; color validation
pass/fail results; provide the focused likely-local-file reference diagnostic;
and remove the redundant public `--docker` option from parsing, help, examples,
and tests.

**Primary findings:**

- `Validate result presentation`;
- `Likely local blueprint path treated as an index shorthand`;
- “`info` exposes internal target and build terminology”; and
- “Remove the redundant `--docker` option”.

**Verification gate:**

- golden tests cover validation success/failure with and without color;
- a path-like reference always gets the focused local-file forms while a true
  shorthand error remains concise;
- `info` uses only the approved public vocabulary by default; and
- no public help, parser, generated control usage, or documentation retains
  `--docker`.

### Slice 10: Finish override-editor interaction and workspace selection

**State:** complete; implemented and reviewed.

Bring the native editor to the interaction quality demonstrated by the
prototype: clear visual hierarchy, arrow-key navigation, source-specific
fields, visible validity, explicit dependency styling, and a discoverable
workspace-root choice during local-project selection. Preserve absolute paths
as an escape hatch and load existing overrides faithfully.

**Primary findings:**

- the general interaction-design portion of
  `Development override editor usability and dependency coverage`; and
- `Workspace-root selection is too hidden`.

**Verification gate:**

- screen-model/golden tests cover explicit dependencies, local/upstream source
  selection, invalid values, existing overrides, workspace-root changes, and
  absolute paths;
- keyboard tests cover arrows, search, selection, escape, and double-Ctrl-C;
- narrow terminal behavior remains usable; and
- a hands-on run reproduces the reviewed prototype flow without Python tooling.

### Slice 11: Define and implement override dependency discovery

**State:** complete; designed, implemented, and reviewed.

Define when transitive package rows become available, how they are sourced
before and after the first build, how dynamic Python metadata is handled, and
how provider-neutral overrides coexist with provider-specific discovery.
Explicit blueprint dependencies remain first and visually distinct.

**Primary finding:** `Transitive dependencies are absent`.

**Decision gate before implementation:**

- choose whether pre-build transitive rows come from local project metadata,
  an explicit metadata preparation operation, only a prior build lock, or a
  defined combination; and
- define failure, refresh, and offline behavior so opening the editor does not
  unexpectedly perform a package resolution or build.

**Verification gate after approval:**

- the OmegaConf Inspector local override exposes `omegaconf`;
- explicit and discovered dependencies remain distinguishable;
- first-build, prior-lock, stale-lock, offline, and dynamic-metadata cases are
  deterministic; and
- non-Python providers use the same override model without fabricated
  provider-specific behavior.

### Slice 12: Make Python packaging define local-source contents

**State:** complete; implemented and reviewed.

Replace generic pre-build directory snapshots with an isolated sdist-first
pipeline: the declared Python build backend produces an sdist, Reploy validates
and retains it, and the wheel is built from that retained artifact. Keep the
escaping-symlink safety boundary for artifacts Reploy consumes. Do not add VCS
metadata support in v1; projects requiring it fail clearly and may receive an
explicit opt-in later.

**Primary findings:**

- `Local source snapshots include generated virtual environments`; and
- `Python packaging metadata should define the package boundary`.

**Approved implementation contract:**

- Reploy observes and copies an immutable projection containing every selected
  local-source entry except VCS metadata. Generated environments, caches, and
  other development files remain visible to the declared build backend; the
  backend alone decides whether they enter the sdist.
- The projection is exposed read-only only inside the existing disposable
  resolver container. The container retains its read-only root filesystem,
  private tmpfs scratch, closed environment, pinned interpreter and `uv`
  frontend, default build network, and no host credentials or Docker socket.
- Reploy builds exactly one sdist first with portable `uv --no-sources`
  semantics. Failure to produce an sdist is a v1 error; there is no direct-wheel
  or generic-tree fallback. Legacy `setup.py` projects remain supported through
  the frontend's setuptools compatibility when they can produce a valid sdist.
- Reploy validates the sdist before publication or wheel construction: it must
  be a bounded regular `.tar.gz` archive with one normalized package root,
  root `PKG-INFO`, either `pyproject.toml` or legacy `setup.py`, unique safe
  relative paths, ordinary files and directories, and only internal
  non-escaping symbolic links. Absolute
  paths, traversal, hard links, special files, malformed metadata, and archive
  bombs are rejected.
- The validated sdist is retained in the provider store, extracted by
  Reploy-owned Go code into a fresh immutable input, and the wheel is built only
  from that retained artifact. VCS metadata is unavailable in v1; a backend
  that requires it fails with an actionable local-source diagnostic.
- Resolved source identity records the full source-input digest as a freshness
  and TOCTOU key, the retained sdist digest, the resulting wheel digest, the
  exact build-environment digest, builder profile, build settings, and ecosystem
  metadata. Exact input reuse requires both retained artifacts. If source
  inputs change but produce the same retained sdist under the same selected
  platform, immutable upstream image, and interpreter evidence, the existing
  wheel may be reused through that sdist identity.
- Broader cross-run build-environment caching and pre-resolved offline build
  dependencies remain separate post-v1 work.

**Verification gate after approval:**

- `pyproject.toml` and legacy `setup.py` projects build through their declared
  backend;
- unrelated virtual environments, including `.venv-demo`, do not enter the
  retained package artifact;
- wheel reuse is keyed through the retained sdist;
- escaping links and malformed archives fail with concise local-source
  diagnostics; and
- packages requiring unavailable VCS metadata get an actionable error.

### Slice 13: Add a self-contained staged control surface

**State:** complete; implemented and reviewed.

The app-named control command now appears at stage time. It locates the
stage-owned runtime at `.reploy/bin/reploy` relative to the command itself, so
moving the complete staging directory does not introduce an external executable
or workspace dependency. Each user-managed staging directory owns one runtime
copy; `stage --update` refreshes it when Reploy changes.

**Primary finding:** `Staging does not contain the environment control script`.

**Implemented ownership and lifecycle:**

- stage creates the app-named wrapper and embedded runtime without building;
- a private canonical manifest tracks generated paths and content identities;
- update refreshes changed generated files and removes an obsolete renamed
  wrapper only while it still matches the managed identity;
- build cleanup preserves the control surface because it is staging
  control-plane state, not a build artifact;
- uninstall without directory removal converts the retained installed command
  back to the relocatable staged form; and
- direct install's private temporary staging workspace skips this user-facing
  surface.

**Verification evidence:**

- a moved staging directory retains a working app-named control command;
- no external workspace path is required;
- staged and installed help share the intended command model; and
- stage, rebuild, and cleanup update or remove the surface predictably; and
- `nox -s cli-integration` passes the complete staged runtime and persistent
  install lifecycle against Docker.

### Slice 14: Simplify the OmegaConf Inspector demo and walkthrough

**State:** complete; implemented and reviewed after the staged control surface
and app-owned readiness contract settled.

Remove project-inspection commands from the blueprint and Python CLI, keep only
deployment-relevant commands, update the local flow to build before app
commands, and revise the walkthrough around the actual staged and installed
control surfaces. Start documentation with the smallest useful blueprint and
explain optional nodes only when introduced.

**Primary finding:** `Remove project-inspection commands from the demo`.

**Verification gate:**

- CLI and blueprint tests contain no `project list` or `project show`;
- the documented stage-to-install walkthrough runs successfully from a clean
  directory;
- the web UI remains the sole project-management interface; and
- every documented command is exercised by an acceptance test or smoke script.

**Verification evidence:**

- the resolved demo blueprint exposes only `serve`, `config init`,
  `config check`, `config show`, and `version`, with conservative installed
  exposure;
- the built Python CLI and staged app command reject the removed `project`
  surface;
- the local blueprint completed a clean Docker-backed build, staged config and
  readiness lifecycle, user install, installed config/status/logs lifecycle,
  stop, and uninstall; and
- the public website build passes with the rewritten minimal-to-complete
  walkthrough.

### Slice 15: Restore metadata-private workload environment injection

**State:** complete; implemented and reviewed.

Treat a deployment-root `.env` as operator-owned runtime configuration rather
than blueprint or image configuration. Validate the host file defensively,
keep it outside every Docker and Reploy persistence surface, and inject its
assignments only after container creation through a fixed one-shot workload
launcher channel.

**Verification gate:**

- POSIX permissions and Windows ACLs limit the file to its deployment owner
  and supported administrative identities;
- symbolic or hard links, replacement races, invalid assignment syntax,
  ancestor bind mounts, and autonomous Docker restart policies fail before
  workload creation;
- Compose, image/container metadata, commands, state, locks, and
  Reploy-generated diagnostics contain neither the host file nor its variable
  names or values;
- first install copies the file defensively, reinstall preserves it, explicit
  `.env` replacement updates it, and a missing replacement source never
  deletes the installed copy; and
- focused tests plus a real-Docker test prove that the workload receives the
  value while `docker inspect` does not.

## Coverage and dependency summary

Every detailed finding below has exactly one primary slice above. Slices 1-15
and the prerequisite health-check redesign are complete. The broader cross-run
build-cache design remains post-v1 and was not a prerequisite.

## Fresh end-to-end review

The post-implementation review confirmed that the original 14 findings remain
resolved. It also exercised the project-owned local build recipe for OmegaConf:
overriding OmegaConf to its local source discovered `tool:java`, prepared the
portable Java tool layer, built the setuptools-legacy artifact, and completed
the OmegaConf Inspector image without relying on host Java.

The review found two new P1 follow-ups. Both are transferred to the active
backlog rather than reopening the completed implementation slices.

### Runtime-only blueprint updates reuse an obsolete build lock

**State:** resolved and verified on 2026-07-27.

Reproduction:

1. stage and build the OmegaConf Inspector demo;
2. change only `docker.workload.endpoints.http.publish.staging`;
3. run `reploy stage --update` with the changed blueprint;
4. run `reploy build`; and
5. run the staged app command's `up`.

Observed result:

- build reports `reusing current validated image` and
  `environment already current`; then
- `up` fails with `runtime build is missing or stale; run reploy build`.

The runtime check is correct: staged state contains the new blueprint while the
reused lock still contains the old blueprint digest. The defect is the reuse
path returning that old generation without publishing a new lock/state binding
for the unchanged image and provider closure.

The build reuse path now rebinds the unchanged validated image and provider
closure to the desired blueprint digest and publishes a new recoverable
generation. Policy-changing inputs still take the validated build path. A real
Docker acceptance changed only the staged HTTP publication port, retained the
exact image digest, completed the reuse path in 2.4 seconds, started the
workload on the new port, passed its health probe, and stopped it cleanly.

### Expected APT resolver mechanics leak as user warnings

**State:** resolved and verified on 2026-07-27.

The successful local OmegaConf build ended with:

```text
reploy warning: APT: Not using locking for read only lock file /var/lib/dpkg/lock-frontend
reploy warning: APT: Not using locking for read only lock file /var/lib/dpkg/lock
```

These warnings are emitted by APT while Reploy intentionally performs
read-only dependency planning. They identify no blueprint, package, security,
or recovery problem. Reploy currently promotes every `W:` line from successful
APT work unless its exact text appears in a version-specific allowlist; that
allowlist recognizes older permission-denied lock messages but not these newer
forms.

The resolver now sets APT's `Debug::NoLocking=1` option for its non-installing,
read-only package planning and download operations. Warning parsing was not
broadened. A clean real Docker build of local OmegaConf through its
project-owned `tool:java` requirement completed in 2m36s without either lock
warning while retaining ordinary APT diagnostics.

### Follow-up redesign review

**State:** complete on 2026-07-27.

The next implementation queue completed the metadata-private workload
environment channel, separated application ownership from physical provider
nodes, and documented and tested the smallest base-only blueprint. The review
confirmed that runtime-only blueprint changes republish a current generation,
APT's read-only resolver remains warning-free without weakening diagnostics,
canonical application contribution identities survive shared OS
materialization, and optional environment nodes do not invent defaults for
identity, platform support, or the base image.

The private workload environment review applied the stronger invariant that
neither variable names nor values may enter Docker image/container metadata,
labels, command arguments, mounts, Compose environment content, Reploy state,
locks, or diagnostics. The one-shot stdin relay satisfies that boundary, and a
real-Docker test verifies both workload delivery and absence from
`docker inspect`.

The review found one additional isolation defect on the immediate user-install
startup path: its planning check compared lexical mount paths, but it did not
repeat the symlink-resolving check after installed bind sources existed. A bind
source symlinked to the installed deployment directory could therefore expose
the host `.env` on that first start. User-install startup now resolves and
revalidates every realized bind source before container creation, matching
staged starts and system-service starts. A focused regression test proves that
the start is rejected without exposing the private variable name or value.

## Detailed evaluation evidence

The sections below preserve the observations as recorded during the
walkthrough. Descriptions of “current” behavior refer to the evaluated pre-fix
state, not the current repository.

## Validate result presentation

Command:

```text
reploy validate ./examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml
```

Current result:

```text
valid blueprint: omegaconf-inspector (syntax and semantics)
```

Preferred result:

```text
pass: omegaconf-inspector (syntax and semantics)
```

Show successful validation in green and failed validation in red.

## Likely local blueprint path treated as an index shorthand

Command:

```text
reploy stage examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml
```

The missing explicit local-file prefix causes Reploy to treat the argument as
an index shorthand and print a wall of general help text.

When an argument looks like a local path, show a focused error:

- Explain that no such item exists in the blueprint index.
- Explain that the argument looks like an intended local blueprint file.
- Suggest explicit local-file forms:

  ```text
  ./examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml
  file://examples/omegaconf-inspector/reploy/omegaconf-inspector.blueprint.yaml
  ```

Show this focused explanation regardless of whether a matching index shorthand
exists. Reserve the full blueprint-reference explanation for `--help`.

## `info` exposes internal target and build terminology

Command:

```text
reploy info
```

Current output includes:

```text
target: docker
resolved: not built
materialized image: not built
```

Meaning in the current implementation:

- `target` is the selected deployment backend, not the environment platform.
- `resolved` means that exact provider dependencies and artifacts were selected
  and recorded in a build lock.
- `materialized image` is the Docker image produced from those resolved
  provider bundles.

The current labels expose implementation terminology and make the unbuilt
state harder to understand. Preferred operator-facing direction:

```text
runtime: docker
bundle: not built
image: not built
```

During triage, decide whether `backend` is clearer than `runtime`, and whether
`bundle` conflicts with the existing pre-build bundle/request terminology.
Internal details such as the build-lock digest can be reserved for verbose
output.

## Development override editor usability and dependency coverage

The native override editor is substantially less polished than the earlier
Python TUI prototype. Review its layout, visual hierarchy, navigation, and
discoverability as a focused interaction-design task rather than a collection
of isolated styling fixes.

### Transitive dependencies are absent

The editor showed the explicit `omegaconf-inspector` requirement but did not
show `omegaconf`.

Current behavior:

- The editor lists package roots declared directly by the blueprint, selected
  options, direct package additions, and override-only rows.
- `omegaconf` is declared transitively in the selected local project's
  `pyproject.toml`.
- The editor does not inspect a selected local project's dependency graph or
  load transitive dependencies from a prior build lock.

This does not match the expectation that explicit dependencies appear first
with distinct styling while other discovered dependencies remain available for
overrides. Triage when and how transitive dependencies become available,
including the behavior before the first build.

### Workspace-root selection is too hidden

The selected local source was stored as:

```yaml
path: /home/omry/dev/reploy/examples/omegaconf-inspector
```

This is expected when the optional workspace root is unset. The editor exposes
workspace configuration only through the `W workspace` shortcut in its footer,
which was not clear during the normal local-project selection flow.

The UI should make the common-root choice explicit and discoverable when
selecting a local project. With `/home/omry/dev` as the workspace root, the
stored path would instead be:

```yaml
path: "{{ workspace_root }}/reploy/examples/omegaconf-inspector"
```

Retain absolute paths as an escape hatch for projects outside the configured
workspace.

## Historical finding: local source snapshots included generated virtual environments

Before slice 12, building the locally overridden OmegaConf Inspector failed
with:

```text
reploy build error: execute provider graph: prepare provider node "python/application": fresh Python resolution failed: local Python override "omegaconf-inspector" source manifest: source symlink ".venv-demo/bin/python": resolved path "/usr/bin/python3.12" escapes workspace root
```

At that time, the final safety check was valid: a source snapshot could not
follow or include a symlink that resolved outside the selected source
directory. The failure occurred because generated development state entered
the source manifest:

- The selected project contains `.venv-demo`.
- The source scanner ignores `.venv` exactly, along with a fixed set of other
  generated directory names.
- It does not ignore `.venv-demo` and does not honor Git ignore rules.
- The virtual environment's Python symlink resolves to the system interpreter,
  correctly triggering the escape check.

Slice 12 replaced that generic snapshot boundary with a complete provisional
input (excluding VCS metadata), followed by backend-defined sdist creation and
Reploy-owned sdist validation. Generated development state may be visible to
the backend but does not enter the retained artifact unless the project's
packaging metadata selects it.

That historical diagnostic also exposed excessive internal context and used
the ambiguous phrase `workspace root`. Slice 12 now reports source-input,
sdist-build, and retained-artifact failures at their actual boundary, including
an explicit correction when a backend cannot build without VCS metadata.

### Historical finding: Python packaging metadata should define the package boundary

The pre-slice-12 procedure snapshotted a generically filtered directory tree
first and only then ran `uv build`, where the project's `pyproject.toml`,
`setup.py`, and selected build backend take effect. This means Reploy's fixed
filesystem ignore list decides the candidate inputs before the Python packaging
system can apply its own inclusion rules.

Slice 12 made the Python build backend authoritative for package contents with
the isolated sdist-first flow:

1. Ask the declared PEP 517 backend to build an sdist from the local project.
2. Validate and retain that closed source artifact.
3. Build the wheel from the retained sdist.
4. Key reuse to the retained source artifact and resulting wheel.

This supports both `pyproject.toml` and legacy `setup.py` projects through the
build frontend and prevents unrelated development directories from becoming
package inputs when the backend excludes them.
Projects that cannot produce a valid sdist fail explicitly. Untracked files are
visible to the backend, while VCS metadata is deliberately unavailable in v1.

## Build progress starts too late

`reploy build` can spend a substantial amount of time doing local-source
observation, source-wheel construction, dependency resolution, and provider
preparation before printing anything. In the captured run, the first visible
output was the later Docker materialization build:

```text
[+] Building 5.6s (10/10) FINISHED
```

Print an immediate build-start message and operator-facing phase progress for
the work Reploy performs before Docker emits output. The default view should
make it clear whether Reploy is observing local sources, building source
artifacts, resolving dependencies, assembling an image, or validating the
result, without exposing provider-graph internals.

## Aggregate build warnings at command completion

The build invokes Docker more than once. Each internal Docker build currently
prints its own warnings inline, including repeated warnings:

```text
InvalidDefaultArgInFrom
SecretsUsedInArgOrEnv
```

Collect warnings from internal build steps, deduplicate them, and present the
warning summary once at the end of the Reploy command. Keep the normal output
focused; detailed Docker transcripts and expanded warning context can be
available through `--verbose`.

During triage, distinguish actionable warnings caused by generated Reploy
Dockerfiles from warnings inherited from or triggered by the selected base
image. Do not make recurring internal warnings look like application failures.

The Docker-generated footer is not an acceptable user instruction:

```text
1 warning found (use docker --debug to expand)
```

The user invoked Reploy, not Docker, and has neither the hidden Docker command
nor the generated Dockerfile needed to follow that advice. Reploy must own
diagnostics from tools it invokes internally. It should capture the expanded
warning context during the current operation and then either:

- explain the warning and the relevant user action in Reploy terms;
- identify it as an internal Reploy warning with no user action; or
- suppress it after Reploy fixes the generated input that causes it.

Do not forward instructions that require users to reconstruct or rerun hidden
internal Docker operations.

### Build has no explicit final Reploy outcome

After the internal Docker output and warnings finish, `reploy build` returns
without printing a Reploy-level completion line. The user must infer success
from the shell prompt and exit status, and the last visible text may be a
warning.

Every foreground operation must finish with an unmistakable Reploy outcome.
For a successful build, report at least:

- explicit success;
- the environment that was built;
- the resulting image or other useful build identity; and
- elapsed time when the operation is long-running.

Failures should end with an equally clear failure summary after the primary
diagnostic. The final line must describe the Reploy operation, not merely the
last internal Docker step.

A repeated `reploy build` against the completed unchanged staging directory
returned successfully without printing anything. A cache hit or no-op is still
an operation outcome and must be explicit. Report that the environment is
already up to date, identify the reused build/image, and finish with the same
clear success convention as a fresh build.

### Default build output must use Reploy concepts

Raw Docker and BuildKit progress is an implementation detail and should not
appear in normal `reploy build` output. Present a coherent Reploy-owned progress
view using concepts visible in the blueprint, such as:

- preparing the selected environment and platform;
- resolving a named component's packages;
- building a named local package source;
- preparing a named component layer;
- assembling the environment image;
- validating component layers when requested;
- validating the final image; and
- publishing the completed staged build.

Progress should identify blueprint component names and provider types where
useful, without exposing generated Dockerfiles, internal image tags, provider
graph node IDs, or BuildKit step output.

`--verbose` may add detailed Reploy-level operations. If raw backend diagnostics
are retained at all, reserve them for an explicitly diagnostic mode rather than
normal or merely verbose operation. Reploy must still translate the primary
failure and warnings into its own terminology.

## Staging does not contain the environment control script

The evaluated pre-fix staging directory contained only private `.reploy/` state
and `overrides.yaml`; it did not contain the environment's generated command
surface.

Resolved implementation:

- `reploy stage` generates the app-named command and a stage-owned Reploy
  runtime before any environment build.
- The command resolves both its staging directory and runtime relative to its
  own location, so the complete directory can be moved.
- The embedded control runtime distinguishes staged and deployed state:
  staging exposes all native app commands, while installed control remains
  limited to commands declared for deployment.
- `reploy stage --update` refreshes the runtime and wrapper and safely removes
  a renamed managed wrapper.

General staging management such as `stage --update`, build customization,
doctor, and install remains exclusive to the main `reploy` CLI.

## Transient shell home is not writable by the runtime user

In `reploy shell` for the staged OmegaConf Inspector environment, both a file
in the initial working directory and a file under `$HOME` failed:

```text
touch: cannot touch 'aaaa': Permission denied
/bin/sh: cannot create /mnt/reploy-home/reploy-shell-proof: Permission denied
```

The first failure can be expected because the container root filesystem is
read-only. The `$HOME` failure is not expected. Reploy creates a fresh anonymous
Docker volume at `/mnt/reploy-home`, then starts the container directly as the
configured non-root runtime user. A newly created volume retains root ownership,
and the current transient-container path does not initialize its ownership or
permissions before starting the shell.

This violates the documented contract that the transient `$HOME` is an
operation-local writable workspace. It also affects one-shot app commands and
lifecycle actions because they use the same transient-container mechanism.

The container itself is still transient: it is created with `--rm`, registered
as a live shell run while active, and removed when the shell exits. Until the
home ownership bug is fixed, container removal can be verified using the live
run list and Docker container identity, but filesystem transience cannot be
demonstrated by writing to `$HOME`.

## Status exposes raw Docker Compose output

After starting the staged environment, `reploy status` prints the raw Docker
Compose process table:

```text
[STAGING : omegaconf-inspector] NAME                                   IMAGE                                                                        COMMAND                  SERVICE       CREATED         STATUS         PORTS
[STAGING : omegaconf-inspector] omegaconf-inspector-staging-13abcebe   reploy/env/omegaconf-inspector-13abcebe:g-1a88a7104084025675020af1ee3cca3b   "/opt/reploy/provide…"   environment   5 seconds ago   Up 4 seconds   127.0.0.1:18076->8076/tcp
```

This exposes backend implementation details instead of reporting the state of
the Reploy environment. Normal status output should use Reploy concepts and
answer whether the environment is running, which phase it belongs to, how long
it has been running, and how to reach its published endpoints. It should not
normally expose generated container names, internal image tags, truncated
container commands, Compose service names, or Docker port-mapping syntax.

A suitable shape for this environment would be approximately:

```text
[STAGING : omegaconf-inspector] status: running
[STAGING : omegaconf-inspector] started: 5 seconds ago
[STAGING : omegaconf-inspector] endpoint http: http://127.0.0.1:18076
```

Backend-specific process details may remain available through an explicitly
diagnostic interface, but should not define the default command output.

The generated installed control script has the same defect:

```text
[DEPLOYED : omegaconf-inspector] NAME ... IMAGE ... COMMAND ... SERVICE ... STATUS ... PORTS
```

The normalized status contract must be shared by the staging CLI and installed
control script; deployment phase changes the values, not the output model.

## Remove the redundant `--docker` option

The public `--docker` option does not provide a meaningful choice while Docker
is Reploy's only supported runtime backend. It adds syntax and exposes backend
selection prematurely without changing the operation's behavior.

Remove the option for now and use Docker implicitly. If Reploy later supports
another backend, introduce an explicit backend-selection interface based on the
actual choices and their requirements at that time.

## Every staged app command enters the automatic-build path

Running an already-built command twice:

```text
reploy app version
prepare current build
[STAGING : omegaconf-inspector] omegaconf-inspector 0.1.0
```

causes both invocations to enter the complete automatic provider-build path.
This applies to every staged app command; `version` is not treated specially.

For an unchanged environment, exact reuse prevents resolver execution, image
construction, and final validation from running again. However, deciding that
reuse is valid still observes selected local sources, reloads the resolved build
request, selects and inspects the base image, validates current build metadata
and the provider store, and compares the resulting inputs. This can add
noticeable latency to a cheap command.

The progress message is also misleading: `prepare current build` does not say
whether Reploy is checking, reusing, or actually rebuilding anything.

App commands must not refresh the environment. They consume the already
published current generation and perform only the lightweight checks needed to
run that exact generation safely. They must not rescan local package sources,
resolve packages, select a base image, or enter the provider build pipeline.

If no current build exists, `reploy app` should fail with a concise instruction
to run `reploy build`; it should not build implicitly. Environment refresh
belongs only to commands whose contract explicitly includes it. This remains
separate from the previously approved automatic build behavior for `reploy up`.
Normal output for those refresh-capable commands must distinguish checking,
reuse, and actual build work.

## Remove project-inspection commands from the demo

The evaluated pre-fix OmegaConf Inspector blueprint exposed:

```text
reploy app project list
reploy app project show -- PROJECT_ID
```

The second command requires first obtaining an opaque project ID from the first,
and its positional argument must follow Reploy's `--` forwarding separator.
Invoking `reploy app project show` without that ID merely reaches the
application's argparse usage error.

These commands are not needed for the demo's user journey. Projects are created,
selected, edited, and inspected through the web application, so the CLI
duplicates part of that interface in a less convenient form. Demonstrating
positional argument forwarding is not sufficient reason to expose a command
that has no natural deployment-management use.

Resolved implementation:

- the blueprint and standalone Python CLI no longer expose `project list` or
  `project show`; project workflows remain in the web UI;
- the internal `serve` workload command remains;
- `config init` remains staging-only;
- `config check`, `config show`, and `version` remain staged and deployed; and
- public walkthroughs use the app-named staged and installed control commands.

The local demo flow now builds before invoking `config init`:

```text
stage -> select overrides -> build -> config init -> config check -> up
```

## Logs have no Reploy record format or presentation

Reploy currently runs `docker logs --timestamps` for the selected immutable
container and forwards the result unchanged. Docker supplies a timestamp, while
the remainder of each line uses whatever format the application emitted.
OmegaConf Inspector uses Uvicorn's logging format; that is not a Reploy
contract.

Adding `colorlog` to the Python demo is not the appropriate general solution.
It would embed ANSI escape sequences in the application's stored Docker logs,
affecting redirected output, automation, and viewers that do not support color.
It would also solve the problem only for Python applications.

`reploy logs` should own terminal presentation and add color only when its
output is an interactive color-capable terminal. It can safely give timestamps
and Reploy-owned framing a consistent visual treatment while leaving redirected
output plain. Coloring by severity requires an explicit severity field from a
standardized or structured application-log contract; Reploy should not infer
levels from arbitrary message text or from stdout versus stderr.

For v1, standardize the Reploy-owned displayed envelope and color that envelope.
Preserve application messages verbatim. Defer severity-aware coloring until
Reploy defines an optional structured log input that applications can emit
without embedding presentation codes.

Do not force Docker's capture timestamp into default output. Applications often
emit their own event timestamp, in which case the current unconditional
`docker logs --timestamps` produces two timestamps. The application event time
and Docker capture time have different meanings, and Reploy cannot safely
recognize or remove an application timestamp from arbitrary text. Default
`reploy logs` to the original application lines and provide an explicit
`--timestamps` option when the user wants the runtime capture time.

When `--timestamps` is enabled on an interactive color-capable terminal, render
the Reploy-supplied timestamp as distinct date, time, and timezone fields. Use a
restrained treatment such as a dim neutral date, a brighter cyan time, and a
dim neutral timezone. Keep the exact same timestamp text without ANSI codes for
redirected output, `NO_COLOR`, and terminals without color support. Do not try
to recolor timestamp-like text inside the application message.

## `--wait` blocks without explaining why

When `reploy app --wait version` queues behind an active shell, it stops
producing output and appears hung. The queue behavior is correct, but the user
cannot see what is blocking the command, whether Reploy is still working, or how
to cancel the wait.

Every operation that enters the live FIFO queue must immediately print a
Reploy-level waiting message. It should identify the active run or operation,
state the relevant concurrency reason, report how many entries are ahead when
useful, and say that Ctrl-C cancels this waiter without affecting the active
run. For example:

```text
Waiting for active shell to finish (shared writable mounts: /conf, /data).
Ctrl-C cancels this wait.
```

With earlier waiters:

```text
Waiting behind 2 runs; the active shell has exclusive access to /conf and /data.
Ctrl-C cancels this wait.
```

The message should appear before blocking and should not expose internal queue
records or control-marker terminology.

## Add a read-only shell mode

The shell container already has a read-only image filesystem, but normal
`reploy shell` preserves the blueprint's writable access to shared mounts such
as `/conf` and `/data`. It therefore claims exclusive access under
`allow_concurrent: auto`.

Add:

```text
reploy shell --read-only
```

This mode mounts every shared environment path read-only regardless of the
blueprint's normal writable setting. Its operation-local temporary `$HOME`
remains private and writable after the separate home-ownership defect is fixed.
Because the shell cannot modify shared mounts, it is non-exclusive and can
overlap with other non-exclusive app commands and shells.

Read-only mode is not an unconditional queue bypass. It must still wait behind
an already-active exclusive writer and must obey lifecycle-operation admission.
Keep the existing writable shell as the default; the flag expresses the user's
explicit willingness to inspect without modifying shared state.

## An intentionally stopped shell reports a Docker failure

Stopping an active shell through `reploy runs stop RUN_ID` works, but the
terminal running that shell receives:

```text
reploy shell error: runtime container: run admitted transient container: docker failed: exit status 137
```

Exit status 137 is the backend result of force-removing the container. It is not
the user-level outcome. Reploy initiated the stop and can distinguish it from an
unexpected container death.

Translate an acknowledged `runs stop` into a concise Reploy result such as:

```text
[STAGING : omegaconf-inspector] shell stopped by `reploy runs stop`.
```

Do not print the nested runtime-container error or Docker exit status for this
case. The originating shell command should still return a stable Reploy
interruption status rather than pretending that the shell exited normally.
Unexpected container termination should remain an error and retain useful
diagnostic context.

## Successful install leaks internals and omits the installed result

Running:

```text
reploy install --scope user
```

successfully installed OmegaConf Inspector, but the visible output consisted of
raw JSON from the before/after lifecycle checks, Docker Compose network and
container progress, and:

```text
[STAGING : omegaconf-inspector] installing... done
```

The actual installed directory was:

```text
/home/omry/.local/share/Reploy/installs/omegaconf-inspector
```

and its generated control script was:

```text
/home/omry/.local/share/Reploy/installs/omegaconf-inspector/omegaconf-inspector
```

Neither appeared in the output. The blueprint's configured installation-success
line containing the deployed Inspector URL was also absent.

Normal installation output must present Reploy-owned progress. Successful
lifecycle actions should be summarized as named checks rather than dumping
their stdout; preserve that output for a failure or an explicitly diagnostic
view. Suppress normal Docker network/container progress just as for builds.

The final result must use the deployed phase and report at least:

- explicit installation success;
- installed directory;
- generated control command;
- running/stopped service state;
- published endpoint; and
- any blueprint-defined success lines.

For this installation, an appropriate result would be approximately:

```text
[DEPLOYED : omegaconf-inspector] installed successfully
[DEPLOYED : omegaconf-inspector] location: /home/omry/.local/share/Reploy/installs/omegaconf-inspector
[DEPLOYED : omegaconf-inspector] control: /home/omry/.local/share/Reploy/installs/omegaconf-inspector/omegaconf-inspector
[DEPLOYED : omegaconf-inspector] status: running
[DEPLOYED : omegaconf-inspector] inspector url: http://127.0.0.1:19076
```

### Reinstall is not presented as an update

Reinstalling the same staged environment correctly stopped and removed the
existing container, removed its network, ran pre-start validation, recreated
the workload, and ran the live check. The terminal showed all of those actions
as raw Docker progress and application JSON, then again ended with:

```text
[STAGING : omegaconf-inspector] installing... done
```

The output never said that Reploy found an existing installation or was
updating it. It also did not identify the preserved managed paths, announce the
start and end of service interruption in Reploy terms, or report the deployed
result.

A reinstall should use an update-oriented progression such as:

```text
[DEPLOYED : omegaconf-inspector] updating existing installation
[DEPLOYED : omegaconf-inspector] preserving: config, data
[DEPLOYED : omegaconf-inspector] stopping service... done
[DEPLOYED : omegaconf-inspector] installing new generation... done
[DEPLOYED : omegaconf-inspector] starting service... done
[DEPLOYED : omegaconf-inspector] validating service... passed
[DEPLOYED : omegaconf-inspector] update completed successfully
```

The final result should include the same location, control command, state, and
endpoint information as first install. Raw container and network operations
remain diagnostic details.

Both the pre-change plan and final result must explicitly classify state as
preserved, replaced, or reused. For the unchanged default reinstall, that would
look approximately like:

```text
Preserved:
  config
  data

Replaced:
  service instance
  deployment files

Reused:
  environment image
```

The classification must reflect the actual operation. For example,
`--replace config` moves `config` from preserved to replaced, `--clean` replaces
all managed paths, and installing a newly built generation replaces rather than
reuses the environment image. Include resolved host paths when they help the
user identify or recover their data.

## Repeated completed uninstall incorrectly requires root

The first user-scope uninstall with `--remove-dir` succeeded: the installed
directory was removed and the deployed endpoint stopped responding. Repeating
the exact command then produced:

```text
reploy uninstall error: root privileges are required to stop systemd services and remove Docker resources
rerun with sudo
```

This is incorrect. The root preflight treats any missing or unreadable
installation state as if it might be a system-scope deployment. It therefore
rejects the operation before the uninstall path can report that no installation
exists.

A repeated completed uninstall must be idempotent. When an explicit `--from`
directory is absent and no recovery service name was supplied, report a
successful no-op:

```text
No installation found at PATH; it may already have been removed.
```

Do not request root merely because state is absent. Recovery of a service whose
installation directory was unexpectedly deleted is a different, explicit
operation: require `--service-name` and determine privileges from the recovered
service definition.

The successful uninstall output also needs a Reploy-owned result summary. The
observed output:

```text
[DEPLOYED : omegaconf-inspector] uninstalled service: omegaconf-inspector
[DEPLOYED : omegaconf-inspector] uninstalling... done
```

is also ordered incorrectly: it announces that the service was uninstalled
before completing the `uninstalling... done` progress line. Progress must finish
before any final result is emitted; the command must have one clear completion
boundary rather than appearing to complete twice in reverse order.

The output also does not say that Docker resources and the installation
directory were removed. Report removed and retained resources explicitly,
including managed data, before claiming completion.

### Install and uninstall need one shared progress model

For an interactive terminal, both installation/update and uninstall should use
one Reploy-owned spinner whose label advances through meaningful logical steps.
For example, update may move through planning managed paths, stopping the
service, installing the generation, starting the service, and validating it;
uninstall may move through stopping the service, removing runtime resources,
and removing or retaining the installation directory.

Do not print a spinner plus successful step output plus backend progress. The
spinner owns in-progress presentation, then clears or completes before one final
result summary is printed.

With `--verbose`, replace the animated spinner with durable Reploy-level
per-step lines. Non-interactive output and dumb terminals such as `TERM=dumb`
must also use stable plain lines rather than terminal animation, cursor
control, rewritten output, or color. Backend commands and raw Docker output
remain hidden unless a separately explicit diagnostic interface is selected;
`--verbose` means more Reploy detail, not unfiltered backend output.

On failure, stop the spinner on the named failed step, retain that step in the
terminal, and then print the translated diagnostic and recovery action.
