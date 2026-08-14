# Multi-identity Podman conformance

This private harness proves the candidate Linux identity boundary selected by
the multi-identity security contract. It does not add Reploy configuration,
runtime behavior, or a supported workload interface.

The harness builds an offline `scratch` image containing a static probe, runs
two concurrent workloads through both Podman mapping profiles, records
host-side and in-container evidence, and removes every resource it creates.
Docker Engine is inspected only so the harness can demonstrate rejection at
its own backend gate before sending mapping syntax or a mutation request. The
product-level Docker rejection remains later integration work.

## Prerequisites

- Linux with rootless Podman and a compatible OCI runtime;
- delegated subordinate UID and GID ranges for the current user;
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

Each iteration creates new containers and managed volumes. It proves:

- exact sparse and bounded private UID/GID mapping geometry;
- distinct subordinate host authority for two workloads using the same
  container IDs;
- declared supervisor transitions and rejection of undeclared identities;
- raw in-range and out-of-range transition behavior;
- exact child credentials, groups, descriptor allowlist, capabilities,
  `no-new-privileges`, and seccomp state;
- authoritative per-ID NSS collision checks, plus agreement between the
  enumerable subordinate-ID files and the active libsubid result;
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

Container GID 5 is mapped explicitly because the rootless OCI `devpts` mount
requires it. It is runtime-only authority and the supervisor rejects it as an
application identity.

## Evidence and cleanup

`host.json` records the Reploy parent head, probe source hash, host delegation,
Podman and OCI runtime versions, seccomp path and hash, runtime-required IDs,
and supervisor capability rationale. Each trial writes `evidence.json` and
`cleanup.json`; `summary.json` contains the compact result and engine inventory
comparison. `commands.txt` contains every executed command.

Resources use a `reploy-mi-<token>-` prefix whose token includes 128 bits of
cryptographic randomness. Trial cleanup targets only those exact containers
and volumes, and final cleanup targets only the unique probe image. A
successful process exit additionally requires the complete Podman container,
image, and volume inventory to match its starting state.

Passing the harness confirms that the selected Podman mechanisms can satisfy
the frozen boundary on the recorded host. It authorizes later design work; it
does not implement host bootstrap, durable allocation, lifecycle handling,
public identity or capability declarations, or production support.
