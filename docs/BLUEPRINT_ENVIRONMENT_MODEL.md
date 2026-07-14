---
status: Active
updated: 2026-07-11
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
- Docker still has real concerns, such as images, restart policy, runtime
  preparation, mounts, and container-specific paths.

The Arbiter blueprint makes the boundary problem visible. Arbiter has:

- an HTTPS workload endpoint
- config and data paths
- environment commands such as `serve`, `config check`, `config check --live`,
  `bootstrap`, `env check`, and `plugins list`
- install/runtime lifecycle checks
- Docker-specific rendering details, including image, container bind address,
  generated component layers and mounted paths

## Desired Ownership Split

Environment-owned:

- environment id and software components
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

- image and container settings
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
-> build closed bundle
-> materialize the backend environment
-> prepare managed paths
-> run after-install actions
-> run before-start actions
-> start the workload
-> satisfy readiness requirements
-> run after-start actions
```

The internal phases are not author-defined lifecycle hooks. Blueprint authors
provide component, path, workload, readiness, and lifecycle intent; Reploy and
the selected backend determine how to realize the prerequisite phases. Docker
may materialize a derived image from bundle layers, while another container
backend may use a different native mechanism without changing the environment
schema.

Inspection should remain lightweight. Dry-run and status output should show the
resolved phase order, commands, endpoint addresses, selected bundle identity,
and locations of generated backend files. Inspection must not imply a graphical
renderer and should not trigger package resolution, image builds, or other
material changes unless the requested operation normally performs them.

Without package resolution, dry-run cannot know a changed environment's future
bundle identity. It reports the currently recorded bundle and materialized
backend identity when available, whether static bundle inputs changed, and the
candidate identity as `unresolved` until stage/update performs resolution.
Generated backend paths are reported as existing, changing, or planned; dry-run
does not need to generate their final contents.

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

Component `type` selects an ecosystem such as `python`, `go`, `rust`, `debian`,
`rpm`, or `alpine`.
The provider is Reploy's internal implementation for materializing that
component; it is not a blueprint object. Components are declarative, not an
ordered shell-script sequence. Reploy resolves materialization order from
provider semantics and explicit dependencies where a component type requires
them. Incompatible requirements are validation errors rather than
last-writer-wins behavior. Every component must be compatible with the selected
backend and runtime.

A component is required by default. An optional component declares selection
metadata and is activated in deployment-local state through the existing bundle
option UX:

```yaml
environment:
  components:
    application:
      type: python
      requirements: [arbiter-server]

    imap:
      type: python
      optional:
        group: plugins
        description: Install the Arbiter IMAP plugin.
      requirements: [arbiter-imap]
```

`reploy bundle options`, `bundle add --name imap`, and
`bundle remove --name imap` retain their current user-facing behavior. Selected
optional components and direct package/source additions are deployment-local
provider inputs; they do not rewrite the blueprint. All active components of a
type are resolved together. A direct addition does not create a public
component object.

An inactive optional component contributes no requirements or outputs and is
never activated by reference. Any reference whose required component, output,
executable, or installed artifact is absent is an error at the earliest phase
that can establish the absence: normally resolution/materialization, or runtime
preflight if installed state has drifted.

### Provider Expansion Gate

Python is the proving component provider. Before Reploy adds another component
provider, it must first implement the common bundle-to-image-layer pipeline
described below. New providers must contribute a closed bundle artifact set and
a deterministic offline layer recipe; they must not introduce provider-specific
runtime volumes, startup installers, or cache lifecycles.

Go, Rust, and system-package examples in this document are future shapes used to
test the generality of that contract. They are not commitments to implement
those providers before the shared layer pipeline exists.

System-package providers fit the same two-phase contract. A Debian provider may
use APT during bundling to resolve a closed `.deb` dependency set, then install
that set offline with `apt-get`/`dpkg` in a generated image layer. RPM/DNF and
Alpine/APK providers would likewise bundle closed, checksummed package sets and
install them offline in provider-owned layers. Package-manager databases and
system files belong in the derived image, never in startup-time runtime caches.

Components of the same type are resolved together into one provider bundle
section and materialization node. Providers declare their internal prerequisites;
blueprint authors do not order layers. Reploy builds a deterministic dependency
DAG, using stable component-name ordering where independent nodes need a
tie-breaker. Independent artifact builds may run concurrently, while final image
assembly follows a deterministic topological order.

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

At most one system-package provider (`debian`, `rpm`, or `alpine`) is allowed in
an environment, and it must match the base image. Each provider declares the
filesystem paths and executable names it produces. Undeclared overlap or two
providers claiming the same path or executable is a validation error; the
initial model has no last-writer-wins or provider-file override mechanism.

`translations` is separate from `components`. A translation does not request
installation. It explicitly maps an ecosystem package identifier to another
source for resolution. A Python translation means "if resolution requires this
distribution, use this local source instead of fetching it from an index." An
unused mapping has no effect on the materialized environment.

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
      type: debian
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
      container: /config
      update: preserve
    data:
      container: /data
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
        - /config
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
  image: python:3.11-slim

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

`docker.image` is the author-supplied base image. Reploy resolves components and
translations during bundling and records a closed, checksummed artifact set for
the target platform and base-image identity. Image materialization performs no
package resolution and requires no package-index or source-checkout access.
Normal start and restart reuse the recorded bundle and image. An explicit stage
or update operation performs fresh resolution and may produce a new bundle
fingerprint.

When `docker.image` is a mutable tag, stage/update resolves it to an immutable
digest and records that digest as the base-layer input. Reploy generates stable
provider layers whose inputs are limited to the corresponding resolved bundle
section, relevant translation artifacts, target platform, and provider recipe
version. BuildKit reuses layers whose inputs have not changed. Runtime-only
settings such as published ports, mounts, restart policy, lifecycle, and
readiness do not participate in image-layer invalidation.

Reploy generates the Docker build definition; blueprint authors do not maintain
a Dockerfile for supported component types. Each provider supplies a fixed
offline installation recipe for its bundle artifacts. Reploy composes those
recipes into ordered BuildKit `RUN` steps, normally one cacheable filesystem
layer per provider or component group.

Bundle artifacts are exposed to a build step with a read-only BuildKit bind
mount rather than copied permanently into the image. Only installed files remain
in the resulting layer. Debian/APT, RPM/DNF, and Alpine/APK installation may run
as root in their build steps. Python, Go, and Rust layers use the permissions
required to populate their final image paths.

Runtime ownership comes from install scope. A current-user install always runs
workload and transient command containers as the invoking user's numeric UID/GID;
it overrides the image's configured `USER`, and system `run_as` configuration is
inapplicable. A system install uses the resolved service account. Generated
application files are immutable and readable/executable by that runtime owner;
only explicitly writable managed paths and Reploy's temporary home are writable.

Before a current-user install, Reploy must clearly report the resolved UID/GID
and warn that the image's configured user will be overridden. The warning must
explain that the image must tolerate an arbitrary non-root identity, must not
require writes to installed or system paths, and receives persistent writes only
through declared writable paths. If system `run_as` configuration is present,
Reploy must report that it does not apply to current-user scope rather than
silently suggesting that it will be honored.

The materialized image is a private Docker-backend resource, not another
environment-schema object. The Docker backend derives its identity from the
deployment directory and bundle fingerprint, records enough state to reuse and
clean it up, and recreates the container when the selected materialization
changes. Other container backends may represent materialization differently.

Reploy owns its generated Docker containers and image references, while Docker
and BuildKit own physical layer sharing and build-cache garbage collection.
Cleanup removes only resources labeled for the relevant directory identity and
never performs a global Docker prune. These naming, tagging, and retention rules
are backend implementation details rather than cross-backend blueprint
protocol.

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

`environment.executables` binds a logical executable to a named component
output and defines reusable argument defaults. `component` names the component
that must provide `binary`. The component provider resolves the binary's
backend-specific absolute path: for example, a Python console script in a
virtual environment or a Go/Rust build artifact.
Reploy does not guess a path or rely on unrelated entries in the container's
`PATH`.

The component reference selects the provider. In the example,
`executables.arbiter_server.component: application` resolves
`components.application`, whose `type: python` selects the Python provider. That
provider resolves `arbiter-server` from its materialized Python environment,
validates that the console script exists, and returns its absolute path. The
blueprint author declares the component and binary name but never a
provider-specific filesystem path.

Each command selects an executable by name. Invocation is assembled from five
argv segments: `binary`, `prefix`, `command`, `forwarded`, and `suffix`.
`environment.executables.<name>.order` defines the default order, and an
individual command may replace it with its own `order`. The default is
`[binary, prefix, command, forwarded, suffix]`.

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
components + translations -> closed bundle -> image layers -> executables -> commands -> optional workload
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
paths such as `/config` and `/data` are intentionally known to environment
commands. They are not Docker host paths or mount-source paths.

For Arbiter, the environment needs a config path and a writable data path:

```yaml
environment:
  paths:
    config:
      container: /config
      update: preserve
    data:
      container: /data
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
not another install scope. A current-user install always runs workload and
transient containers as the invoking numeric UID/GID. If `system.run_as` is
present, Reploy reports that it is inapplicable to user scope along with the
non-root image compatibility warning defined above.

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
In summary, environment, bundle, command, and workload intent are shared across
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
materialization must be deterministic and content-addressed, but its generated
bundle manifest, layer graph, and BuildKit integration are private implementation
details rather than blueprint schema.

### Private Implementation Backlog

The following capabilities are intentionally deferred from the initial slice:

- Go, Rust, Debian, RPM, and Alpine component providers.
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
