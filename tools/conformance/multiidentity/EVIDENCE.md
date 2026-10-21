# Multi-identity Podman conformance evidence

## Current 1B supervisor capability and UDP evidence

Three fresh exact and three fresh bounded trials passed on 2026-10-14 in 565.601 seconds, including setup and cleanup.
Each trial ran a successful same-owner `fchown` control on the supervisor's
private file. Changing its owner to the mapped application UID then failed
with `EPERM`, and UID/GID remained `100:100`. The operation ran on the
supervisor's locked thread with the exact reviewed capability masks.
All earlier admission, root-rejection, application, namespace, ownership and
cleanup checks passed. The complete engine inventory was restored.
The canonical four-source probe SHA-256 is
`c975a2c83dec68efb133932159332e4ae912e6cb65d61724dbbff15a2e6b0310`.

The UDP fixture now requires a distinct producer response frame. A real
self-connected UDP socket reproduced receipt of its own request without a
producer. The new self-echo negative and TCP/UDP positive controls passed 25
repetitions. The first live attempt failed its older UDP check and restored
the inventory; its socket addresses were not captured, so that failure's
exact cause remains unconfirmed. Its failed receipts remain in `correction/mechanism/`.
The two supervisor evidence regressions failed on the original checker and
pass on this correction. These changes affect only the private fixture.

The fresh six-trial receipts and before/after source snapshots are retained at
`~/.local/state/awd/reploy-mi-recovery/20261013-bottom-up/pr-69/attempt-4/correction/retry-1/`.
The mechanism receipt manifest SHA-256 is `d9047a80e29dd94b66eb14bdba855cb046b369593ab86f6c91f3855b54ed54f7`.
Only the two known compiled fixture binaries were removed; their hashes and
sizes remain in `excluded-binaries.json`. No host delegation or live installation changed.
Product integration and public capability policy remain open.

## Earlier 1B failure-evidence correction

Three fresh exact and three fresh bounded trials passed on 2026-10-13 in 585.613 seconds, including fixture setup and cleanup.
Three behavioral regressions reproduced loss of the original error or missing
receipts in the old implementation and pass on this correction. They inject
trial, image-cleanup and inventory failures through the actual fixture entry points.
Unavailable inventories are explicit failed/unknown observations, and original
failures survive while cleanup/summary/command receipts remain written.
Every live trial rejected root as UID, GID and supplementary group. Admission, descriptor,
capability, namespace, ownership and cleanup checks also passed.
The complete Podman inventory matched its starting state.
The canonical four-source probe SHA-256 is
`2b919327b6cc5edcb69f508ff1109b8f78db9c2a242fef5a0a3b1f65c897c2bf`.
These are private-fixture results on the recorded host; product integration remains open.

The exact command, host identity, source snapshots and six trial/cleanup records
are retained at `~/.local/state/awd/reploy-mi-recovery/20261013-bottom-up/pr-69/attempt-3/correction/`.
The mechanism receipt manifest SHA-256 is `677c352e0499a2290566ce3bbecb924d8ccbfb8410d26e2605ca4ca51a84e758`.
Only the two known compiled fixture binaries were removed; their hashes and
sizes remain in `excluded-binaries.json`. The trials used an attempt-owned
rootless Podman store. No host delegation or live installation changed.

## Earlier 1B root-rejection evidence

Three fresh exact and three fresh bounded trials passed on 2026-10-13 in 578.4 seconds, including fixture setup and cleanup.
Every trial explicitly rejected mapped container root as an application UID,
primary GID and supplementary group. All previous admission, descriptor,
capability, namespace, ownership and cleanup checks also passed.
The complete Podman inventory matched its starting state.
The canonical four-source probe SHA-256 is
`4ef9220d14740f1607313f7e43a8b20c6c6b13a36724b2620de9e34cd255f451`.
These are private-fixture results on the recorded host; product integration remains open.

The exact command, host identity, source snapshots and six trial/cleanup records
are retained at `~/.local/state/awd/reploy-mi-recovery/20261013-bottom-up/pr-69/attempt-2/correction/`.
The mechanism receipt manifest SHA-256 is `d47f06a0080b9a51f40cb5b98d147a5e403178ca46839e2f6bfc8bda83ebaa38`.
Only the two known compiled fixture binaries were removed; their hashes and
sizes remain in `excluded-binaries.json`. The trials used an attempt-owned
rootless Podman store. No host delegation or live installation changed.

## Earlier 1B admission-release correction

The private harness now publishes its admission-release nonce atomically.
The concurrent reader regression tests 1,000 publications and checks mode `0600`.
This repair was first tested above the historical A1 revision
`9b091c8da39b3ecb551e101ee6731566dfac5b73` and now belongs to 1B.
The exact four probe sources and module inputs match that tested candidate.
Their canonical probe source SHA-256 is
`ce0a74c171cc96232bb9be2ece432cd69bd8ba8632bf0da16c8f13b007c5f1a0`.

The three exact and three bounded trials passed on 2026-10-12 in 567.866 seconds,
including setup and cleanup. All four roles passed admission before payload
execution. The engine inventory was restored. These are recorded private-fixture
results on that host, not new current-host or product-integration claims.
The matching receipts are retained at
`~/.local/state/awd/reploy-mi-recovery/20261012-a1/proof/a1r1/`.
Its 51-file receipt-manifest SHA-256 is
`31c867dadc0d043494c31bb43b6ee7ead04a15e4ce0be9c96bbc9ddabb9d19c6`.
Before/after snapshots and every receipt hash were rechecked during this repair.
The older 1B baseline below remains historical evidence for its own source.
Audit-only behavior remains in A1.

## A1 owned-service cleanup correction

On 2026-10-20, [review](https://github.com/omry/reploy/pull/239#discussion_r4317614026)
identified an owned API service that could survive a termination or wait error.
Driver SHA-256 is `7376bd1c371333d895fa0f1f970eee15610b71b3941bda5ae282407ca3198e94`.
The audit now attempts a bounded forced stop after those errors. If stopping
cannot be proved, it retains the socket for recovery. The original cleanup
failure still makes the audit fail, even when the forced stop succeeds.

Fault regressions call the actual audit entry point. They cover a termination
error, an initial wait error, an already-exited child, persistent wait failure
and failed forced termination. They check service cleanup, retained endpoints,
image cleanup, the original error and both receipt files. The new regressions
fail against the prior driver. The corrected private suite runs 46 tests with
one established opt-in skip. This is a private audit truth fix; it does not
change production code, the core probe, capability policy or backend support.
No new live-engine audit is claimed for this driver. The packets below retain
their original source identities.

## Earlier A1 compatible-operation audit

Independent-failure corrections were validated on 2026-10-14. Driver SHA-256 is
`2c5306490ac6a30c9e0c76a2a15af04f1db80009c29d24ecd089a911040e0f0b`; the unchanged reviewed 1B probe is
`c975a2c83dec68efb133932159332e4ae912e6cb65d61724dbbff15a2e6b0310`.
The fresh audit records nine compatible observations and five explicit blockers.
Its command took 50.432 seconds; its internal timer, including preflight,
reported 50.380 seconds. All nine cleanup records passed, complete inventories
were restored and the attempt-owned API service stopped. Immediate retirement
now also verifies absence for a create whose response or outcome was uncertain.

Raw receipts and exact runtime inputs are retained at
`~/.local/state/awd/reploy-mi-recovery/20261013-bottom-up/a1-audit-receipt-fix/independent-failures-correction/live/`.
Receipt manifest SHA-256 is
`d6cf0fd1dabfff37e667a561d30794ffbe5d576a017a19c2836f5c3c614baea9`.
Only two known fixture binaries were removed after their hashes and sizes were recorded.
These remain private Podman 4.9.3 observations, not supported product acceptance.

[Cleanup finding](https://github.com/omry/reploy/pull/239#discussion_r4265098841)
identified an immediate cleanup exception that suppressed later audit results.
The fix records that failure, blocks dependent mapping candidates and retains
owned final-cleanup obligations while continuing the independent network audit.
The mandatory cleanup result still fails when a removal or absence check fails.
[Diagnostic finding](https://github.com/omry/reploy/pull/239#discussion_r4265098851)
identified a skipped-build reason that falsely asserted every probe was rejected.
The builder-safety blocker now describes its own requirements and leaves the
actual capability observation separate. Production build support remains open.

The fifteen-outcome regression separately fails with either fix absent and
passes with both restored. It covers cleanup deletion/absence failures and both
successful and rejected capability observations as well as prior lifecycle cases.
The complete private suite passes 45 tests with one established opt-in skip.
Focused before/after receipts are retained in `independent-failures-correction/`.
No production code, core probe, capability policy or backend was changed.

## Earlier A1 lifecycle-result correction

The lifecycle-result correction was validated on 2026-10-14. Driver SHA-256 is
`2c82f9124e1fb5934b87ecbe37f34a38dbf58fd39168170d83b5bcfd33ffb400`; the unchanged 1B probe source remains
`c975a2c83dec68efb133932159332e4ae912e6cb65d61724dbbff15a2e6b0310`.
The fresh audit recorded nine compatible observations and five explicit blockers.
Its command took 51.812 seconds; its internal timer, including preflight,
reported 51.762 seconds. All eight cleanup records passed, inventories were
restored and the attempt-owned API service stopped.

Raw receipts and exact runtime-input snapshots are retained at
`~/.local/state/awd/reploy-mi-recovery/20261013-bottom-up/a1-audit-receipt-fix/lifecycle-correction/live/`.
Receipt manifest SHA-256 is
`ee15f6907ad3fee82f9968af66566c0651fb769ae8d95c56431abd0332ab89de`.
Only the two known fixture binaries were removed after recording hashes and sizes.
These are private Podman 4.9.3 observations, not product or Podman 6.1.4 acceptance.

[PR review finding](https://github.com/omry/reploy/pull/239#discussion_r4264835177)
identified missing restart/exec results after failed bounded prerequisites.
The twelve-case regression covers create, start, admission, inspect, initial map,
restart, changed-map and stale-readiness failures, plus exec, wait and logs failures
and a compatible control. Eleven failure cases fail on the old driver; all twelve
pass on this correction. Missing dependent results become explicitly blocked;
already-verified restart success remains intact when a later operation fails.
The full private suite still runs 45 tests with one established opt-in skip.
Before/after regressions are retained in `a1-audit-receipt-fix/lifecycle-correction/`.
No production code, core probe, capability policy or backend changed.

## Earlier A1 failure-receipt correction

The failure-receipt repair was validated on 2026-10-14. The corrected driver
SHA-256 is `5e6da63a837c4173212590345b80f4591e6bcfd2fc561cc3f10cea8dcb21f3fd`.
It uses the unchanged corrected 1B probe source
`c975a2c83dec68efb133932159332e4ae912e6cb65d61724dbbff15a2e6b0310`.
The fresh audit recorded nine observed-compatible operations and five blockers.
Its command took 55.003 seconds; its internal timer, now including preflight,
reported 54.943 seconds. All eight cleanup records passed, the full engine
inventory was restored, and the attempt-owned API service stopped.

Raw receipts and exact runtime-input snapshots are retained at
`~/.local/state/awd/reploy-mi-recovery/20261013-bottom-up/a1-audit-receipt-fix/live-short-socket/`.
The receipt manifest SHA-256 is
`4cd25c4ecaf797223b85aa251304d56beebca591fc880ab20dd214c9436adb97`.
The two known fixture binaries were removed only after recording hashes and sizes.
These remain private Podman 4.9.3 observations; no supported production build,
exact-map acceptance, stock-mail conformance or Podman 6.1.4 acceptance is claimed.

The new failure regressions fail on the previous audit. With the repair, all
45 private tests pass; one existing opt-in live build-failure test is skipped.
An original audit error survives final-inventory and service-stop failures.
Image cleanup and durable summary/command receipts continue after those failures;
unknown inventory cannot establish restoration. Initial-inventory failures also
produce a receipt. The first live retry used an overlong Unix socket path and
failed before any compatible operation. Its original failure, restored inventory
and command transcript are retained under `a1-audit-receipt-fix/live/`.
A fresh short socket produced the passing audit above. No live installation changed.

## Earlier A1 audit before failure-receipt repair

The audit completed on 2026-10-14 on the corrected 1B probe source
`c975a2c83dec68efb133932159332e4ae912e6cb65d61724dbbff15a2e6b0310`.
It recorded nine observed-compatible operations and five explicit blockers.
The command took 52.77 seconds; its internal timer reported 14.654 seconds.
All eight cleanup records passed, and the full engine inventory was restored.
The audit driver SHA-256 was
`d9db6c7ad1f1d53fb5237b47dc549975f51880ff6cbf5ab1683d076b0b5c0db7`.
These are private Podman 4.9.3 compatibility observations, not supported product
builds, exact-map acceptance, or stock-mail conformance.

Raw receipts and before/after runtime-input snapshots are retained at
`~/.local/state/awd/reploy-mi-recovery/20261013-bottom-up/a1-cap-audit/`.
The receipt manifest SHA-256 is
`f45a6f08e0bbdaa7eca49260161cd01c0a61fb8fd469c79b6206d1ab107afb47`.
Only the two known compiled fixture binaries were removed after recording
their hashes and sizes. No host delegation or live installation changed.

## Earlier A1 correction evidence

1B supplies atomic admission-release publication. A1 adds fresh restart admission and
verified restart classification, independent volume failure results and acceptance
of existing empty output directories.
The six-trial exact/bounded campaign passed on 2026-10-12 in 567.866 seconds,
including fixture setup and cleanup.
All four roles passed map admission before payload execution.
Docker was rejected before mutation, and the whole Podman inventory was restored.
The probe source SHA-256 for the 2026-10-12 campaign was
`ce0a74c171cc96232bb9be2ece432cd69bd8ba8632bf0da16c8f13b007c5f1a0`.

The release-marker race regression passed 20 repetitions and verifies mode `0600`.
The private Python suite ran 37 tests with one opt-in skip.
The CI bridge ran the same 37 private tests plus five tooling tests.
The 2026-10-12 live failed-`RUN` regression passed in 6.914 seconds.
Earlier A1 heads passed the complete fourteen-command product validation.
Current-head validation remains part of the final PR gate.
These are private fixture results; product integration and mail gates remain open.

Those earlier raw receipts are retained at:

`~/.local/state/awd/reploy-mi-recovery/20261012-a1/proof/a1r1/`

The 51-file `receipt-manifest.json` SHA-256 is
`31c867dadc0d043494c31bb43b6ee7ead04a15e4ce0be9c96bbc9ddabb9d19c6`.
`source-before.json` and `source-after.json` bind every runtime input.
The tests ran against the pending A1 correction above revision
`9b091c8da39b3ecb551e101ee6731566dfac5b73`; snapshots bind those pending bytes.
Only four known compiled fixture binaries were removed.
Their hashes and sizes are retained in `excluded-binaries.json`.
The host and delegation match the approved 1B baseline below.

## Earlier 1B baseline

The repaired 1B mechanism checkpoint passed on 2026-10-12.
The six-trial campaign ran from 04:39:33 to 04:48:54 UTC and took 561 seconds.
These results cover the private fixture on the recorded host.
Product integration and public identity policy remain separate gates.

## Reproduction identity

- Command: `tools/conformance/multiidentity/conformance.py --profiles exact,bounded --iterations 3`
- Candidate base before the repair: `692193a9343251f4ef5e9097e95658e9c17a6a17`
- Probe source SHA-256: `2bb097b216531269c0022bf9bda4eb41026e134d228b6b1a1a0e4831c4322e33`
- External source-snapshot SHA-256: `ed2cb818ed6cd253e1829d1112a60c5419f661ec9f2dce485f4cb55c3c185775`
- Host: Ubuntu 24.04, Linux 6.18.40.1 WSL2, x86_64
- Operator: canonical account `omry`, effective UID/GID `1000:1000`
- Delegation: UID and GID range `100000:65536`
- Rootless Podman: 4.9.3; OCI runtime: crun 1.14.1
- Tools: Go 1.26.4, Python 3.12.3, GCC 13.3.0
- Seccomp profile: `/usr/share/containers/seccomp.json`
- Seccomp SHA-256: `cc374cf23846ce1f62f4dc807a8e2b8673c783c6f56cb475467621035d281e6c`
- Supervisor capabilities: `CAP_SETGID`, `CAP_SETUID`, `CAP_SETPCAP`

The source identity covers `conformance.py`, `Containerfile`, `posix_ipc.c`, and `probe/main.go`.
The probe hashes a trailing NUL after each file; the external snapshot does not.
Both encodings bind the same four files, whose individual hashes match before and after.
The before and after snapshots also bind `go.mod` and `go.sum`.
The fixture used an offline `scratch` image and an explicitly selected disposable store.
Its user-owned runtime paths were under `/tmp/reploy-mi-p69`.
No host delegation or live installation changed.

## Repeated results

| Profile | Fresh trials | Workload A host mapping | Workload B host mapping | Result |
| --- | ---: | --- | --- | --- |
| Exact | 3 | Sparse IDs beginning at 100000 | Disjoint sparse IDs beginning at 100020 | 3/3 passed |
| Bounded | 3 | 100000–104095 | 104096–108191 | 3/3 passed |

Every trial used two primary workloads and two independent controls.
All four mappings passed admission before any payload ran.
The entire caller delegation passed validation before image or volume creation.
Each admission used current NSS lookups and the active enumerable `files` backend.
Injected NSS and foreign-delegation collisions failed closed for both ID kinds.

The exact profile mapped UID 0, 100 and 1001.
It mapped GID 0, 5, 100, 2001, 3001 and 3002.
GID 5 remained runtime-only authority for `devpts`.
The bounded profile mapped exactly 4096 private UIDs and GIDs.
Focused tests rejected gaps, overlaps, reordered segments and changing offsets.

All six trials passed these checks:

- Declared transitions, forbidden credentials and supplementary groups, and the complete raw UID/GID syscall matrix.
- Application UID 1001, GID 2001, groups 3001/3002, empty capabilities, `NoNewPrivs: 1`, and seccomp mode 2.
- Exact descriptors 0/1/2 before application exec, followed by independent checks for all six canary objects.
- Blocked identity changes, mapped set-ID execution, file capabilities, supervisor signals, pidfds, ptrace and process-memory access.
- Dedicated supervisor credentials, no extra supervisor groups, non-dumpability, separate sessions and the exact capability mask `0x1c0`.
- Empty default capabilities and a successful unfiltered `vmsplice` control paired with the filtered denial.
- Separate PID, IPC, network, mount and user namespaces, with private mount propagation and private IPC mounts.
- Positive own-state mount controls and rejection of peer state sources in every peer mount record.
- Positive producer TCP/UDP round trips, followed by negative peer TCP/UDP checks.
- Positive POSIX shared-memory, semaphore and message-queue controls, followed by negative peer IPC checks.
- Negative abstract Unix socket, process and private-state access checks.
- Correct subordinate ownership for persistent files and the frozen sandbox restrictions.

The application compares kernel object identities, including the target PID for each pidfd.
New runtime descriptors can reuse old descriptor numbers.
A successful raw syscall cannot pass the denial assertion.
The readiness writer publishes complete JSON through an atomic rename.

## Focused regression checks

The private Go suite passed, including descriptor, errno, network and readiness regressions.
It checked TCP echo with deliberately fragmented data.
The concurrent readiness reader saw no partial JSON across 100 publications.
The direct Python suite and CI bridge each passed 22 tests, with one live test skipped.
The separate live failed-`RUN` test passed in 6.342 seconds.
It restored both image and external build-container inventories after the injected failure.
Audit-only tests remain in A1.

## Docker and cleanup

The negative gate identified Docker Desktop Engine 29.8.1.
It rejected Docker before sending mapping syntax or a mutation request.
Docker inventories matched before and after the check.
This verifies the private probe's gate; product backend admission remains unproved.

Each trial removed its owned containers and volumes.
The final image removal passed, and the complete Podman inventory matched its starting state.
The default `podman` network remained present in both snapshots.
Cleanup verified attempt labels and preserved foreign resources.

## Durable receipts and historical records

Raw receipts are retained at:

`~/.local/state/awd/reploy-mi-recovery/20261012-a1/proof/pr69/`

The source snapshots, six trial records, cleanup records and command log are retained.
`receipt-manifest.json` binds each retained file.
Its SHA-256 is `f5323fa48432d7f517ca2454627ff474326742301750db6de4d9dce4726efa07`.
Compiled build-context binaries are excluded; their hashes are recorded separately.

The earlier `99512c984...` and `882ecc0a...` campaigns remain historical observations.
They do not prove the repaired source recorded here.
A pre-reboot trial packet was lost and is not used as current evidence.
The 1B campaign closed those missing controls with executable checks.

## Earlier A1 restart audit (2026-10-12)

The audit command completed in 57.405 seconds on 2026-10-12.
Its internal timer reported 17.929 seconds after host preparation.
It used the same probe bytes as that day's six mechanism trials.
The audit-driver SHA-256 is
`d9db6c7ad1f1d53fb5237b47dc549975f51880ff6cbf5ab1683d076b0b5c0db7`.
The output directory already existed and was empty, exercising that corrected entry path.
Source snapshots bind the driver, fixture, exported capability probe and module files.

Nine operation observations were compatible; five remained blocked.
The bounded automatic and fixed candidates, immutable local-image inspection,
lifecycle commands, volume CRUD and network CRUD passed.
Restart admission required a new exact-nonce ready line before release.
The sparse projection returned HTTP 500, and the exported BuildKit capability probe rejected Podman 4.9.3.
No product build was launched after that rejection.
Supervisor/restart ownership and network-policy parity remain unproved.

All eight owned-resource cleanup records passed.
The full inventory matched its starting state; the API service stopped and its socket was removed.
Native Podman built only the offline fixture and supplied independent observations.
No native fallback converted a failed compatible operation to success.
The latest audit receipts are under `proof/a1r3/audit/` in the evidence directory above.
Its 16-file receipt manifest SHA-256 is
`e73a3345fcef6d245b267ce61cb691d43cb96354a6d3930946ddc37fab970004`.
The before/after snapshots bind the pending repair above local revision
`235df75d9ed0fc077dfd9ffcca778fbaf04f10e0`, including the inherited dev3 version.
Only the two known compiled fixture binaries were removed; their hashes and
sizes remain in `proof/a1r3/excluded-binaries.json`.
The regression reproduced false compatibility for mapping changes and stale
admission logs, plus the missing restart-command failure record, before the fix.
All four success/failure subcases now pass through the actual audit entry point.

The earlier audits and approved 1B six-trial packet remain baseline evidence.
They do not prove the newer A1 bytes.
The [readiness disposition](READINESS.md) owns the remaining product and stock-mail gates.
