---
status: Active
updated: 2026-07-14
summary: Normative blueprint environment, workload, component, lifecycle, and Docker rendering model.
supersedes: docs/CROSS_PLATFORM_INSTALL_LOCATIONS.md
---

# Blueprint Environment and Workload Model

Working note for separating environment intent, runnable workloads, Reploy
orchestration, and Docker rendering in the blueprint. This is the normative
design for the initial implementation.

## Problem

The current blueprint puts too much environment and workload behavior under
`docker`. Health checks make the boundary problem especially visible:

- `docker.health.scheme_env`, `host_env`, and `port_env` expose Reploy internal
  environment variables as blueprint protocol.
- `health_check: {wait: true}` appears in lifecycle hooks, but the actual
  readiness target is defined elsewhere.
- Runtime and install health hooks have similar but separate shapes.
- Ports are treated as install details, even though they are workload endpoints.
- Lifecycle behavior such as config validation, readiness, and live checks is
  environment behavior; Docker is one renderer/runtime for it.
- Docker still has real concerns, such as restart policy, runtime preparation,
  mounts, and container-specific paths. The starting OCI image is environment
  content represented by the required root component, not Docker runtime
  configuration.

The Arbiter blueprint makes the boundary problem visible. Arbiter has:

- an HTTPS workload endpoint
- config and data paths
- environment commands such as `serve`, `config check`, `config check --live`,
  `bootstrap`, `env check`, and `plugins list`
- install/runtime lifecycle checks
- Docker-specific rendering details, including container bind address,
  generated component layers and mounted paths

## Desired Ownership Split

Environment-owned:

- environment id and software components
- the required `base` root component, including its starting OCI image and
  declared executable outputs
- CLI/terminal integration, such as color environment variables
- logical runtime paths such as config and data
- commands
- component requirements and requested build outputs
- explicit requirement-to-source translations

Workload-owned:

- entry command
- endpoint protocol, default listening port, and readiness behavior
- lifecycle checks and readiness semantics

Docker-owned:

- container settings
- restart policy
- generated Compose/service rendering
- environment path mount implementation, including bind mounts and named volumes
- generated component layers and the derived environment image
- container bind settings and staging/deployed port publication

Reploy-owned:

- active deployment phase and install-scope resolution
- mapping workload endpoints to staging/deployed URLs
- process/service state checks
- startup log markers and diagnostics
- command execution plumbing

## Internal Execution Phases

Reploy evaluates an environment through an ordered internal pipeline:

```text
resolve blueprint
-> execute provider graph
   -> resolve a closed provider bundle
   -> materialize its offline backend node
   -> derive or load and validate its selected outputs
-> finalize the backend environment
-> prepare managed paths
-> run after-install actions
-> run before-start actions
-> start the workload
-> satisfy readiness requirements
-> run after-start actions
```

The internal phases are not author-defined lifecycle hooks. Blueprint authors
provide component, path, workload, readiness, and lifecycle intent; Reploy and
the selected backend determine how to realize the prerequisite phases. Provider
graph steps may interleave: a downstream bundle resolver may need a disposable
resolver container based on an already materialized upstream node. Every
individual provider bundle is closed before its own node is materialized, and
every materialization step remains offline. Docker may materialize a derived
image from bundle layers, while another container backend may use a different
native mechanism without changing the environment schema.

Inspection should remain lightweight. Dry-run and status output should show the
resolved phase order, commands, endpoint addresses, selected bundle identity,
and locations of generated backend files. Inspection must not imply a graphical
renderer and should not trigger package resolution, image builds, or other
material changes unless the requested operation normally performs them.

Without package resolution, dry-run cannot know a changed environment's future
bundle identity. It reports the currently recorded bundle and materialized
backend identity when available, whether static bundle inputs changed, and the
candidate identity as `unresolved` until `reploy build` performs resolution.
Generated backend paths are reported as existing, changing, or planned; dry-run
does not need to generate their final contents.

## Target Container Platforms

Every blueprint declares a nonempty compatibility set of target container
platforms under `blueprint.compatibility.platforms`. Values use canonical
lowercase OCI `os/architecture[/variant]` syntax:

```yaml
blueprint:
  compatibility:
    platforms:
      - linux/amd64
      - linux/arm64
```

This set states where the environment is intended to run; it does not request
that every operation build all listed platforms. Reploy selects exactly one
platform for each bundle, stage, install, or update operation:

1. An explicit `--platform` target must match a declared entry and the selected
   backend's capabilities.
2. A single declared entry is selected directly when the backend can realize
   it.
3. With several entries and no explicit target, Reploy queries the container
   backend's native platform and selects it only when exactly one declared entry
   matches. It never substitutes the Reploy host process's `GOOS` or `GOARCH`.
4. No match, multiple matches, an incompatible base image, or an incapable
   backend is an error rather than an implicit fallback or emulated build.

Omitting the optional variant means compatibility with that architecture
without requiring a narrower CPU baseline. Reploy records a concrete variant
when the selected base manifest and backend provide one. Authors should declare
a variant only when the environment intentionally requires it.

Common cases are:

| Intended container target | Declaration |
| --- | --- |
| Typical Intel/AMD Linux server | `linux/amd64` |
| Typical 64-bit ARM Linux server or Apple Silicon Docker Desktop | `linux/arm64` |
| Portable across common x86-64 and ARM64 Linux backends | both `linux/amd64` and `linux/arm64` |
| 32-bit ARMv7 device | `linux/arm/v7` |
| Deliberate x86-64-v3 minimum | `linux/amd64/v3` |

These are container platforms, not host-integration platforms. A blueprint run
through Docker Desktop on macOS or Windows normally still declares a
`linux/...` platform. Native host install support and service integration are
covered separately by the install-scope support matrix.

After selection, Reploy uses one normalized platform record containing the OCI
OS, architecture, optional variant, and selected base-manifest descriptor. It
passes that platform explicitly to base resolution, every resolver and build
container, BuildKit, probes, and runtime container creation. Any future portable
import must bind the same record.
Provider bundles, locks, rendered transactions, validation profiles, assembly
keys, and realized-image identities all bind the same record.

The initial Docker renderer also uses a versioned renderer profile containing
the immutable Dockerfile-frontend digest and every result-affecting backend
capability. Reploy never relies on a floating Dockerfile syntax tag,
`DOCKER_DEFAULT_PLATFORM`, or another backend default. A frontend or capability
profile change creates a different rendered transaction and cache identity.

Package providers map this selected OCI platform through a provider-owned,
versioned architecture table. The initial APT provider supports
`linux/amd64` -> Debian `amd64`, `linux/arm64` -> Debian `arm64`, and
`linux/arm/v7` -> Debian `armhf`; supported CPU variants retain the same Debian
architecture while still constraining base and backend selection. Before APT
resolution, Reploy requires the base's `dpkg --print-architecture` result to
match that mapping and requires `dpkg --print-foreign-architectures` to be
empty. Only the mapped native architecture and Debian's architecture-independent
`all` packages may participate. APT package state is keyed by binary package
name plus Debian architecture, while author requests remain
architecture-neutral.

## One Primary Workload, Plus Native Commands

Each blueprint describes one environment with at most one primary workload. In
the Docker backend, that workload maps to one container and its primary process.
The environment may also define many native commands and dependencies, but it is
not a multi-service or multi-container orchestration unit.

Native commands are first-class operations in the prepared environment. They
may initialize, configure, inspect, migrate, test, or otherwise operate on the
environment. They can exist whether or not the environment declares a primary
workload. A service-oriented environment may expose commands that operate on its
service, while a standalone environment may consist entirely of native commands.

Within `environment.commands`, `native_command: true` exposes a command through
Reploy's native command surface. `deployed_command: true` additionally allows it
to target the deployed environment. A command without `native_command: true`
may still serve as an internal workload entrypoint or lifecycle action without
being exposed directly to users.

One defined command may be selected as the primary workload entry command. That
selection gives the command persistent start/stop and readiness semantics; it does
not make the environment's other commands subordinate to the workload.

When present, the primary workload is a long-running service that may be
installed, started automatically, restarted, and addressed through persistent
endpoints. One-shot execution is represented by a native command rather than a
workload type.

Every command invocation is one-shot by default and is expected to exit with a
status. In Docker, Reploy runs it in a transient container created from the same
materialized environment image, as the configured non-root runtime user, with the same managed
paths and application configuration as the workload container. The transient
container is removed when the command exits. Selecting a command as
`environment.workload.command` is the only operation that promotes it to the
persistent container entrypoint.

Lifecycle actions invoke commands through the same one-shot mechanism. An
`after_start` command may communicate with the running workload through its
resolved endpoints, but Reploy does not implicitly execute it inside the
workload container. `native_command: true` exposes the command in staging and
source workflows; `deployed_command: true` also exposes the same transient
command operation through the installed control script.

The environment defines shared identity, paths, commands, and components. Its
primary workload selects one command and defines the lifecycle and endpoints
associated with running it. Service-manager installation, restart policy, port
publication, and other backend mechanics remain backend concerns. Other native
commands remain independently runnable operations against the same environment,
not additional workloads or services.

`reploy shell` is a built-in interactive operation rather than a blueprint
command or workload type. In Docker, it creates a transient container from the
same materialized image and managed paths, attaches standard input and output,
and allocates and manages a TTY when the caller is interactive. Reploy also
forwards signals and terminal resize events and returns the shell's exit status.
The container is removed when the shell exits. Images must provide `/bin/sh`;
configurable, named, or persistent sessions are deferred until a concrete use
case requires blueprint schema.

Blueprint executable declarations are a managed reference contract, not a
runtime allowlist. `reploy shell` exposes the materialized image's normal
runtime filesystem and `PATH`; the interactive user may invoke undeclared
programs that are available to the container's runtime identity. The shell does
not receive provider build credentials, artifact mounts, elevated privileges,
or permission to install packages implicitly.

Environments that need only standalone execution can omit the workload and
expose native commands.

Container lifecycle is intentionally simple. A change to configuration,
managed paths, components, executable defaults, or other workload inputs
requires the container to be recreated before the change takes effect. The
initial model does not
include change notifications, reload-on-change behavior, resource convergence,
guards, or a generic dependency graph. More granular behavior should be added
only for a concrete use case that cannot be handled by the restart operation.

`reploy restart` is a logical request to stop and start the workload using the
current desired configuration. If the effective image and container inputs are
unchanged, the backend may restart the existing container. If an already
resolved image, mount, port, command, or workload input changed, the backend
recreates the container and starts the replacement. Restart does not itself
perform package resolution or stage a new bundle.

`environment.workload` defines the optional primary workload.
`docker.workload` adds backend settings for that workload, such as restart
policy, bind addresses, and published ports. If the environment has no workload,
`docker.workload` is omitted. A second named workload or Docker workload is a
schema error; multi-service composition is intentionally out of scope.

## Software Components

An environment may require software from several ecosystems. `components` is a
map of named software requirements. Each component contributes packages,
runtime preparation, or built executables to the same environment. Component
names give stable references and allow more than one component of the same type
when needed.

Every environment has exactly one required root component named `base`. It
contains the starting OCI image and may declare executable outputs already
present in that image. It has no `type`, upstream component, provider bundle,
or materialization layer. Reploy resolves it first and represents it as the
lowest node in the provider graph.

`base.image` is a required nonempty OCI image reference. `base.exports` uses the
common output-name grammar, but each base output must declare one normalized
absolute `executable` path. Base outputs may carry a
`logical_version_override`; they do not support `discover` because Reploy does
not search an arbitrary image filesystem or its `PATH`. After selecting the
platform-specific immutable image, Reploy validates these declarations and
publishes them under qualified identities such as `base.python`.

Every non-base component's `type` selects a software ecosystem or
package-management stack such as `python`, `go`, `rust`, `apt`, `rpm`, or
`alpine`.
The provider is Reploy's internal implementation for materializing that
component; it is not a blueprint object. Components are declarative, not an
ordered shell-script sequence. Reploy resolves materialization order from
provider semantics and explicit dependencies where a component type requires
them. Incompatible requirements are validation errors rather than
last-writer-wins behavior. Every component must be compatible with the selected
backend and runtime.

A declared component is an independently named, logically owned environment
unit with its own requirements and output namespace. Provider semantics decide
whether components receive separate materialization nodes or share a
transaction node. Optional features that belong to the same logical unit are
component-scoped options, not additional components:

```yaml
environment:
  components:
    arbiter:
      type: python
      requirements: [arbiter-server]
      options:
        imap:
          description: Install the Arbiter IMAP plugin.
          requirements: [arbiter-imap]
        smtp:
          description: Install the Arbiter SMTP plugin.
          requirements: [arbiter-smtp]
```

Component, option, and executable-output names use one common provider
identifier grammar: `[a-z][a-z0-9_-]*`. Names are lowercase ASCII, and `.`,
`/`, and `,` remain unavailable because they delimit qualified outputs and CLI
option selections. Component names are unique within the blueprint; option and
output names are unique within their component. `base` is the required reserved
root component and cannot be used for another component. The stable qualified
output identity remains `<component>.<output>`, for example
`python_env_3.arbiter_server`.

An option has the common `description` field plus only the additive request
fields defined by its component provider. A Python option contributes
`requirements`; an APT option contributes `packages`. The provider applies the
same item grammar and validation used by the owning component. An option cannot
change `type`, `interpreter`, component identity, or define nested options.

`reploy bundle options` lists fully qualified option names. Selection uses a
component prefix and may group several options for that component:

```text
reploy bundle add arbiter/imap,smtp
reploy bundle remove arbiter/imap
```

Several component groups may be supplied as separate arguments. `/` separates
the component from its option list and `,` separates options; component and
option names may contain neither character. Reploy normalizes duplicates,
validates every component and option before changing state, and applies the
entire command atomically.

An enabled option contributes its requirements to the owning component's
provider resolution, bundle, and materialized environment. Its selection is a
deployment-local input to that component's identity and does not rewrite the
blueprint. A disabled option contributes nothing. A separate Python component
always means a separate venv, including when such a component is used only in
some blueprints or deployments.

Direct package/source additions are also deployment-local provider inputs. When
more than one component could accept an addition, the operation must name its
target component; a direct addition does not create a public component or
option object. The public commands are explicit so an addition cannot be
confused with an option selection:

```text
reploy bundle add-package system jq
reploy bundle add-package arbiter arbiter-debug==1.2.0
reploy bundle add-source arbiter ./plugins/local-auth
```

`add-package` accepts one target component followed by one or more
provider-native root requirements and applies them atomically.
`add-source` accepts one target component followed by one or more local source
roots, validates each source's provider metadata, and reports the stable source
identifier stored in the overlay. The corresponding `remove-package` and
`remove-source` commands remove an exact normalized request or a source
identifier reported by `reploy bundle list`. Package strings still use only
the owning provider's public grammar; these commands do not expose raw package
manager expressions.

### Deployment Request Overlay

Option selections and direct additions form a versioned request overlay stored
in the existing directory-scoped deployment state. Reploy does not add a new
state file inside the installation target. The overlay is independent of the
directory identity used to name Docker resources: directory identity answers
which deployment owns a resource, while overlay content answers which software
that deployment requested.

The canonical overlay contains fully qualified component/option selections and
component-qualified typed provider additions. Entries are schema-normalized,
deduplicated, and sorted; raw CLI strings and physical local-source paths are
not canonical identity. Directory-scoped state may retain a local locator as an
auxiliary field so a later stage can find the source; canonicalization replaces
it with the resolved local-source record defined below. Every update validates
the complete overlay against the current blueprint and applies atomically under
the deployment operation lock.

The effective request identity binds:

```text
canonical blueprint fingerprint
+ canonical request-overlay digest
+ selected target-platform record
```

The local build lock contains the complete canonical overlay and its digest in
addition to the blueprint fingerprint and selected platform.
Component/provider identities include only the relevant overlay subset so an
unrelated option does not invalidate every provider node. Adoption and
replacement rules for a future portable import are deferred with that surface.

### Local Source-Derived Artifacts

All providers use one common identity contract when a requested artifact is
built from local source. A local locator is deployment input used to find the
source, not portable identity. Before building, Reploy creates a canonical
source manifest and digest under the provider's declared inclusion and ignore
rules. The build input identity contains that digest, the versioned builder and
toolchain profile, selected platform, and every relevant build setting.

The provider builds its normal raw artifact, validates its ecosystem metadata,
and hashes the exact output bytes. The resolved overlay and lock omit the local
path and record the logical source identity, source-manifest digest, build
profile and settings, artifact metadata, and artifact digest. The portable
bundle carries the resulting artifact, so import requires neither the original
directory nor its build tools.

This contract applies to locally built wheels, Go or Rust binaries, locally
built `.deb` files, and future source-derived artifacts. A package version
inside an artifact remains ecosystem dependency metadata; it is not a substitute
for the source or artifact digest. Rebuilding unchanged source with different
output bytes produces a different resolved bundle, while an exact imported
bundle continues using its locked artifact bytes.

Any reference whose required component, output, executable, option-provided
artifact, or installed artifact is absent is an error at the earliest phase
that can establish the absence: normally resolution/materialization, or runtime
preflight if installed state has drifted.

### Provider Expansion Gate

The APT/dpkg provider and cross-provider executable-output design is developed
in [`APT_PROVIDER.md`](APT_PROVIDER.md). That document is a draft subdesign;
this model remains authoritative until its open decisions are promoted here.

Python is the proving component provider. Before Reploy adds another component
provider, it must first implement the common bundle-to-image-layer pipeline
described below. New providers must contribute a closed bundle artifact set and
a deterministic offline layer recipe; they must not introduce provider-specific
runtime volumes, startup installers, or cache lifecycles.

Go, Rust, and system-package examples in this document are future shapes used to
test the generality of that contract. They are not commitments to implement
those providers before the shared layer pipeline exists.

System-package providers fit the same two-phase contract. An APT/dpkg provider
may use APT during bundling to resolve a closed `.deb` dependency set, then
install that set offline with `apt-get`/`dpkg` in a generated image layer.
RPM/DNF and Alpine/APK providers would likewise bundle closed, checksummed
package sets and install them offline in provider-owned layers. Package-manager
databases and system files belong in the derived image, never in startup-time
runtime caches.

Concise provider request strings are parsed into provider-owned typed records;
they are never forwarded as package-manager expressions. The initial APT
provider accepts only exact Debian binary package names and the Debian
`name=exact-version` convention. It excludes paths, release and architecture
selectors, patterns, ranges, dependency expressions, and options, verifies an
exact package-cache match, and renders the typed result itself. Structured
entries use the same request grammar when adding fields such as exports. The
complete rules are in
[the APT package-request contract](APT_PROVIDER.md#apt-package-request-grammar).

Providers define node grouping according to materialization semantics. Each
Python component represents one independently materialized Python environment
with its own selected interpreter, closed bundle, venv root, and qualified
outputs. System-package components may resolve together into one transaction
node. Providers declare their internal prerequisites; blueprint authors do not
order layers. Reploy first builds a deterministic structural graph containing
the base root, provider nodes, and explicit supplier edges, and rejects cycles
in those known edges. It uses stable component-name ordering where independent
nodes need a tie-breaker. Immediately before resolving each consumer, Reploy
selects unqualified requirements only from already initialized suppliers,
freezes those selections, and adds the selected edges to the final graph.
Automatic edges point to initialized nodes and therefore cannot create cycles.
Independent artifact builds whose selections are already frozen may run
concurrently, while final image assembly follows deterministic final-graph
order.

The current Python implementation has a single aggregated provider node. The
generalized DAG executor and component-scoped Python nodes must be implemented
before the schema or runtime accepts multiple independently materialized Python
environments or a second provider type. It also derives Python executable
requests from public `environment.executables`; the generalized provider must
instead derive the component's console-script catalog from its exact resolved
wheel metadata.

Every provider declares the tools and runtimes it requires for bundling and
materialization. Reploy checks those prerequisites before executing the provider.
A prerequisite may be supplied by the selected base image, a provider-owned
builder environment, or an earlier provider layer represented in the dependency
DAG. Reploy never installs an undeclared prerequisite automatically. Failures
name the provider, missing or incompatible requirement, and selected image or
build environment.

For the initial Python-only implementation, the selected base image must already
provide a compatible Python interpreter. Future Go or Rust providers may require
a compatible toolchain or use a provider-owned builder environment. A system
package provider requires a compatible base distribution and its package
installation tooling.

For every Python component, an omitted `interpreter` field normalizes to the
logical requirement `{command: python}` with no version or supplier constraint.
The normal base-first, then provider-graph supplier order selects it. An author
declares `interpreter` only to constrain the logical version, select a supplier,
or request a future nondefault interpreter capability. Failure to find a
compatible `python` output remains an unsatisfied provider prerequisite.

At most one system-package provider (`apt`, `rpm`, or `alpine`) is allowed in
an environment, and it must match the base image. Component names are unique
within a blueprint, and executable output names are unique within their
component. The stable qualified output identity is
`<component>.<output>`. Equal local output names across components are valid;
incompatible executable claims or overlapping exclusive namespaces remain
validation errors.

Filesystem authority is either exclusive or shared. An exclusive provider owns
a dedicated root or exact leaf; before creating it Reploy validates the complete
ancestor chain without following symlinks, requires the component leaf to be
absent, and safely creates the root beneath a missing or previously verified
Reploy-owned provider hierarchy. Its resulting layer may persist changes only
inside that namespace. Python environments use this model.

A shared system-package provider delegates path ownership, upgrades,
replacement, conffiles, diversions, alternatives, and generated-file semantics
to its native package manager. All component requirements handled by that
provider participate in one authority domain; their blueprint names do not own
individual system files. Reploy does not reconstruct dpkg, RPM, or APK collision
rules. It rejects archive claims and actual resulting-layer changes beneath
Reploy-protected namespaces, validates the package database and required
capabilities, and otherwise accepts the package manager's successful resolved
transaction. Two unrelated providers cannot share such a domain; providers
outside it must use exclusive namespaces.

Symlinks are never followed during boundary checks. An exclusive-root ancestor
cannot be a symlink, non-directory, or mountpoint, and a shared-authority
symlink is interpreted by its native package manager rather than as a Reploy
ownership claim. Provider artifact listings and disposable layer change lists
may be streamed with safety limits; the model requires no global attribution of
every system file to a blueprint component.

`translations` is separate from `components`. A translation does not request
installation. It explicitly maps an ecosystem package identifier to another
source for resolution. A Python translation means "if resolution requires this
distribution, use this local source instead of fetching it from an index." An
unused mapping has no effect on the materialized environment. When a mapping is
used, its root is an auxiliary local locator and the resulting wheel follows
the source-manifest, builder-profile, and artifact-digest contract above; the
physical root never enters the portable bundle identity.

## Blueprint Variables

`environment.vars` owns blueprint-defined variables, but each variable name is
available globally during interpolation. Authors use `{{ project_root }}` rather
than `{{ environment.vars.project_root }}` anywhere in the blueprint. This keeps
declaration ownership explicit without making common interpolation verbose.

Variable names must be valid identifiers and may not shadow the reserved roots
`blueprint`, `environment`, `docker`, `reploy`, `user`, or `system`. Variables
may reference other variables; Reploy resolves their dependency graph and
rejects missing references or cycles. Global blueprint variables are distinct
from process environment variables and do not implicitly read values from the
invoking shell.

"Globally" refers to lookup of blueprint variable names, not to availability of
every dynamic namespace in every field. A dynamic namespace is available only
when the field's consumer has established it.

### Lazy Interpolation

Interpolation is lazy and resolves each expression as late as its consumer
needs the value. Reploy first parses and structurally validates the blueprint
while retaining expressions. Global variables resolve when referenced;
`environment.*` resolves after the environment model is complete; backend
namespaces resolve during backend rendering; and `reploy.workload.*` resolves
after the backend has produced the effective bind and publication model.
`user.*` and `system.*` resolve from the active host/install context;
`reploy.phase` resolves once the staged or installed environment is known; and
`reploy.scope` resolves only for an installed environment. Backend-dependent
success lines resolve during the install operation rather than initial
structural parsing.

After interpolation, Reploy validates the resulting value against the field's
schema. A reference to a namespace that cannot exist in the declared shape is an
error—for example, any evaluated `reploy.workload.*` reference in an environment
without a workload. Missing values, type mismatches, and interpolation cycles
are errors at the latest point where the containing field must be resolved.
Resolved values are deterministic and cached for the operation.

## Representative Components and Translations

```yaml
environment:
  # Resolution overrides: these do not request installation.
  translations:
    local_python:
      type: python
      scope: development
      root: ..
      mappings:
        arbiter-client: client
        arbiter-server: server
        arbiter-imap: plugins/imap
        arbiter-smtp: plugins/smtp

  # Application runtime requirements.
  components:
    # Future system-package provider shape.
    system:
      type: apt
      packages: [ca-certificates, libmagic1]

    application:
      type: python
      requirements:
        - "arbiter-server[imap,smtp]>=1.4"

    mailprobe:
      type: go
      packages:
        - module: github.com/example/mailprobe/cmd/mailprobe
          version: v1.4.0

    message_indexer:
      type: rust
      crates:
        - name: message-indexer
          version: "0.8.2"
          binaries: [message-indexer]
```

Python `requirements` use normal Python requirement syntax. A Python translation
maps an explicitly named distribution to a source path. Reploy normalizes the
declared distribution name for matching but does not infer it from the directory
or scan neighboring directories.

An explicitly declared translation takes precedence over a bundled or index
candidate with the same normalized distribution name. This precedence also
applies to transitive requirements. `root` establishes the translation's source
boundary, and every mapping value is relative to it. Reploy normalizes each path
and rejects absolute mappings or any mapping that escapes `root`.

`scope: development` means the translation is available only while resolving
from the declared source checkout. For Arbiter, `root` is the repository root,
even though the blueprint lives below it. Reploy uses those sources to produce
bundled artifacts; an installed environment consumes the artifacts and does not
need access to the development checkout.

Duplicate normalized names, built metadata that disagrees with the declared
name, and local versions that do not satisfy the active requirement are errors.
Unused mappings are valid because translations are resolution rules, not
installation requests.

The same structure can translate other ecosystem identifiers: a Go module path
or Rust crate name may map to a local directory. The initial mapping value is a
path string. Richer source descriptors such as Git URL/ref or subdirectory
selection should be introduced only for a concrete use case rather than making
every mapping an object preemptively. Translation types define identifier
normalization and path validation for their ecosystem; the mapping itself
remains explicit.

Go components request module packages or local module binaries. Rust components
request crates or packages from a local Cargo workspace. The blueprint uses the
ecosystem name `rust`; Cargo is an implementation detail of the Rust provider.

## Possible Shape

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=NEXT"
  compatibility:
    platforms: [linux/amd64, linux/arm64]

environment:
  id: arbiter
  control_script: arbiter
  vars:
    project_root: ../../../..
    config_name: arbiter-server
  translations:
    local_python:
      type: python
      scope: development
      root: "{{ project_root }}"
      mappings:
        arbiter-client: client
        arbiter-imap: plugins/imap
        arbiter-server: server
        arbiter-smtp: plugins/smtp
        arbiter-suite: meta/arbiter-suite
  components:
    base:
      image: python:3.11-slim
      exports:
        python:
          executable: /usr/local/bin/python
    application:
      type: python
      requirements:
        - arbiter-server
  terminal:
    color_env: ARBITER_COLOR
    # Future extension if an environment does not use always/never/auto:
    # color_values: {always: always, never: never, auto: auto}

  install:
    # Omit target defaults to use Reploy's host/backend/scope-aware defaults.
    target: {}
    system:
      run_as:
        user: arbiter
        group: arbiter
        on_missing: create
    success:
      lines:
        - "server url: {{ environment.workload.endpoints.https.scheme }}://{{ reploy.workload.endpoints.https.publish.address }}:{{ reploy.workload.endpoints.https.publish.port }}"

  paths:
    config:
      container: /mnt/config
      update: preserve
    data:
      container: /mnt/data
      writable: true
      update: preserve

  executables:
    arbiter_server:
      component: application
      binary: arbiter-server
      order: [binary, prefix, command, forwarded, suffix]
      # resolved binary + argv_prefix + command argv + argv_suffix form argv.
      argv_prefix:
        - --config-dir
        - /mnt/config
        - --config-name
        - "{{ config_name }}"
      argv_suffix:
        # `reploy.workload` exposes the active backend's resolved bind and
        # published endpoint values.
        - arbiter.server.bind.host={{ reploy.workload.endpoints.https.bind.address }}
        - arbiter.server.bind.port={{ reploy.workload.endpoints.https.bind.port }}
        - arbiter.server.public.scheme={{ environment.workload.endpoints.https.scheme }}
        - arbiter.server.public.host={{ reploy.workload.endpoints.https.publish.address }}
        - arbiter.server.public.port={{ reploy.workload.endpoints.https.publish.port }}
        - arbiter.storage.server_data_dir={{ environment.paths.data.container }}/server
        - arbiter.storage.plugin_data_dir={{ environment.paths.data.container }}/plugins
        - arbiter.deployment_scope={{ reploy.phase }}
  commands:
    server:
      executable: arbiter_server
      argv: [serve]

    config_check:
      executable: arbiter_server
      trigger: [config, check]
      native_command: true
      deployed_command: true
      forward_flags: [--live]
      argv: [config, check]

  workload:
    command: server
    endpoints:
      https:
        scheme: https
        port: 8075
        readiness:
          path: /_health_
          timeout: 30s
          interval: 1s
          # Reploy is the readiness-probe client. This controls certificate
          # verification for Reploy's requests; it does not configure the
          # workload's TLS server.
          tls_verify: false
    runtime:
      before_start:
        - actions:
            - environment: [config, check]
      after_start:
        - requires:
            endpoints: [https]
          actions:
            - environment: [config, check, --live]

docker:
  # /mnt is always allowed. Additional roots are an explicit opt-in for
  # applications that require mountpoints elsewhere.
  # additional_mount_roots: [/srv/arbiter]
  mounts:
    config:
      extends: environment.paths.config
      mode: managed-bind
      source: conf
    data:
      extends: environment.paths.data
      mode: managed-bind
      source: data

  workload:
    restart: on-failure
    endpoints:
      https:
        extends: environment.workload.endpoints.https
        bind:
          address: 0.0.0.0
        publish:
          address: 127.0.0.1
          deployed: 8075
          staging: 18075

```

### Component Materialization and Executable Defaults

`environment.components.base.image` is the author-supplied starting OCI image.
Reploy resolves the `base` root and its declared outputs before other components,
then resolves translations and provider components through the graph and records
closed, checksummed artifact sets for the target platform and upstream image
identity. A provider's bundle resolver may run with network/source access in a
disposable resolver container based on an earlier materialized node. The
corresponding node materialization performs no package resolution and requires
no package-index or source-checkout access. Normal start and restart reuse the
recorded bundles and image. An explicit `reploy build` performs fresh resolution
and may produce new bundle fingerprints.

When `environment.components.base.image` is a mutable tag, `reploy build`
resolves it to an immutable digest and records that digest as the base-layer
input. A provider node's semantic bundle identity covers its resolved bundle
section, relevant translation artifacts, target platform, provider recipe
version, and selected
prerequisites. Assembly uses the broader rendered transaction identity defined
below, including the exact upstream image and every execution-relevant input.
BuildKit reuses layers only when that complete assembly identity is unchanged.
Runtime-only settings such as published ports, mounts, restart policy,
lifecycle, and readiness do not participate in image-layer invalidation.

#### Base Image Configuration Contract

The resolved base contributes both a filesystem and Docker image configuration.
Reploy inspects that configuration before using the image in any generated
`FROM` or managed container. The initial Docker backend applies this fixed
policy:

| Base-image field | Initial Reploy policy |
| --- | --- |
| `OnBuild` | Reject a nonempty trigger list. Hidden child-build instructions are incompatible with the closed provider recipe. |
| `Volumes` | Reject any declared image volume. Blueprint paths and backend mounts are the only supported runtime storage declarations. |
| `Entrypoint` and `Cmd` | Neutralize both. Every workload, command, lifecycle action, probe, bundle resolver, and shell receives explicit exec-form argv. |
| `Healthcheck` | Disable it. Reploy's declared readiness contract is the only initial health operation. |
| `User` and `WorkingDir` | Do not inherit them as execution policy. Every build and runtime primitive receives an explicit user and working directory. |
| `Env` | Preserve it for normal workload, command, and shell compatibility, with Reploy-managed values taking precedence. Provider tools run under provider-controlled child environments as described below. |
| `StopSignal` | Normalize the managed workload stop signal to `SIGTERM`. |
| `ExposedPorts` | Treat it as informational only. Only declared workload endpoints may be published. |

The backend fails before provider materialization if it cannot inspect or enforce
this policy. Base-image labels outside Reploy's reserved namespace may remain as
informational metadata; Reploy-owned labels override any collision in its
namespace. Generated runtime instructions use exec form. Docker provider
transactions invoke `/bin/sh` explicitly with a Reploy-mounted script, so the
base image's configured Dockerfile `SHELL` does not affect them.

Docker starts the transaction shell with the immutable base image's inspected
environment. Reploy treats that initial carrier environment as trusted base
input and binds its normalized configuration to the base and transaction
identity; it does not claim to clear variables before the first process starts.
The fixed script immediately resets its shell state and invokes every provider
subprocess through a validated absolute clean-environment launcher equivalent to
`env -i`, supplying only the provider-declared environment. Probes and bundle
resolvers use the same clean-child boundary. Initial profiles transport no base
environment values. A future profile that requires one needs a separately
versioned typed transport and must explicitly declare and fingerprint the
value.

Initial provider recipes select a fixed, versioned provider-owned child
environment profile rather than transporting arbitrary blueprint or inherited
name/value pairs. The profile identifier/version, exact bindings, shell-state
policy, scratch policy, and referenced configuration inputs are canonical
transaction inputs. Changing a provider-owned fixed value requires a new
profile version; recorded digests detect drift and participate in cache/lock
identity. The APT provider's initial profiles are defined in
[`APT_PROVIDER.md`](APT_PROVIDER.md); other providers require separately
versioned profiles.

A provider-controlled child environment isolates caller and workload state; it
does not sanitize the immutable base image itself. In particular, the APT
provider treats the selected base's APT/dpkg executables, package database,
configuration, source definitions, keyrings, and hooks as trusted base-image
state transitively bound by the exact base digest. Its generated `APT_CONFIG`
is an additive fixed provider input, while required safety settings use final
explicit arguments where ordering matters. No caller package-manager or proxy
environment is inherited, and materialization remains network-free. This is an
input-isolation and reproducibility boundary, not a sandbox against a malicious
base image.

APT resolution refreshes an initially empty private index directory, treats any
enabled-source acquisition error as fatal, resolves only from that fresh index
set, and discards it with resolver scratch. Offline materialization never
refreshes or consults repository indexes.

For each downloaded APT artifact, the provider verifies and records the exact
chain from authenticated `Release`/`InRelease` metadata, through the signed
checksum of the selected `Packages` index and exact package stanza, to the
artifact's size and SHA-256. Base-origin packages remain bound by the immutable
base-image digest. Repository provenance contains only a safely normalized
public source coordinate or an opaque identifier derived from authenticated
metadata; access credentials never participate in semantic identity or durable
state. Raw source configuration and APT output are not persisted, and any
displayed diagnostic is provider-redacted or replaced by a structured error.

APT materialization requires a clean audited package database before changing
it and snapshots the complete installed package state. After installation it
audits again and compares the full state delta: no package may be removed, and
only exact bundle-origin additions or recorded base-to-bundle upgrades may
change. Base-origin and unrelated package state must remain unchanged.

The same transaction uses an initially empty private APT archive cache and,
immediately before installation, verifies the locked path, size, and digest of
every mounted package artifact. It passes all verified artifacts explicitly;
`--no-download` plus network isolation makes any undeclared requirement fail
rather than drawing from a base-image cache.

Initial bundle resolvers, probes, and materializers receive stdin from
`/dev/null` and no controlling terminal. A provider-specific profile may impose
stricter subprocess controls, but cannot enable interactive input. Stdin and
terminal policy are explicit fingerprinted execution inputs; a backend that
cannot enforce them rejects the operation.

Reploy generates the Docker build definition; blueprint authors do not maintain
a Dockerfile for supported component types. Each provider supplies a fixed
offline installation recipe for its bundle artifacts. Reploy composes those
recipes into deterministic sequential BuildKit transactions, exactly one
cacheable filesystem layer per provider materialization node. Independent
provider bundles may resolve concurrently, but their layers join the final OCI
image in stable node order.

Logical provider dependencies and backend assembly order are distinct. A change
to an earlier independent component may force later filesystem layers to be
rematerialized because the image chain changed, while their unchanged closed
bundles remain reusable without resolution or source access. Custom merging of
sibling image branches is not part of the initial backend.

The materialization recipe is a private provider-to-backend contract, not
blueprint schema. For the initial Docker backend, each provider node renders as
one explicit `/bin/sh` invocation of one read-only mounted, provider-owned POSIX
shell script. The recipe also carries validated positional arguments,
typed executable operands, controlled environment, working directory, build
user, closed stdin, no controlling terminal, explicit network policy, scoped
read-only artifact mounts, provider-node identity, and final image-user
semantics. A backend must render every field or reject the recipe; it may not
silently ignore unsupported execution or security properties. Materialization
network policy is always `none`.

This is the selected baseline carrier contract, not implementation-readiness
approval. The APT/dpkg provider draft's
[review status](APT_PROVIDER.md#review-status) records the remaining provider
design work before implementation.

Blueprint text and resolved values are never interpolated into the script
source. Dynamic values reach the fixed script only through validated positional
arguments, controlled environment fields, or hash-verified files already
contained in declared artifact mounts, and every shell expansion used as data
is quoted. Blueprint authors cannot supply shell fragments. The selected base
and every later immutable prefix used by another operation must provide
executable, validated absolute paths for a POSIX-compatible `/bin/sh` and a
clean-environment launcher.

All low-level prefix checks use one private, versioned validation mechanism.
The validation-record key is the immutable prefix's root-filesystem subject
plus the canonical digest of a requirement profile assembled from backend
baseline requirements and the provider operation that will consume the prefix.
For OCI images, the subject is the canonical digest of the ordered
`rootfs.diff_ids` sequence. Other image configuration that affects execution is
bound separately to transaction identity. On a cache miss, Reploy runs one
bounded, read-only, networkless, noninteractive probe that validates the
declared absolute tools and their complete link chains, executable terminals,
file fingerprints, and capabilities. The profile also validates normalized
mount descriptors syntactically and, without following links, requires the
backend's fixed transient build-mount root to be absent. It does not scan the
whole filesystem or rehash mounted artifact contents.

Every uncached or lock-driven materialization creates a new exact prefix
identity and must acquire a matching validation record before it is accepted or
consumed. A record is a Reploy-generated observation, not a signature,
package-trust proof, or attestation; it is trusted only under the initial
design's trusted-local-container-backend assumption. A cached record is
reusable only for the same immutable root-filesystem subject and complete
profile version. A
failed requirement blocks that node and all downstream nodes. Every executable
requirement uses a private `compatible` or `unchanged` validation policy: the
former may acquire a new compatible record after a layer changes its
implementation, while the latter must match the profile's named record.

A provider prerequisite is validated on the immediate prefix before its
consumer runs, and a later consumer validates again on its own immediate
prefix. That consumer-scoped guarantee ends with the operation. Every output
referenced by a command, directly or through `environment.executables`, is
additionally validated on the final immutable environment image after all
provider layers and before publication. An earlier prefix record cannot
authorize final command exposure.

Ordinary data and executable operands are separate recipe types. Ordinary data
can appear only as an argument to a command; it can never select the command.
A private `ValidatedExecutableInput` may occupy command position only when the
provider graph created it from a fixed provider prerequisite or a selected
base/provider output already validated against the exact immutable upstream
image. It records its recipe role, origin or supplier-qualified identity,
upstream image digest, invocation path, complete bounded link chain, terminal
path, ownership and file digest, and typed compatibility evidence.

The script receives the invocation path as one quoted positional value and may
execute it directly. It never performs a `PATH` lookup or passes it through
`eval`, `sh -c`, or another source-interpreting operation. A provider may also
declare a private `GeneratedExecutableOperand` with a recipe role, exact
provider-derived invocation path beneath a protected provider-owned root, and a
validation policy. The script may invoke it only after the generating operation
and after validating its bounded link chain, regular executable terminal, and
provider ownership. Its terminal may be a newly generated file or an already
validated upstream executable such as the bootstrap interpreter. Arbitrary
blueprint, package, artifact, and forwarded data can never become either kind of
executable operand. This is a rule for Reploy's carrier invocation: code in the
selected trusted package may still use a shebang, search its own `PATH`, load
libraries, or launch subprocesses. Direct executable evidence does not attest
that transitive behavior.

The mounted script is a trusted recipe input and does not enter the resulting
image layer. Its content digest, recipe version, exact runner argv and dynamic
inputs, inspected carrier environment, child environment, mount descriptors and
content identities, complete validated executable-input records, generated
executable declarations and validation policies, and other execution fields
form the rendered transaction identity included in the assembly cache key and
lock. Changing any of them invalidates reuse. A generated operand's actual link
chain, terminal, ownership, and file digest are post-materialization evidence;
Reploy records them against the realized image identity rather than pretending
they are known before the transaction runs.

Build-mount identity is logical and content-addressed. It includes the mount's
provider role, fixed container destination, access policy, and an existing
artifact-manifest or script digest. It excludes physical host, cache,
temporary, and deployment paths. Bundle creation or import verifies artifact
bytes once, atomically publishes the resulting manifest-root object as
immutable, and Reploy's normal identity/cache logic does not reread the bytes
merely to recompute mount identity. The backend resolves the physical source
only when rendering the operation and still performs whatever content I/O the
actual build requires.

Every bundle resolver writes raw artifacts to one initially empty, private
output mount that is its only writable host-backed path. Reploy stops the
resolver container before host ingestion. A shared safe-artifact publisher then
enumerates the output without following links, accepts only provider-accounted
regular files with normalized portable names, rejects links, special files,
aliases, and unaccounted entries, and enforces count, logical-byte, and path
limits. It streams each artifact through provider metadata validation and
content hashing into private temporary storage and atomically publishes the
manifest root only after the entire set validates. Bundle import uses the same
publication primitive after archive-specific framing checks. Resolver output
can never supply Reploy carrier code.

Every rendered provider command has a versioned argument-vector budget checked
before backend execution. The budget uses the exact encoded argv, the exact
environment vector declared for that invocation (fixed child or inspected
carrier), target pointer width, and the execution environment's probed kernel
limit with provider-defined aggregate headroom and per-string limits. It is
computed from typed metadata rather than artifact contents. An over-budget
operation fails before materialization; a provider cannot silently split one
declared transaction into several operations because doing so may change
dependency and installer semantics.

Portable environment export/import and reconstruction of application-level
configuration are deferred from the initial environment model. The following
portable-envelope constraints are retained as future design notes, not as an
initial implementation or public-CLI commitment.

Portable provider bundles contain declarative lock metadata, opaque,
hash-verified raw provider artifacts, and the digest-addressed OCI image graph
for the exact locked realization. The graph carries each required manifest,
configuration, and layer blob once even when several prefix images share it.
For a system-package closure, each exact package record has a tagged origin.
`base` origin records package, version, architecture, and installed status but
has no synthetic artifact hash; the lock-level immutable base-image digest
already binds its package database and files. `bundle` origin additionally
records the raw artifact path, size, and digest. A bundle upgrade records its
exact base predecessor. Materialization verifies base-origin tuples before
installing only bundle-origin artifacts and compares the final state with the
complete mixed-origin closure.
Outside those raw artifact and OCI bytes bundles never contain Reploy carrier
scripts, generated build definitions, or provider-runner executables. A lock
identifies its schema, transaction schema, canonical encoding, digest algorithm
and script-content-digest rule, exact provider recipe/profile versions, and
every base and realized prefix image descriptor. Import selects the matching
historical generator only from locally installed trusted provider code,
regenerates the script and transaction, and requires their digests to match the
lock before accepting the corresponding image. It then reuses or loads those
exact images; it never rebuilds them against a merely compatible upstream
image. Missing compatibility code, a missing OCI object, or any digest mismatch
fails import. Ordinary import never silently migrates a lock, accepts carrier
code from the bundle, or substitutes a new realization. Rebuilding from the
raw artifacts is a new bundle-build/re-lock operation with new identities.
The initial identifiers and byte-level canonicalization and hashing rules in
[the APT provider's bundle-lock contract](APT_PROVIDER.md#bundle-lock-manifest)
are the initial cross-provider lock contract, not APT-specific behavior.

The initial cross-provider archive envelope is `reploy-bundle-v1`: an
uncompressed deterministic POSIX ustar stream. Its canonical lock is the first
regular-file member and enumerates every remaining member's exact path, size,
digest, kind, and owner. The only other members are canonical OCI layout
metadata, digest-addressed OCI blobs, and digest-addressed raw provider
artifacts. Each required object appears exactly once even when import finds the
same digest locally; local presence may avoid publication but never makes the
portable archive incomplete.

Headers and order are canonical. Import rejects links, special and sparse
files, extended headers, directory entries, duplicate or noncanonical paths,
undeclared members, truncation, trailing data, and any count, size, metadata,
or digest mismatch. It parses a bounded lock first, applies a versioned import
profile to all declared counts and byte totals, then streams each expected
member once into private quarantine while hashing it. Tar paths are never
extracted into the destination. Reploy atomically publishes only after the
complete archive, artifact metadata, OCI graph, and lock references validate;
every failure discards quarantine. The precise paths and header values are
defined by [the provider archive contract](APT_PROVIDER.md#reploy-bundle-v1-archive).

Bundle artifacts are exposed to a build step with a read-only BuildKit bind
mount rather than copied permanently into the image. Only installed files remain
in the resulting layer. APT/dpkg, RPM/DNF, and Alpine/APK installation may run
as root in their build steps. Python, Go, and Rust layers use the permissions
required to populate their final image paths.

Runtime ownership comes from the backend and install scope, never from the base
image's configured `USER`. Reploy supplies an explicit user for every container:

- provider materialization uses the provider-declared build identity, including
  container root where system-package installation requires it;
- a native-Linux current-user install uses the invoking user's numeric UID/GID;
- a Docker Desktop current-user install uses a stable Reploy-managed non-root
  Linux UID/GID inside the Desktop VM, recorded in deployment state; this is a
  container identity, not the macOS or Windows account running Docker Desktop;
- a Linux system install uses the resolved service account.

Docker Desktop mediates explicitly shared host files through the Desktop user.
The container identity still controls permissions inside the container, named
volumes, and the container-visible mode of mounted paths. Reploy therefore
preflights every declared mount for the selected runtime identity rather than
assuming that host and container users correspond.

Declared provider outputs satisfy a versioned portable-access profile before
their realized image is accepted. For the initial Linux-container profile,
every directory needed to traverse a selected output path grants search access
to an arbitrary non-root identity, and every terminal executable grants read
and execute access without relying on its owner, supplementary groups, or an
access ACL. Provider-created exclusive roots normalize their immutable files
and directories to deterministic `a+rX`-equivalent modes. Reploy does not chmod
base-image or system-package files; an export from those sources is ineligible
if its complete selected path cannot satisfy the same profile. Provider outputs
contain no secrets, and only explicitly writable managed paths and Reploy's
temporary home are writable.

Portable access is the shareability rule, not the final runtime proof.
Immediately before creating every workload or transient runtime container,
Reploy first requires the selected immutable image's final-output record to
match its current root-filesystem subject. It then performs a bounded access
preflight under that container's exact numeric UID, primary GID, supplementary
groups, immutable image, and effective mount plan. The preflight checks each
executable output referenced by that runtime plan, including traversal of its
invocation and recorded link/terminal paths, without executing package code.
Failure names the selected identity and first inaccessible path and prevents
container creation. The runtime access record is keyed by the runtime plan and
stored in deployment state; it never enters the shareable provider image,
bundle, transaction, or lock identity.

Before a current-user install, Reploy reports the selected policy and numeric
container UID/GID. On native Linux it identifies the invoking host user; on
Docker Desktop it explains that the identity exists only inside the Linux
container/VM. The warning also states that the image's configured user is
overridden, the image must tolerate the selected non-root identity, and
persistent writes are available only through declared writable paths. If
system `run_as` configuration is present, Reploy reports that it does not apply
to current-user scope.

The materialized image is a private Docker-backend resource, not another
environment-schema object. A provider node has a semantic bundle identity, an
bundle-resolver cache key, an order-dependent assembly cache key based on the
preceding image digest, and a realized prefix-image identity containing its
immutable finalized image digest. None is derived from the deployment directory
or staging/deployed phase.
Shared image configuration and image labels contain only content-derived facts;
they never contain deployment-directory identity. A unique environment-owned
generation reference and atomically published deployment state bind the exact
realized image to one environment, and Reploy recreates the container when that
selected materialization changes. Other container backends may represent
materialization differently.

Each provider declares a complete versioned resolver-dependency profile. Reploy
validates its typed prerequisites against the current upstream image and hashes
the resulting evidence into the bundle-resolver cache key together with the
request, resolver recipe/profile, and platform. An unchanged fingerprint may
reuse the closed bundle across an unrelated earlier-layer change; changed
evidence reruns resolution. A provider that cannot enumerate a safe narrower
boundary includes the exact upstream image identity in its profile. The
parent-dependent assembly key still changes whenever the preceding image does.

`reploy build` explicitly resolves provider bundles and materializes the
environment image. `reploy up` and other runtime operations consume the recorded
build and never hide resolution or image construction. `reploy build
--no-cache` bypasses resolver and realized-image cache lookups and reprobes and
rematerializes every node, while verified immutable raw artifacts may remain
reusable by digest. The bypass flag is execution policy, not blueprint state or
semantic identity.

On Docker and Podman, Reploy attaches each canonical executable-output
validation record to the finalized prefix through reserved OCI image-config
labels. The labels contain the record schema and contents plus a subject equal
to the canonical digest of the ordered OCI `rootfs.diff_ids` sequence. The
subject is not the image digest: image-config labels change the image digest
without changing the root filesystem. The finalized image digest covers the
labels and therefore binds the record into the realized prefix identity.

Docker or Podman stores and garbage-collects the labels with the image; Reploy
defines and validates their meaning. Reploy keeps no separate machine-wide
validation database. A child image may inherit a label, but a child filesystem
layer changes the root-filesystem subject, making the inherited record
inapplicable. Reploy must probe and attach a new record at every required
consumer or final-image boundary. Once the backend garbage-collects the last
image configuration carrying a record, rebuilding requires fresh validation.

`unchanged` requires the selected path, complete link chain, terminal,
ownership, file digest, and typed facts to match the named record. `compatible`
allows drift only after a fresh probe produces a compatible replacement record
and updates dependent identity. Outputs that are neither consumed again nor
command-exposed need no repeated validation solely because another layer was
added. These records and checks are private backend state, not blueprint
fields.

Reploy owns its generated Docker containers and image references, while Docker
and BuildKit own physical layer sharing and build-cache garbage collection.
Environment cleanup removes only non-shared resources and image references
owned by the relevant directory identity. Shared images and canonical cache
references carry directory-independent content identity; canonical references
provide cross-installation lookup and are never removed automatically in the
initial design. Explicit Reploy cache cleanup may remove canonical cache
references but never environment references or physical images forcibly.
Existing environments therefore remain pinned while future installs lose only
that Reploy cache hit. Reploy never performs a global Docker or BuildKit prune.
These naming, tagging, and retention rules are backend implementation details
rather than cross-backend blueprint protocol.

State-changing operations for one deployment directory hold an exclusive
directory operation lock. A build publishes under a unique temporary reference,
captures and validates the backend-reported immutable image ID, creates a new
unique environment generation reference, and writes a durable pending-operation
record. Atomic replacement of deployment state with that generation and digest
is the commit point. Only afterward may Reploy update the canonical shared cache
hint and remove the prior generation and temporary reference. Runtime operations
use the state-selected generation, never a mutable staging/deployed tag.

Recovery under the same lock preserves the generation reachable from committed
state and removes or completes only that directory's abandoned references. A
canonical-cache reader resolves the hint once and validates and uses the
resulting immutable digest, so a concurrent cache update cannot retarget the
operation. Different deployment directories remain concurrent and retain their
own generations. "Finalize" means completing this recoverable cutover; it does
not push an image to a remote registry unless a separate explicit future
operation requests it.

An environment has one generated command surface, named by
`environment.control_script`. The field is optional and defaults directly to
`environment.id`; for example, environment `arbiter` generates `arbiter`, not
`arbiterctl`. This intentionally replaces the existing `ctl` suffix convention.
An explicit value overrides the default. The value must be a portable basename
using letters, numbers, `.`, `_`, or `-`; path separators, whitespace, absolute
paths, `.` and `..` are invalid. Reploy adds platform-specific filename details
such as `.ps1`. All native command triggers and service operations are exposed
through that script, and native triggers may not collide with built-in control
operations such as `up`, `down`, `restart`, `status`, or `logs`.

Because `environment.id` supplies the default filename and also contributes to
install, container, and generated-resource identities, it must satisfy the same
portable-basename rules even when `control_script` is overridden. Reploy also
rejects platform-reserved filenames such as Windows device names.

`environment.executables` is an optional named invocation profile. It binds a
public alias to a qualified component output and defines reusable argument
defaults. `component` names the component that must provide `binary`; together
they identify `<component>.<output>`. It does not declare provider output. The
component provider resolves the binary's backend-specific absolute path: for
example, a Python console script in a virtual environment or a Go/Rust build
artifact.
Reploy does not guess a path or rely on unrelated entries in the container's
`PATH` to select that outer executable. The selected package may itself use an
`env` shebang or perform later `PATH` lookups as part of its trusted runtime
semantics. For a Python entry-point wrapper, the Python provider additionally
verifies that the immediate shebang names the interpreter in the same component
environment.

Provider outputs use one model regardless of whether another provider consumes
them, a command references them directly, `environment.executables` configures
them, or several of these apply. A supplier output with explicit
`discover: true` may be a bounded candidate set; each typed consumer records its
own selected path and evidence without changing the supplier-qualified
identity. An explicit path is a singleton candidate set. Empty output objects
are invalid, and `discover` and `executable` are mutually exclusive. Producing
an output does not expose it publicly. An
unqualified provider requirement selects the first compatible output from lower
to higher image layers among catalogs already published by initialized
suppliers: base first, then provider nodes in deterministic initialization order
with stable component-name ordering within a layer. Selection is frozen before
the consumer resolver runs and is recorded as an edge in the final graph and
local build lock. A provider requirement may set
`supplier` to an active component name or the required `base` component to
override that precedence; dotted identities are diagnostic, not blueprint
syntax. `base` cannot be replaced or reused for another component.
Application executable outputs are matched by name only in the initial design;
general application-output versioning is deferred.

An author-supplied `logical_version_override` describes exactly one terminal
executable group. It may accompany an explicit path or discovery that realizes
one group; discovery of multiple groups makes the scalar override ambiguous and
fails. When a later probe differs from provisional metadata, the typed consumer
deterministically accepts it with a warning only when its compatibility rule
passes, otherwise it fails; the accepted observed value becomes authoritative.

Python components derive executable outputs from the console-script entry-point
metadata of every exact wheel in their resolved closure. The catalog records
the exact script name, entry-point target, and actual owning distribution,
including a transitive dependency. A distribution with no console script
produces no executable output merely because of its package name. Duplicate
console-script claims within one component fail as a physical venv collision;
scripts without console-script metadata are not provider outputs in the initial
design. After materialization, every selected wrapper must exist beneath the
component venv and name that environment's interpreter in its immediate
shebang.

Neither a direct command reference nor `environment.executables` has a typed
compatibility filter, so either may expose only a singleton provider output. A
multi-candidate export must be refined through its supplying provider before
command exposure.

The component reference selects the provider. In the example,
`executables.arbiter_server.component: application` resolves
`components.application`, whose `type: python` selects the Python provider. That
provider finds `arbiter-server` in the exact wheel closure's console-script
metadata, records its owning distribution, validates the generated wrapper in
the materialized Python environment, and returns its absolute path. The
blueprint author references the component and script name but never declares
that the package produces it or supplies a provider-specific filesystem path.

When no reusable executable defaults are needed, a command may reference the
same output directly instead of creating an `environment.executables` entry:

```yaml
commands:
  inspect:
    executable:
      component: application
      binary: arbiter-server
    argv: [inspect]
```

The inline object contains only the qualified output reference. Shared
`argv_prefix`, `argv_suffix`, or default `order` belong in an optional named
executable profile.

Each command selects either an executable-profile name or an inline qualified
output reference. Invocation is assembled from five argv segments: `binary`,
`prefix`, `command`, `forwarded`, and `suffix`.
`environment.executables.<name>.order` defines the default order for a profile,
and an individual command may replace it with its own `order`. The default is
`[binary, prefix, command, forwarded, suffix]`; an inline reference has empty
`prefix` and `suffix` segments.

`order` must contain `binary` exactly once and first. Every other segment may
appear zero or one time, and no segment may be duplicated. Omitting a segment
removes it from the final argv. If user arguments are supplied while
`forwarded` is omitted, Reploy fails rather than silently dropping them. This
permits a command to put forwarded arguments after the suffix—or omit defaults
it does not use—without allowing user input to replace or precede the executable:

```yaml
commands:
  special:
    executable: arbiter_server
    order: [binary, command, suffix, forwarded]
    argv: [special]
```

The segments contain the provider-resolved binary path, executable
`argv_prefix`, command `argv`, forwarded user arguments, and executable
`argv_suffix`, respectively.

For example, command `server` resolves with `[serve]` as its command argv, while
`config_check` substitutes `[config, check]` and any allowed forwarded flags in
the middle. A missing component, missing binary output, or ambiguous output name
is a validation error.

Command triggers must be unique; Reploy selects the longest matching trigger.
`native_command` and `deployed_command` default to false, and a deployed command
must also be native. Before `--`, only flags declared by `forward_flags` are
forwarded. Tokens after `--` are application arguments. Unknown flags fail
clearly and should suggest a close declared flag when possible.

All segments are passed to Docker and the process as exec-form argv. Reploy
never constructs a shell command from blueprint or user arguments. Shell
metacharacters therefore remain ordinary argument characters. Lifecycle actions
may invoke only declared environment commands, and argument forwarding cannot
reach the explicit built-in `reploy shell` operation.

The shared suffix is appropriate for configuration arguments required by every
Arbiter Server command, including paths, deployment phase, and resolved endpoint
addresses. A command implemented by another binary selects another executable
definition with its own prefix and suffix; this does not create another control
script or command namespace.

Executable defaults may reference `reploy.workload`, which is Reploy's normalized
view of the active workload after the selected backend has resolved binding and
publication. It must not reference `docker.*` directly. This lets environment
commands consume effective runtime values without making Docker renderer fields
part of the environment contract.

The conceptual flow is:

```text
components + translations -> closed bundle -> derived/declared output catalog
-> image layers -> validated selected outputs -> optional executable profiles
-> commands -> optional workload
```

### Application Configuration

The initial model does not expose Docker process-environment injection or host
environment passthrough. Application environment variables are application
configuration and should be supplied through managed configuration, such as
Arbiter's environment file under the config path. Blueprint variables remain
interpolation values only; they are not copied into the workload process
environment. Reploy may still use private internal variables in generated
runtime plumbing, but their names are not blueprint protocol.

## Path Mount Modes

`environment.paths` describes the filesystem contract visible to the workload
inside its runtime. This blueprint targets a container runtime, so `container`
paths such as `/mnt/config` and `/mnt/data` are intentionally known to
environment commands. They are not Docker host paths or mount-source paths.

For Arbiter, the environment needs a config path and a writable data path:

```yaml
environment:
  paths:
    config:
      container: /mnt/config
      update: preserve
    data:
      container: /mnt/data
      writable: true
      update: preserve
```

`docker.mounts` should describe how Docker materializes those paths:

```yaml
docker:
  mounts:
    config:
      extends: environment.paths.config
      mode: managed-bind
      source: conf
    data:
      extends: environment.paths.data
      mode: volume
      name: arbiter-data
```

`/mnt` is the one built-in runtime-mount root. A normal container destination
must be a strict descendant of `/mnt`; mounting `/mnt` itself is invalid.
Destinations in one effective plan must be distinct and may not have an
ancestor/descendant relationship. Reploy reserves `/mnt` during image
construction: in the selected base and after every provider layer it must be
absent or an empty real directory, and providers may not persist content below
it.

An application that requires another location opts in explicitly through the
Docker backend:

```yaml
docker:
  additional_mount_roots:
    - /etc/arbiter
```

An additional root must be an absolute normalized path other than `/`, must not
overlap another allowed or protected root, and allows destinations equal to or
beneath it. It widens only the set of possible destinations; it does not permit
shadowing image content. Consequently `/etc/arbiter` is usable when that exact
destination is absent or an empty directory in the image, while adding `/usr`
does not make a mount over the existing `/usr/lib` valid. Additional roots are
runtime/backend configuration and do not enter provider bundle, transaction,
assembly, or image identity.

Possible mount modes:

- `managed-bind`: Reploy manages a path inside the deployment directory and
  bind-mounts it into the container.
- `bind`: bind-mount an explicit user-owned host path.
- `volume`: use a Docker named volume.
- `tmpfs`: use an ephemeral in-memory mount.

`environment.paths.*.update` declares ownership and install/update behavior.
`preserve` protects Reploy-managed user-edited contents, `replace` refreshes
managed contents from staging, and `unmanaged` suppresses both modes: Reploy
mounts user-owned contents but never creates, copies, replaces, or deletes them. The existing
`--replace PATH`, `--replace all`, and `--clean` install options may override
`preserve`, but never `unmanaged`. This policy is environment-owned; the Docker
mount controls how the path is materialized.

The Docker backend must either implement that semantic policy or reject the
mount configuration. Initial support is:

| Docker mount | Allowed `update` values |
| --- | --- |
| `managed-bind` | `preserve`, `replace` |
| `volume` | `preserve`, `replace` |
| `bind` | `unmanaged` |
| `tmpfs` | `preserve`, `replace` (both are no-ops) |

An unsupported combination is a validation error. For a named volume,
`preserve` retains the installed volume and `replace` copies the corresponding
staging volume into a newly prepared installed volume. A direct install uses its
temporary staging-like volume as the source. An external `bind` source must be
an explicit absolute host path and must already exist; Reploy only validates and
mounts it. A tmpfs has no durable user-edited contents; both managed update
policies are satisfied without work, and container recreation discards its
contents normally.
`writable` defaults to `false` when omitted.

### Runtime Mount Integrity

Runtime mounts are subtree overlays, not merges with the verified image
contents. After resolving `extends`, interpolation, and backend-generated
mounts, Reploy normalizes every final container destination and treats it as a
claim over that path and all descendants.

Every blueprint or Reploy-generated runtime mount passes three independent
checks against the exact immutable image:

1. Its destination is beneath `/mnt`, or equal to or beneath one declared
   `docker.additional_mount_roots` entry.
2. The destination is absent or an empty real directory. Existing files,
   symlinks, non-directories, mountpoints, and non-empty directories fail. The
   backend validates existing ancestors without following symlinks and needs
   only an `lstat` plus a one-entry directory read, not a recursive scan.
3. Its overlay subtree does not intersect the protected runtime set below.

Docker-intrinsic kernel and resolver mounts such as `/proc`, `/dev`, `/sys`,
and engine-managed resolver files are outside the blueprint mount plan and are
not additional-root exceptions. Every mount introduced by Reploy's resolved
backend plan remains subject to these checks.

The protected runtime set contains:

- Reploy-internal roots and every exclusive provider root, each protected as a
  complete subtree;
- every exact exclusive provider leaf claim; and
- for every executable output referenced by the runtime plan, its invocation
  path, every recorded symlink or alternatives hop, and its terminal path. This
  includes outputs exported by the immutable base image.

A mount conflicts when its overlay subtree contains a protected path, or when
its destination lies anywhere inside a protected root. Reploy compares
normalized paths without following mount-destination symlinks; executable link
targets are protected because the validated chain records each path explicitly.
The error names the mount, conflicting protected path, and owning component or
Reploy facility.

Executable mount protection covers the selected path and its recorded
symlink/alternatives chain. It does not recursively protect a package's shebang
interpreter, ELF loader, shared libraries, or indirectly executed tools outside
an exclusive provider root. Those dependencies are trusted contents and
behavior of the exact immutable image. An exclusive root, including a complete
Python venv, is protected as a subtree and therefore already covers its
interpreter without a generic execution-chain model.

Reploy performs these checks against the complete effective mount plan
immediately before creating every workload or transient runtime container,
including native commands, lifecycle actions, and `reploy shell`. This catches
backend-generated mounts and plans that differ between staging and deployment.
Allowed, empty application mountpoints remain valid. A mount-plan change does
not invalidate provider layers, but an unsafe plan cannot run them.

This keeps the workload-visible container contract stable while allowing Docker
backend to choose the host storage/mount mechanism. It does not claim that this
exact blueprint can run under a non-container backend. A future backend with a
different runtime filesystem model would need to define whether it supports
`container` paths or introduce another workload-visible path mapping.

Staging and deployed execution use the same materialized image, container-side
endpoint scheme and port, runtime user, mounts, application configuration, and
readiness behavior. Backend identity such as container names, managed host
paths, and published host ports may differ where isolation requires it. Docker
publication does not change an endpoint's scheme; `scheme` is declared once on
the environment endpoint and inherited by every scope.

Reploy resolves only endpoint addresses it controls: the workload bind endpoint
and the backend's published endpoint. External public URLs created by reverse
proxies, DNS, or TLS termination are owned by the application. The application
defines them in its own configuration and exposes them when needed. Reploy does
not model, discover, validate, or probe that external route.

`extends` is explicit Reploy structure, not YAML anchors and not string
interpolation. Reploy resolves the referenced environment object, merges backend
fields on top, and validates the result.

Its semantics are prototype-style concrete value inheritance: the extending
node begins with the referenced node's resolved values and adds allowed
backend-owned values. It is not merely a typed association or an instance of the
referenced node. The public contract is the resolved schema and merge rules, not
a particular implementation technique. A Go implementation may resolve raw
YAML nodes and then decode the result into typed structs; another implementation
could use a structured configuration system.

`extends` rules:

- The value is an absolute blueprint reference beginning with `environment.`,
  such as `environment.paths.config` or
  `environment.workload.endpoints.https`.
- The reference must exist and must name the corresponding kind of object. A
  Docker mount may extend an environment path; a Docker endpoint may extend a
  workload endpoint. Cross-kind references are invalid.
- Resolution copies the referenced environment object, then adds backend-owned
  fields from the extending object. `extends` itself is removed from the
  resolved object.
- The extending object may not replace an environment-owned field. For paths this
  includes `container`, `writable`, and `update`. For endpoints this
  includes `scheme` and `readiness`. Backend-owned fields such as `mode`, `source`,
  `bind`, and `publish` are added by the Docker object.
- A field present on both objects is an error; there is no implicit scalar,
  mapping, or list merge. A future explicit override mechanism can relax this
  rule where needed.
- `extends` references are resolved after structural parsing and before backend
  rendering. Copied fields retain any lazy interpolation expressions, which are
  resolved and type-checked when the backend consumes them. Missing references,
  reference cycles, and references outside `environment` are errors. Although
  cycles are not possible with the initial environment-to-backend-only rule,
  implementations should still reject them rather than recurse.

`environment.workload.endpoints.<name>.port` is the authoritative port on which
the workload listens inside the container. `extends` copies that port into the
Docker endpoint. Docker adds the internal bind address and scope-specific host
publication; it does not redeclare the container port. Readiness probes use the
active published host port.

The existing install port override UX is retained. Initially every Docker
endpoint declares both `publish.staging` and `publish.deployed`. For a
single-endpoint workload, `reploy install --port PORT` overrides the deployed
published port. `--port NAME=PORT` overrides a named deployed endpoint and may
be repeated. Overrides never change the environment/container port or staging
publication. Unknown names, duplicate overrides, and the unnamed form with
multiple endpoints are errors. Installed state records the effective published
address and port together with the unchanged container port.

## Readiness Semantics

Lifecycle endpoint requirements replace the current `docker.health` env-var
protocol and `health_check: {wait: true}` hook action.

In the initial model these probes are startup-readiness gates only. Reploy uses
them transparently to order startup and decide when `after_start` may continue.
They do not define periodic monitoring, restart-on-unhealthy behavior, or a
separate ongoing health lifecycle. Those behaviors should wait for a concrete
use case.

Reploy resolves the active URL from
`environment.workload.endpoints.https`,
`docker.workload.endpoints.https.publish`, and the active deployment
phase:

- staging: `https://127.0.0.1:18075/_health_`
- deployed: `https://127.0.0.1:8075/_health_`

Persisted phase `staged` selects `publish.staging`; phase `installed` selects
`publish.deployed`. The publication keys retain the existing user-facing
staging/deployed vocabulary even though the state values are staged/installed.

Published addresses should normally use loopback, with `127.0.0.1` as the
recommended default. Existing wildcard publication remains supported for
services intentionally exposed beyond the host. For readiness, Reploy converts
`0.0.0.0` to `127.0.0.1` and `::` to `::1`; this changes only the client probe
target, not Docker publication. IPv6 addresses are bracketed when constructing
URLs.

Readiness implies waiting with timeout/retry behavior. A separate `wait: true`
flag is redundant. Reploy retries until the endpoint succeeds or the configured
timeout expires.
`environment.workload.endpoints.<name>.readiness.timeout` defaults to `30s`,
and `interval` defaults to `1s`; both may be configured per endpoint. A timeout
is a lifecycle failure and should report the attempted URL, elapsed time, and
last connection or HTTP error.

The `readiness` block configures Reploy's endpoint probe. Reploy is the client for
that request. Consequently, `tls_verify` controls whether Reploy verifies the
server certificate; it does not describe or configure the workload's TLS server.
It defaults to `false`. Reploy installed the workload and probes the local
published endpoint it controls, so readiness is testing availability rather
than establishing the identity of an arbitrary remote server. This also permits
the common self-signed-certificate case without requiring a separate CA trust
configuration. A blueprint with a locally trusted certificate chain may set
`tls_verify: true` explicitly.

Initial readiness probes support only `http` and `https`, and `path` must begin
with `/`.

The initial HTTP probe sends `GET` to the resolved readiness URL and succeeds only
when it receives HTTP status `200`. Redirects are not followed, and the response
body is ignored. Connection errors, TLS errors, timeouts, redirects, and every
non-200 status are failed attempts and are retried until the readiness timeout
expires.

Canonical lifecycle shape:

```yaml
after_start:
  - requires:
      endpoints: [https]
    actions:
      - environment: [config, check, --live]
```

Rules:

- A lifecycle phase is a list of steps.
- Each step may declare `requires`, `actions`, or both.
- Requirements are satisfied before actions in the same step.
- Steps run in order.
- The initial redesign should avoid shorthand forms. Simpler aliases can come
  later once the canonical semantics are stable.

## Service Workload Lifecycle Events

Install events live under `environment.install` because they apply to
materialization of the environment as a whole. Start and stop events live under
`environment.workload.runtime` because they surround the service workload.
Installing a service may start it, but that
composes the two event groups rather than making `before_start` or `after_start`
install events. One-shot execution remains a command concern, not a workload
type.

Initial install event:

- `after_install`: runs after deployment files, mounts, service definitions, and
  the environment runtime have been materialized successfully, but before any
  requested service start. Actions may inspect or initialize the installed
  deployment but must not assume that the service is running.

Initial runtime events:

- `before_start`: runs after the runtime and workload-visible paths are prepared
  but before the service process starts. This is the appropriate place for
  offline config validation.
- `after_start`: runs after the service process starts. Endpoint requirements
  and live validation belong here.
- `before_stop`: runs while the service is still running, before Reploy requests
  shutdown.
- `after_stop`: runs after Reploy confirms that the service has stopped.

When `reploy install` starts the service, the order is:
installation/materialization, `environment.install.after_install`,
`environment.workload.runtime.before_start`, service start, and
`environment.workload.runtime.after_start`. A failure stops the sequence, skips
all later events, and fails the operation. If installation does not request a
start, the sequence ends after `environment.install.after_install`. A standalone
runtime start or stop runs only its workload runtime events. Each event uses the
canonical ordered-step shape above.

### Install Success Output

`environment.install.success.lines` is an optional list printed only after the
entire requested install operation succeeds, including a requested workload
start and its `after_start` lifecycle. Lines use normal lazy interpolation, so
they may report resolved backend values such as the Docker-published endpoint.
They are reporting only and do not define application configuration or an
external public URL.

The previous special success-variable projection is removed. A blueprint writes
the desired line directly:

```yaml
environment:
  install:
    success:
      lines:
        - "server url: {{ environment.workload.endpoints.https.scheme }}://{{ reploy.workload.endpoints.https.publish.address }}:{{ reploy.workload.endpoints.https.publish.port }}"
```

References are resolved in the installed scope. A missing workload, endpoint,
or backend value is a blueprint validation error rather than an empty string.
An install that does not request a start may still report a resolved published
endpoint because publication is known from the deployment plan; it must not
claim that the service is running.

## Install Scope and Locations

Install scope is explicit user intent, not something inferred from a path,
backend, or privilege level. `reploy install` requires `--scope user|system` and
records the selected scope in installed state so later info, update, and
uninstall operations can explain and validate it.

Deployment phase and install scope are distinct:

- `reploy.phase` is `staged` or `installed` and follows the persisted deployment
  state.
- `reploy.scope` is `user` or `system` and exists only for an installed
  environment.
- A staged environment has no install scope. In particular, there is no system
  staging. Referencing `reploy.scope` while resolving staging is an error.

Applications that use different vocabulary may translate the phase in their
own argument or configuration handling. The previous private
`REPLOY_DEPLOYMENT_SCOPE=staging|deployed` plumbing does not define the new
blueprint namespace.

- `user` is a current-user install and must not require root or administrator
  semantics.
- `system` is a machine install and requires a backend with system lifecycle
  semantics plus an appropriate privilege path.
- Linux system scope uses the systemd backend and may apply
  `environment.install.system.run_as`.
- Linux user scope uses user-owned Docker lifecycle and never creates, chowns
  to, or runs as the configured system account.
- macOS and Windows currently support only user scope through Docker Desktop or
  a compatible user runtime. System scope fails clearly rather than silently
  degrading to user scope.

`system.run_as` is ownership and container-process policy for a system install,
not another install scope. A native-Linux current-user install runs workload and
transient containers as the invoking numeric UID/GID; Docker Desktop instead
uses the Reploy-managed non-root container identity defined above. If
`system.run_as` is present, Reploy reports that it is inapplicable to user scope
along with the non-root image compatibility warning defined above.

### Install Target Defaults

Blueprint authors normally omit target defaults:

```yaml
environment:
  install:
    target: {}
```

Reploy then chooses a target for the active host, backend, and explicit scope:

| Host/backend | Scope | Built-in target |
| --- | --- | --- |
| Linux systemd | `system` | `/opt/{{ environment.id }}` |
| Linux Docker-managed | `user` | `{{ user.data }}/Reploy/installs/{{ environment.id }}` |
| macOS Docker-managed | `user` | `{{ user.data }}/Reploy/installs/{{ environment.id }}` |
| Windows Docker Desktop | `user` | `{{ user.local_data }}/Reploy/installs/{{ environment.id }}` |

A blueprint may provide one global default, per-OS defaults, scope-and-OS
defaults, or a combination:

```yaml
environment:
  install:
    target:
      default_path: "{{ reploy.install_root }}/{{ environment.id }}"
      default_paths:
        linux: /opt/{{ environment.id }}
        user.macos: "{{ user.data }}/Acme/{{ environment.id }}"
        user.windows: "{{ user.local_data }}/Acme/{{ environment.id }}"
        system.linux: /srv/acme/{{ environment.id }}
```

Install target precedence is:

1. CLI `--to`.
2. `default_paths.<scope>.<host_os>`.
3. `default_paths.<host_os>`.
4. `default_path`.
5. Reploy's built-in default for the active host, backend, and scope.

`--to` changes only the path. It never upgrades or downgrades the requested
scope. Product-facing OS keys are `linux`, `macos`, and `windows`; qualified
keys use `user.<host_os>` or `system.<host_os>`.

### Semantic Host Variables

Install target expressions may use this deliberately small host-variable set:

| Variable | Meaning |
| --- | --- |
| `environment.id` | Environment identity. |
| `user.home` | Current user's home directory. |
| `user.data` | Per-user durable application-data root. |
| `user.local_data` | Per-user machine-local application-data root. |
| `system.data` | System-wide application-data root. |
| `reploy.install_root` | Reploy's default install root for the active host/backend/scope. |

Initial host mappings are:

| Variable | Linux | macOS | Windows |
| --- | --- | --- | --- |
| `user.data` | `~/.local/share` | `~/Library/Application Support` | `%APPDATA%` |
| `user.local_data` | `~/.local/share` | `~/Library/Application Support` | `%LOCALAPPDATA%` |
| `system.data` | `/var/lib` | `/Library/Application Support` | `%ProgramData%` |

These variables select the single installed environment directory. They do not
place application config, data, cache, or state directly into host-global
locations. Managed application paths and generated Reploy state remain under
the resolved install target or in explicitly selected Docker volumes.

The active target expression must resolve to an absolute native path. Inactive
per-OS entries are checked for known variables and template safety but are not
rejected for using another platform's absolute-path syntax. Unknown OS/scope
keys, unknown variables, tabs, newlines, and unsafe app-derived traversal are
errors. Windows may accept forward slashes in templates and render native
separators after resolution.

## Current Platform Scope

The install-scope rules above are the authoritative platform support matrix.
They describe Reploy's host integration, not
`blueprint.compatibility.platforms`. The
initial provider backend materializes Linux container targets only; macOS and
Windows below are hosts running those targets through Docker Desktop. In
summary, environment, bundle, command, and workload intent are shared across
platforms, while current host integration is intentionally asymmetric:

- Linux system installs use Docker with a systemd-managed service.
- Linux current-user installs use user-owned Docker lifecycle and control.
- macOS installs are current-user scoped and use Docker Desktop; Reploy does not
  currently generate launchd agents or daemons.
- Windows installs are current-user scoped and use Docker Desktop; Reploy does
  not currently generate native Windows services.

The schema must not expose systemd syntax merely because Linux has the strongest
native service integration today. Future launchd, Windows service, or alternative
container backends may map the same logical workload differently, but they are
not implied by the current design.

## Initial Implementation Scope

This model remains blueprint schema 1. Reploy and its blueprints are unreleased,
so the new environment model replaces the current shape without a schema-version
transition or compatibility layer. Existing development installations may be
recreated or adjusted manually.

Replacing the unreleased schema does not imply removing existing Reploy
capabilities. Existing CLI and deployment behavior is retained unless it
conflicts with an explicit decision in this model. Such conflicts must be
called out and resolved deliberately rather than silently dropping the feature.

The initial implementation supports Python components, Docker, at most one
primary workload, native one-shot commands, and HTTP readiness. Image
materialization uses deterministic, versioned recipes and input-addressed
provider nodes. This promises stable identity for the same exact resolved
inputs, not byte-identical image bytes after every uncached rebuild. Generated
bundle manifests, layer graphs, and BuildKit integration are private
implementation details rather than blueprint schema.

### Private Implementation Backlog

The following capabilities are intentionally deferred from the initial slice:

- Go, Rust, APT/dpkg, RPM, and Alpine component providers.
- Redesign Python bootstrap-interpreter resolution before a system-package
  provider can supply Python. The Python provider should declare a logical
  command and version requirement without naming its source. The selected base
  image and earlier provider layers may export candidate executables; Reploy
  must validate candidates, apply documented lower-layer-first precedence or an
  explicit supplier override, record the selected source, absolute path, and
  logical version, then create the fixed Reploy-owned
  virtual environment. The current Python provider assumes `python` is already
  available on the base image's `PATH`.
- Multiple primary workloads or multi-service composition.
- Configurable, named, or persistent interactive sessions.
- General named checks beyond the initial HTTP readiness contract.
- Native launchd and Windows service integration.
- Alternative container backends.
- Automatic migration of existing development installations.
- Installation-time blueprint variable overrides, if a concrete application
  configuration use case justifies adding the interface and omission semantics.
- Finer-grained invocation templates if a concrete command needs to interleave
  arguments within a segment; initial `order` can only permute whole segments.
- Delete the private legacy app-schema decoder and its characterization
  fixtures before the first release. It remains temporary migration scaffolding,
  is not part of the environment-model contract, and must not gain new callers.
