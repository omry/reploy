# OmegaFlow scope, reuse and multi-identity compatibility assessment

Current-agent source/contract audit at `9ce81ef698e0882e6b9559e49ee1bfd8443b1fde`.
This assesses the proposed implementation direction, not an implemented feature.
The focused existing tests below passed. No independent review, native platform
qualification, complete LLD acceptance or implementation readiness is claimed.

## Decision and limits

Preserve the approved requirements and HLD. Use the existing controlled-session
entry, broker/protocol, image/build/store, protected-path policy and cleanup
algorithm. Limit new work to missing observations/capture/admission behavior and
the transport/process adapters needed by the existing public host commitment.

No inherent contract conflict was found with multi-identity E/F under the
constraints below. The largest overlap is the shared image-code/mount protection
foundation. Its production derivation is not implemented by E2.2, so it cannot
be counted as already available. Work brought forward by OmegaFlow must be
reusable by F without treating a Docker observation as mapped Podman proof.

The previous candidate total is not a reviewed or accepted delivery forecast.
Source inspection supports significant reuse but does not establish a cheaper
replacement total. Do not lower estimates to satisfy an expectation, count
future F integration as already delivered, or assume every platform needs a
separate implementation of the feature.

## Concrete waste controls from source

| Area | Existing implementation | Minimum extension and waste to avoid |
| --- | --- | --- |
| Controller behavior | `controlledsession/controller_broker.go`, `controller_stream.go`, `client.go` already implement the public broker, bounded messages and session state. | Keep these. Add only the missing engine-stream/private-peer transport integration. The existing broker dials the host socket; it is not itself the new container-side transport endpoint. Neither a second controller broker nor claiming that portability is already solved is justified. |
| Host claim and authorization | `controlledsession/channel.go` already owns one claim, exact UID/GID comparison, opened-first ordering and bounded frames. | Preserve this contract through an appropriate transport adapter. Do not duplicate protocol validation or add another permission system. The container-side trusted peer result must remain bound to the exact host-created exec/container. |
| Engine connection | `docker_pty_attach.go` plus Windows/Unix dialers already provide pinned local connections and hijack/cancellation patterns. | Reuse endpoint selection/dialing. Non-TTY exec framing, multiplexing and ambiguous-start cleanup still need explicit engineering; a PTY adapter cannot simply be relabeled. Avoid host TCP listeners, controller engine access or a new engine SDK. |
| Watchdog | Shared `controlled_session_watchdog.go` owns exact-manifest cleanup, retries and receipts; Linux has the launch adapter. | Keep the shared algorithm. Prove/reuse a Unix adapter for macOS and a private-handle adapter for Windows. Do not implement separate cleanup engines, boot IDs or per-OS observation policies. |
| Source capture | Providerstore has `PublishExpected` and `OpenVerifiedArtifact`; existing source snapshots use held roots and verified copies. | Extend these primitives for the bounded complete expected-hash inventory and native alias checks. Do not build a snapshot service or copy/hash live application data. Store verification does not replace safe source opening or full inventory ownership. |
| Image payload | `portable_runtime_payload_image.go` already owns ordinary payload-layer build and final inventory comparison. | Reuse build/archive/ownership paths. Replace image-shell collision checks for the new trusted-only inspection path; do not duplicate the build pipeline or generalize unrelated provider behavior by default. |
| Protected paths | `runtime_policy_compile.go` already compiles executable/link/provider roots and checks mount overlap. E2.2 checks complete resolved code and mount proof. | Extend canonical evidence and these checks, with one namespace foundation. Do not add an independent OmegaFlow-only policy/compiler. Full loader/configuration lookup derivation is still unresolved and is not proven by current lexical overlap checks. |
| Admission | Current session entry already owns generation selection, sorted operation locks, leases and queue waits. | Add an opaque expected-state comparison to the same path, recheck after waits and retain resources through use. No parallel launcher, receipt authority or lock hierarchy. |
| Testing | Existing protocol, broker, protected-path, proof-refusal and lifecycle tests cover shared behavior. | Reuse them. Add changed-boundary tests and complete required public acceptance per host. Do not write a new exhaustive per-OS protocol suite or repeat a passing check without changed risk. Cross-compilation is not native lifecycle proof. |

Using a different ordinary application launch command would not satisfy approved
R8, which explicitly requires the existing controlled-session entry. It is not
a legitimate shortcut without an approved requirements change. The current
architecture does not require another launcher.

## E/F compatibility crosswalk

| Shared concern | Existing multi-identity authority/owner | Compatibility requirement for OmegaFlow |
| --- | --- | --- |
| Identity declarations and grants | D records users/groups/roles/capability intent; E authorizes actual native startup. | Supplied-file declarations and an inspection identity grant no UID/GID, capability, mapping or launch authority. Keep ordinary empty-capability behavior and exact private-descriptor closure. |
| Immutable image/code closure | E2.2 `mapped_sandbox_proof.go`; E design assigns production closure/mount observations to F. | Add image/payload evidence through shared generation and protected-path machinery. Keep a complete trusted code-closure derivation reusable by F; don't treat the new public report as F's production proof. |
| Proof freshness | E2.2 binds proof to mapped request, generation/image, container/PID, runtime/network policy, complete code and resolved mount observations. | A public opaque inspection identity is only a comparison input. It lacks that live-instance evidence and cannot satisfy or bypass this gate. Reuse common facts through a future trusted adapter, not by decoding caller reports into mapped proof. |
| Managed storage | F1/F2 require descriptor-safe owned objects, exclusive prepare/attach, restart inhibition and exact recovery. | Capturing an external source during preparation must produce independent generation-owned contents. It does not authorize arbitrary external runtime binds in mapped workloads. Reuse descriptor/inventory helpers where sufficient; F keeps attachment/ownership proofs. |
| Lifetime and replacement | E3.1 owns native-master lifetime, leases and host-admitted replacement; engine-autonomous restart cannot bypass admission. | Retain host-owned cleanup, exact resource IDs and journal/receipt handling. A portable host watchdog does not introduce a privileged resident workload supervisor or grant the controller resource-creation authority. |
| Every execution entry | E4.1/E4.2 require authority checks across commands, tests, shell, hooks, controlled sessions, replacement and exec. | Keep the current public unmapped guard. Expected-state success must not unlock identity_policy/identity_mapping. Future mapped session entry still needs the same E/F/G gate and final startup boundary. |
| Backend/host qualification | Mapped cohort is non-root staging/user-scope local rootless Linux Podman; public activation stays closed. | OmegaFlow Docker/Desktop host evidence is separate. It neither activates mapped execution nor qualifies Podman/network/storage behavior. No expansion of mapped host/backend scope is adopted. |
| Lock and schema continuity | E uses the existing writer and F observation provider must not reacquire OperationLock. Retained generations/legacy admission state remain meaningful. | Add records beneath existing ownership, retain current lock ordering and revalidate after queue waits. Do not alter mapping journal/binding semantics, rewrite legacy states or add unreviewed required fields to old records. |
| Same-identity authority | A same-UID role name does not create isolation; privileged native masters retain their accepted authority. | The relay must run at its declared existing controller authority with protected helper code. Peer UID and helper packaging do not promise isolation from other processes with the same accepted identity. |

## Ownership and delivery ordering

1. During OmegaFlow LLD, define one immutable namespace evidence foundation using
   the current protected-path representation, with explicit resolved aliases,
   lookup roots, required absence and mount inventory. Prove a bounded solution
   for the three required profiles. No general dependency-analysis service.
2. Own that foundation once in the eventual OmegaFlow slice inventory. Mark its
   E2.2/F consumers and integration tests; F's mapped live evidence adapter and
   storage/attachment/network qualification remain separate unfinished work.
3. Own portable host transport/process adapters once under existing session
   machinery. E3.1 consumes the unchanged lifecycle contract rather than growing
   another watcher. Preserve protocol/release compatibility when modifying the
   packaged helper.
4. Implement selection, capture, safe inspection and expected-state admission as
   coherent extensions through the current build/generation/session path.
5. Qualify the full public path on the promised hosts, with exact versions and
   storage locations. Endpoint publication and consumer release adoption stay
   separate. No part of this audit resumes E2.3 or changes E/F approval.

This ordering identifies reuse; it does not reserve new slices, change approved
multi-identity documents or subtract unmeasured LOC from future F estimates.

## Review and delivery checks required by this assessment

- Current source calls `unmappedRuntimeDocumentV1` for both loaded session
  deployments. Retain no-effect refusal for identity policy, declared mapping and
  retained mapping authority. Add direct controller/workload expected-identity
  regression coverage when implementing that admission input. The general guard
  tests run below cover other real payload entries; they are not direct tests
  of the not-yet-implemented expected-identity path.
- Preserve E2.2 tests for wrong request/network, writable root, missing/substituted
  closure, required-code omission and direct/resolved mount shadow. Keep missing
  production F observation fail-closed. Root `/` is explicitly rejected as a
  code-closure shortcut.
- One shared fact library must not erase the distinction between immutable-image
  facts, current host-owned plan and fresh live-container observation.
- Retain ordinary unmapped lifecycle and frozen controller protocol behavior
  while adding transport adapters. Refusal, disconnect, parent death and exact
  cleanup must remain valid; no engine-autonomous restart bypass.
- Public opaque comparison identifiers confer no authorization. Private internal
  plan/generation digests remain outside public stream and diagnostics.
- Reuse Linux/WSL container evidence where it establishes identical shared code,
  but obtain actual Windows/macOS adapter and full public-path evidence. Do not
  weaken or silently drop a required acceptance case to reduce testing cost.

## Evidence and stopping point

Three focused Go test groups passed on the unchanged checkout: existing
controller claim/protocol/broker behavior; mapped sandbox and protected-path
contracts plus ordinary session orchestration; and mapped public-entry guard
behavior. Commands, exact source hashes and outputs are retained in
`/home/omry/.local/state/awd/reploy-E-resume-20261028/OmegaFlow-waste-compatibility-audit-evidence.json`.
These are current Linux/WSL checks, not native platform or unimplemented feature
qualification. No runtime fix is claimed.

Planning corrections: the earlier candidate labels did not clearly distinguish
existing broker/policy behavior from missing transport/namespace extensions.
This is a representation fix in the planning material. The underlying native
portability and complete namespace proof gaps remain. The production estimate
needs concrete delta sizing after those interfaces are settled; no lower total
is asserted by this audit.

See [the candidate assessment](candidate-boundaries-and-sizing.md),
[engineering readiness](engineering-readiness.md) and
[consumer reconciliation](consumer-adoption-reconciliation.md).
The approved requirements/HLD remain unchanged. The same parent LLD engineering
stage remains incomplete; this bounded audit is complete. Stop here for this
requested assessment without implementation, publication or E resumption.
