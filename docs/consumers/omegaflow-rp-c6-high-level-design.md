# OmegaFlow RP-C6 high-level design

Approved architecture for LLD engineering and slice planning. Requirements are in
[the approved baseline](omegaflow-rp-c6-requirements.md). This document does not
claim implementation readiness, platform qualification or consumer acceptance.

## Outcome and existing constraints

A consumer selects an immutable image, evaluates its inherited and application
environment, composes its blueprint, prepares and inspects the final workload,
and launches only that inspected state. Reploy refuses relevant changes before
bootstrap. OmegaFlow owns its policy and expected payload inventory. Reploy owns
image delivery, safe observations, retained contents and admission enforcement.

Keep Reploy's Go implementation, native blueprint/YAML and structured JSON
interfaces. Extend the existing build, generation, provider store and controlled
session machinery. Add no inspection daemon, general filesystem snapshot service,
independent receipt authority or parallel build pipeline.

Public host coverage inherits Reploy's existing Linux, native Windows and native
macOS commitment and includes WSL2 with Docker Desktop's Linux-container engine.
WSL does not substitute for native Windows. The initial OmegaFlow workload
profiles remain Linux/amd64 with the three qualified Bash builds in the
requirements. Host and workload architecture are separate qualification facts.
Existing staging/install-scope differences remain. This work adds neither
Windows-container workloads nor Podman, rootless or remote-engine support.

The same integrity, refusal and cleanup contract applies on every supported
host. Record actual versions and filesystem locations in qualification evidence.
See [the platform contract](supporting/omegaflow-rp-c6-high-level-design/platform-qualification.md).

## Actors and authority

| Actor | Responsibility and authority |
| --- | --- |
| Consumer | Authors blueprint, expected file inventory and policy; decides whether reported facts are acceptable; supplies expected prepared identity to launch. |
| Host Reploy | Owns local engine access, selection/preparation, private retained metadata, observation validation, locked admission, session ownership and cleanup. |
| Trusted Reploy container helper | Performs bounded filesystem observations or transports existing session frames. It executes no workload command during inspection and grants no engine access to the controller. |
| Workload/controller application | Uses only its existing session authority and declared resources. It cannot authorize a caller-written observation or alter protected retained contents. |
| Container engine | Supplies immutable images, read-only root/mount enforcement and exact resource identities through the existing trusted local-engine boundary. |

Do not strengthen hostile-host-root or same-identity isolation claims. A public
prepared identity is a comparison input, not a new permission to launch. Existing
host authorization and controller/workload session ownership remain necessary.

## Architecture

### 1. Retain the selected image through ordinary preparation

Expose a host-side structured selection view using the current image resolver
and descriptor. It contains the selected immutable image/platform and inherited
environment before reserved consumer composition. Retain the selected descriptor
under existing preparation ownership; the authored image reference remains
provenance, not a later instruction to resolve a different image.

The consumer carries the expected selection into ordinary final preparation.
The build uses that exact selected upstream image and normal provider graph;
it does not silently substitute a newly resolved tag or edit the consumer YAML.
The final generated image can contain the graph's ordinary declared changes and
protected payload layer. Report its distinct final identity after preparation.
A different selected upstream image requires a fresh view and consumer check.

Return inherited, application and final effective environment as distinct fact
layers. Preserve the inherited facts even when composition overrides values.
Configured private values stay outside reports, comparison identities and errors.
The existing controlled-session refusal of private environment injection remains.

### 2. Capture supplied bytes and deliver them in the normal immutable image

Add a generic protected supplied-file input to existing preparation. It carries
a bounded complete inventory, expected digests, destination paths and executable
versus data modes. Bind the manifest's expected digest independently. The public
syntax and exact bounds are LLD work; they must support the current 18 payload
files plus manifest and approximately 7.9 MiB without copying arbitrary app data.

Open sources safely and stream into private store artifacts using expected-byte
verification. An observed digest cannot become its own expectation. Publish only
a complete verified inventory; failed or cancelled captures publish no usable
prepared record. Source aliases, unsupported file types and escaping paths refuse.
The retained objects must be independent of the original files.

Materialize a deterministic, verified payload archive into the **existing normal
generated-image build**, with no workload RUN command for this step. Encode
`0555` directories/executables and `0444` data; do not preserve source numeric
ownership. Check destination/ancestor collisions and verify the resulting image's
exact selected inventory against expectations before publishing its generation.
Keep the declared container paths, including `/omegaflow-runtime`. Collision and
inventory checks use the trusted probe, not image `/bin/sh` or image utilities;
existing helpers that execute those tools cannot be reused unchanged.

This is a protected-copy implementation. Placing retained bytes in the normal
immutable generation avoids making the integrity boundary depend on Windows,
macOS or shared-filesystem chmod semantics or a surviving source bind. Use the
existing image ownership and store reachability for retention and cleanup.
Later original-source mutation or removal cannot change the published image.

The consumer inspects protected-file delivery facts and contents at declared
paths, not an assertion that the final physical implementation is a source bind.
Its older bind-specific checks must reconcile with its corrected acceptance of
protected copies. Reploy must not encode OmegaFlow names, Bash policy or its
qualified-build table.

### 3. Observe final facts with trusted, non-workload code

Build the view from the verified current generation and actual effective runtime
plan. Report image/platform, environment layers, numeric identity, effective
mount declarations including read/write/tmpfs, endpoint bindings, selected command
and protected inventory. Run only the packaged, release-matched static Reploy
probe in a read-only networkless inspection container; never start the workload
entrypoint, bootstrap shell, package manager or an image-provided inspector.

Use an inspection-specific fixed create policy. Explicitly disable inherited
image healthchecks (`--no-healthcheck`) and every image-controlled automatic
execution path. Drop unnecessary capabilities, use no-new-privileges, fix the
trusted helper entrypoint and sanitize its execution environment. Validate image
volume declarations and the actual mount map before starting inspection. Reuse
the probe/workspace protocol, not the current container-create policy unchanged.

Qualification must include an image whose enabled healthcheck emits an execution
marker: normal enabled execution is the positive control; public inspection must
leave that marker absent while returning its expected observations. Exercise the
same no-image-execution constraint on refusal and cancellation paths. This is a
design obligation; the current source profile has not been fixed by this document.

Extend the existing probe for bounded path resolution, terminal type/digest and
required-absence observations. Resolve ancestor and final symbolic links with
explicit limits; do not equate a broken link, permission failure, non-regular
file or size-limit failure with absence. A required digest is computed from the
opened inspected regular file. No raw Bash or system-rc byte export is required.

The immutable image preserves Bash, `/bin/sh`, loaders and image libraries;
OmegaFlow supplies no dependency inventory for those files. Read-only root is
necessary but not sufficient: Reploy must also reject effective mounts that hide
protected payload or image execution/configuration namespaces, including aliases
and automatic image-volume/engine mounts. It must protect required absence from
being filled by a mutable mount.

Reploy owns deriving and validating that image namespace boundary. It includes
resolved invocation/loader paths and applicable execution/configuration lookup
roots, not only `/bin/bash` as a string. LLD must prove this for all three required
Bash profiles without a new general dependency-analysis service. If the boundary
cannot be enforced simply, revisit this architecture before calling a slice ready.

Application data and `/run/omegaflow` sockets/FIFOs remain live. Hash neither
arbitrary video inputs nor transient socket contents. They cannot shadow the
protected namespace. Their ordinary runtime failures do not invalidate retained
protected contents.

### 4. Bind observations to existing generation admission

Store the validated observation projection and its private comparison bindings
under existing deployment/generation metadata. Return a versioned opaque public
identifier for that record. Do not expose private generation/build/plan digests
or sign arbitrary caller-provided facts. No new global signing service or
credential authority is needed.

Bind selected/final image identities, generation, protected capture, command and
all policy-relevant environment, identity, mount and endpoint facts. Bind the
inspection request and its observations. Exclude per-run ephemeral coordinates
such as newly allocated run IDs from the stable projection; their allowed
creation remains constrained by the same existing session plan.

Expected identity is an optional comparison input on the existing public session
entry. The protected-consumer flow must supply it. Host Reploy loads the retained
record and derives the current projection itself under normal operation locks.
Unknown, malformed, oversized, unsupported-version, unavailable or mismatched
records refuse. A caller-written JSON report grants no admission.

Queue admission can release and reacquire locks. Revalidate the selected state
and protected resource retention after those waits and before bootstrap; do not
rely on a check made only at initial load. Keep the final image and retained
resources pinned through actual use under existing session ownership. A missing
retained image/capture refuses instead of pulling a mutable alias as fallback.

A public identity may be reused while its same bound generation/state remains
available and existing launch authorization allows it. It is not a single-use
credential. A generation or relevant state change requires fresh inspection.
Tag movement with the same retained image, original-source changes after capture
and live app-data changes do not alone invalidate it. Exact retention bounds,
record deletion and replay errors must be resolved in LLD.

### 5. Preserve the session contract across existing hosts

The host retains admission and supervision authority on all platforms. The current
Linux-only host socket and watchdog launch are prerequisites to resolve, not
reasons to narrow the public host contract.

Proposed portable control transport: use the existing pinned engine connection
and a trusted packaged controller helper over a private engine-attached stream.
Keep the controller-side Unix socket/broker inside the Linux controller container;
relay existing bounded session frames to Host Reploy without exposing an engine
socket, host listener or new network capability to application code. Host Reploy
still validates every operation against the same frozen authorization and owns
the workload PTY. The relay makes no policy decisions.

Use one host-independent channel boundary if the proof supports it, preserving
the existing protocol and lifecycle semantics. Compare the change with keeping
the Linux route plus host-specific adapters before choosing the final LLD. Do
not silently add multiple transports or relax claim, peer, cancellation or
disconnect guarantees. Container-internal Linux helpers remain Linux binaries;
that is compatible with native Windows/macOS host Reploy.

Keep the existing watchdog cleanup algorithm, immutable exact resource manifest,
incident receipts and endpoint pinning. Add the necessary native child-launch
and parent-liveness adapters; reuse existing Windows/macOS boot identities and
local Unix/named-pipe engine endpoints. Parent death must leave an independently
owned cleanup path with no broader resource authority. Platform process/handle
inheritance, engine loss/restart and recovery are proof gates, not inferred from
cross-compilation. A watchdog that disappears with its parent is not acceptable.

## End-to-end journeys and failure behavior

1. Select image and inspect inherited/application facts before reserved composition.
2. Compose native blueprint and prepare against that selected upstream identity.
3. Verify supplied expected bytes, incorporate them through normal build and
   publish only a complete verified final generation.
4. Inspect final workload facts with trusted non-workload code; obtain the public
   immutable comparison identity and apply consumer policy.
5. Invoke existing controlled-session entry with expected identity. Revalidate
   current state after admission waits, then start only the frozen owned plan.
6. Complete or cancel through the existing lifecycle. Release resources and keep
   only generation-reachable artifacts and required recovery receipts.

Capture/probe failure produces no admitted record. Stale/malformed admission
refuses before bootstrap and cleans its reservations/inert resources. Concurrent
rebuild cannot retarget a pending or live run. Parent loss invokes exact owned
cleanup; daemon unavailability retains recoverable ownership and incidents rather
than claiming containers were removed. No refusal test passes unless it also
checks that bootstrap did not begin where required.

## Alternatives and proportionality

| Alternative | Assessment |
| --- | --- |
| Verified private copy plus read-only source bind | Small on native Linux and the narrow WSL proof passed. Cross-platform delivery still needs mode, path-sharing, alias, lifetime and cleanup proofs. Prefer normal immutable-image delivery for one portable retained-byte boundary. |
| Protected files in existing generated image | Proposed: reuse build/verification/generation ownership, preserve exact paths/modes, remove later source mutation from the launch dependency. Requires a generic input/graph cut and final inventory verification. |
| Offline general image filesystem reconstruction | Avoid as the primary path: existing account-file archive reader is intentionally narrow; general layer/link/whiteout semantics would add substantial parser work. Reuse trusted probe instead. |
| Host-specific sockets and a separate portability implementation | Compare with engine-attached transport during LLD; retain multiple paths only if the proof and cost of replacing the existing route justify them. |
| New snapshot service, broker authority or permanent daemon | No requirement justifies them; not proposed. |

The initial responsibilities estimate is historical. Payload delivery and portable
session prerequisites must be sized from actual engineering cuts. Do not label
them test-only qualification work or hide them in an integration-only final slice.

## Requirement coverage

| Requirement | Owning architectural responsibility and observable acceptance |
| --- | --- |
| R1 | Retained selected upstream descriptor through ordinary preparation; tag movement cannot substitute image B for selected A. |
| R2 | Separate inherited/application/final environment; forbidden inherited values remain visible and private configured values remain absent. |
| R3 | Verified final plan projection and trusted-only probe; workload/bootstrap execution marker remains absent during inspection. |
| R4 | Bounded path/type/hash/absence probe; explicit link, type, unreadable and over-limit failures. |
| R5 | Expected-hash capture plus separately bound manifest, normal-build final inventory check; substitution and capture races refuse. |
| R6 | Immutable generation payload with read-only runtime and namespace/mount protection; post-capture source changes cannot replace it. |
| R7 | Explicit live data and session-resource paths; mutation is ordinary runtime behavior and cannot shadow protected state. |
| R8 | Public opaque comparison record at existing locked admission; caller-written facts cannot authorize launch. |
| R9 | Stable actual-state projection, post-wait revalidation and retained resources; paired success/refusal tests distinguish external reference movement from actual bound changes. |
| R10 | Existing generation reachability, leases, native supervision and exact cleanup/recovery; no leaked active workload or reservation after required cancellation/refusal paths. |
| R11 | Complete public path on supported native hosts and WSL2/Desktop, with exact versions/paths and actual release adoption by OmegaFlow. |

## Engineering gates before LLD readiness

- Prove selected-image retention and environment provenance through normal builds.
  Resolve duplicate inherited environment entries and private-value redaction.
- Define generic protected-file declaration, inventory/manifest trust, safe native
  source opening, deterministic modes/archive and exact final-image verification.
  Specify numeric limits and preserve source-change independence.
- Prove component-aware observation and image execution/lookup namespace protection,
  including hidden mounts, automatic volumes, absence and aliases. The three
  required Bash profiles cannot silently become unsupported.
- Prove the proposed engine-mediated private stream preserves the existing claim,
  framing, controller identity, disconnect and lifecycle boundaries. Compare the
  simpler retained-route alternative before committing to a transport replacement.
- Prove native watchdog child survival, private handles, parent-loss detection,
  receipt ownership and exact cleanup on Windows and macOS; retain Linux parity.
- Specify opaque identity encoding, bounded record storage, lifetime/replay,
  generation reachability, post-queue comparison and race-free use.
- Define and obtain full native-host plus WSL2/Desktop acceptance evidence for
  success/refusal/cancellation/parent-loss/engine-loss/cleanup and real filesystem
  classes. Build/smoke passes and the protected-copy fixture cannot substitute.
- Produce the complete slice inventory with production/configuration and test-code
  estimates, totals and the agreed changed-physical-line counting convention.
  Shared portability foundations have one owning prerequisite. No working-hour
  estimates; no deferred consumer obligation without owner agreement.

Failure of a gate can invalidate its proposed mechanism or estimates. Reassess
before delivery rather than accumulate exceptions or claim readiness. Reviewed
HLD acceptance permits detailed engineering; it does not by itself approve an unresolved
LLD, implementation, commits, publication or release.

Optional host endpoint publication remains a separate dependency. This design
does not activate mapped execution, resume E2.3 or recover the OmegaFlow campaign.
Consumer acceptance also requires qualification against an explicitly selected
compatible Reploy release.

## Supporting material

- [Source investigation and narrow mechanism proof](supporting/omegaflow-rp-c6-high-level-design/source-investigation.md)
- [Existing public-host contract and qualification evidence](supporting/omegaflow-rp-c6-high-level-design/platform-qualification.md)
- [Observed local versions and filesystem paths](supporting/omegaflow-rp-c6-high-level-design/local-proof-environment.json)
- [Maintainer platform clarification](supporting/omegaflow-rp-c6-high-level-design/platform-contract-clarification.json)
- [Preserved requirements-phase workflow document](supporting/omegaflow-rp-c6-high-level-design/requirements-workflow-preimage.txt)
- [Document and estimate contract](supporting/omegaflow-rp-c6-requirements/design-contract.md)
