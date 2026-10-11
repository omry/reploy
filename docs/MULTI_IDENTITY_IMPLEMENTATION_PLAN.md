---
status: Finalized delivery plan; implementation gated
updated: 2026-10-11
summary: Multi-identity delivery design; initial conformance group is specified, later integration remains subject to explicit design and compatibility gates.
---

# Multi-Identity Delivery Design and Implementation Plan

This document defines the remaining delivery boundaries and acceptance gates.
The approved [security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md) remains
authoritative. Finalizing this plan does not approve its unresolved host
recipient, mapping default, supervisor implementation or public policy choices.
Only the initial group A is specified for delivery now; later groups require
the named decisions and evidence below before their implementation starts.

The campaign uses **Podman through the existing Docker-compatible route**.
Full native Podman support, native build transport, remote engines and Podman
Machine are separate features. Unsupported required operations produce a
blocker, not permission to introduce a native backend or relax isolation.

## Baseline and current standing

- Local stack: `0c85942d4444` (0A APT fix), `fe3a1ab0883b` (1A contract),
  `3a5e9b8ac6e5` (1B private conformance harness).
- Base: `main` at `7dfc956ceaa18638260d380f53d12bc4701dcda2`.
- Rebased 2026-10-10 without conflicts; all three per-commit diffs were preserved.
  The rebase was local. GitHub PRs [62](https://github.com/omry/reploy/pull/62),
  [63](https://github.com/omry/reploy/pull/63), and
  [69](https://github.com/omry/reploy/pull/69) still refer to older revisions.
- At the pre-A1 baseline, 0A is implemented and the focused APT suite passes;
  1A has an existing contract. 1B has historical successful mechanism
  observations, but PR 69's harness was not accepted as complete because of
  the following two P1 findings. A1 repairs both in the current candidate.
- [Pre-start validation finding](https://github.com/omry/reploy/pull/69#discussion_r3787738714):
  PR 69's predecessor harness validated host authority only after primary
  workloads executed. A1 admits all four workload/control mappings before
  releasing any payload.
- [Auxiliary-mapping finding](https://github.com/omry/reploy/pull/69#discussion_r3787738718):
  PR 69's predecessor harness omitted the no-capability and unfiltered control
  containers from that validation. A1 includes both controls in admission.
- Before A1, the retained observation described three exact and three bounded
  trials on 2026-08-14, and its four-source SHA-256 matched the rebased harness.
  That design pass ran no fresh live campaign; rebase and compilation alone did
  not establish current conformance. A1 now changes the harness source identity
  and supplies six fresh passing trials on the current host, recorded in
  [the current evidence](../tools/conformance/multiidentity/EVIDENCE.md).
  The older observation still cannot prove sound pre-payload admission.
- The original detailed campaign is the private, untracked
  [`temp/flux-support-work-plan.md`](../temp/flux-support-work-plan.md).
  Its old baseline, gap map and sprint status are historical planning context,
  not a current completion ledger. The contract introduction also retains
  historical one-off-probe wording. A wording correction alone would not have
  closed the harness defects; A1 supplies the repairs and fresh evidence above.

## Groups, delivery slices and gates

Groups have unique letters **A–I**, with numbered delivery slices such as
**A1**, **B1** and **B2**. Further splits use **A1.1**, **A1.2**, and so on;
the parent becomes an aggregate, and dependencies name the required children.
Every delivered implementation PR has the same production LOC limit.

The earlier 66-row inventory mixed delivery work with decisions and evidence.
This plan has **42 candidate delivery slices**: 30 with estimated production
changes, seven audit-gated runtime responsibilities, and five test-only
packages. The seven runtime entries are provisional boundaries, not seven
approved PRs: compatible operations may need no change, and an oversized
concrete change must split before implementation. Review/approval decisions,
conformance and final reports are gates rather than standalone code slices.

| Group | Scope | Delivery slices | Previous inventory groups |
| --- | --- | --- | --- |
| A | Conformance and compatibility readiness | A1 | A, B, F |
| B | Host authority and durable allocation | B1–B6 | C, D |
| C | Existing-backend runtime integration | C1–C7 | E |
| D | Public identity and capability policy | D1–D5 | G, H |
| E | Filesystem preparation and storage lifecycle | E1–E9 | I, J |
| F | Secret-file delivery | F1–F3 | K |
| G | Network and endpoint compatibility | G1–G2 | L |
| H | Shared interfaces and participant lifecycle | H1–H5 | M, N, O |
| I | Component validation and final evidence | I1–I4 | P, Q, R, S, T |

Previous-inventory letters refer only to the superseded 66-row draft. Historical
milestone labels in the baseline, contract and retained evidence remain unchanged.

## Delivery sizing rule

The user set this delivery constraint on 2026-10-10:

- Aim for **approximately 500 production LOC** per initial implementation PR.
  Smaller coherent changes are welcome; do not add code to reach the target.
- **750 production LOC is a hard limit per PR**, not a limit per parent milestone
  or a campaign budget. Combining tasks does not combine their allowances.
- Use this document's incremental metric: count added or rewritten handwritten
  production lines, including comments, blank lines and shipped helper scripts.
  Do not subtract deleted lines to make an oversized change appear smaller.
  Report deletions and mechanical moves separately; count additions at the
  destination so moving code cannot bypass the cap. Tests/fixtures and docs
  remain separately reported; test LOC has no user-imposed numeric cap.
- Before implementation, identify the exact review unit, its production target,
  estimate range, test scope and prerequisites. If its estimated upper bound
  exceeds 750, split it before starting. An unknown estimate is not a zero-code
  allowance: the responsible audit/design must resolve and bound it first.
- Inspect actual production LOC before each checkpoint commit/publication.
  If actual code exceeds 750, split into coherent dependent commits/PRs before
  publishing; do not silently raise the limit or remove required behavior/tests.
  Revalidate each independently reviewable child and the assembled behavior.
- Preserve ownership, rollback and security invariants across a split. An
  intermediate helper may land inactive, but activation waits for its complete
  admission/launch/storage contract. Code below the cap is not automatically safe.

Initial targets in the delivery table are planning targets, not predictions or
authorization to implement an unresolved design. Every numbered implementation
unit has the same 750-LOC hard limit. Parent rows below are aggregate work, not
one-PR delivery instructions.

## Estimation method and assumptions

Numbers are engineering estimates of **incremental handwritten lines added or
meaningfully rewritten**, including ordinary comments and blank lines. They
are not measured future diffs, net repository growth, or a token/time budget.
Estimate ranges are separate from the user-specified hard delivery cap. A
reused existing file is not charged in full. Ownership of
shared helpers belongs to the first introducing unit; downstream units count
only their additional integration and assertions. Actual PR churn can be larger
when later work revises earlier code.

- **Production:** shipped Go/runtime helpers, supported command and service
  behavior, policy/schema parsing, allocator and storage code. A script is
  production if it implements supported behavior.
- **Tests:** unit/integration tests, disposable Go/Python/C probes, fixtures,
  synthetic configuration and blueprint files, and test-only orchestration.
  The existing private conformance harness and component experiments count here even though they
  are not named `_test.go`.
- **Excluded from both:** design prose, changelog/release-note prose, website
  documentation, generated code, dependency/vendor trees, build artifacts and
  captured environment inventories. Therefore a zero/zero design or report row
  still represents work.
- Estimates retain both exact and bounded profiles. Neither silently replaces
  the other and no default is selected.
- The reference sizing case is a local rootless Linux Podman engine behind
  the existing Docker-compatible transport, a delegated OS-user pool, explicit
  administrator bootstrap and reuse of existing build/generation machinery.
  The host/delegation choices remain **sizing assumptions, not approved choices**.
  Compatibility gaps are unresolved until audited. Native Podman execution,
  native build support, Podman Machine/remote-engine support and a general
  runtime framework are separate features, not alternative paths included here.
  A privileged broker likewise requires a separate authority decision and estimate.
- Durable allocations own concrete host IDs for both profiles. The probe's
  `auto:size=4096` invocation is not a production persistence design. Rootless
  mapping translates through an intermediate namespace; planned host authority
  and effective maps must agree before executing any payload.
- Existing capabilityless Docker behavior and ordinary sandbox policy remain
  enforced. Resource-limit design remains separately deferred by the contract;
  no new resource policy is hidden in these estimates.
- Backup/restore covers checked, offline Reploy-managed storage with approved
  ownership preservation/translation. Database dump/import is component test
  orchestration, not a new generic database backup facility.
- Component estimates assume the mail-compatible gate and the approved public contracts permit their
  process/capability models. If they do not, full campaign completion has an
  unbounded design dependency; the conditional total below is not a promise to
  solve it within a numeric allowance.

Confidence is medium for bounded parser/registry/preparation changes and low
for bootstrap authority, runtime/image/service integration and cross-install
sharing until their designs are approved. The endpoints below describe
plausible implementation sizes under the stated assumptions; they are not
statistical confidence intervals or automatic reasons to stop.

## Source-grounded reuse and scale

Physical source line counts at the baseline, used as scale references rather
than targets:

| Existing seam | Production LOC | Paired test LOC | Reuse and limitation |
| --- | ---: | ---: | --- |
| `internal/deploy/operation_lock.go` | 200 | 122 | Advisory-lock technique; lock is per deployment, not a cross-install allocator lock |
| `internal/dockerdeploy/application_sandbox_plan.go` | 208 | 282 | Central sandbox policy; currently rejects retaining capabilities |
| `internal/dockerdeploy/install_runtime_identity.go` | 142 | 124 | Existing numeric entry identity and groups; no identity-transition set |
| `internal/dockerdeploy/runtime_host_filesystem_linux.go` | 626 | 509 | Host path/mount checks; not mixed-owner object preparation |
| `internal/dockerdeploy/private_workload_environment.go` | 347 | 509 | Private-input/redaction patterns; no declared secret-file lifecycle |
| `internal/deploy/runtime_policy.go` | 209 | 101 | Locked effective-policy structure; needs identity/backend integration |

`atomic_state.go` supplies file sync/atomic replacement/directory-sync patterns.
`generation_reference_docker.go` (296 lines) owns Docker image references;
`provider_install_systemd.go` (131 lines) emits a system service bound to Docker
Compose. Neither is a reusable Podman backend merely by changing an executable
name. The current `dockerdeploy` package contains 55,089 production and 73,569
test/testdata Go lines; this plan sizes changes to the relevant seams, not a
rewrite of that package.

The four existing private conformance code files total 2,449 lines. Existing Flux
functionality scripts are 333 lines for Rspamd/Redis, 396 for Postfix and 726
for Dovecot, before their synthetic blueprints/configuration. These provide
fixtures and protocol checks; they still directly orchestrate Docker and do
not prove the supported Reploy integration.

The retained [compatibility experiment](../temp/flux-identity-probe/DECISION_GATE_1.md)
and its [machine-readable result](../temp/flux-identity-probe/results-podman-docker-compat/20260814T074049Z-21912/summary.json)
proved a bounded-range creation request through the Docker-compatible API and
subsequent start, exec, inspect, volume and cleanup operations through a scoped
Docker CLI. The CLI rejected the Podman-specific `UsernsMode` before sending
it; direct create JSON succeeded. This supports reuse rather than a presumption
of a new backend. It does not prove exact sparse creation, durable fixed
mappings, Reploy's complete operation inventory, its build/service path, or
the required supervisor/application trust split. Native Podman was used to
build the disposable scratch fixture and independently inspect the runtime;
that is not evidence that production needs native backend support.

Primary runtime references checked for this design pass:

- [Podman 4.9.3 run/mapping documentation](https://docs.podman.io/en/v4.9.3/markdown/podman-run.1.html):
  rootless UID/GID maps pass through an intermediate namespace; automatic
  namespace allocation is runtime behavior, not Reploy's retained-allocation
  contract. The persistence conclusion is this plan's design inference.
- [Podman service documentation](https://docs.podman.io/en/v4.9.3/markdown/podman-system-service.1.html):
  distinguishes the Docker-compatible API from the native Libpod API. The
  requirement to positively identify Podman before using either profile comes
  from Reploy's approved contract, not from API compatibility alone.

## Remaining scope: delivery slices and estimates

Bold group rows are subtotals, followed by their delivery slices. Do not add
both. Tests, probes and fixtures are reported separately from production code;
prose and generated observations do not count as code. All ranges remain
conditional engineering estimates, not measured diffs or support claims.

| Group | Slice | Concrete result | Initial prod target | Production LOC | Test/fixture LOC | Prerequisites |
| --- | --- | --- | ---: | ---: | ---: | --- |
| **A** | **Subtotal** | **Conformance and compatibility readiness** | — | **0** | **650–1,300** | — |
| A | A1 | Repair the private harness and retain readiness dispositions; fresh mechanism 6/6 passed, delivery review pending | 0 | 0 | 650–1,300 | existing private harness (PR 69); [current evidence](../tools/conformance/multiidentity/READINESS.md) |
| **B** | **Subtotal** | **Host authority and durable allocation** | — | **2,150–3,350** | **3,550–5,450** | — |
| B | B1 | Read-only host/delegation preflight | 450 | 350–550 | 550–850 | A1; B-design |
| B | B2 | Explicit administrator preparation with rollback, idempotency and audit receipt | 500 | 450–700 | 650–1,000 | B1 |
| B | B3 | Versioned registry transaction and global lock | 500 | 400–600 | 650–950 | B1; B-design |
| B | B4 | Exact and bounded reservation/allocation geometry | 450 | 350–550 | 600–900 | B3, B1 |
| B | B5 | Crash recovery, tombstones and safe-release checks | 300 | 250–400 | 550–850 | B4 |
| B | B6 | Revocation admission and runtime/service stop orchestration | 450 | 350–550 | 550–900 | B2, B3, C1, C6 |
| **C** | **Subtotal** | **Existing-backend runtime integration** | — | **TBD** | **TBD** | — |
| C | C1 | Positive engine identity, scoped endpoint and supported create/map request | 500 | TBD | TBD | A1; C-design |
| C | C2 | Existing build/image compatibility and immutable-reference verification, only where changes are needed | 500 | TBD | TBD | C1 |
| C | C3 | Reserved exact/bounded map compilation and pre-start admission | 500 | TBD | TBD | B4, B1, C1 |
| C | C4 | Declared child credentials, capability drop, descriptor/dumpability/session confinement | 500 | TBD | TBD | C3, D1; C-design |
| C | C5 | Trusted supervisor replacement/lifetime and common launch admission boundary | 400 | TBD | TBD | C4 |
| C | C6 | User-level install/start/stop/status and restart-owner revocation interlock | 500 | TBD | TBD | C2, C5, B2, D3, D5, E2, G2 |
| C | C7 | Failed-operation reconciliation and exact owned-resource cleanup | 400 | TBD | TBD | C6, B5 |
| **D** | **Subtotal** | **Public identity and capability policy** | — | **1,250–2,100** | **2,250–3,600** | — |
| D | D1 | Public identity model, parser and validation | 300 | 250–400 | 400–650 | A1; D-design |
| D | D2 | Offline locked-image name/number evidence | 400 | 300–500 | 500–800 | D1, C2 |
| D | D3 | Generation admission and identity change diagnostics | 300 | 200–350 | 350–600 | D2, B4 |
| D | D4 | Capability declarations, role validation and locking | 300 | 200–350 | 350–550 | D1; D-design |
| D | D5 | Runtime delivery and startup capability/privilege-regain verification | 400 | 300–500 | 650–1,000 | D4, C5 |
| **E** | **Subtotal** | **Filesystem preparation and storage lifecycle** | — | **2,600–4,300** | **4,550–7,250** | — |
| E | E1 | Descriptor-safe declared object preparation | 500 | 400–650 | 650–1,050 | D3, B5; E-design |
| E | E2 | Exclusive preparation/attach, restart fencing and no-mutation failure handling | 300 | 250–400 | 500–800 | E1, C5 |
| E | E3 | Ownership-stable stop/start of the existing generation | 300 | 250–400 | 350–550 | C6, E2, D3; E-design |
| E | E4 | Recreation preserving allocation and host ownership | 300 | 250–400 | 450–750 | E3 |
| E | E5 | Identity evolution, dormant assignments and rollback | 450 | 350–550 | 650–1,000 | E4, B5 |
| E | E6 | Removal, retained storage and safe reuse integration | 350 | 250–450 | 500–800 | E5, B6 |
| E | E7 | Exclusive offline export and integrity manifest | 500 | 350–600 | 550–900 | E4; E-design |
| E | E8 | Archive validation and explicit ownership translation | 400 | 300–500 | 550–850 | E7, E6 |
| E | E9 | Exclusive restored-state activation and rollback | 300 | 200–350 | 350–550 | E8, E6 |
| **F** | **Subtotal** | **Secret-file delivery** | — | **850–1,400** | **1,500–2,450** | — |
| F | F1 | Secret reference model and redacted resolution | 300 | 250–400 | 400–650 | D3; F-design |
| F | F2 | Confined file delivery and absence from metadata/logs | 400 | 300–500 | 550–900 | F1, E2 |
| F | F3 | Rotation, rollback, restart and removal | 400 | 300–500 | 550–900 | F2, E5, C6 |
| **G** | **Subtotal** | **Network and endpoint compatibility** | — | **600–1,000** | **1,000–1,700** | — |
| G | G1 | Podman egress policy enforcement and current-network denial parity | 500 | 350–600 | 600–1,000 | C1, C5; G-design |
| G | G2 | Declared endpoint publication, observation and cleanup | 300 | 250–400 | 400–700 | G1, C5 |
| **H** | **Subtotal** | **Shared interfaces and participant lifecycle** | — | **1,700–2,850** | **3,550–5,750** | — |
| H | H1 | Prove the approved shared-interface mechanism and prepare its dedicated runtime paths | 400 | 300–500 | 950–1,550 | D3, E6, E2; H-design |
| H | H2 | Stale socket reconciliation and producer restart safety | 350 | 250–450 | 500–900 | H1 |
| H | H3 | Resource ownership, discovery and participant admission | 500 | 400–650 | 700–1,100 | H2; H-design |
| H | H4 | Narrow participant attachment and private mapping isolation | 500 | 350–600 | 650–1,000 | H3 |
| H | H5 | Producer/consumer replacement, revocation and removal | 500 | 400–650 | 750–1,200 | H4, E6 |
| **I** | **Subtotal** | **Component validation and final evidence** | — | **0** | **2,750–4,550** | — |
| I | I1 | Rspamd/Redis reproduction on existing capability-free path | 0 | 0 | 450–750 | A1 |
| I | I2 | Postfix reproduction using approved supervisor model | 0 | 0 | 650–1,100 | generic-launch, D5, E9, F3, G2; mail-compatible |
| I | I3 | Dovecot reproduction including synthetic SQL and restore | 0 | 0 | 1,000–1,600 | generic-launch, D5, E9, F3, G2; mail-compatible |
| I | I4 | Postfix–Dovecot shared-interface campaign and final evidence/gap report | 0 | 0 | 650–1,100 | I2, I3, H5, I1 |
| **Total excluding C** | — | **Conditional estimate** | — | **9,150–15,000** | **19,800–32,050** | — |

The conditional combined estimate excluding C remains **28,950–47,050 LOC**.
The regrouping preserves the previous numeric scope: A includes the harness
increment and compatibility probes, B combines preparation with its idempotency
checks, and H includes the disposable shared-interface proof. Gate prose adds
no code allowance. Any additional test code discovered while collecting gate
evidence belongs to its owning delivery slice and updates that estimate.

C targets are initial planning targets, not estimates or extra limits. A1 must establish which
compatible operations work, which need targeted changes and which are blocked.
Before starting any C slice, approve its concrete approach and record production
and test ranges with a production upper bound at most 750; otherwise close it
with reuse evidence, split it, or report the blocker. A new trusted supervisor
requires an explicit design decision, and native Podman is never a fallback.
There is no complete campaign total while C or mail compatibility is unresolved.

## Group descriptions

### A — Conformance and compatibility readiness

Current A1 evidence: three exact and three bounded trials passed on the current
host after admission repairs; all four primary/control roles were gated before
payload execution. The compatible endpoint accepted a tested fixed bounded
request, lifecycle/volume CRUD and network CRUD. The tested sparse-map projection
failed, and Reploy's actual build preflight rejected Podman 4.9.3. Service launch
ownership, network-policy parity and stock-mail supervisor approval remain
unproved. These are explicit downstream blockers, not native-backend work.
See the [readiness disposition](../tools/conformance/multiidentity/READINESS.md).
A1 itself remains pending its delivery review and exact-head PR checks.

A1 repairs the private Python/Go/C harness, validates every primary and auxiliary
container before payload execution, and retains fresh dual-profile observations.
After mechanism conformance passes, audit actual Reploy create/map, build/image,
inspect/exec, volume, service, network and cleanup operations through the scoped
compatible endpoint. Separately investigate whether stock Postfix/Dovecot can
fit the frozen supervisor/child trust split. These are checkpoints of one
readiness package, not new production backends or mandatory additional PRs.

### B — Host authority and durable allocation

Read-only preflight precedes explicit administrator preparation. Preparation
includes rollback, rerun verification and its audit receipt in the same slice.
A versioned cross-install registry and allocation lock own disjoint subordinate
UID/GID assignments for exact and bounded profiles, including dormant assignments
and retained storage. Revocation lands after real runtime resources and restart
owners exist; it stops resources without releasing retained data or its mapping.

### C — Existing-backend runtime integration

Reuse the existing endpoint, build/image and service seams wherever A1 proves
compatibility. Bind each start to locked images, delegated reserved maps and
verified effective credentials. The trusted launch boundary enforces declared
child identities, groups, empty child capabilities, descriptor closure,
dumpability/session separation and privilege-regain denial before untrusted
execution. Replacement, restart interlocks and exact failure reconciliation
remain trusted Reploy behavior. Neither a new supervisor binary nor native
transport is predetermined. The seven candidate responsibilities are re-sized
after the audit; they cannot bypass the hard cap by remaining TBD.

C5 supplies the common launch-admission boundary used by preparation, attachment
and supervisor replacement. C6 must integrate every owned service-manager and
autonomous restart path with it before public activation. An unfenced restart
path is a blocker, not an exception to offline preparation.

### D — Public identity and capability policy

Public declarations, resolved state and lock records capture image-local IDs
and role-specific allowlists. Locked-image name/number evidence and generation
admission diagnose mismatches before mutation. Capability sets stay empty by
default; application children never inherit the supervisor allowlist. Schema
work begins only after A1 viability and explicit declaration design approval;
it does not wait for the supported-interface acceptance it enables.

### E — Filesystem preparation and storage lifecycle

Trusted no-follow descriptor operations prepare only declared inert objects,
with exclusive attachment and no application hooks at elevated authority.
Stop/start and recreation preserve mappings and host ownership. Generation
changes retain removed assignments without rewriting data. Retention/release,
offline export, integrity checks, explicit checked ownership translation and
exclusive restore activation are independently reviewed responsibilities.
Database dump/import remains component test orchestration.

Before preparation, offline export or restore mutates an object, acquire
exclusive lifecycle ownership, revalidate that no workload is attached, and
inhibit all launch/restart paths until the operation releases that ownership.
On interruption, startup stays inhibited until explicit recovery verifies
allocation, ownership and operation state. E-design must specify that recovery
and tests for racing restart, crash and reopening, reusing existing lifecycle
locking/admission primitives where applicable. C5 provides the common admission
boundary; C6's restart-owner integration and generic-launch evidence prove that
public operations cannot bypass it. No new authority or locking implementation
is selected by these required outcomes.

### F — Secret-file delivery

Resolve declared references without secret leakage into metadata or logs,
materialize confined files for the approved recipient/mode, and handle rotation,
restart, rollback and removal. Existing private environment injection supplies
patterns, not a secret-file product contract.

### G — Network and endpoint compatibility

Adapt existing declared egress and endpoint semantics through the compatible
engine interface. Preserve separate network namespaces and denial of
undeclared peer reachability. Networkless mechanism probes cannot substitute
for these tests. No new discovery, networking policy or resource-limit product
is included.

### H — Shared interfaces and participant lifecycle

An approved interface design and disposable mechanism proof precede supported
cross-install attachment. Prepare dedicated runtime paths, safely reconcile
stale sockets, and admit only declared producers/consumers to the narrow
interface. Registry ownership, participant replacement, revocation and cleanup
must preserve independent private mappings and retained state.

### I — Component validation and final evidence

Adapt disposable Rspamd/Redis, Postfix and Dovecot fixtures to supported Reploy
behavior. The mail tests require a favorable compatibility disposition; a
blocker cannot be relabeled a pass. The combined interface campaign collects
positive and negative participant/lifecycle observations and produces the final
acceptance/gap report. Component defects return to their owning production
slice. Synthetic mail configuration and SQL fixtures do not authorize production
Flux deployment, daemon patches or a general database backup facility.

## Required design and evidence gates

Gates are required work within group delivery, not additional implementation
PRs. Human approval is required only for an unresolved decision about the
intended behavior or authority; evidence checks are evaluated against the
already approved contract. A design review stamp does not provide human approval.

| Gate | Prerequisites | Required disposition or evidence |
| --- | --- | --- |
| B-design | A1 | Explicit delegation recipient, supported host/session policy, bootstrap/revocation authority, registry ownership, lock scope and retention rules. The OS-user pool is a sizing assumption, not a selected default. |
| C-design | A1, B-design | Operation-by-operation compatibility disposition; concrete approach and <=750 upper-bound estimate for each required runtime change; explicit trusted supervisor ownership and launch/replacement boundary. Unsupported required operations block their dependent work. |
| D-design | A1, C-design | Image-locked identity declarations and role-specific capability syntax/allowlist, including default-empty behavior. Mail-specific bundles are not implicit. |
| E-design | B-design, D-design | Declared filesystem object/ownership rules, exclusive lifecycle ownership, restart inhibition and interruption recovery, retention authority and offline archive/translation contract. |
| F-design | D-design, E-design | Secret sources, recipients, confined paths, redaction and lifecycle contract. |
| G-design | A1, C-design | Exact adaptation of existing network/endpoint policy and observation/cleanup to the compatible engine. |
| H-design | D3, E6, E2 | Shared-interface identity/access proof criteria, participant registry/discovery authority and narrow attachment/revocation contract. H1 must pass its disposable proof before supported attachment is activated. |
| mail-compatible | A1 | Stock mail process/capability evidence satisfies the approved supervisor/child model, or the mail branch remains blocked for an explicit design decision. This gate does not block independent generic work. |
| generic-launch | B6, C7, D3, D5, E2, G2 | Both profiles pass supported-interface identity, capability, namespace, sandbox, pre-start mismatch, preparation/restart-race exclusion, ordinary owned-storage and active-revocation evidence. This is launch acceptance, not full lifecycle/sharing support. |
| completion | I4, E9, F3, G2, H5 | Every security-contract acceptance row has supported-interface evidence or an explicit unresolved blocker in the gap report. Full campaign completion requires every mandatory row to pass; a gap report alone cannot satisfy a failing row. |

## Activation and dependency order

1. Deliver A1 and retain the exact harness/host/image identities, fresh results,
   operation inventory and mail disposition. Fix the two confirmed P1 defects
   before rerunning payload probes. Mechanism conformance is the first checkpoint;
   backend/mail investigations follow it and may independently identify blockers.
2. Obtain B-design and C-design dispositions; begin B1–B5 and audit-bounded C1–C3
   as their prerequisites become satisfied. Bootstrap/registry paths remain
   explicit; no public start may use partial admission or cleanup behavior.
3. Approve D-design and the required E/G designs. Implement declarations and
   locked-image admission in D, then credential launch and supervisor lifetime
   in C4/C5, filesystem preparation in E1/E2 and network policy in G1/G2.
   These controls can be tested through internal integration seams while the
   public multi-identity activation remains gated.
4. Assemble C6/C7 and B6, then pass generic-launch. C6's internal tests provide
   real resources/restart owners for B6; approval of the public start follows
   complete revocation, cleanup and pre-start admission evidence. Generic-launch
   is an activation condition, not a prerequisite for constructing its tests.
5. Continue E3–E9, F and H in prerequisite order. A shared-path exception is
   activated only after the approved H-design and its mechanism/participant
   denial tests pass. A generation update never implicitly chowns existing data.
6. I1 may run after A1 on the existing capability-free path. I2/I3 wait for
   mail-compatible and the listed product prerequisites; I4 validates their
   shared interfaces and closes the final evidence report. Stop at the affected
   branch on a blocker and retain independent ready work.

Dependencies include named gates and slice IDs and are acyclic. A group can
span dependencies in another group; its letter is an ownership boundary, not
an instruction to deliver all its slices contiguously. Neither a helper test
nor a code change below 750 establishes support or security conformance.

## Acceptance ownership and publication checks

The security contract's 19 rows remain authoritative and unchanged. The table
assigns collecting/fixing responsibilities; passing a named implementation slice
does not automatically mark a contract row complete.

| Contract acceptance row | Owning delivery and evidence |
| --- | --- |
| ID-01 | A1 pre-payload proof; B1, B4, C3 and generic-launch |
| ID-02 | C4, C5, D1–D3 and generic-launch |
| ID-03 | C4, C5, D5 and generic-launch |
| ID-04 | A1 mechanism isolation; C6, G1 and generic-launch; H must preserve private boundaries |
| BE-01 | A1 Docker-negative proof; C1 and generic-launch |
| CAP-01 | C4, D4, D5 and generic-launch |
| LC-01 | E3, E4 |
| LC-02 | B4–B5, D3, E5 |
| LC-03 | E7–E9 |
| AL-01 | B3–B5, C3, E2, E6 |
| RV-01 | B6, C6, C7, E6 and generic-launch |
| HB-01 | B1, B2, C6 and generic-launch |
| SB-01 | A1 mechanism evidence; C3 and generic-launch |
| SB-02 | C4, D5, G1–G2 and generic-launch |
| FS-01 | E1, E2 |
| CF-01 | B1, C3, D2–D3, E3–E4, E8–E9 and generic-launch |
| SI-01 | H1 proof, H3–H4 and I4 |
| SI-02 | H1, H4 and I4 |
| SI-03 | H2, H5 and I4 |

PR-cycle approval requires every job generated by the current `ci.yml` for
that exact PR head. Derive the check list again at delivery time. At this local
baseline it is Linux CI checks, Bash native evidence checks, four Target smoke
jobs (linux-amd64, linux-arm64, darwin-arm64, darwin-amd64), and two Windows host
smoke jobs (windows-amd64, windows-arm64). Missing, pending, cancelled or failed
jobs block approval. Historical seven-job results are insufficient.

## Initial group A delivery specification

**Task A1** is the only initial delivery request. It is test/fixture code:
**0 production LOC, 650–1,300 test LOC**, plus prose and retained observations.
It does not add a public schema, change production runtime policy or approve
later host/supervisor choices. Its 500/750 production rule still applies if a
product defect changes that classification; return such work to a separately
bounded and approved production slice instead of absorbing it here.

Acceptance checkpoints inside A1:

1. Validate planned host UID/GID authority before mutation and gate payload
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
The existing open PR 69 owns the harness; delivery must resolve its exact
local/remote stack mapping before deciding whether to update it or publish a
follow-up. Do not silently create a duplicate or rewrite ancestors.

### Proposed AWD request — not executed by this design pass

```text
global:swe:deliver-design-stack(
  "docs/MULTI_IDENTITY_IMPLEMENTATION_PLAN.md",
  tasks="A1"
) -> produce run stats, timing, observed tokens and issues/opportunities
```

Stop after group A's reviewed delivery and readiness dispositions. The next
delivery request is selected from evidence-backed B/C/D prerequisites after
required human design decisions; it is not an automatic full-campaign run.

