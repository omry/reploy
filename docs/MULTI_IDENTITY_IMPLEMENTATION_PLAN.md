---
status: Single-installation mapping design proposed; composition deferred; implementation gates remain
updated: 2026-10-19
summary: Implementation-ordered single-installation identity slices, explicit durable mappings, separate composition scope, and conditional production/test estimates.
---

# Multi-Identity Delivery Design and Implementation Plan

The [security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md) is authoritative.
On 2026-10-19, the maintainer accepted separating composition from multi-identity.
This proposal adds explicit mappings to one existing workload installation.
A general service-composition system is separate work, not a prerequisite.
The maintainer also accepted blueprint mapping declarations, deployment overrides
and `???` for required values on 2026-10-19. The concrete syntax and integration
below implement that design direction as a proposal; command names and the
concrete host policy remain subject to B-design approval. Review stamps assess
the proposal; they do not select a production supervisor or approve unrelated APIs.

The main delivery table is in implementation order. Each group can finish
without a later implementation slice. Named design and evidence gates still
apply. Earlier IDs are preserved in the crosswalk; historical evidence keeps
its original milestone labels. No later implementation has started.

The target is local rootless Linux Podman through the existing Docker-compatible
route. A general native Podman backend, remote engines and Podman Machine are
separate features. Rootful multi-identity support needs separate qualification.
The proposed first runtime cohort is non-root staging and user-scope installation
under that same rootless operating account. System-scope multi-identity install
and root-run mapped operations reject before mutation; the existing system-unit
path does not establish that rootless runtime account. This restriction is part
of the pending concrete host-policy decision, not a claim of system-scope support.
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
are arithmetic planning ranges; do not add them to their slice rows. A1's written
code is excluded from remaining totals. Targets do not require padding small
changes to reach 500.

| Group | Slice | Concrete result | Prod target | Production LOC | Test/fixture LOC | Prerequisites |
| --- | --- | --- | ---: | ---: | ---: | --- |
| **A** | **Subtotal** | **Readiness** | — | **0** | **0** | — |
| A | A1 | Private mechanism and compatibility readiness; code written | 0 | 0 | 0 | PR 69; final PR 239 delivery gate |
| **B** | **Subtotal** | **Single-installation inputs and durable mappings** | — | **2,250–3,650** | **3,750–5,900** | — |
| B | B1 | Read-only caller, delegation and host-principal preflight | 450 | 350–550 | 550–850 | A1; B-design |
| B | B2.1 | Blueprint mapping schema, required values and immediate opt-in execution inhibition | 500 | 450–700 | 700–1,150 | B1; B-design |
| B | B2.2 | Typed deployment overrides and operation-specific effective mapping resolution | 500 | 400–650 | 650–1,050 | B2.1; B-design |
| B | B3 | Exact/bounded geometry, disjoint assignments and dormant-ID comparison | 450 | 350–550 | 600–900 | B2.2 |
| B | B4 | Identity record, operation lock and complete directory-removal barrier | 450 | 350–600 | 600–950 | B3 |
| B | B5 | Pending intents, recovery, tombstones and conservative reservation retention | 450 | 350–600 | 650–1,000 | B4 |
| **C** | **Subtotal** | **Existing engine and build integration** | — | **600–1,200** | **1,150–1,900** | — |
| C | C1 | Scoped Podman admission and compatible exact/bounded create transport | 500 | 350–650 | 650–1,050 | B5; C-design; compatible-map-proof |
| C | C2 | Reuse compatible build/image path; bind immutable service images | 400 | 250–550 | 500–850 | C1 |
| **D** | **Subtotal** | **Locked identity and capability declarations** | — | **950–1,600** | **1,600–2,600** | — |
| D | D1 | Public identity declarations and name/number validation | 300 | 250–400 | 400–650 | C2; D-design |
| D | D2 | Role-specific capability declarations and policy lock | 300 | 200–350 | 350–550 | D1 |
| D | D3 | Offline locked-image name/number evidence | 400 | 300–500 | 500–800 | D2 |
| D | D4 | Locked-identity coverage, generation admission and identity-change diagnostics | 300 | 200–350 | 350–600 | D3; B3 |
| **E** | **Subtotal** | **Mapped execution and supervisor admission** | — | **1,400–2,550** | **2,800–4,550** | — |
| E | E1 | Compile reserved maps; pre-start verification and host-device rejection | 500 | 350–650 | 650–1,100 | D4; C1; launch-design |
| E | E2 | Child credentials, capability drop, descriptors, dumpability and sessions | 500 | 400–750 | 800–1,300 | E1; D2 |
| E | E3 | Supervisor lifetime/replacement and common admission including runtime exec | 450 | 350–650 | 700–1,150 | E2 |
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
| **Total** | — | **Conditional remaining implementation** | — | **12,200–20,950** | **25,000–40,550** | Named gates apply |

There are **43 delivery candidates** including written A1;
**42 remain**. B2 is split into B2.1 and B2.2 for the revised input design.
The conditional code total is
**37,200–61,500 LOC**, split into the production and test ranges above.
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
| B4 | Commit optional identity metadata atomically with the existing deployment state under its existing operation lock. Bind the selected staged or installed owner and compare expected mapping revision and generation; updates preserve the record and copied records cannot authorize another directory. Extend the B2.1 guard to retained identity state even if the declaration is removed. Guard all directory-removal routes before effects: staged removal, installed `uninstall --remove-dir`, terminal retries and pending-removal recovery, including force, wait and service-name aliases. Retain this barrier until H4 provides checked release. Prove every entry remains inhibited and omission cannot downgrade authority. Concurrent writers serialize; stale/substituted state rejects. Read-only inspection creates nothing; old strict readers reject opt-in state |
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


## Proposed single-installation mapping design

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
`current_app_command_run.go`, controlled-session and `provider_install_run.go`
routes, including aliases and native/deployed command branches. Read-only inspection
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

Before product implementation, approve B's concrete single-installation mapping contract.
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
  A1 / [#239](https://github.com/omry/reploy/pull/239), then
  B-design / [#250](https://github.com/omry/reploy/pull/250).
- Base: `main` at `33d0583c9dfa07367541b1d71d0f3e56bed760ea`, including
  merged HF3–HF8.
- HF1 and HF2 are merged and published in
  [0.8.0.dev3](https://github.com/omry/reploy/releases/tag/v0.8.0.dev3).
  The remaining four slices were locally rebased onto the stated main revision
  on 2026-10-15. All four commit patches are unchanged by that rebase. Their
  rebased heads are now published; all eight required CI jobs passed on each
  head as observed on 2026-10-19. Approval records still name the original
  reviewed heads. Equivalent-review recovery has passed native proof and
  semantic assessment but is deferred by the maintainer while a separate SWE
  design-record compatibility issue is assessed. This does not establish
  current PR-cycle approval. 0A is unchanged;
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


Read-only reconciliation confirms that local 0A, 1A, 1B and A1 are synchronized
with their published heads: `39f61fc2fd79`, `fa43b5805fce`, `62689dd733b8` and
`18bbf46a63b4` respectively. Their original reviewed patches, full commit
messages and file modes pass SWE's native equivalence proof. Existing approval
labels alone do not renew the original approval records for these heads.
The design checkpoint is published above A1 as
[#250](https://github.com/omry/reploy/pull/250). Its substantive design review
proceeds independently under the maintainer's explicit ancestor-recovery
deferral. Formal stack approval remains separate and pending.
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
Unresolved later decisions do not block independent foundations.

| Gate | Required before | Required decision or evidence |
| --- | --- | --- |
| B-design | B1–B5 | Reviewed single-installation mapping input/state proposal, concrete schema/command syntax, initial files-backed host contract and failure/recovery behavior. Manual coordination, composition deferral, staged ownership with independent destination input, and blueprint mappings with deployment overrides and `???` are approved directions. Concrete spelling/host policy still require maintainer approval; stamps do not imply it. B2 is delivered as B2.1 then B2.2. |
| compatible-map-proof | C1 implementation completion | Fresh exact and bounded compatible creation, planned/effective host maps and pre-payload negative tests; preserve private-harness receipts as separate evidence. |
| C-design | C1–C2 | Operation dispositions and bounded adapter/build approach; no general native backend. |
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
approved; the rebased head is now published and its eight required CI jobs
passed. Approval renewal for that head remains deferred as described above.
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

Finish the local and remote review of this design checkpoint first. Under the
maintainer's explicit deferral, substantive design review and B-design approval
can proceed while ancestor approval recovery remains pending. This does not
waive formal PR-cycle or merge checks, and a later approval pass must revalidate
the evidence against the then-current stack. Obtain B-design approval of the
concrete public contract and report before implementation. The first revised delivery
set is B1–B5: host preflight, installation mapping input, geometry, durable state
and recovery. The former composition-specific B6/B7 are deferred to a separate
feature; their IDs are not silently reused. The next major system is
C's existing-engine integration, with compatible-map-proof before activation.
Do not resume the old enrollment proposal or infer an automatic full campaign.

```text
global:swe:deliver-design-stack(
  "docs/MULTI_IDENTITY_IMPLEMENTATION_PLAN.md",
  tasks="B1,B2.1,B2.2,B3,B4,B5"
) -> produce run stats, timing, observed tokens and issues/opportunities
```

This invocation is proposed, not started by this document. Every commit title
must include its slice ID. No merge, release, live service changes or changes
to the Arbiter/Podman checkouts are part of this design pass.
