# E re-plan engineering investigation

This is supporting context for the current E LLD. It preserves investigation and
proof limits outside the clean design. It does not grant runtime qualification.

## Fixed scope and inspected seams

The accepted cohort remains non-root staging/user-scope local rootless Linux
Podman. No Windows/macOS host portability, Docker mapped backend, OmegaFlow
public inspection API or image namespace production provider is needed for E.
D declarations and the native-master process model stay unchanged.

`podman_mapped_waiter.go` already owns exact image/engine/namespace fencing,
non-TTY single-frame release, held-lock admission and uncertain cleanup.
`mapped_sandbox_proof.go` already requires complete code/mount observations from
F's trusted effect owner and refuses missing evidence. It does not contain a
production provider. `live_run_container.go` has reusable transient queue/lease
cleanup but unlocks before execution; mapped release must remain under its
existing admission lock. `current_workload_lifecycle.go` and
`deploy/workload_lifecycle.go` supply persistent lifecycle records, not permission
to reuse an old admitted release. `identity_mapping_native.go` already journals
native attempts and conservative recovery. Do not create a second lifecycle or
reservation database.

The ordinary `podmanApplicationCreateRequest` validates five setup capability
names and one pinned seed. It cannot be relaxed globally to accept application
capabilities or a different filter. A private typed mapped wrapper must apply
the qualified mapped create changes after ordinary strict parsing; all mapped
prepare/create/observe/release users must use that same request representation.

The startup code already closes the launching thread's private fd table through
CLOSE_RANGE_UNSHARE and raw exec-or-exit. It supports empty ceilings at accepted
non-root IDs and nonempty ceilings at UID 0 only; nonzero UID plus nonempty caps,
private-env transfer, observed-exit supervision and TTY remain unqualified.
Do not silently enable those tuples while integrating later entry paths.

## Concrete policy discovery

Pinned seed SHA256: `785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334`.
It allows namespace/mount/new-mount syscalls and clone/clone3 under CAP_SYS_ADMIN;
open_by_handle_at under CAP_DAC_READ_SEARCH; name_to_handle_at unconditionally.
Its no-CAP_SYS_ADMIN clone rule masks namespace bits and clone3 returns ENOSYS.
An accepted capability therefore changes effective policy semantics.

The compiler must remove forbidden names from every seed allow rule regardless
of capability conditions, replace all clone/clone3 rules with a closed ABI-aware
legacy-clone mask and unconditional clone3 ENOSYS, retain the seed default-deny
semantics and ordinary unrelated rules, and bind architecture, final/setup caps,
seed and derived bytes. The ordinary profile remains byte-identical.

## Disposable mechanism result

A fresh amd64 child-process C fixture installs a bounded BPF policy, then runs
actual fork and pthread creation. In the unfiltered control, clone3(NULL,0)
returns EINVAL, a NEWUSER clone succeeds, unshare(0) succeeds, and malformed
mount/handle calls return EFAULT. With the fixture policy, fork/pthread still
succeed, clone3 returns ENOSYS and namespace clone/unshare/mount/handle calls
return the diagnostic EACCES. This distinguishes policy interception from
ordinary invalid-argument failures and demonstrates libc fallback on this host.
The NEWUSER control child exits immediately and is waited for. No host mount or
service is modified.

The diagnostic EACCES is fixture-only. Production follows its declared derived
policy errno contract. This is an amd64 WSL kernel mechanism proof, not a
production JSON compiler, engine/libseccomp translation proof, rootless Podman
qualification, ARM64 evidence, capability qualification or public activation.
Original source and complete receipts:
- `/home/omry/.local/state/awd/reploy-E-replan-20261030/seccomp-branch-proof.c`
- `/home/omry/.local/state/awd/reploy-E-replan-20261030/seccomp-branch-proof-result.json`
- `/home/omry/.local/state/awd/reploy-E-replan-20261030/seed-audit.json`

Focused unchanged-source tests passed for one-shot release, uncertain cleanup,
sandbox-proof refusal, public guard callbacks and transient queue cleanup:
`go test ./internal/dockerdeploy -run
'^(TestOwnedMappedWaiterReleasesExactlyOnceAfterProofV1|TestOwnedMappedWaiterReleaseFailuresRetainJournalAndCleanupV1|TestMappedSandboxProofV1|TestMappedSandboxSelectedPlanRefusalsV1|TestIdentityMappingGuardV1EntryCallbacksStopBeforeRuntimeWork|TestRunAdmittedTransientContainerV1ReleasesLockForExecutionAndCompletesQueue)$'
-count=1` (exit 0, package output 1.126s).

## Engineering decisions and remaining qualification

Use the existing operation/queue/lifecycle records and instance labels. Split
policy compilation from its mapped engine application; split instance lifetime
from replacement. Share one final entry-plan constructor across controlled
entries. Refuse all currently unqualified occupied-container mapped exec before
creating an exec object; retain its future-proof harness without implementing a
stronger temporary helper. G retains public orchestration/activation ownership.

Successful internal E tests may inject only the existing trusted effect-owner
interfaces and exact typed requests. They cannot be advertised as actual engine
qualification. Missing/mismatched effective-policy, F storage/network/code or
entry evidence keeps release closed. A policy string plus Seccomp:2 is
insufficient to qualify effective semantics; actual policy/ABI syscall controls
under the selected runtime must be bound to the case before support is claimed.

No owner decision changing architecture, defaults, public syntax, mapping
profiles or trust was identified by this bounded re-plan investigation. Later
mechanism findings that require such changes must return to architecture review.
