# Multi-identity Podman conformance evidence

## Current 1B failure-evidence correction

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
