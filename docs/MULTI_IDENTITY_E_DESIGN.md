---
status: Remaining-E LLD reviewed and maintainer accepted; public mapped execution remains inhibited
updated: 2026-10-30
summary: Concrete remaining E contracts, shared admission boundaries and source-based implementation slices after merged E2.2.
---

# Multi-identity E detailed design and remaining implementation plan

## Authority, scope and stopping point

This document refines the E contract in the [campaign plan](MULTI_IDENTITY_IMPLEMENTATION_PLAN.md).
Its other explicit governing dependencies are the authoritative
[security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md), the approved native-process
[mail reconciliation](MULTI_IDENTITY_MAIL_RECONCILIATION.md), and the
[D design](MULTI_IDENTITY_D_DESIGN.md). Those four documents remain unchanged.
Source, test receipts and supporting investigations are context, not extra authorities.

The input implementation is merged E2.2 at
`944d564fc3565cf91f3d72af4f4730fbd6155b3a`. Available prerequisites are E1.1's pure
identity closure, E1.2's native admission journal, E1.3's owned waiter and one-shot
release, E2.1's final credential transition, and E2.2's descriptor closure and
sandbox-proof boundary. Their implementation is retained, not scheduled again.
This plan covers only unfinished E2.3, E3.1, E3.2, E4.1 and E4.2.

The initial mapped cohort stays non-root staging/user-scope local rootless Linux
Podman. Docker, rootful, system-scope, remote engines and new native Windows/macOS
mapped support remain outside E. Exact and bounded mappings keep their approved
semantics. There is no default profile, global allocator or new public syntax.
OmegaFlow work is not a prerequisite. Public mapped entries remain inhibited
through E; F storage/network observations, G orchestration and the generic-launch
qualification gate after G3 remain required before activation.

The native application master owns its children and may use only admitted
workload authority. No privileged resident Reploy supervisor or application-callable
privilege socket is introduced. Same-UID roles do not create isolation.

## Existing contracts that remain fixed

- C is the selected configuration candidate; R is the complete retained reservation
  union; P is previously admitted authority. E1.1/E1.2 keep their distinction,
  exact/bounded rules, generation binding and never-admitted/dormant handling.
- The host owns the installation operation lock, current D image evidence,
  selected role, engine identity, exact create and pending native transaction.
  No caller-supplied geometry, role name or digest authenticates those facts.
- E1.3 starts only the immutable trusted waiter and consumes one bounded stdin
  release frame. The admission context, map revision, image, instance and plan
  bind that attempt. Partial/duplicate/stale frames, lost responses and timeout
  never authorize another release. Application input after the frame is data.
- E2.1/E2.2 establish exact final credentials and groups, P/E/B equal to the
  accepted ceiling, empty I/A and NNP. The final raw tail detaches the descriptor
  table with CLOSE_RANGE_UNSHARE, keeps constructed fd 0/1/2, then execs or exits
  the process. No Go cleanup or application callback runs after table closure.
- Empty capability entries retain ordinary locked NOROOT/drop-all behavior.
  Current nonempty startup tuples require UID 0. Nonzero UID plus nonempty caps,
  mapped private-environment transfer, observed-exit supervision and TTY transport
  remain unqualified and refuse. D's capability catalog is not a runtime allowlist.
- E2.2's sandbox request already binds exact generation/image, container/PID,
  runtime/network policy, required code and current complete resolved mounts.
  Missing, stale or substituted trusted F evidence denies release. Positive
  injected tests do not supply a production F provider.

## Private data and handoff contracts

These are internal Go values, not blueprint inputs, new persisted ledgers or
public comparison tokens. Use existing canonical digest and mapping types.

| Value | Closed contents and ownership |
| --- | --- |
| `mappedKernelPolicyV1` | Seed digest, selected native ABI, final uint64 capability bitmap, sorted setup capability names, canonical profile bytes and derived identity. E2.3.1 owns creation and defensive cloning. |
| `mappedEntryModeV1` | Fresh instance, occupied exec or host-native. E3.2 owns the enum/refusal helper; E4.1.1 derives it from the actual controlled entry, not a public safety assertion. |
| `mappedEntryPlanV1` | Installation, operation/run identity, selected build-lock/image/runtime/network digests, expected mapping revision, entry kind/mode, locked role/executable/argv/environment, UID/GID/sorted groups, active geometry, kernel policy and stream options. E4.1.1 owns construction from validated snapshots. |
| Extended sandbox request/proof | Add mandatory `KernelPolicy` digest to `mappedSandboxRequestV1` and `mappedSandboxProofV1`. The digest binds the compiler's ABI/cap/profile inputs; the existing request hash binds it to container/PID/image/generation and other policy/code/mount facts. E2.3.2 owns validation; F's trusted owner supplies actual observations. No missing-field compatibility fallback on release. |
| Owned instance | E3.1.1 combines the existing owned waiter, final plan, live-run/lease and transient-versus-master entry kind. Persistent identity uses the existing workload lifecycle record. Do not persist a copy of final authorization or invent an independent instance registry. |

E4.1.1 computes `Binding.Plan` as the canonical private entry payload digest before
constructing the binding; exclude the digest/binding field itself to avoid a
recursive identity. Include exact normalized startup arguments and kernel policy.
The trusted constructor supplies the binding, never a caller's arbitrary digest.
Earlier E2.3.2 integration extends the existing sandbox request hash and verifies
the derived policy against the trusted startup inputs and exact instance. It
does not construct a final entry plan or introduce a temporary plan digest.
Its fixtures retain their existing trusted binding; production entry integration
later requires E4.1.1's complete binding through E4.1.2.
The binding revision is the expected pending-operation revision for that attempt.
The admission writer advances state; retain its returned accepted state/revision
without mistaking that expected advance for stale input or reusing the old plan
for a second operation.

The fresh runtime observer returns exact container ID, image/owner labels,
running/exited state, process/start identity, automatic-removal setting and restart policy/count through the
existing pinned endpoint. A running observation does not acknowledge exec.
Existing phase-specific trusted effect evidence alone may resolve the native
release journal; exact retirement must be proved before its recovery writer
clears uncertainty. Run queue/lifecycle completion under the existing operation
lock and call the existing native recovery methods without nested lock ownership.

## 1. Derived mapped syscall policy

### Pure compiler and identity

Add one private mapped-policy compiler beside `podman_seccomp.go`. Inputs are the
fixed embedded seed, target ABI, exact final capability bitmap and exact setup
capability set. The caller derives capabilities from the locked role and required
trusted setup; the compiler does not authorize them. Setup is the union of the
accepted ceiling and individually required existing setup capabilities, never an
implicit application grant. Sort/deduplicate canonical inputs and reject unknown
ABIs, bits outside the kernel/catalog or contradictory tuples.

Verify the embedded seed digest before parsing. Retain its default-deny action,
ordinary unrelated rules and their capability conditions. Remove every allow for
`mount`, `umount`, `umount2`, `pivot_root`, `fsopen`, `fsconfig`, `fsmount`, `fspick`,
`open_tree`, `move_mount`, `mount_setattr`, `setns`, `unshare`, `open_by_handle_at`
and `name_to_handle_at`, including names grouped inside capability-conditioned
rules. Remove empty rules. A later seed change requires review of this transform;
unknown policy shapes/actions reject instead of passing through unchecked.

Replace all seed clone/clone3 rules. For each admitted ABI, allow legacy clone
only when its flag argument has no namespace-creation bits. Include NEWNS,
NEWCGROUP, NEWUTS, NEWIPC, NEWUSER, NEWPID, NEWNET and NEWTIME. The closed ABI
table is amd64 → X86_64 and arm64 → AARCH64; legacy clone flags use
argument 0 for these native ABIs. Retain only the selected native architecture;
remove its compatibility subarchitectures. The resulting filter must reject
compatibility and unknown audit architectures, not merely omit them from tests.
Do not infer support from the seed's broader architecture list. Adding an ABI
requires separately bound qualification of its syscall interpretation and positive
controls before acceptance. The namespace mask is 0x7e020080,
checked against target headers. Production tests cover each admitted ABI and
refuse unknown entries before create. Return ENOSYS for
clone3 regardless of capabilities, allowing qualified libc fallback. Preserve
ordinary fork/vfork/thread creation and required accepted setuid/setgid/chroot
operations. Do not broaden a rule because a setup phase needs extra authority.

The derived profile has a private canonical identity over seed digest, admitted
ABI set, final/setup capability sets and exact canonical policy bytes. Its size
is bounded by the fixed seed and fixed replacement rules. No user-selected policy
path, unconfined fallback, policy service or new persisted authorization cache is
added. Ordinary unmapped profile bytes and validation remain unchanged.

### Mapped create and pre-release evidence

Keep `podmanApplicationCreateRequest` strict for ordinary application setup.
Introduce a private typed mapped wrapper with mandatory compiler output. It first
uses the existing strict generated-command parser, then applies only the mapped
capability union, exact derived inline profile and fixed native launch arguments.
It cannot accept arbitrary CLI options or an arbitrary profile string. All mapped
prepare/create/observe/sandbox-request paths use this same representation.
The constructor requires that its final capability mask equals the trusted
startup argument. Container root/setup and final application authority stay
separate. Retain immutable-image, read-only-root, no-new-privileges, exact maps,
no engine restart, endpoint pinning and resource-owner checks.

The mapped overlay always transmits `AutoRemove=false` and
`RestartPolicy={Name:"no",MaximumRetryCount:0}`. Preserve ordinary request
serialization and its `--rm` behavior; use the mapped wrapper for these fields.
Before release, require explicit matching automatic-removal and restart-policy
observations and `RestartCount=0`. Missing or conflicting fields refuse. Only
the exact host owner removes the instance after observing or conservatively
recovering its outcome; an exit or lost attach response must not erase ownership
through engine auto-removal.

Include the policy identity in the sandbox request's hashed expected facts.
E4.1.1 later includes it in the canonical `mappedlaunch.Binding.Plan`; E2.3.2
does not depend on that later constructor. Extend the trusted effect-owner response with the selected
kernel-policy identity and current observation binding. Reject absent/mismatched
policy, capability or ABI evidence before release. The trusted owner runs inside
the existing admission writer and must not reacquire OperationLock.

Engine-reported policy strings are provenance. `Seccomp: 2` shows filter mode;
neither alone establishes effective semantics. Qualifying a runtime tuple requires
the actual derived bytes through the selected engine/libseccomp/OCI path, exact
create/process observations and paired syscall controls. If compatible metadata
or sufficient evidence is unavailable, that tuple refuses. No post-start payload
inspection is substituted for the pre-release gate. E implements the closed
request/verifier and negative behavior; production F effect observations and
public activation remain independent gates.

## 2. One owned instance lifetime

### Ownership and phases

Add a mapped branch at the existing admitted-container/lifecycle boundary, not a
second supervisor or database. Its private input uses the already implemented E4.1.1 final entry plan and holds the existing operation
lock, run/lease identity, native pending operation, exact generation/map revision,
compiled final plan and owned waiter. It cannot adopt a container selected only
by name. Use existing live-run queue entries for transient instances and existing
workload-lifecycle records for persistent instances; both retain exact container
ID, engine binding and the selected generation/runtime binding.

| Phase | Required action and failure rule |
| --- | --- |
| Planned | Validate exact current locked state and entry. No engine effect on rejection. |
| Intent/create | Persist native intent and exact owner labels; create once. Lost response retains uncertainty and owned-object evidence. |
| Waiting | Hold admission ownership, verify complete maps/process/policy and trusted F facts. Do not unlock as the ordinary transient runner does before start. |
| Release attempted | Commit native acceptance and exact live ownership under the lock, then send the single frame. A frame write is not proof of application startup. |
| Attached run | Release the operation lock only after the release attempt/ownership is recorded. Retain the existing live-run lease, bounded cancellation and exact instance identity. |
| Exit/cancel | Observe the owned instance result; stop/clean that instance as required, then reacquire the existing lock and complete its queue/lifecycle ownership. |
| Cleanup uncertain | Retain exact container/operation ownership and reservations; report recovery required. No successful completion or replacement while absence is unproved. |

PID/start identity, immutable image, owner labels and restart count must remain
bound to the created instance. Application output cannot establish startup,
ownership, completion or release acknowledgement. Preserve the native journal's
release-attempted uncertainty until its existing trusted evidence verifies actual
acknowledged execution or exact retirement. Mere engine presence/absence does not
clear it. An admission write or a still-running trusted waiter cannot mark a
persistent workload startup complete.

The required foreground native master's exit ends that container instance. A
finite app command, test or hook completes only its own entry. Worker fork/exec
and replacement stay inside already accepted authority and are not new host
admission. Do not reintroduce an ordinary stronger observed-status supervisor on
the mapped path. Retain status/cancellation/error propagation and never shrink R
or release dormant IDs as execution cleanup.

### Fresh replacement and persistent handoff

A host-admitted replacement first proves the old exact instance is stopped and
retired under its existing ownership. It then reloads current generation, mapping
revision, host principal/delegation and entry policy, reconstructs E4.1.1's final
entry plan, and starts a new operation
through the same owned-waiter gate. A retry never resends an old frame or reuses
an old accepted plan. Service restart is a caller of this operation; engine
restart stays disabled. G owns public service-manager integration and revocation.

Persisted lifecycle state distinguishes starting/up/stopping/down using the
existing validated schema. Up requires the existing trusted startup-completion
observation for the exact instance, not a frame write or application marker.
A detached handoff additionally requires qualified stream detachment and a fresh
matching live-instance observation; loss of the foreground owner before such a
handoff follows bounded stop/cleanup. No unqualified detach is exposed as success.
After a proved detached handoff, kernel/container master lifetime remains the
instance boundary and no continuously connected host broker is required.

E supplies the private lifetime/replacement operation, typed observation
requirements and fixture tests for confirmed/uncertain handoff. F/G supply their
production effect/service integration before detached public support. Missing
proof keeps the path closed; do not build a new persistent Reploy authority to
fill an observation gap. Future I secret delivery is once per admitted instance,
not per worker or repeated release attempt. No secret feature is implemented in E.

## 3. Occupied-container entry boundary

There is no qualified mapped occupied-container exec tuple in this plan. Add a
typed private request classification for fresh instance, occupied exec and
host-native execution. Preserve entry semantics: an operation that requires an
occupied namespace returns an explicit unsupported-entry error before creating an
exec object, attaching or starting a helper. Do not substitute a fresh container.
This slice owns the small private entry-mode enum and refusal helper; E4.1.1
reuses that classification. The helper takes the existing validated owner state
and a trusted entry intent; it does not accept a public qualification flag.
Caller-provided capability names, UID strings or expected-state identifiers cannot
authorize a stronger temporary process there.

Keep a bounded test harness capable of checking any future proposed transition
against existing same-UID processes, ptrace/proc access, capabilities and private
file/directory/socket/pidfd/high descriptors. The current slice establishes
pre-effect refusal and the harness; it implements no live-exec enablement. A
future successful mechanism requires separately reviewed evidence and an approved
E3.2 decimal amendment. There is no privileged/raw-exec fallback.

## 4. One final entry-plan constructor

Build a private immutable mapped-entry plan from current locked blueprint, D
policy/image evidence, requested controlled entry and argument/environment plan.
Use existing command/executable resolution; do not invent another role registry.
The closed entry kind identifies workload start, application command, runtime
test, shell, controlled session, lifecycle hook or host replacement. Entry mode
separately identifies fresh instance, occupied exec or prohibited host-native use.

The constructor is pure: its caller supplies validated build/state snapshots
from the existing held-lock readers. It verifies their consistency but does not
authenticate a public caller or replace fresh E1.2 admission checks.

The plan binds installation/operation, generation and image, map revision,
selected role and executable, all UID/GID/groups, final/setup capability sets,
active maps, runtime/kernel/network policies, protected code and requested stream
mode. Canonical cloning prevents later caller mutation. `Binding.Plan` is computed
from those private facts including exact normalized launch arguments; it is not
accepted from a public caller. Runtime-required accounts come from explicit
locked evidence and cannot enlarge configured/accepted mappings.

The constructor rejects stale generation/image/policy, unresolved role/executable,
unsupported credential/capability tuple, private-env/observed-exit/TTY mode,
host-native mapped execution and occupied exec. Reuse E1.1's closure planner and
E1.2's writer; the final plan itself performs no mutation and grants no authority.

Every controlled entry's mapped branch calls this constructor and its applicable
owned-instance runner or returns the same pre-effect unsupported result. Keep
public `unmappedRuntimeDocumentV1`/equivalent guards in place. A passing private
fixture cannot remove them. For a two-deployment controlled session, both owners
are checked; the session transport remains unqualified where it needs TTY or a
startup authority combination not covered by the current proof. Hook/test/app
completion must not be classified as required-master failure. G performs later
public orchestration wiring through this same boundary, not a raw engine path.

## 5. Slice inventory and dependency order

Existing E1.1, E1.2, E1.3, E2.1 and E2.2 are prerequisites, not unfinished slices.
Preserve their original IDs, source, review evidence and completed campaign entries.
The proposed replacements are E2.3 → E2.3.1/E2.3.2,
E3.1 → E3.1.1/E3.1.2, and E4.1 → E4.1.1/E4.1.2. E3.2 and E4.2 retain their IDs.
Parents remain historical owners until this exact proposal is approved and adopted
through supported campaign re-plan tooling. Do not reserve children or mutate
existing delivery counters/budgets during design. This table is the one ordered
unfinished implementation inventory. IDs retain responsibility identity; delivery follows
this dependency order rather than numeric sorting. The refusal and pure planner
move ahead of lifetime integration so those slices consume an existing contract
instead of inventing temporary planning logic. No parent is declared complete
before all its children and required later integration pass.

| Slice | Concrete responsibility and owned paths | Prerequisites | Completion check |
| --- | --- | --- | --- |
| E2.3.1: Compile mapped syscall policy | Pure pinned-seed transform, ABI clone/clone3 rules, canonical capability inputs and derived identity. Own private mapped-policy compiler beside `dockerdeploy/podman_seccomp.go`; ordinary seed/materialization unchanged. | E2.1, E2.2 | All forbidden names are absent from every allow regardless of seed capability conditions; canonical identity changes with any relevant input; ordinary path unchanged; native positives, compat/unknown rejection and fork/thread cases covered. |
| E2.3.2: Apply and verify the mapped policy | Mandatory typed mapped create wrapper; exact final/setup caps, inline profile, no auto-removal/restart and pre-release observation contract. Own mapped create/waiter/sandbox integration and focused fixtures; final entry-plan construction stays in E4.1.1. | E2.3.1 | Wrong/missing profile, tuple, ABI, removal/restart observation or owner proof refuses before frame; no ordinary-parser relaxation; exact passed create policy and current observation bind to the same instance. |
| E3.2: Refuse unqualified occupied exec | Typed entry-mode boundary and retained adversarial safe-entry harness. Own mapped exec classifier and affected controlled entry guards; no enablement. | E2.3.2 | No engine exec-create, helper or attach for every unqualified mapped occupied request; entry semantics preserved; no stronger or raw fallback. |
| E4.1.1: Construct the final entry plan | One pure closed entry-plan constructor from current locked D/generation/role facts, maps, policies and stream mode. Own private dockerdeploy planner and exact identity tests. | E3.2, E2.3.2 | Complete exact canonical plan or deterministic rejection; no caller mutation, implicit grants or I/O; stale inputs and unsupported modes refuse. |
| E3.1.1: Own mapped instance completion | Connect held-lock native release to existing live-run/lifecycle ownership, master exit, cancellation and exact cleanup. Own mapped branch near `live_run_container.go`, using existing native journal/lease methods. | E1.3, E2.2, E4.1.1 | Release stays fenced; unlock only after recorded ownership; finite entries vs master exit distinguished; all cleanup failures retain exact recoverable ownership and R/P history. |
| E3.1.2: Admit a fresh replacement | Fresh host replacement and persistent lifecycle/handoff contract using existing records. Own mapped branch near `current_workload_lifecycle.go`; no public service/unit changes. | E3.1.1 | Old instance retired before new admission; current authority/generation reloaded; engine restart refused; ambiguous acknowledgement or detach stays uncertain; missing F/G handoff evidence refuses. |
| E4.1.2: Connect every controlled entry | Private/guarded mapped branches for workload/app/test/shell/session/hook/replacement use the shared constructor/runner. Own existing entry adapters and their parity fixtures. | E4.1.1, E3.1.2 | Every listed entry reaches the shared boundary or rejects before effects; both session owners checked; public guards and ordinary behavior preserved; no host-native shortcut. |
| E4.2: Qualify and report E conformance | Cross-entry/profile authority/kernel/lifetime checks and evidence-producing harness. Own E test fixtures and qualification reports; no new production behavior, F provider or G activation. | E4.1.2 | Integrated private journey covers both map profiles, production startup authority, exact effective policy and failure/recovery; unavailable real-runtime cases remain explicit blockers, never support claims; actual source/compiler/tuple bound to receipts. |

## 6. Estimates and counting convention

The unit is a changed physical text line: additions plus removals against each
accepted parent, including changed blank/comment lines. Production/configuration
counts shipped runtime/build/packaging changes. Test-code counts tests, test-only
helpers, textual fixtures and test configuration. Count each line once. Exclude
Markdown design/review material, Changie prose, generated/vendor/lockfile churn
and binary payloads; disclose material non-LOC work separately. Shared compiler,
runner, planner and fixtures each have one owner above. No working-hour or token
forecast is asserted. Target 500 production LOC per PR; hard maximum 750.

| Unfinished leaf | Production/configuration LOC | Test-code LOC |
| --- | ---: | ---: |
| E2.3.1 | 200–350 | 450–700 |
| E2.3.2 | 300–450 | 650–1000 |
| E3.1.1 | 250–400 | 550–850 |
| E3.1.2 | 250–400 | 500–800 |
| E3.2 | 75–150 | 300–500 |
| E4.1.1 | 250–400 | 450–700 |
| E4.1.2 | 250–400 | 600–900 |
| E4.2 | 0 | 650–1050 |
| **Remaining E total** | **1575–2550** | **4150–6500** |

These are source-based planning ranges, not measured results or padding targets.
E3.2 is a small pre-effect boundary; E4.2 is test-only and receives no production
padding. Missing production behavior discovered there returns to its owning
slice for reviewed correction.

Actual runtime/ABI qualification, disposable host access, CI/remote review waits,
evidence collection and later F/G integration are material work outside a simple
LOC forecast. Reuse exact applicable mechanism evidence. If implementation
requires new authority, a stronger resident process or work beyond a coherent
slice's bound, stop and re-plan the affected scope before proceeding.

## 7. Requirement coverage and release gates

| Obligation | Owning unfinished leaf and observable check |
| --- | --- |
| Capability-conditioned effective filter and clone ABI/fallback | E2.3.1/.2: seed-condition negatives and actual selected-runtime fork/thread/identity/chroot positives; namespace/mount/handle negatives. |
| Default immutable code and protected loader/dependency boundary | Existing E2.2 proof interface, retained by E2.3.2/E4.2; missing/substituted F code/mount/network proof blocks release. F owns production derivation/storage. |
| Native-master lifetime and fresh host replacement | E3.1.1/.2: master exit ends instance, finite entries finish normally, no engine restart/replayed release, exact cleanup and restart fencing. |
| Safe occupied namespace and private descriptors | E3.2 refusal before any exec effect, plus existing E2.2 fd transition and E4.2 adversarial checks; future enablement needs separate amendment. |
| Same authority on every controlled entry | E4.1.1/.2 and E4.2: complete entry matrix, both session owners, no host-native bypass, exact role/caps/maps/generation and private-descriptor checks. |
| Actual qualification vs injected mechanism fixtures | E4.2: bind each case to code/compiler/architecture/profile/engine/OCI policy; repeat fresh-instance positives at least three times per profile; retain controls and failures. |
| Ownership retention and later secrets/services | E3.1.1/.2 preserve native journal, R/P/dormant history, instance boundary and conservative recovery; G/I retain public services and once-per-instance secret delivery. |
| Public execution and unrelated consumers | E4.1.2 retains guards; F/G/generic-launch stay separate; OmegaFlow is not a dependency and Docker results do not qualify mapped Podman. |

E's integrated internal journey is locked D plan → pure closure → native intent →
exact owned waiter → effective-policy and trusted F observation gate → one-shot
release → native authority/streams → exit/cancel → owned cleanup and retained
state. Test interrupted and uncertain outcomes at every arrow. Include omitted
runtime accounts, extra/wrong maps, stale PID/image/revision/host authority,
private/high/concurrently opened fds, set-ID/file-cap/ambient/inheritable/securebits
regain, ordinary fork/thread/chroot/identity transitions, namespace/mount/handle
escapes, wrong-owner cleanup and missing proof. A negative passes only when it
also checks that application execution did not occur where required.

Internal implementation can complete with the F provider and public activation
unavailable, provided required refusal and exact trusted-interface tests pass.
The separate real-runtime qualification matrix must report each supported,
unsupported or untested tuple honestly; tests using injected evidence cannot be
renamed production qualification. No release or mapped support claim follows
from this design, a clean document stamp or the completion of E alone.

## Supporting evidence and continuation

The [prior E design](supporting/MULTI_IDENTITY_E_DESIGN/pre-replan-E-design.txt)
retains completed-slice specifications, historical investigation and earlier
estimates. [Engineering context](supporting/MULTI_IDENTITY_E_DESIGN/engineering-investigation.md)
records current source reuse, the bounded syscall mechanism and its limits.
[Sizing basis](supporting/MULTI_IDENTITY_E_DESIGN/sizing-basis.md) and review/approval
records live beside it, outside this clean LLD.

Before implementation, obtain exact current review and maintainer approval of
this remaining inventory, refresh affected stamps through supported helpers,
checkpoint/publish the design and complete its PR cycle with every current
ci.yml job passing for the exact head. Use supported campaign reconciliation to
adopt the exact separately approved legacy replacement snapshot through
`swe_delivery_state.py apply-transition` with `transition_kind=campaign-replan`.
Preserve the completed queue/mappings, existing work reservations, cumulative
counters/budgets and historical assessments; let supported tooling reconcile
new children rather than silently resetting or double-counting work. Do not use
standardized-owner `replan-slices` to migrate this legacy campaign or rewrite
approved ancestors. Then, only on delivery authorization,
resume the existing E campaign at its actual retained stage. Stop after E4.2 and
report actual approved PRs, production/test LOC, timing, observed token data if
available, remaining gates and the proposed F sprint. No automatic merge/release.
