# Multi-identity Podman conformance evidence

A1's geometry repair campaign passed on 2026-10-11, 05:47:43–05:56:50 UTC:
exact 3/3 and bounded 3/3, 547 seconds, with `engine_restored=true`.
The subsequent compatibility audit completed in 12.787 seconds and restored
its inventories. These are private mechanism observations; product and mail
gates remain open. A1 still requires delivery review and exact-head PR checks.

## Current retained receipts

The actual raw receipts are retained outside the repository and OS temporary
storage under:

`/home/omry/.local/state/awd/swe-review-authority/runs/run_75d70spapeq246zb8jbaryqb1q-full/proof/a1-geometry-fixed`

| Receipt | SHA-256 |
| --- | --- |
| `mechanism/host.json` | `d8e50b4b2a90a413ae47d8fba54b5fcdd2e147c5c7308410f5054ff5f35ad696` |
| `mechanism/summary.json` | `8497330926e34c654f3d8ec0ba6fbc23607bdaf3534dccf9d0dd3d91fb6a16e6` |
| `audit/summary.json` | `1b664c7270e0840f88a1c3cbaf7b8dee5b66b3fea9f1143a93d17d10348c5be7` |
| `mechanism/focused-geometry.stderr` | `8081cd9479336c3dcaebe6fae13b540b03f8049ac290c9b8896164d27319c7c4` |
| `mechanism/focused-ci.stderr` | `5ad97bd1476bd15d5da1d27fcb24dee341460bca57d90c098a43165f0c5395ce` |
| `directory-digest-manifest.sha256` | `2a4f841b4aeb7cb941b40de252c1fb1f0733508dc36870109f84b7193119bd42` |

The repaired four-source identity is
`882ecc0a1402a5772573c5bd7da069e305a78f47a4d0509609c3ff97e4e2e840`.
Both the mechanism host receipt and operation-audit receipt record that
identity. `source-before.json` is the repair-before snapshot; its
`conformance.py` entry records the pre-repair file hash
`a01eb444300c17a4e388dcd31e989540042b7bc11fd7ac922d9a6071ef3c7aed`, not a
four-source identity. A distinct live source-frozen-before receipt was not
retained. The final source binding is established by the mechanism host
receipt, the run capture and end checks, the audit receipt, and
`source-after.json`, all carrying the repaired four-source identity above. The
source hashes stayed unchanged through all six trials and the audit. The audit source SHA-256 is
`adce0c650aa83e864cc693d3ef152114754383620c1bd90ff52b8283b7c1a23c`.
`directory-digest-manifest.sha256` binds the copied raw receipts. Static
build-context binaries and Unix sockets are excluded; their build, image and
service receipts are retained.

Commands used `conformance.py --profiles exact,bounded --iterations 3` followed
by `operation_audit.py`, with the retained Podman wrapper first in `PATH`.
The wrapper selected user-owned runtime directories and a separate empty graph
store using native Podman flags; its crun wrapper selected a user-owned runtime
root. No host configuration or Reploy runtime backend was changed. Podman was
4.9.3 and crun 1.14.1 on Ubuntu 24.04, WSL2 Linux 6.18.40.1, x86_64. Effective
UID 1000 resolved canonically to `omry`, with UID/GID delegation `100000:65536`.

The repaired bounded gate validates each UID and GID map independently as an
ordered contiguous affine range. The focused suite passed 26 tests (one
opt-in live test skipped), and the CI suite passed 31 tests (one opt-in live
test skipped). Its synthetic regressions reject reordered, gapped, overlapping,
offset-changing and non-positive bounded segments, including malformed maps
that would otherwise be skipped when UID and GID map counts differ.

## Failure and admission regressions

The live fixture failure test deliberately executes an unknown probe mode in a
Containerfile `RUN`. With the old build arguments it failed its inventory
assertion, leaving two intermediate images in a separate initially empty store.
The exact failed-build STEP images were removed; independent read-back confirmed
that store empty. With `--layers=false --force-rm=true`, the same regression
passed, including external build-container inventory restoration. The earlier
recovery campaign's live focused suite passed 23 tests in 14.636
seconds. Ordinary runs skip that opt-in test; admission, authority,
response-loss and cleanup regressions remain host-independent and are discovered
by the Linux `tools/ci` bridge. The final geometry-fixed six-trial campaign
above is the current source pairing.

Initial recovery attempts failed on missing runtime directories, before a
mechanism trial could complete. A failed pre-fix fixture build also exposed
intermediate-image leakage; exact-ID cleanup restored its disposable store.
Those failures are not passing conformance evidence. The recovered six-trial
campaign above used the fixed builder and frozen sources throughout.

The earlier recovered six-trial receipts under
`run_0trjyc5tah5yf6cj2h7p2s4nrs-full` and their source identity
`ddd560bfb090aa2f00e2f96fc96784dc9ec1f0139cc8d94ffc522389db67b7b2` remain
historical observations from before the geometry repair. They are not rebound
to the repaired source. The newly retained receipts establish the current
source pairing directly. No lost original receipt has been reconstructed.

## Repeated results

| Profile | Iterations | Workload A mapping | Workload B mapping | Result |
| --- | ---: | --- | --- | --- |
| Exact | 3 | sparse host IDs beginning at 100000 | disjoint sparse host IDs beginning at 100020 | 3/3 passed |
| Bounded | 3 | container range 0–4095 to host range 100000–104095 | container range 0–4095 to host range 104096–108191 | 3/3 passed |

Every iteration created two fresh workloads and two fresh control containers.
All four started only the trusted admission waiter on a private `/run` tmpfs.
The host verified their effective UID/GID maps, authoritative delegation, NSS
freedom and pairwise separation before releasing any supervisor or control
payload. The entire delegated pool was checked before image or volume creation.

Every iteration used the same application container IDs. The
exact profile mapped only UID 0, 100, and 1001 and GID 0, 5, 100, 2001, 3001,
and 3002. GID 5 was explicitly justified as runtime-only `devpts` authority.
The bounded profile mapped exactly 4096 private UIDs and GIDs. Host mappings
were subordinate, non-root, non-overlapping, and free of known identity or
delegation aliases. Each trial also injected UID and GID aliases for an NSS
principal and another principal's delegation; all four cases failed closed for
the intended reason.

All six trials passed the following groups of checks:

- declared supervisor transitions, undeclared-policy rejection including
  mapped runtime and supervisor GIDs as supplementary groups, exact unmapped
  denial, bounded in-range success, and bounded out-of-range denial across the
  complete required UID/GID syscall matrix;
- final application UID 1001, GID 2001, supplementary groups 3001 and 3002,
  empty capability sets, `NoNewPrivs: 1`, and seccomp filter mode 2;
- an exact `0/1/2` descriptor allowlist after closing six deliberately
  inherited non-`CLOEXEC` supervisor canaries;
- blocked post-drop identity changes, privilege regain through a mapped
  supervisor-owned `100:100` set-user/group-ID executable, file-capability
  privilege regain, supervisor signaling, pidfd signaling, ptrace, proc-memory,
  and process-VM access;
- an opted-in supervisor capability mask of exactly `0x1c0`, a separate
  default-capability run with every capability set empty, and a
  profile-denied `vmsplice` round trip paired with a successful unconfined
  control;
- separate PID, IPC, network, mount, and user namespaces, private mount
  propagation, private `/dev/shm` and `/dev/mqueue`, and denial of cross-workload
  System V IPC, named POSIX IPC, TCP, UDP, abstract Unix sockets, process
  access, and private-state access, with positive `shm_open` and `sem_open`
  round trips plus a POSIX message-queue send/receive control in every producer
  workload;
- read-only roots, one managed writable volume, no privileged mode, host
  namespace, host device, external bind, or runtime socket; and
- managed files owned by the exact expected subordinate host UID and GID.

## Docker and cleanup

The negative backend check positively identified Docker Desktop Engine 29.8.1,
rejected it in the private harness before sending mapping syntax or a mutation
command, and observed identical Docker inventories before and after the check.
This proves the probe's pre-mutation backend gate, not the later Reploy product
integration.

Each trial reported no remaining prefixed container, volume or network. The
recorded rootless Podman inventories were containers `[]`, images `[]`, volumes
`[]`, and the default network `podman` both before and after the campaign,
including after final image removal. The harness exited zero only after that
complete inventory comparison passed. The compatibility audit recorded five
deterministic create names before issuing requests (volume, three container
candidates and network), while the focused response-loss tests exercised exact
name and attempt-label reconciliation. The live audit verified post-delete
absence; its cleanup removed two containers, one volume, one network and one
image.

The full machine-readable evidence remains reproducible from the recorded
command and source hash. It is not committed because it includes verbose host
and runtime inventories; this curated record contains the review-relevant
results without turning the repository into an environment snapshot.

## Evidence limits

Host-independent tests inject UID/GID NSS and foreign-delegation collisions in
all four roles, hostile login names, real/effective UID mismatch and an outer
non-1000 effective UID. Rejection releases no payload. Planned rejection creates
no runtime object, and invalid admission tokens never release the waiter.
Cleanup preserves foreign labels and reports engine errors rather than treating
them as absence.

The earlier 2026-08-14 observation had late authority checks and ungated auxiliary
controls. Fresh trials establish repaired admission; they do not retroactively
make that historical ordering sound. See [A1 readiness](READINESS.md) for the
operation dispositions and stock-mail gate. A mechanism pass is not product
readiness.
