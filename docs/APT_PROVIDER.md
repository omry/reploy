---
status: Active
updated: 2026-07-15
summary: Subdesign for closed .deb package layers, provider outputs, and Python runtime dependencies.
refines: docs/BLUEPRINT_ENVIRONMENT_MODEL.md
---

# APT/dpkg Provider and Cross-Provider Executable Outputs

This document defines the first provider beyond Python. Its accepted provider
model and public semantics refine the normative environment model in
`BLUEPRINT_ENVIRONMENT_MODEL.md`. Concrete implementation contracts and gates
are defined in `APT_PROVIDER_DETAIL_DESIGN.md`.

The APT/dpkg provider is useful on its own for native libraries and utilities.
It also forces Reploy to answer the more general question of how one provider
can consume an executable supplied by another provider. Python is the
motivating case: a `.deb` package may supply the interpreter used to construct
Reploy's Python environment.

## Review Status

The conceptual-design review is complete, and its accepted decisions are
promoted into `BLUEPRINT_ENVIRONMENT_MODEL.md`. This document is authoritative
for APT-provider product semantics; it is not the concrete implementation
specification.

The final section lists intentionally deferred provider-scope choices rather
than unresolved v1 public schema.

Concrete Go types, state files, Docker operations, migration boundaries, and
implementation gates are specified in
[`APT_PROVIDER_DETAIL_DESIGN.md`](APT_PROVIDER_DETAIL_DESIGN.md).

Portable environment export/import is unsupported in v1. Any future transfer
feature requires a separate design; this document does not reserve its format or
behavior.

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
and local lock validation. Every backend request carries the platform
explicitly. A base, artifact set, lock, or backend capability mismatch fails
before the affected phase; Reploy does not silently choose another architecture
or use an implicit emulation path.

The private Docker renderer profile pins the Dockerfile frontend by immutable
digest and records every result-affecting backend capability. The selected
platform plus this profile participate in transaction, assembly-cache, lock,
and realized-image identities. A floating syntax tag,
`DOCKER_DEFAULT_PLATFORM`, and backend default platform selection are not
inputs.

### Debian/Ubuntu Compatibility Contract

The initial APT provider considers an image part of its family when the parsed
`/etc/os-release` `ID` is `debian` or `ubuntu`, or one exact
whitespace-delimited `ID_LIKE` token is `debian` or `ubuntu`. Substring matching
is forbidden. `VERSION_ID` is required, and the exact OS fields are recorded for
diagnostics and locks, but distribution names and release numbers are not an
allowlist: past, future, and derived releases use the same provider only when
their APT/dpkg configuration schemas and required behavior remain compatible.

Compatibility is established from runtime probes, not inferred from version
numbers. The image must provide the required APT/dpkg tools and options, mapped
native architecture, no foreign architectures, update-error propagation, exact
download behavior, offline installation, and clean package-state validation.
Exact distribution, APT, and dpkg versions are retained as evidence and
diagnostics but do not decide acceptance.

The `ID`/`ID_LIKE` match selects this capability profile; it does not establish
support by itself. A derivative that lacks one required tool interface or fails
any package-state check is rejected with the failed check identified. Its APT
source and trust configuration remains part of the trusted immutable base.

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
stable Reploy-managed non-root Linux identity recorded in deployment state.
Reploy validates portable output access and mount destinations while building,
then checks host mount-source existence and policy before runtime; Docker and
the workload report identity-dependent mount permission failures. Linux system
scope uses the resolved service account. The base image's configured `USER` is
never the implicit runtime identity.

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

        - python3

    application:
      type: python
      interpreter:
        command: python
        version: ">=3.11,<3.12"
      requirements:
        - arbiter-server
```

`system` is an ordinary component name chosen by the author. `apt` selects the
APT/dpkg component provider. The versioned well-known-tool profile recognizes
the exact `python3` package request and publishes the logical `python` candidate
at `/usr/bin/python3`. The Python component requests that logical command
without naming its source or an executable path.

A pinned package uses the same well-known mapping:

```yaml
- python3=3.11.2-1+deb12u1
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
        executable: /custom/python3
```

Options and deployment-local direct additions normalize through this same
typed request. `reploy bundle add-package COMPONENT REQUIREMENT...` adds APT
roots to a named APT component; `remove-package` removes exact normalized
request entries. Once parsed, these commands cannot introduce a broader APT
grammar.

### Deployment Request Identity

The blueprint is not the whole provider request. Enabled component options and
direct package additions form the canonical request overlay defined by
`BLUEPRINT_ENVIRONMENT_MODEL.md`. It lives in existing directory-scoped
deployment state, is updated atomically under the deployment operation lock,
and contains sorted fully qualified selections plus component-qualified typed
provider additions. A directory-path-derived Docker resource identity is not a
request identity.

The effective request identity binds the blueprint fingerprint, canonical
overlay digest, and selected target-platform record. Each provider node binds
only its relevant overlay subset. The bundle lock embeds the complete overlay
and digest. An existing local lock is valid only for an exact match.

When a blueprint translation is used, its filesystem path is only a local build
input. Reploy identities use a canonical source-manifest digest, versioned
builder/toolchain profile, selected platform, and relevant build settings.
After the provider validates and emits its normal raw artifact, the resolved
request and lock additionally record ecosystem metadata and the exact artifact
digest. The deployment-local provider store contains that artifact, not the
source tree or its physical path. This same contract covers local wheels,
binaries, `.deb` files, and future source-derived artifacts.

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

APT outputs are singleton executable candidates; v1 performs no generic
package-file or image-filesystem discovery. An arbitrary package may declare an
output only with an explicit normalized absolute path:

```yaml
- package: custom-python
  exports:
    python:
      executable: /opt/custom/bin/python3
```

`exports` is a map keyed by the component-local output name. Each entry requires
`executable`; `discover` is not a valid field.

The initial versioned well-known-tool profile contains exactly one mapping:

```text
exact requested package: python3
logical output:          python
candidate path:          /usr/bin/python3
consumer kind:           python
```

Requesting `python3`, pinned or unpinned, publishes this candidate without an
`exports` block. The mapping carries no trusted Python identity or version. The
consuming Python resolver validates the candidate as its first step inside the
existing resolver container based on the exact immutable supplier prefix. It
requires the path to exist and be executable, invokes it only with fixed
provider-owned inspection arguments, confirms that it is Python, and obtains a
parseable version before network or source work. It performs no fallback path
search and starts no additional probe container.

If the built-in candidate is missing, is not Python, or has no parseable
version, the error identifies the `python3` mapping and shows the explicit
`exports.python.executable` form. An explicit `python` export on the structured
`python3` request replaces the built-in candidate path. Other well-known tools
require a separately justified profile revision; v1 does not contain a generic
registry populated speculatively.

Reploy resolves the selected path without searching for alternatives or other
executables. It rejects cycles and paths escaping the image. For each ordinary
path and terminal file, literal `dpkg-query -S` must identify an installed
package key whose exact status tuple appears in the complete locked APT node.
The owner may come from any component in that shared node; Reploy records the
actual owner and does not reconstruct a per-root dependency graph.

An unowned symlink hop is accepted only when the chain enters the alternatives
directory and read-only `update-alternatives --query` confirms the named link
group, current selected value, and exact chain to the terminal. Reploy never
enumerates alternatives or changes their selection. The selected terminal must
still be owned by an exact package in the complete locked APT node. A missing,
unregistered, inconsistent, or unsupported alternatives chain fails with a
suggestion to declare the terminal executable path directly. Other unowned
maintainer-script-created files or links cannot be exported in v1.

This record identifies the direct executable file selected by Reploy; it is
not a transitive attestation of every program or library that file may execute.
Package artifacts and their runtime behavior remain trusted inputs. A script
may use a shebang, including `/usr/bin/env`, or later launch other tools under
the package's normal operating-system semantics. Reploy's absolute invocation
path prevents an outer command-name lookup through `PATH`; it does not claim
that the invoked package code performs no internal `PATH` lookup.

Logical output names must be unique within a component. Provider resolution
records the source package even though consumers request only the logical
command. The output record contains the complete selected link/alternatives
chain and the actual terminal owner from the locked APT node.

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

### Logical Version Determination

Supplier outputs carry candidate paths and provenance, not logical versions.
Neither a `.deb` package version nor an author assertion stands in for the
runtime version.

The typed downstream consumer owns the logical interpretation. As the first
step in its existing bundle-resolver container, before network or source work,
it executes each eligible candidate with fixed consumer-owned inspection
arguments against the current immutable prefix. It confirms the executable's
identity, determines its actual version, applies the consumer's version
constraint, and records the observed facts. An unqualified requirement checks
candidates in established lower-layer-first order and selects the first
compatible one. An explicit `supplier` checks only that supplier and fails if
it is incompatible. No separate probe container is created.

For Python, this is the fixed isolated `sys.version_info` inspection described
below. A missing executable, wrong executable kind, unparseable version, or
version-constraint mismatch rejects that candidate. Once selection is frozen,
later validation drift fails under the normal validation policy rather than
silently switching suppliers.

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
consumer's executable-identity and version semantics. For an unqualified
requirement, Reploy traverses only catalogs already published by initialized
suppliers, from lower to higher image layers: the immutable base first, then
active provider nodes in initialization order, using stable component-name
order within one layer. The first compatible candidate is selected and
recorded; no compatible candidate is an unsatisfied prerequisite.

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

All provider-produced executables use one output model. A supplier output names
one executable candidate, whether explicitly declared, supplied by the APT
well-known-tool profile, or derived from exact ecosystem metadata. It may be
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

Interpreter requirements may use provider-specific logical-version filtering.
Application outputs are matched by name only in the initial design; general
application-output versioning is deferred.

Collision validation applies to qualified identities and incompatible physical
path ownership claims. Multiple references or public aliases for the same
qualified output are not collisions, nor are equal local output names from
different components.

Initial supplier catalogs contain candidate paths and provenance. The
consumer's resolver validates the actual executable and version in its existing
container before freezing an automatic selection; Reploy does not create an
intermediate image or separate container solely to discover a version.

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

Selection occurs once, in the validation prelude at the start of the consuming
resolver during deterministic provider-node initialization. A node may consider
only eligible outputs from the resolved base and already initialized upstream
nodes; a later or sibling node does not retroactively become its candidate. An
explicit `supplier` establishes the required structural dependency before
initialization and only that supplier is validated. An unqualified requirement
validates eligible initialized suppliers in documented layer order, freezes the
first whose observed actual value is compatible, and adds only that selected
edge to the final graph and lock. Because this edge always points to an
initialized supplier, automatic selection cannot introduce a cycle.

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
```

The supplying backend/provider owns source identity and filesystem ownership.
The Python provider owns Python version parsing and validation of its fixed
recipe prerequisites. Neither side needs provider-specific knowledge of the
other.

## Provider Dependency Graph

Candidate matching produces one of these graphs:

```text
immutable base image export -> Python provider node

immutable base image -> APT provider node -> Python provider node
```

Blueprint authors declare requirements and exports; they do not order image
layers. Reploy first plans a structural graph and rejects cycles formed by
explicit supplier edges. It uses stable names to order otherwise independent
nodes. As each consumer becomes ready, its resolver validates candidates from
already published catalogs, freezes the first compatible automatic selection,
and adds the selected edge to the final graph before network or source work.

Graph execution initializes nodes in deterministic topological order:

```text
resolve immutable base and validate base exports
-> initialize/materialize system-provider node, if active
-> initialize component-scoped Python environment nodes
-> initialize any higher-level dependent nodes
```

Initialization means resolving and materializing the node far enough to publish
singleton candidate paths and provenance, then validating realized outputs
before those outputs become eligible upstream candidates. This natural ordering
supplies candidate records before a consumer validates and selects one; no
separate public discovery graph is required.

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

Within one deployment, Python wheel downloads and build artifacts may be reused
when their complete artifact identities match, but materialized venv nodes
remain component-scoped because their roots and outputs differ. Independent
Python bundles may resolve concurrently when their semantic graph dependencies
permit it. Final image materialization is sequential in stable node order
because an OCI image is an ordered layer chain.

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
the root using no-follow operations. Provider recipes use fixed destinations
beneath that root, and final validation checks the declared root and outputs.
V1 does not export or scan the produced layer to prove write confinement.

APT/dpkg owns ordinary paths in its shared system domain. Reploy does not
reinterpret `.deb` payload collisions or attempt to duplicate dpkg semantics
for upgrades, `Replaces`, conffiles, diversions, alternatives, or maintainer-
generated files. It still streams archive listings before execution to reject
claims beneath Reploy-protected namespaces. Paths created dynamically by
maintainer scripts remain trusted package behavior in v1. APT transaction
success, dpkg consistency, declared outputs, and the required fixed
tool-interface evidence are validated separately.

Protected Reploy namespaces include every provider's exclusive claims and the
internal provider-root hierarchy. Path comparisons normalize entries and never
follow symlink targets; a symlink is a non-directory leaf, and a symlink in an
ancestor chain is rejected. Executable evidence resolves links with cycle
detection and no fixed hop limit as a separate post-materialization operation.

This contract deliberately does not build an environment-wide file-ownership
index for a package manager's shared domain. Artifact listings are streamed
with bounded memory and need not be retained after protected-path checks.
Deployment state records the exclusive-root ownership evidence and declared
executable evidence rather than attributing every system file to a blueprint
component.

### Runtime Overlay Validation

The filesystem-authority declarations also protect materialized provider
content from runtime mount overlays. `/mnt` is the built-in runtime-mount root
and is reserved from image content: the selected base must expose it as absent
or an empty real directory, provider archives reject declared paths beneath it,
and final-image validation requires it to remain absent or empty. Normal runtime
mount destinations must be strict descendants of `/mnt`.

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
path to the protected set. During `reploy build`, Reploy compiles every
deployment runtime plan after generated and phase-specific mounts are known and
validates its destinations against the final immutable image. A changed plan
makes the recorded build stale. Docker-intrinsic kernel and resolver mounts are
not blueprint mounts and are outside this allowlist. Blueprint mounts never
become provider-owned claims, and changing a safe runtime mount plan or its
additional roots does not change provider-node cache identity.

## APT/dpkg Resolution

Resolution occurs during `reploy build` inside a temporary container created
from the selected immutable base image.

1. Resolve the author-supplied image tag to an immutable digest or image ID.
2. Probe the image for a supported Debian-family identity, `apt-get`, and
   `dpkg`; require `dpkg --print-architecture` to match the selected
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
8. Inspect every archive and record package, version, architecture, path, size,
   and SHA-256.
9. Construct the complete resolved package closure. A package already in exact
   `install ok installed` state in the immutable base and retained by the plan
   is `base` origin. Every package supplied or upgraded by a downloaded `.deb`
   is `bundle` origin; an upgrade also records the exact base predecessor.
10. Reject duplicate bundle storage paths, package architectures other than the
    mapped native architecture or `all`, or artifacts not accounted for by the
    provider result. Any APT failure to resolve the complete transaction also
    fails the resolver. `.deb` payload overlap within the transaction follows
    APT/dpkg semantics; archive entries beneath Reploy-protected namespaces are
    rejected.

The closed artifact set is a delta relative to the immutable base image. A
dependency already installed in that exact base need not be copied into the
bundle, but its satisfaction is tied to the base digest and recorded provider
inputs.

The lock-level base-image digest binds the complete base filesystem, including
its dpkg database and installed package files. A resolved package record is
therefore a tagged origin rather than a shape that always demands an artifact
hash. The lock's canonical provider request records the normalized root package
requests once; the resolved APT payload does not duplicate them:

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
complete closure records package, version, architecture, and origin for both
forms. It does not copy Debian relationship fields. APT reads those from the
package data, and materialization validates the result with `dpkg --audit`,
`apt-get check`, and exact installed-state comparison.

APT network access is allowed only during this resolution phase. Repository
signature verification uses the keys and policy already present in the selected
base image. Supporting author-defined repositories or keys requires a separate
trust and secret-handling design.

Resolution and download use only the index files acquired into that private
empty directory. They never consult index files inherited from the base image,
so a failed refresh cannot fall back to stale metadata. Resolver scratch,
including those indexes, is discarded after the closed bundle has been
validated and is not included in the bundle or materialized image.

### Repository Trust and Secret Boundary

The immutable base image owns APT's implementation, configuration, sources,
keys, credentials, and repository trust policy. Reploy does not parse or
rewrite source trust options and does not reconstruct a second
release-to-index-to-artifact authentication chain. It runs `apt-get update`
against fresh private indexes with `--error-on=any`, accepts APT's configured
trust decision, and treats any error APT reports as fatal. Reploy never adds a
trust override, changes keys, or retries under a different trust policy.

Before publishing a bundle-origin `.deb`, Reploy inspects its control metadata
and normalized file list, verifies the selected package tuple, and records the
artifact's exact path, size, and SHA-256. Base-origin package records remain
bound by the immutable base-image digest. Source configuration and repository
credentials are not copied into the bundle or duplicated in identity metadata.

Raw APT stdout/stderr and source declarations are secret-tainted and are not
persisted. Before display or diagnostic logging, provider-owned filtering
removes URI credentials and other recognized secret forms; output that cannot
be rendered safely is replaced by a structured phase/error code. A future
blueprint-defined repository feature must transport credentials through an
ephemeral backend secret mechanism rather than argv, environment, locks, or
image layers.

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
- create a private, initially empty APT archive-cache directory beneath private
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
cycle-checked link chain, terminal path, ownership and file digest, and typed
compatibility evidence. The backend verifies that record and passes the
invocation path as one positional value; the script executes the quoted absolute
path directly, without `PATH`, `eval`, `sh -c`, or source interpolation.

A private `GeneratedExecutableOperand` declares a recipe role, exact
provider-derived invocation path beneath a protected provider-owned root, and a
validation policy before the transaction runs. The script may invoke it only
after the generating operation and after verifying its cycle-checked link chain,
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
and resolved-bundle identity. That bundle already binds the ordered logical
paths, sizes, kinds, and verified content digests, so no second mount manifest
or descriptor hash is created. A trusted-script mount uses its fixed role and
destination, read-only policy, and existing exact script digest. Physical source
paths—including cache, temporary, staging, deployment, and host installation-
directory paths—are late-bound backend locations and never enter either
descriptor or the materialization cache key.

Environment build hashes artifact bytes while acquiring them. Reploy then
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

### Exact-Prefix Validation Evidence

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
the exact absolute `apt-get` and `dpkg` prerequisites needed by their bundle
resolver or materializer. The same profile may include other typed
executable inputs and backend capabilities required by the next operation. The
consuming resolver or materializer validates each declared invocation path,
complete cycle-checked link chain, regular executable terminal, ownership and file
fingerprint, plus provider-specific fixed tool-interface checks before doing
network work or changing the filesystem. Each executable requirement has a
private validation policy:
`compatible` may acquire a new record after a layer legitimately updates its
implementation, while `unchanged` must match the record named by the profile.
Backend carrier and APT tool prerequisites initially use `compatible`; selected
provider outputs may use the stricter policy under their lifetime rules.

The consuming operation also requires the reserved `/.reploy-build` root to be
absent in the exact prefix image before mounting anything there. This one
absence check makes the rendered mount target unshadowed and symlink-free at the
boundary; Reploy does not scan the whole filesystem or mounted artifact
contents. A provider transaction that persists the reserved root is rejected by
final image validation.

Reploy does not launch a standalone prerequisite-probe container before a
resolver or materializer. On a resolver miss, the resolver performs the checks
as its first step. On a bundle hit, the materializer compares the locked
evidence with the current prefix before its first filesystem change. A mismatch
commits no layer, discards that hit, and reruns resolution against the fixed
current prefix before materialization is attempted again. A mismatch after that
fresh resolution is an operation failure rather than another retry. A new
filesystem layer has a new root-filesystem-chain fingerprint and cannot inherit
evidence from the preceding layer.

A provider prerequisite is validated against the exact prefix immediately
inside the consuming operation. That consumer-use guarantee ends when the
operation ends unless a later consumer selects the output again. Each later
consumer validates it inside its own operation against its immediate prefix. An
output referenced by a command, directly or through `environment.executables`,
is validated by the full final-image validation before publication. An earlier
consumer observation never authorizes final command exposure.

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

### APT Invocation Size

Reploy runs each download or installation as one complete APT transaction and
does not split the `.deb` closure. It does not probe `ARG_MAX`, predict encoded
argument size, or impose a smaller provider-defined limit. If the operating
system rejects process creation with `E2BIG`, Reploy reports the provider node,
phase, package or artifact count, and the operating-system error. A failed
process creation publishes no resolver result or node layer.

Reploy does not chunk the transaction because dependencies, `Pre-Depends`,
cycles, and maintainer-script timing can cross arbitrary chunk boundaries. If
real workloads encounter the operating-system limit, a different invocation
strategy requires a separate design based on that evidence.

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
It does not reinterpret the immutable base's APT trust configuration. The
provider's typed request rules, `--no-remove`, hold/no-downgrade policy, exact
locked artifacts, and complete before/after package-state comparison enforce
the transaction result independently. A repository, package, maintainer script,
or hook that bypasses the declared noninteractive mechanisms and requires
terminal input is unsupported and fails the bundle-resolver or materialization
transaction.

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

## Executable Validation and Probing

Resolution records each singleton explicit or well-known candidate path; it
does not search package contents or the image. After the APT node is
materialized, the consuming operation or final-image validator resolves that
path, validates its file type and executability, and uses `dpkg-query -S` to
require every ordinary owned hop and the terminal to belong to exact tuples in
the complete locked APT node.

When the selected chain enters the alternatives directory, Reploy derives the
link-group name from that already selected chain and invokes read-only
`update-alternatives --query`. The reported selected value must match the
observed chain, and the terminal must still pass `dpkg-query` ownership. Reploy
does not enumerate link groups, choose an alternative, or accept an unregistered
alternatives link. Both commands run inside the already-required consuming or
final-validation container.

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
packages, Reploy does not treat the package name or version as a provisional
Python runtime version. Executing the candidate interpreter supplies the actual
logical version used for compatibility and selection. Debian epochs, backports,
distribution revisions, and version conventions are package-manager metadata,
not the Python runtime contract.

### Bundle-Resolver Validation and Execution

When a downstream provider must execute an output to build its bundle, Reploy
starts one disposable bundle-resolver container from that image:

```text
RunBundleResolver
  image: selected upstream provider-node image
  executable: selected typed provider output
  user and working directory: explicit provider-owned values
  root filesystem: read-only
  network: provider resolution policy
  inputs: declared read-only bundle/source mounts
  outputs: one initially empty private writable artifact mount
  scratch: private temporary storage
  environment: fixed versioned provider profile
  stdin and terminal: /dev/null and none
  host capabilities: none
  deadline: no Reploy-wide elapsed-time deadline
  output: continuously drained and forwarded through the safe output path
  cleanup: always
```

The bundle resolver validates every prerequisite before performing network or
source work, then uses the validated executable to produce the raw artifacts
for the closed downstream bundle. The initial Docker implementation uses a
disposable container. A throwaway BuildKit stage may implement the same contract
later. The resolver cannot modify the upstream image and remains an internal
provider-graph operation rather than a public command.

The materializer performs the equivalent checks before its first persistent
change. Python does not create a temporary venv merely to prove `venv` support;
creation of the real component venv is the authoritative fixed-recipe check.
Failure commits no provider layer and identifies the selected interpreter so
the author can supply a package set that provides `venv` or choose another
supplier.

The host creates the output directory as an empty, private Reploy temporary
directory outside deployment and cache publication paths and confirms that it
is empty immediately before mounting it. It is the resolver container's only
writable host-backed mount. After the resolver exits, Reploy stops the container
and detaches the mount before examining the directory, so resolver code cannot
race host ingestion.

Host ingestion derives the artifact descriptors used by the canonical
resolved-bundle manifest; it never trusts a resolver-supplied filesystem
manifest by itself. Starting from an opened output
directory descriptor, it enumerates normalized relative names without following
links. Initial APT and Python bundle outputs are regular raw `.deb` and wheel
files; future providers must declare their permitted raw artifact kinds. Reploy
rejects absolute or traversing names, unaccounted files, symlinks, hard links,
directories outside the declared layout, sockets, devices, FIFOs, duplicate
normalized names, and names that alias under the canonical artifact-name
normalization rules.

Reploy opens each accepted file relative to the output directory with no-follow
semantics, verifies that it remains the same regular single-link file, and
streams its bytes through provider-specific artifact inspection and SHA-256
into private temporary content-addressed storage. It rejects malformed data and
integer overflow and obeys actual format, filesystem, backend, and kernel
constraints, but adds no numeric artifact, byte, path, or scratch quota. APT
control data or wheel metadata must account for every accepted artifact. Sparse
input is copied and hashed as verified logical bytes rather than retained as a
resolver-controlled sparse object.

Only after every artifact descriptor validates does Reploy atomically publish
the resolved-bundle manifest as the one manifest root. Failure removes the
temporary objects and publishes no bundle. Bundle build and future local
artifact producers reuse this safe-artifact publication primitive after their
source-specific checks.

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
a logical-version constraint or a particular supplier.

The provider must:

1. Normalize an omitted interpreter to logical command `python`, then require
   that command with any explicit supplier and logical-version constraint.
2. In the existing bundle-resolver container, evaluate singleton candidates
   exported by the base image and earlier provider nodes in established
   lower-layer-first order; an explicit supplier limits evaluation to that
   supplier.
3. Execute each candidate's absolute path, never a `PATH` lookup, and reject a
   path that does not identify a usable Python interpreter.
4. Parse and validate the actual Python version against the consumer constraint,
   selecting the first compatible candidate or failing the explicit supplier.
5. Freeze the selection before network or source work; later drift does not
   trigger selection of a different supplier.
6. Record the selected source, absolute path, and logical version.
7. Encode that complete evidence as the Python transaction's
   `ValidatedExecutableInput`; the interpreter path is never ordinary data.
8. Use the same absolute interpreter in the disposable bundle resolver to
   resolve and build the closed wheel bundle for its actual version and ABI.
9. During offline materialization, invoke that typed, quoted absolute
   interpreter to create the
   component-scoped Reploy-owned environment, conceptually:
   `/opt/reploy/providers/python/<component>`.
   Successful creation of this real environment proves the recipe's fixed
   `venv` prerequisite; there is no separate capability probe.
10. Validate the generated environment interpreter at its declared path, then
    use it as a provider-generated executable operand to install the closed
    wheel bundle offline and record its realized link/terminal evidence.
11. Derive the component output catalog from the exact wheels' console-script
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
- export declarations and resolved paths;
- APT/dpkg provider recipe version.

The Python node identity additionally includes:

- logical command requirement and selected supplier identity;
- resolved absolute interpreter path;
- logical Python version;
- Python requirements, wheel hashes, translations, and recipe version.

Every provider node participates in four private identities:

- a bundle-resolver cache key derived from the provider's validated
  resolver-dependency fingerprint, declared provider request, resolver
  recipe/profile, and selected platform; a matching provider-node entry in the
  current deployment's committed build lock may reuse resolution;
- a semantic bundle identity derived from its declared upstream provider
  selection and evidence, exact closed bundle, provider recipe version, and
  selected platform record;
- an assembly cache key derived from the previous finalized local image digest,
  semantic bundle identity, materialization recipe, and renderer profile; and
- a realized prefix-image identity containing the immutable finalized image
  digest after applying that node's layer and attaching its validation record.

The serialized resolved-bundle manifest separates its stored `Identity` from
the payload. The semantic bundle digest is calculated from the complete
canonical payload and excludes the `Identity` field itself. Every load
recalculates the digest and requires it to match both the stored field and the
content-addressed manifest path. An existing manifest is a cache hit only after
that validation; any mismatch is reported as corruption and is never
overwritten.

Target directory, staging/deployed phase, ports, mounts, readiness, runtime
configuration, and workload lifecycle state are not provider-node inputs.
Matching semantic bundle identities reuse exact closed artifacts after
resolution or a matching current build-lock entry. Assembly keys identify
materialization inputs and resulting prefixes, but Reploy does not publish a
machine-wide image reference for cross-deployment lookup.

An executable-output validation record includes its selected invocation path,
resolved terminal path, ownership chain, relevant file digest, and typed facts
such as interpreter implementation, version, ABI, and platform. It covers
direct path and symlink/alternatives selection, not the
transitive programs, ELF loader, shared libraries, or subprocesses used by the
executable. Those are trusted contents and behavior of the exact realized
image. Provider-specific invariants may be stronger; Python entry-point wrappers
must name their own component environment's interpreter.

On Docker and Podman, Reploy stores only the validation schema,
root-filesystem-subject digest, and canonical record digest in Reploy-reserved
OCI image-config labels. The complete canonical record is an immutable object
in the deployment's provider store and is referenced by its build lock. The
subject is the canonical digest of the ordered OCI `rootfs.diff_ids` sequence,
not the image digest: adding image-config labels changes the image digest but
does not change the root filesystem, which avoids a circular identity. The
finalized image digest nevertheless covers the labels and is the realized
prefix identity used by references and caches.

There is no machine-wide Reploy validation database. A missing local record is
a cache miss for a later build, which runs fresh validation and republishes it;
runtime can still verify the committed image labels against the build lock
without loading the record body. OCI labels may be inherited by a child image,
but any child filesystem layer changes the current root-filesystem-chain
fingerprint, so the inherited record is inapplicable. Reploy must validate and
attach a new record before accepting or consuming that prefix. Matching inputs
alone never authorize reuse of observations from a different subject.

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

The final build validation requires every runtime-exposed output to satisfy the
portable access profile and validates every compiled mount destination against
the exact resulting image. Immediately before creating a workload or transient
runtime container, Reploy performs only host-side checks that do not require a
probe container, such as confirming that the selected recorded build still
matches the runtime plan and that declared mount sources exist with the expected
kind and read/write policy. It does not simulate traversal or executable access
under the selected numeric identity. Docker container creation and the workload
are authoritative for runtime permission failures, which Reploy reports without
adding a separate runtime-access record to deployment state.

For an APT output, supplier state records the singleton explicit or well-known
candidate and its mapping provenance. Realized evidence records the selected
ordinary/alternatives chain, exact terminal owner from the locked APT node, and
typed consumer facts. A different path, alternatives selection, owner, or
terminal produces different realized evidence.

The Python resolver-dependency profile includes the selected interpreter's
complete validation evidence, target platform, declared system/build
prerequisites, builder/toolchain profile, requirements and translations, and
local-source manifests and build settings. A changed upstream image triggers a
cheap validation of that profile; an unchanged fingerprint reuses the exact
wheel bundle, while changed evidence reruns the Python resolver.

The Docker backend creates an environment-owned generation reference for each
staged or installed environment. That reference and deployment state pin the
exact image that one environment validated. V1 creates no canonical Reploy
image tag or cross-installation completed-image lookup. Docker and its builder
remain free to reuse physical layers and build-cache entries under Docker's own
policies. Image configuration and Reploy-owned labels contain only content
facts such as the assembly key, base identity, renderer profile, and
root-filesystem-bound validation records. Deployment-directory identity belongs
in reference names, deployment state, and runtime-resource labels; it is never
baked into image content.

Every operation that can change one deployment directory's image references or
state acquires that directory's exclusive operation lock before reading current
state and holds it through publication, state cutover, and cleanup. Operations
for different directories do not share this lock except during install
transfer. Install locks its staged or private temporary source first and holds
that lock through source build selection and closure reads. It acquires the
installed destination lock only after the source build is current, then holds
both through verified transfer and installed-state commit before releasing them
in reverse order. Direct install also locks its private source workspace so both
install paths use the same protocol. Installed deployments are never install
sources, and no operation acquires a source lock while holding an installed-
destination lock, so the fixed source-before-destination order cannot cycle.
An uncached build uses a
collision-resistant operation-specific temporary reference and captures the
backend-reported immutable image ID directly; it never discovers the result by
reinspecting a mutable staging or deployed tag. All output probes and validation
address that immutable ID.

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
4. After the committed state is durable, remove the prior environment
   generation and temporary reference, delete every non-current build lock and
   provider-store object not reachable from the new current lock, then remove
   the pending record last so recovery retains the complete cleanup inventory
   until cleanup finishes.

Recovery runs under the same directory lock. It treats the atomically published
deployment state as authoritative, preserves the generation reachable from
that state, completes or removes only this directory's pending references, and
never retargets another environment. A crash before the commit point leaves the
old state active and the candidate removable; a crash after it leaves the new
state active and the old generation removable. Operations in another directory
cannot change the generation pinned by this state.

After successful recovery or publication cleanup, the directory retains only
the generation named by current state. V1 keeps no previous generation for
rollback and exposes no image-generation rollback command. Docker may retain
the underlying layers under its own cache and garbage-collection policies.

The same cleanup leaves exactly one content-addressed build-lock file: the lock
whose digest is named by current state. A build may temporarily add a candidate
lock while the old lock remains current, but a failed build removes the
candidate and preserves the old lock. Successful publication or recovery also
removes provider-store objects not transitively referenced by the state-selected
lock. The lock directory is therefore an atomic-cutover mechanism, not build
history or a multi-generation cache.

Reploy has no global image-cache references to clean. Environment cleanup
removes only that directory's references and never forcibly deletes physical
images or invokes a global backend prune. Docker owns shared-layer reference
tracking, build-cache garbage collection, and physical reclamation; a future
Podman backend may apply the same ownership rule through its own native image
store.

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

The environment-image build pipeline is explicit heavy work. `reploy build`
runs it without installing the deployment. `reploy install` runs the same
pipeline as part of installation when its staged or temporary workspace does
not already have a matching recorded build. Runtime operations such as
`reploy up` use the recorded result and never resolve providers or build an
image; a missing or stale build fails with instructions to run `reploy build`.

`reploy build --no-cache` bypasses the current deployment's build-lock reuse and
the backend build cache, reruns all provider resolvers, rematerializes every
provider node, and runs full final validation. It does not delete caches and may
still read an already verified immutable raw artifact from that deployment's
content-addressed storage. Results become visible only after the complete build
validates and publishes atomically. Cache-bypass policy is not a blueprint or
semantic-identity input; identical clean-build outputs retain identical
semantic identities.

Install first ensures that its source workspace has a current build. A staged
install reuses a matching staged build or runs the build pipeline there. Direct
install creates a temporary staging-like workspace and runs the build pipeline
there. Install then copies only the provider-store objects transitively
referenced by the selected build lock into the installed deployment's own
`.reploy/` store. Unreferenced or superseded objects are not copied, and the
installed deployment retains no path back to the source. Copying verifies each
locked digest and publishes the destination object atomically before installed
state commits; failure preserves the previous installed state. CLI help and
progress make install's image-build work and its Docker/network requirements
visible.

An assembly cache key does not promise byte-identical image output after an uncached
rebuild. Maintainer scripts, generated caches, build metadata, or other
environmental behavior may vary even when the exact artifacts and recipe are
the same. The semantic identity proves identical declared and resolved provider
inputs; the realized prefix-image identity distinguishes the actual result.

### Bundle Lock Manifest

An environment build records a local lock manifest containing its exact
resolved inputs. The initial implementation uses that lock only for local
identity, validation, and state. It is not a transfer format or a public rebuild
instruction.

The lock is stored as `.reploy/locks/sha256-<digest>.json`, and current state
names that digest. The content-addressed filename lets the current and candidate
locks coexist safely during publication. Outside an active or recoverable
cutover, exactly one lock file remains, and its transitive provider-store
closure is the deployment's complete retained provider cache.

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
- selected executable-output identities and validated compatibility facts.

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
Portable export/import is unsupported in v1 and imposes no additional fields,
archive layout, compatibility loaders, or test obligations on this local lock.

Invalidation follows graph edges:

```text
APT component package change -> APT node + dependent Python node
Python requirement change -> Python node only
port/mount/readiness change -> neither provider node
```

Normal start and restart reuse recorded provider results and generated images.
Only `reploy build` and the explicit build phase of `reploy install` resolve
package sources or refresh artifacts.

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
  or its terminal owner is not an exact member of the complete locked APT node;
- an ordinary symlink hop is unowned, or an alternatives hop is unregistered,
  inconsistent with `update-alternatives --query`, or resolves to an unowned
  terminal;
- an APT export contains `discover` or omits its required explicit executable
  path;
- the built-in `python3` mapping's candidate is missing, is not Python, or does
  not report a parseable version, and no explicit replacement path was given;
- a base-image export omits its required absolute executable path;
- a logical command has no compatible active supplier, or candidate
  dependencies are cyclic;
- the interpreter's logical version does not satisfy the Python constraint;
- the selected Python/package set cannot create the required real component
  environment, with guidance to supply `venv` or select another interpreter;
- a transaction command position contains ordinary data, or a validated or
  generated executable operand lacks the required path and evidence checks;
- qualified output identities or exclusive provider namespaces collide;
- an exclusive provider root is preexisting, has an unsafe ancestor, or lacks
  valid Reploy ownership evidence;
- a shared-authority artifact claims a Reploy-protected path;
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
- APT resolution must use the immutable base's APT configuration and trust
  policy without parsing or rewriting it, propagate every update error, never
  retry under changed trust settings, and inspect and hash every downloaded
  artifact before publication. Provider redaction applies before emitting APT
  diagnostics.
- The provider request model must carry typed command requirements, candidate
  outputs, and selected supplier identities.
- Directory-scoped deployment state must persist one versioned canonical
  request overlay, update it under the existing operation lock, and derive
  component-local overlay subsets.
- Provider bundles/state must record executable provenance and logical probe
  results without conflating package and runtime versions.
- APT bundle and lock models must represent one complete mixed-origin closure,
  require exact installed tuples for base-origin members, require artifacts for
  bundle-origin members, and record base predecessors for upgrades without
  synthesizing package hashes.
- Local environment builds must emit and validate the exact versioned lock
  manifest and bind it to the blueprint, request overlay, selected platform,
  provider recipes, and realized state.
- Local-source builders must separate auxiliary physical locators from content
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

Portable environment export/import is unsupported in v1. Any future transfer
feature requires a separate design and is not part of these slices.

Each slice should retain Python-only behavior and should not make the public
schema accept `type: apt` until the end-to-end path is complete.

## Required Evidence

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
  local lock validation, and identity construction; the Dockerfile frontend is pinned by
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
- Multi-Python tests proving independent interpreter selection,
  component-scoped venv roots and outputs, content-addressed artifact reuse
  within one deployment, parallel independent bundle resolution,
  deterministic sequential image assembly, and logical invalidation confined
  to dependent component nodes.
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
  distinguish option `add`/`remove` from `add-package`/`remove-package`,
  exercise atomic multi-addition behavior, and prove every package request
  still uses its provider's strict grammar.
- Public-schema tests covering the shared component/option/output identifier
  grammar, separator and reserved-name rejection, scope-specific uniqueness,
  provider-owned Python `requirements` and APT `packages` option payloads,
  rejection of structural or nested option fields, omitted interpreter
  normalization to logical `python`, and explicit version/supplier overrides.
- Request-overlay tests proving fully qualified typed entries, stable sorting
  and deduplication, atomic directory-locked updates, blueprint validation,
  component-local invalidation, separation from directory/Docker resource
  identity, complete lock embedding, existing-state exact matching, and
  explicit replacement semantics.
- Local-source tests proving physical locators remain auxiliary local state and
  never enter content identity; source inclusion/ignore rules are deterministic;
  source-manifest, builder/toolchain, platform, setting, metadata, and artifact
  changes affect the correct identity; nominal package versions cannot replace
  content digests; and the provider store contains the built raw artifacts but
  no source tree.
- APT bundle-resolver tests for closed transitive sets, architecture, metadata,
  hashes, component-scoped options, and repository failures. Mixed-origin cases
  cover base-only satisfiers, downloaded members, upgrades with exact base
  predecessors, absence of synthetic base-package hashes, base-digest
  invalidation, exact pre-install base-status checks, final full-closure
  comparison, and local lock round trips.
- APT architecture tests covering every supported OCI mapping and variant,
  native-base agreement, native and `all` closure members, rejection of
  unsupported platforms, mismatched base architecture, configured foreign
  architectures, foreign artifacts or installed tuples, and distinct
  `(package, architecture)` keys.
- APT trust-boundary tests proving source trust options are left to the
  immutable base; Reploy adds no trust override or key handling; every update
  error stops resolution; every downloaded artifact's tuple, metadata, size,
  file list, and digest are validated; base-origin records rely only on the
  immutable base digest; and credentials, tokens, headers, and secret values
  never enter identities, locks, state, labels, caches, logs, or errors.
- Bundle-resolver ingestion tests proving the output mount starts empty and is
  the only writable host-backed mount; the container is stopped before host
  ingestion; only accounted regular raw artifacts with normalized canonical
  names are accepted; links, special files, aliases, races, unaccounted output,
  malformed lengths, and resource exhaustion fail without partial publication;
  sparse files are copied and hashed by logical content; and successful files
  are streamed through metadata/hash validation before one atomic
  resolved-bundle publication.
- Generated-image tests proving offline installation and absence of bundled
  `.deb` files from the final image.
- Recipe-contract tests proving every declared field is rendered or rejected,
  exactly one provider-owned script is mounted and invoked per node, no dynamic
  value is interpolated into its source, positional values remain distinct and
  quoted, ordinary data is rejected in command position, validated upstream
  executables retain supplier/path/digest and provider-specific observed
  evidence, generated
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
- APT invocation tests proving resolution and materialization use one unsplit
  transaction, an operating-system `E2BIG` failure identifies the provider
  node, phase, and package or artifact count, and failed process creation
  publishes no resolver result or node layer.
- APT child-environment tests proving `apt-resolve-v1` and `apt-dpkg-v1` each
  supply exactly their fixed `PATH`, locale, home, temporary directory, debconf
  frontend, APT config, and umask; leak no inherited variables; clean their
  private scratch; and change the corresponding transaction identity and
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
  and group-only terminal files. Runtime tests prove that stale plans and
  invalid mount sources fail without a separate access-probe container, while
  Docker or workload permission failures are reported as runtime failures.
- Filesystem-authority tests covering exclusive-root ancestor validation and
  safe creation, absent component leaves, exact Reploy ownership evidence,
  symlinks treated as unfollowed leaves, rejection of mountpoints and namespace
  overlap, fixed Python destinations, one matching shared system authority,
  delegated APT/dpkg replacement semantics, and protected-root checks on
  streamed archive listings without produced-layer export.
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
  have no network, output is continuously drained, scratch is private, and
  interruption always cleans up.
- Exact-prefix validation tests proving resolver misses validate prerequisites
  before network/source work; bundle-hit materializers validate locked evidence
  before persistent changes; a mismatch commits no layer, triggers one fresh
  resolution against the fixed prefix, and fails if fresh evidence still does
  not match. Broken, cyclic, and non-regular or non-executable tool
  chains fail; `/.reploy-build` as a directory, file, or symlink fails without
  being followed; mounted artifact bytes are not rescanned for identity; and no
  standalone prerequisite-probe container is created.
- Output-record lifetime tests proving fixed schema, subject, and record-digest
  labels are stored in reserved image configuration and covered by the
  finalized image digest; the complete record is a deployment-store object;
  their subject is the root-filesystem layer chain; an inherited record is
  rejected after any child filesystem layer; each provider consumer validates
  its immediate prefix inside the consuming operation; command-exposed outputs
  validate on the final image; compatible drift creates a fresh dependent
  identity; unchanged drift fails; unused outputs are not needlessly validated;
  deleting the store record leaves the committed image runnable; and a later
  build or install build phase recreates missing validation evidence.
- APT/Python tool-mapping tests proving exact `python3` requests publish only
  the singleton `/usr/bin/python3` candidate; pinned and unpinned requests use
  the same versioned mapping; explicit `exports.python.executable` replaces the
  built-in path; other exports require an explicit absolute path; `discover`
  is rejected; and the consuming Python resolver, before network work, reports
  a clear explicit-form correction when the candidate is missing, is not
  Python, or has no parseable version. Python tests also cover actual
  logical-version constraints, lower-layer-first selection of the first
  observed compatible candidate, explicit-supplier incompatibility, missing
  `venv`, and the fact that package versions and author assertions cannot stand
  in for the runtime version.
- APT output-ownership tests proving literal `dpkg-query -S` attribution accepts
  an exact owner anywhere in the complete locked APT node without a per-root
  relationship graph; an owner outside that closure and an ordinary unowned
  hop fail; a registered alternatives chain is accepted only when read-only
  `update-alternatives --query` matches the observed selected value and its
  terminal is closure-owned; missing, malformed, unregistered, mismatched, and
  broken alternatives fail with the direct-terminal explicit-path correction;
  and validation never enumerates or changes alternatives.
- Real-Docker tests from Linux and Docker Desktop hosts using both:
  - a Debian base where an APT component supplies Python; and
  - a Python Debian-family base where an APT component supplies only native
    libraries.
- Cache tests proving APT component changes invalidate Python while Python-only
  changes reuse the APT node within one deployment; independent deployments do
  not consult one another's build locks or Reploy image tags.
- Reference-lifecycle tests proving image labels contain no directory identity;
  same-directory mutations serialize while different directories remain
  independent; builds validate an immutable backend-reported ID under a unique
  temporary reference; state cutover uses unique generation references and an
  atomic state-file commit point; recovery at every cutover boundary preserves
  the state-reachable generation and removes only that directory's abandoned
  references; environment cleanup removes only its own references; and no
  operation issues forced image deletion or global prune.
- Build/install CLI tests proving `reploy build` builds without installing;
  staged install reuses a matching build and builds a missing or stale one;
  direct install builds in its private temporary staging-like workspace; help
  and progress expose install's build work and Docker/network requirements;
  stage and overlay mutations never build; and runtime operations reject a
  missing/stale build without invoking resolution or image construction.
- Install-transfer tests proving staged and direct install copy exactly the
  transitive provider-store closure referenced by the selected current build
  lock, omit unreferenced objects, retain no source path, and preserve previous
  installed state on a failed source build, missing or invalid source object,
  or interrupted copy. Concurrency cases prove source-before-destination lock
  acquisition, direct temporary-workspace locking, exclusion of source
  build/cleanup during transfer, and release of both locks on every exit path.
- Lock-retention tests proving current and candidate locks may coexist only
  during publication or recovery; failed builds preserve the current lock and
  closure; and successful publication or recovery leaves exactly the
  state-selected lock plus its transitive provider-store closure.
- Identity/record regression tests proving input keys and realized identities
  remain distinct, observations are reused only for an exact root-filesystem
  subject and profile, uncached builds always re-probe, changed records
  invalidate only selected dependents, and different deployments remain pinned
  to their own recorded generations.
- Bundle-lock tests proving blueprint binding, canonical-encoding and digest
  vectors, exact artifact/hash and recipe validation, secret omission, and
  verification that the stored identity matches the canonical lock contents
  and deployment-local manifest path.

## Open Provider-Scope Decisions

1. When local `.deb` translations and blueprint-defined repositories become
   justified, including their trust and credential model.

## Authoritative References

- [`apt-get install` package/version syntax](https://manpages.debian.org/bookworm/apt/apt-get.8.en.html#install)
- [Debian Policy: package version fields](https://www.debian.org/doc/debian-policy/ch-controlfields.html#version)
- [Debian Policy: maintainer scripts](https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html)
- [Debian Policy: native `Provides`](https://www.debian.org/doc/debian-policy/ch-relationships.html#virtual-packages-provides)
- [Debian Policy: `update-alternatives`](https://www.debian.org/doc/debian-policy/ap-pkg-alternatives.html)
- [`dpkg-query` installed-file ownership](https://manpages.debian.org/trixie/dpkg/dpkg-query.1.en.html#ACTIONS)
- [Debian Python Policy](https://www.debian.org/doc/packaging-manuals/python-policy/)
- [`dpkg-deb` archive inspection](https://manpages.debian.org/bookworm/dpkg/dpkg-deb.1.en.html)
- [Docker container execution controls](https://docs.docker.com/engine/containers/run/)
