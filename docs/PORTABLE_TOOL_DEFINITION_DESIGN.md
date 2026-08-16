---
status: Accepted
updated: 2026-08-16
summary: Accepted composition, targeting, acquisition, identity, and validation model for proposed embedded portable-tool definitions.
refines: docs/REPOSITORY_DESIGN.md
---

# Portable Tool Definition Design

## Status and Authority

This document defines the accepted concrete portable-tool definition model for
the proposed embedded catalog. It refines the portable-tool contract in
`REPOSITORY_DESIGN.md` and the proposed built-in tool behavior in
`BLUEPRINT_ENVIRONMENT_MODEL.md`. Acceptance fixes the intended design; it does
not claim that the implementation is present in this revision.

The immediate implementation scope is `tool:java` and `tool:playwright`.
Repository publication, TUF metadata, publisher authorization, and lifecycle
policy remain owned by `REPOSITORY_DESIGN.md`. The embedded catalog will be an
implementation bridge, but its definition boundaries are intended to carry
forward into published tool definitions.

A separate implementation WIP uses flat, complete JSON files as a checkpoint;
those files are not the final schema described here or part of this design-only
revision. Reploy has not been released, so the migration does not need a
compatibility reader for that format.

## Goals

- Model a tool version independently from an operating-system release.
- Express support for an exact OS generation and architecture without copying
  the complete tool definition into every target file.
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
- General-purpose inheritance, templating, or conditional expressions inside
  definition files.
- Designing every future tool category before Java and Playwright are complete.

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
5. Composition is a closed graph. There is no implicit inheritance, overlay,
   package fallback, or closest-version matching.
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
10. One artifact may declare an ordered, statically bounded mirror set. Reploy
    automatically falls back between those locators under core network and
    resource limits while requiring every mirror to produce the same bytes.

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
plus the selected binding, selected payloads, native package sets, exports, and
probes that affect one resolved tool request. The contract projection contains
the selected context, resolved binding, selections, normalized parameter
values, and every contract field that governs their resulting behavior. The
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
 package manager, native architecture, binding, selections,
 normalized parameters)
```

The binding, selections, or parameters may be absent only when the release
contract permits that. A definition may advertise a tuple only when every
referenced artifact, binding, native package set, export, and probe is available
and validated for it.

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
- binding, selection, and typed-parameter schemas, including required/default
  behavior;
- public executable and capability exports;
- environment variables and final-image placement rules;
- reviewed resolver primitive names;
- the canonical supported-Reploy version requirement;
- target-independent probes and other compatibility constraints.

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

### Target Leaf

A target leaf owns only data whose truth is specific to one OS generation and
architecture:

- exact target identity and base-profile match fields;
- the native package manager and native architecture;
- unconditional native package-set references;
- the bindings, selections, and typed-parameter constraints available on that
  target;
- unconditional architecture-specific payload references;
- a canonical target-specific contribution mapping for every advertised
  binding;
- a canonical target-specific contribution mapping for every advertised
  selection;
- target-specific exports or probes when the shared contract is insufficient;
- the integration-fixture and validation-profile references required to prove
  the support claim.

Two target leaves may reference the same immutable native package set or
payload record. They do not inherit from each other. If Ubuntu 25.10 and 26.04
currently use the same package roots, each remains an independently validated
target and explicitly names the shared set. Either can later switch to a new
set without affecting the other.

A target leaf's parameter constraints are keyed by a parameter declared in the
release contract. They may narrow that parameter's enumerated values or numeric
range, but cannot change its type, required/default behavior, or widen its
contract-level domain. Omitting a target constraint leaves the complete
contract-level domain available. Publication rejects a target whose narrowed
domain excludes the contract default. Resolution validates normalized
parameter values against both the contract schema and the selected target's
constraints before acquisition.

The binding contribution mapping is keyed by symbols declared in the release
contract and advertised by the target. Every advertised binding has exactly one
entry; an unadvertised symbol cannot have one. Each entry references exactly one
binding contract and the target-compatible binding artifacts it selects, plus
any binding-specific native package-set references and export or probe values.
Binding inference chooses a public symbol first, and resolution traverses only
that entry. Record names and reverse artifact-to-contract references are never
used as selection conventions.

The selection contribution mapping is keyed by symbols declared in the release
contract and advertised by the target. Every advertised selection has exactly
one entry; an unadvertised symbol cannot have one. Each entry contains exact
payload and native package-set references plus the export and probe values
contributed by that selection on this target. Resolution unions unconditional
target contributions with only the entries for normalized selected symbols,
then applies the ordinary deduplication and conflict rules. This makes coupled
payloads and selection-specific native roots deterministic without forcing
unselected contributions into the closure.

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
- install directory, archive root, and executable or capability probes.

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
package sets, exports, or probes. Contributions common to every request remain
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
context, binding, selections, and normalized parameters that CI must exercise.
It owns no pass/fail result. Target leaves reference fixture records by
canonical digest.

### Validation Profile Record

A `portable-tool-validation-profile-v1` record names the reviewed
Reploy-owned validator and version for one tool release, its required probes,
and the requirement that materialization and validation run without network
access. Release manifests and target leaves reference profile records by
canonical digest. Placeholder or unresolved validation references are not
valid catalog data.

These records describe required validation work. The resulting pass/fail
evidence remains external to definition identity as described below, avoiding
a cycle in which validating a definition changes the definition being
validated.

## Catalog Layout

The filesystem layout follows semantic ownership rather than placing every
definition in one flat directory. A representative layout is:

```text
internal/toolcatalog/definitions/
  java/
    tool.json
    21/
      contract.json
      payloads/
        jdk-linux-amd64.json
      targets/
        debian/
          12/
            amd64.json
          13/
            amd64.json
        ubuntu/
          25.10/
            amd64.json
          26.04/
            amd64.json
      validation/
        fixtures/
          debian-12-amd64.json
          debian-13-amd64.json
          ubuntu-25.10-amd64.json
          ubuntu-26.04-amd64.json
        profiles/
          default.json
      revisions/
        1/
          manifest.json
          sources/
            jdk-linux-amd64.json
  playwright/
    tool.json
    1.61.0/
      contract.json
      bindings/
        python/
          contract.json
          linux-amd64.json
      package-sets/
        debian-12-amd64.json
        ubuntu-t64-amd64.json
      payloads/
        chromium/
          chromium-headless-shell-linux-amd64.json
          chromium-linux-amd64.json
          ffmpeg-linux-amd64.json
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
      revisions/
        1/
          manifest.json
          sources/
            python-linux-amd64.json
            chromium-linux-amd64.json
            chromium-headless-shell-linux-amd64.json
            ffmpeg-linux-amd64.json
```

The tree is organizational, not an inheritance mechanism. Every semantic edge
is an explicit record reference. A record is looked up and persisted by the
`(id, digest)` pair carried by that reference; relative filesystem ancestry does
not determine identity. The catalog may retain multiple canonical records with
the same semantic ID and different digests when different immutable release
revisions reference them. One exact pair must resolve to exactly one canonical
record.

Each exact scheme-native tool version is a directory immediately below its
tool. A redundant `versions/` wrapper adds no ownership or identity information
and is therefore omitted.

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
6. There are no overlays. A child cannot add to, delete from, or override a
   parent because there are no semantic parents.
7. There is no implicit fallback between OS versions, architectures, package
   managers, bindings, payload variants, or acquisition strategies.
8. Reusable records are allowed only when their complete semantics are truly
   identical. Sharing a package set does not share target validation evidence.
9. The selected closure is a canonical, order-independent union. Contributions
   with the same semantic key deduplicate only when their complete canonical
   value or referenced digest is identical. Otherwise resolution fails.
10. Semantic keys include provider requirement identity, artifact logical path,
    artifact install destination, environment-variable name, executable or
    capability export name, and probe identity. Payload-owned directory trees
    may share an unowned parent but cannot overlap each other's owned paths.
    Identical environment values and exports deduplicate; conflicting values or
    destinations fail before acquisition.

These rules make repetition an explicit authoring tradeoff without making the
resolved result depend on merge order.

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

Core schema policy also places non-raiseable limits on individual and aggregate
definition bytes, record count, reference-edge count and depth, string and
array sizes, and selected-closure contributions. Definitions cannot raise those
limits. Publication and consumption apply the same versioned limits before
allocating or traversing the complete graph, preventing an authenticated but
pathological definition from exhausting client or repository resources.

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
including an integrity mismatch. Reploy retains structured diagnostics for
every failed attempt so a compromised or stale mirror is visible rather than
silently hidden, and the lock records the successful locator as provenance.
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
destination atomically. It never restores archive-supplied user or group
ownership, ACLs, extended attributes, file capabilities, platform security
descriptors, or other privileged metadata. Reploy applies core-defined ownership
and mode normalization, removes group/world write and privileged bits, and
preserves ordinary read or declared executable access only as required by the
payload contract. Symbolic or hard links are rejected unless the primitive
explicitly supports them and proves that both the link and target remain within
the owned archive tree. Device nodes, sockets, FIFOs, absolute paths, escaping
paths, and encrypted entries are never allowed.

Definition-provided inventory values may tighten limits or describe the vetted
archive, but cannot raise core safety caps or disable checks. Selected payload
destinations are collision-checked before extraction, and materialization runs
without network access. A failed verification or extraction leaves no accepted
partial installation.

## Resolution and Materialization

Reploy resolves all pending tool requirements in this order:

1. Load each tool record's immutable version scheme and normalize every tool
   name, version constraint, optional exact definition revision, context,
   binding request, selection set, and typed parameter value without choosing a
   release. Requirements for one qualified tool merge under the public
   constraint rules; identical normalized requirements deduplicate, while an
   incompatible merge is an error. When an opaque requirement omits its version,
   normalize it to exact equality with the tool record's `default_version`.
2. Observe the base image's OCI platform, `/etc/os-release`, package manager,
   and manager-native architecture.
3. For each normalized requirement, enumerate authorized release revisions
   satisfying its version constraint and, when supplied, its exact
   definition-revision pin. Require the running Reploy version and primitive set
   to satisfy each candidate release contract; require exactly one target leaf
   matching the observed base; apply binding inference and validate the context,
   selection set, and parameter values against that target; then traverse the
   selected references and construct the candidate contribution union. A
   client, target, binding, selection, parameter, or intrinsic contribution
   conflict removes that candidate before joint solving.
4. Resolve the remaining candidates for every pending tool as one constraint
   problem against the complete active application provider graph. Requirements
   are ordered by qualified tool name and then canonical normalized-request
   bytes. Ordered-scheme candidates are tried by descending scheme-native
   version and then descending definition revision; an opaque request has one
   exact version and tries revisions newest first unless pinned. Bounded
   deterministic backtracking selects the lexicographically first complete
   assignment whose combined tool contributions and active provider graph are
   conflict-free. Thus request input order cannot change the result, and a
   candidate is eligible only when it participates in a complete assignment.
   A non-raiseable core cap bounds visited assignment states; exceeding it fails
   closed with a diagnostic rather than accepting a partial or order-dependent
   result.
5. Finalize every chosen target, inferred binding, selection set, normalized
   parameter map, selected closure, and the already-validated combined
   contribution union. No complete assignment is an error that reports the
   incompatible requirements. Multiple matching target leaves within one
   candidate are invalid definition data, not fallback choices. Recheck that
   the provider inputs used for joint solving have not changed before
   acquisition.
6. Reject conflicting semantic keys and overlapping owned paths within the
   chosen union before acquisition.
7. Use the release manifest's source mapping and bounded automatic mirror
   failover to acquire and verify all provider data and upstream artifacts while
   networking is permitted. Retrieval sources are acquisition provenance, not
   members of the selected closure.
8. Materialize and run every declared probe with networking disabled.
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
  tool version, the canonical resolved contract projection (chosen context,
  binding, selections, normalized parameter values, runtime, exports, and
  probes), the canonical resolved target projection, the exact selected
  binding, payload, and package-set records, and selected export, probe, and
  selection values used by the request. The target projection retains the exact
  target identity, unconditional references, selected binding and selection
  contributions, target probes, and other materialization behavior. Both
  projections exclude unselected availability and validation-fixture and
  validation-profile references. The identity also excludes the release
  manifest, definition revision, artifact source records, and retrieval URLs;
  the full contract and target records remain covered transitively by release
  provenance.

The selected-closure identity input is an object with exactly five members:
`tool`, `version`, `contract`, `target`, and `records`. `tool` and `version` are
canonical strings. `contract` has exactly `context`, `binding`, `selections`,
`parameters`, `runtime`, `exports`, and `probes`. `binding` and `runtime` are
null when absent; `selections` is the sorted normalized selection set;
`parameters` is an object keyed by canonical parameter name; and the remaining
values are the resolved runtime, export, and probe projections with unselected
availability removed. A runtime object has exactly `install_root` and
`environment`; each environment entry has exactly `name` and `value`. An export
has exactly `name` and `path`. A probe has exactly `path`, `args`, and `network`,
where `args` retains argument order and `network` is `none` in schema v1.

`target` has exactly `identity`, `package_sets`, `binding`, `payloads`,
`selections`, `exports`, and `probes`.
`identity` has exactly `platform`, `os_release_id`, `version_id`,
`oci_architecture`, `native_architecture`, and `package_manager`; target
`binding` is null when absent and otherwise has exactly `name`, `contract`,
`artifacts`, `package_sets`, `exports`, and `probes`. Every target selection has
exactly `name`, `payloads`, `package_sets`, `exports`, and `probes`. The target's
top-level contribution arrays contain only unconditional contributions; its
binding and selection objects contain only the chosen contributions. `records`
is the sorted unique array of `{id, digest}` references for every selected
binding contract and artifact, payload, and native package set.
Artifact-source, validation-fixture, and validation-profile references never
appear in this input.

Every member is present. Semantically unordered string arrays are sorted by
UTF-8 byte order; record references are sorted by `id` and then `digest`;
named contribution, export, and environment arrays are sorted by canonical
name; and nested reference arrays use the same reference ordering. Probe arrays
are sorted lexicographically by each complete probe object's
`canonical-json-v1` bytes; duplicate byte-identical probes deduplicate before
sorting. Ordered values such as probe arguments retain declared order. The input
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
alongside their ordinary provider inputs. Adding an ARM64 target, a Node
binding, or a WebKit selection must not by itself invalidate an existing AMD64
Python/Chromium materialization. Reuse across definition revisions is allowed
only when the selected closure is byte-for-byte identical; the lock still
records the newly selected release provenance.

Changing only an artifact mirror or source-provenance reference changes release
provenance but preserves selected-closure identity. Changing the expected
artifact size, SHA-256 digest, extraction contract, or destination changes the
selected closure.

This replaces the current aggregate definition digest, where every known target
contributes to one digest and an unrelated target addition invalidates existing
build identity.

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
- Build-only recipe requirements use the same compact tool grammar and
  version/revision semantics as runtime requirements. For example,
  `tool:java==21` requests Java 21 while `tool:java==21~2` pins definition
  revision 2 as well. An omitted version retains the shared newest-eligible
  resolution rule; the resolved request and lock always contain the exact
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
  package, extraction rule, and launch test succeeds for the exact target.

## Validation and Test Contract

### Static Definition Validation

Before definitions can be embedded, validation must reject:

- invalid UTF-8, duplicate JSON member names, non-canonicalizable values, or a
  record graph that exceeds a core structural limit;
- unknown schemas, fields, record kinds, package managers, or primitives;
- a missing or unknown tool version scheme, or a release version or alias that
  is invalid under that scheme;
- exact-version or alias collisions anywhere in one tool index;
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
- invalid selection cardinality, defaults, compatibility groups, or a selection
  option not covered by a maximal group;
- a parameter domain that is not finite and enumerable or whose required
  integration-coverage product exceeds the core cap;
- malformed integration fixtures or validation profiles, validation references
  outside their release namespace, or fixtures whose target tuple disagrees
  with the referencing target;
- artifacts without exact size and SHA-256 metadata;
- an externally acquired artifact without exactly one source mapping, or a
  mapping whose key and expected artifact SHA-256 fields disagree, whose source
  or artifact reference has a canonical record-digest mismatch, or whose size
  authority or resolver primitive is inconsistent;
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
described above. Candidate-selection tests cover parameter defaults,
normalization, bounds, target compatibility, exact revision pins,
version-scheme ordering, opaque defaults, canonical version-segment encoding,
alias uniqueness, selection compatibility, and filtering a newer Reploy- or
provider-incompatible release in favor of the highest compatible release.
Binding and selection tests cover complete target mappings, coupled
contributions, and exclusion of unselected artifacts, payloads, and native
roots. Validation-record tests cover strict fixture/profile parsing, namespace
and target agreement, missing or wrong-kind references, and complete bounded
parameter-domain coverage.
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
and atomic cleanup after failure. Parser tests cover duplicate member names,
canonical-equivalent encodings, and every structural limit. Static mapping tests
cover missing, duplicate, orphaned, and identity-inconsistent artifact-source
mappings. Probe-runner tests prove that declared probes cannot enable networking
or inherit a network-enabled execution policy.

### Reploy Integration Validation

Every advertised target tuple requires an integration fixture that uses Reploy
itself against a representative base image. The fixture must exercise the same
definition resolution, provider merge, acquisition, offline materialization,
and final-image behavior used by a real blueprint.

The integration plan is derived from release manifests and target leaves, not
maintained as an independent handwritten target list. CI fails when any target,
binding, valid normalized selection set, or normalized parameter value lacks a
runnable case. Valid selection sets are enumerated from `minimum`, `maximum`,
and `compatibility_groups`; contribution collisions cannot stand in for this
declared compatibility contract.

Schema v1 parameter domains must be finite and enumerable under a core
publication cap. Validation exercises every boolean or enumerated value and
every value in an integer range, across every supported target, binding, and
valid selection-set combination. Publication rejects a definition whose
required Cartesian coverage exceeds the cap; a future explicit equivalence-
class contract may relax this without weakening existing support evidence.
Hand-authored evidence metadata cannot satisfy this coverage gate.

Java validation checks the executable and confirms the requested Java version.
Playwright validation imports the selected binding, launches each selected
browser, loads a local page, and exits cleanly with networking disabled during
materialization and probe execution. Negative fixtures verify that unsupported
OS versions, architectures, bindings, selections, and parameter values fail
before downloads begin.

Architecture support requires execution on that architecture or an explicitly
approved equivalent CI environment. Schema coverage or successful AMD64 tests
cannot establish ARM64 support.

Successful cases produce evidence bound to the release provenance and selected
closure, exact observed target tuple, base-image platform and immutable digest,
binding, selections, normalized parameters, and validator version. The
documented support matrix is generated only by joining manifest entries with
current successful external
evidence. Validation results are attestations over immutable definition
digests; they are not embedded back into the records they validate. A target
file's presence, an AMD64 result for an ARM64 leaf, or a manually asserted
result is not sufficient.

## Migration from the Flat WIP Definitions

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
6. Extend local-source recipe parsing to the shared compact requirement grammar
   and carry exact Java upstream version, definition revision, manifest digest,
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

Across every manifest indexed by one tool record, exact versions and aliases
form one collision-free lookup map. Each normalized input token resolves to
exactly one canonical tool-version coordinate. An alias cannot equal any exact
version or another alias, including a redundant alias for its own release.
Publication validates this invariant over the complete tool index before any
release is advertised.

The release contract represents the singular binding request as sorted
`options`, a `required` boolean, and an optional `default`. It represents the
selection set as sorted `options`, canonical-decimal `minimum` and `maximum`,
sorted `defaults`, and canonical `compatibility_groups`. Each compatibility
group is a sorted maximal set of options that may coexist; the group list is
sorted, has no duplicates or subset groups, and covers every option. A request
is valid only when its normalized set satisfies the cardinality bounds and is a
subset of at least one group. Defaults must satisfy the same rule. Typed
parameter schemas are sorted by canonical parameter name and
carry the public type, required/default behavior, and type-specific enum or
range constraints. Resolution normalizes defaults and explicit values to their
canonical typed representation before candidate filtering and identity. A
required binding with a default is present in the resolved request but may be
omitted by the user; resolution inserts the default before identity is
computed. Playwright initially permits only the `python` binding, marks it
required, and sets it as the default, so an omitted binding is inferred while
an explicit `python` remains valid. It permits only the `chromium` selection,
sets both selection cardinality bounds to one, declares `chromium` as its sole
compatibility group, and supplies no selection default. Java permits neither a
binding nor a selection. The initial Java and Playwright contracts declare no
parameters. Binding requirements remain owned by
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
native dependency, materialization rule, and target probe in its selected
closure succeeds.

Validation evidence is external to definition identity. Schema v1 records the
tool, upstream version, definition revision, manifest digest, selected-closure
digest, exact target tuple, immutable base-image digest, binding and selections,
fixture ID, validator version, result, and observed probe digests. Only a
passing record whose immutable fields match the selected manifest and closure
can contribute to the generated support matrix.

These decisions settle the initial wire representation and support claims.
Additional Java versions, runtime Java, Playwright bindings or browsers, ARM64,
and repository publication remain explicit later extensions; none is inferred
from the initial embedded records.
