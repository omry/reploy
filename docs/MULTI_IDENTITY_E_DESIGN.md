---
status: Proposed E implementation plan; not implementation or runtime qualification
updated: 2026-10-28
summary: Host-admitted native process startup, exact final authority, lifecycle and entry-point qualification.
---

# Multi-Identity E Design

## Authority and delivery boundary

This LLD refines E1–E4 in the [implementation plan](MULTI_IDENTITY_IMPLEMENTATION_PLAN.md).
Its explicit design dependencies are that plan, the authoritative
[security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md), the approved
[mail reconciliation](MULTI_IDENTITY_MAIL_RECONCILIATION.md), and the
[D design](MULTI_IDENTITY_D_DESIGN.md). Source code, upstream specifications and
private experiments are context, not additional normative authorities.

Baseline: merged D at `8a0f78c19f15e007db9a28344dd6754f0a9587a7`.
D supplies canonical users, groups, roles, explicit capability requests,
immutable-image account evidence, coverage and generation invalidation. It does
not authorize runtime grants. This document proposes E internals and decimal
slice boundaries. It does not change approved public syntax, mapping profiles,
installation scope or trust expectations. No E implementation is delivered here.
The four dependency documents remain unchanged.

The initial execution cohort remains non-root staging/user-scope installation
on local rootless Linux Podman. Docker, rootful, system-scope and remote mapped
execution remain outside scope. Public mapped execution remains inhibited until
generic-launch after G3. Completing E does not establish F storage/network,
G activation, H restore/ownership, I secrets, J interface or K release qualification.

## Direction: reuse host admission and direct exec

Use the existing installation operation lock, retained mapping state, owned
Podman create seam, live-run queue and host service controller. Add an explicit
mapped branch to the existing startup verifier. That branch waits while the host
checks the new container, then drops setup authority and directly execs the
selected application. Postfix or Dovecot manages its own native children. There
is no resident privileged Reploy supervisor, broker, allocator, new scheduler,
or application-callable privilege socket. Existing ordinary startup remains strict.

The alternatives are inferior for the approved requirement: reusing the ordinary
helper unchanged loses every requested capability; extending a permanent
identity supervisor restores a process model the mail amendment replaced;
launching a privileged helper through live exec creates an unproved stronger
process beside untrusted code. None is the default or a fallback. Unsupported
runtime combinations fail before application effects.

Roles select initial credentials and requested ceilings for Reploy-controlled
entries. They do not isolate same-UID processes or constrain a native master with
SETUID/SETGID to role transitions. Exact maps expose the explicitly accepted
container IDs. Bounded maps expose their whole accepted range. The administrator
coordinates host ranges across independent installations; Reploy checks current
host authority and preserves reservations but does not allocate globally.

## Current code and required seams

| Existing code | Reuse | Required E change |
| --- | --- | --- |
| `deploy/identity_mapping_state.go`, `identity_mapping_operations.go` | Revision, generation, held-lock mutation, protected/dormant reservations, uncertain-effect recovery | Native process binding and explicit execution-acceptance transition; preserve legacy supervisor meaning |
| `deploy/identity_mapping_effects.go` | Fresh host observation tied to the pending operation | Keep configuration-candidate equality; separately validate accepted execution geometry |
| `dockerdeploy/podman_mapping_compile.go`, `podman_mapping_create.go` | Interval projection through current parent maps, immutable image, local endpoint/peer checks, exact owned create | Kernel-map attestation, trusted wait/release and admission receipt |
| `probe/sandbox_exec_linux.go`, `startup_exec_linux.go` | Locked OS thread, trusted setup, direct exec, ordinary capability clearing | Separate exact nonempty-capability path, all-thread and descriptor proof |
| `dockerdeploy/application_sandbox_plan.go`, `podman_application_create.go` | Ordinary strict sandbox and compatible API transport | Typed mapped plan and explicit setup/app capability separation; do not relax ordinary validation |
| `dockerdeploy/podman_seccomp.go`, `runtime_policy_compile.go` | Pinned policy provenance, protected-path and mount collision checks | Mapped effective kernel policy and immutable-code closure |
| `dockerdeploy/live_run_container.go`, `deploy/live_run_queue_file.go` | Owned container/lease recording, cancellation, completion and cleanup | Native-master lifetime and fresh replacement admission |
| Current workload/command/test/shell/session entry planners | Existing role resolution, queue/lock integration and early guards | Shared mapped planner behind retained public inhibition; no raw engine bypass |

Paths in this table are under `internal/`. The existing compatible-create seam
is not production admission: its fixture deliberately refuses lifecycle admission.
The ordinary transient runner currently unlocks before container start. The
mapped path must retain or reacquire the admission lock and revalidate its fence
before release. It cannot inherit that ordering unchanged.

## Internal contracts and state

### Invocation-local plan

Add one typed mapped entry plan to the existing execution planning boundary.
Provisional Go names below describe responsibility, not new public API:

- `MappedEntryPlanV1`: installation/owner identity, operation/run ID, exact
  generation and mapping revision, immutable image ID, entry kind, role,
  final UID/GID and supplementary groups, accepted capability set, active
  UID/GID maps, protected code paths and effective policy identity.
- `MappedContainerObservationV1`: exact owned container/image ID, live PID and
  process start identity, complete kernel maps, namespace identities, current
  engine/API/runtime tuple and all required isolation observations.
- `MappedReleaseV1`: the in-memory result of checking that plan and observation
  against the current held-lock state. It is consumed by that attempt only.

These values are not reusable bearer tokens, durable approval caches or a
second policy database. Recompute plans from the generation's locked D evidence
and fresh host/runtime facts. Reject unknown fields/versions, malformed intervals,
integer overflow, noncanonical groups, image or generation mismatch and any
unsupported entry before create. Never execute image NSS to resolve identities.

### Retained state and acceptance

Keep the existing revision, installation binding, accepted assignments, dormant
IDs, admitted generation, engine binding and pending operation journal. Introduce
an explicit native process-model discriminator in a versioned state record.
Recommended encoding: `identity-mapping-v2` with
`process_model: native-master-v1`, existing engine binding and no supervisor
binding for native admission. Preserve v1 reading and its supervisor semantics.
Old readers must reject v2 rather than reinterpret it. Do not write a fake
supervisor identity, silently clear a legacy binding or infer conversion from
new generation contents.

New native installations and never-admitted inhibited intents can enter v2
through an explicit checked operation under the existing lock. A legacy admitted
supervisor installation remains reserved and inhibited for the native path;
process-model migration is separate work, not an automatic upgrade in E.
Failure to support that legacy case must identify the conflicting binding and
leave its reservations unchanged.

Configuration intent and execution acceptance are distinct checks. B's
`ObserveEffects` continues to require the active geometry to equal the pending
configuration candidate. E's acceptance step checks the complete generation
closure against that candidate and the previously accepted retained ledger.
Every active exact ID must be declared or an explicitly accepted, image-locked
runtime account. Every newly introduced exact assignment must be active; surplus
new assignments are rejected. Previously admitted assignments may remain dormant
and reserved without appearing in active kernel maps. Bounded intervals keep
their approved full authority and do not invent names for unused numbers.

Do not make runtime-required accounts automatic. For example, the private Podman
fixture needs GID 5 for devpts. Production acceptance requires exact image evidence
and the explicit runtime-account decision prescribed by the architecture. A
missing account or unsupported runtime tuple is an actionable admission error.

### Admission ordering and crash behavior

| Phase | Permitted action | Failure / crash handling |
| --- | --- | --- |
| Locked plan | Validate owner, host delegation/principals, generation, retained mapping state, entry and qualification | Refuse without engine effects; preserve prior accepted ledger |
| Owned create intent | Journal the operation and exact owner labels; create immutable-image container running only trusted waiter | Uncertain POST remains journaled; discover only by exact ownership evidence, never blindly create again |
| Trusted wait | Start waiter with bounded timeout; pin process identity; read complete kernel maps and isolation | Missing/extra map rows, stale PID, wrong namespace/owner/image, disconnect or timeout prevents release and triggers owned cleanup |
| Locked release | Recheck current operation/revision/generation, host authority and container observation; require F-owned storage/network proof | A changed fence invalidates the attempt; do not release on earlier evidence |
| Accepted release | Persist admission and live-container ownership under the lock, then release waiter once | Lost release response is uncertain execution; retain reservation and ownership until exact reconciliation; never issue an unbound second release |
| Running / exit | Host controller observes master/container, closes lease, performs owned cleanup | Cleanup failure retains ownership and IDs; recovery cannot manufacture success or re-exec payload |

The host, not application output, owns the admission verdict. For a fresh
container, reuse the existing host command/session attach transport. Start the
trusted waiter blocked on its initial stdin request; do not require a separate
socket, mounted control file or resident relay. After the locked checks, the
host sends one 4-byte unsigned big-endian length followed by that many UTF-8
JSON bytes, bounded to 64 KiB. The closed version-1 request binds installation,
operation/run ID, generation, mapping revision, exact container/image ID, role
and the canonical final-plan digest fixed in the trusted launch arguments.
Reject duplicate/unknown keys, invalid length/version/digest or binding mismatch.
The digest binds an attempt; it is not a secret or reusable authorization token.

The waiter consumes exactly this frame using unbuffered reads on fd 0. Remaining
bytes are application stdin and must neither be prefetched nor parsed as a second
request. EOF, cancellation or the existing command-admission deadline before a
complete frame aborts without application exec; there is no retry on that waiter.
Host application input is sent only after the release frame. The final exec
preserves constructed streams only; there is no remaining control parser or
private descriptor. A literal copy of a frame in later application stdin has no
control meaning. A new host operation always creates a fresh attempt.

Container identity and process readiness come from the owned runtime/process
observations, not a marker in application logs. A lost attach/release response
is uncertain execution, never evidence to send the frame again. E1.3 must test
partial/coalesced frames, EOF, stdin canaries and all planned stream modes.
Raw TTY modes cannot carry this frame unless the trusted adapter proves an
unambiguous pre-application handoff; an unqualified mode rejects before create.
The disposable SIGUSR1 fixture tests the underlying startup transition, not this
production transport protocol or TTY support.

The lock need not remain held for the application's lifetime. It must fence the
plan, create identity, state acceptance and release. Cancellation, lock loss or
controller restart before release cannot default to application startup. A crash
after recording accepted state but before confirmed exec leaves an uncertain
owned instance; recovery checks it and cleans up or reports uncertainty. It does
not treat a recorded admission as permission to launch another instance.

## Final credentials, code and kernel policy

### Capability transition

The trusted setup capability set is the minimum needed for the selected setup,
plus the explicitly accepted application ceiling. Setup-only SETPCAP, CHOWN or
NET_ADMIN must not become implicit application grants. The final verifier checks
all real/effective/saved/filesystem UID/GID fields, exact supplementary groups,
P/E/B equal to the accepted ceiling, I/A empty and NNP set. Verify the post-exec
application state through a trusted test payload; a pre-exec capset success is
not proof that exec preserved the set.

For the empty ceiling, preserve the ordinary locked-NOROOT, drop-all, NNP path.
For a nonempty ceiling, use a separately qualified securebits/direct-exec path;
blindly applying NOROOT destroys required capabilities. The fresh experiment
qualified the underlying transition only at UID/GID 0:0 in a private user
namespace. It did not qualify every identity/capability combination. Nonzero UID
with nonempty P/E requires a distinct successful mechanism proof before support;
otherwise reject that tuple. Do not add ambient capabilities, file capabilities,
set-ID recovery or an implicit root-only portable policy to fill this gap.

The implementation must address Go's process/thread semantics explicitly. All
threads that can reach the application must have the final authority, and no
stronger thread may survive exec preparation. Do not claim that locking one OS
thread alone proves this. The E2.1 implementation spike must choose and test the
smallest reliable arrangement using existing helper/process primitives; if a
new permanent helper or trust model appears necessary, stop for realignment.

Close every private descriptor before application exec. Preserve only explicitly
constructed stdin/stdout/stderr and approved application handles. Test leaked
control sockets, namespace/root directory descriptors, pidfds and pre-opened
files outside a later chroot. CLOEXEC marking, enumeration and exec must not
race with a stronger thread opening descriptors. A final clean fd listing alone
does not establish how earlier access was prevented.

### Effective policy and immutable code

Derive one mapped effective seccomp policy from the existing pinned seed and the
accepted tuple. Preserve provenance and default-deny behavior. Explicitly deny
namespace creation/join, namespace-bearing clone flags, mount APIs and handle
escapes, even when a capability-conditioned seed rule would permit them.
Include both legacy and new mount/handle APIs. Preserve ordinary fork/vfork and
thread creation; clone argument position depends on ABI. Deny clone3 with ENOSYS
when the supported libc requires fallback, and prove the fallback works. Do not
weaken denials merely because the setup phase needs different authority: setup
and final application filters must compose monotonically.

The seed currently enables `open_by_handle_at` with DAC_READ_SEARCH and broadens
namespace/mount calls with SYS_ADMIN. Thus its name/hash is provenance, not a
sufficient effective policy. The portable D catalog accepts names that E may
refuse to grant. No automatic capability bundles or qualification by name alone.

Image code, interpreter/loader and runtime dependencies stay immutable by default.
Reuse protected-path compilation and reject writable mount shadows, symlink
replacement and writable dependency search paths. F owns final mount/storage
construction and any explicit code-mutation input. E defines the checked proof
input at its existing sandbox-plan boundary: exact generation/image, protected
code closure and mount/network observations bound to the owned container. Missing,
stale or mismatched proof denies release. E tests that interface with injected
positive and negative observations; F later supplies the production proof.
Until then, product release stays closed. No new blueprint spelling
for mutation is introduced here. Explicit future opt-in cannot make the trusted
Reploy startup code writable. Binary-exclusive privileges remain deferred as
approved; immutable code does not prevent a privileged native master from using
its accepted authority or executing other permitted immutable code.

## Native lifetime, replacement and live exec

The required foreground native master defines the instance lifetime. Its exit
ends that container; do not restart only the failed required process in place.
A native master can manage its workers within its accepted authority. Completion
of a finite command/hook/test has that entry's normal meaning, not a workload
master failure. Reuse the existing queue, leases, stream cancellation and cleanup
ownership. Keep any ordinary observed-status supervisor out of the mapped native
path if it would retain stronger credentials or act as a privilege broker.

Disable autonomous engine restart for mapped instances. Host user-service or
controller replacement must enter the full fresh admission path with current
host authority, generation and map revision. Native worker recreation is not a
new instance. Future I secret delivery remains once per admitted instance, not
once per worker or repeated release attempt; E provides the instance boundary.

Live exec into an occupied container has a different proof obligation. Do not
start a stronger temporary root/setup process there. Any entry verifier must
have no more than the requested accepted authority before it becomes observable
to existing untrusted processes, and cannot carry private authority descriptors.
The stock compatible API's User/capability settings do not by themselves prove
this ordering. Source inspection of crun 1.14.1 shows namespace joining before
the exec entrypoint's final capability setup; it does not establish safe entry.

E3.2 delivers deterministic refusal for unqualified live exec before creating an
exec object and a bounded proof harness. Safe live-exec enablement is not included
in that slice's estimate. If investigation establishes a viable transition,
first review its exact evidence and separately estimate an E3.2 decimal amendment
before implementation. Enablement requires adversarial observation of the full
transition, including existing same-UID processes, ptrace/proc access, descriptors
and policy. No `exec --privileged`, private socket,
raw engine call or fresh-container substitution that changes command semantics
is a fallback. This refusal is allowed by the approved contract and does not
relax fresh-container entry requirements.

## Entry coverage and qualification matrix

The common planner must cover initial workload start, application commands,
runtime tests, shell, controlled sessions, hooks, host-driven restart/replacement
and runtime exec. Existing public guards remain in place through E. Test private
seams plus no-effect public guard coverage; do not label those tests public support.
A staged/install/service route that lacks a required proof must refuse before
side effects. Cross-entry generation changes must invalidate the same plan.

For each supported tuple record image ID/code closure, role/credentials, exact
or bounded geometry, accepted caps, policy identity, host/kernel/architecture,
Podman/OCI runtime and API behavior. Qualification is verified evidence bound to
those inputs, not a version allowlist or a new authority service. Unrecognized
combinations reject. Repeat fresh-instance tests at least three times per profile
before activation and retain negative controls, cleanup and host observations.

Required tests include:

- Positive: declared exact IDs, bounded undeclared in-range transition, ordinary
  fork/thread creation, required native child identity changes/chroot, clean
  streams and native worker restart within accepted authority.
- Authority: all UID/GID fields, supplementary groups, post-exec P/E/B/I/A/NNP,
  unmapped IDs, omitted capabilities, same-UID roles, set-ID/file-cap exec attempts,
  securebits changes, ambient/inheritable recovery and capability-boundary probes.
- Kernel/code: namespace clone/unshare/setns, clone3 fallback, mount/new mount and
  handle calls, inherited-fd chroot escapes, writable code/dependency/mount shadows.
- Admission: complete-map surplus/missing/wrong-host rows, runtime GID omission,
  stale PID/start identity, peer/namespace/image substitution, host principal or
  delegation changes between observation and release, stale generation/revision.
- Recovery: uncertain create/start/release, interrupted lock, timeout/EOF, failed
  ownership verification, master exit, failed cleanup, service replacement and
  retained dormant IDs. Wrongly owned objects must never be deleted.
- Entry parity: every controlled entry reaches the same final checks or refuses
  before effects. Include shell/session/test/hook and occupied-container exec.

## Investigation evidence and limits

Planning evidence lives outside the checkout at
`/home/omry/.local/state/awd/reploy-E-planning-20261028-ovsb17p5`.
`proof-summary.json` records exact hashes and limitations; original receipts and
controllers remain under `probes/`. These paths are evidence locators for this
machine, not portable approvals.

On 2026-10-28, eight fresh raw user-namespace cases passed: exact/bounded profiles
with empty, Postfix (`0xc4`), Dovecot (`0x400c1`) and omitted-identity (`0x40`)
ceilings. Six private Podman startup cases passed: both profiles with empty,
Postfix and Dovecot ceilings. The trusted waiter ran alone; complete kernel maps
were checked before release, then the static probe directly execed itself and
checked credential/capability state and permitted/forbidden ID transitions.
All owned containers/images were removed, the isolated inventory was restored
and the private API service stopped. The normal Podman store was not used.

Host: WSL2 Linux 6.18.54.1 x86_64, Podman 4.9.3, crun 1.14.1, non-root UID/GID
1000. Private numeric/delegation checks do not certify production files-only
principal inventory. The fixture used the engine's default seccomp policy,
not Reploy's production effective policy or inline-profile transport. It did
not run Postfix/Dovecot, the Go startup helper, occupied-container exec, code or
fd adversaries, production lifecycle, F networking/storage, or release repeats.
These successful mechanisms narrow implementation uncertainty; they do not
satisfy those remaining gates or qualify WSL as a supported production host.

Primary technical context:
[Linux capabilities and exec](https://man7.org/linux/man-pages/man7/capabilities.7.html),
[OCI 1.2.1 process configuration](https://github.com/opencontainers/runtime-spec/blob/v1.2.1/config.md),
[OCI Linux configuration](https://github.com/opencontainers/runtime-spec/blob/v1.2.1/config-linux.md),
[crun 1.14.1 exec implementation](https://github.com/containers/crun/blob/1.14.1/src/libcrun/container.c).
The retained crun source hash is
`a113bf6db56aad6200b368538136572e7d8873a4fcc5d12bd27da77c0b64a827`.

## Proposed delivery slices

Implement sequentially in one checkout. Each slice reuses this group LLD and
approved architecture; it does not trigger a new full design by default.
Production estimates count added plus removed non-test implementation lines;
test estimates count added plus removed test/fixture lines. They exclude this
LLD, generated files, review state and changelog fragments. They are planning
ranges, not measured results or time commitments. Target 500 production LOC per
PR, hard maximum 750; split before work if the current estimate exceeds the hard
limit, and reassess before crossing the target. Keep acceptance-coherent changes.

| Slice | Production LOC | Test LOC | Scope and acceptance | Prerequisites |
| --- | ---: | ---: | --- | --- |
| E1.1 | 250–400 | 450–700 | Pure execution identity/map closure; exact active vs retained/dormant rules, runtime-account evidence, boundary/overflow rejection. Own mapping compiler/acceptance planner, no engine effects. | D4, C1 |
| E1.2 | 300–450 | 550–800 | Versioned native admission state and locked transition; legacy supervisor rejection, uncertain operations/reservation preservation. Own deploy mapping state/operations. | E1.1 |
| E1.3 | 350–500 | 600–900 | Owned create/wait, complete kernel maps and process/peer fences, single release protocol and cleanup failures. Own mapped Podman create and trusted waiter plumbing. | E1.2, C2; release still requires F proof |
| E2.1 | 300–450 | 550–850 | Empty/nonempty final capability paths and all-thread exec transition, exact credentials and securebits. Own probe startup transition; do a bounded Go mechanism spike first. | E1.1; startup mechanism receipt |
| E2.2 | 200–350 | 450–700 | Descriptor closure and protected executable/dependency plan; canary-fd and substitution tests. Own startup fd handling and runtime-policy integration. | E2.1; E owns proof seam, F later supplies production observations |
| E2.3 | 300–450 | 650–950 | Derived effective kernel policy for accepted tuples; capability-conditional rules, ABI clone/fork/threads and mount/handle negatives. Own mapped seccomp policy; ordinary path unchanged. | E2.1 |
| E3.1 | 300–450 | 550–850 | Native-master lifetime, owned lease/queue cleanup and fresh host replacement; disable engine-autonomous restart. Own existing lifecycle integration. | E1.3, E2.2, E2.3 |
| E3.2 | 150–300 | 350–600 | Typed occupied-container exec boundary; deterministic pre-effect refusal for unproved tuples, safe-entry proof harness. No enablement implementation in this estimate. | E3.1 |
| E4.1 | 200–350 | 650–950 | Shared final-authority checks across every controlled entry; privilege-regain and failure-path adversarial payloads. Own entry integration; keep public guards. | E3.2 |
| E4.2 | 100–250 | 600–950 | Cross-entry/profile qualification harness and evidence receipts, repeated-instance tests, integration-gate diagnostics and delivery closeout. Own conformance tests and docs. | E4.1; actual F/G readiness reported separately |
| **Total** | **2450–3950** | **5400–8250** | Ten proposed implementation PRs, conditional on proof results. | Design approval/stamp/PR cycle before implementation |

The old E estimate was 1400–2550 production and 2800–4550 test LOC. It did not
itemize the state/process-model change, setup versus application policy, thread/fd
proofs, or entry/lifecycle parity now visible in the source. The new estimate
counts that existing E scope; it adds no service or public feature. Compared with
D's measured 1606 production and 3331 test changed lines, E is approximately
1.5–2.5 times the production work and 1.6–2.5 times the test work. Do not interpret
these ratios as elapsed-time or token forecasts.

E can finish its internal behavior and missing-proof refusal before F exists.
No E slice depends on F implementation; the F references above identify later
integration/activation gates. E1.3 and E2.2 own the proof seam and negative gate
tests. Their injected positives are not production admission evidence.

Before each slice, resolve its planning-critical mechanism and refresh estimates.
If safe nonempty-capability exec requires a new resident authority process, or
safe live exec requires a broader trust model, stop and present that change.
Do not grow another mechanism to keep an optimistic slice estimate. A failed
live-exec proof can land the explicit refusal and retained gate under E3.2;
it cannot be reported as supported live exec.

## Review and continuation

Review the complete proposal against the four explicit dependencies and current
source seams. Resolve findings and repeat until clean or a specific material
decision remains. Local deep review is not remote design review, maintainer
approval, a design PR cycle or runtime qualification. Record their actual status
separately rather than treating a document stamp as permission to execute.

Before implementation, adopt the decimal map in the owning campaign through the
supported delivery tooling; refresh affected design stamps, publish the reviewed
design and finish the design PR cycle. Reuse prior approvals for settled public
syntax, profiles, native process model and immutable-code default. Present only
actual unresolved public syntax or material authority/default/scope changes.
For each implementation PR, derive every current `ci.yml` job for the exact head,
require all to succeed, run the supported review/PR cycle, use Sapling and add a
Changie fragment for user-facing changes. Do not merge or release automatically.
Stop after E closeout and propose F; this planning task stops before E production
edits, commits or publication.
