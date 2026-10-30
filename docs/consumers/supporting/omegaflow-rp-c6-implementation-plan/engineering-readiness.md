# RP-C6 LLD engineering and proof readiness

HLD approval permits LLD engineering and slice planning. It does not accept an
unproven namespace boundary, qualify a native host or authorize implementation.
Accepted requirements/HLD decisions remain unchanged. Original approved bytes
remain in the retained preimages and approval records; the documentation
checkpoint reconciles only stale approval/status wording. This material remains
an unfinished planning checkpoint, not an approved LLD. The full public support
contract remains Linux, native Windows, native macOS and WSL2, with the same
integrity/refusal/cleanup guarantees.

## Bounded evidence retained

[Proof identities and original locators](bounded-proof-evidence.json) bind the
real records. These are mechanism observations, not final public qualification.

- A static disposable Go helper in a scratch image relayed binary bytes through
  a non-TTY engine exec stream and a container-private Unix socket. The socket
  refused UID 2000; the UID/GID 1000:1000 client exchanged exact bytes including
  NUL and 0xff. Closing/unlinking after claim refused a second connection.
  Exact owned image/container cleanup returned exit 0. The helper used read-only
  root, no network, dropped capabilities and no-new-privileges. It did not mount
  the host engine socket or publish a listener. This ran only on WSL2/Desktop.
- The first fixture attempt failed before READY. Its original source remains in
  its ignored scratch workspace and its failure receipt is retained. Replacing
  the fixture's bare blocking select with a timed hold permitted the second
  attempt above. This is a fixture correction, not a Reploy runtime fix.
- Three focused current watchdog tests passed: exact-manifest parent-loss
  cleanup, retry while Docker is unavailable, and rejection of a previous boot
  before ready. These exercise the existing shared algorithm with test backends;
  they do not prove a native detached child survives parent/process-job death.
- Cached Debian 12, Debian 13 and Ubuntu 26.04 candidates were created using
  immutable image IDs, no-healthcheck, a never-started entrypoint, no network and
  read-only root. Bounded docker-copy archives supplied Bash and loader config
  metadata to trusted host readelf. All three Bash files name
  `/lib64/ld-linux-x86-64.so.2`, `libtinfo.so.6` and `libc.so.6`; their loader
  configuration includes multiarch and `/usr/local/lib` roots. Each container
  remained `created` and was removed successfully. This is not a proof of all
  lookup roots, protection enforcement or qualified Bash version/digest.

## Chosen direction and finite unresolved engineering

Use one engine-attached host channel if the complete proof preserves claim and
framing semantics. The scratch test favors it over maintaining a host Unix
transport plus native adapters: it avoids dependence on host socket ownership
and sharing while keeping peer credentials inside Linux. The current engine
attach module already has Unix/named-pipe endpoint adapters. Production still
needs non-TTY Docker multiplexing, cancellation and exact-container exec binding;
raw CLI fixture success does not prove those. No final transport replacement is
implementation-ready yet. Keep all protocol bounds and host validation.

Keep the shared watchdog algorithm. Darwin can use inherited Unix descriptors;
Windows needs explicitly inherited private handles and child lifecycle flags.
The installed Go Windows syscall implementation exposes
`AdditionalInheritedHandles`, `NoInheritHandles` and `CreationFlags`. Their
existence does not establish process-job survival or leak-free native cleanup.
No replacement receipt authority, boot identifier or engine endpoint is needed.

Namespace protection must include ancestor aliases, invocation/loader paths,
applicable library/configuration lookup roots, supplied payload and required
absence. Lexical comparison against `/bin/bash` alone is insufficient. The
current probe resolves final symlinks only; the account archive reader is narrow
and must not become a general image reconstruction service. Bounded trusted
path resolution and a demonstrably sufficient namespace derivation remain open.
An image-parser/dependency system or weaker guarantee would require renewed
architecture review. No such expansion is adopted here.

## Design-readiness proof work, sized separately

These are disposable design proofs, not production foundations or approved
implementation slices. Test-code ranges are authored fixture/harness physical
lines, additional to the shipping candidate estimate. Reuse results where
sufficient; actual temporary code is not silently counted as shipped product.

| Proof | Production/configuration LOC | Disposable test-code LOC | Required result |
| --- | ---: | ---: | --- |
| Image namespace boundary on all three required profiles | 0 | 100–180 | Full path/alias/loader/config/absence and automatic-volume/mount-shadow refusal, alongside allowed live data; no image code during inspection. |
| Portable private stream parity | 0 | 80–140 | Single exact peer claim, frame limits/multiplexing, disconnect/cancel/ambiguous exec cleanup on Unix and Windows local endpoints. |
| Native macOS watchdog | 0 | 80–140 | Detached child survives abrupt parent exit, private descriptor inheritance, exact receipt and owned cleanup. |
| Native Windows watchdog | 0 | 100–180 | Explicit private handle list, parent/job-loss survival, no inherited liveness writer leak, exact receipt and owned cleanup. |
| **Total remaining design-proof envelope** | **0** | **360–640** | Separate from production implementation and full public-path acceptance. |

The envelope is provisional; real fixture size and results replace it. It does
not promise that production foundations can be implemented as a design proof.
The existing WSL stream and old shared-algorithm tests remain reusable evidence,
not substitutes for the listed unresolved controls.

## Execution prerequisites and next supported point

The current execution environment is WSL2. The maintainer confirms native
Windows and macOS machines are easily available; Linux is possible. This answers
the machine-availability question. Execution targets/access details are not yet
bound and no native proof is inferred. Availability no longer blocks engineering. The T3 linked-environment listing tool is not callable in this
session (`TypeError: tools.mcp__t3_code__t3_environment_links is not a function`).
Earlier Windows interop returned UtilAcceptVsock failures; a bounded recheck did
not produce a retained result and establishes no new version/access fact.
Native host target/access information is needed before claiming those mechanism
proofs. Existing Windows CI explicitly skips Docker and cannot stand in for
full native acceptance. Do not drop platforms or use WSL as native evidence.

The current responsible action is
`f01-f05-s02-inspect-current-source-and-real-prerequi` in
`run_3k0spmw1cd3w8pmjsq65ggeese`. Source investigation and the narrow results
above are retained. Important engineering rules and native proof remain open.
The canonical LLD and implementation inventory have not been authored, reviewed
or accepted; do not advance to a ready implementation plan from this material.

See [candidate ranges and source basis](candidate-boundaries-and-sizing.md) and
[consumer adoption reconciliation](consumer-adoption-reconciliation.md).

## Subsequent bounded scope/compatibility check

The [source/contract audit](scope-and-multi-identity-compatibility.md) is complete.
It identifies shared E/F ownership, reuse and required regression boundaries.
It does not complete the broader LLD/proof stage. Three focused existing Go
test groups passed on the unchanged source; the native and namespace proof
gates remain open. Do not revive the answered availability-only question.
