# Multi-identity Podman conformance evidence

Status: passed on 2026-08-14 from 21:57:55 through 22:00:20 UTC.

This is private mechanism evidence for slice 1B. It confirms the selected
Podman primitives on the recorded host; it does not claim that Reploy exposes
or supports multi-identity workloads yet.

## Reproduction identity

- Command: `tools/conformance/multiidentity/conformance.py --profiles exact,bounded --iterations 3`
- Reploy parent head: `7dfc218c07ca247e1914de59cf07e4e8c3891b09`
- Probe source SHA-256: `5e21d39628b6a70d02324033d4c0f39c523b4ebb435ce8e277ad6c88f88df84c`
- Host: Ubuntu 24.04 on Linux 6.6.114.1 WSL2, x86_64
- Rootless Podman: 4.9.3, local socket
- OCI runtime: crun 1.14.1
- Delegation: UID and GID range `100000:65536`; the active `files` libsubid
  backend agreed with `getsubids`, and the harness checked every mapped ID
  through NSS lookup plus every other enumerable subordinate delegation
  present on the host
- Seccomp profile: `/usr/share/containers/seccomp.json`, SHA-256
  `cc374cf23846ce1f62f4dc807a8e2b8673c783c6f56cb475467621035d281e6c`
- Supervisor allowlist: `CAP_SETGID`, `CAP_SETUID`, and `CAP_SETPCAP`; the last
  capability is used by the trusted launcher to empty the child bounding set

The source hash covers `conformance.py`, `Containerfile`, and `probe/main.go`.
The static image was built offline from those sources with no base image or
registry access.

## Repeated results

| Profile | Iterations | Workload A mapping | Workload B mapping | Result |
| --- | ---: | --- | --- | --- |
| Exact | 3 | sparse host IDs beginning at 100000 | disjoint sparse host IDs beginning at 100020 | 3/3 passed |
| Bounded | 3 | container range 0–4095 to host range 100000–104095 | container range 0–4095 to host range 104096–108191 | 3/3 passed |

Every iteration created two fresh workloads using the same container IDs. The
exact profile mapped only UID 0, 100, and 1001 and GID 0, 5, 100, 2001, 3001,
and 3002. GID 5 was explicitly justified as runtime-only `devpts` authority.
The bounded profile mapped exactly 4096 private UIDs and GIDs. Host mappings
were subordinate, non-root, non-overlapping, and free of known identity or
delegation aliases.

All six trials passed the following groups of checks:

- declared supervisor transitions, undeclared-policy rejection, exact unmapped
  denial, bounded in-range success, and bounded out-of-range denial across the
  complete required UID/GID syscall matrix;
- final application UID 1001, GID 2001, supplementary groups 3001 and 3002,
  empty capability sets, `NoNewPrivs: 1`, and seccomp filter mode 2;
- an exact `0/1/2` descriptor allowlist after closing six deliberately
  inherited non-`CLOEXEC` supervisor canaries;
- blocked post-drop identity changes, set-ID privilege regain, file-capability
  privilege regain, supervisor signaling, pidfd signaling, ptrace, proc-memory,
  and process-VM access;
- an opted-in supervisor capability mask of exactly `0x1c0`, a separate
  default-capability run with every capability set empty, and a
  profile-denied `vmsplice` round trip paired with a successful unconfined
  control;
- separate PID, IPC, network, mount, and user namespaces, private mount
  propagation, private `/dev/shm` and `/dev/mqueue`, and denial of cross-workload
  System V IPC, named POSIX IPC, TCP, UDP, abstract Unix sockets, process
  access, and private-state access, with a positive POSIX message-queue
  send/receive control in every producer workload;
- read-only roots, one managed writable volume, no privileged mode, host
  namespace, host device, external bind, or runtime socket; and
- managed files owned by the exact expected subordinate host UID and GID.

## Docker and cleanup

The negative backend check positively identified Docker Desktop Engine 29.6.2,
rejected it in the private harness before sending mapping syntax or a mutation
command, and observed identical Docker inventories before and after the check.
This proves the probe's pre-mutation backend gate, not the later Reploy product
integration.

Each trial reported no remaining prefixed container or volume. The complete
rootless Podman inventory was empty before the campaign and empty afterward,
including after final image removal. The harness exited zero only after that
inventory comparison passed.

The full machine-readable evidence remains reproducible from the recorded
command and source hash. It is not committed because it includes verbose host
and runtime inventories; this curated record contains the review-relevant
results without turning the repository into an environment snapshot.
