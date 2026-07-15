---
status: Active
updated: 2026-07-15
summary: Evidence-driven implementation plan for the blueprint environment and workload model.
implements: docs/BLUEPRINT_ENVIRONMENT_MODEL.md
---

# Blueprint Environment Model Implementation Plan

This document defines the work and evidence behind
`BLUEPRINT_ENVIRONMENT_IMPLEMENTATION.awd`. The environment model is normative;
this plan may choose implementation structure but must not invent public schema.

## Scope and Compatibility

Replace the existing unreleased schema-1 shape with the schema-1 environment
model. Do not add a compatibility loader or automatic migration for existing
development installations.

Initial scope:

- Blueprint-level platform compatibility and one required `base` root component
  containing the starting OCI image and any explicitly declared outputs.
- Python components, component-scoped options, and explicit development
  translations.
- Docker with a BuildKit-generated Python image.
- At most one persistent service workload.
- Native one-shot commands and built-in `reploy shell`.
- Managed binds, named volumes, external unmanaged binds, and tmpfs mounts.
- Staged and installed phases; user/system install scopes only where supported.
- HTTP(S) startup readiness and the documented install/runtime events.

Keep the model's private backlog out of the public schema. Existing Reploy CLI
and deployment capabilities remain unless the model explicitly records a
conflict. The intentional changes are the new schema, direct control-script
default (`environment.id`, without `ctl`), generated-image materialization, and
removal of private health/success-variable protocols.

## Implementation Principles

- Keep blueprint reference/artifact acquisition separate from schema decoding.
- Add the resolved model beside legacy code; cut callers over only after the
  replacement path passes its gate.
- Resolve once into typed environment and Docker execution plans. Compose,
  commands, lifecycle, install, status, dry-run, and cleanup consume those plans.
- Keep generated bundle metadata, build definitions, layer graphs, and image
  identities private.
- Pass application invocations as argv arrays. Never construct shell command
  text from blueprint or user arguments.
- Preserve unrelated user changes and existing functionality in the dirty
  worktree. Remove legacy code only after caller and test coverage is proven.
- Use table tests for validation matrices, golden tests for resolved/rendered
  plans, fake Docker runners for command construction, and focused real-Docker
  tests for behavior fakes cannot establish.

## Implementation Flow

```mermaid
flowchart TD
    P0["Phase 0<br/>Baseline and contract coverage"]
    G0{{"Baseline contract passes"}}
    P1["Phase 1<br/>Schema, validation, and lazy resolution"]
    G1{{"Schema resolution gate passes"}}
    P2["Phase 2<br/>Component and provider contract"]
    G2{{"Python provider gate passes"}}

    P3A["Phase 3 prototype<br/>BuildKit, identities, reuse, recovery, cleanup"]
    R3{"Architecture and safety<br/>evidence approved?"}
    P3B["Complete Python image materialization"]
    G3{{"Generated image gate passes"}}

    P4["Phase 4<br/>Resolved Docker execution plan"]
    G4{{"Docker execution plan gate passes"}}
    P5["Phase 5<br/>Commands and interactive shell"]
    G5{{"Command and shell gate passes"}}
    P6["Phase 6<br/>Install and workload runtime events"]
    G6{{"Lifecycle and readiness gate passes"}}
    P7["Phase 7<br/>Cutover and legacy removal"]
    A7{"Arbiter checkout available?"}
    A7Y["Migrate and exercise Arbiter"]
    A7N["Record external validation as deferred"]
    G7{{"Cutover gate passes"}}

    F0["Format and focused changed-package tests"]
    GF{{"Focused verification passes"}}
    T1["Full Go tests"]
    T2["CLI Docker integration smoke"]
    T3["Release build and docs build"]
    INSPECT["Inspect artifacts, Sapling diff,<br/>release note, and safety properties"]
    GV{{"Final verification passes"}}
    REVIEW{"Final implementation matches<br/>the normative model?"}
    COMMIT["Commit implementation"]

    P0 --> G0 --> P1 --> G1 --> P2 --> G2 --> P3A --> R3
    R3 -- "revise" --> P3A
    R3 -- "approved" --> P3B --> G3 --> P4 --> G4 --> P5 --> G5
    G5 --> P6 --> G6 --> P7 --> A7
    A7 -- "yes" --> A7Y --> G7
    A7 -- "no" --> A7N --> G7
    G7 --> F0 --> GF
    GF --> T1
    GF --> T2
    GF --> T3
    T1 --> INSPECT
    T2 --> INSPECT
    T3 --> INSPECT
    INSPECT --> GV --> REVIEW
    REVIEW -- "changes needed" --> P7
    REVIEW -- "approved" --> COMMIT

    classDef phase fill:#e8f1ff,stroke:#2563eb,color:#172554;
    classDef gate fill:#ecfdf5,stroke:#059669,color:#064e3b;
    classDef decision fill:#fff7ed,stroke:#ea580c,color:#7c2d12;
    class P0,P1,P2,P3A,P3B,P4,P5,P6,P7,F0,T1,T2,T3,INSPECT,A7Y,A7N,COMMIT phase;
    class G0,G1,G2,G3,G4,G5,G6,G7,GF,GV gate;
    class R3,A7,REVIEW decision;
```

## Phase 0: Baseline and Contract Coverage

Inventory and map:

- `internal/deploy/pack.go`: schema, defaults, install locations, commands.
- `internal/providers/python`: roots, resolution, executable discovery.
- `internal/dockerdeploy`: bundle, runtime volume, Compose, paths, ports,
  lifecycle, state, install/update, control scripts, commands, cleanup.
- `internal/cli`: stage/install/bundle/app/runtime/shell-facing parsing and I/O.
- Smoke, git-source, OmegaConf demo, and external Arbiter blueprints.

Build a replacement table from every retained legacy surface to its new model,
explicit removal, or backend-private equivalent. At minimum account for:

- bundle options/add/remove/list/prepare/check/upgrade, including explicit
  removal of prepared-wheelhouse-only commands rather than compatibility
  aliases;
- managed-path preserve/replace and `--replace`/`--clean`;
- single and named `--port` overrides;
- user/system ownership and cross-platform install targets;
- one-shot stdout/stderr/exit propagation and command matching;
- staged/installed state and installed scope persistence.

Gate: focused parser, provider, Docker config/runtime/install, and CLI tests pass.

## Phase 1: Schema, Validation, and Lazy Resolution

Create `internal/blueprint` (or an equivalently focused package) with raw
decoding internal and a typed resolved document public to callers.

Implement:

- Metadata and reserved interpolation roots: `blueprint`, `environment`,
  `docker`, `reploy`, `user`, `system`.
- Portable `environment.id`; optional `control_script` defaults directly to ID.
  Reject unsafe/reserved filenames and native-trigger collisions with control
  operations.
- Blueprint compatibility; the required `components.base` root; vars,
  translations, provider components and component-scoped options, paths,
  executables, commands, optional workload, `workload.runtime`, install, and
  Docker runtime nodes including `additional_mount_roots`.
- Install target defaults, semantic host variables, `system.run_as`, success
  lines, and current platform/scope validation.
- Strict unknown-field rejection and explicit rejection of legacy top-level
  shapes after cutover.

Resolution order:

1. Decode and structurally validate while retaining expressions.
2. Resolve global-variable dependencies; reject missing names and cycles.
3. Resolve prototype `extends` only from environment path to Docker mount and
   environment endpoint to Docker endpoint; reject field replacement/cross-kind
   references.
4. Resolve `user.*`/`system.*` from the active host/install context.
5. Resolve `reploy.phase: staged|installed`; expose `reploy.scope: user|system`
   only for installed environments. There is no system staging.
6. Resolve `reploy.workload.*` after the Docker plan has effective bind and
   publication values.
7. Resolve install-success lines during install, then type-check consumed fields.

Validation includes ports/durations, readiness paths, path/mount combinations,
component/output references, command order and triggers, install target keys,
and disabled component options contributing no requirements or outputs.
Missing referenced outputs fail at resolution/materialization or runtime
preflight if installed state drifted.

Tests:

- Validation table for every rule and legacy rejection.
- Var chains/cycles and phase/scope/host/workload availability.
- Allowed/rejected `extends` and lazy copied expressions.
- Golden resolved Arbiter-shaped document.

## Phase 2: Component and Provider Contract

Define a provider contract that:

- **P2-01:** represents the required `base` component as the graph root, validates its
  explicitly declared outputs against the selected immutable image, and gives
  it no provider bundle or materialization transaction;
- **P2-02:** groups active components into provider nodes according to their
  materialization semantics;
- **P2-03:** plans a structural graph containing the base root and explicit supplier
  edges and rejects structural cycles before resolution; each consumer
  resolver then validates candidates from already initialized output catalogs
  and freezes its automatic selection as the resolver's first step;
- **P2-04:** resolves a closed checksummed artifact set for platform/base identity;
- **P2-05:** applies translations without turning them into install requests;
- **P2-06:** reports provider-owned executable outputs and final image paths;
- **P2-07:** emits a deterministic offline recipe with a recipe version;
- **P2-08:** distinguishes ordinary recipe data from typed executable operands and binds
  each executable operand to its supplier/prerequisite, immutable upstream
  image, invocation/link/terminal paths, file evidence, and provider-specific
  observed facts;
- **P2-09:** separates pre-build declarations for provider-generated executables from
  their post-materialization realized link/terminal/file evidence;
- **P2-10:** declares versioned provider-owned resolver/materializer child environments with
  closed stdin and no controlling terminal;
- **P2-11:** emits versioned canonical bundle locks containing only declarative metadata
  and raw provider artifacts, with exact recipe version and digest validation;
- **P2-12:** declares tool/runtime prerequisites from the base image,
  provider-owned builder, or an earlier provider DAG node;
- **P2-13:** validates declared tools inside the resolver or materializer that
  consumes them, before network work or persistent change;
- **P2-14:** compiles backend and provider prerequisites into canonical versioned
  exact-prefix requirement profiles whose evidence is keyed by immutable image
  digest and profile digest.
- **P2-15:** for APT, selects the Debian/Ubuntu behavior profile when exact parsed `ID` or
  one exact `ID_LIKE` token is `debian` or `ubuntu`; forbid substring matching;
  retain the exact OS fields for diagnostics; and gate actual use on the
  supported configuration schemas and runtime APT/dpkg behavior probes. Keep
  representative releases in CI rather than hard-coding derivative names or
  release versions into runtime acceptance.

Python implementation:

- **P2-16:** resolve each Python component independently from its own requirements,
  enabled component options, and explicitly targeted direct package additions.
- **P2-17:** keep option selections and direct package roots in deployment state;
  disabled options contribute nothing.
- **P2-18:** normalize explicit distribution mappings, enforce translation-root boundaries,
  and give mappings precedence over index candidates including transitives.
- **P2-19:** validate built metadata, constraints, duplicate normalized names, collisions,
  and unused mappings.
- **P2-20:** preflight compatible Python through a declared and validated `base` output;
  never search the image or install an undeclared prerequisite implicitly.
- **P2-21:** pass the selected interpreter to resolution and materialization only as a
  typed validated executable operand; never use ordinary data or `PATH` to
  select the command.
- **P2-22:** install at a provider-owned fixed path and resolve console scripts absolutely.

**P2-23:** Adapt the bundle UX to the final option/package overlay commands,
top-level `reploy build`, install's explicit use of the same build pipeline,
and provider-store cleanup before removing the legacy bundle projection.

Gate:

- **P2-24:** provider unit tests cover closed resolution, option selection,
  translations, deterministic ordering, prerequisites, incompatibilities, and
  executable lookup.
- **P2-25:** graph tests cover base-first automatic selection,
  incompatible-base selection of an earlier provider output, explicit supplier
  override, no retroactive use of later nodes, observed incompatibility failure
  without fallback, and deterministic selected edges in the final lock.

**P2-26:** Retain the current single aggregated Python node only as a migration step.
Implement the generalized provider DAG executor and component-scoped Python
nodes before accepting multiple independently materialized Python environments
or a second component provider.

**P2-27:** At the Phase 2 gate, delete the old `internal/providers.Provider`, aggregate
request/prerequisite types, `providers.Bundle`/`Artifact`/`ExecutableOutput`,
`ValidateBundle`, and their fingerprint/serialization tests. The temporary
Python wheelhouse adapter returns the new `ResolvedBundle`; it does not keep the
old provider contract alive.

## Phase 3: BuildKit Image Materialization

Prototype first; complete only after the architecture review gate.

- **P3-01:** resolve the mutable image reference from `components.base.image` to an
  immutable platform-specific descriptor during `reploy build`.
- **P3-02:** inspect and normalize the base-image configuration according to the model;
  reject unsupported hidden build/runtime behavior before materialization.
- **P3-03:** generate the build definition and invocation internally.
- **P3-04:** mount the closed bundle read-only; install offline; retain only installed
  results in the generated image.
- **P3-05:** render each provider node as one explicit invocation of one read-only mounted,
  provider-owned script and exactly one layer-producing BuildKit transaction.
  Render every recipe field or reject it, run materialization without network,
  and launch provider subprocesses under an exact versioned provider-owned
  child-environment profile without inherited or blueprint-provided variables,
  with stdin from `/dev/null` and no controlling terminal.
- **P3-06:** permit command position only for provider-fixed absolute tools, typed
  validated upstream executables, or recipe-declared generated executables
  after validation; ordinary dynamic data remains arguments only. Bind generated
  declarations to transaction identity and observed evidence to realized image
  identity.
- **P3-07:** distinguish semantic bundle identity, the broader order-dependent assembly
  transaction identity, and realized image identity. Assembly additionally
  binds the exact upstream image, script and runner, controlled environments,
  execution policy, build mounts, and typed executable inputs; realized
  identity adds the immutable image digest and observed output evidence.
- **P3-08:** implement local `lock-v1` digest vectors for environment-build identity and
  validation. Portable environment export/import is unsupported in v1; any
  future transfer feature requires a separate design and is outside this plan.
- **P3-09:** represent build mounts with directory-independent logical descriptors and
  existing manifest/script digests. Atomically publish verified bundle roots,
  late-bind physical backend paths, and do not rehash artifact bytes during
  normal cache lookup or materialization.
- **P3-10:** validate carrier and provider tools, typed executable evidence, and the
  absence of the fixed transient build-mount root as the first step inside the
  consuming resolver or materializer. On a cached-bundle mismatch, commit no
  layer, resolve once against the fixed current prefix, and materialize the
  replacement. Create no standalone prerequisite-probe container; keep the
  separate full final-image validation and optional `--validate-layers` runs.
- **P3-11:** run each package-tool transaction unsplit and rely on the operating system's
  actual process-argument limit. Report `E2BIG` with provider, phase, and
  operand-count context; do not add a predictive budget or silently chunk one
  provider transaction.
- **P3-12:** exclude runtime-only inputs such as published ports, runtime mounts,
  phase/scope, runtime owner, lifecycle, readiness, and restart policy from
  provider-node image identity.
- **P3-13:** keep semantic image identity independent of deployment directories. Record
  each deployment's one committed generation plus temporary and pending-cutover
  references safely; retain no previous generation after successful cleanup and
  do not create a canonical Reploy image tag for cross-deployment lookup.
- **P3-14:** reuse the current deployment's unchanged recorded image; allow the backend to
  reuse its own layers and build cache; invalidate changed DAG nodes and
  downstream nodes; and recover interrupted relinking from state.
- **P3-15:** permit current and candidate content-addressed locks to coexist only during
  publication or recovery. After state cutover, retain exactly the selected
  lock and its transitive provider-store closure; on failure preserve the old
  current closure and remove candidate-only data.
- **P3-16:** for staged install, reuse a matching current staged build or run the
  build pipeline there; for direct install, run it in the private temporary
  staging-like workspace. Lock that source workspace first in both cases, then
  acquire the installed destination lock after the source build is current, and
  hold both through transfer and installed-state commit; installed deployments
  never serve as install sources. Install calls a lock-aware internal build
  routine rather than recursively acquiring the source lock. Then copy into the
  installed deployment only the digest-verified provider-store objects
  transitively referenced by the selected
  current build lock; omit unreferenced objects and commit installed state only
  after the destination objects publish atomically. A failed build or transfer
  leaves the previous installed state active.
- **P3-17:** remove only environment-owned references during environment cleanup, never
  force-delete an image, and never globally prune.

**P3-18:** At the Phase 3 gate, delete `providers.Materialization`,
`MaterializationStep`, the free-form generated-image renderer, and its frontend
1.7 `generatedImageDockerfileSyntax` constant/tests. Only exhaustive
`MaterializationTransaction` rendering and the pinned frontend 1.24.0 digest
remain. Introduce the state/pending-owned
generation-reference lifecycle here; do not extend the old
staging/deployed/previous tag lifecycle.

**P3-19:** Probe and document the supported Linux Engine and Docker Desktop BuildKit
invocation. Fail preflight clearly when unavailable; do not add a classic-builder
fallback or user-authored Dockerfile.

Review evidence:

- **P3-20:** inspectable generated build input and fake-runner command tests.
- **P3-21:** identity/invalidation/reuse/cleanup tests.
- **P3-22:** real-Docker smoke proving offline install and execution.
- **P3-23:** recorded Linux Engine and Docker Desktop capability results.

The authoritative Phase 2/3-to-slice crosswalk is in
`APT_PROVIDER_DETAIL_DESIGN.md`. Its coverage check compares the complete ID
set in these two phases with the crosswalk, requires every ID exactly once, and
requires every detailed-design slice to own at least one mapped task.

## Phase 4: Resolved Docker Execution Plan

Derive one plan from resolved blueprint, deployment identity, phase, optional
installed scope, materialized image, and CLI install overrides.

Paths and mounts:

- Environment owns `container`, `writable` (default false), and `update`.
- Reserve `/mnt` as the default runtime-mount root; require normal destinations
  to be strict descendants, and admit other roots only through normalized,
  non-overlapping `docker.additional_mount_roots` entries.
- Against the exact immutable image, require every effective Reploy/blueprint
  mount target to be absent or an empty real directory; reject files, symlinks,
  mountpoints, non-empty directories, overlapping destinations, and every
  protected provider/Reploy intersection without recursive filesystem scans.
- Keep Docker-intrinsic kernel and resolver mounts outside the blueprint
  allowlist while validating every Reploy-generated mount.
- Enforce the model matrix:
  - managed-bind: preserve or replace;
  - named volume: preserve or replace, with replacement copied from the staging
    volume (temporary staging-like volume for direct install);
  - external bind: `unmanaged` only, existing absolute source, never changed by
    Reploy or replacement flags;
  - tmpfs: preserve/replace accepted as no-op update policies.
- Enforce read-only mounts when `writable` is false.

Endpoints and readiness inputs:

- Environment scheme/container port is authoritative and inherited through
  `extends`; Docker adds bind and required `staging`/`deployed` publication.
  Persisted phase `staged` selects staging and `installed` selects deployed.
- Preserve `--port PORT` for one endpoint and repeatable `--port NAME=PORT` for
  named installed publications. Never change staging or container ports.
- Reject unknown/duplicate/ambiguous overrides and record effective installed
  address, published port, and container port.
- Keep image, container endpoint, mounts, application configuration, and startup
  behavior equivalent across phases except host identity needed for isolation.

Ownership:

- Staging has no install scope and uses the backend's current-user container
  identity policy.
- On native Linux, staged and installed user containers use the invoking user's
  numeric UID/GID.
- On Docker Desktop, staged and installed user containers use a stable,
  recorded Reploy-managed non-root Linux UID/GID inside the Desktop VM rather
  than the macOS or Windows account's numeric identity.
- User-scope operations warn when overriding image `USER` or ignoring
  `system.run_as`.
- Installed system scope uses the resolved service account.
- Only writable paths and Reploy temporary home are writable.
- During build, validate every compiled mount destination and runtime-exposed
  executable against the exact final image. Before container creation, perform
  only host-side mount-source existence, kind, and read/write-policy checks;
  create no separate access-preflight container or runtime-access record.

Regenerate Compose, backend env/state, dry-run, status, and control inputs from
the plan. Use exec-form Compose commands, not `sh -c`.

Gate: golden rendering/state tests plus Linux system/user and Docker Desktop
current-user planning tests. Mount-policy tests cover `/mnt`, additional roots,
no-shadow image inspection, protected intersections, intrinsic-mount exclusion,
host-side source checks, and runtime permission-failure reporting.

## Phase 5: Commands and Interactive Shell

Invocation segments are `binary`, `prefix`, `command`, `forwarded`, `suffix`.
Executable `order` supplies the default; a command may replace it.

- `binary` appears exactly once and first.
- Every other segment appears zero or one time; reject duplicates.
- If forwarded user arguments exist but `forwarded` is omitted, fail.
- Triggers are unique and use longest matching trigger.
- `native_command` and `deployed_command` default false; deployed requires native.
- Before `--`, accept only declared `forward_flags`; after `--`, forward
  application arguments as inert argv values. Suggest close declared flags.
- Resolve provider binary and lazy workload values before constructing argv.
- Invoke all commands directly as argv; prove shell metacharacters remain inert.

One-shot commands run in transient generated-image containers with the same
owner, configuration, and paths as the active workload, preserve separate
stdout/stderr without a TTY, return the process status, and always clean up.

`reploy shell` is an explicit built-in staging/management operation, not
reachable through forwarding. Run `/bin/sh` in a transient container, attach
streams, select TTY only for an interactive caller, support piped stdin, forward
signals/resizes, restore terminal state, return status, and clean up.

Gate: order/trigger/forwarding/injection tests plus streams, status, cleanup,
TTY, signal, resize, and terminal restoration.

## Phase 6: Install and Workload Runtime Events

Implement ordered steps containing `requires`, `actions`, or both. Requirements
precede actions; failures stop and skip later events.

- `environment.install.after_install` follows materialization/deployment setup
  and precedes any requested start.
- `environment.workload.runtime` owns before/after start and stop.
- Install with start: materialize, after-install, before-start, start, readiness,
  after-start, success lines.
- Install without start: materialize, after-install, success lines. Success
  output may report planned publication but must not claim the service runs.
- Standalone start/stop runs only workload runtime events.

HTTP(S) readiness:

- GET the active controlled publication until success or timeout (30s default,
  1s interval default); only HTTP 200 succeeds, body ignored, no redirects.
- Recommend loopback publication. Retain wildcards, but probe `0.0.0.0` through
  `127.0.0.1` and `::` through `::1`; bracket IPv6 URLs.
- Require readiness paths beginning `/`.
- Default `tls_verify` false because Reploy probes its locally installed target;
  allow explicit true for a trusted chain.
- Fail early if service state leaves running and include bounded diagnostics.

Gate: event ordering and short-circuit tests, install-without-start, success-line
resolution, readiness retries/timeouts/status/TLS/wildcards/IPv6, service exit,
and diagnostic bounds.

## Phase 7: Cutover and Legacy Removal

Cut CLI, state, generated scripts, status, dry-run, install/update, bundle,
commands, and cleanup to resolved plans. Keep schema 1 but only the new shape.

Migrate repository smoke, git-source, and OmegaConf demo blueprints. Migrate and
exercise Arbiter when its checkout is available; otherwise record that external
validation as deferred evidence.

Only after all callers move, remove legacy app/provider structures, install
port/managed-path schema, special success variables, Docker service/health
protocol, startup virtualenv/runtime-volume installer, `REPLOY_DEPLOYMENT_SCOPE`
blueprint coupling, duplicate endpoint/command reconstruction, and the `ctl`
default. Preserve adapted bundle UX and other nonconflicting behavior.

The provider-state cutover has no backward-compatibility path. Loading the
prototype integer-schema state containing `bundle`, `materialization`, or
`images` fails `state.legacy_unsupported`, leaves the directory untouched, and
instructs the user to recreate the deployment. Do not add a legacy provider-
state decoder or infer target components from old roots.

Provider cutover removal is explicit:

- delete `BundleState`, `ArtifactRoot`, `SelectedComponents`,
  `PreparedFingerprint`, `bundlePreparedFingerprint`, `MaterializationState`,
  and every helper/test that computes or invalidates the prepared fingerprint;
- delete `PreparedBundleResolver`, `reploy-wheelhouse.json`, `.reploy/bundle`,
  `preparedBundleManifestName`, `copyWheelhouse`, `BundleAddWheel`,
  `BundleAddSource`, `BundleDirName`, `REPLOY_BUNDLE_DIR`, configurable
  `docker.deployment_dirs.bundle`, and their install/doctor/info fixtures;
- delete `GeneratedImagesState`, `GeneratedImageState`,
  `GeneratedImageIdentity`, the staging/deployed/previous tags, directory and
  fingerprint labels, `promotePreviousEnvironmentImage`,
  `promoteGeneratedImageState`, recovery-from-labels, `generatedImageCleanup*`,
  and previous-generation retention tests; and
- remove `bundle list-options`, `prepare`, `check`, and `upgrade` rather than
  retaining aliases. Keep `bundle options`, overlay-oriented `list`, explicit
  option/package mutations, `clean`, and top-level `reploy build`.

The gate includes repository searches proving those symbols, serialized fields,
paths, labels, tags, and commands are absent, plus an old-state fixture proving
rejection without mutation. Prototype characterization tests are deleted or
rewritten against the replacement contract; they are not compatibility tests.

Add a `Docs` Changie fragment and update blueprint-facing docs/examples.

Gate: repository migrations, explicit legacy rejection, retained CLI behavior,
and focused end-to-end install/update/runtime tests pass. CLI tests prove that
top-level build does not install, staged install reuses a matching build and
builds a missing or stale one, direct install builds in its private temporary
workspace, install help/progress exposes the build work, stage/overlay commands
do not build, and runtime commands never invoke resolution or image
construction. Install concurrency tests cover source-before-destination
acquisition, direct source-workspace locking, exclusion of concurrent source
cleanup/build during transfer, and release on every exit path.

Implementation note: repository and Arbiter blueprints now use the environment
shape, and that path no longer emits or runs the startup virtualenv protocol.
The old decoder remains temporarily isolated for the existing legacy
characterization suite. Deleting that decoder and converting or retiring those
fixtures is tracked in the model's private backlog and remains a pre-release
requirement rather than a compatibility promise.

## Phase 8: Final Verification and Commit

1. Run gofmt and focused changed-package tests.
2. In parallel where safe, run:
   - `GOCACHE=${GOCACHE:-/tmp/reploy-go-cache} go test -timeout 2m ./...`;
   - `nox -s cli-integration`;
   - `nox -s release-build-smoke`;
   - `nox -s docs-build`.
3. Verify generated artifacts, Sapling diff scope, release note, no secrets/local
   paths/runtime state, directory-scoped cleanup, opt-in destructive updates,
   and absence of deferred public schema.
4. Review the final diff against the model. Commit only after approval; pushing
   and publishing are outside this workflow.
