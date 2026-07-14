---
status: Draft
updated: 2026-07-14
summary: Subdesign for closed .deb package layers, provider outputs, and Python runtime dependencies.
refines: docs/BLUEPRINT_ENVIRONMENT_MODEL.md
---

# APT/dpkg Provider and Cross-Provider Executable Outputs

This document explores the first provider beyond Python. It refines the provider
expansion backlog in `BLUEPRINT_ENVIRONMENT_MODEL.md`; that document remains the
normative environment model. Its conceptual provider model and initial public
shape have completed review; this draft is now the input to detailed
implementation design.

The APT/dpkg provider is useful on its own for native libraries and utilities.
It also forces Reploy to answer the more general question of how one provider
can consume an executable supplied by another provider. Python is the
motivating case: a `.deb` package may supply the interpreter used to construct
Reploy's Python environment.

## Review Status

The conceptual-design review has no active findings. This draft is ready to
drive detailed design, but it is not yet an implementation specification. The
next phase must map these contracts to concrete interfaces, schemas, state
transitions, backend operations, failure handling, and implementation gates.

The final section lists intentionally open provider-scope choices rather than
unresolved initial public schema. Operational resource limits remain deferred
to implementation policy and do not block the detailed-design phase.

Concrete Go types, state files, Docker operations, migration boundaries, and
implementation gates are specified in
[`APT_PROVIDER_DETAIL_DESIGN.md`](APT_PROVIDER_DETAIL_DESIGN.md).

Public export/import, portable environment archives, and reconstruction of
application-level configuration are deferred. The lock and archive sections
retain future design constraints, but portable transfer is not part of the
initial APT-provider implementation gate.

## Goals

- Resolve `.deb` package requirements inside a container with APT/dpkg, without
  requiring either tool on the host.
- Produce a closed, checksummed `.deb` bundle relative to an immutable base
  image.
- Install that bundle offline into a generated image layer, never at container
  startup and never onto the host.
- Let a package export a logically named executable without requiring the
  blueprint author to know its installed path.
- Let downstream providers consume executable outputs through source-neutral
  logical command requirements.
- Let the selected base image and earlier provider nodes contribute candidates
  to the same command namespace.
- Record both provider-native package identity and consumer-specific logical
  version information.
- Derive deterministic provider dependencies and BuildKit cache invalidation.
- Reuse Reploy's transient execution machinery for restricted output probes.

## Non-Goals for the First Slice

- Installing `.deb` packages on the host.
- Supporting RPM, DNF, APK, or another system-package manager.
- Blueprint-defined APT repositories, keys, credentials, or arbitrary APT
  configuration.
- Modeling dpkg conffile migration, interactive package configuration, or
  package-specific configuration policy. The provider uses one fixed
  noninteractive installation recipe; application configuration remains an
  application/blueprint concern.
- Running `apt upgrade` or installing recommended or suggested packages.
- Resolving packages or probing executables during normal `up`, restart, shell,
  or application commands.
- Accepting blueprint/user-authored shell fragments or user-authored
  Dockerfiles.
- Treating package-manager databases as runtime caches or mounted data.
- Supporting more than one system-package provider in an environment.
- Application-output version matching. Application executables are matched by
  name in the initial design; version constraints may be added when a concrete
  use case establishes how providers should derive and validate them.
- Public export/import, portable environment archives, and portable
  reconstruction of application configuration or runtime data.

## Host, Build, and Runtime Boundaries

The word "system" means the operating-system layer inside the generated
container image. It does not mean the host operating system.

```text
host: Reploy + Docker
  -> temporary APT bundle-resolver container: networked resolution
  -> closed Reploy bundle: .deb files + manifest
  -> generated intermediate image: offline APT/dpkg installation
  -> downstream bundle-resolver container: inspect export + build closed bundle
  -> generated next image: offline downstream provider layer
  -> workload and one-shot containers: no package installation
```

The host may be Linux, macOS, or Windows. It needs Reploy and a supported Docker
runtime, but it does not need APT, `dpkg`, Python, or host root access.

APT and `dpkg` run as root inside provider-owned bundle-resolver/build
containers when their operation requires it. They do not run with host root
semantics. Normal workloads and executable probes use non-root runtime policy
where possible.

### Target Platform Contract

The blueprint declares its supported target container platforms through the
`blueprint.compatibility.platforms` set defined by
`BLUEPRINT_ENVIRONMENT_MODEL.md`. Reploy selects one canonical OCI
`os/architecture[/variant]` target before resolving the base manifest or any
provider bundle. Explicit selection uses `--platform`; otherwise a single
compatible entry or exactly one match for the container backend's reported
native platform may be selected. Reploy never derives the target from its own
process's `GOOS` or `GOARCH`.

The selected platform and exact base-manifest descriptor are common inputs to
APT resolution, artifact download, offline installation, downstream provider
resolution, probing, final image construction, runtime creation, bundle locks,
and import. Every backend request carries the platform explicitly. A base,
artifact set, imported lock, or backend capability mismatch fails before the
affected phase; Reploy does not silently choose another architecture or use an
implicit emulation path.

The private Docker renderer profile pins the Dockerfile frontend by immutable
digest and records every result-affecting backend capability. The selected
platform plus this profile participate in transaction, assembly-cache, lock,
and realized-image identities. A floating syntax tag,
`DOCKER_DEFAULT_PLATFORM`, and backend default platform selection are not
inputs.

### Debian Package Architecture Contract

The APT provider maps the selected OCI platform to exactly one native Debian
package architecture. Initial mappings are:

| Selected OCI platform | Native Debian architecture |
| --- | --- |
| `linux/amd64` and supported amd64 variants | `amd64` |
| `linux/arm64` and supported arm64 variants | `arm64` |
| `linux/arm/v7` | `armhf` |

An environment without an APT component may support other backend platforms,
but the initial APT provider rejects a selected platform absent from this table.
An OCI CPU variant may constrain base/backend selection while still mapping to
the same Debian package architecture.

The base probe runs `dpkg --print-architecture` and requires the result to equal
the mapped native architecture. It also runs `dpkg
--print-foreign-architectures` and requires no configured foreign architecture;
multiarch package installation is outside the initial provider contract.

Every resolved or installed binary-package record has one concrete Debian
architecture. Only the mapped native architecture and Debian's
architecture-independent `all` value are eligible. Reploy rejects every other
artifact or installed closure member before materialization. The internal
package key is `(binary package name, Debian architecture)`; adding the exact
version forms the resolved tuple used by locks, base predecessors, state
snapshots, and final comparison. Author requests remain architecture-neutral
and cannot contain `:architecture` selectors.

### Docker Base Configuration Boundary

The selected base contributes Docker configuration as well as filesystem
contents. Reploy validates that configuration against the normative base-image
contract in `BLUEPRINT_ENVIRONMENT_MODEL.md` before using it in a generated
`FROM` or managed container. The initial backend rejects nonempty `OnBuild` and
`Volumes`; neutralizes inherited entrypoint, command, and healthcheck behavior;
and explicitly supplies every build/runtime user and working directory. Managed
workloads use `SIGTERM` for shutdown. Base environment variables remain runtime
defaults with Reploy-owned values taking precedence. Provider carriers start
with the inspected, identity-bound base environment, then launch materializer,
probe, and bundle-resolver tools under a closed provider-controlled child
environment.

Container root in Docker Desktop remains root only inside the Linux container
and Desktop VM, not macOS root or Windows Administrator. Native-Linux user-scope
containers use the invoking UID/GID. Docker Desktop user-scope containers use a
stable Reploy-managed non-root Linux identity recorded in deployment state, and
Reploy verifies declared mount access for that identity. Linux system scope uses
the resolved service account. The base image's configured `USER` is never the
implicit runtime identity.

## Proposed Blueprint Shape

Simple requirements remain strings. A requirement becomes structured when it
exports a command to another component:

```yaml
blueprint:
  compatibility:
    platforms: [linux/amd64, linux/arm64]

environment:
  components:
    base:
      image: debian:bookworm-slim

    system:
      type: apt
      packages:
        - ca-certificates
        - libmagic1

        - package: python3
          exports:
            python:
              discover: true
              # Optional logical-version override. This is not the .deb
              # package version and is normally derived for recognized command
              # kinds.
              logical_version_override: "3.11"

    application:
      type: python
      interpreter:
        command: python
        version: ">=3.11,<3.12"
      requirements:
        - arbiter-server
```

`system` is an ordinary component name chosen by the author. `apt` selects the
APT/dpkg component provider. `python` under `exports` is a logical output name
and does not need to equal the `.deb` package name or installed executable
name. The Python component requests that logical command without naming its
source.

A pinned package uses provider-native requirement syntax while retaining the
same export map:

```yaml
- package: python3=3.11.2-1+deb12u1
  exports:
    python:
      discover: true
```

An unpinned package resolves to exact artifacts during `reploy build`. A later
explicit build may resolve newer artifacts, just as an unpinned Python
requirement may resolve a newer wheel set. The prepared bundle and generated
image remain input-addressed by the exact resolved artifacts and other
materialization inputs.

### APT Package Request Grammar

An author-facing APT root request uses Debian's install convention but only a
strict Reploy-owned subset:

```text
package-name
package-name=exact-debian-version
```

A scalar package entry and the `package` field of a structured entry use the
same grammar. The name is an exact Debian binary package name: at least two
characters drawn from lowercase ASCII letters, digits, `+`, `-`, and `.`,
beginning with an ASCII letter or digit. The optional version is one nonempty
exact Debian version under Debian Policy, including an epoch or revision when
applicable. Reploy rejects
leading or trailing whitespace, control characters, an empty side of `=`, more
than one `=`, and every string outside those two forms.

APT paths, local filenames, globs, regular expressions, APT patterns,
`package/release`, `package:architecture`, version ranges, dependency
expressions, source-package requests, and option-like operands are not
root-request syntax. A trailing `+` or `-` is name content only after the exact
package-cache match; Reploy never uses APT's appended install/remove operator
semantics. Architecture is selected by the target-platform contract. Debian
dependency metadata may still contain its normal version relationships and
virtual packages; APT evaluates those while closing the transitive dependency
graph.

Reploy parses the request into typed name and optional exact-version fields and
never forwards the author string as an APT expression. Before rendering an
operand, the bundle resolver requires an exact binary-package match in its
current package cache, preventing APT's deprecated regular-expression fallback.
It then generates `name` or `name=version` itself as one positional argv value
after all provider-owned options. A missing exact name or version fails without
asking APT to reinterpret it.

Unpinned and pinned examples are therefore:

```yaml
packages:
  - ca-certificates
  - python3=3.11.2-1+deb12u1

  - package: python3=3.11.2-1+deb12u1
    exports:
      python:
        discover: true
```

Options and deployment-local direct additions normalize through this same
typed request. `reploy bundle add-package COMPONENT REQUIREMENT...` adds APT
roots to a named APT component; `remove-package` removes exact normalized
request entries. `add-source` and `remove-source` are the corresponding local
source operations when that provider feature is supported. Once parsed, none
of these commands can introduce a broader APT grammar.

### Deployment Request Identity

The blueprint is not the whole provider request. Enabled component options and
direct package/source additions form the canonical request overlay defined by
`BLUEPRINT_ENVIRONMENT_MODEL.md`. It lives in existing directory-scoped
deployment state, is updated atomically under the deployment operation lock,
and contains sorted fully qualified selections plus component-qualified typed
provider additions. A directory-path-derived Docker resource identity is not a
request identity.

The effective request identity binds the blueprint fingerprint, canonical
overlay digest, and selected target-platform record. Each provider node binds
only its relevant overlay subset. The bundle lock embeds the complete overlay
and digest. Import into a fresh deployment adopts them; import into an existing
deployment requires an exact match unless a separate explicit operation first
replaces that deployment's request.

When an addition is built from local source, its filesystem path is only an
auxiliary locator retained in local state. Reploy identities use a canonical
source-manifest digest, versioned builder/toolchain profile, selected platform,
and relevant build settings. After the provider validates and emits its normal
raw artifact, the resolved request and lock additionally record ecosystem
metadata and the exact artifact digest. The portable bundle carries that
artifact, not the source tree or its physical path. This same contract covers
local wheels, binaries, `.deb` files, and future source-derived artifacts.

### Public Provider Names and Options

Component, option, and executable-output names use
`[a-z][a-z0-9_-]*`. `base` is the required reserved root component and cannot
be used for another component; `.`, `/`, and `,` remain reserved qualification
and CLI separators. Component names are blueprint-global; option and output
names are supplier-local. Thus
`python_env_3.arbiter_server` is a valid qualified output.

An option contains `description` plus the additive request field owned by its
provider. Python options use `requirements`; APT options use `packages`, with
the exact item syntax documented above. Options cannot replace component type,
interpreter selection, identity, or other structural fields, and cannot contain
nested options.

### Exported Executable Semantics

`exports` is a map keyed by the component-local output name. A recognized typed
consumer may discover an executable within the exact package closure:

```yaml
- package: python3
  exports:
    python:
      discover: true
```

The fields mean:

- `package`: the provider-native package requirement.
- each `exports` key: a stable logical command name contributed by the
  component.
- `discover: true`: explicitly request bounded candidate discovery within the
  exact package closure;
- `executable`: explicitly select an absolute path inside the generated image;
- `logical_version_override`: an optional author-supplied override for the
  logical runtime version normally derived by a typed adapter. It is not a
  `.deb` package version or a downstream version constraint.

Every export must specify exactly one of `discover: true` and `executable`; an
empty export object is invalid. With discovery, the supplier export is a stable
candidate-set identity rather than an already selected physical path. Reploy
lists executable files and links from the declared package's exact dependency
closure once and publishes them under the component-qualified export. Each
recognized typed consumer independently filters that same set without
executing implausible files. After the supplier node is materialized, Reploy
resolves bounded symlink/alternatives chains and groups aliases by terminal
file. The consumer adapter may treat aliases as equivalent and choose a
preferred invocation path; Python normally prefers `/usr/bin/python3` over its
versioned terminal alias.

Each consumer records its selected path and validation record separately from
the supplier candidate-set identity. Different consumers may select different
compatible terminal groups from the same export, for example Python 3.11 and
3.14 environments. Zero compatible groups is unsatisfied; several compatible
groups for one consumer require the author to refine the export or add
`executable`. Untyped application/public outputs require a singleton candidate
set, so they need an explicit path when discovery yields more than one group.
An explicit path makes the export a singleton but does not skip ownership or
capability validation.

`logical_version_override` describes one executable, not an entire
heterogeneous candidate set. It is therefore valid with an explicit
`executable`, or with `discover: true` only when complete discovery produces
exactly one terminal group. Multiple discovered terminal groups make the
scalar override ambiguous and fail before consumer matching; the author must
select an executable path.

Every selected path, chain element, and terminal regular file must be owned by
an exact package in the declared root's resolved dependency closure. Reploy
records the actual owning packages. Symlink resolution is bounded and rejects
cycles or paths escaping the image. An alternatives path is accepted only when
the same closure-attribution rule holds. Unowned maintainer-script-created
files cannot be exported in the initial design.

This record identifies the direct executable file selected by Reploy; it is
not a transitive attestation of every program or library that file may execute.
Package artifacts and their runtime behavior remain trusted inputs. A script
may use a shebang, including `/usr/bin/env`, or later launch other tools under
the package's normal operating-system semantics. Reploy's absolute invocation
path prevents an outer command-name lookup through `PATH`; it does not claim
that the invoked package code performs no internal `PATH` lookup.

Logical output names must be unique within a component. Provider resolution
records the source package even though consumers request only the logical
command. A selected executable may be owned by a transitive package in the
declared root's exact resolved closure; Reploy records that actual owner.

For example, one package may export several commands without changing shape:

```yaml
- package: example-tools
  exports:
    server:
      executable: /usr/bin/example-server
    admin:
      executable: /usr/bin/example-admin
```

There is no scalar single-output shortcut. Keeping one map-only representation
avoids field-ownership ambiguity and scalar/map normalization rules.

An explicit path remains the deterministic ambiguity escape hatch:

```yaml
- package: python3
  exports:
    python:
      executable: /usr/bin/python3
```

`exports` is intentionally distinct from Debian's native `Provides` control
field. Native `Provides` declares virtual package substitution for Debian
dependency resolution. Reploy lets APT honor that metadata and records the
selected concrete package, but native `Provides` does not identify an
executable path or logical runtime version.

### Logical Version Derivation

The APT/dpkg provider should derive provisional logical versions for recognized
downstream command kinds from the exact resolved package closure. This does not
require installing the package or executing its command.

For the standard Debian-family Python packages, the provider can determine
major and minor mechanically. Debian's `python3` package depends on the concrete
`python3.Y` package, which supplies `/usr/bin/python3.Y`; `/usr/bin/python3` is a
symlink to that versioned interpreter. Reploy parses `3.Y` from the resolved
package name and cross-checks the package control version and archive-owned
path.

The typed downstream requirement selects the logical interpretation. A Python
consumer asks for a Python command, so the APT/dpkg provider applies its Python
metadata adapter. Other recognized command kinds may gain similar adapters
without changing the package-resolution contract.

When the provider has no adapter, or its derivation is wrong for a nonstandard
package, the author may supply `logical_version_override` on the export. The
override is recorded as an input and used during dependency matching. If a
downstream consumer has a version constraint and neither derivation nor an
override supplies a logical version, resolution fails with a request for an
explicit logical-version override.

The derived or overridden value is provisional. Once the supplying provider
node is materialized, Reploy starts the downstream provider's disposable bundle
resolver from that intermediate image. The bundle resolver first validates the
installed command, then uses the same verified absolute executable to build its
closed bundle. A missing command or violated version/capability constraint is
an error. When the observed value differs from provisional metadata, Reploy
always applies the consumer adapter's compatibility rule. A compatible value is
accepted with a warning and becomes authoritative; an incompatible value fails.
The consumer owns the definition of compatibility, including whether a
difference changes its ABI.

### Base Component and Exports

Every environment has one required root component named `base`. It selects the
starting OCI image and may contribute commands to the same logical namespace:

```yaml
environment:
  components:
    base:
      image: python:3.13-slim
      exports:
        python:
          executable: /usr/local/bin/python
```

The `base` component is the lowest graph node. It has no `type`, upstream
component, provider bundle, or materialization layer. Reploy resolves its image
to an immutable platform-specific descriptor, validates each export against that
exact image, and records the base digest, absolute path, and probe result. Its
qualified outputs use the ordinary component form, such as `base.python`.

Every base-image export requires an explicit absolute `executable` path. Reploy
does not list the whole image, search its `PATH`, or probe candidate files.
Bounded search within an explicitly declared directory may be considered later
if a concrete need justifies its ambiguity and validation rules. The OCI image
belongs to the environment's root component rather than the Docker runtime
section; another OCI-compatible backend may realize the same base contract.

Exports define the executables that Reploy may resolve, validate, track, and
reference through managed blueprint behavior. They are not a runtime allowlist.
An interactive `reploy shell` uses the materialized image's normal runtime
filesystem and `PATH`, subject to its non-root user and container isolation, and
may invoke undeclared programs already present in the image.

### Logical Command Requirements and Matching

`interpreter.command: python` is a typed logical requirement, not string
interpolation, a `PATH` lookup, or shell source. Candidate suppliers may include
the immutable base image and active earlier provider nodes:

```text
base image export:        python -> /usr/local/bin/python
APT component export:     python -> /usr/bin/python3
consumer requirement:     python >=3.11,<3.12
```

Reploy asks the consuming provider to validate each candidate using the
consumer's version and capability semantics. For an unqualified requirement,
Reploy traverses only catalogs already published by initialized suppliers, from
lower to higher image layers: the immutable base first, then active provider
nodes in initialization order, using stable component-name order within one
layer. The first compatible candidate is selected and recorded; no compatible
candidate is an unsatisfied prerequisite.

A requirement may override automatic precedence with `supplier`:

```yaml
interpreter:
  command: python
  supplier: system
  version: ">=3.11,<3.12"
```

`supplier` is either an active component name or the reserved backend-neutral
identity `base`. When present, Reploy evaluates only that supplier's named
output and fails directly if the supplier is missing, inactive, incompatible,
or does not export the command. `base` cannot be used as a component name. The
field participates in the consumer node identity. Dotted forms such as
`system.python` and `base.python` are diagnostic identities, not blueprint
syntax.

### Executable Output Identity and Exposure

All provider-produced executables use one output model. A supplier output is
either a singleton declared path or a bounded candidate set. It may be
consumed by another provider, exposed through `environment.executables`, or
both; these uses do not create different kinds of output.

Component names are unique within a blueprint, and output names are unique
within their component. Their combination forms the stable qualified identity
`<component>.<output>`, for example `system.python`,
`python314.arbiter_server`, or `python_env_3.arbiter_server`. Repeated local
output names across components are valid. A qualified reference resolves
directly. An unqualified provider requirement uses lower-layer-first compatible
selection. `environment.executables` remains explicitly qualified through its
existing `component` and `binary` fields.

Provider types may declare or derive their output catalogs. The Python provider
derives its initial catalog from the console-script entry-point metadata of
every exact wheel in the component's resolved closure. It records each script's
exact name, owning distribution, and entry-point target; a distribution name by
itself never implies an executable. A console script supplied by a transitive
dependency is valid and retains that dependency as its actual owner. Two wheels
claiming the same console-script name in one Python environment are a physical
venv collision and fail resolution. Wheel scripts without console-script
metadata are not outputs in the initial design.

`environment.executables` is optional alias and invocation configuration. It
references a component output, assigns it a unique public alias, and may define
reusable argument defaults; it never declares what the provider produces. A
command that needs no shared executable defaults may instead name `component`
and `binary` directly in its `executable` field. Because command exposure has no
typed consumer constraint, either form must resolve to one terminal candidate;
otherwise Reploy asks for provider-specific disambiguation. Merely producing an
output does not make it publicly invocable. Reploy records and invokes the
selected verified absolute image path without using `PATH` to select that outer
path. The selected program's own shebang and subprocess behavior remain part of
the trusted package semantics.

Interpreter requirements may use provider-specific logical-version and
capability filtering. Application outputs are matched by name only in the
initial design; general application-output versioning is deferred.

Collision validation applies to qualified identities and incompatible physical
path ownership claims. Multiple references or public aliases for the same
qualified output are not collisions, nor are equal local output names from
different components.

Initial candidate matching uses the provider-derived logical version or
`logical_version_override`. Reploy does not create an intermediate image solely
to discover a version. The image is the real cached upstream provider node
needed by the downstream bundle resolver; that resolver replaces provisional
metadata with an observed result before finalizing its own bundle.

The initial structural graph contains the base root, provider nodes, and edges
implied by explicit `supplier` fields. It does not choose suppliers for
unqualified requirements. Only the consumer-selected candidate establishes an
automatic dependency edge from its supplier to that consumer. Candidate-set
metadata is reevaluated during `reploy build`. Each provider declares a
versioned resolver-dependency profile
covering every upstream input its resolver may observe. Reploy validates that
profile against the current upstream image and canonicalizes the resulting
typed evidence into a dependency fingerprint. An unchanged fingerprint may
reuse the closed bundle even when an unrelated earlier layer changed; a changed
fingerprint reruns the resolver. A provider that cannot completely enumerate a
narrower dependency boundary must include the exact upstream image identity.
The blueprint requirement remains source-neutral.

Selection occurs once, immediately before resolving a consumer during
deterministic provider-node initialization. A node may consider only eligible
outputs from the resolved base and already initialized upstream nodes; a later
or sibling node does not retroactively become its candidate. An explicit
`supplier` establishes the required structural dependency before
initialization. An unqualified requirement examines all eligible initialized
suppliers in documented layer order, freezes the first compatible candidate,
and adds only that selected edge to the final graph and lock. Because this edge
always points to an initialized supplier, automatic selection cannot introduce
a cycle.

An executable candidate supplied by an APT component contains at least:

```yaml
provider: apt
component: system
name: python
package: python3
package_version: 3.11.2-1+deb12u1
architecture: amd64
path: /usr/bin/python3
artifact_sha256: ...
```

After the Python provider validates and selects a candidate, state also records:

```yaml
logical_kind: python
logical_version: 3.11.2
capabilities:
  - venv
```

The supplying backend/provider owns source identity and filesystem ownership.
The Python provider owns Python version parsing and capability validation.
Neither side needs provider-specific knowledge of the other.

## Provider Dependency Graph

Candidate matching produces one of these graphs:

```text
immutable base image export -> Python provider node

immutable base image -> APT provider node -> Python provider node
```

Blueprint authors declare requirements and exports; they do not order image
layers. Reploy first plans a structural graph and rejects cycles formed by
explicit supplier edges. It uses stable names to order otherwise independent
nodes. As each consumer becomes ready, Reploy freezes its automatic selections
from already published catalogs and adds the selected edges to the final graph.

Graph execution initializes nodes in deterministic topological order:

```text
resolve immutable base and validate base exports
-> initialize/materialize system-provider node, if active
-> initialize component-scoped Python environment nodes
-> initialize any higher-level dependent nodes
```

Initialization means resolving the node far enough to publish provisional
outputs, materializing it when a downstream consumer needs a live output, and
validating realized outputs before those outputs become eligible upstream
candidates. This natural ordering supplies candidate metadata before a consumer
selects it; no separate public discovery graph is required.

All active APT components resolve together into one APT bundle node. Each
active Python component represents one independently materialized Python
environment and creates its own provider node. It selects its own upstream
interpreter output, resolves its own closed wheel bundle, owns its own venv
root, and exports its own component-qualified outputs. Several Python components may
select the same interpreter, while others may select different compatible
interpreters.

Each provider type owns its bundle-resolver implementation, and the graph runs
it once for each provider node that needs a closed artifact bundle. The combined
APT node therefore has one invocation, while each Python environment node has
its own invocation. The immutable base image is an input and has no bundle
resolver.

Python wheel downloads and build artifacts may be shared when their complete
artifact identities match, but materialized venv nodes remain component-scoped
because their roots and outputs differ. Independent Python bundles may resolve
concurrently when their semantic graph dependencies permit it. Final image
materialization is sequential in stable node order because an OCI image is an
ordered layer chain.

The backend applies exactly one filesystem layer per provider materialization
node: one combined APT/dpkg transaction layer and one layer for each Python
environment component. One provider-owned POSIX shell script performs all
offline install and verification subprocesses for that node inside one BuildKit
`RUN`. The backend invokes `/bin/sh` explicitly and mounts the script and bundle
artifacts read-only. Mounting does not itself add their raw contents to the
layer; the fixed recipe forbids copying the script or artifact archives into
final image paths and verifies their absence before accepting the layer.

Sequential assembly does not make later siblings semantic dependencies. A
change to an earlier component may require later filesystem layers to be
rematerialized, but their unchanged closed bundles remain reusable without
resolution or source access. Custom sibling-layer merging is deferred until
measured rebuild cost justifies the additional backend contract.

The graph executor must support:

- deterministic topological ordering;
- one closed bundle section and recipe version per provider node;
- interleaved node execution when a downstream bundle resolver consumes an
  executable from an upstream materialized node;
- a disposable bundle-resolver container based on the selected upstream image;
- prerequisite checks against the base or an earlier node;
- typed candidates exposed only through declared logical command requirements;
- component-name uniqueness within the blueprint and output-name uniqueness
  within each component;
- optional supplier overrides and lower-layer-first compatible selection for
  unqualified references across components exporting the same local name;
- collision checks across executable paths, exclusive provider namespaces, and
  Reploy-protected roots;
- invalidation of a changed node and every downstream node, without invalidating
  independent upstream nodes.

## Filesystem Authority and Protected Roots

Filesystem ownership has two explicit provider modes. An exclusive-namespace
provider owns a dedicated root or exact leaf, such as one component-scoped
Python venv. A shared-authority provider delegates overlap, upgrade, replacement,
diversion, and generated-file semantics to one ecosystem package manager. All
active APT components resolve into the same APT bundle and dpkg authority;
blueprint component names do not imply separate filesystem ownership inside
that transaction.

At most one shared system-package authority is active in an environment and it
must match the base image. Two unrelated providers cannot claim the same shared
filesystem domain. A provider that is not part of that authority must use an
exclusive namespace.

Before creating an exclusive root, Reploy validates its complete ancestor chain
without following symlinks. Existing ancestors outside the Reploy namespace
must be real directories rather than symlinks, non-directories, or mountpoints.
The Reploy namespace and provider-root ancestors must either be absent for safe
creation or carry exact ownership evidence from an earlier accepted Reploy
layer. The component leaf must be absent. The backend then creates and claims
the root using no-follow operations. After materialization, the layer change
list must show that every persistent provider write remained within the
exclusive namespace and that no protected path was replaced, deleted, or
escaped through a link.

APT/dpkg owns ordinary paths in its shared system domain. Reploy does not
reinterpret `.deb` payload collisions or attempt to duplicate dpkg semantics
for upgrades, `Replaces`, conffiles, diversions, alternatives, or maintainer-
generated files. It still streams archive listings before execution to reject
claims beneath Reploy-protected namespaces, then inspects the disposable
resulting layer for any actual protected-path change before accepting it. APT
transaction success, dpkg consistency, declared outputs, and the required
capability fingerprints are validated separately.

Protected Reploy namespaces include every provider's exclusive claims and the
internal provider-root hierarchy. Path comparisons normalize entries and never
follow symlink targets; a symlink is a non-directory leaf, and a symlink in an
ancestor chain is rejected. Bounded symlink resolution for executable evidence
remains a separate post-materialization operation.

This contract deliberately does not build an environment-wide file-ownership
index for a package manager's shared domain. Artifact listings and resulting
layer change lists are processed with explicit entry-count and size limits and
need not be retained after their protected-boundary checks. Deployment state
records the exclusive-root ownership evidence, protected-boundary validation,
and declared executable evidence rather than attributing every system file to a
blueprint component.

### Runtime Overlay Validation

The filesystem-authority declarations also protect materialized provider
content from runtime mount overlays. `/mnt` is the built-in runtime-mount root
and is reserved from image content: the selected base must expose it as absent
or an empty real directory, and provider archive and resulting-layer checks
reject persistent changes beneath it. Normal runtime mount destinations must
be strict descendants of `/mnt`.

`docker.additional_mount_roots` may explicitly admit another absolute,
normalized root other than `/`. Additional roots may not overlap each other or
protected Reploy/provider roots. They broaden destination placement only; they
never authorize replacing image content. After the backend resolves the
complete mount plan, Reploy requires every destination to be absent or an empty
real directory in the exact immutable image. It rejects an existing file,
symlink, non-directory, mountpoint, or non-empty directory. Existing ancestors
are checked without following symlinks, and emptiness requires only a bounded
one-entry directory read rather than recursive enumeration.

Every normalized container destination is then treated as a claim over its
entire subtree. Reploy rejects the plan when that subtree contains an exact
exclusive provider leaf, an executable's validated invocation/link/terminal
path, or a Reploy-protected path. It also rejects any destination inside an
exclusive provider root, because the root protects its complete subtree.
Executable-chain protection includes outputs selected from the immutable base
image even when they are not owned by a materialized provider component.

The separately validated executable chain contributes every link and terminal
path to the protected set. The checks run immediately before every workload and
transient runtime container is created, after Reploy-generated and
phase-specific mounts are known. Docker-intrinsic kernel and resolver mounts
are not blueprint mounts and are outside this allowlist. Blueprint mounts never
become provider-owned claims, and changing a safe runtime mount plan or its
additional roots does not change provider-node cache identity.

## APT/dpkg Resolution

Resolution occurs during `reploy build` inside a temporary container created
from the selected immutable base image.

1. Resolve the author-supplied image tag to an immutable digest or image ID.
2. Probe the image for a supported Debian-family identity, `apt-get`, `dpkg`,
   and `getconf`; require `dpkg --print-architecture` to match the selected
   platform's mapped native Debian architecture; require no configured foreign
   architectures; and validate the Docker configuration against the base-image
   contract before any generated build.
3. Treat the image's APT/dpkg executables, package database, configuration,
   source definitions, and trust configuration as trusted base-image state
   bound by the immutable base digest.
4. Create a private, initially empty APT lists directory beneath the resolver
   scratch, including its `partial` directory. Run `apt-get update
   --error-on=any` under `apt-resolve-v1`, with a final
   `Dir::State::lists` override selecting that directory, stdin connected to
   `/dev/null`, and no controlling terminal. The base probe requires support
   for the error mode; any enabled-source acquisition error fails resolution.
5. Resolve all active APT components, including requirements contributed by
   their enabled component-scoped options, together.
6. Use a provider-generated `apt-get --download-only install` transaction with
   `--assume-yes`, `--no-remove`, `--no-install-recommends`, and
   `-o APT::Install-Suggests=false` to download the requested packages and
   complete missing dependency closure.
7. Copy the resulting `.deb` files into a Reploy-managed provider bundle
   directory.
8. Inspect every archive and record package, version, architecture, dependency
   metadata, path, size, and SHA-256.
9. Construct the complete resolved package closure. A package already in exact
   `install ok installed` state in the immutable base and retained by the plan
   is `base` origin. Every package supplied or upgraded by a downloaded `.deb`
   is `bundle` origin; an upgrade also records the exact base predecessor.
10. Reject duplicate bundle storage paths, package architectures other than the
    mapped native architecture or `all`,
    unresolved dependencies, or artifacts not accounted for by the provider
    result. `.deb` payload overlap within the transaction follows APT/dpkg
    semantics; archive entries beneath Reploy-protected namespaces are rejected.

The closed artifact set is a delta relative to the immutable base image. A
dependency already installed in that exact base need not be copied into the
bundle, but its satisfaction is tied to the base digest and recorded provider
inputs.

The lock-level base-image digest binds the complete base filesystem, including
its dpkg database and installed package files. A resolved package record is
therefore a tagged origin rather than a shape that always demands an artifact
hash:

```yaml
base_image: sha256:<immutable OCI image digest>
packages:
  - package: libexample
    version: 1.2.3-1
    architecture: amd64
    origin: base
    status: install ok installed

  - package: application-runtime
    version: 4.5.6-2
    architecture: amd64
    origin: bundle
    artifact: sha256:<exact .deb digest>
    size: 12345
    base_predecessor:
      version: 4.5.6-1
      architecture: amd64
```

`base_predecessor` is present only when the bundle replaces an installed package
from the base. A base-origin member has no `.deb` path, size, or artifact hash;
Reploy never synthesizes one. A bundle-origin member always has all three. The
complete closure records package, version, architecture, origin, and required
dependency metadata for both forms.

APT network access is allowed only during this resolution phase. Repository
signature verification uses the keys and policy already present in the selected
base image. Supporting author-defined repositories or keys requires a separate
trust and secret-handling design.

Resolution and download use only the index files acquired into that private
empty directory. They never consult index files inherited from the base image,
so a failed refresh cannot fall back to stale metadata. Resolver scratch,
including those indexes, is discarded after the closed bundle has been
validated and is not included in the bundle or materialized image.

### Repository Provenance and Secret Boundary

Before publishing a bundle-origin `.deb`, the APT resolver records and verifies
one authenticated provenance chain:

```text
authenticated InRelease, or Release plus Release.gpg
  -> signed checksum entry for the selected Packages index
  -> exact Package/Version/Architecture/Filename/Size/SHA256 stanza
  -> downloaded .deb bytes
```

The package record carries the signed release-metadata digest, the selected
index identity and digest, and those exact stanza fields. Its artifact path,
size, and SHA-256 must match the stanza. A missing link, mismatch, weak or
absent artifact digest, or package not represented by the authenticated index
fails resolution. Base-origin package records need no synthetic repository
chain because the immutable base-image digest binds their installed state.

Repository provenance stores only a credential-free source descriptor: a
normalized public scheme, host, port, and path when they can be separated
safely, plus the selected suite and component. URI user information, query and
fragment data, authorization headers, APT authentication entries, tokens,
passwords, private keys, and secret environment values are never identity,
lock, state, label, or cache metadata. When Reploy cannot safely separate a
public coordinate from access material, it omits the coordinate and records an
opaque source identifier derived only from authenticated release/index
digests. Access credentials do not affect bundle identity.

The resolver may let the base's APT configuration obtain credentials, but
Reploy does not copy that configuration into provider output. Raw APT
stdout/stderr and source declarations are secret-tainted and are not persisted.
Before display or diagnostic logging, provider-owned filtering removes URI
credentials and other recognized secret forms; output that cannot be rendered
safely is replaced by a structured phase/error code and a redacted source
identifier. A future blueprint-defined repository feature must transport
credentials through an ephemeral backend secret mechanism rather than argv,
environment, locks, or image layers.

### APT Base-Image Trust Boundary

Reploy does not attempt to sanitize or replace APT configuration supplied by
the immutable base image. The base image is already a trusted execution
boundary: it supplies `apt-get`, `dpkg`, their shared libraries, package state,
source definitions, keyrings, configuration fragments, and any configured APT
hooks. The exact base-image digest binds all of that state. Reploy cannot make a
malicious base image safe merely by replacing `apt.conf`, and doing so would
also risk breaking legitimate distribution-specific behavior.

Caller state is outside that boundary. Resolver and materialization children
therefore use the closed provider environments below and never inherit a
caller-supplied `APT_CONFIG`, proxy, or package-manager override. Reploy's own
`APT_CONFIG` file is a fixed additive provider configuration; it does not hide
the base image's normal `/etc/apt/apt.conf` and `apt.conf.d` configuration.
Required noninteractive and safety settings are rendered as final explicit
arguments where ordering matters. Materialization additionally has no network
and accepts only the locked local artifacts.

The base digest transitively covers inherited APT configuration; Reploy does
not duplicate every base configuration file in transaction identity. The fixed
provider profile, generated additive configuration, argv, resolved artifacts,
and results remain explicit identity and lock inputs. This is a reproducibility
and input-isolation contract, not a sandbox against code contained in the base
image or installed packages.

## Offline APT/dpkg Materialization

The APT node installs the closed bundle into an image derived from the same
immutable base identity used for resolution.

Materialization must:

- mount the provider bundle read-only;
- require `dpkg --audit` to report a clean package database before installation
  and snapshot the canonical installed package/version/architecture/status set;
- verify every base-origin package still has its exact recorded tuple and
  installed state in the selected immutable base;
- create a private, initially empty APT archive-cache directory beneath bounded
  transaction scratch and select it with a final `Dir::Cache::archives`
  override, so no archive cached in the base image is eligible;
- immediately before installation, validate that every mounted bundle-origin
  `.deb` is a regular file with its exact locked path, size, and SHA-256;
- pass every exact `.deb` path as a separately validated positional argument;
  the provider-owned script consumes quoted arguments and never uses a glob;
- disable network access;
- invoke APT only as the offline local-package transaction driver, with
  downloads and removals prohibited;
- use noninteractive package configuration and preserve conffiles already
  modified in the immutable base image;
- run package installation only while building the provider layer;
- leave package databases and installed system files in the derived image;
- leave `.deb` artifacts outside the final image because they came from a
  read-only build mount.

### Internal Materialization Recipe Contract

`MaterializationStep` is a private provider-to-backend contract, not blueprint
schema. Blueprint authors declare component intent; a provider returns a fixed,
versioned recipe. For the initial Docker backend, the complete provider-node
recipe compiles to exactly one step and therefore one BuildKit `RUN`.

That step declares:

- provider recipe version and provider-node identity;
- the complete provider-owned POSIX shell script and its content digest;
- explicit runner argv consisting of `/bin/sh`, the POSIX `-eu` options, the
  mounted script path, and separately validated positional arguments;
- typed executable operands used by the transaction;
- a fixed working directory;
- a provider-owned child-environment profile identifier/version and its exact
  fixed bindings;
- one build user for the transaction, including root where package installation
  requires it;
- an explicit network policy, with materialization fixed to `none`;
- the read-only trusted-script mount and one or more provider-node-scoped
  read-only artifact mounts; and
- the expected final image-user semantics, which the backend must preserve
  after any root build step.

The script source is recipe code owned by Reploy and the provider. Blueprint
text, package requests, artifact paths, and other resolved values are never
interpolated into it. Dynamic data reaches the script only through the validated
argument vector, controlled environment, or hash-verified files inside declared
artifact mounts. The script quotes expansions used as data and does not use
`eval` or shell globs. Blueprint authors cannot supply shell fragments.

Ordinary dynamic data never occupies command position. A private
`ValidatedExecutableInput` may do so only when it represents a fixed provider
prerequisite or a selected base/provider output already validated against the
exact immutable upstream image. The record contains its recipe role, origin or
supplier-qualified identity, upstream image digest, invocation path, complete
bounded link chain, terminal path, ownership and file digest, and typed
compatibility evidence. The backend verifies that record and passes the
invocation path as one positional value; the script executes the quoted absolute
path directly, without `PATH`, `eval`, `sh -c`, or source interpolation.

A private `GeneratedExecutableOperand` declares a recipe role, exact
provider-derived invocation path beneath a protected provider-owned root, and a
validation policy before the transaction runs. The script may invoke it only
after the generating operation and after verifying its bounded link chain,
regular executable terminal, and provider ownership. Its terminal may be a new
generated file or an already validated upstream executable such as the
bootstrap interpreter. No blueprint, package, artifact, or other ordinary data
can select either an upstream or generated command.

The script resets `IFS`, `PATH`, `umask`, and other provider-defined shell state
before doing work. It invokes tools by validated absolute path through an
absolute clean-environment launcher equivalent to `env -i`; each child receives
only its versioned provider-owned environment profile. Every mandatory
subprocess is a simple command whose status is checked explicitly. A pipeline
or conditional cannot stand in for that check, so POSIX `set -e` behavior is
only defense in depth. Dynamic values are parsed against their field grammar
before rendering and are placed after the invoked tool's own `--` separator
when it supports one; otherwise the provider accepts only values whose typed
grammar cannot be parsed as options.

The initial APT provider uses two fixed profiles and does not transport an
arbitrary environment map. `apt-resolve-v1` covers networked index refresh,
dependency closure, and artifact download:

```text
PATH=/usr/sbin:/usr/bin:/sbin:/bin
LC_ALL=C
LANG=C
HOME=/root
TMPDIR=/tmp/reploy-apt-resolve
DEBIAN_FRONTEND=noninteractive
APT_CONFIG=/tmp/reploy-apt-resolve/apt.conf
```

`apt-dpkg-v1` covers offline installation and post-install verification:

```text
PATH=/usr/sbin:/usr/bin:/sbin:/bin
LC_ALL=C
LANG=C
HOME=/root
TMPDIR=/tmp/reploy-apt-dpkg
DEBIAN_FRONTEND=noninteractive
APT_CONFIG=/tmp/reploy-apt-dpkg/apt.conf
```

The restricted bundle-resolver runner and the materialization script each set
`umask 022` before launching a child. For each profile, its carrier prepares the
declared `TMPDIR` as empty provider-owned transaction scratch, writes the fixed
additive provider APT configuration there, and removes the scratch before
accepting the bundle-resolver result or image layer. The generated file does
not replace the base image's normal APT configuration tree. The scratch size
limit and exact generated configuration bytes are transaction inputs. Neither
profile inherits proxy, locale, `DPKG_*`, debconf override, shell-option, host,
or base-image environment variables. Repository source and trust configuration
remain filesystem state of the immutable base image rather than forwarded
environment. Reploy's own mandatory tools still use validated absolute paths;
the fixed `PATH` exists for APT/dpkg children and maintainer scripts that locate
standard system tools.

The provider resource-limit design still defines the numeric scratch and other
limits; whatever values it selects become profile/transaction inputs.

Each profile identifier, version, exact bindings, umask, scratch policy, and APT
configuration content participate in the corresponding bundle-resolver or
materialization transaction identity and lock. Changing any provider-owned
fixed value requires a new profile version and invalidates reuse; the recorded
digests detect unversioned drift rather than legitimizing it. A future provider
may define another fixed versioned profile; arbitrary blueprint-provided or
unframed dynamic environment transport is outside the initial contract.

Artifact mount sources are resolved provider artifacts, not arbitrary host or
blueprint paths. The initial image backend reserves the otherwise absent
top-level path `/.reploy-build` for transient trusted-script and artifact
mounts. Every mount destination is a normalized, disjoint descendant of that
root, contains no `..` component, and is checked without following a
destination symlink. The root and its contents are removed with the build
mounts and never enter the resulting layer. A step cannot request secrets,
undeclared writable mounts, or network access. The backend must validate and
render every declared field exactly. If a backend cannot support a field or
combination, it rejects the recipe before building; silently ignoring a
security or execution field is invalid.

Every build mount has a canonical logical descriptor. An artifact-bundle mount
records its provider-node role, fixed container destination, read-only policy,
and manifest-root digest. The root is a canonical digest over the ordered
artifact records already produced by environment build or a future import:
normalized
relative logical path, byte size, and verified content digest. The
`artifact-mount-manifest-v1` record sorts entries by logical-path bytes and uses
the existing `canonical-json-v1`/`sha256` identity rules. A trusted-script mount
uses its fixed role and destination, read-only policy, and existing exact script
digest. Physical source paths—including cache, temporary, staging, deployment,
and host installation-directory paths—are late-bound backend locations and
never enter either descriptor or the materialization cache key.

Environment build hashes artifact bytes while acquiring them; a future import
would verify them once while streaming them into Reploy-managed storage. Reploy then
atomically publishes the bundle under its manifest root as an immutable input.
Normal identity and cache lookup reuse that root without rereading or rehashing
artifact bytes. The backend still reads mounted content as required to build the
layer and may perform its own mandatory cache processing; Reploy does not add a
second byte scan merely to reconstruct an identity it already verified.

Provider steps never inherit the base image's configured user, working
directory, entrypoint, command, or healthcheck. Docker necessarily starts the
transaction shell with the immutable base image's inspected environment. That
carrier environment is trusted base input and is bound to the transaction
identity; the script does not pass it wholesale to provider subprocesses. A
base environment value cannot enter an initial provider child. A future profile
that requires one needs a separately versioned typed transport and must declare
and fingerprint the value. Neither initial APT profile declares such inputs.

### Exact-Prefix Validation

The initial image backend defines one private `prefix-validation-v1` mechanism.
Its input is an immutable prefix image's root-filesystem subject plus a
canonical requirement profile assembled from the backend baseline and the
provider operations that will consume that prefix. The subject is the canonical
digest of the ordered OCI `rootfs.diff_ids` sequence. Its validation-record key
contains both the subject and the complete versioned profile digest. It is
internal execution state, not blueprint schema; other image configuration that
affects a transaction remains bound separately to that transaction identity.

A validation record is a content-bound observation made by Reploy, not a
signature, package-trust proof, or attestation. Reploy trusts one only while the
local container backend and its content-addressed objects are trusted. An actor
that can forge both an image and its Reploy-reserved metadata is outside the
initial local-backend threat model.

The backend baseline requires executable, validated absolute paths for a
POSIX-compatible `/bin/sh` and a clean-environment launcher. APT profiles add
the exact absolute `apt-get`, `dpkg`, and `getconf` prerequisites needed by
their bundle resolver or materializer. The same profile may include other typed
executable inputs and backend capabilities required by the next operation. The
probe validates each declared invocation path, complete bounded link chain,
regular executable terminal, ownership and file fingerprint, and required
capability. Each executable requirement has a private validation policy:
`compatible` may acquire a new record after a layer legitimately updates its
implementation, while `unchanged` must match the record named by the profile.
Backend carrier and APT tool prerequisites initially use `compatible`; selected
provider outputs may use the stricter policy under their lifetime rules.
`getconf` is invoked without `PATH` lookup to obtain `ARG_MAX`.

The probe also requires the reserved `/.reploy-build` root to be absent in the
exact prefix image. This one absence check makes every subsequently rendered
mount target unshadowed and symlink-free at the boundary; Reploy does not scan
the whole filesystem or mounted artifact contents. A provider transaction that
persists the reserved root is therefore rejected when its result is validated.

Before an exact prefix is accepted after uncached or lock-driven materialization,
Reploy requires its backend-baseline validation record. Before a bundle
resolver or materializer consumes the prefix, Reploy requires the composite
profile for that operation. A cache miss runs one bounded, read-only,
networkless, noninteractive probe for that exact image/profile pair; a hit
reuses the matching record without repeating checks for each consumer. When the
baseline and operation profiles are identical, acceptance and consumption
share the same probe. A new filesystem layer has a new root-filesystem-chain
fingerprint and cannot use a record inherited from the preceding layer. Any
failed or incomplete requirement blocks the operation and all downstream nodes.

A provider prerequisite is validated against the exact prefix immediately
before the consuming operation. That consumer-use guarantee ends when the
operation ends unless a later consumer selects the output again. Each later
consumer validates it against its own immediate prefix. An output referenced by
a command, directly or through `environment.executables`, is additionally
validated against the final immutable environment image after every provider
layer and before publication. An earlier provider-prefix record never
authorizes final command exposure.

The base image's Dockerfile `SHELL` setting is ignored because the runner path
is explicit. A future backend-native carrier requires a separately versioned
recipe schema and capability profile; it may preserve blueprint intent but
cannot silently reinterpret this POSIX-script recipe. A backend that cannot
provide the declared atomic transaction rejects it.

The canonical rendered transaction identity includes the script digest, recipe
version, runner argv and positional arguments, inspected carrier environment,
controlled child environment, working directory, build user, network policy,
canonical logical mount descriptors and their existing manifest/script digests,
complete validated executable-input records, generated executable declarations
and validation policies, and final image-user semantics. Mounted inputs are
atomically published immutable objects identified before execution. The
transaction digest is included in the assembly cache key and lock, so a script
or execution-input change cannot reuse an earlier layer. A generated operand's
actual link chain, terminal, ownership, and file digest become realized evidence
bound to the resulting immutable image after materialization.

Although the mounted script may perform several provider-controlled
subprocesses, the backend commits one filesystem layer for the complete node
transaction. Transaction failure produces no accepted node layer.

### Bounded Argument Vectors

The initial `apt-argv-budget-v1` policy keeps one complete APT transaction and
does not split the `.deb` closure. Before rendering either a networked bundle
resolver or materialization operation, the backend probes that operation's
actual Linux execution environment's `ARG_MAX` through a validated absolute `getconf`
prerequisite through its exact-prefix validation profile. Each probe uses
the same carrier and resource-limit policy as the operation it covers; a backend
that cannot preserve that equivalence rejects the operation. Probe values are
execution-capability evidence, not blueprint fields.

Reploy checks the networked `apt-get --download-only install` invocation, the
provider-script invocation, and its final offline `apt-get install` invocation.
For each it computes the complete encoded footprint from the exact argument and
environment vectors, including terminating zero bytes, plus the target-platform
pointer-array footprint. The download calculation uses `apt-resolve-v1`, the
script calculation uses the inspected carrier environment, and the offline APT
calculation uses `apt-dpkg-v1`. Each invocation must fit within half of the
limit probed for its execution environment; the 50 percent reserve covers
kernel/runtime bookkeeping and avoids depending on a boundary value. The
policy also limits every individual argument or environment string, including
its terminating zero byte, to 64 KiB so it remains below Linux's independent
per-string ceiling on supported systems. The calculation uses request or
manifest paths and lengths and never reads artifact content. The policy
identifier and formula are provider-recipe inputs; a measured machine value
does not change semantic bundle or cache identity when the invocation remains
admissible.

An over-budget operation fails before the affected bundle resolver or BuildKit
execution with the provider node and phase, argument/artifact count, calculated
bytes, probed `ARG_MAX`, and permitted budget. The initial design does not chunk
APT transactions because dependencies, `Pre-Depends`, cycles, and
maintainer-script timing can cross arbitrary chunk boundaries. If a real bundle
exceeds the budget, a future recipe may construct an isolated local APT
repository from the same verified `.deb` set and install exact root
package/version requests from it; that added machinery is deferred until
measured need.

The initial Docker recipe runs `apt-get install` with every local `.deb` path
listed explicitly, `--assume-yes`, `--no-download`, `--no-remove`,
`--no-install-recommends`, and `-o APT::Install-Suggests=false` under BuildKit
`--network=none`. APT remains responsible for dependency and `Pre-Depends`
ordering while `dpkg` performs unpacking and configuration. The network
boundary and empty private archive cache, rather than an assumption about APT
behavior, guarantee that the transaction cannot fetch or inherit a missing
package. Every eligible archive is therefore one of the hash-verified locked
positional inputs; a transaction requiring anything else fails.

### Noninteractive APT Execution

Every APT invocation in both phases receives stdin from `/dev/null` and runs
without a controlling terminal. The resolution download transaction and the
offline installation transaction both pass `--assume-yes`; `apt-get update`
and read-only verification commands use the same closed I/O policy without a
meaningless confirmation option. Every provider-generated `apt-get` invocation
passes `-o Dpkg::Use-Pty=0` as a final command-line override, so base APT
configuration cannot cause APT to create a dpkg progress pseudo-terminal.

`--assume-yes` answers only ordinary APT confirmation. Reploy never enables
`--force-yes`, `--allow-unauthenticated`, `--allow-insecure-repositories`,
`--allow-remove-essential`, `--allow-change-held-packages`,
`--allow-downgrades`, `--allow-releaseinfo-change`, or an equivalent override.
Both profiles append explicit false command-line configuration overrides for
those behaviors after every inherited APT configuration input; the exact
versioned override set is part of the generated argv and transaction identity.
APT's built-in safety aborts and the provider's `--no-remove` rule therefore
remain authoritative. A repository, package, maintainer script, or hook that
bypasses the declared noninteractive mechanisms and requires terminal input is
unsupported and fails the bundle-resolver or materialization transaction;
resource deadlines remain a separate safeguard for code that hangs instead of
failing.

The `apt-dpkg-v1` environment and the installation transaction's exact
`-o Dpkg::Options::=--force-confdef` and
`-o Dpkg::Options::=--force-confold` arguments provide the debconf/conffile
portion of unattended operation and preserve deliberate base-image
modifications. This policy is backend/provider behavior, not a blueprint field.
Reploy does not model or migrate package configuration.

Maintainer scripts, including service-start attempts, may run in the temporary
materialization container. Processes do not survive the build step, although
their filesystem changes become part of the provider layer. A package that
hangs, requires interaction, or fails configuration is unsupported by the
initial recipe and fails materialization.

After installation, Reploy delegates consistency checks to the package tools:

- require the install transaction to exit successfully;
- run `dpkg --audit`;
- run `apt-get check` under the same network isolation;
- query package status and compare exact package, version, and architecture
  against the resolved manifest; and
- compare the complete installed-package snapshot with the pre-install state.
  No package may be removed. A package may be added or changed only when it is a
  bundle-origin member of the resolved closure, and an upgrade must change from
  its recorded base predecessor to its exact resolved tuple. Base-origin and
  unrelated package tuples and status must remain unchanged.

Any failure stops the provider graph before a downstream bundle resolver starts.
The error includes the package set, provider phase, and captured APT/dpkg
output.

`.deb` packages may execute `preinst` and `postinst` maintainer scripts during
installation. Those scripts are part of the trusted package and execute with
image-build privileges. Reploy cannot treat an installed package as passive
data. Package-manager configuration must complete during materialization;
application-specific configuration may run later through the existing
application configuration and lifecycle mechanisms.

No package installation occurs during workload startup, one-shot commands,
shell sessions, restart, readiness, or lifecycle actions.

## Executable Discovery, Validation, and Probing

Resolution uses archive contents, control metadata, known package policy, and
any declared absolute executable path to establish provisional candidates and
logical versions without installing the package. Discovery is bounded to the
exact dependency closure and is available only through a typed consumer
adapter. It never searches the whole image or executes arbitrary closure files.

After the APT node is materialized, Reploy validates candidate paths,
bounded symlink chains, terminal file types, executability, and ownership
against installed package state. Equivalent aliases resolving to one terminal
file form one candidate group. Only plausible candidates selected by the typed
adapter are probed.

The consuming provider then applies semantic validation as the first operation
in its disposable bundle resolver. For Python, it can execute:

```text
/usr/bin/python3 -I -S -c
  import sys; print(".".join(map(str, sys.version_info[:3])))
```

It then checks the requested Python version constraint and separately verifies
that the interpreter can create a virtual environment.

Package and logical versions are distinct:

```yaml
package_version: 3.11.2-1+deb12u1
logical_version: 3.11.2
```

`.deb` package metadata supplies the package version. For standard Debian Python
packages, the resolved version-specific package name supplies a reliable
major/minor logical version before installation. Executing the selected
interpreter later supplies the authoritative full Python runtime version.
Inferring the full logical version only by removing the Debian revision is
insufficient because epochs, backports, distribution revisions, and version
conventions are not the Python runtime contract.

### Probe and Bundle-Resolver Execution

When a downstream provider needs only to validate an output, Reploy should reuse
the lower-level primitive beneath one-shot commands rather than model probes as
public environment commands. The primitive accepts an image reference,
exec-form argv, user, timeout, output bound, and isolation policy.

```text
RunImageCommand
  image: intermediate APT node image
  argv: provider-controlled absolute executable and probe arguments
  user: non-root
  network: none
  root filesystem: read-only
  scratch: one private size-bounded tmpfs at /tmp/reploy-probe
  HOME: fresh directory beneath scratch
  capabilities: none
  other mounts and secrets: none
  timeout and output: bounded
  cleanup: always
```

The scratch tmpfs is the only writable probe path. It is mounted with `nodev`
and `nosuid`, contains no bundle, deployment, or persistent provider data, and
is discarded with the probe container on success, failure, timeout, or
interruption. A capability probe may write only beneath this root. The Python
probe creates a temporary virtual environment there to verify actual `venv`
creation rather than merely importing the module.

When a downstream provider must execute the output to build its bundle, Reploy
instead starts a disposable bundle-resolver container from the same image:

```text
RunBundleResolver
  image: selected upstream provider-node image
  executable: verified provider output
  user and working directory: explicit provider-owned values
  root filesystem: read-only
  network: provider resolution policy
  inputs: declared read-only bundle/source mounts
  outputs: one initially empty private writable artifact mount
  scratch: bounded temporary storage
  environment: fixed versioned provider profile
  stdin and terminal: /dev/null and none
  host capabilities: none
  timeout and output: bounded
  cleanup: always
```

The bundle resolver validates the executable before performing network or
source work, then uses it to produce the raw artifacts for the closed downstream
bundle. The initial Docker implementation uses a disposable container. A
throwaway BuildKit stage may implement the same contract later. Neither probes
nor bundle resolvers can modify the upstream image, and they remain internal
provider-graph operations rather than public commands.

The host creates the output directory as an empty, private Reploy temporary
directory outside deployment and cache publication paths and confirms that it
is empty immediately before mounting it. It is the resolver container's only
writable host-backed mount. After the resolver exits, Reploy stops the container
and detaches the mount before examining the directory, so resolver code cannot
race host ingestion.

Host ingestion derives the canonical artifact manifest; it never trusts a
resolver-supplied filesystem manifest by itself. Starting from an opened output
directory descriptor, it enumerates normalized relative names without following
links. Initial APT and Python bundle outputs are regular raw `.deb` and wheel
files; future providers must declare their permitted raw artifact kinds. Reploy
rejects absolute or traversing names, unaccounted files, symlinks, hard links,
directories outside the declared layout, sockets, devices, FIFOs, duplicate
normalized names, and names that alias under the bundle's portable
case/normalization rules.

Entry-count, per-file, total logical-byte, path-depth, and path-length limits are
checked while streaming. Reploy opens each accepted file relative to the output
directory with no-follow semantics, verifies that it remains the same regular
single-link file, and streams its bytes through provider-specific artifact
inspection and SHA-256 into private temporary content-addressed storage. APT
control data or wheel metadata must account for every accepted artifact. Sparse
input is charged by logical size and copied as verified bytes rather than
retained as a resolver-controlled sparse object.

Only after every artifact and manifest record validates does Reploy atomically
publish the immutable manifest-root object. Failure removes the temporary
objects and publishes no bundle. Bundle build, archive import, and future local
artifact producers reuse this safe-artifact publication primitive after their
source-specific framing checks.

Executing a package-provided binary is a security-sensitive action, but it is
not the first package-code execution: APT/dpkg installation may already run
maintainer scripts. Probe isolation is defense in depth for build integrity and
resource control, not a substitute for repository trust.

## Python Provider Consumption

The Python provider no longer assumes `python` exists on the base image's
`PATH`. It declares a logical command requirement, validates candidates, and
consumes the selected executable as its Python runtime.

An omitted Python `interpreter` field normalizes to an unconstrained logical
`python` command requirement. The existing base-first and provider-graph order
selects its supplier. The explicit form is required only when the author wants
a logical-version constraint, a particular supplier, or a future nondefault
capability.

The provider must:

1. Normalize an omitted interpreter to logical command `python`, then require
   that command with any explicit supplier, logical-version, and capability
   constraints.
2. Evaluate candidates exported by the base image and earlier provider nodes.
3. Reject no-match and unresolved multi-match results.
4. Execute the resolved absolute path, never a `PATH` lookup.
5. Parse and validate the actual Python version against the consumer constraint.
6. Validate the capabilities needed by its recipe, initially Python 3 and
   `venv` support.
7. Record the selected source, absolute path, logical version, and capabilities.
8. Encode that complete evidence as the Python transaction's
   `ValidatedExecutableInput`; the interpreter path is never ordinary data.
9. Use the same absolute interpreter in the disposable bundle resolver to
   resolve and build the closed wheel bundle for its actual version and ABI.
10. During offline materialization, invoke that typed, quoted absolute
   interpreter to create the
   component-scoped Reploy-owned environment, conceptually:
   `/opt/reploy/providers/python/<component>`.
11. Validate the generated environment interpreter at its declared path, then
    use it as a provider-generated executable operand to install the closed
    wheel bundle offline and record its realized link/terminal evidence.
12. Derive the component output catalog from the exact wheels' console-script
    entry-point metadata. For each output selected by a provider consumer,
    direct command reference, or `environment.executables` profile, verify that
    the generated wrapper exists and its immediate shebang names the interpreter
    in that same component environment. Other package-supplied scripts retain
    their package-defined execution semantics.

The exact root encoding remains a private Python recipe decision and must map
the globally unique component identity to a safe deterministic path. It must
also keep the generated interpreter path space-free and within the target's
direct-shebang length limit; a bounded digest segment may represent a longer
component name. The bootstrap interpreter path is a resolved upstream provider
output. A change to the component, interpreter output, wheel bundle, or Python
recipe version invalidates that component's Python node without invalidating
independent Python nodes.

For the first slice, the Python interpreter is a resolution-time and
materialization-time prerequisite. Reploy materializes the real cached upstream
node, starts a disposable Python bundle resolver from it, inspects the
interpreter, and builds the Python bundle. The final image continues from the
same upstream node, so `.deb` packages are not installed twice. This establishes the general
`resolve node -> materialize node -> resolve dependent node` graph contract for
providers that execute upstream outputs while building their bundles.

## Identity, Caching, and Invalidation

The APT node identity includes at least:

- selected canonical OCI platform record and exact base-manifest descriptor;
- immutable base identity;
- normalized active package requirements and canonical relevant request-overlay
  subset;
- complete exact package, version, architecture, status, and tagged-origin
  records, with artifact path, size, and hash only for `bundle` origin;
- relevant APT source/trust identity available from the bundle resolver;
- export declarations and resolved paths;
- APT/dpkg provider recipe version.

The Python node identity additionally includes:

- logical command requirement and selected supplier identity;
- resolved absolute interpreter path;
- logical Python version and required capabilities;
- Python requirements, wheel hashes, translations, and recipe version.

Every provider node participates in four private identities:

- a bundle-resolver cache key derived from the provider's validated
  resolver-dependency fingerprint, declared provider request, resolver
  recipe/profile, and selected platform; matching keys may reuse resolution;
- a semantic bundle identity derived from its declared upstream provider
  selection and evidence, exact closed bundle, provider recipe version, and
  selected platform record;
- an assembly cache key derived from the previous finalized local image digest,
  semantic bundle identity, materialization recipe, and renderer profile; and
- a realized prefix-image identity containing the immutable finalized image
  digest after applying that node's layer and attaching its validation record.

Target directory, staging/deployed phase, ports, mounts, readiness, runtime
configuration, and workload lifecycle state are not provider-node inputs.
Matching semantic bundle identities reuse exact closed artifacts after
resolution or resolver-cache lookup. Matching assembly keys may additionally
reuse an already realized prefix image across target directories and phases.

An executable-output validation record includes its selected invocation path,
resolved terminal path, ownership chain, relevant file digest, and typed facts
such as interpreter implementation, version, ABI, platform, and required
capabilities. It covers direct path and symlink/alternatives selection, not the
transitive programs, ELF loader, shared libraries, or subprocesses used by the
executable. Those are trusted contents and behavior of the exact realized
image. Provider-specific invariants may be stronger; Python entry-point wrappers
must name their own component environment's interpreter.

On Docker and Podman, Reploy stores the canonical record in Reploy-reserved OCI
image-config labels. The labels include the record schema, the record itself,
and a subject equal to the canonical digest of the ordered OCI
`rootfs.diff_ids` sequence. The subject is not the image digest: adding
image-config labels changes the image digest but does not change the root
filesystem, which avoids a circular identity. The finalized image digest
nevertheless covers the labels and is the realized prefix identity used by
references and caches.

Container backends store and garbage-collect this metadata with the image;
Reploy defines, writes, and validates its contents. There is no separate
machine-wide Reploy validation database. Once the backend garbage-collects the
last image configuration carrying a record, rebuilding requires a fresh probe.
OCI labels may be
inherited by a child image, but any child filesystem layer changes the current
root-filesystem-chain fingerprint, so the inherited record is inapplicable.
Reploy must probe and attach a new record before accepting or consuming that
prefix. Matching inputs alone never authorize reuse of observations from a
different subject.

Record lifetime follows use rather than original production. A provider
consumer requires a matching record on the immediate prefix it consumes. A
command-exposed output requires a matching record on the final environment
root filesystem. `unchanged` requires the selected path, complete link chain,
terminal identity, ownership, file digest, and typed facts to match the named
prior record; any drift fails. `compatible` permits drift only after a fresh
probe produces a compatible replacement record and the dependent identity is
updated. Outputs that are neither consumed again nor exposed need no repeated
validation merely because another layer was added.

Every executable output selected for provider consumption or command exposure
also satisfies `portable-output-access-v1` before its realized image is
accepted. On the initial Linux-container backend, Reploy checks the invocation
path, every recorded link/alternatives hop and terminal, and the complete
ancestor paths needed to reach them. Required directories must grant search
access and the terminal must grant read and execute access through portable mode
bits, without depending on the recorded owner, supplementary groups, or an
access ACL. Python's exclusive-root recipe normalizes its immutable environment
to deterministic `a+rX`-equivalent access. Base and APT exports are validated
but never chmodded; a nonportable candidate cannot be selected. The profile
identifier and complete access record are stored with the realized output
record and included wherever that record participates in downstream identity or
the lock.

Immediately before creating each workload or transient runtime container,
Reploy first requires the selected immutable image's final-output record to
match its current root-filesystem subject. It then performs a separate bounded
access preflight for every executable output referenced by that final runtime
plan. The check uses the exact immutable image, effective mounts, numeric UID,
primary GID, and supplementary groups selected for that container and verifies
traversal plus terminal read/execute access without launching the output. It
covers native current-user, Docker Desktop managed-user, and system-service
identities. Failure prevents container creation and identifies the first
inaccessible path and selected identity. The runtime access record is keyed by
the final runtime plan and recorded in deployment state; it never changes
provider bundle, transaction, lock, assembly, or realized-image identity.

For a discovered export, supplier state records the closure-derived candidate
set and its ownership metadata. Consumer state separately records the chosen
candidate and typed evidence. The supplier-qualified export identity therefore
remains stable even when different component-scoped consumers select different
paths from it.

The Python resolver-dependency profile includes the selected interpreter's
complete validation evidence, target platform, declared system/build
prerequisites, builder/toolchain profile, requirements and translations, and
local-source manifests and build settings. A changed upstream image triggers a
cheap validation of that profile; an unchanged fingerprint reuses the exact
wheel bundle, while changed evidence reruns the Python resolver.

The Docker backend keeps a Reploy cache lookup from the assembly key to an
immutable realized prefix-image digest through a canonical cache reference and
a separate environment-owned generation reference for each staged or installed
environment. The canonical reference provides cross-installation discovery and
reuse; the generation reference and deployment state pin the exact image that
one environment validated. Shared image configuration and Reploy-owned image
labels contain only content facts such as the assembly key, base identity,
renderer profile, and root-filesystem-bound validation records.
Deployment-directory identity belongs in reference names, deployment state,
and runtime-resource labels; it is never baked into a shared image.

Every operation that can change one deployment directory's image references or
state acquires that directory's exclusive operation lock before reading current
state and holds it through publication, state cutover, and cleanup. Operations
for different directories do not share this lock. An uncached build uses a
collision-resistant operation-specific temporary reference and captures the
backend-reported immutable image ID directly; it never discovers the result by
reinspecting a mutable staging, deployed, or canonical tag. All output probes
and validation address that immutable ID.

Publication is a recoverable two-phase cutover because Docker references and a
filesystem state file cannot participate in one atomic transaction:

1. Write and durably publish a pending-operation record naming the prior state,
   temporary reference, candidate immutable digest, and new unique environment
   generation reference.
2. Create the generation reference from the validated immutable digest without
   retargeting the prior generation.
3. Atomically replace the deployment state file so it names the new generation
   and digest. This state-file replacement is the commit point; runtime
   operations use the generation named by state rather than a mutable phase
   alias.
4. Atomically update the canonical assembly-key reference as a best-effort
   shared cache hint. A reader resolves that hint once to an immutable digest
   before validation or use.
5. After the committed state is durable, remove the prior environment
   generation and temporary reference, then remove the pending record last so
   recovery retains the complete cleanup inventory until cleanup finishes.

Recovery runs under the same directory lock. It treats the atomically published
deployment state as authoritative, preserves the generation reachable from
that state, completes or removes only this directory's pending references, and
never retargets another environment. A crash before the commit point leaves the
old state active and the candidate removable; a crash after it leaves the new
state active and the old generation removable. Concurrent canonical-reference
updates may select either fully validated realization for future cache reuse,
but they cannot change an existing environment's pinned generation.

Canonical cache references are never removed automatically in the initial
design. Explicit Reploy cache cleanup may remove canonical references without a
global environment database because environment references keep images in use
reachable. Cache cleanup never removes environment references, forcibly deletes
physical images, or invokes a global backend prune. Docker owns shared-layer
reference tracking and physical reclamation; a future Podman backend may apply
the same reference model through its own native image store.

Reploy records the complete node chain needed to resume or diagnose
`reploy build`. Docker and BuildKit own physical layer sharing and cache garbage
collection. Reploy never runs global Docker image or BuildKit cache pruning;
unreferenced physical layers remain Docker's concern. Named multi-stage targets
are one possible implementation, not part of the provider contract.

The unresolved blueprint request and resolved bundle have different identities.
An unchanged request such as `packages: [curl]` may resolve to newer exact
artifacts during a later explicit build. The semantic bundle identity uses the
resolved bundle, not merely the request fingerprint. Normal start and restart
reuse recorded resolution; when `reploy build` produces the same exact
bundle and semantic inputs, that identity remains unchanged even if a changed
earlier sibling produces a new assembly key. Before reusing resolution against
a different upstream image, Reploy revalidates the provider's complete
resolver-dependency profile. Only an unchanged dependency fingerprint permits
reuse; otherwise the resolver runs again.

### Environment Build and Cache Bypass

`reploy build` is the explicit heavy operation. It resolves provider bundles,
materializes the ordered provider graph, publishes the realized environment
image, and records the local lock. Runtime operations such as `reploy up` use
that recorded result and never resolve providers or build an image; a missing or
stale build fails with instructions to run `reploy build`.

`reploy build --no-cache` bypasses every derived bundle-resolver and realized
image cache lookup, reruns all provider resolvers, rematerializes every provider
node, and reprobes outputs. It does not delete caches and may still read an
already verified immutable raw artifact from content-addressed storage. Results
become visible only after the complete build validates and publishes
atomically. Cache-bypass policy is not a blueprint or semantic-identity input;
identical clean-build outputs retain identical semantic identities.

An assembly cache key does not promise byte-identical image output after an uncached
rebuild. Maintainer scripts, generated caches, build metadata, or other
environmental behavior may vary even when the exact artifacts and recipe are
the same. The semantic identity proves identical declared and resolved provider
inputs; the realized prefix-image identity distinguishes the actual result.

### Bundle Lock Manifest

An environment build records a local lock manifest containing its exact
resolved inputs. The initial implementation uses that lock only for local
identity, validation, and state. Exporting it as a rebuild instruction or
embedding it in a portable archive is deferred and is not implied merely
because a lock is located beside a blueprint.

The lock contains at least:

- lock-schema, canonical-encoding, digest-algorithm, script-content-digest, and
  materialization-transaction-schema identifiers;
- a canonical fingerprint of the blueprint it was built for;
- the complete canonical deployment request overlay and its digest;
- blueprint schema and declared platform compatibility set;
- selected canonical OCI OS, architecture, optional variant, and exact
  base-manifest descriptor;
- immutable base-image digest;
- renderer profile, immutable Dockerfile-frontend digest, and required backend
  capability set;
- provider graph, recipe and child-environment profile versions, upstream input
  keys, realized image digests, and validated output evidence;
- the OCI descriptor for every locked base and realized prefix image, with the
  parent/provider-node relationship needed to reconstruct the exact image
  graph;
- each provider node's transaction-script digest and canonical rendered
  transaction digest;
- every validated executable operand's recipe role, qualified supplier or
  prerequisite origin, upstream image, invocation/link/terminal paths, file
  digest, ownership, and compatibility evidence;
- every generated executable operand's recipe role, declared invocation path,
  protected root, validation policy, and post-materialization realized
  link/terminal/ownership/file evidence;
- complete exact package/distribution names, versions, architectures, statuses,
  tagged `base` or `bundle` origins, optional base-predecessor tuples, artifact
  paths/sizes/hashes for bundle-origin members only, and resolved bundle
  identities;
- every local-source input's logical identity, canonical source-manifest
  digest, builder/toolchain profile, selected build settings, validated artifact
  metadata, and exact output-artifact digest, with physical source paths omitted;
- selected executable-output identities and validated compatibility facts; and
- repository provenance for every bundle-origin package: a credential-free or
  opaque source identifier, selected suite/component, authenticated
  `Release`/`InRelease` digest, signed `Packages` index identity and digest, and
  the exact package stanza binding name, version, architecture, filename, size,
  and SHA-256 to the downloaded artifact.

The initial identifiers are `lock-v1`, `canonical-json-v1`, `sha256`,
`script-bytes-sha256-v1`, and `materialization-transaction-v1`.
`canonical-json-v1` applies the
[RFC 8785 JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785)
to a schema-normalized I-JSON record. Map keys are unique strings, array order
is significant, byte strings and digests are lowercase hexadecimal strings,
and integer values are decimal strings consisting of `0`, or an optional `-`,
a nonzero digit, and zero or more following digits. Identity-kind and schema
identifiers are fixed lowercase ASCII tokens containing letters, digits, and
hyphens. A canonical identity digest is:

```text
SHA-256(UTF-8("reploy:" + identity-kind + ":" + schema-identifier)
        + 0x00
        + canonical-json-v1-bytes)
```

The identity kind distinguishes blueprint, bundle, transaction, and other
domains. The digest output is lowercase hexadecimal. A provider script content
digest follows `script-bytes-sha256-v1`: SHA-256 over its exact generated bytes,
with no newline or text normalization.

The lock contains no credentials, repository secrets, or private key material.
The portable bundle archive carries the lock metadata, the actual `.deb`, wheel,
and other raw provider artifacts, and one digest-addressed OCI image graph for
the locked realization. That graph contains the required image manifests,
configurations, and layer blobs for the immutable base and every realized
prefix image. Shared blobs occur only once in the archive. Outside those
opaque, hash-verified artifact and OCI bytes, the archive contains no Reploy
materialization script, generated build definition, or Reploy provider-runner
executable. The embedded lock provides self-validation and may also be exported
as a separate, reviewable companion to the blueprint.

#### Deferred `reploy-bundle-v1` Archive

The initial portable representation is an uncompressed deterministic POSIX
ustar archive. Its only permitted members are regular files at these exact
paths:

```text
reploy/lock.json
oci-layout
index.json
artifacts/sha256/<64 lowercase hexadecimal digits>
blobs/sha256/<64 lowercase hexadecimal digits>
```

`reploy/lock.json` is first. It is canonical `lock-v1` JSON and enumerates the
exact path, byte length, digest, media/artifact kind, and logical owner of every
following member, including `oci-layout` and `index.json`. Every listed member
must occur exactly once even when the importer already has the corresponding
content-addressed object. After the lock, members occur in bytewise
lexicographic path order. The archive ends immediately after the standard two
zero blocks; nonzero trailing data is invalid.

Every header uses ustar form, mode `0644`, numeric UID and GID zero, empty user
and group names, modification time zero, the exact declared size, and no
extended header. Directory entries, links, devices, FIFOs, sparse files,
absolute or noncanonical paths, backslashes, `.` or `..` components, duplicate
paths, PAX/GNU extensions, and every other tar entry type are invalid. Export
with the same lock and object bytes therefore produces the same archive bytes.
The outer archive is not compressed; `.deb`, wheel, and OCI layer payloads are
already opaque formats, and any future outer compression requires a new
versioned envelope and decompression limits.

Import first reads the bounded lock member, verifies its canonical encoding and
blueprint binding, and computes the declared member count and total logical
bytes with overflow-safe arithmetic. The private versioned `bundle-import-v1`
profile supplies hard lock-size, member-count, per-member, and total logical-byte
limits. Any declaration beyond them fails before another member is consumed.
Each following header must equal the next expected path and size. Reploy streams
its bytes once into private quarantine while computing the declared digest; it
never asks a tar implementation to extract a path into the destination.

Only after the final zero blocks, complete expected-member set, object hashes,
artifact metadata, OCI layout, descriptor graph, and lock references all
validate does Reploy atomically publish the quarantined objects and manifest
root. An object already present in a trusted content store may avoid a second
publication after its archive bytes are still read and verified; it cannot be
omitted from a portable archive. Failure, truncation, interruption, unexpected
padding/data, or a missing member discards quarantine and publishes nothing.

Bundle import requires the blueprint and validates its canonical fingerprint,
lock schema, transaction schema, canonical encoding, digest algorithm,
script-content-digest rule, platform, provider recipe/profile versions, graph,
every raw-artifact hash, and the complete OCI descriptor graph against the lock.
Every referenced artifact, manifest, configuration, and layer blob must be
present in the archive exactly once and must match its digest. An exact object
already present in the backend store may skip loading only after its archive
member is verified. No image descriptor in the lock may resolve to different
bytes.

For each node, the exact locked provider recipe version selects a historical
generator from the locally installed, trusted Reploy/provider code. That
generator reproduces the internal materialization script and canonical
transaction; import recomputes both digests and requires exact matches before
accepting the corresponding locked image. Archive content can never supply or
override carrier code. An unknown compatibility identifier, unavailable
historical recipe generator, missing OCI object, or digest mismatch fails import
without falling back to a newer recipe or rebuilding an image.

Import reuses or loads the exact locked base and realized prefix images, verifies
their graph relationships, and establishes references to those immutable
digests. It never substitutes a merely compatible upstream realization.
Rebuilding from the locked raw artifacts is a new bundle-build/re-lock operation
with new transaction and realized-image identities. A successful import
establishes normal recorded deployment state, after which start, restart, shell,
and commands do not reread the lock.

```text
future export: built environment -> portable archive
future import: blueprint + portable archive -> validate -> load exact realization
```

A complete lock can be produced only after the provider graph has built its
closed bundles, including any upstream materialization needed by downstream
bundle resolvers. The human-readable embedded or companion serialization may be YAML
or another implementation-selected representation, but it must decode to the
same versioned typed record before canonicalization. Import never silently
migrates a lock or recipe. A future explicit migration operation may generate a
new lock under a new schema or recipe after revalidation. Exact export, import,
and migration UX remains deferred. Supplying a resolved list without its
artifact archive as a future online replay input is likewise deferred.

Invalidation follows graph edges:

```text
APT component package change -> APT node + dependent Python node
Python requirement change -> Python node only
port/mount/readiness change -> neither provider node
```

Normal start and restart reuse recorded provider results and generated images.
Only `reploy build` resolves package sources or refreshes artifacts.

## Validation and Failure Rules

Reploy fails before final image publication when:

- the base is not a supported Debian-family image;
- the base declares `OnBuild` triggers or image volumes, or the backend cannot
  neutralize its entrypoint, command, healthcheck, user, working-directory, or
  stop-signal defaults;
- APT or `dpkg` prerequisites are missing or incompatible;
- an APT root request is outside the strict package-name/exact-version grammar
  or has no exact binary-package match in the resolver cache;
- package resolution is incomplete or crosses the selected architecture;
- a closure member lacks exactly one valid `base` or `bundle` origin, a
  base-origin tuple is not installed in the locked base, a base-origin record
  carries artifact fields, or a bundle-origin record lacks its exact artifact;
- downloaded artifact hashes or package metadata change unexpectedly;
- two active system-package providers are requested;
- a logical export name is duplicated within one supplier;
- an explicit executable path is not absolute, does not exist, is not runnable,
  or cannot be attributed to the exact declared package closure;
- a discovered APT export has no typed consumer adapter or produces zero or
  multiple compatible terminal candidate groups;
- `logical_version_override` accompanies discovery of more than one terminal
  candidate group;
- a base-image export omits its required absolute executable path;
- a logical command has no compatible active supplier, or candidate
  dependencies are cyclic;
- the interpreter's logical version does not satisfy the Python constraint;
- required Python capabilities such as `venv` are absent;
- a transaction command position contains ordinary data, or a validated or
  generated executable operand lacks the required path and evidence checks;
- qualified output identities or exclusive provider namespaces collide;
- an exclusive provider root is preexisting, has an unsafe ancestor, or lacks
  valid Reploy ownership evidence;
- a shared-authority artifact claims, or its resulting layer changes, a
  Reploy-protected path;
- the selected base or a provider layer persists content beneath the reserved
  `/mnt` runtime-mount root;
- a runtime mount destination is not admitted by `/mnt` or an explicit
  additional root, overlaps another destination, or would replace an existing
  image file, symlink, mountpoint, non-directory, or non-empty directory;
- a final runtime mount plan overlays a protected provider root, exact exclusive
  provider leaf, or recorded executable-chain path;

Errors name the component, provider, package, requested output, selected base,
and failing phase where applicable.

## Inspection and User Experience

`reploy info` and bundle inspection should distinguish requested, resolved, and
materialized state:

```text
component system [apt]
  requested: python3, ca-certificates, libmagic1
  resolved: python3=3.11.2-1+deb12u1 [amd64] ...
  exports: python -> /usr/bin/python3

component application [python]
  interpreter requirement: python >=3.11,<3.12
  selected source: system.python
  logical version: 3.11.2
  environment: /opt/reploy/providers/python/application
```

Dry-run remains non-mutating. Without fresh package resolution it reports the
recorded provider state, whether static inputs changed, and unresolved future
identities rather than contacting APT or starting probes.

## Implementation Impact

The current implementation assumes one aggregated Python materialization and
therefore needs structural work before the component-scoped provider graph can
be enabled:

- Blueprint syntax/model must represent `.deb` packages, component and base
  image exports, logical command requirements, and version constraints.
- Blueprint validation must enforce the common provider identifier grammar,
  exactly one required reserved `base` root component, provider-owned additive
  option shapes,
  explicit direct-addition commands, and omitted-Python-interpreter
  normalization.
- APT request parsing must implement only the documented Debian `name` or
  `name=exact-version` subset, retain typed fields through planning, and require
  exact package-cache matches before provider-owned argv rendering.
- Blueprint validation and operation planning must implement the required
  platform compatibility set, deterministic explicit/native selection, and one
  selected OCI platform record without using the Reploy process architecture.
- APT planning must implement the supported OCI-to-Debian architecture table,
  cross-check the base's native dpkg architecture, reject configured foreign
  architectures, accept only native or `all` package records, and key package
  state by binary name plus Debian architecture.
- APT resolution must retain and verify the authenticated release-to-index-to-
  package provenance chain for each downloaded artifact, serialize only safe
  source descriptors or digest-derived opaque identifiers, and apply the
  provider's redaction policy before emitting APT diagnostics.
- The provider request model must carry typed command requirements, candidate
  outputs, and selected supplier identities.
- Directory-scoped deployment state must persist one versioned canonical
  request overlay plus auxiliary local-source locators, update it under the
  existing operation lock, and derive component-local overlay subsets.
- Provider bundles/state must record executable provenance and logical probe
  results without conflating package and runtime versions.
- APT bundle and lock models must represent one complete mixed-origin closure,
  require exact installed tuples for base-origin members, require artifacts for
  bundle-origin members, and record base predecessors for upgrades without
  synthesizing package hashes.
- Local environment builds must emit and validate the exact versioned lock
  manifest and bind it to the blueprint, request overlay, selected platform,
  provider recipes, and realized state. Portable lock consumption and
  export/import are deferred follow-up work.
- Local-source builders must separate auxiliary physical locators from portable
  identity, create canonical source manifests, bind builder/toolchain profiles
  and settings, validate normal ecosystem artifacts, and publish exact artifact
  digests into the bundle and lock.
- `BuildEnvironmentImage` must stop constructing a Python provider directly and
  instead execute a provider graph.
- Generated-image planning must accept ordered provider nodes rather than one
  `Materialization` value.
- Generated-image lifecycle code must replace directory-bearing image labels
  and direct mutation of staging/deployed tags with directory locking, unique
  temporary and generation references, immutable-ID validation, pending
  operation records, and atomic deployment-state cutover.
- Prefix finalization must attach canonical validation records to reserved OCI
  image-config labels, bind them to the root-filesystem layer chain, reject
  inherited subject mismatches, and require final-image records for every
  command-exposed output.
- BuildKit generation must support intermediate node identities and offline
  materialization for every provider.
- Docker rendering must pin its Dockerfile frontend by digest, version its
  capability profile, pass the selected platform explicitly to every backend
  request, and reject platform or capability mismatches.
- A restricted internal image-command runner should be shared with transient
  one-shot cleanup/status handling without exposing probes as public commands.
- Python prerequisite validation must consume a resolved absolute executable
  output instead of probing `python` through `PATH`.
- Python output discovery must derive console scripts from the exact wheel
  closure rather than treating public `environment.executables` aliases as
  provider output declarations.

## Suggested Implementation Slices

1. Introduce typed provider inputs/outputs, Python console-script catalog
   derivation, and a deterministic graph planner; retain the existing single
   Python node behavior.
2. Generalize generated-image materialization to ordered provider nodes and
   the complete private recipe contract, rejecting unsupported fields and
   enforcing network-free provider recipes.
3. Add APT component syntax and closed APT resolution against an immutable
   Debian-family base.
4. Add offline `.deb` materialization, manifest validation, and layer identity.
5. Add package-provided executable declarations, ownership validation, and
   state.
6. Add restricted image-command probing and logical version validation.
7. Change the Python provider to resolve a logical interpreter requirement from
   base/provider candidates and consume the selected output.
8. Add inspection, dry-run, cleanup, and complete Docker integration coverage.

Portable lock replay and environment export/import remain deferred follow-up
work after the initial provider graph and local environment build are complete.

Each slice should retain Python-only behavior and should not make the public
schema accept `type: apt` until the end-to-end path is complete.

## Required Evidence

Portable archive/import evidence listed below is retained for the deferred
feature and is not required for the initial APT-provider implementation.

- Schema tests for string and structured packages, base/provider exports,
  invalid paths, supplier-local duplicate outputs, unsatisfied commands, and
  cycles.
- APT request-grammar tests covering valid Debian names, unpinned and exact
  versions, epochs, revisions, tildes, and exact names containing `+`, `-`, or
  `.`; identical parsing for scalar, structured, option, and direct-addition
  inputs; and rejection of whitespace, controls, empty or repeated `=`, paths,
  releases, architecture selectors, patterns, ranges, dependency/source
  expressions, appended operations, and option-like inputs. Resolver tests
  require an exact binary-package cache match, exercise no regex/pattern
  fallback, and prove argv is generated from typed fields as one positional
  operand.
- Platform tests covering required nonempty canonical compatibility sets,
  common `linux/amd64` and `linux/arm64` declarations, optional variant
  matching, explicit target selection, single-entry selection, exact backend
  native matching, ambiguity and unsupported-target failures, and independence
  from the Reploy host process's OS and architecture.
- Renderer-platform tests proving the selected OCI record and exact base
  manifest are used by resolution, every build/resolver/probe/runtime request,
  lock import, and identity construction; the Dockerfile frontend is pinned by
  digest; capability profiles are versioned; floating syntax tags and backend
  platform defaults are ignored; and every mismatch fails before execution.
- Candidate-selection tests proving base-first and graph/stable-name provider
  precedence, compatibility filtering, explicit component/base supplier
  overrides, recorded supplier identity and final graph edge, observed-value
  incompatibility failure without fallback, and reuse when the selected result
  is unchanged.
- Provider graph tests for stable ordering and downstream-only invalidation.
- Ordered-initialization tests proving consumers see every eligible initialized
  upstream output, never see later/sibling outputs, explicit suppliers establish
  dependencies before initialization, and `reploy build` can change an
  automatic selection without runtime re-resolution.
- Multi-Python tests proving independent interpreter selection, component-scoped
  venv roots and outputs, shared artifact reuse, parallel independent bundle
  resolution, deterministic sequential image assembly, and logical invalidation
  confined to dependent component nodes.
- Python-output tests proving console scripts are derived from exact wheel
  entry-point metadata across the resolved closure; distribution names do not
  imply binaries; transitive owners are recorded; duplicate script claims fail;
  scripts without console-script metadata are initially absent; direct command
  references and optional executable profiles resolve the same qualified output;
  and selected wrappers exist with a shebang naming their component interpreter.
- Assembly tests proving one layer per materialization node, both venvs remain
  present when either component changes, unchanged later bundles are reused even
  when their layers must be rematerialized, transaction failure commits no
  layer, and finalized images remain local unless an explicit future export/push
  operation is requested.
- Component-option tests proving `COMPONENT/OPTION[,OPTION...]` parsing,
  atomic multi-component selection, option requirements joining the owning
  Python venv, component-scoped identity changes, and explicit targeting for
  direct additions when several Python environments exist. Public-surface tests
  distinguish option `add`/`remove` from `add-package`/`remove-package` and
  `add-source`/`remove-source`, exercise atomic multi-addition behavior and
  source identifiers, and prove every package request still uses its provider's
  strict grammar.
- Public-schema tests covering the shared component/option/output identifier
  grammar, separator and reserved-name rejection, scope-specific uniqueness,
  provider-owned Python `requirements` and APT `packages` option payloads,
  rejection of structural or nested option fields, omitted interpreter
  normalization to logical `python`, and explicit version/supplier overrides.
- Request-overlay tests proving fully qualified typed entries, stable sorting
  and deduplication, atomic directory-locked updates, blueprint validation,
  component-local invalidation, separation from directory/Docker resource
  identity, complete lock embedding, fresh-import adoption, existing-state
  exact matching, and explicit replacement semantics.
- Local-source tests proving physical locators remain auxiliary local state and
  never enter portable identity; source inclusion/ignore rules are deterministic;
  source-manifest, builder/toolchain, platform, setting, metadata, and artifact
  changes affect the correct identity; nominal package versions cannot replace
  content digests; portable bundles contain the built raw artifacts but no
  source tree; and import works without the original path or build tools.
- APT bundle-resolver tests for closed transitive sets, architecture, metadata,
  hashes, component-scoped options, and repository failures. Mixed-origin cases
  cover base-only satisfiers, downloaded members, upgrades with exact base
  predecessors, absence of synthetic base-package hashes, base-digest
  invalidation, exact pre-install base-status checks, final full-closure
  comparison, lock round trips, and portable import using the embedded base OCI
  graph plus bundle-origin artifacts.
- APT architecture tests covering every supported OCI mapping and variant,
  native-base agreement, native and `all` closure members, rejection of
  unsupported platforms, mismatched base architecture, configured foreign
  architectures, foreign artifacts or installed tuples, and distinct
  `(package, architecture)` keys.
- APT provenance tests proving every downloaded artifact is linked through an
  exact package stanza and signed index checksum to authenticated release
  metadata; every field or digest mismatch fails; base-origin records rely only
  on the immutable base digest; and URI credentials, query/fragment data,
  authentication entries, tokens, headers, and secret values never enter
  identities, locks, state, labels, caches, logs, or errors. Unsafe source
  coordinates must produce digest-derived opaque identifiers and structured
  redacted diagnostics.
- Bundle-resolver ingestion tests proving the output mount starts empty and is
  the only writable host-backed mount; the container is stopped before host
  ingestion; only accounted regular raw artifacts with normalized portable
  names are accepted; links, special files, aliases, races, unaccounted output,
  and every configured count/size/path limit fail without publication; sparse
  files are charged and copied by logical content; and successful files are
  streamed through metadata/hash validation before one atomic manifest-root
  publication. The same safe publication primitive is exercised by bundle
  import.
- Portable-archive tests proving byte-stable ustar export; lock-first and
  lexicographic ordering; canonical headers and exact allowlisted paths; one
  occurrence of every artifact and OCI object even when locally cached; and
  rejection of compressed input, extended headers, directories, links, special
  or sparse files, duplicates, traversal, undeclared or missing members,
  noncanonical headers, malformed termination, trailing data, truncation,
  integer overflow, and every import-profile limit. Import tests also prove
  single-pass hashing into quarantine, no path extraction, complete metadata
  and OCI-graph validation, atomic publication, and cleanup after every failure
  boundary.
- Generated-image tests proving offline installation and absence of bundled
  `.deb` files from the final image.
- Recipe-contract tests proving every declared field is rendered or rejected,
  exactly one provider-owned script is mounted and invoked per node, no dynamic
  value is interpolated into its source, positional values remain distinct and
  quoted, ordinary data is rejected in command position, validated upstream
  executables retain supplier/path/digest/capability evidence, generated
  executable declarations affect transaction identity, provider-generated
  executables cannot run before declared-path validation, their actual
  link/terminal/file evidence is bound to the realized image, downstream
  option-like values are rejected, mandatory command failures propagate outside
  conditionals and pipelines, carrier environment is identity-bound, children
  receive only their versioned closed environment, every mounted input is
  content-addressed,
  executable-operand evidence plus script and rendered-transaction digests
  affect identity, raw scripts and archives are absent from the layer, root
  build steps preserve final image-user semantics, mounts remain read-only, and
  materialization has no network.
- Build-mount identity tests proving logical descriptors include fixed role,
  container destination, policy, and existing manifest/script digest; exclude
  every physical host, cache, temporary, and deployment path; reuse the same
  key across target directories and phases; change when logical content or
  policy changes; and require no artifact-byte reread by Reploy's identity/cache
  logic during normal lookup or materialization. Backend mount and installation
  I/O is outside that last assertion.
- Argument-budget tests proving bundle-resolver and materialization `ARG_MAX` probes
  use a validated absolute prerequisite under their respective carrier/resource
  policies; complete download, script, and offline APT argv/environment
  footprints include zero bytes and pointer arrays; the 50 percent aggregate
  and 64 KiB per-string budgets are enforced without artifact reads; a
  representative thousand-artifact closure remains admissible under normal
  modern-Linux limits; over-budget diagnostics are complete; and no transaction
  chunking occurs.
- APT child-environment tests proving `apt-resolve-v1` and `apt-dpkg-v1` each
  supply exactly their fixed `PATH`, locale, home, temporary directory, debconf
  frontend, APT config, and umask; leak no inherited variables; clean their
  bounded scratch; and change the corresponding transaction identity and
  profile version when any provider-owned fixed input changes. Materialization
  additionally proves that maintainer scripts can find standard system tools.
  Base-trust tests prove the generated `APT_CONFIG` is additive, the immutable
  base's normal APT configuration remains active, caller `APT_CONFIG` and proxy
  values cannot enter either child, required final argv overrides win where
  ordering matters, and offline materialization cannot use network access.
- Base-configuration tests covering rejection of `OnBuild` and `Volumes`,
  neutralized entrypoint/command/healthcheck behavior, explicit working
  directories and users, runtime-only base environment defaults, provider
  environment isolation, informational exposed ports, and `SIGTERM` shutdown.
- Runtime-identity tests proving native-Linux user scope uses the invoking
  UID/GID, Docker Desktop uses a recorded Reploy-managed non-root Linux identity,
  system scope uses its service account, base `USER` is ignored, container root
  never implies Desktop host root/Administrator, and every declared mount is
  usable by the selected identity. Output-access cases cover portable
  `a+rX`-equivalent Python roots; rejection of base/APT exports that rely on
  owner, group, or ACL access; inaccessible parent and link-target directories;
  group-only terminal files; exact UID/GID/supplementary-group checks under the
  effective mount plan for every workload and transient container; clear
  pre-creation failure; and runtime evidence remaining outside every shareable
  bundle, image, transaction, assembly, and lock identity.
- Filesystem-authority tests covering exclusive-root ancestor validation and
  safe creation, absent component leaves, exact Reploy ownership evidence,
  symlinks treated as unfollowed leaves, rejection of mountpoints and namespace
  overlap, confinement of Python layer changes, one matching shared system
  authority, delegated APT/dpkg replacement semantics, protected-root checks on
  both archive listings and actual layer changes, and processing safety limits.
- Runtime-overlay tests covering exact, ancestor, and within-provider-root
  mount destinations; exact exclusive leaves; executable symlink chains and
  base-image exports; an absent or empty `/mnt`; rejection of provider changes
  beneath `/mnt`; accepted `/mnt/config` and `/mnt/data` destinations; rejection
  of `/mnt` itself, overlapping destinations, targets outside allowed roots,
  existing files, symlinks, mountpoints, and non-empty directories; explicit
  additional-root acceptance for an absent or empty target; rejection of
  `/usr/lib` even when `/usr` is additional; backend-generated mounts; every
  persistent and transient runtime container type; and Docker-intrinsic mount
  exclusion. Tests also prove that absolute output selection does not imply
  transitive execution attestation and that a Python entry-point wrapper names
  its own venv interpreter.
- APT transaction tests proving index refresh, download, installation, and
  verification all use stdin `/dev/null` and no controlling terminal; download
  and installation use `--assume-yes`; update starts from an empty private lists
  directory, supports and uses `--error-on=any`, fails for every enabled-source
  acquisition error, and cannot consult inherited indexes; and the final
  configuration overrides retain dangerous-operation safety aborts.
  Installation tests additionally prove no dpkg pseudo-terminal, local-package
  ordering, an initially empty private archive cache, immediate path/size/hash
  verification of every mounted `.deb`, no inherited or undeclared archive, no
  network access, no removals, noninteractive conffile handling, a clean
  pre-install `dpkg --audit`, canonical package-state snapshots, `dpkg
  --audit`/`apt-get check` after installation, exact manifest comparison,
  rejection of every undeclared addition, removal, tuple change, or status
  change, and graph termination on any failure.
- Security tests proving blueprint command arguments receive no shell
  interpretation, materialization accepts no author shell fragments, probes
  have no network, timeout/output and scratch size are bounded, writes remain
  confined to the private tmpfs, and interruption always cleans up.
- Exact-prefix validation tests proving every new filesystem subject acquires
  fresh baseline records; only the same subject and complete profile reuse a
  probe; compatible tool replacement refreshes the record while
  unchanged-policy drift fails; broken, cyclic, overlong, and non-regular or
  non-executable tool chains fail; `/.reploy-build` as a directory, file, or
  symlink fails without
  being followed; mounted artifact bytes are not rescanned for identity; and
  any failure blocks all downstream nodes.
- Output-record lifetime tests proving records are stored in reserved image
  configuration and covered by the finalized image digest; their subject is
  the root-filesystem layer chain; an inherited record is rejected after any
  child filesystem layer; each provider consumer validates its immediate
  prefix; command-exposed outputs validate on the final image; compatible drift
  creates a fresh dependent identity; unchanged drift fails; unused outputs are
  not needlessly re-probed; cache deletion removes records with images; and
  rebuilding performs fresh validation.
- Python tests for bounded closure discovery, alias grouping, explicit-path
  overrides, multiple consumers selecting different paths from one candidate
  set, singleton requirements for untyped/public exposure, logical-version
  constraints, package-derived versions and `logical_version_override`, missing
  `venv`, package-version/runtime-version distinction, rejection of empty
  export objects, mutual exclusion of `discover` and `executable`, singleton
  discovery for an override, and deterministic acceptance or rejection of an
  observed provisional-version mismatch.
- Real-Docker tests from Linux and Docker Desktop hosts using both:
  - a Debian base where an APT component supplies Python; and
  - a Python Debian-family base where an APT component supplies only native
    libraries.
- Cache tests proving APT component changes invalidate Python while Python-only
  changes reuse the APT node, and identical nodes are shared across target
  directories and staging/deployed environments without cross-environment
  deletion.
- Reference-lifecycle tests proving canonical cache lookup enables
  cross-installation reuse; shared image labels contain no directory identity;
  same-directory mutations serialize while different directories remain
  independent; builds validate an immutable backend-reported ID under a unique
  temporary reference; state cutover uses unique generation references and an
  atomic state-file commit point; recovery at every cutover boundary preserves
  the state-reachable generation and removes only that directory's abandoned
  references; environment cleanup removes only its own references; explicit
  cache cleanup preserves environment references; and no operation issues
  forced image deletion or global prune.
- Identity/record regression tests proving input keys and realized identities
  remain distinct, observations are reused only for an exact root-filesystem
  subject and profile, uncached builds always re-probe, changed records
  invalidate only selected dependents, canonical readers pin one immutable
  digest before use, and atomic cache-hint updates do not retarget concurrent
  environments.
- Bundle-lock tests proving blueprint binding, canonical-encoding and digest
  vectors, exact artifact/hash and recipe validation, embedded and companion
  manifests, secret omission, rejection of archive-supplied carrier code,
  deterministic local regeneration of historical recipe bytes, failure for an
  unavailable recipe/schema/algorithm, complete content-addressed OCI graph
  export with shared blobs stored once, exact image-digest verification and
  reuse/load on import, rejection of missing or substituted image objects,
  absence of compatible rematerialization fallback, and establishment of normal
  imported state.

## Open Provider-Scope Decisions

1. Which Debian-family identities are accepted under `type: apt`, including
   Ubuntu and other `ID_LIKE=debian` images.
2. When local `.deb` translations and blueprint-defined repositories become
   justified, including their trust and credential model.

## Authoritative References

- [`apt-get install` package/version syntax](https://manpages.debian.org/bookworm/apt/apt-get.8.en.html#install)
- [Debian Policy: package version fields](https://www.debian.org/doc/debian-policy/ch-controlfields.html#version)
- [Debian Policy: maintainer scripts](https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html)
- [Debian Policy: native `Provides`](https://www.debian.org/doc/debian-policy/ch-relationships.html#virtual-packages-provides)
- [Debian Policy: `update-alternatives`](https://www.debian.org/doc/debian-policy/ap-pkg-alternatives.html)
- [Debian Python Policy](https://www.debian.org/doc/packaging-manuals/python-policy/)
- [`dpkg-deb` archive inspection](https://manpages.debian.org/bookworm/dpkg/dpkg-deb.1.en.html)
- [Docker container execution controls](https://docs.docker.com/engine/containers/run/)
