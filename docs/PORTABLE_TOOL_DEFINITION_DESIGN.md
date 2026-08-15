---
status: Accepted
updated: 2026-08-16
summary: Concrete composition, targeting, acquisition, identity, and validation model for embedded portable-tool definitions.
refines: docs/REPOSITORY_DESIGN.md
---

# Portable Tool Definition Design

## Status and Authority

This document defines the concrete portable-tool definition model used by the
embedded catalog. It refines the portable-tool contract in
`REPOSITORY_DESIGN.md` and the built-in tool behavior in
`BLUEPRINT_ENVIRONMENT_MODEL.md`.

The immediate implementation scope is `tool:java` and `tool:playwright`.
Repository publication, TUF metadata, publisher authorization, and lifecycle
policy remain owned by `REPOSITORY_DESIGN.md`. The embedded catalog is an
implementation bridge, but its definition boundaries are intended to carry
forward into published tool definitions.

The existing flat, complete JSON files are a work-in-progress checkpoint, not
the final schema described here. Reploy has not been released, so the migration
does not need a compatibility reader for that format.

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

The release contract, exact target, selected binding, selected payloads, native
package sets, exports, and probes that affect one resolved tool request. Records
for unrelated targets, bindings, or selections are outside that closure.

## Support Unit

The exact support claim is the following tuple:

```text
(tool, upstream version, definition revision, context,
 target OS, target OS version, OCI architecture,
 package manager, native architecture, binding, selections)
```

The binding or selections may be absent only when the release contract permits
that. A definition may advertise a tuple only when every referenced artifact,
binding, native package set, export, and probe is available and validated for
it.

There is intentionally no tool-wide `supported_architectures` promise. The
supported architectures shown to users are derived from valid target leaves.
This prevents an AMD64-only wheel, browser build, or native package from being
mistaken for ARM64 support.

## Definition Records

### Tool Record

The tool record contains only stable catalog metadata:

- schema and qualified tool name;
- summary, upstream project, source, and license references;
- documentation metadata;
- references to the available release manifests.

### Release Manifest

The release manifest owns the published or embedded release coordinate and its
complete availability index:

- tool identity, exact upstream version, and Reploy definition revision;
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
- binding and selection schemas, including required/default behavior;
- public executable and capability exports;
- environment variables and final-image placement rules;
- reviewed resolver primitive names;
- target-independent probes and compatibility constraints.

The contract does not enumerate targets and does not contain the Reploy
definition revision. The release manifest references the contract and target
leaves independently. Target leaves then reference the binding, payload, and
native-package records that form one supported closure.

### Target Leaf

A target leaf owns only data whose truth is specific to one OS generation and
architecture:

- exact target identity and base-profile match fields;
- the native package manager and native architecture;
- native package-set references;
- the bindings and selections available on that target;
- architecture-specific binding and payload references;
- target-specific exports or probes when the shared contract is insufficient;
- the integration-fixture and validation-profile references required to prove
  the support claim.

Two target leaves may reference the same immutable native package set or
payload record. They do not inherit from each other. If Ubuntu 25.10 and 26.04
currently use the same package roots, each remains an independently validated
target and explicitly names the shared set. Either can later switch to a new
set without affecting the other.

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
validation requires the mapping key, source-record digest, and referenced
artifact-record digest to agree and requires that artifact record's size and
resolver primitive to govern every mapped locator.

The lock records the authorizing source record and acquisition outcome. A
network acquisition records the successful declared source locator; redirect
hops are sanitized transport diagnostics rather than provenance locators. A
verified-cache hit records that no locator was contacted during the operation;
any retained original locator is labeled as historical object provenance rather
than the locator used by the current operation.

Selections map to one or more payload records. Playwright's `chromium`
selection, for example, may contribute full Chromium, Chromium Headless Shell,
and FFmpeg as one coupled set.

### Native Package-Set Record

A native package set contains:

- package-manager kind;
- manager-specific root requirements;
- optional repository requirements already supported by that provider;
- manager-specific validation metadata.

Package sets are reusable only through explicit references. They do not contain
OS matching expressions and cannot select themselves.

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
```

The tree is organizational, not an inheritance mechanism. Every semantic edge
is an explicit record reference. Record IDs and canonical contents determine
identity; relative filesystem ancestry does not.

Architecture remains visible in target and artifact filenames because those
records make architecture-specific claims. It disappears from files whose
contents are architecture-independent. This is the intended balance between a
single oversized definition and complete per-target duplication.

## Composition Rules

1. A release manifest explicitly references one release contract, enumerates
   its target records, and maps artifact content identities to source records.
   The contract, targets, and payloads do not point back to the manifest or
   select retrieval sources.
2. A target explicitly references every binding artifact, payload, and native
   package set that can participate in that target.
3. Every reference includes or resolves to an expected content digest.
4. References cannot escape their embedded or published release namespace.
5. Cycles, duplicate record IDs, and incompatible exact package requirements
   are errors.
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

Reploy resolves a tool in this order:

1. Normalize the requirement and resolve one upstream version and definition
   revision.
2. Observe the base image's OCI platform, `/etc/os-release`, package manager,
   and manager-native architecture.
3. Select exactly one matching target leaf. Zero or multiple matches are
   errors.
4. Validate the requested or inferred binding and selections against that
   target's advertised availability.
5. Traverse the explicit references to form the selected closure.
6. Construct its canonical contribution union, rejecting incompatible
   requirements, conflicting semantic keys, and overlapping owned paths before
   acquisition.
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

- **Release provenance identity** records the tool name, upstream version,
  definition revision, and release-manifest digest. Adding a target produces a
  new immutable revision and therefore new provenance.
- **Selected-closure identity** hashes the tool name and exact upstream version,
  the release contract, and only the exact target, binding, payload,
  package-set, export, probe, and selection records used by the request. It
  excludes the release manifest, definition revision, artifact source records,
  and retrieval URLs.

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
  version is `21`; definition revision 1 pins upstream `21.0.12+8`. The JDK is
  build-only and exports both `java` and `javac`; runtime Java remains a later
  support decision.
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
- duplicate IDs, missing references, cycles, or digest mismatches;
- references outside the selected release namespace;
- target tuples that are ambiguous or internally inconsistent;
- advertised bindings or selections with incomplete referenced closures;
- artifacts without exact size and SHA-256 metadata;
- an externally acquired artifact without exactly one source mapping, or a
  mapping whose key, source digest, referenced artifact digest, size authority,
  or resolver primitive is inconsistent;
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
an unrelated target does not change an existing selected-closure digest. They
also cover identical contribution deduplication and every conflicting semantic
key or overlapping destination described above. Identity tests prove that a
source-locator-only change preserves selected-closure identity while an
artifact size, digest, or materialization change does not. Resolver-primitive
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
binding, or individual selection lacks a runnable case. When a release permits
multiple simultaneous selections, CI also exercises every declared
compatibility group and at least one union containing all simultaneously
supported selections. Hand-authored evidence metadata cannot satisfy this
coverage gate.

Java validation checks the executable and confirms the requested Java version.
Playwright validation imports the selected binding, launches each selected
browser, loads a local page, and exits cleanly with networking disabled during
materialization and probe execution. Negative fixtures verify that unsupported
OS versions, architectures, bindings, and selections fail before downloads
begin.

Architecture support requires execution on that architecture or an explicitly
approved equivalent CI environment. Schema coverage or successful AMD64 tests
cannot establish ARM64 support.

Successful cases produce evidence bound to the release provenance and selected
closure, exact observed target tuple, base-image platform and immutable digest,
binding, selections, and validator version. The documented support matrix is
generated only by joining manifest entries with current successful external
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
`tool:<name>/releases/<upstream-version>`; its manifest is
`tool:<name>/releases/<upstream-version>/revisions/<definition-revision>/manifest`.
Contracts, targets, bindings, payloads, and package sets live beneath the
release namespace without a revision segment so unchanged records can retain
identity across manifest revisions. Manifest-owned source records live beneath
the revision namespace. IDs use ASCII letters, digits, `.`, `+`, `-`, `:`, and
`/`, with no empty, `.` or `..` path segments.

Definition JSON uses the record schemas named in this document with a
`portable-tool-` prefix and `-v1` suffix. Integer quantities are canonical
decimal strings. Parsing is strict: duplicate members, unknown fields, invalid
UTF-8, noncanonical IDs or decimal strings, and values outside core structural
limits are errors before references are resolved.

The release contract represents the singular binding request as sorted
`options`, a `required` boolean, and an optional `default`. It represents the
selection set as sorted `options`, a canonical-decimal `minimum`, and sorted
`defaults`. A required binding with a default is present in the resolved request
but may be omitted by the user; resolution inserts the default before identity
is computed. Playwright initially permits only the `python` binding, marks it
required, and sets it as the default, so an omitted binding is inferred while
an explicit `python` remains valid. It permits only the `chromium` selection,
requires at least one selection, and supplies no selection default. Java
permits neither a binding nor a selection. Binding requirements remain owned by
binding-contract records, while the runtime subrecord owns only final-image
placement and environment values.

The initial Java implementation is Eclipse Temurin JDK 21 for build use. The
public request `tool:java==21` resolves definition revision 1 to upstream
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
