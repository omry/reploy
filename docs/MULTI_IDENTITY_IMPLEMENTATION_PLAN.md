---
status: Composition delivery design proposed; direction approved; implementation gates remain
updated: 2026-10-16
summary: Implementation-ordered composition delivery slices, explicit durable mappings, conditional production/test estimates, and separate approval of public syntax and supervisor authority.
---

# Multi-Identity Delivery Design and Implementation Plan

The [security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md) is authoritative.
The maintainer approved composition-supplied initial mappings and manual host
coordination. This pass proposes the concrete composition boundary and state
protocol. Review stamps assess that proposal; they do not approve new public
syntax, capability policy, or a production supervisor.

The main delivery table is in implementation order. Each group can finish
without a later implementation slice. Named design and evidence gates still
apply. Earlier IDs are preserved in the crosswalk; historical evidence keeps
its original milestone labels. No later implementation has started.

The target is local rootless Linux Podman through the existing Docker-compatible
route. A general native Podman backend, remote engines and Podman Machine are
separate features. Rootful multi-identity support needs its own host/mapping
qualification; ordinary rootful services are not evidence for this profile.
Default ordinary single-identity behavior remains outside this opt-in capability.

## Approved initial direction — 2026-10-16

- A Compose-like composition describes services and their initial UID/GID maps.
- The administrator supplies existing OS-user delegation and coordinates host
  IDs with other installations and unmanaged services, including retained data.
- Durable installation state preserves mappings across recreation, generation
  changes, rollback and inactive retained-data lifetimes. A blueprint edit
  never silently remaps an installation or rewrites its storage ownership.
- Every service keeps private mapping and isolation. Composition membership
  grants no cross-service authority. Sharing needs the explicit interface contract.
- Reploy validates its own inputs and state. It provides no host-wide allocator,
  mandatory registry, enrollment service or concurrency guarantee against
  external administrative changes. Later system configuration is separate work.

## Remaining scope: implementation-ordered delivery table

Letters are delivery groups, numbers are slices within the group. Read top to
bottom. Earlier independent work may run sooner (notably K1). Group subtotals
are arithmetic planning ranges; do not add them to their slice rows. A1's written
code is excluded from remaining totals. Targets do not require padding small
changes to reach 500.

| Group | Slice | Concrete result | Prod target | Production LOC | Test/fixture LOC | Prerequisites |
| --- | --- | --- | ---: | ---: | ---: | --- |
| **A** | **Subtotal** | **Readiness** | — | **0** | **0** | — |
| A | A1 | Private mechanism and compatibility readiness; code written | 0 | 0 | 0 | PR 69; final PR 239 delivery gate |
| **B** | **Subtotal** | **Composition inputs and durable mappings** | — | **2,450–4,150** | **4,100–6,450** | — |
| B | B1 | Read-only caller, delegation and host-principal preflight | 450 | 350–550 | 550–850 | A1; B-design |
| B | B2 | Composition envelope, stable service keys and explicit mapping syntax | 450 | 350–600 | 500–850 | B1; B-design |
| B | B3 | Exact/bounded geometry, disjoint assignments and dormant-ID comparison | 450 | 350–550 | 600–900 | B2 |
| B | B4 | Versioned composition/service state and ordered transaction locks | 450 | 350–600 | 600–950 | B3 |
| B | B5 | Pending intents, recovery, tombstones and conservative reservation retention | 450 | 350–600 | 650–1,000 | B4 |
| B | B6 | Resolve service blueprints and stage existing per-service build inputs | 500 | 400–700 | 650–1,050 | B5 |
| B | B7 | Publish composition generation manifest and reject inconsistent updates | 400 | 300–550 | 550–850 | B6 |
| **C** | **Subtotal** | **Existing engine and build integration** | — | **600–1,200** | **1,150–1,900** | — |
| C | C1 | Scoped Podman admission and compatible exact/bounded create transport | 500 | 350–650 | 650–1,050 | B7; C-design; compatible-map-proof |
| C | C2 | Reuse compatible build/image path; bind immutable service images | 400 | 250–550 | 500–850 | C1 |
| **D** | **Subtotal** | **Locked identity and capability declarations** | — | **950–1,600** | **1,600–2,600** | — |
| D | D1 | Public identity declarations and name/number validation | 300 | 250–400 | 400–650 | C2; D-design |
| D | D2 | Role-specific capability declarations and policy lock | 300 | 200–350 | 350–550 | D1 |
| D | D3 | Offline locked-image name/number evidence | 400 | 300–500 | 500–800 | D2 |
| D | D4 | Generation admission and identity-change diagnostics | 300 | 200–350 | 350–600 | D3; B3 |
| **E** | **Subtotal** | **Mapped execution and supervisor admission** | — | **1,400–2,550** | **2,800–4,550** | — |
| E | E1 | Compile reserved maps; pre-start verification and host-device rejection | 500 | 350–650 | 650–1,100 | D4; C1; launch-design |
| E | E2 | Child credentials, capability drop, descriptors, dumpability and sessions | 500 | 400–750 | 800–1,300 | E1; D2 |
| E | E3 | Supervisor lifetime/replacement and common admission including runtime exec | 450 | 350–650 | 700–1,150 | E2 |
| E | E4 | Effective capability and privilege-regain verification on every entry | 400 | 300–500 | 650–1,000 | E3 |
| **F** | **Subtotal** | **Exclusive preparation and network isolation** | — | **1,350–2,400** | **2,400–3,950** | — |
| F | F1 | Descriptor-safe declared-object preparation without application hooks | 500 | 400–650 | 650–1,050 | E4; storage-design |
| F | F2 | Exclusive prepare/attach admission, crash inhibition and restart fencing | 400 | 300–550 | 600–950 | F1; E3 |
| F | F3 | Existing egress policy and cross-service network-denial enforcement | 500 | 350–650 | 650–1,100 | F2; network-design |
| F | F4 | Declared endpoint publication, effective observation and cleanup | 400 | 300–550 | 500–850 | F3 |
| **G** | **Subtotal** | **Service orchestration and initial activation** | — | **1,050–1,800** | **1,900–3,100** | — |
| G | G1 | Composition install/start/stop/status and every restart-owner interlock | 500 | 400–700 | 750–1,200 | F4; B7; service-design |
| G | G2 | Active revocation, stop/removal and retained-mapping admission | 450 | 350–550 | 550–900 | G1; B5 |
| G | G3 | Failed operation reconciliation and exact owned-resource cleanup | 400 | 300–550 | 600–1,000 | G2; B5 |
| **H** | **Subtotal** | **Ownership lifecycle and offline restore** | — | **2,050–3,500** | **3,600–5,800** | — |
| H | H1 | Ownership-stable stop/start of the existing generation | 300 | 250–400 | 350–550 | G3; generic-launch |
| H | H2 | Recreation preserves mapping and host ownership | 300 | 250–400 | 450–750 | H1 |
| H | H3 | Identity evolution, dormant assignments and rollback | 450 | 350–550 | 650–1,000 | H2; B5 |
| H | H4 | Removal, retained storage and checked reservation release | 350 | 250–450 | 500–800 | H3; G2 |
| H | H5 | Exclusive offline export and integrity manifest | 500 | 350–600 | 550–900 | H4; restore-design |
| H | H6 | Archive/destination validation and explicit checked ownership translation | 450 | 350–650 | 650–1,050 | H5 |
| H | H7 | Exclusive restore activation and failure rollback | 350 | 250–450 | 450–750 | H6; F2 |
| **I** | **Subtotal** | **Secret-file lifecycle** | — | **850–1,400** | **1,500–2,450** | — |
| I | I1 | Secret references and redacted resolution | 300 | 250–400 | 400–650 | D4; secret-design |
| I | I2 | Confined recipient files and no metadata/log disclosure | 400 | 300–500 | 550–900 | I1; F2 |
| I | I3 | Rotation, rollback, restart and removal | 400 | 300–500 | 550–900 | I2; H3; G1 |
| **J** | **Subtotal** | **Shared interfaces and participants** | — | **1,700–2,850** | **3,550–5,750** | — |
| J | J1 | Disposable interface proof and dedicated runtime-path preparation | 400 | 300–500 | 950–1,550 | D4; H4; F2; interface-design |
| J | J2 | Stale socket reconciliation and producer restart | 350 | 250–450 | 500–900 | J1 |
| J | J3 | Composition participant ownership, discovery and admission | 500 | 400–650 | 700–1,100 | J2; B7 |
| J | J4 | Narrow participant attachment preserving private mappings | 500 | 350–600 | 650–1,000 | J3 |
| J | J5 | Participant replacement, revocation and removal | 500 | 400–650 | 750–1,200 | J4; H4 |
| **K** | **Subtotal** | **Component qualification and final evidence** | — | **0** | **2,750–4,550** | — |
| K | K1 | Rspamd/Redis on supported capability-free path | 0 | 0 | 450–750 | A1 (may run independently) |
| K | K2 | Stock Postfix through the approved supervisor model | 0 | 0 | 650–1,100 | H7; I3; F4; mail-compatible; generic-launch |
| K | K3 | Stock Dovecot including synthetic SQL and restore | 0 | 0 | 1,000–1,600 | H7; I3; F4; mail-compatible; generic-launch |
| K | K4 | Combined interface/lifecycle campaign and all-row evidence report | 0 | 0 | 650–1,100 | K1; K2; K3; J5 |
| **Total** | — | **Conditional remaining implementation** | — | **12,400–21,450** | **25,350–41,100** | Named gates apply |

There are **44 delivery candidates** including written A1;
**43 remain**. The conditional code total is
**37,750–62,550 LOC**, split into the production and test ranges above.
This is a sizing scenario, not a complete supported-feature commitment: compatible
exact mapping, production supervisor and mail compatibility remain substantive
design/evidence dependencies. A failing gate can change the count and total.
B6/B7 make previously uncounted composition handling visible. Tests include
adversarial state, crash, restart and supported-interface qualification.

### Group B acceptance by slice

Each checkpoint must prove a complete bounded behavior through the real parser,
resolver or state transaction entry, not only construction of helper structs.
Before B7, use private integration seams; do not expose partially admitted public
execution. B7's public staging route must rerun the assembled B1–B6 contract.

| Slice | Independently verifiable result |
| --- | --- |
| B1 | Read-only proposed-map authority check succeeds for exclusive delegated IDs; UID/GID collisions, hostile caller identity, libsubid disagreement and unavailable inventory reject with unchanged host and installation state |
| B2 | Whole composition decodes to stable service/input records; duplicate services/fields, unknown versions/fields, bad references and missing profile/maps reject. Round-trip canonical input preserves declared authority; no host/runtime mutation occurs |
| B3 | Candidate versus retained-state comparison accepts disjoint exact/bounded maps and rejects overflow, intra-composition overlap, missing exact assignments and prohibited remapping. Removing/reintroducing an identity retains/reactivates the same reservation |
| B4 | First state creation and compare-revision update are durable under the existing safe-file primitives; concurrent writers serialize, stale revisions and substituted lock/state paths reject, and read-only status creates nothing |
| B5 | Inject a crash before/after each intent/effect/commit boundary and reopen through recovery. Pending/ambiguous effects preserve reservations and inhibition; foreign handles cannot be adopted, and retained storage prevents release |
| B6 | Resolve every service through the existing loader/build input route; changed mutable source, invalid child blueprint and failed service build leave the accepted generation untouched. Use immutable snapshots; do not execute application hooks with new authority |
| B7 | Stage a multi-service candidate end to end, reject a raced revision at publication, and retain the previous accepted manifest on failed validation/build. A staged candidate with no supervisor/engine admission cannot launch through any public entry |

The table order is one valid delivery order. The prerequisite column records
technical dependencies, not artificial dependencies merely to serialize unrelated
work. For example secret references do not require restore, and an independent
Postfix qualification does not require the shared-interface campaign.


## Proposed composition design

### Input boundary and operator behavior

The proposal is a versioned composition document with a finite service map.
Each service has a stable key, an existing blueprint reference and an explicit
mapping profile plus UID/GID assignments. UID and GID maps are separate sets of
numeric `(container_start, host_start, count)` intervals. The exact profile
admits only the current declared, supervisor and justified runtime-required IDs;
the bounded profile records explicit fixed geometry. No profile is inferred.
The operator supplies host-specific assignments; an image or service blueprint
cannot grant itself host authority.

Reuse existing blueprint loading, provider builds and generation locks per
service. A composition is a coordination envelope, not another package system
or a replacement for ordinary environment blueprints. Resolve every service
reference to immutable blueprint/build identities before accepting a generation.
Record the accepted inputs separately from the original source; preserve source
authority and reject tampering. Unknown fields, duplicate keys, invalid numbers,
interval overflow, overlapping maps and conflicting service identities fail.

Proposed initial limits: one composition level, explicit service membership,
no nested composition inheritance and no implicit startup dependencies. A renamed
service is not automatically the same installation. Removed services retain
mapping obligations. The exact public field spellings, schema-version handling,
reference forms and command syntax below are **proposals to approve at B-design**,
not approved APIs. B2 cannot ship a public schema before that gate. B6/B7 count
service resolution and staged-generation handling explicitly rather than hiding
composition work in a UID parser.

A composition operation validates the complete candidate before committing its
mapping/generation state. Building service images can run outside the composition
lock against an immutable candidate snapshot. Publication reacquires the lock
and rejects a changed state revision. Failed builds do not replace the accepted
generation. Runtime start is enabled only at generic-launch. The proposed later
batch-start behavior records per-service results and stops on failure; it does
not claim atomic rollback of external side effects. B-design and service-design
must approve the visible partial-failure behavior before implementation.

### Proposed input and operation contract (not yet approved)

Use a distinct composition document selected explicitly by the operator. Do not
reinterpret an ordinary environment blueprint or scan directories for a new
default. The proposed YAML root is `composition`, with required `schema: 1`
and a nonempty `services` map. Reject other root fields. Each service key uses
the existing safe environment-name rules; each value has exactly `blueprint`
and `mapping`. `blueprint` uses an existing `file:`, `git:` or `pypi:` reference.
Resolve relative file references against the composition document's directory;
reuse the existing source-containment and resolution checks. Preserve original
source bytes and record resolved source/build identities separately.

`mapping` has exactly `profile`, `uids` and `gids`. `profile` is required and
is either `exact` or `bounded`. Each nonempty map array contains objects with
exactly `container_start`, `host_start` and `count`. All three are YAML integers,
not strings, booleans or floats. Starts are nonnegative, count is positive,
and every resulting ID must fit the runtime's usable unsigned 32-bit ID space
excluding its unchanged-ID sentinel. Check end arithmetic before addition.
Reject duplicate YAML keys, unknown fields, nulls and overlapping intervals
and unsupported schema versions. Canonical resolved maps sort by container ID;
input order does not change authority. There are no implicit mapping entries,
profile defaults, environment-variable overrides or auto-selected free ranges.

For `exact`, these intervals describe explicit reserved assignments. D4/E1
derive the active map from the image-locked current identity set; dormant
assignments stay reserved but do not enter that active map. For `bounded`, each
ID kind has one fixed contiguous interval. Neither form supplies application
identity declarations or supervisor approval. B staging cannot execute it.

Example of bounded input geometry only; these IDs are illustrative, not a host
delegation claim, a recommended size or an executable supervisor policy:

```yaml
composition:
  schema: 1
  services:
    smtp:
      blueprint: file:./smtp.blueprint.yaml
      mapping:
        profile: bounded
        uids: [{container_start: 0, host_start: 300000, count: 4096}]
        gids: [{container_start: 0, host_start: 400000, count: 4096}]
```

Proposed commands are `reploy compose check --source FILE [--dir DIR]`,
`reploy compose stage --source FILE --dir DIR`,
`reploy compose status --dir DIR`, and
`reploy compose recover --dir DIR --operation ID`.
`FILE` selects a local composition document; its service references can use the
existing source schemes. There is no default file/directory discovery. For
`check`, an optional existing `DIR` supplies retained-state comparison; a missing
or malformed selected installation rejects without creating it. The other
commands require an explicit installation directory. B-design must approve
these flags and their operator-visible behavior before implementation.
`check` reads inputs/delegation and returns a candidate or a precise rejection;
it creates no installation metadata. `stage` resolves/builds service inputs and
publishes a staged composition generation, with execution still disabled.
`status` reads existing state without creating files and distinguishes staged,
execution-admitted, inhibited, revoked and retained records. `recover` is an
explicit mutation restricted to the recorded pending operation and exact owned
objects; it never chooses a replacement mapping. No `prepare` command changes
host accounts or delegation. Runtime commands and partial-start reporting are
specified at service-design, not silently added by B.

### Durable authority and transaction boundary

Proposed state lives below a private composition directory inside the existing
`.reploy` metadata root, using a versioned 0600 state file and a 0700 private
child directory. Reuse descriptor-safe opening, atomic replacement, file sync
and directory sync. Do not change ordinary deployment metadata permissions.
The composition has a stable installation ID and records the operating account,
selected local engine identity, revision and service records. Each service record
contains its stable installation ID, profile, supervisor identity when selected,
accepted UID/GID assignments, active/dormant identities, generation references,
retained managed-storage handles and pending lifecycle operation.

The composition lock serializes all reservation/state changes. If a service lock
is also required, acquire the composition lock first and service locks in stable
key order. Ordinary service operations must join this admission boundary; a
standalone command cannot bypass composition reservations or pending inhibition.
Recheck file/lock identity after acquisition. No host-wide lock is introduced.
A copied metadata directory is not a supported new installation or safe-reuse
proof; administrator coordination remains necessary outside the original state.

Pure validation failure leaves accepted state and owned data unchanged. Before
an admitted operation mutates runtime resources or owned data, persist its
intent and retained reservations. Persist completion only after verifying the
result. If a process crashes after intent commit, ordinary start stays inhibited
until explicit recovery reconciles exact owned objects. Recovery never guesses
that an ambiguous request failed, adopts a foreign object, or releases a range
because a container disappeared. A post-commit failure reports the pending
operation; it does not claim the whole operation left no state change.

B5 implements the state/recovery rules while runtime activation stays disabled.
G1–G3 later integrate real resources and every restart owner. H4 integrates safe
release after actual managed storage disappears or approved translation completes.
Unknown cleanup or retained ownership keeps the reservation. No new global
registry or automatic replacement assignment is part of this protocol.

Proposed JSON state schema `composition-state-v1` has these exact semantic fields.
Unknown versions/fields, duplicate keys and malformed references reject writes
and execution; diagnostics report the incompatible state without repairing it.

| Record | Fields and writer | Required reader behavior |
| --- | --- | --- |
| Composition | `schema`, generated `installation_id`, monotonic `revision`, numeric `operator_uid`/`operator_gid`, `services`, `staged_generations`, nullable `admitted_generation`, nullable `pending_operation`; B4/B5 write under the composition lock | Every mutation compares the expected revision and stable installation identity; G's launch paths also require complete admission and no inhibition |
| Service | Generated `installation_id`, `profile`, `uid_assignments`, `gid_assignments`, `dormant_uids`, `dormant_gids`, nullable `supervisor`, nullable `engine`, `retained_storage`, `revoked`; B4/B5 own assignments, D4 records accepted supervisor/policy evidence, G records engine/resource ownership | Staging may leave supervisor/engine null; execution rejects null or mismatched authority. Once admitted, ordinary updates cannot change profile, supervisor or established assignments |
| Staged generation | Content identity, original composition-source identity, service keys with resolved blueprint identities and existing build-lock references; B7 publishes after full validation | Staging is not runtime admission. D4 validates image-local policy; G records admission only after all launch conditions pass. Updates/rollback use immutable references, never reread a mutable tag as authority |
| Pending operation | Unique `operation_id`, `kind`, expected prior revision, affected service IDs, candidate-generation reference if any, exact owned-object handles, phase and inhibition flag; B5 records intent, G/H extend kinds within their reviewed scope | Only the matching recovery operation can advance it. Missing evidence retains inhibition/reservations. Do not treat an absent container as proof that data or restart owners are gone |

`uid_assignments` and `gid_assignments` use the input interval fields above;
dormant sets identify previously accepted numeric container IDs. `supervisor`
holds accepted UID/GID and locked policy identity when D-design/launch-design
select them. `engine` holds the verified local endpoint/engine binding produced
by C/G. Storage/resource handles use existing ownership labels and immutable
identifiers; paths alone never prove ownership. Do not introduce a second build
lock or copy secret values into composition state. State IDs are installation
identities, not authorization to import a copied installation.

The recovery phases are `intent`, `effects-pending`, `verified` and committed
completion. Write/sync intent and inhibition before effects. An ambiguous
effect stays pending. After verifying exact effects, atomically commit the new
accepted revision and clear that operation; a crash before the atomic replace
leaves the earlier inhibited intent, and a crash after it leaves the committed
revision. No independently updated per-service file is a second authority for
the same assignment. A revoked service remains blocked even when recovery
finishes. Retained reservations survive every incomplete phase. B5 tests these
transitions with injected effects; G/H prove the real runtime/storage effects.

### Mapping admission and lifecycle invariants

B1 validates the real/effective operating account, existing subordinate ranges,
active libsubid agreement and every proposed host UID/GID against local/NSS
principals and other principals' delegation. Unsupported subordinate-ID sources,
unavailable authoritative lookups or incomplete required inventory reject the
operation. Do not treat an unavailable lookup as an unused ID. Initial support
may require an enumerable files delegation backend, as in the private harness;
its exact supported-host requirement must be explicit at B-design.

B3 checks profile geometry and disjoint active, dormant and retained assignments
within the composition. UID and GID collision domains are checked independently.
Every host ID must lie within the selected account's exclusive delegation; host
root and named host accounts are forbidden. Cross-composition coordination is
an administrator obligation, not a safety claim made by this local lock.
Admission refreshes delegation and engine identity before each execution and
checks effective kernel maps before payload release. If host delegation changes
concurrently, Reploy cannot prevent that administrator action; it must reject
known disagreement and never report revocation complete while resources remain.

A later generation can add an exact identity only with an explicit unused
assignment. Removing it makes the assignment dormant. Rollback reactivates that
same numeric assignment. Bounded geometry, profile and supervisor identity stay
fixed. Missing assignments or an attempted remap reject before mutation. No
ordinary update scans or recursively chowns application data. Existing ordinary
same-ID/keep-id handling is not the subordinate multi-identity mapping policy.

### Runtime scope and unresolved decisions

C1 must first prove a compatible creation request for both explicit profiles.
The recorded sparse candidate failed; native private-probe success is not proof
of supported compatible transport. If no bounded change satisfies that request,
block its dependents and revise the design. Do not introduce a native backend
or substitute the bounded profile for the exact profile.

E2/E3 require a reviewed production supervisor/launch design. The private Go
probe is test evidence, not a selected shipped supervisor. The estimate assumes
reuse of existing runtime helper/admission infrastructure and a narrow accepted
supervisor role. A new general supervisor or application process manager is
outside that estimate and needs a separate proposal and new splits.

Before product implementation, approve B's concrete public composition contract.
Before D/E, approve identity/capability syntax and supervisor authority. Before
F/G, approve preparation/restart ownership and supported service behavior.
Mail compatibility is independent: the stock master process cannot silently
become either an ordinary capability-bearing child or the trusted supervisor.

## Baseline and current standing (historical IDs)

- The published native stack is #238:
  HF1 / [#236](https://github.com/omry/reploy/pull/236),
  HF2 / [#237](https://github.com/omry/reploy/pull/237),
  0A / [#62](https://github.com/omry/reploy/pull/62),
  1A / [#63](https://github.com/omry/reploy/pull/63),
  1B / [#69](https://github.com/omry/reploy/pull/69), then
  A1 / [#239](https://github.com/omry/reploy/pull/239).
- Base: `main` at `33d0583c9dfa07367541b1d71d0f3e56bed760ea`, including
  merged HF3–HF8.
- HF1 and HF2 are merged and published in
  [0.8.0.dev3](https://github.com/omry/reploy/releases/tag/v0.8.0.dev3).
  The remaining four slices were locally rebased onto the stated main revision
  on 2026-10-15. All four commit patches are unchanged by that rebase; the new
  heads have not yet been published or checked. PR checks proceed bottom-up
  after publication. 0A is unchanged;
  1A strengthens SI-01 evidence for unauthorized identities inside participants.
  1B owns the atomic admission-release repair and matching source-bound trial
  evidence. Commit titles retain their slice IDs. Exact-head PR approval remains
  distinct from earlier code-equality evidence; A1 requires final review.
- The approved repair-ownership change placed the private harness fixes in 1B.
  Its repaired baseline is `b6862f67661d5861b9b857fab55283291d815684`;
  its earlier rebased head was `901013a2f4e4ff64b0d76cc1d40cf58f4a600e3e`.
  The reviewed pre-rebase 1B repair is `8b1ca4f046a968c932eff83e98060b334ce368d4` and
  includes atomic admission-release publication, its concurrent regression and
  explicit mapped-root policy rejection cases, failure-receipt preservation,
  a supervisor omitted-CAP_CHOWN operation with a same-owner positive control,
  and a framed UDP producer response that rejects client self-echo.
  A1 retains the audit, audit tests, readiness and plan. Its final review also
  fixes restart readiness and classification,
  volume failure independence and existing empty evidence-directory handling.
  A1's final design and PR checks remain its delivery gate.
- [Pre-start validation finding](https://github.com/omry/reploy/pull/69#discussion_r3787738714):
  the 1B repair admits all four workload/control mappings before payload release.
- [Auxiliary-mapping finding](https://github.com/omry/reploy/pull/69#discussion_r3787738718):
  both the no-capability and unfiltered controls now pass admission.
  Six further accepted findings covered IPC success detection, application-side
  descriptor checks, producer network controls, atomic readiness and peer mounts.
  All eight findings have verified repairs and resolved review threads.
- On 2026-10-14, the fresh campaign of three exact and three bounded trials passed in
  565.601 seconds, including setup and cleanup. The probe source SHA-256 is
  `c975a2c83dec68efb133932159332e4ae912e6cb65d61724dbbff15a2e6b0310`.
  The compatible-operation audit used those same probe bytes. Its command took
  50.432 seconds; its internal timer, including preflight, reported 50.380 seconds.
  The audit retains original failure and cleanup receipts when inventory or
  service cleanup fails; unknown inventory cannot establish restoration. Failed bounded
  setup or restart records its dependent lifecycle seams as blocked. Unproved mapping
  cleanup blocks later mapping cases while independent network evidence continues. Restart is recorded
  as compatible only after unchanged mappings and fresh admission are verified.
  [Current evidence](../tools/conformance/multiidentity/EVIDENCE.md) retains
  exact source identities, raw receipts and cleanup results.
  Older observations do not prove the repaired source or pre-payload boundary.
- A separate operator report tested Reploy 0.8.0.dev3 with rootless Podman
  6.1.4, Docker CLI 29.8.0, Compose 5.5.1 and Buildx 0.37.0 on Debian 11.
  Execution, Compose and a registry-based BuildKit build with loading passed;
  Reploy generated-layer building failed because the containerized builder
  could not see daemon-local base tags. This is a concrete C2 transport
  prerequisite, not evidence for a native backend. The production build path
  does not call `ProbeBuildKitCapabilities`; its earlier Podman 4.9.3 audit
  rejection must not be generalized to this newer host. Duplicate-tag cleanup
  was fixed by merged HF3 [#241](https://github.com/omry/reploy/pull/241) and
  HF4 [#242](https://github.com/omry/reploy/pull/242). Subsequent merged fixes
  add automatic builder selection (HF5 / #243), authoritative health metadata
  (HF6 / #244), disabled-healthcheck timer preparation (HF7 / #245) and Docker
  seccomp policy/private-home ownership compatibility (HF8 / #246).
  The [readiness disposition](../tools/conformance/multiidentity/READINESS.md)
  records each result and remaining limits. HF8 requires an engine with upstream
  #29726 and rebuilt runtime images; stock Podman 6.1.4 remains insufficient.
  Flux reports that Podman looks OK and is continuing evaluation. Final
  qualification, unexplained conmon monitors and multi-identity acceptance remain
  open. The stack now includes these fixes through its main baseline; the
  earlier private mechanism and operation audits have not been rerun by this
  rebase. These hotfixes do not establish multi-identity acceptance.
- The original detailed campaign is the private, untracked
  [`temp/flux-support-work-plan.md`](../temp/flux-support-work-plan.md).
  Its old baseline, gap map and sprint status are historical planning context,
  not a current completion ledger. The contract introduction also retains
  historical one-off-probe wording. A wording correction alone cannot close a
  harness defect; the reviewed 1B baseline and current A1 source-bound trials supply that proof.


Read-only reconciliation on 2026-10-16 confirms that local 0A, 1A, 1B and A1
patches are byte-equal to the corresponding published git-style patches. Their
local heads differ from the published heads. The current local design tip is
`cc3b23a52441`, above local A1 `18bbf46a63b4`; it has no open design PR yet.
Published PR approval records name the published heads, not these local heads.
Do not rebase this stack again as part of this design pass or rewrite ancestor
patches/descriptions. Use the existing PR mappings if synchronization is needed.

HF9 [#247](https://github.com/omry/reploy/pull/247), HF10
[#248](https://github.com/omry/reploy/pull/248) and HF11
[#249](https://github.com/omry/reploy/pull/249) are now merged on newer main.
They address effective base override, Podman tagged-image identity and ordinary
same-primary-ID managed-storage access respectively. They are **not present in
this stack's base**. Reuse assessment must distinguish the local baseline from
newer main. In particular HF11's ordinary keep-id policy is not the isolated
multi-identity policy, and its network-enabled Podman controlled sessions remain
deferred. Existing mechanism receipts are not new qualification of those fixes.

## Delivery sizing and estimation

Aim for about **500 production LOC per PR**. **750 is a hard maximum**.
Count added or rewritten handwritten shipped code, comments, blank lines and
helpers. Do not subtract deletions; moving code counts at the destination.
Report deletions and mechanical moves separately. Tests/fixtures are separate
and have no numeric cap. Prose, review sidecars, generated code, dependencies
and observations do not count as production or tests.

Ranges below estimate incremental changes, not full reused files, future measured
diffs, elapsed time or token budgets. Shared helpers belong to the first slice
that introduces them. Recheck actual production LOC before every checkpoint;
split an oversized slice before publication as `B1.1`, `B1.2`, and update its
prerequisites. Do not remove required behavior to fit. Each initial upper bound
is at most 750, but unresolved transport/supervisor designs can invalidate their
conditional estimates. In that case re-estimate/split before implementation;
an estimate is not authorization or proof of feasibility.

## Source-grounded reuse and scale

Physical line counts below were remeasured at `cc3b23a52441` on 2026-10-16,
including comments and blank lines. Test counts cover the paired test file only.
These are reuse references, not incremental estimates.

| Existing seam | Production LOC | Paired test LOC | Reuse and limitation |
| --- | ---: | ---: | --- |
| `internal/deploy/operation_lock.go` | 214 | 122 | Stable descriptor-held deployment lock; not a composition transaction |
| `internal/deploy/atomic_state.go` | 50 | 39 | Atomic file replacement and sync; not multi-service recovery |
| `internal/deploy/blueprint_loader.go` | 200 | — | Existing blueprint references; no composition service map |
| `internal/blueprint/model.go` | 352 | — | Single-environment model; composition is new scope |
| `internal/blueprint/syntax.go` | 257 | 195 | Strict YAML decoding patterns, not new public syntax approval |
| `internal/dockerdeploy/application_sandbox_plan.go` | 208 | 282 | Common sandbox; currently requires dropping all capabilities |
| `internal/dockerdeploy/install_runtime_identity.go` | 142 | 124 | Single numeric account adaptation; no declared transition set |
| `internal/dockerdeploy/runtime_host_filesystem_linux.go` | 626 | 509 | Host path/mount checks; not mixed-owner object preparation |
| `internal/dockerdeploy/private_workload_environment.go` | 347 | 509 | Private input/redaction patterns; no secret-file lifecycle |
| `internal/deploy/runtime_policy.go` | 209 | 101 | Locked policy structure; no multi-identity roles |
| `internal/dockerdeploy/generation_reference_docker.go` | 299 | 313 | Existing image identity/reference validation |
| `internal/dockerdeploy/provider_install_systemd.go` | 131 | 193 | Existing Compose service; not complete rootless restart ownership |

The private harness and component fixtures remain tests. They are sources of
assertions and historical mechanism evidence, not production implementations.
Build/image compatibility must reuse merged fixes when available; do not copy
an obsolete local implementation or add registry credential machinery to this
composition work. Direct native Podman operations already approved for individual
hotfixes do not authorize a general native backend.

## Group descriptions

- **A — Readiness.** Retain the six source-bound trials, compatible-operation
  dispositions and precise stock-mail blocker. PR 239 is the existing delivery.
- **B — Composition foundation.** Read-only host checks, explicit inputs,
  mapping geometry, durable state/recovery, service blueprint resolution and
  staged generation publication. Finish this group without launching workloads.
  It creates no host accounts, delegation changes, enrollment or global registry.
- **C — Existing runtime integration.** Identify and pin the selected local
  Podman endpoint, prove both compatible map requests, and reuse immutable image
  handling. A transport gap blocks dependent integration; native backend work
  does not enter through this group.
- **D — Locked declarations.** Define and validate image-local names/numbers,
  role-specific capabilities and generation evidence. Keep public activation
  disabled; default capabilities remain empty.
- **E — Mapped execution.** Compile reserved maps and verify them before payload
  release. Enforce final child credentials/groups, capability sets, descriptor
  closure, supervisor non-dumpability and separate sessions on every supported
  entry. An unproved exec/restart path rejects instead of running raw exec.
- **F — Preparation and isolation.** Prepare only declared inert managed objects
  with no elevated application code. Exclude attached workloads, inhibit restart
  through recovery, and prove existing egress/endpoint policy on Podman. Keep
  private network namespaces and reject undeclared peer reachability.
- **G — Initial service activation.** Integrate composition/user-service lifecycle,
  all autonomous restart owners, active revocation and exact cleanup. Complete
  generic-launch before public multi-identity execution is supported.
- **H — Durable ownership.** Verify stop/start and recreation separately, then
  identity evolution, retained-data removal, offline export and restore. Check
  destination allocation/delegation before writing archived ownership; reject
  unsafe destinations without altering their accepted state. Use collision-safe
  staging or an explicitly checked translation. Never automatically chown updates.
- **I — Secrets.** Resolve references without values in metadata/logs, deliver
  recipient-confined files and handle rotation, rollback and retained state.
- **J — Shared interfaces.** Prove the narrow mechanism first, then dedicated
  paths, stale-socket recovery, participant authority and private-map isolation.
  Declared participants do not gain access as every identity in their workload.
- **K — Qualification.** Adapt disposable component fixtures to supported Reploy
  operations. Stock Postfix/Dovecot remain conditional on approved supervisor
  compatibility. SQL fixtures are tests, not a generic database backup product.

This order deliberately moves revocation behind runtime ownership, declaration
policy before child launch, and preparation/network checks before service
activation. It replaces the earlier group table with forward prerequisites.
No obsolete enrollment/global-allocator implementation remains in B.

## Required design and evidence gates

Gates are work inside delivery, not extra code PRs. Review establishes design
quality; only the maintainer can approve a new intended behavior or authority.
Unresolved later decisions do not block independent foundations.

| Gate | Required before | Required decision or evidence |
| --- | --- | --- |
| B-design | B1–B7 | Reviewed composition input/state proposal, public syntax, supported-host/delegation inventory and operator-visible failure/recovery behavior. Manual coordination is already approved. |
| compatible-map-proof | C1 implementation completion | Fresh exact and bounded compatible creation, planned/effective host maps and pre-payload negative tests; preserve private-harness receipts as separate evidence. |
| C-design | C1–C2 | Operation dispositions and bounded adapter/build approach; no general native backend. |
| D-design | D1–D4 | Public identity/capability syntax, locked image evidence, role allowlists and default-empty behavior. |
| launch-design | E1–E4 | Production supervisor ownership and launch/replacement boundary; helper/supervisor authority, child descriptor/credential/capability plan and every supported entry. |
| storage-design | F1–F2 | Declared objects, confined ownership preparation, exclusion, restart inhibition and explicit crash recovery. |
| network-design | F3–F4 | Existing egress/endpoint semantics, pre-start effective observation and cleanup on selected Podman engine; HF11's deferred endpoint session gap is not declared fixed. |
| service-design | G1–G3 | Concrete user-service/restart ownership and composition partial-failure behavior; no hidden general scheduler. |
| generic-launch | Public execution after G3 | Both profiles; all accepted/undeclared identity transitions; final credentials/groups/capabilities/descriptors; supervisor separation; every application entry including exec; namespaces, devices, network denial, ordinary owned storage, mismatch/no-mutation, restart exclusion, active revocation and exact cleanup. |
| restore-design | H5–H7 | Offline export, integrity, destination allocation/delegation before ownership writes, collision-safe staging or approved checked translation and rollback. |
| secret-design | I1–I3 | Sources, recipients, confined paths, redaction and lifecycle authority. |
| interface-design | J1–J5 | Explicit producer/consumer identities, dedicated path/registry ownership, narrow discovery/attachment and safe replacement/revocation. Disposable proof passes before attachment activation. |
| mail-compatible | K2–K3 | Stock-master role and image-locked process/capability evidence fit the contract. Otherwise this branch remains blocked without relabeling a failure. |
| completion | After K4 | All 19 mandatory contract rows pass through supported interfaces. A report listing blockers is not completion. |

B can deliver inactive state/model code only after B-design approval. This pass
is the reviewed proposal for that decision, not its automatic approval. Later
gates are resolved immediately before the group that needs them; do not ask
for every later policy decision before delivering independent B work.

## Previous-to-current slice crosswalk

The prior table had 42 candidates. IDs below refer to that table, not the older
66-row inventory. Historical milestones 0A/1A/1B and evidence/PR titles do not
change. Existing A1 stays A1. Prefix future commit and PR titles with the new
slice ID. Further splits use decimal children, never a new singleton group.

| Previous ID | Current ID | Responsibility |
| --- | --- | --- |
| A1 | A1 | Private mechanism and compatibility readiness; code written |
| B1 | B1 | Read-only caller, delegation and host-principal preflight |
| B2 (input model) | B2 | Composition envelope, stable service keys and explicit mapping syntax |
| B4 | B3 | Exact/bounded geometry, disjoint assignments and dormant-ID comparison |
| B3 | B4 | Versioned composition/service state and ordered transaction locks |
| B5 | B5 | Pending intents, recovery, tombstones and conservative reservation retention |
| B2 (service resolution; newly explicit) | B6 | Resolve service blueprints and stage existing per-service build inputs |
| B2 (generation binding; newly explicit) | B7 | Publish composition generation manifest and reject inconsistent updates |
| C1 | C1 | Scoped Podman admission and compatible exact/bounded create transport |
| C2 | C2 | Reuse compatible build/image path; bind immutable service images |
| D1 | D1 | Public identity declarations and name/number validation |
| D4 | D2 | Role-specific capability declarations and policy lock |
| D2 | D3 | Offline locked-image name/number evidence |
| D3 | D4 | Generation admission and identity-change diagnostics |
| C3 | E1 | Compile reserved maps; pre-start verification and host-device rejection |
| C4 | E2 | Child credentials, capability drop, descriptors, dumpability and sessions |
| C5 | E3 | Supervisor lifetime/replacement and common admission including runtime exec |
| D5 | E4 | Effective capability and privilege-regain verification on every entry |
| E1 | F1 | Descriptor-safe declared-object preparation without application hooks |
| E2 | F2 | Exclusive prepare/attach admission, crash inhibition and restart fencing |
| G1 | F3 | Existing egress policy and cross-service network-denial enforcement |
| G2 | F4 | Declared endpoint publication, effective observation and cleanup |
| C6 | G1 | Composition install/start/stop/status and every restart-owner interlock |
| B6 | G2 | Active revocation, stop/removal and retained-mapping admission |
| C7 | G3 | Failed operation reconciliation and exact owned-resource cleanup |
| E3 | H1 | Ownership-stable stop/start of the existing generation |
| E4 | H2 | Recreation preserves mapping and host ownership |
| E5 | H3 | Identity evolution, dormant assignments and rollback |
| E6 | H4 | Removal, retained storage and checked reservation release |
| E7 | H5 | Exclusive offline export and integrity manifest |
| E8 | H6 | Archive/destination validation and explicit checked ownership translation |
| E9 | H7 | Exclusive restore activation and failure rollback |
| F1 | I1 | Secret references and redacted resolution |
| F2 | I2 | Confined recipient files and no metadata/log disclosure |
| F3 | I3 | Rotation, rollback, restart and removal |
| H1 | J1 | Disposable interface proof and dedicated runtime-path preparation |
| H2 | J2 | Stale socket reconciliation and producer restart |
| H3 | J3 | Composition participant ownership, discovery and admission |
| H4 | J4 | Narrow participant attachment preserving private mappings |
| H5 | J5 | Participant replacement, revocation and removal |
| I1 | K1 | Rspamd/Redis on supported capability-free path |
| I2 | K2 | Stock Postfix through the approved supervisor model |
| I3 | K3 | Stock Dovecot including synthetic SQL and restore |
| I4 | K4 | Combined interface/lifecycle campaign and all-row evidence report |

Previous gate names follow their responsibilities: old C-design is now C-design
for transport and launch-design for supervisor execution; old D-design remains
D-design; old E-design becomes storage-design/restore-design; old F/G/H-design
become secret-design/network-design/interface-design. Old generic-launch and
completion retain their meaning with the current prerequisites above.

## Acceptance ownership and publication checks

The contract's 19 acceptance rows remain authoritative. Slice completion does
not automatically close an evidence row.

| Contract row | Current owning slices and evidence |
| --- | --- |
| ID-01 | A1; B1, B3, E1; generic-launch |
| ID-02 | D1, D3–D4, E2–E3; generic-launch |
| ID-03 | E2–E4; generic-launch |
| ID-04 | A1; F3, G1; generic-launch; J preserves isolation |
| BE-01 | A1; C1; generic-launch |
| CAP-01 | D2, E2, E4; generic-launch |
| LC-01 | H1–H2 |
| LC-02 | B3–B5, D4, H3 |
| LC-03 | H5–H7; destination authority checked before archived ownership |
| AL-01 | B3–B5, E1, F2, H4 |
| RV-01 | G1–G3, H4; generic-launch |
| HB-01 | B1–B2, G1; generic-launch |
| SB-01 | A1; E1; generic-launch |
| SB-02 | E1–E4, F3–F4; generic-launch; every application entry |
| FS-01 | F1–F2 |
| CF-01 | B1, B7, D3–D4, E1, H1–H2, H6–H7; generic-launch |
| SI-01 | J1, J3–J4, K4; reject unauthorized identities inside participants |
| SI-02 | J1, J4, K4 |
| SI-03 | J2, J5, K4 |

PR-cycle approval requires every exact-head job generated by `ci.yml`: Linux CI
checks, Bash native evidence checks, four Target smoke jobs (linux-amd64,
linux-arm64, darwin-arm64, darwin-amd64), and two Windows host smoke jobs
(windows-amd64, windows-arm64) at this baseline. Derive the list again at delivery.
Missing, pending, cancelled or failed jobs block approval. Preserve exact-head
approval evidence; a byte-equal rebased patch alone is not a current stamp.
Do not rebase the multi-identity stack on unrelated hotfixes during this pass.

## Initial group A delivery specification

**Task A1** is the only initial delivery request. It is test/fixture code:
**0 production LOC**, plus test/fixture code, prose and retained observations.
The original estimate was 650–1,300 test LOC. After the approved ownership move,
A1 adds 879 test/fixture lines against 1B; its published PR is approved; the rebased local head has not been published or checked.
It does not add a public schema, change production runtime policy or approve
later host/supervisor choices. Its 500/750 production rule still applies if a
product defect changes that classification; return such work to a separately
bounded and approved production slice instead of absorbing it here.

Acceptance checkpoints inside A1:

1. Use the approved 1B harness to validate planned host UID/GID authority before mutation and gate payload
   execution on verified effective maps. Cover both primary workloads, the
   no-capability default and the unfiltered seccomp control. Inject NSS and
   foreign-delegation collisions for UID and GID; prove no application/control
   payload ran and no probe-owned persistent data was written on rejection.
   Cleanup may remove only objects created and identified by this attempt.
2. After those fixes and focused tests pass, record three consecutive fresh
   runs for each exact/bounded profile with new two-workload isolation cases,
   Docker pre-mutation rejection, current image/source/host identity and exact
   attempt-owned cleanup. Preserve the full frozen security contract. Do not
   rerun the known-defective harness or use old observations as current proof.
   Every required mechanism check must pass. A failed or unavailable fresh-run,
   Docker-negative or cleanup checkpoint leaves A1 blocked and prevents later
   product implementation; an independently reviewed repair PR does not close A1.
3. Audit the actual Reploy operation set through the explicitly scoped compatible
   endpoint, including exact sparse and bounded fixed mapping, image/reference,
   build, service/restart ownership, volume, network/endpoint and cleanup seams.
   Record supported reuse, narrow changes and blockers independently; a failed
   operation is evidence, not an instruction to implement a native backend.
4. Produce stock Postfix/Dovecot process/capability compatibility evidence or a
   precise blocker against the supervisor/child trust split. A1 can deliver a
   readiness report with that blocker, but mail-compatible remains unsatisfied.
5. Update curated evidence and the source-grounded plan with dispositions and
   bounded downstream estimates where supported; do not invent a total for
   unresolved alternatives. All runtime resources are disposable and uniquely
   owned. Do not change production Flux, Arbiter checkout, host delegation or
   unrelated containers/volumes to make a probe pass.

A1 completion requires the mandatory fresh dual-profile mechanism checkpoint
to pass and the reviewed compatibility/mail observations to be attributable.
It does not mean either runtime compatibility or the mail branch passed if
their disposition is blocked. Without the required fresh mechanism pass, A1
remains incomplete even when its harness repair PR is ready or merged. Later
public schema/runtime implementation remains unauthorized until that pass and
its owning design decisions are satisfied.
The existing PR 69 owns the harness and its approved repairs.
A1 is the existing PR 239 follow-up in the same six-PR native stack.
Preserve that mapping and every slice-prefixed commit title.
Do not create a duplicate PR or rewrite an approved ancestor.


## Next delivery request

Finish the local and remote review of this design checkpoint first. Then obtain
B-design approval of its concrete public contract. The first revised delivery
set is B1–B5: host preflight, inputs, geometry, durable state and recovery. B6–B7
complete composition staging after that foundation. The next major system is
C's existing-engine integration, with compatible-map-proof before activation.
Do not resume the old enrollment proposal or infer an automatic full campaign.

```text
global:swe:deliver-design-stack(
  "docs/MULTI_IDENTITY_IMPLEMENTATION_PLAN.md",
  tasks="B1,B2,B3,B4,B5"
) -> produce run stats, timing, observed tokens and issues/opportunities
```

This invocation is proposed, not started by this document. Every commit title
must include its slice ID. No merge, release, live service changes or changes
to the Arbiter/Podman checkouts are part of this design pass.
