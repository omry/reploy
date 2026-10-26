---
status: A/B landed; C internal integration delivered; full-campaign architecture approved; remaining group delivery includes LLD preparation and review; public mapped execution remains inhibited
updated: 2026-10-26
summary: Full-campaign architecture and contracts for isolated single-installation identities, with implementation-ordered slices and separate proof and approval gates.
---

# Multi-Identity Delivery Design and Implementation Plan

The [security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md) is authoritative.
On 2026-10-19, the maintainer accepted separating composition from multi-identity.
A1 and B1–B5 are landed. They provide explicit single-installation mapping
input, validation, durable state and conservative recovery. General composition
remains separate. The delivered C checkpoint adds internal engine/image seams;
it does not enable public mapped execution. C-design approval and C1/C2 delivery
are recorded in the retained C campaign. Existing stamps attest their exact
recorded bytes. The maintainer approved the reviewed campaign architecture on
2026-10-26; that approval does not supply later LLD or runtime proof.

The main delivery table is in implementation order. Each group can finish
without a later implementation slice. Named design and evidence gates still
apply. Each group's delivery includes LLD preparation and review before its
implementation, as specified in the group delivery sequence below. Earlier IDs
are preserved in the crosswalk; historical evidence keeps
its original milestone labels. C1 and C2 are implemented; their live proof uses
a patched engine in a disposable eligible guest. This is not stock-Podman or
public mapped-workload qualification. Current remote approval remains a separate
exact-head check; this document update does not establish it.

The target is local rootless Linux Podman through the existing Docker-compatible
route. A general native Podman backend, remote engines and Podman Machine are
separate features. Rootful multi-identity support needs separate qualification.
The proposed first runtime cohort is non-root staging and user-scope installation
under that same rootless operating account. System-scope multi-identity install
and root-run mapped operations reject before mutation; the existing system-unit
path does not establish that rootless runtime account. This is the initial mapped runtime scope; it does not claim system-scope support.
Ordinary single-identity behavior keeps its backend-independent default.

## Approved scope boundary — 2026-10-19

- Implement mapping input, validation, state and recovery for one workload
  installation through the existing deployment path.
- The administrator supplies existing OS-user delegation and coordinates host
  IDs across installations, unmanaged services and retained data.
- Allow explicit mappings on staged test deployments as well as installed
  destinations. Preserve stage/run/test/install through the same security gates.
  Installation requires an explicit destination mapping decision; it cannot
  copy source reservations, pending operations or mapped storage ownership.
- Preserve mappings across recreation, generations, rollback and retained-data
  lifetimes. No automatic remapping or recursive ownership rewrite occurs.
- Defer general composition: service membership, blueprint references, batch
  staging, startup ordering and multi-service orchestration need a separate design.
  A future composition may supply the same per-installation mapping input; it
  must not become a second authority over an installation's recorded mappings.
- Keep sharing separately gated. Grouping or naming services grants no shared
  identity, storage or network authority.
- Add no Reploy-wide enrollment, allocator, registry or privileged broker.
- Put mapping declarations in the blueprint. Allow deployment overrides to supply
  machine-specific values, including required values marked `???`. A separate
  mapping file is not required. Existing assignments remain protected regardless
  of where new input comes from.

The 2026-10-16 discussion suggested composition as a place for initial mappings.
The superseded proposal made that a delivery dependency. The scope correction
above removes that dependency while retaining explicit mappings and manual
external coordination. It does not approve a composition API.

## Remaining scope: implementation-ordered delivery table

Letters are delivery groups, numbers are slices within the group. Read top to
bottom. Earlier independent work may run sooner (notably K1). Group subtotals
are arithmetic planning ranges; do not add them to their slice rows. Landed A1 and B1–B5
and delivered C1/C2 are excluded from remaining totals. Targets do not require padding small
changes to reach 500.

| Group | Slice | Concrete result | Prod target | Production LOC | Test/fixture LOC | Prerequisites |
| --- | --- | --- | ---: | ---: | ---: | --- |
| **A** | **Subtotal** | **Readiness** | — | **0** | **0** | — |
| A | A1 | Private mechanism and compatibility readiness; landed | 0 | 0 | 0 | Delivered |
| **B** | **Subtotal** | **Single-installation inputs and durable mappings; landed** | — | **0** | **0** | — |
| B | B1 | Read-only caller, delegation and host-principal preflight; landed | — | 0 | 0 | Delivered |
| B | B2.1 | Blueprint mapping schema, required values and immediate opt-in execution inhibition; landed | — | 0 | 0 | Delivered |
| B | B2.2 | Typed deployment overrides and operation-specific effective mapping resolution; landed | — | 0 | 0 | Delivered |
| B | B3 | Exact/bounded geometry, disjoint assignments and dormant-ID comparison; landed | — | 0 | 0 | Delivered |
| B | B4 | Identity record, operation lock and destructive-state retention guards; landed | — | 0 | 0 | Delivered |
| B | B5 | Pending intents, recovery, tombstones and conservative reservation retention; landed | — | 0 | 0 | Delivered |
| **C** | **Subtotal** | **Internal engine and build integration; delivered** | — | **0** | **0** | — |
| C | C1 | Scoped Podman admission and compatible exact/bounded create transport; delivered | — | 0 | 0 | Delivered; public activation remains gated |
| C | C2 | Reuse compatible build/image path; bind immutable service images; delivered | — | 0 | 0 | Delivered; public activation remains gated |
| **D** | **Subtotal** | **Locked identity and capability declarations** | — | **950–1,600** | **1,600–2,600** | — |
| D | D1 | Public identity declarations and name/number validation | 300 | 250–400 | 400–650 | C2; D-design |
| D | D2 | Role-specific capability declarations and policy lock | 300 | 200–350 | 350–550 | D1 |
| D | D3 | Offline locked-image name/number evidence | 400 | 300–500 | 500–800 | D2 |
| D | D4 | Locked-identity coverage, generation admission and identity-change diagnostics | 300 | 200–350 | 350–600 | D3; B3 |
| **E** | **Subtotal** | **Mapped execution and supervisor admission** | — | **1,400–2,550** | **2,800–4,550** | — |
| E | E1 | Compile reserved maps; pre-start verification and host-device rejection | 500 | 350–650 | 650–1,100 | D4; C1; launch-design |
| E | E2 | Child credentials, capability drop, descriptors, dumpability and sessions | 500 | 400–750 | 800–1,300 | E1; D2 |
| E | E3 | Supervised lifetime, host-admitted whole-container replacement and runtime exec | 450 | 350–650 | 700–1,150 | E2 |
| E | E4 | Effective capability and privilege-regain verification on every entry | 400 | 300–500 | 650–1,000 | E3 |
| **F** | **Subtotal** | **Exclusive preparation and network isolation** | — | **1,350–2,400** | **2,400–3,950** | — |
| F | F1 | Descriptor-safe declared-object preparation with fresh ownership authority | 500 | 400–650 | 650–1,050 | E4; storage-design |
| F | F2 | Exclusive prepare/attach admission, crash inhibition and restart fencing | 400 | 300–550 | 600–950 | F1; E3 |
| F | F3 | Existing egress policy and cross-service network-denial enforcement | 500 | 350–650 | 650–1,100 | F2; network-design |
| F | F4 | Declared endpoint publication, effective observation and cleanup | 400 | 300–550 | 500–850 | F3 |
| **G** | **Subtotal** | **Service orchestration and initial activation** | — | **1,050–1,800** | **1,900–3,100** | — |
| G | G1 | Existing staged/installed lifecycle and explicit destination mapping boundary | 500 | 400–700 | 750–1,200 | F4; B5; service-design |
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
| J | J3 | Explicit participant ownership, discovery and admission | 500 | 400–650 | 700–1,100 | J2; B5 |
| J | J4 | Narrow participant attachment preserving private mappings | 500 | 350–600 | 650–1,000 | J3 |
| J | J5 | Participant replacement, revocation and removal | 500 | 400–650 | 750–1,200 | J4; H4 |
| **K** | **Subtotal** | **Component qualification and final evidence** | — | **0** | **2,750–4,550** | — |
| K | K1 | Rspamd/Redis on supported capability-free path | 0 | 0 | 450–750 | A1 (may run independently) |
| K | K2 | Stock Postfix through the approved supervisor model | 0 | 0 | 650–1,100 | H7; I3; F4; mail-compatible; generic-launch |
| K | K3 | Stock Dovecot including synthetic SQL and restore | 0 | 0 | 1,000–1,600 | H7; I3; F4; mail-compatible; generic-launch |
| K | K4 | Combined interface/lifecycle campaign and all-row evidence report | 0 | 0 | 650–1,100 | K1; K2; K3; J5 |
| **Total** | — | **Conditional remaining implementation** | — | **9,350–16,100** | **20,100–32,750** | Named gates apply |

There are **43 delivery candidates**. A1 and six B slices are landed;
**34 remain after delivered C1/C2**. B2 is split into B2.1 and B2.2 for the revised input design.
The conditional code total is
**29,450–48,850 LOC**, split into the production and test ranges above.
This is a sizing scenario, not a complete supported-feature commitment: compatible
exact mapping, production supervisor and mail compatibility remain substantive
design/evidence dependencies. A failing gate can change the count and total.
The superseded composition-specific B6/B7 are deferred and excluded. Other
foundation estimates remain conservative; this is not a measured code saving. Tests include
adversarial state, crash, restart and supported-interface qualification.

### Group B acceptance by slice

Each checkpoint proves one bounded behavior through the real parser, resolver
or state transaction entry. B uses private integration seams until its complete
metadata admission path is assembled. It cannot launch a multi-identity payload.

| Slice | Independently verifiable result |
| --- | --- |
| B1 | Read-only proposed-map authority check succeeds under the proposed files-backed host contract; UID/GID collisions, hostile caller identity, unsupported delegation backends, libsubid disagreement and incomplete bulk principal inventory reject without mutation. Large-range controls prove query work does not grow with represented IDs; invalid/out-of-delegation intervals reject before inventory queries |
| B2.1 | Decode blueprint mapping declarations with typed unresolved scalar values and ship the common declaration-based execution guard in the same slice. Reject every existing payload/installation path before effects for any mapping declaration, whether resolved or missing, until generic-launch. Prove guards through actual run/test/app-command/session/install entry points before accepting the declaration publicly. Preserve `???` through unrelated portable stage/build; reject malformed input and invalid literals. Exercise resolved-document round trips and strict old-reader rejection; add no second loader |
| B2.2 | Apply the bounded mapping override section through existing deployment input paths before resolving required values and validating final types. Reject unresolved fields with exact paths before mapping-dependent effects. Check duplicate keys, complete-array replacement, precedence, override snapshots and source races. Changing deployment-only mappings does not rewrite original blueprint/build authority. Preserve existing package overrides and sidecar variable semantics; no service list or generic deep merge is added |
| B3 | Compare proposed assignments with retained installation state; reject overflow, overlapping intervals and prohibited reassignment. Omitted retained assignments remain reserved. Numeric geometry needs no image or final supervisor identity |
| B4 | Commit optional identity metadata atomically with the existing deployment state under its existing operation lock. Bind the selected staged or installed owner and compare expected mapping revision and generation; ordinary updates preserve the record and unrelated workloads or copied directories cannot adopt it. Extend the B2.1 guard to retained identity state even if the declaration is removed. Before effects, reject every destructive authority transition, including cross-environment `stage --force`, staged removal, installed `uninstall --remove-dir`, terminal retries and pending-removal recovery; force, wait and aliases cannot bypass this. Retain the guards until H4 provides checked release. Prove every writer preserves authority or rejects before effects; omission cannot downgrade it. Concurrent writers serialize; stale/substituted state rejects. Read-only inspection creates nothing; old strict readers reject opt-in state |
| B5 | Assemble mapping configure/check/status/recovery; inject crashes around intent/effect/commit boundaries. Pending or ambiguous effects retain inhibition and reservations. Reuse and retest the B2.1/B4 guards across every workload entry; B5 is not their first implementation. Reject unknown/foreign ownership and keep ordinary absent-declaration/absent-state behavior unchanged. Test staged configuration/update preservation, copied-owner rejection, destination ID mismatch, exact pending retry and checked unused-candidate replacement/cancellation through the private deployment seam |

Identity completeness is a later admission check. D4 uses D1/D3's declared,
image-locked identity evidence and rejects missing assignments for those IDs.
E1 verifies coverage for the final execution set, including the resolved
supervisor and justified runtime-required identities, and rejects surplus new
exact assignments before payload release. Only previously execution-admitted
assignments can have retained dormant authority.
Both reject insufficiency without inventing assignments or remapping retained
ones. B3 validates the supplied numeric reservations and retained-state
consistency; B staging does not claim that a complete execution map exists.

The table order is one valid delivery order. The prerequisite column records
technical dependencies, not artificial dependencies merely to serialize unrelated
work. For example secret references do not require restore, and an independent
Postfix qualification does not require the shared-interface campaign.


## Full-campaign architecture and contracts — approved 2026-10-26

The maintainer approved this architecture for the complete campaign, including
delivered A–C and remaining D–K, after design review on 2026-10-26. The approval
is recorded in the completed architecture run
`run_23t2akmygv0s75eg00cqk8z5wh` against document SHA-256
`2f03ffd8b182c7ca6fb59923bf4e53f16d1d53b4eddbd13ca9c9a97d407fa0d1`.
This update incorporates the subsequent request to make group LLD part of
delivery. It does not authorize implementation or weaken the
[security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md). Later sections retain
their dated B/C specification and evidence; this section supplies the approved
common contract for the remaining groups, not a second runtime authority.

Architecture approval settles the intended behavior, authority, defaults and
failure rules below. Later LLD selects algorithms, exact internal types and
bounded implementation slices. It must demonstrate conformance rather than
reopen settled choices. A change to public input, authority, defaults or scope
returns for maintainer approval. A failed mechanism proof returns to design;
it cannot be closed by weakening the claim or substituting another profile.

### Scope and trust boundary

The initial cohort is one non-root operator's staged workload or user-scope
installation on an eligible local rootless Linux Podman engine. Profiles are
explicit `exact` or `bounded`; neither is the default or a fallback. Docker,
remote engines, Podman Machine, rootful mapped workloads and system-scope mapped
installation reject before workload effects. Ordinary single-identity behavior
retains its existing backend-independent policy. Native Podman backend work,
host-aligned identities, general composition, host enrollment, automatic
allocation and a system-wide installation registry remain separate features.

The administrator coordinates host-ID reservations across installations,
retained data and unmanaged services. Reploy serializes and checks one
installation's own authority. Host snapshots and installation locks are not a
global allocator or an atomic lock over administrator changes. Operators must
stop and fence affected workloads before changing delegation or reservations.
Every new controlled execution admission still refreshes the required host
inventory and delegation checks; earlier success is never a cached permission.

The host operator, Reploy implementation and accepted blueprint are trusted.
Application code, application-controlled data and ordinary image hooks are
untrusted. Names in the image prove consistency, not permission. The engine
socket and supervisor control channel are never application resources.

### Components and authority ownership

| Component | Owns | Must not do |
| --- | --- | --- |
| Existing blueprint/build pipeline | Portable declarations, immutable build/image evidence and accepted command definitions | Embed host mappings or resolved secret values in portable image authority |
| Existing deployment transaction | Installation ID, accepted and dormant assignments, generation/revision, pending operation and exact resource handles | Adopt a copied installation or release ambiguous retained ownership |
| Existing host/geometry validator | Fresh delegation/principal checks, stable interval assignments and final active-map coverage | Allocate free ranges, repair host accounts or infer global exclusivity from a local lock |
| Existing pinned runtime adapter | Positive engine/peer identity, bounded compatible requests and owned-object observation | Fall through to same-ID/raw exec or silently switch engines/profiles |
| Existing host operation/service runner | Fresh admission for explicit starts/entries, foreground monitoring, exact resource ownership and restart fencing | Add a persistent staged broker or grant general host service authority |
| Reploy-owned in-container identity supervisor | Release approved commands under their accepted identities; retain only explicitly approved control-plane capabilities | Execute application code in its own authority or accept arbitrary identity/capability requests |
| Trusted inert-object preparer | Create/check declared managed objects while unattached under the exact operation authority | Run image hooks, follow untrusted links or recursively repair application data |

Host admission belongs to the current CLI operation or existing foreground
run/session/user-service process. It is not a new daemon, enrollment service,
allocation registry or persistent staged broker. Detached staging may outlive
its initiating CLI after a successful start, but the container cannot then
autonomously select or relaunch privileged roles. Existing host controlled-session
supervision is useful lifecycle context; it is not already the in-container
identity supervisor. The private probe is evidence, not production code to copy.

The proposed initial supervisor is Reploy-owned and bound to the immutable
runtime image. An image-provided master does not silently become trusted.
Supporting one requires a separately reviewed, image-locked implementation of
the same supervisor contract and explicit maintainer approval. Stock mail
compatibility therefore remains a real gate rather than an assumption.

### Shared records and handoffs

These are logical contracts; reuse and extend existing records rather than
create a parallel state store or admission cache.

| Record | Required binding | Producer and consumer |
| --- | --- | --- |
| Locked identity policy | Original blueprint/build identity, immutable image, application names/numbers/groups, command-role assignments, supervisor identity and capability policy | D produces; E–K consume |
| Retained mapping authority | Existing installation owner, operator account, profile, revision, stable accepted/dormant assignments and retained ownership | B owns; C–K preserve |
| Invocation execution plan | Exact installation, selected generation, mapping revision, operation ID, current host evidence, pinned endpoint, immutable image, active maps and sandbox/role policy | C/D/E assemble; F/G validate before effects |
| Operation resource evidence | Attempt identity plus exact container/network/storage/service/interface handles and completed effect phases | Each effect owner records through the existing pending operation; G/H reconcile |
| Runtime admission result | Positive owned-object identity, complete effective maps/namespaces, verified supervisor boundary and a bounded authorization for the selected entry | E/F/G produce; supervisor may consume only for that entry |

An execution plan is immutable input for one invocation, not durable permission.
Any identity, generation, revision, endpoint, host-evidence or operation mismatch
rejects dispatch. Record intended effects before issuing them. Record returned
handles before clearing inhibition. Lost responses retain an ambiguous operation
and its reservations. Recovery observes exact handles and phase; it does not
replay a payload, reinstall secrets or infer absence from an arbitrary error.

Only complete final acceptance records an admitted generation. Partial create,
successful image verification or a ready container alone cannot promote it.
No consumer may use B's reservation union as the exact active map: dormant
assignments remain reserved but inactive until their same numeric bindings are
explicitly reactivated.

### D contract: identities, roles and generation evidence

There is one portable identity policy for a mapped generation, beside the
existing deployment-only `identity_mapping` input. It declares application
users by both image-local name and numeric UID, groups by name and numeric GID,
each user's primary and supplementary groups, exactly one dedicated supervisor
UID/GID, and named execution roles. Every application entry resolves to one
role through its accepted executable/command definition. No entry inherits the
supervisor role or chooses a UID through an unvalidated argument/environment
value. Selecting the public field spelling and strict wire representation is
D's LLD; its semantics are fixed here and are subject to the same public-input
approval guardrail before shipping.

Each role references one accepted application identity and exact supplementary
group vector. Supervisor UID/GID are disjoint from application authority.
Role labels are selectors, not separate security principals. Roles sharing an
application UID share that identity's process and ownership authority. Secret
and interface grants must be consistent across those roles; a request for
asymmetric access rejects before effects. Different labels or group vectors
alone cannot establish confidentiality between processes sharing the same UID.
Stronger per-role isolation needs a separately proved and approved mechanism.
Runtime-required IDs are separately enumerated and justified; they grant no
application role. Container ID 0 required by the current compatible projection
is not implicitly an application or supervisor identity. Inactive image accounts
need no mapping merely because they exist in account files.

Application capability sets remain empty. Supervisor capabilities also default
to empty; selecting multiple identities does not add them implicitly. Explicit
supervisor opt-in is limited initially to the proved identity-transition needs
`CAP_SETUID`, `CAP_SETGID` and, only when required by the implementation,
`CAP_SETPCAP`. No implicit package-specific capability bundle is added. A request
that cannot perform its declared transitions with its accepted policy rejects
before launch; it does not inherit engine defaults. Broader application or
supervisor capabilities require a separate explicit security decision.

Image evidence is collected without executing image NSS, hooks or application
code. The initial supported image account source is bounded, regular immutable
account/group content. Ambiguous aliases, duplicate/conflicting name-number
relationships, unresolved required groups or an account source whose required
relationship cannot be proved offline reject. File extraction must be confined
and bounded; successful archive parsing is not permission to trust traversal.
Evidence binds the selected immutable generation, not a mutable tag or a host's
account lookup. Host mapping overrides do not rewrite image authority.

D4 checks all declared application identities against reserved assignments.
E1 separately checks the complete execution set, including supervisor and
runtime-required IDs, and rejects surplus new exact assignments. Updates may
change declarations, but do not remap retained numeric assignments, migrate
ownership or authorize a previously omitted role.

### E contract: execution and supervisor separation

All Reploy-controlled application paths use one admission boundary: initial
start, application command, test, shell, controlled session, lifecycle hook,
runtime exec and restart. An unsupported path
rejects before payload execution. An already unprivileged application may fork
or exec under its same confined authority; it cannot select a different role
or regain supervisor privileges through that operation.
Mapped application code executes only inside the admitted container. Existing
host-native command branches retain pre-effect rejection for mapped owners;
resolving an accepted role cannot authorize a host-native shortcut.

Before payload release, the host operation runner validates the selected operation,
host authority and immutable generation; the adapter creates an exact owned
container running only a trusted admission waiter. E verifies the complete
effective kernel maps, owner/peer and every required namespace. F supplies
storage/network admission. Only then may the trusted supervisor release an
accepted application entry. The waiter and supervisor never execute arbitrary
image entrypoints with setup authority.

The supervisor uses exactly its dedicated real, effective, saved and filesystem
UID/GID in every thread. Its supplementary groups are empty or only its
dedicated GID, it is non-dumpable while capability-bearing, and it occupies a
POSIX session distinct from every application child. Its control channel and
descriptors are private. The child receives only constructed standard streams
and explicitly declared application descriptors. Immediately before untrusted
exec, normalize all four child UID/GID fields and exact supplementary groups,
remove all child capability sets, close all other descriptors, apply the approved
seccomp/no-new-privileges policy and verify the final boundary. Credential
changes in a multithreaded implementation require a demonstrated safe launch
primitive; the private Go probe is not that demonstration.

The host operation authorizes each requested role entry against fresh host and
installation authority. Reuse the existing command/session transport to invoke
the immutable trusted launch boundary, binding the installation, generation,
mapping revision, exact owned container, role and attempt. This is operation
evidence, not a new bearer token, admission cache or always-connected replacement
protocol. Stale, disconnected or incomplete requests launch no application code;
ambiguous replies retain their operation and never authorize a blind replay.

Initial automatic replacement uses the whole-container lifecycle, not privileged
autonomous child restart. If a required long-lived workload child exits, the
supervisor stops its remaining workload children and exits. Completion of a
transient hook or explicit app command does not itself end the workload.
An installed user-service may apply its existing restart policy only by
re-entering host admission for a new instance. Detached staging requires an
explicit new host operation. No engine restart policy bypasses these checks.
Foreground/session ownership loss follows existing bounded stop/cleanup and
retains uncertainty when stopping cannot be proved. An already admitted detached
instance needs no continuously running host broker, but cannot launch a new
role without a new admitted host operation. A future privileged child-replacement
protocol needs separate architecture review, authority and liveness proof.

Application children cannot signal or inspect any supervisor thread, including
through the same-session `SIGCONT` exception, pidfds or process-memory APIs.
All-thread observations and positive syscall controls are required; exited or
missing targets are not denial evidence. E4 tests every supported entry, not
only the first container process.

### F contract: managed objects and network isolation

Initial workloads use immutable image content and declared Reploy-managed
storage only. A managed host-backed mount is admitted by its resource record
and preparation contract, not because its runtime type happens to be `bind`.
External user-selected binds, host devices, runtime sockets, privileged mode,
host namespaces and private-path exposure reject before workload mutation.

Storage declarations identify a managed root, relative object, kind, intended
application owner, mode and lifecycle class. Prepare new declared inert objects
or verify already recorded objects under the expected ownership. Resolve and
operate through held directory/object descriptors with no untrusted symlink,
mount crossing, hard-link alias or type substitution. Never traverse and
recursively chown application-controlled trees. An existing foreign or
inconsistent tree requires a separately checked offline migration; `force`
does not adopt it. Any ownership-setting helper has only its reviewed setup
authority inside the selected subordinate mapping and runs no application code.
It exits before payload admission.
Managed mounts and per-command mount overrides must preserve existing protected
image/provider paths and the immutable supervisor, helper and control paths.
An application-writable overlay cannot replace the trusted launch boundary.

Preparation, restore and attachment are mutually exclusive for the relevant
managed resources. Hold the existing installation operation lock and persist
restart inhibition before effects. Engine restart policy cannot independently
bypass this fence. A crash leaves the resource inhibited until recovery proves
the exact preparation/attachment state. A successful create or stopped PID
alone does not prove the storage is unattached.

Network policy retains existing public/local egress semantics, but does not
equate external egress permission with inter-installation peer access. Each
installation has distinct network/PID/IPC/mount namespaces and private mount
propagation. No peer-reachable attachment or shared network namespace is admitted.
F3/F4 must prove existing egress policy, declared endpoint publication and exact
cleanup on the selected engine, including pre-start effective observation.
Missing compatible metadata is an unresolved transport proof, not permission
to start and inspect later. J may add only its explicit narrow interface rights.

### G contract: activation, restart and revocation

The existing host operation or foreground user-service runner owns application
admission and the permitted whole-container restart chain. User-service startup re-enters the normal host authority and
generation checks under the selected OS user. Reuse the existing user-service
lifecycle; do not delegate autonomous workload restart to an engine policy
that can bypass Reploy. Non-root foreground staging/run/test follows the same
admission. System units and root-run mapped operations remain ineligible.

Installing from staging creates a separate destination mapping owner with an
explicit independently validated mapping decision. It copies portable build
evidence as permitted, never source reservations, pending operations, admission
results, managed ownership or secret values. Validate destination/source retained
overlap before effects. Moving mapped test data uses H's explicit offline
translation boundary, not the ordinary install path. Test environments remain
valid independent owners.

Public mapped execution stays inhibited until G3 closes `generic-launch` for
both profiles through supported interfaces. Until then E/F may use private
integration seams with disposable trusted fixtures; no intermediate public
command may fall back to the ordinary path.

Retain the existing external `down`, `starting`, `up`, `stopping` lifecycle.
`up` requires completed startup, including its required hooks/readiness, rather
than only a running container. Repeated `up` on the same accepted live instance
does not repeat initialization or secret delivery. Failed new startup enters
`stopping`; report `down` only after stopping is proved. If stop cannot be
proved, retain the uncertain operation and inhibition rather than report success.
Shutdown hooks run only through their accepted application roles; security
revocation may suppress hooks to prevent further untrusted execution.

Revocation first inhibits new admission and all restart owners, then stops and
removes exact owned runtime resources and verifies absence. Report success only
after the restart fence and resource absence are proved. Retain mappings and
storage. Lost ownership evidence blocks cleanup; it never permits deleting a
foreign object or releasing IDs. External administrator changes are not an
automatic successful Reploy revocation transaction.

### H contract: ownership lifetime, export and restore

Ordinary stop/start, recreation, generation update and rollback preserve the
installation's established numeric associations. Identity removal makes an
exact assignment dormant; reusing that numeric ID or rolling back restores its
original binding and Unix ownership access. Account names do not revoke old
numeric ownership. Profile, supervisor identity or bounded geometry changes
reject as ordinary updates and require explicit migration or replacement.

Removal distinguishes runtime teardown from managed-data deletion. Retained
data keeps assignments reserved. Checked reservation release requires exact
absence of owned runtime, restart owners, attachments and retained managed
storage; ambiguous cleanup retains inhibition and reservations. Manually
deleting a deployment directory does not prove safe release. A future central
installation log may help discovery/recovery; it is not a dependency here.

Export/restore is offline and exclusive. The archive includes application data,
portable generation evidence, declared object metadata and original logical
container ownership, plus an integrity manifest. It excludes live sockets,
runtime/control handles, admission results, source-owner authority and
Reploy-managed ephemeral secret delivery. Reploy does not serialize resolved
secret values into the archive metadata. The archive itself is data, not a destination mapping reservation or
permission to start a workload.
Application-controlled data may contain values copied from a delivered secret.
Reploy cannot prove their absence and does not silently redact application
backups. Treat exports as sensitive and protect their operator-owned files.

Before writing archived ownership, validate the destination owner, selected
profile, complete UID/GID translation and fresh exclusive host delegation.
An offline restore into the same existing bound owner retains that owner's
unchanged accepted associations; it cannot adopt an owner from archive metadata
or change its profile/supervisor/geometry. Import into an independent destination
requires an explicit new destination mapping, non-overlapping with every other
live or retained owner, including the retained source. Both paths retain their
own existing authoritative state and the same atomic data-publication contract.
Either prove the same numeric mapping safe for the selected owner or require
explicit checked translation into the independently accepted destination; never silently
preserve unsafe source host IDs. Reject unmapped/foreign owners, traversal,
unexpected object types and unsafe links. Materialize in collision-safe private
staging with descriptor-safe inert operations; no archive code or hooks run
with preparation authority. Publish only a verified complete restore under
the existing transaction. Failure leaves accepted destination data/state
unchanged and retains any uncertain candidate ownership for recovery. Restore
does not start the workload until normal G admission succeeds.

### I contract: recipient secret files

Portable policy carries named references and explicit recipient roles, not
secret values. Resolve only explicitly selected operator-owned sources under
the current installation authority. Initial sources are local private files;
no implicit environment search, network vault, global configuration or credential
discovery is added. Source selection is deployment input and does not become
portable build content.
Resolve recipient roles to their accepted application identities before access
validation. Distinct role names using the same UID cannot be promised different
secret access; inconsistent declarations reject rather than silently broadening
the recipient set. Use distinct admitted identities when different access is needed.

Deliver values as recipient-confined regular files in ephemeral managed storage,
with declared ownership/modes and no parent traversal or peer access. Reploy must
not put resolved values in its command arguments, image layers, state, engine
labels, logs, diagnostic dumps or export metadata. Omit its ephemeral delivery
storage from exports. This does not promise to redact values that an authorized
application copies into its own data or output. Unrelated roles and the application environment
receive no implicit copy. Supervisor/preparer access is limited to their trusted
delivery duty; application children cannot access its control descriptors.

Bind delivery to a new accepted runtime instance. A repeated `up` or ambiguous
startup retry never resends initialization secrets to the same instance.
Replacement after confirmed teardown obtains new instance authority. Rotation
is explicit and initially uses stop/recreate/admit with an atomic new secret
set; no implicit live reload or partial file replacement. Failure retains the
previous accepted reference set, reports the failed instance state accurately,
and requires an explicit operation to retry or roll back. Old ephemeral values
are removed only through exact owned cleanup. Export never acts as secret backup.

### J contract: explicit shared interfaces

The initial interface is a named filesystem Unix-stream socket with one producer
and explicit consumer installation/role identities. General shared directories,
abstract sockets, shared network namespaces and peer TCP/UDP attachment are
outside this contract. Membership in a future composition grants no interface
right. The owner and each participant must explicitly accept the same interface
revision; a matching human-readable name alone cannot identify a resource.
All initial participants use the same eligible operating account, host and
positively identified pinned local engine, with independent disjoint private
mappings. Cross-user, cross-engine and cross-host grants reject before any
publication; later support needs a separate architecture and authority design.

Each interface has a dedicated Reploy-managed runtime path, producer identity,
consumer allowlist, generation/mapping bindings, exact object identity and
pending/active/revoked lifecycle. Grant only the declared connection right and
path. Preserve disjoint private mappings; the implementation may not expose
an entire participant map or silently add overlapping shared host assignments.
Required access must be independently enforceable for each accepted role,
including denial to unauthorized identities inside a participating workload.
Roles resolve to the same accepted-identity boundary as D/I. All roles sharing
an application UID must have consistent interface grants; a role label cannot
deny a socket to another process with the same UID. Negative conformance cases
use genuinely unauthorized identities, not renamed copies of an authorized UID.

J1 must select and prove a narrow mechanism satisfying that access contract in
a disposable fixture before attachment work. The architecture does not claim
that ordinary mode bits or a shared group already solve it. A failed proof
keeps sharing disabled and returns the mechanism to design; no proxy, ACL,
extra identity mapping or privileged host broker is silently substituted.

Reuse each installation's own operation state and deterministically ordered
participant locks for an explicit grant operation. Partial publication leaves
all affected attachments inhibited; no global service registry or multi-service
transaction manager is introduced. Reconcile a dedicated socket path without
walking application data. Never delete a live or foreign socket by pathname
alone. Producer restart requires a new proved object and matching participant
admission. Revocation must invalidate existing access, not merely unlink a name:
if open connections cannot be revoked safely, stop/recreate affected owned
participants before reporting success. Retain reservations and participant
authority until exact teardown is proved.

### K contract: qualification and release claims

Use supported Reploy operations on disposable installations to close every
security-contract acceptance row. Record exact Reploy/image/engine/runtime/host
identities, both mapping profiles, fresh role admission, positive controls,
negative isolation tests, lifecycle ownership and exact cleanup. Private probe
success and ordinary same-ID Podman hotfixes remain separate evidence.

K1 may qualify capability-free Redis/Rspamd independently. K2/K3 require a
proved stock-mail supervisor/child split and image-locked role/capability
policy; naming a stock master as trusted does not supply that proof. Any
additional trusted supervisor implementation or broader capability request
returns for explicit design approval before implementation. K4 closes the
combined lifecycle/shared-interface evidence. A report of blockers is useful
but is not completion. Resource-limit design remains outside this identity
campaign and inherits ordinary runtime policy without weakening it.

### Cross-group failure rules and acceptance ownership

| Boundary | Required failure result |
| --- | --- |
| D to E | Missing/changed image identity or incomplete role/map coverage rejects before create or payload release; no inferred identities |
| E to F | An unproved waiter/supervisor boundary cannot receive application code or storage-preparation authority |
| F to G | Unproved ownership, attachment exclusion or effective network policy leaves activation inhibited |
| G to H | Stop/removal uncertainty retains reservations and prevents destructive ownership operations |
| H to G | Restored bytes are not runnable until destination authority and ordinary startup admission both pass |
| I to G | Missing private source or incomplete recipient installation fails startup without publishing values or resending to an ambiguous instance |
| J to G/H | Incomplete grant/revocation blocks attachment/restart/release for the affected resource rather than granting partial shared access |
| Every group to K | Passing a slice's tests does not close a contract row without the required supported-interface evidence |

The acceptance-ownership table below remains the complete 19-row responsibility
map. ID/CAP rows are owned by D/E plus generic launch; FS/SB/network rows by F;
allocation/revocation and initial supported activation by B/G/H; lifecycle and
restore by H; interface rows by J/K. I preserves all relevant confinement and
lifecycle rows even though the original ledger has no separate secret row.

Design reviews must check both profiles, ordinary absent-policy compatibility,
every process-entry path, state/effect crash boundaries, dormant ownership,
separate staging/destination owners, and every cross-group handoff above.
Evidence gates remain independent from design approval. Completing this section
does not claim the supervisor, storage, network, restore or interface mechanisms
have passed. Their owning LLD must provide the exact proof plan before code.

The existing 34 remaining slice estimates are conditional. The explicit
host-operation/supervisor launch boundary, exclusive storage and interface obligations
may require decimal splits, especially E2/E3, F2 and J1/J3. Re-estimate against
the selected LLD before implementation; do not force a new control plane into
an old estimate or exceed the 750-production-line bound. This design pass
does not manufacture an actual LOC forecast from unimplemented mechanisms.

## B-design specification (delivered)

The following section retains the reviewed B specification and its original
proposal wording. B1–B5 are now landed; C does not reopen their approved scope.
Public mapped payload execution remains inhibited.

### Boundary and operator input

Reuse one existing deployment directory, its ordinary blueprint/build route,
its state file and operation lock. Declare mappings in the blueprint and apply
deployment overrides before a mapping-dependent operation needs their values.
Blueprint defaults are requests, not host authority: images and blueprint authors
cannot grant themselves host IDs. Explicit operator configuration, fresh delegation
and collision checks, and the selected deployment's retained state grant admission.
No service list, child-blueprint resolver,
composition generation, batch-start operation or composition lock is introduced.
An explicitly selected staged test deployment or installed destination can own
its own mappings and reservations. Configuration binds them to that deployment;
input cannot create or adopt an unrelated owner. Reusing a stage as build input
does not transfer its runtime or mapping authority to another destination.

The proposed optional blueprint field is `environment.runtime.identity_mapping`,
containing one deployment's declaration. Omitting it preserves ordinary behavior
only when there is also no retained identity record. Removing the declaration
from an existing mapped owner cannot remove its opt-in obligations or reservations.
Its presence is an explicit multi-identity request, including when some values are
unresolved. No `???` means automatic selection, default ranges or permission to
run as an ordinary workload. The public field spelling remains a proposal under
the accepted blueprint-plus-override direction.

`identity_mapping` has exactly `schema`, `profile`, `uids` and `gids`;
`schema` is the integer `1`. `profile` is required and
is either `exact` or `bounded`. Each nonempty map array contains objects with
exactly `container_start`, `host_start` and `count`. After resolution, all three
must be YAML integers, not numeric strings, booleans or floats. Starts are nonnegative, count is positive,
and every resulting ID must fit the runtime's usable unsigned 32-bit ID space
excluding its unchanged-ID sentinel. Check end arithmetic before addition.
Reject duplicate YAML keys, unknown fields, nulls and overlapping intervals
and unsupported schema versions. Canonical resolved maps sort by container ID;
input order does not change authority. There are no implicit mapping entries,
profile defaults, implicit shell-environment reads or auto-selected free ranges.

### Required values and deployment overrides

The exact whole scalar `"???"` means required but unset. It is distinct from
null, an omitted optional field, zero and an empty string. Quoting does not escape
the marker. In this first use, the new mapping subtree accepts it in scalar
value positions (`profile`, `container_start`, `host_start`, `count`), not keys,
`schema`, structural objects or arrays. Existing ordinary string fields retain
their meaning, including a literal `???`; this work does not reserve that spelling
across the whole blueprint schema. This is a first bounded use of required values,
not a separate configuration language or a claim of full OmegaConf compatibility.

Use the existing blueprint syntax and `{{ variable }}` interpolation where a
mapping scalar references a blueprint variable. A whole reference preserves the
variable's type; a string containing a numeric rendering is not an integer.
If the referenced value is `???`, retain its required-but-unset status for that
mapping consumer. Do not propagate the marker as literal text or replace it with
zero. Existing interpolation/type rules for other fields remain unchanged.
No new interpolation namespace or override-variable shadowing is introduced.

Extend the existing sparse `overrides.yaml` shape with
`environment.runtime.identity_mapping`. Its `environment.id` must match the
selected deployment. Within this new section, omitted fields inherit blueprint
intent; supplied scalar fields replace their blueprint values, and `uids`/`gids`
replace their entire respective arrays. Never merge arrays by position or append
entries implicitly. A supplied array must contain complete interval objects.
Unknown/duplicate fields, null deletion, wrong versions and overrides without a
blueprint mapping declaration reject. This is a typed extension for mapping
inputs, not a generic YAML deep merge. Existing base/package choices and sidecar
`environment.vars` keep their current independent meanings; sidecar variables
do not replace blueprint variables or change the mapping authority.

Apply a selected mapping override before resolving mapping expressions or testing
for missing values. A complete override can replace a missing blueprint leaf
without evaluating that leaf. Validate the resulting value against its schema;
no string-to-ID coercion is allowed. Structural decoding always rejects malformed
input and known invalid literal geometry. Portable loading/staging/building can
retain unresolved mapping leaves when their consumers do not need them. Before
any operation that requires a map, collect unresolved field paths and fail before
mapping-state publication, build/runtime work undertaken on behalf of that
map-dependent operation, ownership changes or payload execution. An unresolved
mapping never becomes a candidate reservation. Errors identify the deployment,
field path and required override, without printing unrelated private values.

Reuse existing explicit deployment and sidecar selection. Blueprint-adjacent
local sidecar import remains limited to the existing new-stage route; remote
blueprint references import no local sidecar, and stage updates do not overwrite
existing deployment choices. Stage/build may record desired unresolved input but
cannot allocate IDs or admit a workload. Installed operations must use explicit
destination input or the destination's recorded state, never discover a source
sidecar or read the original source checkout. No dedicated mapping file is
required. An explicitly selected override file may use the same sparse shape;
there is no directory search, implicit process-environment input or global config.

The original blueprint declaration and immutable image/build evidence retain
their existing integrity checks. Preserve the symbolic declaration in the normal
blueprint representation; do not substitute deployment host IDs into the stored
portable blueprint or weaken canonical-byte/tamper checks. Original declaration
changes may invalidate normal blueprint/build identity conservatively. Effective
host values from deployment overrides belong in the candidate/accepted mapping
state and do not create a second image lock or require a rebuild by themselves.
B2 must exercise the existing source/resolved blueprint round trips and build
validation, not merely a new standalone YAML parser. The current code does not
yet implement these extensions: strict blueprint and package-sidecar decoders,
numeric syntax and early global-variable expansion need explicit adaptation.

Safe intermediate delivery is mandatory. B2.1 ships declaration-based inhibition
in the same change that first lets public stage/build persist a mapping declaration.
Every payload-capable reader must check it before control-marker publication,
install-triggered build/preparation, lifecycle/application hooks, container creation
or payload exec. A successful blueprint decode is not admission. Cover the actual
`current_workload_run.go`, `current_runtime_test_v1.go`,
`current_app_command_run.go`, `current_shell_run.go` (`RunCurrentShellV1`),
`installed_service_container.go` (`RunInstalledServiceContainerV1`),
controlled-session and `provider_install_run.go` routes, including aliases,
shell wait and installed-service start/restart, and native/deployed command branches.
Read-only inspection
and existing exact-owned cleanup without application hooks may remain available;
neither can become an execution bypass. Tests invoke the real entry points with
both complete and unresolved declarations, assert no effectful callbacks or
admission-state changes, and include ordinary absent-declaration positive controls
and portable stage/build controls. Do not ship a publicly accepted declaration
while these guards are deferred to B5. When B4 first introduces retained identity
state, extend the same guard immediately to declaration OR identity state, and
test declaration removal plus retained/pending/revoked records at every entry.
B5 reuses and tests this boundary; it does not introduce it. C–G may replace
inhibition only with their complete reviewed admission path at generic-launch.

Read each selected input once into an immutable operation snapshot. Bind the
selected blueprint/generation, normalized effective input and expected mapping
revision to the pending candidate under the existing lock. Concurrent edits to
input files cannot retarget an operation midway; a later operation rereads input
and performs normal revision checks. A changed desired mapping never overrides
accepted or uncertain reservations. Check/status distinguish desired missing or
conflicting input from recorded candidate/accepted/retained authority. Existing
runtime restart uses recorded accepted maps plus fresh admission, not a mutable
sidecar. Removing overrides or introducing `???` cannot erase stored reservations.

Range length does not authorize work proportional to the represented ID count.
Validate interval arithmetic and delegated containment before account/group
inventory queries. Compare intervals with complete delegation and principal
inventories; do not expand a range into individual IDs or issue a principal
lookup for each mapped ID. Validation work scales with the explicit intervals
and inventory records, not the numeric span. Bulk reads/queries must use bounded,
cancelable execution and reject on timeout, output bounds or incomplete evidence.
This avoids a new public range cap while preventing per-ID amplification.

For `exact`, these intervals describe explicit reserved assignments. B3 checks
their geometry and retained-state consistency. D4 checks assignment coverage
against the image-locked declared identities; E1 checks the complete resolved
execution set and derives its active map. Before execution admission, E1 also
rejects every supplied exact assignment that is neither in that resolved set
nor a previously execution-admitted dormant assignment. D4 cannot make this
final decision before supervisor/runtime identities are resolved. A never-admitted
candidate entry cannot become dormant authority merely because it was staged.
A failed admission preserves the accepted generation and leaves candidate
reservations subject to the existing conservative recovery/release rules.
Dormant assignments stay reserved but do not enter the active map. For `bounded`, each
ID kind has one fixed contiguous interval. Neither form supplies application
identity declarations or supervisor approval. B metadata configuration cannot execute it.

Example geometry only; these IDs are neither delegation evidence nor a recommended
size or an executable supervisor policy:

Blueprint fragment:

```yaml
environment:
  id: mail
  runtime:
    identity_mapping:
      schema: 1
      profile: bounded
      uids: [{container_start: 0, host_start: "???", count: 4096}]
      gids: [{container_start: 0, host_start: "???", count: 4096}]
```

Deployment override fragment; unrelated package/base choices are unchanged:

```yaml
environment:
  id: mail
  runtime:
    identity_mapping:
      uids: [{container_start: 0, host_start: 300000, count: 4096}]
      gids: [{container_start: 0, host_start: 400000, count: 4096}]
```

These are proposed extensions, not input accepted by the current release. The
sidecar decoder/editor must preserve this section when reading/writing unrelated
choices. A mapping-only sidecar must not need an artificial empty package mapping;
an omitted package section normalizes to existing empty intent for this new form,
while explicit null and malformed package data still reject. Existing sidecars
retain their present semantics.

Proposed mapping-only commands:

- `reploy identity check --dir DIR [--override FILE]`: read the selected deployment's
  blueprint plus selected deployment override, compare its effective mapping with
  retained authority and validate complete input; create nothing. Without an
  explicit override file, use only that deployment's existing sidecar when staged;
  installed inspection uses its recorded input/state.
- `reploy identity configure --dir DIR [--override FILE]`: validate and record a
  candidate mapping in an inactive selected staged or installed deployment under
  its deployment lock; never build, start, remap or chown.
- `reploy identity status --dir DIR`: read existing identity state without
  creating files; distinguish candidate, admitted, inhibited, revoked and retained.
- `reploy identity recover --dir DIR --operation ID`: reconcile only the recorded
  pending mapping operation and exact owned objects; never infer a replacement map.

The optional file supplies overrides, not a second standalone mapping schema.
`configure` with fully specified blueprint defaults still requires explicit
operator action; merely loading a blueprint cannot accept host assignments.
Installed configuration without a new override can only revalidate its recorded
mapping; first opt-in requires an explicit destination decision through G1.
All deployment paths are explicit. Missing/malformed selected deployments reject. No
`compose` command, aggregate lifecycle command or host `prepare` command is part
of B. Ordinary staging/building uses existing commands and immutable generation
references. A failed build does not replace the accepted runtime generation.
A candidate mapping is not execution admission. Configure operates only on an
inactive explicitly selected staged or installed deployment with proved ownership
and compatible mapping/storage history. Unknown live/retained inventory rejects
before metadata changes; an ordinary live deployment cannot be silently inhibited
or converted. Once generic-launch passes, existing stage/build/run/test paths
must support mapped staging with the same fresh admission and sandbox checks as
installed workloads. B's metadata tests alone do not establish that runtime support.

### Staged testing and installation boundary

A staged test deployment owns its explicit mappings. Ordinary stage updates,
build publication and repeated test runs preserve its owner ID, mappings,
reservations and pending operation under the existing deployment lock. Removal
must use the same safe-release accounting as installed workloads: deleting a
stage or container cannot erase a reservation while mapped data may remain.
Starting in B4, the first readable identity record must inhibit the actual
`removeStagedDeploymentV1` route, including `stage --remove --force`, before
control-marker publication, resource retirement, directory isolation or deletion.
Any retained identity record triggers this barrier, including pending, revoked
or dormant state and a removed blueprint declaration. B4 tests the real removal
entry points and proves that rejected attempts preserve the directory, identity
record, reservations and managed data without effectful callbacks. The guard
precedes publication recovery as well as ordinary removal admission. Recheck
under any reacquired lock before effects. B5 retests this barrier. Ordinary
staging without identity state keeps its existing removal
behavior. Passing `generic-launch` permits reviewed payload admission; it does
not remove this deletion barrier. Only H4's checked safe-release transaction may
replace it. Thus usable mapped staging does not depend on H4, but its directory
cannot be removed before checked release is available.
Copying a stage to another directory cannot create a second mapping owner;
copied owner/path bindings reject rather than silently rebinding or allocating IDs.

Directory deletion is not the only destructive authority transition. Starting in
B4, `ForceReplaceStagedDesiredStateV1` and its public cross-environment
`stage --force` route reject any retained identity record before pending
publication recovery, force admission, stopping work, owner retirement, state
publication or storage cleanup. Repeat the check under any reacquired lock.
Neither a fresh `StateV1` that drops reservations nor a copied record that lets
the replacement environment adopt them is permitted. The same-environment
update branch must preserve its own record, not treat it as a fresh owner.
B4 tests actual replacement, retry and lock-reacquisition paths with removed
declarations and pending/revoked/dormant state, asserting unchanged authority,
resources and data and no effectful callbacks. Ordinary cross-environment force
replacement remains a positive control. B5 retests this barrier; it survives
`generic-launch` and only H4's checked release may replace it.

The same B4 barrier covers installed directory removal, not only staged removal.
Guard `UninstallProviderV1`/`RunProviderUninstallV1`, the remove-directory backend,
and `retryPendingProviderUninstallRemovalV1` before publication recovery, terminal
removal resumption, control-marker publication, host cleanup or resource retirement.
Include `uninstall --remove-dir` with immediate, wait, drain and force admission,
and service-name aliases. A terminal retry is not an exemption. If the original
directory is absent, inspect the retained tombstone/control state under its lock;
missing original state cannot authorize pending-directory deletion or erase IDs.
The pending finalizer must require checked admission before moving control state
or deleting either directory. Unknown, foreign, malformed or conflicting retained
identity authority rejects without changing the retained state or resources.
B4 exercises the real public, terminal and pending-removal routes, including
lock reacquisition and interruptions before and after directory isolation. Tests
assert that rejected attempts preserve the original or retained directories,
mapping record, reservations and data, with no recovery, cleanup or retirement
callbacks. Include ordinary staged, installed and pending-removal positive controls.
B5 retests this complete removal family. This barrier also remains after
`generic-launch`; H4 alone may replace it with checked safe release. Non-removing
uninstall remains the separate retained-staging transaction described below.

G1 integrates installation from a mapped stage through the existing immutable
build route. The reviewed install adapter requires explicit destination mapping
input for first opt-in install. Under the destination lock, validate independent
non-overlapping assignments and durably record the destination owner plus proposed
installation facts in its pending intent before destination runtime, storage or
ownership effects. Installed-state publication atomically commits `Deployment`
and the matching active installation binding and clears `Staging`; until then the proposed binding
belongs only to the inhibited pending intent.
The source stays valid, unchanged and separately reserved. Do not inherit its
mapping ID, reservation history, pending operation, inhibition or owned resources.
No source-to-destination ownership transfer or cross-directory commit is needed.
The exact public install flags remain part of service-design.

Only immutable build/image authority crosses this boundary automatically. Existing
path-update/config/data transfer routes must reject mapped source storage that
would need ownership adoption or translation before destination writes. Do not
silently drop that data or copy its numeric owners. Such promotion requires the
separate checked export/restore design in H5–H7. A clean destination can prepare
its own managed objects using its own validated mappings. Test and production
may use the same image-local IDs while their subordinate host assignments differ.

On retry, the same destination mapping-owner ID and exact pending operation resume
with retained destination mappings. Updates preserve and revalidate the existing
destination record; any supplied map must agree with it. A different destination
requires its own explicit input and administrator-coordinated IDs. Source/destination
overlap is a known conflict and rejects even when external coordination is manual;
include accepted, pending and dormant source reservations. Hold both existing
operation locks, acquired in deterministic canonical-directory order rather than
source/destination order. All install paths that can contend for those locks,
including ordinary callers, must use the same pair order. Resolve both directory
identities before acquiring either; reject same-directory/aliasing/overlap and
recheck directory identities, selected target, source generation/mapping revision
and destination revision after both locks are held. If an earlier source-only
phase was needed to resolve the target, release it before ordered pair acquisition
and revalidate; do not upgrade it by waiting for a lower-ordered destination.
Keep both locks through validation and destination publication; neither mapping
can change during that boundary. Cancellation/error releases every acquired lock.
G1 proves reciprocal A-to-B/B-to-A operations and ordinary-versus-mapped contention
finish or reject without deadlock or ownership effects from stale observations.
This requires a bounded change to the current source-first acquisition path;
existing lock retention alone is not proof of safe ordering. Pending,
inhibited, revoked or unknown source authority rejects until reconciled. This is
not a transfer of source reservations or a global reservation guarantee.
An ambiguous destination operation retains its own inhibition
and reservations until exact recovery; it cannot release source reservations.

`PublishInstalledBuildV1`/`CommitInstalledStateV1` must construct the destination
identity record explicitly rather than copying it through `candidate := sourceState`.
Without the reviewed adapter, a mapped source rejects; source metadata must never
be silently dropped to produce an ordinary unmapped destination. Destination
updates preserve their own identity record. G1 acceptance includes mapped staging
run/test, installs into two independently mapped destinations, source preservation,
same-destination retry/update, missing explicit input, overlap, copied-owner rejection,
source-revision races, mapped-data transfer rejection and retained-state failure.
No automatic ordinary-data conversion or recursive ownership migration is added.

### State and transaction boundary

Extend existing `StateV1` with an optional `identity_mapping` record, persisted
in the existing 0600 `.reploy/state.json`. Ordinary behavior requires both absent
identity state and an absent blueprint mapping declaration. An unresolved declared
map is an inhibited opt-in request, not ordinary authority. Never infer opt-in
from an image label or backend. Existing strict readers reject the
new field instead of silently launching an opt-in installation as ordinary.
Use existing descriptor-safe state access, atomic file/directory sync and
`OperationLock`; the override sidecar is desired input, never committed mapping
authority. Do not create an independently committed mapping sidecar or
second generation authority. Preserve metadata ownership and permissions.

The proposed record has schema `identity-mapping-v1`, a stable mapping-owner
`installation_id`, canonical `owner_dir`, nullable `installation_binding`,
monotonic `revision`, numeric `operator_uid`/`operator_gid`,
explicit `profile`, `uid_assignments`, `gid_assignments`, retained dormant sets,
nullable accepted `supervisor`/`engine` bindings, nullable `admitted_generation`,
`retained_storage`, `revoked` and nullable `pending_operation`. Assignment fields
use the input interval geometry. Accepted supervisor/engine bindings are produced
only by E/G, not inferred in B. Generation references use existing immutable
build/runtime identities; no build-lock format is duplicated.

Under the existing deployment operation lock, compare the mapping installation
ID/revision and the existing generation before one atomic state replacement.
The current generation comparison alone is insufficient for mapping-only changes;
B4 must add the explicit mapping comparison. First configuration generates its
stable mapping-owner ID once under the lock, for either staged or installed
state. This ID is independent of the active service InstanceID and survives
uninstall. Derive staged/installed status from existing deployment facts rather
than adding a second owner-kind authority. When `Deployment` is present,
`installation_binding` equals its installation InstanceID; otherwise the binding
is null. All paths verify canonical owner directory, stable ID, revision and any
active binding; retries cannot allocate a new identity. Existing stage/build/overlay
and set/clear-installation writers preserve and compare the identity record
rather than rebuilding it from portable input. Relocation/cloning needs separate
design. Copied metadata is not a new safe installation. Read-only inspection uses
non-creating existing-state access and cannot repair authority.
All Reploy-issued lifecycle/session/control paths must honor opt-in state and
inhibition; until generic-launch, opt-in payload execution rejects on every path.
B2.1 already protects declarations. B4 must extend that common guard when this
record becomes readable, including when no declaration remains; it cannot wait
for B5 to prevent a state-only opt-in owner from executing as ordinary.

B4 must test the complete existing state-writer family, not only a new mapping
transaction. These responsibilities apply from its first retained record:

| Existing writer or entry | Required B4 behavior |
| --- | --- |
| `setDesiredStateV1`, platform/overlay updates, `PublishBuild` | Same-environment updates preserve the exact mapping owner, reservations and pending authority and compare the expected revision; fresh struct construction cannot drop them |
| `ForceReplaceStagedDesiredStateV1` and `stage --force` | Reject retained identity before recovery, admission, retirement, replacement or cleanup; do not copy it into another environment |
| `CommitStateV1`, installed-state set/ready/clear writers | Validate the selected local mapping authority and the intended transition under the held lock; stale or omitted authority cannot silently remove, replace or rebind it |
| `CommitInstalledStateV1` and `PublishInstalledBuildV1` | Keep destination authority independent of the source; until G1's adapter exists, reject mapped install before effects rather than copy or drop source metadata |
| Legacy-components recovery and its forced staging entry | Keep strict rejection of retained identity state before admission or retirement; no compatibility decoder may strip it to recover an owner as ordinary |
| Normal/terminal/pending directory removal | Apply the earlier before-effects barriers, including retained control/tombstone state and direct finalization |

Exercise these real entries and their error/retry branches. A failed final state
commit cannot undo earlier cleanup; a commit-time check alone is not sufficient
for an effectful caller. Existing locks and state remain the only transaction
authority. This work adds no migration or implicit mapping release.

### Uninstall with retained staging

The existing `executeProviderUninstallWithV1`/`ClearInstallationStateV1` route can
remove installed facts while leaving the same directory and managed data. It must
not remove or replace the mapping owner. B4 defines one atomic state transition
that clears `Deployment` and the active installation binding together, restores
the existing `StagingStateV1{Schema: StagingStateSchemaV1}` marker, and preserves
the mapping ID, canonical directory, assignments, dormant reservations and retained
ownership evidence, and compares the exact prior installation plus mapping revision.
Validate this candidate as a normal staged `StateV1`, including retained
`BlueprintSource`, before host-uninstall effects. If it cannot be constructed from
proved retained inputs, reject before cleanup rather than fabricate authority.
The returned deployment must pass existing staged readers unchanged: clearing
installed facts alone is insufficient. Readers must not invent a missing marker
or a new owner ID. The reverse installed publication atomically clears `Staging`
while recording `Deployment` and its matching active binding.

G2 records an uninstall intent with the exact installed binding and resource
handles before host effects. It persists `effects-pending` before service/runtime
removal. G3 recovery must prove all restart owners stopped and reconcile exact
effects before the atomic installed-to-staged publication. A crash before or after
that publication retains a valid old or new state; unknown effects keep admission
inhibited and reservations retained. Historical installed-resource ownership stays
in retained-object/tombstone and operation evidence; clearing the active binding
cannot erase that evidence or permit another installation to adopt those objects.

Later staged run/test uses the same owner and assignments, with fresh normal
admission after recovery. Installing into this retained destination again must
preserve its owner/mapping history and bind the reviewed active installation facts;
it cannot treat a missing `Deployment` as a fresh allocation. This uses the existing
supported source/destination install route, not a new in-place self-install command.
Any new explicit input must agree with protected mappings. Directory removal
remains blocked until safe release is proved; uninstall alone does not free IDs.
B4/B5 test the state/revision transition and injected crash boundaries. G2/G3/H4
test real uninstall, retained staged access, pending recovery, later reinstall and
final safe removal, with before/after owner, assignment and ownership checks.
Tests must use existing staged override/build validation, update/replacement and
removal entry points after retained uninstall, rather than inspecting metadata
alone. Include invalid/missing retained staged inputs and crash controls proving
no observable cleared-installation state lacks its required staging marker.

### Pending operations

A pending operation records `operation_id`, `kind`, expected revision, candidate
and prior accepted references, exact owned-object handles, `phase` and inhibition.
Phases are `intent`, `effects-pending`, `verified` and committed completion.
New proposed geometry is stored inline in that pending intent's candidate;
protected accepted `uid_assignments`/`gid_assignments` and their history are not
replaced merely by configure. Count both protected and pending geometry as
reserved. Before first acceptance, protected assignment sets are empty; the
pending candidate carries the required nonempty input maps. Configure returns
candidate/pending status, not execution admission.
Before issuing any mapped-ID ownership or runtime effect, durably advance to
`effects-pending` under the same lock. Thus `intent` proves no such request was
issued; a crash after advancement remains conservative even if the call never ran.
Verify exact effects before atomically accepting the result and clearing that operation. A crash or ambiguous
response leaves inhibition/reservations. A revoked installation stays blocked.
Recovery cannot adopt foreign objects, assume cleanup succeeded or release IDs
because a container disappeared. Retained storage keeps its reservation.
B5 proves metadata/recovery for both staged and installed owners with injected
effects; G/H prove actual runtime and storage effects, including staging cleanup.
Pure validation failure leaves accepted state/data unchanged.
A post-intent failure reports the pending operation and its conservative state.

### Correcting an unused candidate

An explicit `identity configure` with corrected input may replace an existing
candidate only while its exact pending operation remains in `intent`, its
installation/revision still match, and current owned-resource/storage evidence
proves no proposed assignment was used. Preserve the complete protected accepted
assignment set and profile/geometry obligations. Atomically replace only the
unused pending candidate and operation ID/revision; release only its never-used,
unaccepted reservations. This is the correction path for an E1 surplus rejection
before any effects, not an omission-based release of retained assignments.

`identity recover --operation ID` may instead cancel that same proved unused
intent, preserving accepted authority and clearing only its candidate. Missing or
ambiguous history/inventory, any `effects-pending` transition, active preparation,
admitted use or retained ownership forbids this fast cancellation/replacement.
Those cases keep inhibition/reservations and require the existing exact effect
reconciliation and safe-release rules in G/H. Never infer no use from container
absence alone. B5 tests surplus correction, cancellation, stale revision, crashes
before/after phase advancement and preservation of accepted/used/unknown authority.

### Separate composition feature

General composition needs its own design and estimates: service membership,
blueprint references, aggregate staging/generation publication, ordering and
partial-start behavior. None is required to configure one installation's map.
A later composition can call the same installation operation with explicit input;
it must preserve that installation's accepted mappings and ownership obligations.
No composition implementation or public API is authorized by this plan.

### Mapping admission and lifecycle invariants

The proposed initial B-design host contract is local rootless Linux Podman with
existing subordinate delegation from `/etc/subuid` and `/etc/subgid`. Require
`/etc/nsswitch.conf` to select only `files` for `subid`, or to have no `subid`
entry while the active libsubid result agrees with those files. Require
`getsubids` for both UID and GID delegation checks. Other subordinate-ID
backends are unsupported initially; B1 must reject them before mutation rather
than infer their complete inventory from a partial file view. This is the
recommended concrete host policy for maintainer approval, not an approved
expansion to other backends.

B1 validates the real/effective operating account, canonical account ownership,
the complete files-backed delegation inventory, agreement with active libsubid,
and proposed host intervals against local/NSS principals and other principals'
delegation. Require one complete authoritative bulk UID/GID principal inventory
per admission, with a supported completeness proof for every configured
account/group source. A files-backed principal source can be read and validated
once; another source requires equivalent authoritative bulk evidence. A generic
`getent` enumeration or an empty response alone does not establish completeness.
Unsupported, unavailable, ambiguous or partial inventory rejects; there is no
per-ID query fallback or new general NSS-provider framework. Do not treat a
lookup failure as an unused ID. The operating
account and delegation must already exist; Reploy does not change accounts,
subordinate-ID files or NSS configuration to satisfy this contract.

For the proposed initial cohort, caller, deployment owner, rootless engine account
and user-service runtime account must be the same non-root numeric UID/GID. Stored
`operator_uid`/`operator_gid` bind that account; they are not proof about an unrelated
daemon or restart owner. C1/G1 must positively identify the selected local rootless
engine and its operating account and pin its endpoint. Configure/admission rejects
root, sudo-mediated account substitution, a different engine account, and installed
`scope: system` before mapped workload mutation. Every start, restart, recreation,
exec and ownership-preparation path rechecks this binding and delegated authority.
Unproved endpoint/runtime-account evidence rejects rather than trusting the caller
or a socket path alone. Existing system installation resolves an application account
separately but its systemd unit runs as root; that account is not a rootless engine
identity. Supporting that route needs a separately reviewed runtime/service-account
design and qualification. No automatic account switching or system-unit rewrite
enters B. B1/G1 tests include root/system-scope rejection and caller/engine/service
UID/GID mismatches with unchanged workload state.
B1 owns the caller/deployment account preflight. C1/G1 own actual engine and
service-account proof; B's metadata tests cannot establish that runtime evidence.

B3 checks profile geometry and disjoint active, dormant and retained assignments
within the installation. UID and GID collision domains are checked independently.
Every host ID must lie within the selected account's exclusive delegation; host
root and named host accounts are forbidden. Coordination across installations is
an administrator obligation, not a safety claim made by this local lock.
Immediately before any mapped-ID object creation or ownership change, reuse
B1's complete bounded delegation/principal refresh and interval collision checks.
This includes declared-object preparation in F1/F2, install preparation in G1,
and restore/translation writes in H. Earlier configuration, execution or planning
evidence cannot authorize the write. Incomplete/changed authority rejects before
mutation under the existing exclusive-preparation boundary. No ownership write
uses rejected authority. If a later check fails after earlier authorised writes,
retain those recorded effects and inhibition for exact recovery; do not claim
atomic rollback of a multi-object preparation. F1/F2 test account/delegation changes
between configuration and preparation, with unchanged objects on rejection.

Every execution admission, including start, restart, recreation and runtime
exec, also refreshes delegation, engine identity and the complete authoritative bulk
UID/GID principal inventory. Rerun the interval collision checks and reject
unavailable, incomplete or conflicting evidence before payload release; staging
or earlier execution evidence cannot substitute. Verify effective kernel maps
before release as well. If host delegation changes
concurrently, Reploy cannot prevent that administrator action; it must reject
known disagreement and never report revocation complete while resources remain.

A later generation can add an exact identity only with an explicit unused
assignment. Removing it makes the assignment dormant. Rollback reactivates that
same numeric assignment. Bounded geometry, profile and supervisor identity stay
fixed. D4 rejects missing assignments for locked declared identities; E1 also
rejects missing supervisor/runtime-required assignments before execution.
B3 rejects changes to retained assignments without needing those later identity
sets. No
ordinary update scans or recursively chowns application data. Existing ordinary
same-ID/keep-id handling is not the subordinate multi-identity mapping policy.

### Historical B runtime scope and unresolved decisions

This subsection records the B checkpoint before C implementation. C1/C2 now
provide the internal integration described above; its failed sparse candidate
is historical evidence, not the delivered compatible request. The proposed
full-campaign architecture supplies the current cross-group design for review.

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

Before product implementation, approve B's concrete single-installation mapping contract.
Before D/E, approve identity/capability syntax and supervisor authority. Before
F/G, approve preparation/restart ownership and supported service behavior.
Mail compatibility is independent: the stock master process cannot silently
become either an ordinary capability-bearing child or the trusted supervisor.

## Approved C-design — scoped engine and image integration

This is the approved C checkpoint after landed A1 and B1–B5. It adds two
internal integration slices. It does not enable public mapped execution.
The security contract remains authoritative. No native backend, global
allocation service, account changes or general composition API is added.

### Operation boundary

| Operation | C disposition |
| --- | --- |
| Stage and explicit build | Preserve B input rules, including unresolved mapping leaves. Reuse current builders. |
| Mapping check/configure/status/recover | Preserve B authority, locks and conservative recovery. These operations grant no payload permission. |
| Mapped container create | Add the internal C1 seam below. Do not expose a new public command. |
| Mapped start/restart/recreate/app/run/test/session/exec or autonomous startup | Keep existing declaration-or-retained-state rejection until the later launch and lifecycle gates. |
| Ownership preparation, release or system-scope mapped install | Add no permission in C. Later storage and lifecycle slices own these operations. |

### C1 — scoped admission and compatible create

Admit only a positively identified local rootless Linux Podman engine over a
local Unix socket. Reuse `verifiedLocalDockerTargetV1`, endpoint selection and
endpoint pinning. Verify the actual connected peer UID/GID and the engine's
parent-namespace UID/GID maps. A socket pathname or caller identity alone is
not engine-account evidence. Each new connection must meet the same peer check;
all requests in the operation stay pinned to that endpoint.

Obtain rootless status and both parent maps through the existing bounded
`/v<positively-identified-Podman-version>/libpod/info` metadata request on that
same client. This reuses the ordinary same-ID adapter's narrow metadata read;
it is not a native create backend. Require explicit rootless status, canonical
nonoverlapping maps and exactly one container-ID-0 entry of size one binding
each parent domain to the verified operating account. Reject incomplete or
inconsistent version, peer or mapping evidence. Do not use an unscoped Podman
CLI, a default engine or a native-create fallback to fill missing evidence.

Reuse B's same-account policy for caller, selected deployment owner and rootless
engine. Reject Docker, rootful Podman, TCP/remote endpoints, Podman Machine,
root/sudo invocation, system-scope mapped use and inconsistent or unknown
identity evidence. These are mapped-operation restrictions; ordinary workloads
retain their existing behavior.

Read-only preparation runs under the existing deployment operation lock. Bind
the selected owner, expected mapping revision and generation, explicit active
geometry and exact operation ID. Compare its bindings with protected history
and the selected pending candidate. Preparation accepts no candidate, changes no
state and grants no public execution permission.

The trusted caller persists `BeginEffects` before preparing a dispatchable
request. Bind that returned state revision. Earlier inspection of an `intent`
cannot produce a dispatchable request; a phase transition requires fresh
preparation because it changes the state revision.

Before create dispatch, require the same owner/revision/generation and the trusted
caller's exact recorded operation in `effects-pending`, under the same lock.
The caller must first persist B's `BeginEffects` transition. Reject an unrelated,
stale or ambiguous pending operation; do not reject the correctly bound operation
merely because its effects are pending. C1 does not supply a lifecycle coordinator,
invent effect evidence, call `Recover`, clear inhibition or accept reservations.
G/H supply real effect owners before public use. Private C1 integration tests may
use B's injected effect-owner seam to establish their exact fixture intent; that
is not evidence of production lifecycle admission.

Refresh B1 `identitymapping.CheckHost` authority before each create. Keep complete
bulk principal inventory, delegation checks and interval collision checks. Do
not expand a range into per-ID lookups. Return an invocation-local prepared
request, not a persisted bearer token or a global cached permission. Ambiguous
create/cleanup keeps the recorded operation inhibited and its reservations held.

This C1 check does not establish a future service account's autonomous restart
admission. G must prove that separate lifecycle boundary.

### Explicit mapping compilation

The explicitly selected active geometry expresses actual host IDs. It is not
B's retained reservation union or `Status.Reservations`: those also protect
dormant history. Validate the active geometry and its stable bindings against
the selected owner and exact candidate before compiling it. For an exact map,
encode only the selected active identities. Keep dormant reservations unchanged
but unmapped unless current explicit input reactivates their same binding.
D/E still determine complete final execution identity coverage and admission.

Translate each active UID/GID interval through the verified rootless parent map
into the IDs expected by the engine's compatible create request. Validate both
parent maps for arithmetic
overflow, overlaps, ambiguous coverage and uncovered host IDs. Split an interval
at parent-map boundaries when needed. Use interval operations, not fixed host
offsets, a single-delegation assumption or enumeration of every ID.

Use the existing compatible create route with explicit `HostConfig.UsernsMode`:

```text
auto:size=1,uidmapping=CONTAINER:PARENT:COUNT,...,gidmapping=CONTAINER:PARENT:COUNT,...
```

Every mapping entry comes from the bound active geometry after translation.
Require both active UID and GID maps to cover container ID 0 explicitly. With that
condition, the explicit entries cover the whole `[0,1)` automatic range, so the
engine must add no automatic IDs. Reject a missing container-ID-0 assignment;
never insert a reservation or silently broaden either profile. Sparse exact
entries outside that range remain explicit. Bounded profiles carry their full
accepted intervals through the same encoding.

Do not substitute `keep-id`, unconstrained automatic allocation, inspect-shaped
`IDMappings` input or a native-create fallback. E1 still owns complete final
execution-identity coverage and surplus new exact-assignment rejection. C's
compiler cannot claim that a numeric map alone permits payload execution.

### Bounded transport and object binding

Refactor the narrow generated-create parser/client and its attempt ownership
rather than copy them or introduce a general Docker command parser. Preserve
immutable image selection, the fixed trusted entrypoint, read-only root,
namespaces, seccomp, dropped capabilities and no-new-privileges settings.
Explicit mapped requests must not fall through the ordinary same-ID adapter.

Use `/v1.44/containers/create` with no dynamic API fallback. Before any send,
limit the serialized outgoing create body to the existing 4 MiB
`imageHealthConfigLimit`. Retain the separate 64 KiB create-response limit,
bounded engine-info and inspect reads, and caller deadlines. Test the exact
request limit and oversize rejection before any HTTP effect. The current
response limit is not an outgoing-request bound.

A successful HTTP 201 and syntactically valid ID are insufficient. Before
returning a successful create handle or starting that object, inspect the exact
ID on the pinned endpoint. Require its ID, attempted name, random attempt label
and expected immutable image to agree. A substituted object must reject without
stopping or deleting a foreign container. Keep ambiguous attempt ownership for
reconciliation; never infer absence from a non-404 error.

Reuse exact-name/random-label uncertain-create recovery. Delete only a proved
owned object. A positive object-identity check does not replace kernel mapping
verification or payload admission.

### C1 acceptance

Prove exact and bounded requests through the implemented seam on an isolated
rootless endpoint. A fixture may start only the trusted admission waiter. Inspect
the live kernel UID/GID maps before releasing the test payload. Compare complete
maps, including unexpected entries. Stop and remove only exact owned resources.

Cover recreation, sparse holes, parent-boundary splits, missing container ID 0,
wrong/extra/missing mappings, changed or unavailable host authority, wrong peer,
engine/account/endpoint mismatch, cancellation, malformed/oversized responses,
valid-201/wrong-object responses and uncertain cleanup. Retain all B public guards.
Include exact current-operation versus unrelated pending-operation controls,
wrong-phase/stale-state dispatch rejection, unavailable or inconsistent pinned
parent metadata, dormant-but-unmapped exact identities and explicit stable
reactivation. Rejected cases must not accept a candidate, clear inhibition,
release reservations or dispatch an unrecorded effect.
A version number or successful create response is not general qualification.

The historical A1 `private` plus `HostConfig.IDMappings` rejection remains a
failed candidate. The explicit `auto:size=1` proposal is a different projection.

### C2 — reuse build and immutable image authority

Reuse invocation-scoped selection in `generated_builder.go`, existing endpoint
pinning and feature checks. Preserve Docker BuildKit and identified Podman's
compatible build route. Reuse current frontend/base identity, local-image access,
result loading and authoritative healthcheck behavior. Do not reintroduce the
obsolete shared-builder workaround or registry credential machinery.

A deployment mapping does not rewrite original portable blueprint/build
authority. Unresolved mapping leaves may pass ordinary portable stage/build
without establishing a mapped-runtime host admission. Foreign endpoint caches
or an earlier builder selection do not grant create admission.

For the internal mapped path, bind the verified generation reference to the
immutable local image ID at the C1 endpoint. Preserve mutable-tag race checks,
platform/config/build identity checks and missing-image rejection. Pass the
immutable ID to create. Reuse existing authority formats; add no durable service
activation or state schema merely to cache an admission result.

Later D/E/G admission must revalidate the actual selected generation. C2's image
binding does not replace locked identity evidence, supervisor admission or
service lifecycle checks.

Test ordinary Docker/Podman builder selection, absent and unresolved mappings,
immutable image binding, tag mutation, wrong platform/config/endpoint and missing
images. Keep C2 smaller if the existing path already meets its requirements.

### Size and delivery gates

| Slice | Production target | Production estimate | Test/fixture estimate |
| --- | ---: | ---: | ---: |
| C1 | 500 | 350–650 | 650–1,050 |
| C2 | 400 | 250–550 | 500–850 |
| Total | — | 600–1,200 | 1,150–1,900 |

The 750-production-LOC hard limit applies to each implementation PR, including
added or rewritten comments and blank lines. Split a larger slice rather than
use net deletions to conceal its size. Do not add code to meet a target.

Review this design locally and remotely. Resolve findings and obtain maintainer
approval before refreshing affected design stamps. Use the SWE stamp writer;
do not hand-edit clean attestations. Then checkpoint/publish/review the design,
deliver C1 followed by C2, report measured LOC and run evidence, and stop.
Commit and PR titles must contain `C-design`, `C1` or `C2` as applicable.

### Maintainer decision — 2026-10-25

The maintainer approved the reviewed C-design in draft PR #277 at
`80d08bd66f55168291a5462c8008334183f8a790` and added repair of the observed CI
failures to this milestone. This authorizes the stated C1/C2 design and bounded
CI repairs. It does not approve the PR, enable public mapped execution, or waive
any exact-head CI job. Refresh affected design stamps before implementation.

### Historical C-design recovery and evidence status — 2026-10-25

This draft is reconstructed from the retained session state after its uncommitted
`/tmp` checkout disappeared. It is not the original byte-identical reviewed draft.
The earlier local review reported two corrected transport issues: the outgoing
request bound and positive create-object binding. Both requirements appear above.
The reconstructed bytes required fresh review. At recovery, no C PR or clean
C stamp existed. The maintainer approval recorded above followed fresh local
and remote review; stamps and implementation remain separate delivery work.

Earlier feasibility runs reported exact and bounded maps, recreation and owned
cleanup on rootless Podman 4.9.3 through API routes 1.41 and 1.44. Their raw
requests, scripts and logs were lost with `/tmp`. Treat those observations as
historical reports, not reusable source-bound proof or qualification of 6.1.4.
A fresh four-trial API 1.44 proof has locally retained, untracked receipts, summarized in
[READINESS](../tools/conformance/multiidentity/READINESS.md#fresh-compatible-projection-proof--2026-10-25).
The raw receipts are not shipped in this PR. The proof establishes the private
projection only. This host does not satisfy production B
principal-source admission. No C1/C2 implementation or production-path negative
test had run at that recovery checkpoint; compatible-map-proof was still open.
Subsequent C1/C2 implementation and the retained C campaign close their internal
integration requirements. They do not close generic-launch, stock-engine
qualification or the later supervisor/storage/network evidence gates.

## Baseline and current standing (historical IDs)

On 2026-10-22, the maintainer approved an all-thread supervisor evidence repair.
Three fresh exact and three bounded trials passed in 706.020 seconds with
five supervisor tasks per observation and complete engine restoration. The
probe SHA-256 is
`7bcf7ebc76498025ddff3219f6bfb85f24924d0e1b4f8fb7887b2a88ec56b65a`.
A fresh compatible-operation audit used those same probe bytes, took 55.878
seconds (54.757 internally), retained nine compatible observations and five
explicit blockers, restored inventories and stopped its owned API service.
The [current evidence](../tools/conformance/multiidentity/EVIDENCE.md) retains
exact sources, receipts and the failed pre-image discovery attempt. This is
private mechanism evidence; production integration remains separately gated.
Native stack #238 and its PR records hold current published heads and exact-head
approval evidence. Earlier heads and dated trial records below remain historical.


- The design's published predecessor chain is in native stack #238:
  HF1 / [#236](https://github.com/omry/reploy/pull/236),
  HF2 / [#237](https://github.com/omry/reploy/pull/237),
  0A / [#62](https://github.com/omry/reploy/pull/62),
  1A / [#63](https://github.com/omry/reploy/pull/63),
  1B / [#69](https://github.com/omry/reploy/pull/69), then
  A1 / [#239](https://github.com/omry/reploy/pull/239), then
  B-design / [#250](https://github.com/omry/reploy/pull/250).
- Source baseline for this design review: `main` at
  `4f1debfa61585ce2a1194ed8eb8047979aecc5a2`, including merged HF3–HF12.
  The maintainer-authorized rebase onto that revision completed on 2026-10-22.
  HF9–HF11 are present in this baseline, together with HF12 and the intervening
  main changes. This source update does not rerun the private mechanism trials
  or establish multi-identity product qualification.
- HF1 and HF2 are merged and published in
  [0.8.0.dev3](https://github.com/omry/reploy/releases/tag/v0.8.0.dev3).
  Earlier rebase and equivalence observations from 2026-10-15 through 2026-10-19
  are historical evidence for their recorded heads. Current published heads and
  approval records must be read from native stack #238 and the owning PR state;
  old labels or code-equality evidence do not establish current approval.
  Every reviewed head still requires its regular review and all eight exact-head
  CI jobs. The 2026-10-22 rebase preserves the slice patches and commit descriptions.
  0A is unchanged;
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


An earlier read-only reconciliation recorded local 0A, 1A, 1B and A1 at
`39f61fc2fd79`, `fa43b5805fce`, `62689dd733b8` and `18bbf46a63b4` respectively.
Those hashes and their native-equivalence observations are historical; they are
not the current rebased heads or current approval receipts. The design checkpoint
remains [#250](https://github.com/omry/reploy/pull/250) above A1. Current PR mappings,
exact-head checks and approval records are the delivery evidence. This standing
update grants no further rebase or ancestor-patch/description rewrite.

HF9 [#247](https://github.com/omry/reploy/pull/247), HF10
[#248](https://github.com/omry/reploy/pull/248) and HF11
[#249](https://github.com/omry/reploy/pull/249) are merged into the stated baseline.
They address effective base override, Podman tagged-image identity and ordinary
same-primary-ID managed-storage access respectively. HF12
[#267](https://github.com/omry/reploy/pull/267) is also present and restores pinned
Playwright ffmpeg acquisition through an official mirror in definition revision 2.
Reuse assessment must start from these existing implementations. HF11's ordinary
keep-id policy is not the isolated
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
| `internal/deploy/operation_lock.go` | 214 | 122 | Stable descriptor-held deployment lock; reuse for mapping transactions |
| `internal/deploy/atomic_state.go` | 50 | 39 | Atomic file replacement and sync; not multi-service recovery |
| `internal/deploy/blueprint_loader.go` | 200 | — | Existing blueprint references; reuse without a new service-map loader |
| `internal/blueprint/model.go` | 352 | — | Extend the existing single-environment declaration; keep accepted host authority in deployment state |
| `internal/blueprint/syntax.go` | 257 | 195 | Extend strict decoding with typed unresolved mapping leaves; validate known literals without requiring deployment values |
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
single-installation work. Direct native Podman operations already approved for individual
hotfixes do not authorize a general native backend.

## Group descriptions

- **A — Readiness.** Retain the six source-bound trials, compatible-operation
  dispositions and precise stock-mail blocker. PR 239 is the existing delivery.
- **B — Deployment mapping foundation.** Read-only host checks, explicit
blueprint declarations with required values, typed deployment overrides, geometry,
and identity state/recovery for staged and installed
  owners in the existing deployment transaction. Finish without launching workloads or adding a service-composition
  system. Create no host accounts, delegation, enrollment or global registry.
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
- **G — Initial service activation.** Integrate staged run/test and the existing
  installed workload/user-service lifecycle, explicit independent destination
  mappings, all autonomous restart owners, active revocation and exact cleanup. Complete
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
The approved full-campaign architecture supplies the common intended-behavior
authority for later LLD. It cannot supply
runtime proof, approve a new public wire syntax that it leaves unspecified, or
approve a later change to the reviewed contract. Routine implementation choices
within the approved contract do not require another maintainer gate.

### Group delivery sequence

LLD preparation is required work inside each remaining group's delivery. The
operator selects the implementation slices; the delivery run includes their
design prerequisites without a separate manual request. This is a requirement
on the agent executing this plan, not a claim that a SWE helper automatically
generates LLD. A bare slice-delivery call must not bypass these prerequisites.

Before implementing the first selected unfinished slice in a group:

1. Reuse current reviewed LLD where it covers the selected slices and still
   conforms to this architecture. Otherwise prepare or update the group's LLD.
   Define the concrete interfaces, data/state transitions, failure handling,
   implementation boundaries and validation plan. For qualification-only work,
   define the fixture and evidence plan instead of inventing production code.
2. Resolve the mechanism proofs needed to choose a sound implementation. Keep
   disposable probes separate from production implementation. Record later
   integration proofs with their owning slices and keep their gates closed
   until they pass. A failed proof returns to design; it does not weaken the
   requirement or silently substitute a different mechanism.
3. Deep-design-review the LLD locally and remotely against this architecture
   and the security contract. Resolve findings. If remote review needs a PR,
   publish a review-only draft; do not represent pending review as clean.
4. Present any unresolved public syntax or material change to behavior,
   authority, defaults or scope concisely at a `??>` gate. Record maintainer
   approval before depending on that decision. Reuse existing approvals;
   routine internal choices do not require repeated approval.
5. Update production/test estimates and split implementation slices before
   they exceed the 750-production-LOC hard limit; target 500. Preserve parent
   slice IDs with decimal children. Refresh affected stamps through SWE helpers
   after review and any approved design changes. Checkpoint and complete the
   design PR-cycle before dependent implementation.
6. Deliver the selected slices from that LLD through implementation review,
   checkpoint, publication and PR-cycle. Require every exact-head `ci.yml` job
   before approval. Report actual production/test LOC, timing, observed tokens
   and unresolved issues at the requested stopping point.

Prepare one coherent LLD per group or closely coupled group set, not a new
design exercise for every slice. D/E may share a design pass. F must cover both
storage and network gates; H includes ownership lifecycle before its later
restore gate; K needs qualification plans and the applicable evidence gates.
The table below identifies named gates, not an exemption for slices without a
named design gate. Independent earlier work such as K1 may prepare its own
bounded plan without waiting for unrelated groups.

If a later finding changes the LLD, review and re-stamp the affected design
before dependent implementation. Reopen maintainer approval only for a decision
outside recorded authority. Do not rewrite approved ancestors or rebase an
unrelated stack merely to repeat this preparation.

| Gate | Required before | Required decision or evidence |
| --- | --- | --- |
| B-design | B1–B5 | Approved and delivered. Preserve the single-installation schema/override contract, initial files-backed host checks, durable state and conservative recovery. No composition, global enrollment or payload admission is implied. B2 was delivered as B2.1 and B2.2. |
| campaign-architecture | Remaining D–K implementation | Approved on 2026-10-26 after design review. Preserve the approved intended behavior and authority; group LLD, unresolved public syntax and mechanism proofs remain separate delivery work. |
| compatible-map-proof | C1 implementation completion | Delivered for the internal C seam with retained patched-engine guest receipts. Public supervisor/storage/network admission remains separate; preserve private-harness receipts as separate evidence. |
| C-design | C1–C2 | Approved on 2026-10-25: operation dispositions and bounded adapter/build approach; no general native backend. Refresh affected stamps before implementation. |
| D-design | D1–D4 | Public identity/capability syntax, locked image evidence, role allowlists and default-empty behavior. |
| launch-design | E1–E4 | Production supervisor ownership and launch/replacement boundary; helper/supervisor authority, child descriptor/credential/capability plan and every supported entry. |
| storage-design | F1–F2 | Declared objects, fresh delegation/principal checks before ownership mutations, confined preparation, exclusion, restart inhibition and explicit crash recovery. |
| network-design | F3–F4 | Existing egress/endpoint semantics, pre-start effective observation and cleanup on selected Podman engine; HF11's deferred endpoint session gap is not declared fixed. |
| service-design | G1–G3 | Concrete staged run/test and installed user-service/restart ownership; explicit destination install input, independent reservations, source-revision checks and mapped-data transfer rejection; no hidden general scheduler. |
| generic-launch | Public execution after G3 | Both profiles; fresh complete principal/delegation checks before every execution admission; real mapped stage/run/test and separately mapped installation; staged-to-start and restart collision/unavailable-inventory negatives; surplus new exact-assignment rejection and retained-dormant controls; all accepted/undeclared identity transitions; final credentials/groups/capabilities/descriptors; supervisor separation; every application entry including exec; namespaces, devices, network denial, ordinary owned storage, mismatch/no-mutation, restart exclusion, active revocation and exact cleanup. |
| restore-design | H5–H7 | Offline export, integrity, destination allocation/delegation before ownership writes, collision-safe staging or approved checked translation and rollback. |
| secret-design | I1–I3 | Sources, recipients, confined paths, redaction and lifecycle authority. |
| interface-design | J1–J5 | Explicit producer/consumer identities, dedicated path/registry ownership, narrow discovery/attachment and safe replacement/revocation. Disposable proof passes before attachment activation. |
| mail-compatible | K2–K3 | Stock-master role and image-locked process/capability evidence fit the contract. Otherwise this branch remains blocked without relabeling a failure. |
| completion | After K4 | All 19 mandatory contract rows pass through supported interfaces. A report listing blockers is not completion. |

B delivered inactive mapping state and recovery under its approved design.
C-design is approved and C1/C2 are delivered internally. The campaign architecture
is approved. Group LLD and later evidence gates remain independent and are
proved before the group or feature that needs them. C cannot infer public
execution permission from delivered B code or a clean design review.

## Previous-to-current slice crosswalk

The prior table had 42 candidates. IDs below refer to that table, not the older
66-row inventory. Historical milestones 0A/1A/1B and evidence/PR titles do not
change. Existing A1 stays A1. Prefix future commit and PR titles with the new
slice ID. Further splits use decimal children, never a new singleton group.

| Previous ID | Current ID | Responsibility |
| --- | --- | --- |
| A1 | A1 | Private mechanism and compatibility readiness; code written |
| B1 | B1 | Read-only caller, delegation and host-principal preflight |
| B2 (administrator preparation) | — (removed) | Superseded by approved manual coordination; no `prepare` command |
| — (new) | B2.1, B2.2 | Blueprint declaration/required values, then typed deployment overrides; no service envelope or mandatory separate map file |
| B4 | B3 | Exact/bounded geometry, disjoint assignments and dormant-ID comparison |
| B3 | B4 | Optional identity record and mapping revision checks in existing deployment transaction |
| B5 | B5 | Pending intents, recovery, tombstones and conservative reservation retention |
| C1 | C1 | Scoped Podman admission and compatible exact/bounded create transport |
| C2 | C2 | Reuse compatible build/image path; bind immutable service images |
| D1 | D1 | Public identity declarations and name/number validation |
| D4 | D2 | Role-specific capability declarations and policy lock |
| D2 | D3 | Offline locked-image name/number evidence |
| D3 | D4 | Locked-identity coverage, generation admission and identity-change diagnostics |
| C3 | E1 | Compile reserved maps; pre-start verification and host-device rejection |
| C4 | E2 | Child credentials, capability drop, descriptors, dumpability and sessions |
| C5 | E3 | Supervisor lifetime/replacement and common admission including runtime exec |
| D5 | E4 | Effective capability and privilege-regain verification on every entry |
| E1 | F1 | Descriptor-safe declared-object preparation with fresh ownership authority |
| E2 | F2 | Exclusive prepare/attach admission, crash inhibition and restart fencing |
| G1 | F3 | Existing egress policy and cross-service network-denial enforcement |
| G2 | F4 | Declared endpoint publication, effective observation and cleanup |
| C6 | G1 | Existing staged/installed lifecycle and explicit destination mapping boundary |
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
| H3 | J3 | Explicit participant ownership, discovery and admission |
| H4 | J4 | Narrow participant attachment preserving private mappings |
| H5 | J5 | Participant replacement, revocation and removal |
| I1 | K1 | Rspamd/Redis on supported capability-free path |
| I2 | K2 | Stock Postfix through the approved supervisor model |
| I3 | K3 | Stock Dovecot including synthetic SQL and restore |
| I4 | K4 | Combined interface/lifecycle campaign and all-row evidence report |

The superseded 44-candidate composition proposal additionally had B6 (service
blueprint staging) and B7 (composition generation publication). Both are deferred
to the separate composition feature and absent from the active table. Its B2/B4
are narrowed here to one installation; G1 uses the existing workload lifecycle.
J3 remains separately gated explicit sharing between independently configured
installations, with no aggregate service-list or composition dependency.

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
| ID-01 | A1; B1, B3, D4, E1; generic-launch |
| ID-02 | D1, D3–D4, E2–E3; generic-launch |
| ID-03 | E2–E4; generic-launch |
| ID-04 | A1; F3, G1; generic-launch; J preserves isolation |
| BE-01 | A1; C1; generic-launch |
| CAP-01 | D2, E2, E4; generic-launch |
| LC-01 | H1–H2 |
| LC-02 | B3–B5, D4, H3 |
| LC-03 | H5–H7; destination authority checked before archived ownership |
| AL-01 | B3–B5, E1, F2, G1–G3, H4 |
| RV-01 | G1–G3, H4; generic-launch |
| HB-01 | B1–B2, G1; generic-launch |
| SB-01 | A1; E1; generic-launch |
| SB-02 | E1–E4, F3–F4; generic-launch; every application entry |
| FS-01 | F1–F2 |
| CF-01 | B1, B4–B5, D3–D4, E1, H1–H2, H6–H7; generic-launch |
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
A1 adds 879 test/fixture lines against 1B. Its original published head was
approved. Published-head and CI observations from earlier rebases are historical.
Read current exact-head approval and checks from the owning PR state, as the
baseline section requires; earlier observations cannot establish current approval.
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
A1 is the existing PR 239 follow-up in native stack #238; the current
B-design PR #250 is above it.
Preserve that mapping and every slice-prefixed commit title.
Do not create a duplicate PR or rewrite an approved ancestor.


## Next delivery request

The architecture design-only run is complete. The next proposed sprint delivers
D and E, including their LLD as part of that delivery. Continue in
`/home/omry/dev/reploy`; preserve delivered A–C and all existing local work.
Publish the reviewed architecture first, then append the D/E design and
implementation without an unnecessary rebase. No merge, release, live-service
changes, global-helper edits or changes to the Arbiter/Podman checkouts are
authorized.

```text
checkpoint and publish the reviewed architecture ->
global:swe:pr-cycle-stack("current") ->
prepare D/E LLD and prerequisite mechanism proofs under the group delivery sequence ->
deep-design-review D/E LLD locally and remotely; resolve findings ->
(present unresolved public syntax or material design decisions concisely ??> record maintainer approval) ->
refresh affected design stamps through SWE helpers ->
global:swe:checkpoint-commit() ->
global:swe:submit-native-stack("main", "draft") ->
global:swe:pr-cycle-stack("current") ->
global:swe:deliver-design-stack(
  "docs/MULTI_IDENTITY_IMPLEMENTATION_PLAN.md",
  tasks="D1,D2,D3,D4,E1,E2,E3,E4"
) -> report approved PRs actual production/test LOC timing observed tokens and issues ->
stop after E4 and propose the next sprint
```

This example does not start itself or authorize D–K implementation. If LLD
requires decimal splits, substitute those reviewed child IDs in the task list.
The design-decision gate uses existing approvals when they already cover all
decisions; it does not require another approval for routine internal choices.
Use slice-prefixed commit titles and the production size bound. Public mapped
execution remains inhibited until generic-launch passes after G3.
