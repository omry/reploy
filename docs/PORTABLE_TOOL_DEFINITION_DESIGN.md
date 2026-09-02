---
status: Active
updated: 2026-09-01
summary: Active composition, targeting, acquisition, identity, and validation model for proposed embedded portable-tool definitions.
refines: docs/REPOSITORY_DESIGN.md
---

# Portable Tool Definition Design

## Status and Authority

This document defines the accepted concrete portable-tool definition model for
the proposed embedded catalog. It refines the portable-tool contract in
`REPOSITORY_DESIGN.md` and the proposed built-in tool behavior in
`BLUEPRINT_ENVIRONMENT_MODEL.md`. Acceptance fixes the intended design; it does
not claim that the implementation is present in this revision.

Delivery sequencing, reviewable task boundaries, migration of the current WIP,
and per-slice acceptance evidence are defined by the
[Portable Tool Definition Implementation Plan](PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md).
That plan implements this document but does not override its normative design
decisions.

The immediate implementation scope is `tool:java`, `tool:playwright`, and
`tool:asciinema==3.2.1`.
Repository publication, TUF metadata, publisher authorization, and lifecycle
policy remain owned by `REPOSITORY_DESIGN.md`. The embedded catalog will be an
implementation bridge, but its definition boundaries are intended to carry
forward into published tool definitions.

Rules in this document assigned to publication validation are performed by the
static definition validation gate. For the embedded catalog, the catalog
generator is the canonical-byte writer: after validation succeeds, it emits the
RFC 8785 canonical bytes for each record and embeds those exact bytes. Catalog
generation does not acquire or execute the tools described by the records.
Repository publication will later run the same validation and canonical
emission before advertising a release.

Canonical portable-tool record contracts have one consumer-neutral owner.
Their versioned field shapes, exact nested-member decoding, per-record bounds,
canonical spelling rules, and record-local validation must be implemented once
below both catalog and provider planning. Catalog loading, embedded-catalog
generation, and locked replay consume that same contract implementation; they
must not maintain parallel record structures or validators for the same schema.

The catalog remains responsible for discovery, authoring resolution, graph
composition, publication validation, and canonical emission. Provider planning
and build locks remain responsible for selected responsibilities, execution
ordering, lock-specific cross-record authorization, acquisition outcomes, and
binding the persisted plan to the build graph. The shared record-contract layer
does not own catalog graph semantics or provider and build-lock policy, and it
must not depend on either consumer. Consolidating the already accepted record
contracts under this ownership does not change their JSON shapes, canonical
bytes, identity inputs, or digests.

A separate implementation WIP uses flat, complete JSON files as a checkpoint;
those files are not the final schema described here or part of this design-only
revision. Reploy has not been released, so the migration does not need a
compatibility reader for that format.

## Goals

- Model a tool version independently from an operating-system release.
- Express support for an exact OS generation and architecture without copying
  the complete tool definition into every target file.
- Keep large version families maintainable without copying invariant authoring
  fields into every version file.
- Allow native packages, pinned upstream artifacts, and ecosystem bindings to
  participate in one explicitly composed tool.
- Make unsupported OS, architecture, binding, and selection combinations fail
  before acquisition.
- Keep definition resolution deterministic, reviewable, lockable, and usable
  without running upstream installer scripts.
- Make it easy to validate a tool on many base images using ordinary Reploy
  blueprint fixtures.
- Keep the model open to package managers and distributions other than APT and
  Debian-family systems.

## Non-Goals

- Defining the repository transport, trust, publication, or lifecycle protocol.
- Accepting third-party definition code or arbitrary installer scripts.
- Normalizing literal package names across distributions.
- Claiming architecture support merely because Reploy can build that OCI
  architecture.
- General-purpose or runtime inheritance, templating, conditional expressions,
  or value overrides inside definition files.
- Designing every future tool category before the initial Java, Playwright, and
  asciinema definitions are complete.

## Decision Summary

1. A requested tool is identified by its tool name, upstream version, and
   Reploy definition revision. The OS version is a target dimension, not the
   tool version.
2. Support is declared for an exact target tuple: OS, OS generation, OCI
   architecture, native package architecture, and package manager.
3. Every exact target tuple has a small target leaf file. Architecture remains
   in that leaf because native packages and upstream artifacts are not
   necessarily available on the same architectures.
4. Tool-wide, release-wide, binding, payload, and reusable native-package data
   live in separate records. Target leaves compose them through explicit,
   digest-checked references.
5. Canonical composition is a closed graph. There is no implicit inheritance,
   overlay, package fallback, or closest-version matching. Before canonical
   composition, the first-party authoring loader may resolve the bounded,
   authoring-only imports and localized single-parent extension defined below;
   neither construct survives into a catalog record.
6. Tools may mix acquisition strategies. Prefer pinned upstream artifacts for
   portable versioned payloads and native packages for system libraries or
   distribution-coupled tools.
7. Locks and build identity cover the selected definition closure. Adding an
   unrelated target must not invalidate materialization for an existing target.
8. A target is supported only after static validation and a real Reploy
   integration test of the exact tuple and selected features.
9. Every acquired artifact is identified by its exact byte size and SHA-256
   digest. Locators and upstream provenance authorize where Reploy may obtain
   those bytes, but do not replace content verification.
10. The release manifest maps one artifact content identity to one source
    record, which declares an ordered, statically bounded mirror set. Reploy
    automatically falls back between those locators under core network and
    resource limits while requiring every mirror to produce the same bytes.
    Artifact records neither declare nor reference mirrors, which is what keeps
    a mirror change out of selected-closure identity.

11. No ceiling is declared on the size of a definition, in records, packages,
    payloads, contributions, or bytes. This is a decision rather than a pending
    item; the Canonical Encoding and Structural Limits section records why, and
    names repository publication as the trigger that would make an aggregate
    ceiling answerable.
12. Authoring files may share invariant fields through explicit local imports
    and one typed `extends` edge. Resolution is deterministic and conflict-only:
    a child may add an absent field but cannot replace or merge a field already
    supplied by its parent. Fully resolved records remain explicit, strict, and
    independently digest checked.

## Terminology

### Tool

A stable user-facing capability such as `java` or `playwright`. The tool record
owns durable naming, summary, provenance, and documentation metadata. It does
not own an OS package list.

### Release

One exact upstream-facing tool version plus one Reploy definition revision.
For example, Playwright `1.61.0` revision `1` and Java `21` revision `1` are
different releases. A revision can correct acquisition, target, validation, or
documentation data without pretending the upstream version changed.

### Target

One exact execution environment described by:

- OCI operating system and architecture;
- observed `/etc/os-release` `ID` and `VERSION_ID`;
- package-manager kind;
- package-manager-native architecture.

For example, Ubuntu 26.04 on `linux/arm64` with APT architecture `arm64` is a
different target from Ubuntu 26.04 on `linux/amd64`, even when their package
names happen to be identical.

### Binding

An application-facing ecosystem interface to a shared tool payload, such as
the Python or Node binding for Playwright. A binding owns its ecosystem package
requirements, artifacts, compatibility constraints, and exports. Bindings do
not own browser or operating-system payloads.

### Payload

A versioned non-package component materialized by a reviewed Reploy primitive.
Examples include a Java runtime archive, Chromium, Chromium Headless Shell, and
FFmpeg. Payload records are exact to every dimension that changes their bytes,
including architecture.

### Native Package Set

A manager-typed set of root package requirements. A target may reference a
named package set when several exact targets truly use the same roots. The set
does not imply that those targets are otherwise equivalent.

### Selected Closure

The canonical resolved projections of the release contract and exact target,
plus the selected binding set, selected payloads, native package sets, and exports
that affect one resolved tool request. The contract projection contains the
selected context, resolved binding set, selection map, and every contract field
that governs their resulting behavior. The
target projection contains the exact target identity and all selected
materialization contributions, but
excludes validation-fixture and validation-profile references because they
authorize support rather than change built bytes. Both projections exclude
unselected availability. Records and contract options for unrelated targets,
bindings, or selections are outside the closure.

## Support Unit

The exact support claim is the following tuple:

```text
(tool, upstream version, definition revision, context,
 target OS, target OS version, OCI architecture,
 package manager, native architecture, binding set, selection-dimension map)
```

The binding set or selection dimensions may be absent only when the release
contract permits that. A definition may advertise a tuple only when every
referenced artifact, binding, native package set, and export is available and
validated for it.

Every advertised tuple is one canonical support case in its target leaf. A
support case contains exactly one allowed context, the exact resolved binding
set or absence, and one complete normalized selection-dimension map. Every
symbol must be declared by the release contract and available in that leaf.
Cases are unique and sorted canonically. The release-wide schemas constrain
what a leaf may advertise; they are never cross-produced with targets or with
each other to invent additional support.

There is intentionally no tool-wide `supported_architectures` promise. The
supported architectures shown to users are derived from valid target leaves.
This prevents an AMD64-only wheel, browser build, or native package from being
mistaken for ARM64 support.

## Definition Records

### Tool Record

The tool record contains only stable catalog metadata:

- schema and qualified tool name;
- the immutable shared version scheme used to parse requirements and order
  releases;
- summary, upstream project, source, and license references;
- documentation metadata;
- references to the available release manifests.

The version scheme is one of Reploy's shared `semver`, `pep440`, `integer`, or
`opaque` schemes and cannot change for an existing qualified tool name. Generic
catalog resolution reads it from this record; it does not contain hard-coded
per-tool parsing or ordering rules. Java initially uses `integer`, while
Playwright uses `semver`.

The tool record also carries an optional exact `default_version`. It is required
for `opaque`, must name one advertised eligible release, and is forbidden for
ordered schemes. An omitted opaque requirement normalizes to equality with this
coordinate. Ordered schemes continue to select their highest compatible release
when no version constraint is supplied.

### Release Manifest

The release manifest owns the published or embedded release coordinate and its
complete availability index:

- tool identity, exact scheme-native tool version, and Reploy definition
  revision;
- one digest-checked release-contract reference;
- the complete set of exact target-leaf references;
- exactly one artifact-source reference for every externally acquired artifact
  content identity reachable from an advertised target;
- release-level provenance and validation-profile references.

Adding or removing a target changes the manifest and requires a new immutable
definition revision. The manifest establishes release provenance and what the
release advertises, but it is deliberately outside selected-closure identity.
This is what permits an unchanged target closure to be reused across two
revisions while the lock still records which revision authorized it.

### Release Contract

The release contract owns behavior shared by all supported targets and stable
across definition revisions when that behavior has not changed:

- allowed use contexts such as `build` or `runtime`;
- binding-set and selection-dimension schemas, including binding selection
  modes and exact supported selection combinations;
- public executable and capability exports;
- environment variables and final-image placement rules;
- reviewed resolver primitive names;
- the canonical supported-Reploy version requirement;
- target-independent compatibility constraints.

The supported-Reploy requirement uses Reploy's built-in SemVer requirement
grammar and is required for every release contract. During candidate filtering,
the running client's exact version must satisfy it and every named primitive
must be implemented by that client. An incompatible candidate is removed before
tool-version or definition-revision selection, allowing an older compatible
release to win before target/contribution traversal or acquisition.

The contract does not enumerate targets and does not contain the Reploy
definition revision. The release manifest references the contract and target
leaves independently. Target leaves then reference the binding, payload, and
native-package records that form one supported closure.

Validation profiles are definition records. A profile owns the
executable reference and argument vector for its probes, while Reploy owns the
constrained executor, environment, working directory, time/output/resource
bounds, and forced network disablement. Targets and fixtures reference a shared
profile rather than replicating the same probe in every variant record. Probe
definitions cannot invoke a shell, relax executor bounds, or enable networking.

### Target Leaf

A target leaf owns only data whose truth is specific to one OS generation and
architecture:

- exact target identity and base-profile match fields;
- the native package manager and native architecture;
- unconditional native package-set references;
- the exact context, resolved binding-set, and selection-map support cases
  advertised by that target, from which its binding and selection availability
  is derived;
- unconditional architecture-specific payload references;
- a canonical target-specific contribution mapping for every advertised
  binding;
- a canonical target-specific contribution mapping for every advertised
  selection;
- target-specific exports when the shared contract is insufficient;
- the integration-fixture and validation-profile references required to prove
  the support claim.

Two target leaves may reference the same immutable native package set or
payload record. They do not inherit from each other. If Ubuntu 25.10 and 26.04
currently use the same package roots, each remains an independently validated
target and explicitly names the shared set. Either can later switch to a new
set without affecting the other.

The binding contribution mapping is keyed by symbols declared in the release
contract and used by at least one target support case. Every such binding has
exactly one entry; an unadvertised symbol cannot have one. Each entry references the
target-compatible binding artifacts it selects, plus any binding-specific
native package-set references and export values. Entries may exact-reference
the same tool's existing artifact, payload, package-set, and validation-profile
records so that common material is declared once. Exports remain inline
canonical `{name, path}` values: identical values deduplicate in the
contribution union, while the same name with different paths is a conflict. The
selected closure's canonical union deduplicates identical contribution
references; validation-profile references remain outside closure identity.
References are typed, acyclic, and intra-tool. This does not introduce a
generic component record, general cross-tool dependencies, or configuration
inheritance in the canonical graph.

A binding request resolves to a set:

1. an explicit supported binding list selects exactly that set;
2. explicit `all` selects every binding advertised by the selected target;
3. an omitted binding selects the advertised bindings matching active
   application providers in the same resolution scope;
4. when no active provider matches, the sole advertised binding may be
   selected implicitly.

If an omitted binding leaves several advertised bindings without an active
provider match, resolution fails and lists the supported values. Resolution
traverses every entry in the resolved set. Record names and reverse
artifact-to-contract references are never used as selection conventions. The
resolved binding set is part of request, lock, cache, and diagnostics identity.

The selection contribution mapping is keyed by `(dimension, value)` pairs
declared in the release contract and used by at least one target support case.
Every such value has exactly one entry; an unadvertised dimension or value
cannot have one. Each entry contains exact
payload and native package-set references plus the export values
contributed by that selection on this target. Resolution unions unconditional
target contributions with only the entries for normalized selected symbols,
then applies the ordinary deduplication and conflict rules. This makes coupled
payloads and selection-specific native roots deterministic without forcing
unselected contributions into the closure.

Selection contributions are strictly additive across those `(dimension,
value)` entries. Release-contract combinations and target support cases
constrain which complete normalized selection maps are valid, but they do not
carry or imply additional payload, native-package, or export contributions.
Schema v1 has no contribution record keyed by a whole selection map. A case
whose required contributions depend on an interaction among selected values
rather than the union of their pair entries is therefore not representable and
must not be advertised under schema v1.

### Binding Contract Record

A binding contract owns ecosystem semantics shared across targets. For the
initial Playwright Python binding these include:

- exact Python requirement roots;
- supported Python versions and wheel tags;
- bundled Node.js and `playwright-core` constituent metadata;
- the Playwright CLI export.

### Binding Artifact Record

A binding artifact record owns one exact platform-specific ecosystem artifact,
such as a Playwright Python wheel:

- component name and exact ecosystem version;
- OCI platform, ecosystem compatibility tags, and any additional compatibility
  fields;
- exact byte size and SHA-256 content digest;
- the reviewed resolver primitive and provider materialization metadata;
- the binding contract that consumes the artifact.

This separation permits a binding contract to remain constant while its wheel
or other artifact differs by architecture. Like a payload record, an externally
acquired binding artifact has an exact content identity and is eligible for one
release-manifest source mapping.

### Payload Record

A payload record contains an exact immutable artifact and its materialization
contract:

- component name, upstream version, and component revision;
- OCI platform and any additional compatibility fields;
- exact byte size and SHA-256 content digest;
- the reviewed resolver primitive and its materialization metadata;
- archive kind, expected inventory, and validated extraction limits;
- install directory, archive root, and declared executable or capability paths.

The SHA-256 digest is the artifact identity and vetting anchor. The URL is only
a locator for bytes expected to have that identity. Content with a different
digest requires a new immutable definition revision even when its advertised
upstream version is unchanged.

### Artifact Source Record

An artifact source record contains retrieval metadata for one acquired artifact
content identity, whether owned by a binding artifact or payload record:

- the expected SHA-256 content digest;
- one or more unique, ordered, credential-free HTTPS mirror URLs;
- upstream release, checksum, signature, or equivalent provenance references;
- source-specific diagnostics that do not affect materialized behavior.

Source-record URLs are long-lived public locators. They must not contain URL
userinfo, a query string, or a fragment; expiring or signed URLs are therefore
not valid definition data. Redirect targets are transient transport data. Their
query strings, if any, are treated as sensitive and are never written to locks,
diagnostics, or provenance records. Operator-owned proxy credentials are also
never exposed to definitions or retained in tool diagnostics.

The release manifest owns the source mapping; binding artifacts, payload
records, and target leaves do not reference source records. Changing a URL or
its provenance therefore requires a new immutable release manifest and
definition revision, but does not change selected-closure identity when the
expected artifact bytes and materialization contract are unchanged. Static
validation requires the mapping key, the source record's expected artifact
SHA-256, and the referenced artifact record's content SHA-256 to agree. The
source-record and artifact-record references are validated independently
against their respective canonical record digests. The artifact record's size
and resolver primitive govern every mapped locator.

The lock records the authorizing source record and acquisition outcome. A
network acquisition records the successful declared source locator; redirect
hops are sanitized transport diagnostics rather than provenance locators. A
verified-cache hit records that no locator was contacted during the operation;
any retained original locator is labeled as historical object provenance rather
than the locator used by the current operation.

Selections map to contributions through the selected target leaf. Playwright's
`chromium` entry, for example, contributes full Chromium, Chromium Headless
Shell, and FFmpeg as one coupled payload set plus any Chromium-specific native
package sets or exports. Contributions common to every request remain
unconditional target references rather than being copied into each selection
entry.

### Native Package-Set Record

A native package set contains:

- package-manager kind;
- manager-specific root requirements;
- optional repository requirements already supported by that provider;
- manager-specific validation metadata.

Package sets are reusable only through explicit references. They do not contain
OS matching expressions and cannot select themselves.

### Integration Fixture Record

A `portable-tool-integration-fixture-v1` record binds one exact target tuple to
a tagged base image, its platform-specific immutable image digest, and the
context, binding set, and selection-dimension map that CI must exercise.
It owns no pass/fail result. Target leaves reference fixture records by
canonical digest.

### Validation Profile Record

A `portable-tool-validation-profile-v1` record owns the executable reference
and argument vector for its probes. A probe shared by multiple targets,
bindings, selections, or fixtures is declared once in a shared profile; a case
that needs distinct validation references an additional profile rather than
embedding an inline probe. Release manifests and target leaves reference
profile records by canonical digest. Placeholder or unresolved validation
references are not valid catalog data.

Reploy owns the probe executor, not the command. The executor prohibits shells,
disables networking, fixes the environment and working directory, and enforces
time, output, and resource bounds. Catalog data cannot relax those constraints.

These records describe required validation work. The resulting pass/fail
evidence remains external to definition identity as described below, avoiding
a cycle in which validating a definition changes the definition being
validated.

## Authoring Imports and Localized Extension

The embedded catalog has a first-party authoring layer before canonical record
composition. It exists to share invariant typed fields across target and
version families without making inheritance part of the catalog, resolver, or
runtime contract. This is the same localized pattern used by blueprint
environment/backend `extends`: references are explicit, the extension is
kind-restricted, and conflicting ownership fails instead of applying an
override.

Each authoring source is one UTF-8, single-document YAML file containing a typed
record fragment with exactly `kind`, optional `imports`, optional `extends`, and
`fields`. Its data model is the JSON-compatible subset of YAML: mappings have
unique string keys; sequences and JSON null, boolean, number, and string scalars
are accepted; explicit tags, anchors, aliases, merge keys, non-string mapping
keys, timestamps, binary values, non-finite numbers, and other YAML-specific
scalar forms are rejected. The source byte limit is enforced before parsing,
and the structural field, member, scalar, and depth limits are enforced while
constructing this node model.

`kind` is a nonempty string naming the canonical record schema the resolved
fragment must produce. `fields` is a mapping of canonical record fields other
than `schema`; the loader inserts `kind` as that field after extension.
`extends`, when present, is one import-alias string. `imports` is a mapping whose
optional reserved `root` member is a nonempty string and whose only other member
is the alias named by `extends`; that member is exactly a mapping containing one
nonempty string `path`. `imports` without `extends`, an `extends` without its one
matching import, and any unused or additional import alias are invalid.

The generator receives an explicit ordered-independent set of entry
descriptors. Each descriptor pairs one authoring source file with its exact
intended catalog-relative `.json` output path. Duplicate source entries and
duplicate output paths are invalid, and output paths pass the existing PTD-11
catalog-path validation unchanged. An entry may extend exactly one imported
fragment of the same `kind`. Imported files may themselves import and extend,
but have no output path and are not emitted merely because they were imported.
An imported file is emitted only when it also has its own explicit entry
descriptor and resolves to a complete valid record.

For example, given this source tree:

```text
java/
  common.yaml
  versions/
    21.yaml
    shared/
      temurin.yaml
```

`java/versions/21.yaml` may declare:

```yaml
kind: portable-tool-release-contract-v1
imports:
  root: ../
  common:
    path: /common.yaml
extends: common
fields:
  # Version-specific fields omitted.
```

`imports.root` is an authoring path anchor, not a general variable. It is
one nonempty file-relative directory path, resolved once relative to the entry
file. It cannot begin with `/`. Imported files inherit that exact root and
cannot redeclare it. A path beginning with `/` is relative to the declared
logical root, never the host filesystem root. Every other path is relative to
the file containing that import, so an import from `versions/21.yaml` with path
`shared/temurin.yaml` resolves to `versions/shared/temurin.yaml`; `./` is
unnecessary and has no special role. Using a root-relative path requires
`imports.root`.

The generator is given one trusted source-tree boundary for the tool. Paths use
`/` separators; backslashes, empty segments, and platform-specific volume or
drive prefixes are invalid. The boundary, resolved root, entry files, imported
files, and every traversed directory must be non-symlink regular files or
directories as appropriate. The resolved root and every source must remain
within the trusted boundary after lexical normalization. Root-relative paths
cannot contain `.` or `..` segments. Absolute filesystem paths, URL imports,
imports from another tool, and paths that escape either the resolved root or
trusted tool boundary are invalid. Import aliases are local to one file, unique,
and match `[a-z][a-z0-9_]{0,63}`; `root` is reserved. Normalized paths relative
to the trusted boundary, rather than lexical aliases, identify files for cycle,
entry-duplicate, and collision detection. The same normalized source may be the
parent of multiple entry closures; it is read, parsed, and represented in the
source manifest once, while cycle detection is evaluated independently along
each entry's parent chain.

Extension copies the fully resolved parent fragment and adds child fields. It
does not perform scalar replacement, list concatenation, map overlay, deletion,
or interpolation. A field present in both parent and child is an error,
including a structured field: authors split independently reusable behavior
into an existing typed record instead of partially merging that field. The
resolved entry must decode as one complete ordinary record and pass all current
record-local and graph validation. `imports`, `root`, import aliases, source
paths, and `extends` are authoring metadata and are absent from canonical bytes,
record identity, selected-closure identity, and runtime state.

Import and extension resolution follows only entry and `extends` parent edges.
Non-raiseable core limits apply to each source, each fully resolved entry, and
the depth of one extension chain. No ceiling applies to the number of authoring
files, their total source bytes, or aggregate resolved fields across one
definition. Missing files, kind mismatches, cycles, ambiguous or unused
aliases, conflicting fields, incomplete resolved entries, and any per-unit or
structural limit violation fail before canonical composition. Diagnostics
identify the entry, complete import and extension chain, and conflicting source
locations.

Each source is opened and read once. The loader hashes those exact raw bytes and
parses the same in-memory bytes, so generation observes one source snapshot per
file. Generation records the reachable transitive authoring closure as a
manifest sorted by normalized `/`-separated source path relative to the trusted
tool boundary, with the SHA-256 of each source's exact raw bytes. That manifest
is build and review evidence, not catalog data, and cannot change canonical
output when the fully resolved record bytes are unchanged. The existing
composer receives only cloned, fully-resolved typed records at the explicit
entry output paths and remains the sole canonical-byte writer.

## Catalog Layout

The filesystem layout follows semantic ownership rather than placing every
definition in one flat directory. A representative layout is:

```text
internal/toolcatalog/definitions/
  java/
    tool.json
    versions/
      21/
        revisions/
          1/
            manifest.json
            contract.json
            sources/
              runtime-linux-amd64.json
              runtime-linux-arm64.json
            payloads/
              runtime-linux-amd64.json
              runtime-linux-arm64.json
            package-sets/
              debian-runtime-amd64.json
            targets/
              debian/
                12/
                  amd64.json
                  arm64.json
              ubuntu/
                26.04/
                  amd64.json
                  arm64.json
            validation/
              fixtures/
                debian-12-amd64.json
                ubuntu-26.04-amd64.json
              profiles/
                default.json
  playwright/
    tool.json
    versions/
      1.61.0/
        revisions/
          1/
            manifest.json
            contract.json
            sources/
              python-linux-amd64.json
              chromium-linux-amd64.json
            bindings/
              python/
                contract.json
                linux-amd64.json
                linux-arm64.json
            payloads/
              chromium/
                linux-amd64.json
                linux-arm64.json
            package-sets/
              debian-12-amd64.json
              ubuntu-t64-amd64.json
            targets/
              debian/
                12/
                  amd64.json
              ubuntu/
                25.10/
                  amd64.json
                26.04/
                  amd64.json
            validation/
              fixtures/
                debian-12-amd64.json
                ubuntu-25.10-amd64.json
                ubuntu-26.04-amd64.json
              profiles/
                default.json
```

The emitted catalog tree is organizational, not an inheritance mechanism.
Authoring imports and extension are resolved before this tree is written. Every
semantic edge in the emitted tree is an explicit record reference. A record is
looked up and persisted by the `(id, digest)` pair carried by that reference;
relative filesystem ancestry does not determine identity. The catalog may
retain multiple canonical records with the same semantic ID and different
digests when different immutable release revisions reference them. One exact
pair must resolve to exactly one canonical record.

Architecture remains visible in target and artifact filenames because those
records make architecture-specific claims. It disappears from files whose
contents are architecture-independent. This is the intended balance between a
single oversized definition and complete per-target duplication.

## Composition Rules

1. A release manifest explicitly references one release contract, enumerates
   its target records, and maps artifact content identities to source records.
   The contract, targets, and payloads do not point back to the manifest or
   select retrieval sources.
2. A target explicitly references every binding artifact through its binding
   contribution mapping. It references every payload and native package set
   that can participate in the target either unconditionally or through a
   binding or selection contribution mapping.
3. Every reference includes or resolves to an expected content digest.
4. References cannot escape their embedded or published release namespace,
   except that a tool record may index release manifests in release namespaces
   beneath that same tool ID. This exception permits release discovery; it does
   not permit a release graph to reference another release or tool.
5. Cycles, duplicate `(id, digest)` definitions, two digests for the same
   record ID in one resolved release graph, and incompatible exact package
   requirements are errors. The same record ID at different digests may coexist
   in the catalog only when separate immutable release revisions select them.
6. There are no canonical or runtime overlays. Canonical records have no
   semantic parents. The authoring-only extension above is resolved away before
   these rules apply and cannot replace, delete, or merge a parent field.
7. There is no implicit fallback between OS versions, architectures, package
   managers, bindings, payload variants, or acquisition strategies.
8. Reusable records are allowed only when their complete semantics are truly
   identical. Sharing a package set does not share target validation evidence.
9. The selected closure is a canonical, order-independent union. Contributions
   with the same semantic key deduplicate only when their complete canonical
   value or referenced digest is identical. Otherwise resolution fails.
10. Semantic keys are provider requirement identity, artifact logical path,
    artifact install destination, environment-variable name, and executable or
    capability export name. Payload-owned directory trees may share an unowned
    parent but cannot overlap each other's owned paths. Identical environment
    values and exports deduplicate; conflicting values or destinations fail
    before acquisition.

These rules, together with conflict-only authoring extension, reduce repetition
without making canonical output depend on merge order.

Mirror failover is not semantic fallback under rule 7. Every mirror in one
source record is authorized only to supply the same size- and checksum-identified
artifact through the same resolver primitive.

## Canonical Encoding and Structural Limits

Every record is strict UTF-8 JSON. Schema v1 uses the JSON Canonicalization
Scheme defined by RFC 8785 for record identity. Parsing rejects invalid UTF-8,
duplicate object member names, and values that cannot be represented by the
schema before semantic decoding. Publication emits canonical bytes; clients
compute record digests from the same canonical representation so alternate
whitespace or member ordering cannot create a second identity.

All portable-tool record schemas share one versioned canonical identity domain.
For a schema-normalized record value `R`, its record digest is exactly:

`"sha256:" + lowercase_hex(SHA-256(UTF-8("reploy:portable-tool-record:portable-tool-record-v1") || 0x00 || canonical-json-v1(R)))`

Here `canonical-json-v1` is the RFC 8785 encoding above, restricted by these
schemas to objects, arrays, strings, booleans, and null; schema integers are
canonical decimal strings rather than JSON numbers. The record's public
`schema` field remains inside `R`. Publishers and clients use the fixed
`portable-tool-record` kind and `portable-tool-record-v1` identity-schema tokens
for every portable-tool record and do not substitute the public record schema
for either token. Digest output is exactly `sha256:` followed by 64 lowercase
hexadecimal characters.

Core schema policy places non-raiseable limits on what a single unit may
contain: individual definition bytes, JSON nesting depth and member count,
string sizes, reference-edge depth, and per-record array sizes. Definitions
cannot raise those limits. Publication and consumption apply the same versioned
limits before decoding a record, so one malformed or hostile file cannot exhaust
a parser.

No upper limit is defined on the size of a definition. Neither the number of
packages, payloads, records, or closure contributions, nor any aggregate data
size, is bounded by a declared ceiling. This is deliberate rather than pending.
No basis for such a number exists: the tools a definition may describe are
open-ended, so measuring the tools already embedded establishes a floor and
never a ceiling; and Reploy is general purpose, running on hardware from large
servers to single-board computers, so no allocation budget generalizes across
clients. A definition is as large as the tool it describes genuinely requires,
and a selected closure is as large as the request it resolves genuinely
requires.

An aggregate ceiling becomes answerable only alongside repository publication
and publisher authorization. Those introduce definitions authored by someone
other than the client's operator, and with them the question of which client
must be able to consume any published definition. That question, not a number
chosen in advance, is what would determine the limit. Until then every
definition is embedded and first-party, the per-unit limits above bound what any
single record or file may contain, and nothing bounds their sum.

One definition-wide ceiling does exist and is unrelated to size: a definition
whose required integration coverage exceeds the core cap is rejected, as
described under Reploy Integration Validation. That cap bounds the CI work a
definition can demand, which is a shared and measurable resource, rather than
the memory a client must have, which is not.

## Acquisition Model

A tool may combine three reviewed sources:

### Pinned Upstream Artifacts

Use exact upstream artifacts for portable, versioned payloads when the upstream
project publishes suitable platform builds. Reploy downloads them during the
networked acquisition phase, verifies their size and digest, and materializes
them offline through a named primitive.

Definition authoring obtains the artifact from its reviewed upstream source,
records its exact size and SHA-256 digest, and retains the available checksum,
signature, release-manifest, or equivalent upstream provenance as review
evidence in its source record. Definition review approves those exact bytes. At
resolution time, Reploy obtains locators from the selected release manifest,
streams the download into its content-addressed artifact store under an enforced
byte limit, and accepts it only after both size and digest match the payload
record. A mismatch is discarded and never reaches a resolver or materializer.

The downloader's network rules protect Reploy from unsafe retrieval behavior;
they do not replace content verification. Retrieval uses HTTPS, definitions
cannot supply credentials or arbitrary headers, redirects are bounded and
revalidated, and local, loopback, link-local, private, and other non-public
destinations are rejected. Before each connection, Reploy resolves the
hostname, rejects the complete answer if any address is not globally routable,
and pins the connection to a validated address while retaining the hostname for
TLS certificate validation. Redirects cannot downgrade HTTPS or carry
credentials; every permitted redirect hop is independently resolved,
validated, and pinned under the same policy. This prevents a second DNS lookup
or redirect from changing a previously validated public destination into an
internal one.

Proxy and credential policy is operator-owned, must preserve the same
destination restrictions, and cannot be changed by a definition. A reviewed
resolver primitive may narrow the allowed URL shape for its upstream, but
Reploy does not need a compiled per-tool origin allowlist to establish artifact
identity.

### Mirror Failover

The consumer checks its verified content-addressed cache first. When acquisition
is necessary, it tries the source record's mirrors in their declared order under
one fixed, bounded retry policy. A transport failure, timeout, non-success HTTP
response, rejected redirect, size mismatch, or SHA-256 mismatch discards any
partial bytes and advances to the next mirror. Every mirror and redirect is
subject to the same network policy and the same expected size and digest.

Core policy places non-raiseable limits on the number of mirrors, attempts per
mirror, aggregate attempts, aggregate downloaded bytes, and total elapsed time
for one artifact acquisition. Each attempt independently enforces the expected
artifact-size bound. Definitions may use fewer mirrors or tighter limits but
cannot increase these caps, and an otherwise finite mirror list that exceeds
them fails static validation.

A successful later mirror may complete acquisition after an earlier failure,
including an integrity mismatch. Reploy durably retains a structured failure
record for every failed attempt so a compromised or stale mirror is visible
rather than silently hidden. Size and digest mismatches are classified
separately from transport failures, identify the artifact and sanitized source,
and never retain the rejected downloaded bytes. The lock records the successful
locator as provenance.
The successful locator is evidence, not a future pin: later consumers may use
another declared mirror when it yields the same verified bytes.

If all mirrors fail, acquisition fails with one diagnostic containing the
ordered per-mirror reasons. Reploy never publishes, caches as verified, or
materializes bytes from a failed attempt. Definitions with duplicate mirrors,
different expected content identities per mirror, an over-limit mirror list,
or an unbounded/dynamic mirror source fail static validation.

This is the preferred initial model for consistent Java versions and
Playwright browser payloads. It avoids tying the user-visible tool version to
whatever version a distribution happens to ship.

### Native System Packages

Use the selected OS provider for system libraries and distribution-coupled
tools. Playwright's browser libraries belong here. Tools such as `debuild` or
`rpmbuild` may also be naturally native because distribution integration is
their purpose.

Literal package roots stay target-specific. Reploy does not translate an APT
name into an RPM, apk, or another APT name merely because the packages provide
similar capabilities.

### Ecosystem Bindings

Bindings contribute strict ecosystem requirements and, when needed, exact
artifacts. They resolve through the owning application provider so the binding
and the application's other dependencies form one dependency graph.

### Mixed Definitions

A release may deliberately mix these sources. Playwright combines a Python
binding, pinned browser artifacts, and native OS libraries. Java may combine a
pinned runtime archive with a small native package set required by that
runtime.

The chosen source for each component is part of the selected closure. Reploy
never silently falls back from an upstream artifact to a system package, or
from one target's package set to another. Changing strategy requires a new
definition revision and explicit target data.

Definitions select reviewed data-driven primitives only. They cannot run
`curl | sh`, `playwright install`, `playwright install-deps`, or arbitrary
package-manager commands.

### Safe Materialization

Archive handling belongs to a named Reploy-owned primitive, not to executable
definition logic. Every primitive enforces path normalization and destination
containment, rejects duplicate normalized paths and unsafe special entries,
applies core entry-count and unpacked-size limits, and installs into a new
destination atomically by extracting into a temporary location it owns and
publishing that location under the final path in one step, so an interrupted
extraction is never observable as a partial install. It never restores
archive-supplied user or group ownership, ACLs, extended attributes, file
capabilities, platform security descriptors, or other privileged metadata.
In the temporary location and before atomic publication, Reploy applies
core-defined ownership and mode normalization, removes group/world write and
privileged bits, and preserves ordinary read or declared executable access only
as required by the payload contract. Symbolic or hard
links are rejected unless the primitive explicitly supports them and proves
that both the link and target remain within the owned archive tree. Device
nodes, sockets, FIFOs, absolute paths, escaping paths, and encrypted entries
are never allowed.

Definition-provided inventory values may tighten limits or describe the vetted
archive, but cannot raise core safety caps or disable checks. Selected payload
destinations are collision-checked before extraction, and materialization runs
without network access. A failed verification or extraction leaves no accepted
partial installation.

## Resolution and Materialization

Reploy resolves all pending tool requirements in this order:

1. Load each tool record's immutable version scheme and normalize every tool
   name, version constraint, optional exact definition revision, context,
   binding request mode and explicit binding set, and selection map without
   choosing a release. Retain each requirement's
   canonical resolution scope: the owning
   application provider identity for a runtime requirement or the isolated
   source-builder identity for a recipe requirement. Requirements for one
   `(scope, qualified tool)` merge under the public constraint rules; identical
   normalized requirements in that scope deduplicate, while an incompatible
   same-scope merge is an error. The same qualified tool in different scopes
   remains separate. When an opaque requirement omits its version, normalize it
   to exact equality with the tool record's `default_version`.
2. Observe the base image's OCI platform, `/etc/os-release`, package manager,
   and manager-native architecture.
3. For each normalized requirement, enumerate authorized release revisions
   satisfying its version constraint and, when supplied, its exact
   definition-revision pin. Version selection follows the shared rules in
   `REPOSITORY_DESIGN.md` except for repository yank state, which the embedded
   catalog does not carry: a revision constraint is valid only alongside an
   exact upstream version, an omitted ordered-scheme constraint selects the
   highest compatible stable version and then its highest eligible revision,
   and prereleases are excluded unless the request names one. Repository-backed
   resolution applies repository yank policy only when that repository supplies
   the required state. Require the running Reploy version and
   primitive set to satisfy each candidate release contract; require exactly
   one target leaf matching the observed base; apply binding inference, merge
   cumulative binding and selection demands under the public rules, and require
   the resulting context, binding set, and selection map to equal one exact
   support case in that target; then traverse the selected references and
   construct the candidate contribution union. A client, target, binding, selection, or
   intrinsic contribution conflict removes that candidate before joint solving.
4. Resolve the remaining scoped candidates as one constraint problem against
   the active provider graph for each scope and every provider or destination
   domain shared by those scopes. Requirements are ordered by canonical scope
   identity, qualified tool name, and then canonical normalized-request bytes.
   Ordered-scheme candidates are tried by descending scheme-native version and
   then descending definition revision; an opaque request has one exact version
   and tries revisions newest first unless pinned. Bounded deterministic
   backtracking selects the lexicographically first complete assignment whose
   contributions are conflict-free within each scope and across genuinely
   shared package-manager, filesystem, environment, export, and capability
   domains. Thus request input order cannot change the result, isolated provider
   environments do not constrain one another, and a candidate is eligible only
   when it participates in a complete assignment. A non-raiseable core cap
   bounds visited assignment states; exceeding it fails closed with a diagnostic
   rather than accepting a partial or order-dependent result.
5. Finalize every chosen target, resolved binding set, selection map, selected
   closure, and the already-validated combined
   contribution union. No complete assignment is an error that reports the
   incompatible requirements. Multiple matching target leaves within one
   candidate are invalid definition data, not fallback choices. The blueprint
   requirements, Reploy policy, catalog and platform facts, and every direct or
   indirect constraint introduced by candidate definitions form one immutable
   operation snapshot. Acquisition executes the finalized selected closures
   from that snapshot unchanged. A change to those constraints starts a new
   operation rather than mutating or re-solving the current one.
6. Reject conflicting semantic keys and overlapping owned paths within the
   chosen union before acquisition.
7. Use the release manifest's source mapping and bounded automatic mirror
   failover to acquire and verify all provider data and upstream artifacts while
   networking is permitted. Retrieval sources are acquisition provenance, not
   members of the selected closure.
8. Materialize without executing target tools. Verify catalog digests and apply
   portable static checks, including archive layout and executable format,
   target operating system, and architecture where those properties are
   inspectable without running the target binary.
9. Record release provenance and selected-closure identity in the lock and
   provider bundle.

Image tags are not target evidence. Target selection uses the validated base
profile observed by Reploy.

## Identity and Digests

Every record has a canonical content digest. Two related identities serve
different purposes:

- **Release provenance identity** records the tool name, scheme-native tool
  version, definition revision, and release-manifest digest. Adding a target
  produces a new immutable revision and therefore new provenance.
- **Selected-closure identity** hashes the tool name and exact scheme-native
  tool version, the canonical resolved release-contract and target projections,
  the exact selected binding records, payload
  records, package-set records, and selected export and selection values used by
  the request. It excludes unselected availability, validation-fixture and
  validation-profile references, the release manifest, definition revision,
  artifact source records, and retrieval URLs. The full contract and target
  records remain covered transitively by release provenance.

The selected-closure identity input is an object with exactly five members:
`tool`, `version`, `contract`, `target`, and `records`. `tool` and `version` are
canonical strings. `contract` has exactly `context`, `bindings`, `selections`,
`runtime`, and `exports`. `bindings` and `runtime` are
null when absent; `bindings` is otherwise the sorted normalized binding set;
`selections` is an object keyed by canonical selection-dimension name, with
each value a sorted normalized selection set; the remaining values are the
resolved runtime and export projections with unselected
availability removed. A runtime object has exactly `install_root` and
`environment`; each environment entry has exactly `name` and `value`. An export
has exactly `name` and `path`.

`target` has exactly `identity`, `package_sets`, `bindings`, `payloads`,
`selections`, and `exports`.
`identity` has exactly `platform`, `os_release_id`, `version_id`,
`oci_architecture`, `native_architecture`, and `package_manager`; target
`bindings` is the sorted array of selected bindings; each entry has exactly
`name`, `contract`, `artifacts`, `package_sets`, and `exports`. Every target
selection has exactly
`dimension`, `value`, `payloads`, `package_sets`, and `exports`. Target
selection entries are sorted by `dimension` and then `value`; duplicate pairs
are invalid. The target's
top-level contribution arrays contain only unconditional contributions; its
binding and selection objects contain only the chosen contributions. `records`
is the sorted unique array of `{id, digest}` references for every selected
binding contract and artifact, payload, and native package set.
Artifact-source, validation-fixture, and validation-profile references never
appear in this input.

Every member is present. Semantically unordered string arrays are sorted by
UTF-8 byte order; record references are sorted by `id` and then `digest`;
named contribution, export, and environment arrays are sorted by canonical
name; and nested reference arrays use the same reference ordering. The input
uses the schema-normalized scalar rules above and is encoded with
`canonical-json-v1`. Its digest is exactly:

`"sha256:" + lowercase_hex(SHA-256(UTF-8("reploy:portable-tool-selected-closure:portable-tool-selected-closure-v1") || 0x00 || canonical-json-v1(input)))`

Publishers and clients therefore compute this identity as
`canonical.Sum("portable-tool-selected-closure",
"portable-tool-selected-closure-v1", input)`; no release revision, source, or
validation field may be added to that versioned input schema.

Locks retain both. Diagnostics can therefore identify the definition release
that authorized a build without making unrelated records part of that build's
materialization identity.

Provider nodes and materialization caches include the selected-closure identity
alongside their ordinary materialization inputs. Adding an ARM64 target, a Node
binding, or a WebKit selection must not by itself invalidate an existing AMD64
Python/Chromium materialization. Reuse across definition revisions is allowed
only when the selected closure is byte-for-byte identical; the lock still
records the newly selected release provenance.

Changing only an artifact mirror or source-provenance reference changes release
provenance but preserves selected-closure identity. Changing the expected
artifact size, SHA-256 digest, extraction contract, or destination changes the
selected closure.

This replaces the aggregate definition digest used by the flat WIP definitions,
where every known target contributed to one digest and an unrelated target
addition invalidated existing build identity.

## Tool-Specific Decisions

### Java

- Java has an explicit upstream version independent of the OS release and
  definition revision.
- A versioned `tool:java` must not use `default-jre-headless` as its stable
  implementation. That package currently maps the same request to different
  Java releases on different distributions.
- The preferred portable strategy is a pinned upstream runtime or JDK artifact
  per supported architecture, with target-specific native dependencies where
  necessary.
- A distribution-native Java variant may be added later only as an explicit
  strategy with truthful version semantics. It is not an automatic fallback.
- The initial Java context remains build-only and preserves source-builder
  ownership. Runtime Java is a separate support decision.
- Build-only recipe requirements use the same canonical tool-request semantics
  as runtime requirements. A simple request may use compact scalar shorthand:
  `tool:java==21` requests Java 21 while `tool:java==21~2` also pins definition
  revision 2. A request with bindings or selections uses the
  structured YAML mapping defined below. An omitted version retains the shared
  newest-eligible resolution rule; the resolved request and lock contain the exact
  upstream version, definition revision, manifest digest, and selected-closure
  digest.
- The initial Java distribution is Eclipse Temurin JDK 21. The public request
  version is `21`; definition revision 1 pins payload component version
  `21.0.12+8`. The JDK is build-only and exports both `java` and `javac`;
  runtime Java remains a later support decision.
- The initial Java target matrix is Debian 12, Debian 13, Ubuntu 25.10, and
  Ubuntu 26.04 on AMD64. ARM64 is added only after an independently validated
  target closure exists.

### Playwright

- The Playwright upstream release, ecosystem binding, and browser selections
  are distinct dimensions.
- A binding contract may have architecture-specific artifacts. The current
  Python wheel must not imply support for architectures for which no validated
  wheel is defined.
- Each browser selection owns its complete coupled payload set. For Chromium,
  that currently includes Chromium, Chromium Headless Shell, and FFmpeg.
- Native browser libraries remain exact target package roots.
- The initial behavior remains Playwright `1.61.0`, Python binding, and explicit
  Chromium selection. Node and additional browsers are later additions, not
  inferred capabilities.
- ARM64 is advertised only if every binding artifact, browser payload, native
  package, extraction rule, and definition-supplied validation-profile probe
  succeeds for the exact target.

## Validation and Test Contract

### Static Definition Validation

Before definitions can be embedded, validation must reject:

- invalid UTF-8, duplicate JSON member names, non-canonicalizable values, or a
  record graph that exceeds a core structural limit;
- unknown schemas, fields, record kinds, package managers, or primitives;
- a missing or unknown tool version scheme, or a release version or alias that
  is invalid under that scheme;
- a version or alias token mapped to more than one canonical tool-version
  coordinate anywhere in one tool index;
- a non-canonical, ambiguous, or non-reversible encoded tool-version segment;
- a missing, malformed, unadvertised, or ineligible opaque `default_version`, or
  any `default_version` on an ordered scheme;
- a missing or invalid supported-Reploy requirement;
- duplicate `(id, digest)` definitions, missing references, cycles, digest
  mismatches, or two digests for one record ID in a resolved release graph;
- references outside the selected release namespace, except same-tool tool
  record references to release manifests beneath that tool ID;
- target tuples that are ambiguous or internally inconsistent;
- advertised bindings or selections with incomplete referenced closures, a
  target binding without exactly one valid contribution mapping, or a target
  selection without exactly one valid contribution mapping;
- duplicate selection dimensions, malformed or duplicate combination maps,
  tuple values not advertised for their dimension, or a combination count over
  the core cap;
- malformed integration fixtures or validation profiles, validation references
  outside their release namespace, or fixtures whose target tuple disagrees
  with the referencing target;
- artifacts without exact size and SHA-256 metadata;
- an externally acquired artifact without exactly one source mapping, or a
  mapping whose key and expected artifact SHA-256 fields disagree, or whose
  source or artifact reference has a canonical record-digest mismatch;
- an artifact-source mapping that is not reachable from any advertised target;
- artifact locators or provenance that violate the selected primitive's
  retrieval contract;
- source-record locators containing userinfo, query strings, fragments, or
  other credential material;
- duplicate, non-canonical, dynamically discovered, or content-inconsistent
  mirror entries;
- conflicting exports or incompatible exact provider requirements;
- a claimed architecture without architecture-compatible artifacts.

Unit tests cover parsing, reference resolution, target selection, closure
construction, canonical identity, error diagnostics, and the rule that adding
an unrelated target, binding option, or selection option does not change an
existing selected-closure digest. They also cover identical contribution
deduplication and every conflicting semantic key or overlapping destination
described above. Candidate-selection tests cover selection-map normalization,
exact supported combinations, target compatibility, exact revision pins,
version-scheme ordering, opaque defaults, canonical version-segment encoding,
alias uniqueness, selection compatibility, and filtering a newer Reploy- or
provider-incompatible release in favor of the highest compatible release.
Binding and selection tests cover complete target mappings, coupled
contributions, and exclusion of unselected artifacts, payloads, and native
roots. Validation-record tests cover strict fixture/profile parsing, namespace
and target agreement, missing or wrong-kind references, and complete bounded
advertised-combination coverage.
Identity tests prove that a source-locator-only change
or validation-only reference change preserves selected-closure identity while
a resolved contract or target behavior, artifact size, digest, or
materialization change does not. Resolver-primitive
tests cover verified-cache hits without networking, deterministic mirror order,
fallback after each eligible failure class, size and digest mismatch cleanup,
network-versus-cache provenance, successful-locator provenance, aggregate
exhaustion diagnostics, mirror and aggregate resource caps, bounded redirects,
mixed public/non-public DNS answers, DNS rebinding resistance, redirect-hop
resolution and pinning, redirect and proxy credential redaction, rejected
non-public destinations, archive path and entry-type attacks, archive ownership,
ACL, extended-attribute, capability, and mode normalization, extraction limits,
and atomic cleanup after failure. Static compatibility tests inspect executable
formats and target OS/architecture without executing target binaries. Parser tests cover duplicate member names,
canonical-equivalent encodings, and every structural limit. Static mapping tests
cover missing, duplicate, orphaned, and identity-inconsistent artifact-source
mappings. Build tests prove that validation-profile commands cannot enter
materialization or runtime plans and that cross-platform materialization never
requires executing a target binary.

### Reploy Integration Validation

Every advertised target tuple requires an integration fixture that uses Reploy
itself against a representative base image. The fixture must exercise the same
definition resolution, provider merge, acquisition, offline materialization,
and final-image behavior used by a real blueprint.

The integration plan is derived from release manifests and each target leaf's
exact support-case list, not maintained as an independent handwritten target
list. CI fails when any advertised context, target, binding set, or normalized
selection combination lacks a runnable case, or when a fixture claims a case
the target does not advertise. Every support tuple is explicit definition data
and is exercised directly under the core publication cap; Reploy never infers
an outer product. Hand-authored evidence metadata cannot satisfy this coverage
gate.

On a compatible integration runner, Java's definition-supplied profile probe
checks the executable and confirms the requested Java version. Playwright's
definition-supplied profile probes import every selected binding, launch each
selected browser, load a local page, and exit cleanly. The Reploy-owned probe
executor disables networking and enforces the fixed execution bounds.
Asciinema's definition-supplied profile probe reports exact version `3.2.1`.
A probe failure fails that integration-validation job and produces no
successful support record; it does not make cross-platform build
materialization depend on executing the target binary. Negative fixtures verify
that unsupported contexts, OS versions, architectures, bindings, and selections
fail before downloads begin.

Architecture support requires execution on that architecture or an explicitly
approved equivalent CI environment. Schema coverage or successful AMD64 tests
cannot establish ARM64 support.

Successful cases produce evidence bound to the release provenance and selected
closure, exact observed target tuple, base-image platform and immutable digest,
binding, selection map, and validator version. The
documented support matrix is generated only by joining manifest entries with
current successful external validation records. These are trusted first-party
CI records over immutable definition digests, not authenticated attestations;
their trust comes from the project-controlled workflow and evidence store. They
are not embedded back into the records they validate. A target
file's presence, an AMD64 result for an ARM64 leaf, or a manually asserted
result is not sufficient.

## Migration from the Flat WIP Definitions

This section records the migration this design intends. It is not a delivery
tracker: sequencing, per-step status, and acceptance evidence belong to the
implementation plan named under Status and Authority, which tracks delivery but
cannot override this design. If implementation needs to diverge, the conflict
must be escalated and this design corrected before implementation proceeds.

1. Replace the flat loader with strict record parsing and explicit reference
   resolution. Do not retain a compatibility path for the unreleased format.
2. Split the current shared Java and Playwright metadata into tool, release
   manifest, and target-independent release-contract records.
3. Move Playwright's Python binding contract and architecture-specific wheel
   into binding records.
4. Move browser content and materialization contracts into selection payload
   records, their URLs and provenance into manifest-owned source records, and
   native libraries into manager-typed package sets.
5. Convert each current OS/architecture file into a small exact target leaf.
6. Extend local-source recipe parsing to accept the compact simple form and the
   structured YAML form of the canonical tool request, and carry exact Java
   upstream version, definition revision, manifest digest,
   and selected-closure digest through resolved requests, provider requests,
   build evidence, and locks. Update ADR 0001 and its examples with that public
   behavior.
7. Replace Java's distribution-default package with explicitly versioned
   payloads after choosing the initial Java distribution and versions.
8. Preserve the current verified Debian and Ubuntu behavior while rebuilding
   its unit and manifest-derived Reploy integration coverage around the selected
   closure.
9. Add ARM64 leaves only after the full artifact and integration contract is
   satisfied for each exact target.
10. Remove the old flat definitions, aggregate digest behavior, and tests in the
   same change; the two formats must not coexist as public contracts.

## Initial Implementation Decisions

All schema fields use lower `snake_case`. Every reference is an object with
exactly `id` and `digest` fields. Canonical record IDs are slash-delimited
semantic names rooted at `tool:<name>`. A release namespace is
`tool:<name>/releases/<encoded-tool-version>`; its manifest is
`tool:<name>/releases/<encoded-tool-version>/revisions/<definition-revision>/manifest`.
Contracts, targets, bindings, payloads, package sets, integration fixtures, and
validation profiles have semantic IDs beneath the release namespace without a
revision segment. Validation record IDs use `validation/fixtures/<name>` and
`validation/profiles/<name>` beneath that namespace. Their lookup and
persistence identity is the `(id, digest)` pair: an unchanged record reuses the
same pair across manifest revisions, while a corrected record keeps its
semantic ID, receives a new digest, and coexists with the historical pair.
Physical storage may place a changed record blob beneath its owning revision;
the path remains organizational and references never resolve by path.
Manifest-owned source-record IDs live beneath the revision namespace. IDs use
ASCII letters, digits, `.`, `+`, `-`, `_`, `%`, `:`, and `/`, with no empty,
`.` or `..` path segments.

The encoded tool-version segment is derived reversibly from the version
scheme's canonical UTF-8 representation. ASCII letters, digits, `.`, `+`, `-`,
and `_` remain literal; every other byte, including `%`, is encoded as `%HH`
with uppercase hexadecimal digits. If the result would be `.` or `..`, every
dot is encoded. Decoding rejects lowercase hex, escapes for bytes that should
have remained literal except for the required `.` or `..` escape, invalid
UTF-8, or a value that is not canonical under the tool's scheme. The same
encoded segment is used as the version directory
name, so common versions such as `21` and `1.61.0` remain readable while PEP
440 epochs and arbitrary opaque versions retain one canonical path and ID.

Definition JSON uses the record schemas named in this document with a
`portable-tool-` prefix and `-v1` suffix. Integer quantities are canonical
decimal strings. Parsing is strict: duplicate members, unknown fields, invalid
UTF-8, noncanonical IDs or decimal strings, and values outside core structural
limits are errors before references are resolved.

Each release manifest records the exact scheme-native tool version and an
explicit, sorted list of accepted public version aliases. Resolution matches
only that exact version or one of those aliases; generic catalog code does not
infer tool-specific major-version semantics. Exact component or build versions
that are more specific than the public tool coordinate belong to payload or
binding-artifact records.

Across the canonical tool-version coordinates indexed by one tool record, exact
versions and aliases form one collision-free lookup map. Each normalized input
token resolves to exactly one coordinate, after which ordinary revision
selection chooses among that coordinate's immutable release manifests. Multiple
definition revisions may therefore repeat the coordinate's exact version and
aliases. An alias may also repeat across those revisions when it maps to that
same coordinate, but it cannot equal another coordinate's exact version or an
alias mapped to another coordinate. An alias equal to its own coordinate's exact
version is redundant and invalid. Publication validates this invariant over the
complete tool index before any release is advertised.

The release contract represents available binding symbols as sorted `options`
and carries no binding default. A request records one canonical binding mode:
omitted, an explicit sorted nonempty set, or `all`. After target selection,
explicit values must be advertised by that target, `all` expands to every
advertised value, and omission resolves from matching active application
providers or the sole advertised value. The resolved binding set is sorted and
participates in request, closure, cache, lock, and diagnostic identity.

One canonical tool request has two YAML authoring forms. A request without
options may use compact scalar shorthand such as `tool:java==21`; the compact
grammar carries only the qualified tool name, version requirement, and optional
exact definition revision. It never carries bindings or selections.
The compact `~<revision>` suffix is available only when the tool's version
scheme excludes `~` from exact version tokens. Schemes that can contain `~`
require the structured `definition_revision` field.

A request with options uses a mapping. Application/runtime mappings are entries
under `environment.applications.<application>.packages.tools`; source-build
mappings are entries under `.reploy.yaml` `requires`. The container determines
the request context. `tool` names the qualified tool, `version` carries the
version requirement, and optional `definition_revision` pins one revision. The
standard `binding` field accepts a scalar, a list, or the quoted value `"*"` for
all advertised bindings; omission retains the inference rule above. `select`
is a mapping from definition-declared selection-dimension names to a scalar or
list:

```yaml
tool: playwright
version: "1.61.0"
definition_revision: 2
binding: "*"
select:
  browser: [webkit, chromium]
```

Both forms normalize to the same canonical internal request. Scalars in
`binding` or `select` normalize to singleton sets; lists normalize to sorted
sets. Unknown fields, dimensions, or values, duplicate values, empty lists, and
a wildcard mixed with explicit values are invalid. No compact bracket grammar
is defined.

The release contract represents selections with an ordered `dimensions` array
and an explicit `combinations` array of dimension-keyed maps. Each present map
value is a scalar or list that normalizes to a sorted nonempty set. Required
dimensions must appear in every combination; optional dimensions may be
omitted, and omission is distinct from an empty list. Dimensions and normalized
combination maps are unique, values must be advertised for their dimension, and
the non-raiseable core cap bounds the number of combinations. Combination maps
are ordered by canonical encoded bytes. A request is valid only when its
normalized dimension-to-set map exactly matches an advertised combination.
Reploy never infers a Cartesian product. A future authoring helper may expand
product shorthand into this same explicit canonical matrix before validation.
Standard request field names are reserved; publication rejects a dimension-name
collision.
Playwright initially advertises only the `python` binding, so an omitted
binding resolves to that sole advertised value while an explicit `[python]`
remains valid. Its `browser` selection dimension advertises the sole exact
combination `[chromium]`. Java permits neither a binding nor a selection.
Binding requirements remain owned by
binding-contract records, while the release contract owns final-image placement
and environment values directly.

The initial Java implementation is Eclipse Temurin JDK 21 for build use. The
public request `tool:java==21` resolves integer tool version `21`, definition
revision 1, whose JDK payload records exact Temurin component version
`21.0.12+8`. The first target matrix is Debian 12, Debian 13, Ubuntu 25.10, and
Ubuntu 26.04 on AMD64. The JDK payload is pinned by exact size and SHA-256 and
exports `java` and `javac`; no distribution-default Java package is a fallback.

The initial Playwright implementation remains upstream `1.61.0`, Python
binding, and explicit Chromium selection on the existing validated AMD64
targets. ARM64 is not advertised for Java or Playwright until every artifact,
native dependency, materialization rule, and definition-supplied
validation-profile probe for the exact target succeeds.

The initial asciinema implementation is upstream `3.2.1` for build use on the
following exact target tuples:

| OS ID | OS version | OCI architecture | Package manager | Native architecture | Upstream payload |
| --- | --- | --- | --- | --- | --- |
| `debian` | `12` | `amd64` | `apt` | `amd64` | `asciinema-x86_64-unknown-linux-gnu` |
| `debian` | `12` | `arm64` | `apt` | `arm64` | `asciinema-aarch64-unknown-linux-gnu` |
| `debian` | `13` | `amd64` | `apt` | `amd64` | `asciinema-x86_64-unknown-linux-gnu` |
| `debian` | `13` | `arm64` | `apt` | `arm64` | `asciinema-aarch64-unknown-linux-gnu` |
| `ubuntu` | `25.10` | `amd64` | `apt` | `amd64` | `asciinema-x86_64-unknown-linux-gnu` |
| `ubuntu` | `25.10` | `arm64` | `apt` | `arm64` | `asciinema-aarch64-unknown-linux-gnu` |
| `ubuntu` | `26.04` | `amd64` | `apt` | `amd64` | `asciinema-x86_64-unknown-linux-gnu` |
| `ubuntu` | `26.04` | `arm64` | `apt` | `arm64` | `asciinema-aarch64-unknown-linux-gnu` |

Here `apt` is the observed target package manager and the provider for any
native package roots; asciinema itself is acquired from the pinned GitHub
release asset rather than from an APT repository.

These eight tuples are pinned initial support claims, not a moving
"latest two" policy. Each has its own target leaf and integration fixture, but
the leaves share one release contract and validation profile. The four AMD64
leaves exact-reference one payload record and the four ARM64 leaves
exact-reference the other, so the existing explicit-reference composition and
catalog generator declare target-independent metadata once without target
inheritance. Both GitHub-hosted GNU/Linux release assets are pinned by size and
SHA-256. The upstream Apple/Darwin and AMD64-only Linux musl assets are not
advertised. Asciinema v2 remains a non-published coexistence analysis; it is
neither implemented nor advertised.

Validation evidence is external to definition identity. Schema v1 records the
tool, upstream version, definition revision, manifest digest, selected-closure
digest, context, exact target tuple, immutable base-image digest, binding set
and selection-dimension map, fixture ID, validator version, result, and
validator-output digest. Only a passing record whose immutable fields match the
selected manifest and closure can contribute to the generated support matrix.

These decisions settle the initial wire representation and support claims.
Additional Java or asciinema versions, additional asciinema target OS
generations or payload variants, runtime Java, Playwright bindings or browsers,
Java or Playwright ARM64, and repository publication remain explicit later
extensions; none is inferred from the initial embedded records.
