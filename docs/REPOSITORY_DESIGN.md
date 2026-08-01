---
status: Draft
updated: 2026-08-01
summary: Federated, TUF-authenticated Reploy repositories for published blueprints and portable tool definitions.
---

# Reploy Repository Design

## Status

- Decision state: High-level policy decided; wire schemas require focused review
- Implementation state: Not started
- Initial tool examples: `tool:java` and `tool:playwright`

This document defines one repository protocol for versioned Reploy assets. The
initial asset surfaces are published blueprints and portable tool definitions.
It also defines how direct Blueprint URLs coexist with repositories, how
repository and publisher trust compose, and which data becomes part of a
deployment rather than remaining dependent on a global cache.

## Decision Summary

- A Reploy repository is one static, mirrorable, TUF-authenticated publication
  that may contain both blueprints and tool definitions.
- Reploy operates an official repository, but the protocol is open and other
  organizations may operate independently trusted repositories.
- Every repository has one TUF trust domain. Clients grant it authority for
  explicit asset surfaces such as `blueprints` or `tools`.
- The repository owns an authenticated publisher authorization list. Clients
  do not maintain per-publisher, per-namespace, or per-asset grants inside an
  already authorized surface.
- Every published blueprint and tool definition carries a detached publisher
  DID attestation over its exact immutable contents. TUF authenticates
  repository inclusion; the DID signature authenticates authorship.
- Repository trust is APT/PPA-like: repositories are configured independently,
  may publish overlapping qualified names, have mutable priority, and may be
  pinned explicitly. TUF supplies the authentication and update security.
- Published assets are versioned and immutable. Blueprints and tools use one
  shared version-scheme, release-revision, selection, locking, and lifecycle
  model.
- Current publisher authorization controls new publication only. Historical
  releases remain attributable through their publisher attestations and
  repository acceptance records.
- Blueprints and tools share yank, archive, delete, publisher-security
  revocation, rescission, purge, and ownership-transfer lifecycle rules.
- Direct `file:`, `pypi:`, `github:`, and other supported BURLs remain a
  first-class low-friction path. Remote direct sources warn about missing
  repository evidence; the warning does not tell consumers to publish.
- Repository index refresh is explicit through `reploy repository update`.
  Update downloads authenticated metadata and indexes, not asset definitions or
  payloads.
- Once selected, the exact blueprint or tool definition, publisher attestation,
  and repository acceptance record are retained in the deployment-owned
  provider-store closure. The global immutable object cache is acceleration
  only and may be evicted safely.
- The ordinary `reploy` executable is consumption-only. Publisher signing and
  repository administration are separate privilege domains implemented by two
  separately distributed tools.
- Tool definitions are declarative data interpreted through reviewed
  Reploy-owned primitives. They cannot contain arbitrary commands, scripts,
  package-manager expressions, or generic download instructions.

## Terminology

- **Git repository**: an authoring source controlled with Git, whether hosted
  by GitHub or another service.
- **Reploy repository**: one logical TUF-authenticated publication of Reploy
  assets. Avoid bare `repo` where it could mean a Git repository.
- **Repository source**: a URL-shaped transport locator for a Reploy
  repository. It is not the repository's permanent identity.
- **Repository index**: the authenticated, machine-readable inventory of
  published asset identities, versions, lifecycle state, compatibility, and
  immutable target digests. There is no separate catalog concept.
- **Asset surface**: a client-authorized repository capability. The initial
  surfaces are `blueprints` and `tools`.
- **Published asset release**: one immutable version and Reploy revision of a
  blueprint or tool definition, together with its detached publisher
  attestation and repository acceptance record.
- **Publisher DID**: a DID Core identifier whose keys authenticate an asset's
  detached publisher signature.
- **Mirror**: another transport endpoint serving the same authenticated Reploy
  repository.
- **BURL**: a direct Blueprint URL such as `file:`, `pypi:`, or `github:`. A
  BURL locates a blueprint directly rather than selecting it from a trusted
  repository index.

## Context

Reploy currently has two distribution pressures:

1. Application users need a short, durable way to discover, install, and update
   published blueprints.
2. Portable tools such as Java and Playwright need independently updated,
   reviewable build and runtime definitions that should not be hard-coded into
   each Reploy release.

The former shorthand index was only a pointer list. A real repository asserts
more: it retains immutable blueprint releases, validates them before
publication, authenticates the publisher, and supports lifecycle operations.
The tool design independently required authenticated indexes, immutable
definitions, repository trust, and offline transfer. Maintaining two protocols
would duplicate trust and distribution machinery while making repository
terminology harder to understand.

One multi-asset repository provides the shared trust, identity, versioning,
publication, search, and update layer. Blueprint semantics and tool semantics
remain distinct asset contracts within it.

## Goals

1. Make published blueprints and portable tools discoverable, authenticated,
   versioned, updateable, and lockable through one protocol.
2. Preserve explicit repository and publisher provenance without putting
   Reploy in the business of centrally verifying every real-world identity.
3. Support independently operated repositories, static hosting, mirrors, and
   deliberate offline transfer.
4. Preserve a direct BURL path for development, personal deployment, and
   deliberate user installation.
5. Keep deployment replay independent of global repository cache retention.
6. Update downstream tool knowledge independently from Reploy binaries.
7. Keep tool definitions declarative and materially smaller than a generic
   package manager or arbitrary build system.
8. Generate useful user and publisher documentation from validated repository
   records.

## Non-goals

The initial protocol does not:

- centrally author, repair, fork, or supplement third-party application
  blueprints or per-project build recipes;
- publish application binaries or locally built package artifacts;
- make repository publication mandatory for staging or installation;
- infer trust from a blueprint, tool definition, namespace, source priority, or
  untrusted search result;
- implement a web of trust or reputation score across publisher DIDs;
- define a plugin or executable-extension system;
- accept arbitrary commands, scripts, hooks, package-manager expressions, or
  generic download schemas from repository assets;
- guarantee support for packages that perform undeclared dynamic downloads;
- implement a general build-network escape hatch.

Repository maintainers decide what they publish and may maintain third-party
blueprints, but Reploy itself does not become a Conda-Forge-like central owner
of downstream project recipes. This preserves
[`ADR 0001`](adr/0001-local-source-build-recipes.md).

## Asset Model

### Published Blueprints

A published blueprint release contains the exact blueprint document, detached
publisher attestation, and repository acceptance record. Its qualified name,
upstream-facing `blueprint.version`, packaging revision, content digest,
publisher DID, repository identity, and repository snapshot identity are
retained through resolution and installation.

Repository publication performs strict static blueprint parsing, schema and
semantic validation, reference validation, and policy checks without executing
untrusted project code. A repository may additionally require CI image builds
or other tests as publication policy, including stronger checks for less
trusted publishers, but those dynamic checks are not a universal protocol
requirement.

Repository publication retains each accepted immutable blueprint release until
an explicit lifecycle operation changes its availability. The repository does
not merely point at a moving external blueprint.

### Portable Tool Definitions

A portable tool definition is a strict, versioned data record that maps one
portable capability onto supported target systems and Reploy-owned
implementation primitives. Concrete tool definitions are repository data, not
compiled into the Reploy executable.

The Reploy client contains only supported definition schemas, trusted resolver
and materialization primitives, validation, provider composition, caching, and
locking behavior. An official definition may be updated independently of the
client as long as the consuming Reploy version understands its schema and named
primitives.

The publisher of a tool definition is its Reploy definition maintainer, not
necessarily the upstream software vendor. Upstream vendor, source, licensing,
and artifact provenance are recorded separately.

### Direct Blueprint Sources

Direct BURLs remain first-class. A user may stage or install a supported
`file:`, `pypi:`, `github:`, or other BURL without discovering, configuring, or
trusting a Reploy repository first.

An explicit local `file:` BURL shows resolved provenance but does not emit a
trust warning: the user deliberately selected local content. A direct remote
BURL outside a trusted repository reports the missing trust evidence
factually. It does not suggest that the consumer publish the blueprint or add a
repository. Repository publication is promoted through publisher-facing
documentation and publisher tooling instead.

A direct remote source may contain an adjacent detached `attestation.json`.
When present, Reploy verifies the attestation against the exact blueprint,
resolves the current publisher DID document, and requires the attestation key
to remain authorized by that DID. A malformed, mismatched, invalid, or stale
rotated-out attestation is an error, not an unsigned fallback. A valid signature
establishes current publisher authorship but not repository acceptance,
historical key-binding evidence, or publication-validation evidence. Without
an attestation, Reploy warns that neither publisher identity nor trusted
repository publication was established.

Automatic client-side resolution for such an untrusted attestation initially
supports only `did:web`. Reploy derives the method-defined HTTPS URL, requires a
hostname rather than an IP literal, and uses the system TLS trust store. Before
connecting it resolves the hostname and rejects the complete result if any
address is loopback, private, link-local, reserved, infrastructure metadata, or
otherwise not globally routable. The HTTPS connection is pinned to the
validated address while retaining the original hostname for certificate
validation, preventing a second DNS lookup from changing the destination.

Resolution rejects every redirect and applies fixed response-size and elapsed-
time limits. The returned DID document must have an `id` exactly equal to the
requested DID. Reploy reads only the verification material needed for the
attestation and never dereferences `@context`, `service`, `alsoKnownAs`, or any
other URL found in the document. Unsupported DID methods fail with an explicit
diagnostic rather than invoking a generic resolver. A publisher without its own
domain may use a `did:web` document hosted beneath a service such as GitHub
Pages.

This network resolution is specific to direct remote BURLs. For a selected
repository asset, the client validates the TUF-authenticated publisher
attestation and repository acceptance record retained with that asset; it does
not contact the publisher's current DID endpoint during ordinary resolution,
installation, build, or lock replay.

Reploy locks the exact fetched content digest. That digest proves content
identity after retrieval; it does not establish trust in the retrieval source.

## Repository Topology

A Reploy repository is a static, mirrorable publication containing:

1. standard TUF 1.x metadata;
2. one required authenticated `repository.json` identity target;
3. one required authenticated `publishers.json` authorization target;
4. one versioned repository index snapshot covering authorized asset surfaces;
5. immutable blueprint and tool-definition targets with detached publisher
   attestations and repository acceptance records;
6. immutable lifecycle-event targets;
7. additional declarative objects referenced by supported tool definitions.

Logical target paths remain friendly and versioned. These filenames are
illustrative; the exact asset-document filenames remain a schema detail:

```text
blueprints/acme/editor/1.2.0/2/blueprint.yaml
blueprints/acme/editor/1.2.0/2/publisher-attestation.json
blueprints/acme/editor/1.2.0/2/repository-acceptance.json
tools/acme/playwright/1.55.0/3/tool.yaml
tools/acme/playwright/1.55.0/3/publisher-attestation.json
tools/acme/playwright/1.55.0/3/repository-acceptance.json
```

Content hashes live in publisher attestations, TUF target metadata, locks, and
internal content-addressed storage. When TUF consistent snapshots require
hash-prefixed physical filenames, publication tooling generates them without
changing the friendly logical names in the repository index.

Supported repository sources include ordinary `https:`, configured `file:`, a
provider-specific `github:` form, mirrors, and `http:` only under the restricted
bootstrap rule below. GitHub Pages, GitHub Releases, object storage, internal
web servers, and removable media are transports rather than repository
identities.

A Reploy repository is rooted by its repository descriptor rather than by a
version-control boundary. Its authoring source and generated publication may
live at a subdirectory of a Git repository, and one Git repository may contain
multiple sibling Reploy repositories. Each Reploy repository still has its own
descriptor, permanent repository ID, TUF root, canonical origin, and trust
domain. An HTTPS canonical origin includes the complete normalized base path,
not merely the scheme and host.

Publisher and repository-maintainer tooling accepts an explicit repository
directory and does not require that directory to contain `.git`. Compilation
confines all repository input and generated output to that declared directory;
symlinks, archive entries, or references that escape it are rejected.

## Repository Authentication and Trust

### TUF Authentication

Repository authentication follows
[The Update Framework 1.x specification](https://theupdateframework.io/spec/).
Reploy does not define a parallel repository-signing protocol. Every repository
publishes the required top-level roles:

- `root` establishes repository keys, thresholds, and root rotation;
- `targets` authenticates the descriptor, publisher authorization, index,
  immutable asset and acceptance targets, and lifecycle-event targets by path,
  length, and digest;
- `snapshot` binds one consistent metadata view;
- `timestamp` establishes freshness.

Consistent snapshots are required. Mirrors are untrusted transports;
acceptance depends on TUF validation rather than mirror identity or TLS alone.
TUF establishes repository authorization, integrity, consistency, and
freshness. Reploy still strictly validates every authenticated Reploy schema,
publisher relationship, compatibility constraint, and named primitive.

`repository.json` contains a strict schema version, opaque permanent
`repository_id`, canonical origin, display name, and optional presentation
fields. The ID becomes immutable when first trusted. A later authenticated
snapshot that changes it is rejected without replacing the last accepted
local snapshot. Presentation fields do not participate in identity or locks.

### Surface Authorization

Trusting a repository grants one or more explicit client-side asset surfaces.
Trust for `blueprints` does not authorize tools, and trust for `tools` does not
authorize blueprints. Repositories cannot widen those grants through their own
metadata.

```bash
reploy repository trust URL --surface blueprints
reploy repository trust URL --surface tools
reploy repository trust URL --all-surfaces
```

Within an authorized surface, the repository owner decides which publishers
and namespaces it accepts. Clients do not maintain another per-publisher,
per-namespace, or per-asset allowlist. Priority and source pinning cannot bypass
surface authorization.

The official repository may be configured with bundled Reploy trust policy.
Every external repository has its own root and must be trusted explicitly for
each desired surface.

### Publisher Authorization and DID Attestations

Every repository publishes an authenticated `publishers.json`. Each entry
binds one publisher DID to the surfaces and namespaces in which that publisher
may publish. Publication rejects a new release whose publisher, surface,
namespace, or signature does not match the current authorization. Client
validation of an already accepted release does not reapply the current
authorization list; otherwise ordinary removal or key rotation would
retroactively invalidate historical releases.

Every published blueprint and tool definition has a detached publisher
attestation covering at least:

- asset surface and qualified name;
- version scheme, asset version, and Reploy revision;
- exact document digest;
- publisher DID;
- the public verification method and public key used for the signature;
- publisher signature.

Publication resolves the DID document and verifies that the signing key is
currently authorized for the publisher. It then creates an immutable repository
acceptance record covering at least the repository identity, qualified release,
asset and publisher-attestation digests, publisher DID and key fingerprint,
acceptance generation, static-validation policy, and the authorization facts
verified at acceptance time. TUF authenticates the asset, publisher attestation,
and repository acceptance record in one repository snapshot. The asset document
itself remains free of embedded signatures.

The public key retained in the publisher attestation verifies the historical
signature. The TUF-authenticated repository acceptance record establishes that
the repository verified that key as belonging to the publisher DID when it
accepted the release. A repository therefore needs only current authorization
and current publisher keys in `publishers.json`; it does not need an unbounded
central ledger of every former publisher key.

DID documents may expose keys and links to domains, GitHub accounts, or social
profiles so humans and repository maintainers can gather identity evidence.
The repository maintainer remains responsible for deciding which DID controls
a namespace. Reploy validates cryptographic statements but does not operate an
identity-verification service or infer transitive trust from how many other
parties recognize a DID. A web-of-trust or curated cross-repository reputation
service is a possible later extension.

Removing or narrowing publisher authorization prevents future publication in
that scope. It does not silently yank, delete, archive, revoke, or otherwise
change already published immutable releases.

### Initial Trust and Root Rotation

The normal bootstrap fetches the initial TUF root from the exact HTTPS
repository origin using the system trust store. Cross-origin redirects are not
accepted implicitly, and the authenticated repository descriptor must identify
the same canonical origin. HTTPS establishes current domain control; explicit
operator confirmation supplies the judgment that the origin is the intended
repository and authorizes the requested surfaces.

The preview shows the repository URL, authenticated ID, display name, requested
surfaces, TUF root fingerprint, and initial-trust provenance. Examples include:

```text
Initial trust: HTTPS domain control via system trust store
Initial trust: Local TUF root /path/to/root.json
Initial trust: Bundled Reploy policy
Initial trust: Administrator-managed system policy
```

Advanced bootstrap accepts standard JSON root metadata through `--root FILE`
or pins the fetched root with `--root-sha256 SHA256`. A local root contains
public keys, role assignments, thresholds, version, expiration, and signatures,
never private keys. Plain HTTP requires an independently obtained root or exact
root digest; HTTPS-to-HTTP redirects are rejected.

After bootstrap, only a valid sequential TUF root-rotation chain can change
repository signing authority. A later domain compromise alone cannot replace
the root with unrelated keys.

### User and System Trust

Repository trust is user-scoped by default and applies to that user's staging
and user-scoped deployments. `--system` creates administrator-managed
machine-wide trust for system installations. Repository listings show every
effective record and its scope because user and system records may grant
different surfaces or priorities.

A system installation may rely only on system-scoped trust. It never silently
promotes user trust, including under `install --system --yes`. Automation may
perform the same explicit validated action non-interactively:

```bash
sudo reploy repository trust https://repo.example \
  --surface tools --system --yes
```

`--yes` suppresses confirmation but does not bypass URL, TLS, TUF, root,
repository identity, publisher, surface, schema, or compatibility validation.

## Names, Versions, and Repository Selection

A qualified asset name contains a publisher namespace and asset name, such as
`acme/editor` or `acme/playwright`. In the compact selector
`tool:acme/playwright`, `tool:` identifies the asset surface and is not part of
the qualified name. The structured YAML form expresses the same distinction as
the `tool` field with value `acme/playwright`. Namespaces are repository-local:
the same qualified name may be authorized to different publisher DIDs in
different repositories. The globally unambiguous source identity is the
permanent repository ID, asset surface, and qualified name together. The
authenticated publisher DID is retained as provenance for that source identity.

### Shared Asset Versioning

Blueprints and tools use the same `VersionScheme`, version-requirement parser,
release-revision model, ordering, lifecycle targeting, lock representation, and
diagnostics. The implementation is shared rather than two similar surface-
specific implementations. User-facing labels may call the second coordinate a
blueprint packaging revision or a tool definition revision, but both are the
same Reploy revision field.

Within one repository, every qualified asset name chooses one immutable
Reploy-supported version scheme:

- `semver`: exact and ordered Semantic Versioning constraints;
- `pep440`: exact and ordered Python packaging version constraints;
- `integer`: exact and ordered integer constraints, such as Java levels;
- `opaque`: arbitrary upstream strings with exact equality only.

Repositories cannot supply parsers or comparison code. Reploy does not add an
outer epoch or generation to version ordering. A scheme interprets its own
native epoch when it has one. Changing the scheme for an existing qualified
name is not an ordinary update; the initial protocol requires a new qualified
name, with any future cross-scheme migration requiring a focused design.

Each upstream-facing version contains monotonically increasing positive integer
revisions starting at `1`. A revision corrects blueprint packaging or tool
definition data for that exact upstream version without inventing a new
upstream version. Ordering compares the scheme-native version first and the
Reploy revision second. Ecosystem package versions contributed by an asset are
provider-native inputs and remain separate from both coordinates.

For ordered schemes, an omitted constraint selects the highest compatible,
non-yanked stable version and then its highest eligible revision. Prereleases
are excluded unless requested. An `opaque` identity has no ranges or implicit
ordering and must designate its compatible default explicitly. Existing locks
never follow a moving default.

An exact upstream version still permits any eligible revision. The compact
selector `2.4.0~1` pins both version `2.4.0` and revision `1`; `2.4.0` alone pins
the version while selecting its newest eligible revision. Revision constraints
are valid only with an exact upstream version. Because some version schemes may
use `~` themselves, structured `version` and `revision` fields and equivalent
CLI options provide the unambiguous form. Repository records and locks always
store the two coordinates separately.

New unconstrained resolution may select a newer upstream version and revision.
A range-constrained update may move within the range and then select the newest
eligible revision. An exact-version update may move only its revision. A request
that specifies both coordinates is fully pinned. Build, replay, and ordinary
repository-index refresh never move either coordinate; only an explicit
deployment update performs selection again. Every lock records the exact
version, revision, asset digest, attestations, and repository identity.

Repository-backed blueprint discovery enables the short consumption path:

```bash
reploy install omegaconf-inspector
```

The accepted local indexes determine the available blueprint candidates; the
command does not search or trust arbitrary remote repositories implicitly.

An unqualified name resolves only when exactly one authorized qualified
identity matches. Otherwise Reploy lists the qualified candidates. Multiple
trusted repositories may publish the same qualified identity, just as multiple
APT sources may publish the same package.

Repository priority is mutable user or administrator policy stored against the
permanent repository ID. For future resolution Reploy filters by surface,
authenticated repository acceptance, effective lifecycle state, compatibility,
and requested version and revision; applies any explicit source pin; otherwise
considers only the highest repository priority. An equal-priority tie between
repositories is a hard error. Reploy never breaks it using source order,
release revision, or content equality.

### Cross-Repository Conflict Resolution

Repository selection resolves every overlapping-source conflict explicitly:

- An explicit repository pin selects the source. Otherwise the highest-priority
  eligible repository wins, and an equal-priority tie fails.
- Before interpreting an unpinned version constraint, Reploy identifies the
  version schemes declared by all otherwise eligible repositories. If they
  differ, resolution fails and requires an explicit repository pin; repository
  priority never silently changes the meaning of a version constraint.
- Within one repository, the version scheme of an asset identity is immutable.
  An explicit repository pin selects that repository's scheme before the
  constraint is parsed.
- Reusing one repository, qualified identity, version, and revision coordinate
  for different content is rejected.
- If one publisher DID signs different content for the same qualified
  coordinate across repositories, Reploy reports publisher equivocation as a
  security error; repository priority does not hide it.
- Different repositories may authorize the same namespace and qualified name
  to different publisher DIDs. Normal repository selection chooses the source;
  the repository's authenticated acceptance record identifies the publisher.
- Yank, archive, delete, and security-revocation state affects only the
  accepting repository's eligible lineage. Platform, Reploy-version,
  dependency, or parameter incompatibility removes a candidate; when none
  remain, resolution reports the incompatible requirements.

Locks retain the selected repository, qualified name, publisher DID, version
scheme, exact upstream version, Reploy revision, and content identity, so later
policy changes cannot reinterpret an accepted result.

A tool requirement may pin a repository explicitly:

```yaml
requires:
  - tool: acme/playwright
    repository: https://tools.acme.com/
```

The source must already be trusted for tools or enter the explicit trust
workflow. Pinning bypasses priority, not authentication, authorization,
compatibility, or version constraints. Existing locks retain their exact
repository and asset identity when priority later changes.

A blueprint declares every external repository needed for unpinned tool
resolution; the official repository remains implicit. A pinned tool source
also declares that dependency and need not be duplicated:

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.6"
  tool_repositories:
    - https://tools.acme.com/
```

The declaration does not grant trust or assign priority. Interactive staging
may present the normal trust preview; unattended operation fails with the exact
trust command required.

## Asset Lifecycle

Lifecycle semantics apply equally to blueprint releases and tool-definition
releases unless stated otherwise. An operation may target one exact
version/revision release, every revision of one upstream version, or the complete
qualified identity. Publisher security revocation is publisher-wide.

Every lifecycle transition is an immutable event retained as a TUF target. The
current repository index carries only effective state and the event identifiers
needed to explain it; ordinary clients do not download the complete history.
Repository-maintainer tooling consults the retained history to reject release
coordinate reuse, implicit ownership changes, and invalid transitions. Old
events remain available for audit after the TUF snapshots that first published
them expire.

This is a repository-maintained append-only invariant. TUF authenticates the
published event records but does not prevent a malicious repository owner that
still controls its signing threshold from rewriting its own history. A public
transparency-log system is a possible later extension, not an initial protocol
requirement.

### Yank

Yanking follows PyPI/PEP 592 semantics. Ordinary resolution and updates ignore
a yanked release. An existing lock may continue to use it, and an exact request
may select it when no non-yanked candidate satisfies that request. Reploy warns
whenever it selects a yanked release and includes the repository-provided
reason. Yanking one revision leaves other revisions of that upstream version
eligible; a version-level yank covers every revision. A repository may rescind
or reapply a yank. Yanking does not remove immutable content.

### Archive

Archiving follows PyPI project-archival semantics and applies to the complete
qualified blueprint or tool identity. It prohibits publishing new versions but
leaves every existing release available for resolution, installation, update,
and lock replay. Discovery marks archived assets and new selection warns.
Archival means unmaintained, not unsafe, and never bypasses compatibility
checks.

### Delete

Deletion removes its target from repository selection and retrieval. New
resolution, updates, and exact remote requests cannot select deleted content.
An existing deployment can replay only when its own provider-store closure
already contains every exact immutable object; deletion does not claim to erase
downloaded copies.

A deleted qualified-name/version/revision coordinate is permanently retired and
cannot be republished with different content. A correction after deleting one
release requires a new revision. A version-level delete retires the version and
requires a new upstream version. If every release is deleted, the same publisher
DID may restore the qualified identity using a new, previously unused version.

A different publisher may assume an existing qualified name only through an
explicit repository-admin ownership transfer. The old and new publisher
identities remain auditable in repository history. An ordinary edit to
publisher authorization never transfers ownership implicitly.

### Publisher Security Revocation and Rescission

When a publisher is determined to be malicious and the potential damage from
running any of its prior material outweighs availability, a repository
maintainer may perform an explicit publisher-wide security revocation, also
described operationally as the publisher “nuke.” An updated client refuses new
resolution and refuses replay of that publisher's releases from locks or local
caches. It still permits safe teardown operations such as stop, down, remove,
inspection, and evidence export.

The revocation applies only to releases accepted by the issuing repository and
only on asset surfaces for which the client trusts that repository. It does not
revoke the publisher's releases accepted by another repository or extend from
one authorized surface to another. A client may warn that another trusted
repository has revoked the same publisher DID, but does not enforce that event
outside the issuing repository's authority.

Revocation is not remote erasure. It cannot recall copies held by offline
clients, terminate already running workloads, or remove content from systems
that have not accepted the event. Repository-held release bytes remain retained
but unavailable so the action can be audited and, if the security determination
was wrong, reversed safely.

A strongly authorized rescission names the exact revocation event and remains a
new immutable event rather than deleting history. Rescission does not
automatically restore publication authority or availability. It may explicitly
restore exact byte-identical releases, while future publishing requires a
separate current publisher authorization. A distinct `purge` operation
physically destroys repository-held release bytes; purge is irreversible and
cannot be undone by rescission.

The initial protocol does not define a separate quarantine state. Suspected
content may be yanked while investigated; confirmed unsafe publisher material
uses publisher security revocation.

A future independently trusted security authority could publish
cross-repository publisher revocations, and an official Reploy client could
ship with that authority's trust root. Its signing thresholds, update channel,
scope, rescission rules, and failure behavior require a separate design; the
initial repository protocol grants no repository global revocation authority.

## Client Operations

The consumption CLI is grouped under `repository`:

```text
reploy repository list
reploy repository show REPOSITORY
reploy repository trust URL [TRUST OPTIONS]
reploy repository configure REPOSITORY [CONFIGURATION OPTIONS]
reploy repository update [REPOSITORY]
reploy repository distrust REPOSITORY
```

`REPOSITORY` accepts a configured source or permanent authenticated ID.
`configure` owns mutable client policy such as priority. Removing one surface
grant leaves other surface grants intact. `distrust` revokes the whole
repository, including use by existing locks, but does not silently delete
stored data.

```bash
reploy repository configure REPOSITORY --priority 700
```

There is no persistent “added but untrusted” repository state. One-off remote
discovery is explicit:

```bash
reploy search --url https://repo.example/ KEYWORD
```

It fetches and strictly parses the remote index for search only. Results are
visibly untrusted, remain segregated from trusted repository state, and cannot
participate in resolution, staging, installation, or update. Selecting a result
shows the exact trust operation required before repository use.

Because this is a host-side fetch of an untrusted URL, Reploy resolves the
requested hostname once, pins the connection to the validated address while
retaining the hostname for TLS certificate validation, rejects redirects, and
applies fixed response-size and elapsed-time limits. By default it rejects any
resolved address that is loopback, private, link-local, reserved,
infrastructure metadata, or otherwise not globally routable.

One-off discovery of an intentionally internal enterprise repository requires
`--allow-private-network`. That option permits private and IPv6 unique-local
addresses only for the exact requested hostname; it does not permit redirects,
IP-literal substitution, loopback, link-local, infrastructure-metadata,
multicast, reserved, or unspecified destinations, and it does not weaken HTTPS
validation. If that repository is subsequently trusted with the same explicit
authorization, Reploy stores the private-network permission with the canonical
repository identity so later authenticated updates do not require a repeated
override. The permission grants no general access to the private network.

`repository trust` authenticates, configures, grants the requested surfaces,
and atomically accepts the initial repository snapshot. It implies everything
needed to use the repository; there is no separate persistent `add` operation.

### Explicit Repository Update

The optional argument changes scope, not content:

- `reploy repository update` refreshes every trusted repository;
- `reploy repository update REPOSITORY` refreshes only that repository.

Each refresh atomically accepts one coherent view containing:

- the valid TUF root-rotation chain, timestamp, snapshot, and targets metadata;
- `repository.json` and `publishers.json`;
- the authenticated repository index and lifecycle state.

Update does not download blueprint documents, tool definitions, browser
archives, or other payloads. Selected immutable assets are fetched on demand.
Stage, install, build, and ordinary trusted search resolve against the accepted
local index without silently refreshing mutable repository metadata or indexes.
Stage, install, and build may contact the selected trusted repository only to
fetch an authenticated immutable asset that is absent from the local object
cache; doing so never accepts a newer index or metadata generation. Ordinary
trusted search does not fetch asset payloads. When no usable authenticated
index exists for a new resolution, Reploy fails with the exact
`repository update` operation required.

Repository-supplied updates cannot change client-approved surfaces, trust
scope, priority, or initial-trust provenance. Root changes require the existing
TUF rotation chain.

## Resolution, Deployment Retention, and Cache

New resolution uses an eligible accepted local index. It authenticates and
validates a selected asset, publisher attestation, and repository acceptance
record; resolves all ordinary provider inputs; and records exact identities in
the build lock. A lock includes at least repository and snapshot identities,
asset qualified name, version scheme, exact version and revision, document and
attestation digests, selected tool profile and binding where applicable, and
downstream provider inputs.

The exact selected blueprint or tool document, publisher attestation, and
repository acceptance record enter the deployment-owned provider-store closure.
Installation transfers that closure. Staged and installed deployment replay
therefore does not depend on a global repository cache, repository availability,
or discovering other staging and installation directories.

Trusted roots, accepted TUF metadata, repository policy, and the accepted index
are active client state, not disposable cache. Separately, Reploy may cache
fetched immutable asset objects by authenticated identity and digest to speed
later resolutions. That global object cache is non-authoritative and may be
evicted by size, age, or recency without affecting an existing deployment.

TUF expiration prevents accepting expired metadata for new resolution. It does
not revoke a deployment-owned immutable definition already authenticated and
locked. Such replay remains subject to ordinary provider, build, and
compatibility verification and never follows newer repository state. An
accepted publisher-security revocation is deliberately stronger: it blocks
replay of affected local releases even when their immutable closure remains
present. A client that has not received the revocation, including an offline
client, cannot enforce it.

The exact global object-cache size, age, inspection, and cleanup interface is
an implementation decision rather than a correctness dependency.

## Offline Repository Transfer

A portable repository bundle supports disconnected or manually controlled
updates. It contains the root-update chain and unexpired TUF metadata needed for
one snapshot, the complete index snapshot, selected immutable asset objects,
and repository identity and generation information.

Import verifies the root chain, roles, thresholds, versions, expiry, safe
archive paths, bounds, schemas, repository acceptance records, publisher
signatures, effective lifecycle state, and every content digest before
atomically activating the imported snapshot. It never merges loose files into
an authenticated index or widens client-approved surface authority. Existing
TUF rollback and mix-and-match protections apply.

## Publisher and Repository-Maintainer Tooling

The ordinary `reploy` executable remains consumption-only. It may search,
trust, configure, update, distrust, verify, resolve, cache, and import
repositories. It cannot sign publisher submissions, authorize publishers,
change asset lifecycle, manage repository signing keys, or publish TUF state.

Two separately distributed executables use shared protocol, canonicalization,
schema, and validation libraries:

1. A publisher tool prepares an immutable asset submission, validates it,
   constructs the detached attestation, and signs it with the publisher DID.
2. A repository-maintainer tool manages publisher authorization, ownership
   transfer, lifecycle, policy, deterministic index compilation, TUF signing,
   and publication.

The split prevents publishers from needing repository-administration code or
credentials and keeps publication dependencies out of the already substantial
client binary. Exact executable names remain open. The repository formats are
public protocols; compatible third-party tooling may produce valid output.

## Authoring and Publication

A repository may use one Git repository with two persistent histories:

- The primary branch contains human-authored repository policy, publisher
  authorization source, blueprint and tool submissions, tests, documentation
  inputs, and compiler/workflow configuration.
- A separate automation-owned `publish` branch contains only generated static
  repository output: indexes, immutable targets, attestations, generated
  documentation as applicable, and TUF metadata.

Ordinary pull requests modify only human-owned source and tests. CI validates
that source and deterministically compiles a complete preview. Publication
updates the `publish` branch only after validation and configured signature
thresholds succeed. Any transient signing-event branches are automation-owned,
deleted after completion or abandonment, and never merged into the primary
branch.

The publish branch is a deployment artifact rather than a trust source. Clients
authenticate it with TUF exactly as they would object storage, a CDN, an
internal server, or a filesystem mirror.

Repository publication always performs strict static validation without
running untrusted blueprint code. Optional dynamic CI builds are repository
policy and may vary by publisher confidence. Publication also verifies asset
identity, version and revision immutability, the publisher DID signature and
current authorization for new releases, repository acceptance records,
lifecycle-event history and transitions, ownership, target compatibility,
deterministic index generation, and generated documentation completeness.

## Documentation Surface

Validated records generate repository search data and human-facing pages.
Blueprint pages show publisher identity, versions, lifecycle, supported Reploy
versions and targets, required external repositories, validation evidence, and
source provenance. Tool pages additionally show:

- supported downstream versions, operating systems, releases, architectures,
  bindings, and selectable parameters;
- exact root OS packages contributed per target profile;
- executable and capability interfaces;
- build-only or runtime placement;
- acquisition network behavior and artifact provenance;
- validation performed, approximate installed size where known, licensing,
  and known limitations.

Publication rejects an asset whose required generated documentation cannot be
produced. Repository promotion belongs in publisher-facing documentation and
the publisher tool, not in consumer warnings for direct BURLs.

## Portable Tool Contract

### Definition Shape and Safety Boundary

A tool definition represents:

- stable qualified tool identity;
- one exact upstream-facing version and Reploy revision;
- supported Reploy versions, targets, and use contexts;
- human summary, upstream and licensing provenance;
- exact OS-provider root packages per target;
- named executable and capability exports with target-specific validation;
- strict parameter schemas and supported values;
- zero or more application-facing bindings;
- named Reploy-owned resolver primitives for curated non-package artifacts;
- network behavior, final-image placement, and documentation metadata.

Unknown fields, schemas, primitives, targets, parameter values, or executable
contracts fail closed. Definitions cannot provide executable implementation
code. A future primitive requires a reviewed Reploy implementation and release;
ordinary definition updates may only select primitives the client already
supports.

A runtime tool belongs to the application that declares it. Public executables
become application-scoped executable outputs and are validated in the
materialized application environment. Colliding exports are errors unless a
future explicit alias design resolves them. A build-only tool declared by a
local project recipe exposes executables only within that isolated source
builder.

### Tool Requirements and Definition Revisions

Tool requirements accept a compact string or a structured mapping. Both
normalize immediately to the same internal surface, qualified name, symbolic
selections, version requirement, revision, repository pin, binding, and typed
parameter fields:

```yaml
tools:
  - "tool:acme/playwright[chromium,webkit]>=1.55.0,<1.56.0"
```

The equivalent structured requirement is:

```yaml
tools:
  - tool: acme/playwright
    select: [chromium, webkit]
    version: ">=1.55.0,<1.56.0"
```

The compact grammar is:

```text
tool:<qualified-name>[<selection>,...]<version-requirement>
```

The bracketed portion is optional and contains definition-declared symbolic
selections. It is normalized as an order-independent set. The version suffix
is also optional and uses the selected asset's version-scheme grammar. Exact
upstream version `==21` still selects the newest eligible Reploy revision;
`==21~2` pins both coordinates. Syntax that is ambiguous under the selected
version scheme, plus repository pins, bindings, and complex keyed or typed
parameters, requires the structured mapping. Unknown selections or missing
required selections fail with the target-supported values.

The public structured `version` field is an upstream-facing constraint, not a
definition revision:

```yaml
tools:
  - tool: acme/playwright
    version: ">=1.55.0,<1.56.0"
```

Tool requirements use the shared asset versioning and Reploy revision model
defined above. The surface calls that common revision a definition revision. It
may correct packages, target data, validation, acquisition, or documentation
for one exact upstream version without changing that version. Requirements may
specify an exact `revision` only when `version` is exact; otherwise resolution
selects the newest eligible revision after selecting the upstream version.

### Bindings

A binding selects an application-facing ecosystem interface to one shared tool.
For example, one Playwright tool owns browser payloads, OS requirements,
validation, and commands while `python` and `node` bindings contribute their
respective ecosystem packages.

An explicit supported binding wins. If only one exists, Reploy infers it. When
several exist, Reploy may infer exactly one matching an already active
application provider; ambiguity lists supported values. An explicit binding
may activate its provider. The initial requirement selects at most one binding.

Bindings are strict provider mappings, not arbitrary package-manager
expressions. Their constraints merge with ordinary application constraints so
the same ecosystem package is resolved and installed once. Selected or inferred
binding is part of request, lock, cache, diagnostics, and documentation
identity.

### Parameters and Sub-payloads

A definition may expose symbolic selections for declared sub-payloads and
strict typed parameters for other bounded choices:

```yaml
tools:
  - tool: acme/playwright
    select: [chromium, webkit]
```

Selections and parameters declare names or symbols, types, required/default
behavior, allowed values, and target availability. Missing required or
unsupported values fail before acquisition and list target-supported values. A
selection or parameter cannot introduce a URL, command, package expression, or
undeclared repository object.

Multiple selections contribute one normalized, deduplicated, order-independent
union of common and selection-specific provider requirements. Publication
rejects definitions whose selectable payloads contain incompatible exact
requirements.

### Runtime Placement and Provider Composition

Runtime tool requirements live with the owning application:

```yaml
environment:
  applications:
    application:
      packages:
        tools:
          - tool: acme/playwright
```

The selected definition contributes ecosystem packages, artifacts, OS package
roots, capabilities, and executable exports to that application. Reploy
activates the appropriate OS provider or contributes packages to the established
one based on the selected base image and target OS. Tool portability may map a
common capability to different native packages; literal OS package requests
remain literal and are never normalized across distributions.

The existing local project recipe form remains build-only:

```yaml
requires:
  - tool:java
```

### Playwright

`tool:playwright` is the first capability expected to exercise the complete
model. Official support should resolve one exact Playwright release and
compatible browser payloads; contribute reviewed target-specific OS roots;
acquire browser payloads through a named Reploy-owned primitive without
exposing project source, host credentials, or arbitrary paths; materialize
offline; and lock platform, browser revision, provenance, artifact digest, and
definition digest.

Browser selection is explicit because payloads are large and materially change
requirements. Omitting it lists supported browsers. Multiple browsers produce
one canonical OS dependency union while retaining separate exact payload
identities. `chromium` and branded `chrome` remain distinct. The first
OmegaFlow definition may support only `chromium` and uses the `python` binding.

The tool exports the supported Playwright CLI into the application executable
namespace. Browser payload executables remain internal unless the definition
deliberately exposes a stable named interface.

### Unsupported Dynamic Installers

The protocol does not translate arbitrary install scripts or post-install
downloaders. Unsupported software may use ordinary OS/ecosystem packaging, a
reviewed tool definition, or a prepared base image. Reploy reports the blocked
operation and available alternatives rather than granting project build code
network access silently.

A future explicit network-enabled build escape hatch remains possible only
after concrete unsupported cases establish its authority and diagnostics. It
is not reserved in the initial public schema.

## Initial Implementation Slices

1. Finalize the repository descriptor, publisher authorization, index,
   publisher-attestation, repository-acceptance, lifecycle-event, shared
   version/revision, and asset target schemas within TUF 1.x.
2. Implement the deterministic repository compiler, strict static validation,
   publisher and maintainer tooling boundaries, and primary/publish branch
   workflow.
3. Integrate a conformant TUF client with trust scopes and surfaces, explicit
   update, untrusted URL search, atomic accepted state, priority, source pins,
   and exact resolution.
4. Retain selected blueprint/tool records in deployment provider-store closures
   and implement disposable global object caching and validated offline import.
5. Publish the independently updated official repository and generated
   documentation.
6. Move Java's existing portable-tool mapping from Go switches into an official
   definition without changing its current project-owned build-only behavior.
7. Add the reviewed Playwright resolver primitive, definition, OS matrix,
   documentation, and integration evidence.

## Deferred Implementation Details

The following do not block the high-level protocol decision:

- exact JSON field encodings, opaque repository-ID representation, and
  canonical signature serialization;
- exact asset-document filenames and transport-specific adjacent-attestation
  discovery conventions for direct BURLs;
- exact auxiliary executable names;
- global disposable object-cache limits, inspection, and cleanup CLI;
- detailed publisher DID evidence presentation and any future curated
  cross-repository search or web-of-trust service;
- additional shared asset version schemes, multi-binding requirements, and
  unsupported dynamic-installer escape hatches;
- any public transparency log or cross-repository lifecycle-history service;
- the final Playwright browser profile beyond the approved initial Chromium and
  Python-binding scope.
