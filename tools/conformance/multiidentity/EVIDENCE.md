# Multi-identity Podman conformance evidence

The repaired 1B mechanism checkpoint passed on 2026-10-12.
The six-trial campaign ran from 04:39:33 to 04:48:54 UTC and took 561 seconds.
These results cover the private fixture on the recorded host.
Product integration and public identity policy remain separate gates.

## Reproduction identity

- Command: `tools/conformance/multiidentity/conformance.py --profiles exact,bounded --iterations 3`
- Candidate base before the repair: `692193a9343251f4ef5e9097e95658e9c17a6a17`
- Four-source SHA-256: `ed2cb818ed6cd253e1829d1112a60c5419f661ec9f2dce485f4cb55c3c185775`
- Host: Ubuntu 24.04, Linux 6.18.40.1 WSL2, x86_64
- Operator: canonical account `omry`, effective UID/GID `1000:1000`
- Delegation: UID and GID range `100000:65536`
- Rootless Podman: 4.9.3; OCI runtime: crun 1.14.1
- Tools: Go 1.26.4, Python 3.12.3, GCC 13.3.0
- Seccomp profile: `/usr/share/containers/seccomp.json`
- Seccomp SHA-256: `cc374cf23846ce1f62f4dc807a8e2b8673c783c6f56cb475467621035d281e6c`
- Supervisor capabilities: `CAP_SETGID`, `CAP_SETUID`, `CAP_SETPCAP`

The source identity covers `conformance.py`, `Containerfile`, `posix_ipc.c`, and `probe/main.go`.
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
The current campaign closes the missing controls with new executable checks.

## A1 operation audit: refresh pending

The preceding mechanism results cover the current repaired source.
The operation audit must now use that same source.
The earlier A1 audit completed in 12.787 seconds for older source `882ecc0a...`.
Its raw receipts remain under `~/.local/state/awd/swe-review-authority/runs/run_75d70spapeq246zb8jbaryqb1q-full/proof/a1-geometry-fixed/`.
The old `audit/summary.json` SHA-256 is `1b664c7270e0840f88a1c3cbaf7b8dee5b66b3fea9f1143a93d17d10348c5be7`.
Those observations remain historical; they do not prove the current audit result.
A1 retains the operation-audit driver, audit regression tests and readiness decisions.
Product and stock-mail gates remain open.
