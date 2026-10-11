# Multi-identity Podman conformance

This private harness proves the candidate Linux identity boundary selected by
the multi-identity security contract. It does not add Reploy configuration,
runtime behavior, or a supported workload interface.

The harness builds an offline `scratch` image containing a static probe, runs
two concurrent workloads and two controls through both Podman mapping profiles, records
host-side and in-container evidence, and removes every resource it creates.
Docker Engine is inspected only so the harness can demonstrate rejection at
its own backend gate before sending mapping syntax or a mutation request. The
product-level Docker rejection remains later integration work.

## Prerequisites

- Linux with rootless Podman and a compatible OCI runtime;
- delegated subordinate UID and GID ranges for the current effective user,
  keyed by its canonical passwd account name or matching numeric UID;
- the enumerable `files` libsubid backend and the `getsubids` command;
- an explicit Podman seccomp profile reported by `podman info`;
- Go, Python 3, and a C compiler with the static POSIX C library; and
- a Docker CLI connected to the Docker Engine used for the negative backend
  check.

No registry access or image pull is required. The test must run without
`sudo`; needing host elevation or persistent host mutation invalidates this
slice's prepared execution path.

## Run

From the repository root:

```shell
tools/conformance/multiidentity/conformance.py \
  --profiles exact,bounded \
  --iterations 3 \
  --output-dir /tmp/reploy-multiidentity-conformance
```

The output directory must be absent or empty. The default is a fresh directory
under `/tmp`.

The harness verifies the entire caller delegation before image/volume mutation.
Each primary and control container starts in an inert trusted admission wait;
only verified effective maps release its payload. The per-check NSS cache is
not reused across admissions.

Each iteration creates new containers and managed volumes. It proves:

- exact sparse and bounded private UID/GID mapping geometry;
- distinct subordinate host authority for two workloads using the same
  container IDs;
- declared supervisor transitions and rejection of undeclared identities;
- raw in-range and out-of-range transition behavior;
- exact child credentials, groups, descriptor allowlist, capabilities,
  `no-new-privileges`, and seccomp state;
- authoritative per-ID NSS collision checks, plus agreement between the
  enumerable subordinate-ID files and the active libsubid result, recognizing
  both login-name and numeric-UID ownership entries;
- injected UID and GID collision cases that must fail closed for an NSS
  principal and for another principal's subordinate delegation;
- blocked privilege regain through set-ID and file-capability execution;
- a seccomp-denied `vmsplice` round trip paired with a successful unconfined
  control that proves the denial is attributable to the selected profile;
- private PID, IPC, network, mount, `/dev/shm`, and `/dev/mqueue` boundaries,
  including positive `shm_open` and `sem_open` round trips;
- process, state, System V IPC, named POSIX IPC, TCP, UDP, and abstract Unix
  socket isolation;
- expected subordinate ownership on managed persistent files;
- absence of privileged mode, host namespaces, host devices, external binds,
  and runtime sockets; and
- Docker Engine rejection by the private probe before mutation.

The caller identity is resolved from the effective UID's passwd entry. The
environment-selected login name must agree with that account, and real and
effective UIDs must match; only the canonical name and its numeric UID alias
are accepted as delegation owners.

Container GID 5 is mapped explicitly because the rootless OCI `devpts` mount
requires it. It is runtime-only authority and the supervisor rejects it as an
application identity.

## Evidence and cleanup

`host.json` records the Reploy parent head, probe source hash, host delegation,
Podman and OCI runtime versions, seccomp path and hash, runtime-required IDs,
and supervisor capability rationale. Each trial writes `evidence.json` and
`cleanup.json`; `summary.json` contains the compact result and engine inventory
comparison. `commands.txt` contains every executed command.

The [current evidence](EVIDENCE.md) records six passing trials, a matching
compatibility audit and durable receipt hashes outside `/tmp`. Their four-source
identity is `882ecc0a1402a5772573c5bd7da069e305a78f47a4d0509609c3ff97e4e2e840`.
The previous `ddd560bfb...` identity is retained as a historical pre-repair
observation. The current raw receipts include before/after source snapshots and
exclude build-context binaries and Unix sockets. The recovered test environment
uses explicitly recorded user-owned runtime paths and a disposable graph store.

The bounded profile validates each UID and GID map independently as an ordered,
contiguous affine mapping of container IDs 0–4095. Focused tests reject
reordered, gapped, overlapping, offset-changing and non-positive segments,
including malformed extra UID or GID maps that would otherwise be skipped by a
pairwise iteration.

Fixture builds disable intermediate layers and force failed build-container
removal. To exercise the real failed-`RUN` regression, explicitly select a
disposable Podman store and set `REPLOY_MI_LIVE_BUILD_FAILURE_TEST=1` for the
focused unittest command below. Its injected failure must restore both image
and external build-container inventories; ordinary CI runs skip this live test.

Resources use a `reploy-mi-<token>-` prefix whose token includes 128 bits of
cryptographic randomness. Cleanup verifies the attempt label before removing
exact container IDs, volume names, networks or the unique probe image. The
compatibility audit records each deterministic name before its create request;
if a response is lost or a later inspect fails, it reconciles that name and
label before cleanup. Final cleanup retries the deduplicated deterministic
attempts and verified resource identities, while structured HTTP status keeps a
real 500 engine error distinct from a true 404 absence. Foreign name collisions
and engine errors are preserved as failures. A successful process exit
additionally requires the complete Podman container, image, volume and network
inventory to match its starting state.

Passing the harness confirms that the selected Podman mechanisms can satisfy
the frozen boundary on the recorded host. It authorizes later design work; it
does not implement host bootstrap, durable allocation, lifecycle handling,
public identity or capability declarations, or production support.

## Readiness operation audit

After the mandatory mechanism campaign passes, run:

```shell
python3 tools/conformance/multiidentity/operation_audit.py \
  --output-dir /tmp/reploy-multiidentity-operations
```

This starts an attempt-owned local compatibility service on a fresh socket and
stops it afterward. Native Podman supplies only the offline fixture and independent
observations; audited lifecycle operations use the scoped compatible endpoint.
The audit invokes Reploy's actual read-only build preflight. It does not bypass
that gate to launch a shared BuildKit builder. Each operation records compatibility
or a blocker separately; zero exit establishes audit completion and cleanup,
not that every operation is supported. Stock mail has its own readiness gate.

```shell
python3 -m unittest discover -s tools/conformance/multiidentity -p 'test_*.py'
go test ./tools/conformance/multiidentity/probe -run Admission
```
