# F/G prerequisite audit

This working engineering audit supports
[the implementation plan](../../MULTI_IDENTITY_IMPLEMENTATION_PLAN.md).
It records existing mechanisms and remaining work. It is not a reviewed LLD,
an approved replacement slice map, or runtime qualification.

## Rules for the next design draft

- Trace each added mechanism to a concrete requirement and the existing path
  that cannot satisfy it. An old reference to a component is not evidence that
  it exists or that a new component is needed.
- Reuse accepted mapping, transaction, admission, startup and cleanup machinery.
  Do not introduce a shared daemon, global allocator, broker or parallel state
  store. The accepted per-installation controller reuses the existing runner;
  engineer and price its actual adapter/state work explicitly.
- Preserve the accepted public storage and mapping decisions. Do not reintroduce
  superseded flags or a compatibility version for an undeployed mapping API.
- Treat a private interface, injected observation or refusal test as a handoff,
  not a completed production provider or a positive compatibility proof.
- Keep all public mapped activation guards until the actual generic-launch
  evidence passes. Unresolved engineering cannot become a qualification boolean.

## Requirement-to-source assessment

| Requirement or accepted decision | Existing foundation | Work the LLD must actually own |
| --- | --- | --- |
| Stable private exact/bounded mappings and retained reservations | `internal/deploy/identity_mapping_geometry.go`, `identity_mapping_state_file.go` and operation-lock/state writers | Reuse the same state and guards for the selected destination. Do not add a mapping registry or allocation service. |
| `podman.identity_mapping` with staging/deployed selection | Strict schema-1 parser and typed whole-array overrides exist under the old runtime placement. Endpoint phase selection already exists. | Move the undeployed public input directly, update all opt-in detection and serialization/typed-override consumers, and preserve actual-host-ID meaning. G1.3 must connect destination-local overrides; the delivered loader is staging-only. No mapping-specific flags or schema-2 compatibility path. |
| Independent installed owner | Existing installation transaction, immutable build references, pending intents and owner-path checks | Connect independent destination admission, source/destination revision and overlap checks, and exact partial-publication recovery. Do not copy reservations, admissions or mapped data. |
| Existing contextual owner defaults; optional explicit owner/group/permissions; directories before files | Contextual runtime-user resolution and descriptor-owned private metadata helpers exist. | Add only declared inert-object metadata and confined preparation. `fsowner` protects private metadata; it is not already the application-storage preparer. No recursive chown/chmod, foreign-tree adoption or content/template system. |
| Fresh ownership authority before preparation, exclusive attach/prepare and crash inhibition | Existing operation lock, retained identity state and inhibited native-intent machinery | Define the exact managed resource record, effect ordering and recovery observations. A local lock or stopped process does not prove that every attachment is gone. Reuse the transaction; do not invent a global resource manager. |
| Intrinsic image root is read-only by default; explicit root-mount writable opt-in | Existing sandbox defaults to read-only root and rejects `/` as an ordinary mount destination. | Give the intrinsic root policy its own representation inside the mount model. Do not admit a bind over `/`, apply ordinary managed-data preparation to root, or disable overlap validation for other mounts. |
| Trusted startup/control paths remain protected even with writable application root | Existing canonical runtime protected paths and the private mapped sandbox proof interface | Engineer the actual production observation and protection. Current mapped create and proof validators insist on read-only root; changing blueprint parsing alone cannot implement the approved opt-in. |
| Pre-release mount/code/kernel/network verification | `mapped_sandbox_proof.go` defines the expected request/proof binding; E supplies no production provider. | Establish evidence provenance and effective observations. Do not count the interface as working F implementation or add an arbitrary dependency-discovery framework without showing necessity. |
| Existing egress policy plus private, non-peer-reachable installations | Existing nftables policy compiler/installer, declared endpoints and Podman request transport | Prove the selected rootless network transport, DNS, peer denial, endpoint publication and exact cleanup. User-facing public/local egress meanings stay unchanged. |
| Effective network policy before application execution | The mapped waiter waits for host release, then calls `sandboxAndExecApplicationV1`; that function installs nftables rules. | Resolve the ordering with F's requested pre-release effective observation. Do not report installed rules before they exist or silently move application execution ahead of admission. Choose the smallest implementation after tracing the existing trusted transition. This remains an engineering prerequisite. |
| Host-owned checked replacement and active revocation | Private E retirement/reload ordering and workload-lifecycle state exist; production effect providers and replacement composition remain unavailable. | Implement and qualify exact retirement, fresh reload, startup confirmation, lifetime handoff, restart fencing and uncertain cleanup. Never obtain a pass by removing a required positive journey. |
| Correct process-entry semantics and explicit roles | Existing command-to-executable roles and ordinary transient shell path; private E entry classifiers/refusals | Classify the actual adapter rather than assuming every operation called `shell` is occupied exec. D already refuses a shell/raw path without an accepted definition. Do not invent a default shell role or replace genuine occupied exec with a fresh container. |
| No engine restart bypass under the selected controller | Mapped create sets restart to `no`; observation rejects changed policy/count. | Retain one-shot startup and fresh mapped replacement after failure. The approved permission for qualified unchanged-container retry remains available, but this controller does not need a parallel retained-approval mechanism. |

## Recovery direction and remaining engineering

See [the lifecycle analysis](fg-lifecycle-options.md). The maintainer requires
Reploy-owned restart and recovery, with systemd as a possible host mechanism.
Systemd can invoke the same checked operations; manual versus automatic
invocation is not the security boundary. The maintainer accepted the lean
per-installation controller direction, including replacing lifetime-long Docker
CLI children. Engineer and size the adapters and boot/session behavior; the
direction does not approve new platform scope or silent ordinary-install
migration. Current start/stop, independent boot/login enablement and exit retry are separate.
Security/preparation/recovery fences remain durable across all activations.

## Proof and sizing limits

The successful source-level tests and their limits are recorded in the lifecycle
analysis. The local `podman --version` reports 4.9.3 and warns that
`/run/user/1000` is absent. A binary version is not a running eligible rootless
engine, compatible transport proof, or network/mount qualification. Existing
campaign guest evidence must be resolved and checked for reuse before scheduling
new disposable engine proofs; no Podman checkout or host service is to be changed.

The original combined F/G forecast remains 2,400–4,200 production lines and
4,300–7,050 test/fixture lines. This audit does not replace it with a guessed
number. Revised counts must come from the selected mechanisms, actual missing
providers, failure/recovery/platform checks and ordered slice responsibilities.
Count shared work once, identify the unit, and retain qualification-only slices
as zero production when appropriate. Target 500 production lines per PR; split
before the 750-line maximum.

The engineering authoring function is not complete. No fresh design stamp,
review approval, implementation readiness or public mapped support is claimed.
