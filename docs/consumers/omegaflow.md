---
status: Requirements and HLD approved; LLD paused for milestone E
updated: 2026-10-29
summary: Approved OmegaFlow RP-C6 requirements and architecture; detailed engineering and delivery slices remain unfinished.
---

# OmegaFlow

## Design outputs

The active `global:swe:design` run retains this consumer record and its required
tracking. Its additional outputs follow the
[design document contract](supporting/omegaflow-rp-c6-requirements/design-contract.md):
`omegaflow-rp-c6-requirements.md`, `omegaflow-rp-c6-high-level-design.md`, and
`omegaflow-rp-c6-implementation-plan.md`, alongside this file. Each is authored
at its responsible phase. Requirements and HLD are approved; the LLD and slice
plan remain unfinished and unapproved.
Durable supporting material belongs under `supporting/<owning-document-stem>/`.
Feedback for later AWD improvements is collected in
[design workflow feedback](supporting/omegaflow-rp-c6-requirements/design-workflow-feedback.md).

The approved [requirements baseline](omegaflow-rp-c6-requirements.md) carries the full
corrected RP-C6 scope. Its
[interview synthesis](supporting/omegaflow-rp-c6-requirements/interview-synthesis.md)
separates existing project constraints from engineering investigations. Requirements and HLD completed review and maintainer acceptance.
The approval records remain under the requirements and HLD supporting directories.
The HLD readiness gates still apply; no implementation-plan approval, platform
qualification or delivery readiness is implied.

## Use case and source

OmegaFlow needs to inspect the workload it will launch, evaluate its own policy,
and launch exactly that inspected preparation. Reploy must reject relevant
changes before workload or bootstrap execution begins.

Source: the 2026-10-29 planning draft in the OmegaFlow checkout,
`temp/part-c-delivery/implementation-through-C6.1/RP-C6-HANDOFF-PLANNING.md`
(read from `/home/omry/dev/omegaflow`), corrected by the maintainer-relayed
OmegaFlow scope clarification on 2026-10-29. The clarification below takes
precedence over the earlier handoff where they differ. These are consumer
requirements, not an approved Reploy API or an installed-release conformance
claim.

The subsequent [OmegaFlow clarification](supporting/omegaflow-rp-c6-requirements/consumer-clarifications.md)
removes raw Bash/system-rc byte export from the requirements scope and makes
invalidation depend on changes to the bound prepared state. It takes precedence
over older byte-export or unconditional tag-change language in source evidence
and preliminary estimates.

## Corrected ownership and supplied-input scope

OmegaFlow defines the blueprint; Reploy delivers and enforces it. Bash, `/bin/sh`,
loaders and libraries supplied by the workload image remain Reploy's
responsibility. OmegaFlow does not need to enumerate or snapshot that image
dependency closure. OmegaFlow owns compatibility checks against its qualified
Bash build table. Reploy must expose the resolved Bash and selected system-rc
facts for the generation actually launched. The selected system-rc path must be
absent; the consumer reports `/etc/bash.bashrc` for all three current profiles.

For consumer-supplied inputs, OmegaFlow reports a complete expected-SHA-256
inventory of 18 payload files plus `manifest.json`, approximately 7.9 MiB total:

- `bin/envoy` and `bin/awsh`; helper commands use the same Awsh executable;
- `etc/awsh-bashrc`, `etc/inputrc` and `etc/bash-builds.json`;
- one terminal database entry and 12 locale files; and
- `manifest.json`, with its expected digest supplied separately.

Retain established paths under `/omegaflow-runtime`. These are regular files
without symbolic-link or hardlink aliases. The reported staged modes are `0555`
for directories/executables and `0444` for data. The workload needs read/execute
access; source numeric UID/GID preservation is not required. The adapter rcfile
sources no additional startup files. These inventory facts are consumer-reported,
not independently verified measurements in this record.

Protected copies are acceptable. Verify the bytes actually retained against the
supplied expected hashes, then deliver those exact contents at the declared
paths. Computing fresh hashes without checking the supplied expectations could
accept inputs mutated before capture. Once an independent protected capture
exists, changes to its original source need not invalidate it. Enforce explicit
size bounds and reject unsupported sizes. Copying is an implementation candidate,
not a requirement to introduce a general filesystem snapshot service.

Distinguish protected supplied inputs from unprotected application data. Videos
and other application data can remain live; mutation, disappearance and processing
errors are acceptable ordinary runtime failures. `/run/omegaflow` remains writable
for live sockets and FIFOs. Those resources are not protected startup payload.

Protect retained runtime contents against source mutation after verification,
workload writes and mount shadowing through Reploy delivery machinery. File
permissions alone do not establish the boundary. Hostile-host-root containment
and stronger same-identity isolation are outside the clarified requirement.

## Requirements and acceptance

1. Select the workload image through existing public preparation machinery.
   Observe its immutable identity and inherited environment, and evaluate image
   plus application environment before OmegaFlow adds reserved composition
   values. Forbidden inherited values must not disappear from observation.
2. Compose final native blueprints through OmegaFlow's existing Hydra entry and
   prepare with the same selected image. Reject image replacement rather than
   silently changing composed YAML or reusing checks for another image.
3. Inspect the prepared generation's final image/platform, effective environment,
   execution identity, mounts, endpoints, resolved executable path/type/hash and required
   selected system-rc absence. Keep configured private environment values private.
   Define bounded metadata/hash/absence evidence, safe path resolution, and explicit
   absent, symbolic-link, non-regular-file and error behavior. Inspection must
   not start bootstrap or execute untrusted workload code.
4. Bind the observations to one public immutable identity. Accept that expected
   identity at controlled-session admission, using Reploy's existing ownership
   and cleanup. Bind the protected supplied-input capture and relevant image
   facts through admission and use; pinning only the image is insufficient.
5. Prove public inspection and public launch work together. Relevant rebuilds,
   changes to the selected immutable image, bound blueprint/environment/mount facts,
   protected content or selected generation must invalidate stale facts. Moving an
   original tag while the inspected immutable image is retained and launched does
   not by itself invalidate the binding. Source changes after an independent
   verified capture and changes to unprotected application data need not invalidate
   that capture. Each stale case must prove
   bootstrap did not start. Malformed,
   oversized, unknown-version and forged identities must fail closed;
   interruptions must leave no active workload or leaked reservation.
6. Retain the exact release/source identity, public contract and native test
   evidence. Qualify OmegaFlow against an explicitly selected compatible release
   before claiming the consumer is unblocked.

## Current support and gaps

The Reploy source inspection on 2026-10-29 identified reusable internal generation
and build identities in [controlled-session planning](../../internal/dockerdeploy/controlled_session_plan.go),
and executable hash, ownership, permission and symbolic-link observations in the
[probe protocol](../../internal/probe/protocol.go). These do not establish the
complete public inspection/admission contract above. The inspected
[public launch options](../../internal/cli/controlled_session.go) do not accept
the consumer's expected preparation identity. Existing executable metadata/hash
observations do not establish the complete public absence and admission contract.
The current scope does not require raw Bash or system-rc byte export; see the
[consumer clarifications](supporting/omegaflow-rp-c6-requirements/consumer-clarifications.md).

The [controlled-session design](../CONTROLLED_SESSION_DESIGN.md) keeps internal
generation/build/plan digests out of the public session stream. A separate
preparation interface with an opaque public binding is a candidate, not a
selected design. Reuse existing preparation, immutable-image selection, build
verification, inspection and session admission. Do not add a parallel image
selector, inspection daemon, receipt authority or build pipeline.

Reploy owns generic facts, image delivery, protected supplied-input delivery and
admission. OmegaFlow owns its reserved environment policy, Bash compatibility
checks, supplied runtime contents, mount acceptance policy and recording behavior.

## Separate endpoint dependency

The handoff reports that optional host publication for private endpoints is
already known Reploy backlog work. Its exact backlog item is not yet confirmed;
do not create a duplicate. Related endpoint-address and network-policy items are
not an exact match. Existing private controlled-session networking must remain
distinct from the native-blueprint validation gap reported for OmegaFlow's
selected installed release.

The handoff reports this dependency blocks C6.1 acceptance. RP-C6 separately
blocks C6.2/C6.3 and dependent launch acceptance. Resolving one does not prove the
other is resolved.

## Delivery decision and next step

**Minimum delivery: undecided.** Evaluate which acceptance checks are essential
to unblock RP-C6 and recommend the smallest coherent contract from the actual
code. Produce bottom-up production/test estimates, proposed delivery cuts,
acceptance evidence and release/adoption prerequisites. Any deferred consumer
requirements need explicit consumer agreement; do not silently weaken the
launch guarantee to make the delivery smaller.

Resolve image selection through final preparation, protected-input capture,
private-value handling, identity lifetime, cancellation, concurrent replacement,
platform support, bounds and errors during design. Review the resulting design
through the required local deep design review and remote PR cycle before
implementation. Return to design if costs or mechanisms expand.

## Progress and evidence

### Revised preliminary size estimate, 2026-10-29

This is a source-based planning estimate, not a reviewed LLD, measured result or
approved set of slices. Counts are added plus removed lines; tests include
fixtures. Documentation, review bookkeeping, generated output, release execution
and OmegaFlow-side integration are excluded.

| Responsibility | Production LOC | Test/fixture LOC |
| --- | ---: | ---: |
| Bind selected image through preparation; inherited/application/final environment observations | 250–400 | 400–650 |
| Bounded resolved-Bash inspection/export and selected system-rc absence evidence | 300–450 | 600–900 |
| Generic protected supplied-input declaration/capture, expected-hash verification, exact path/mode delivery and mount protection | 350–550 | 700–1150 |
| Expected preparation identity at existing admission; stale-state refusal and capture lifetime/cleanup | 250–400 | 500–850 |
| Public CLI/schema integration and end-to-end inspection/launch qualification | 150–250 | 500–800 |
| **Total** | **1300–2050** | **2700–4350** |

Expect approximately four to six implementation PRs after design, subject to
coherent cuts and the repository's production-size limits. These rows are
responsibilities, not approved task IDs. The superseded estimate was 1550–2500
production and 3200–5200 test LOC. The narrower inventory and protection boundary
reduce estimated work and uncertainty; they do not establish implementation
readiness or a measured saving.

The estimate assumes reuse of the existing controlled-session platform and
ownership model, the consumer's explicit bounded payload inventory and supplied
expected hashes, and reuse of existing artifact storage/materialization where
it meets the required delivery boundary. The public API and delivery mechanism
remain undecided. It does not estimate a general mutable-tree
snapshot service, a new image-selection pipeline, remote-engine support,
multi-identity activation or private-environment injection. Unsupported inputs
must refuse; a read-only bind or permission change alone is not a substitute for
protected delivery. If necessary protection cannot fit this scope, resolve
the design and re-estimate before committing to delivery.

Basis: [current session admission](../../internal/dockerdeploy/current_controlled_session_run.go)
already loads verified current builds under both deployment locks and owns queue
leases; [runtime input handling](../../internal/dockerdeploy/current_runtime_files.go)
validates generated inputs under the operation lock. Those locks do not by
themselves prevent external bind-source mutation. The
[image inspector](../../internal/dockerdeploy/docker_image_inspect.go) already
extracts inherited environment, while the [probe](../../internal/probe/protocol.go)
returns executable metadata/hashes but still needs the complete bounded public
inspection/absence and admission semantics. The historical estimate above included
byte export, which the consumer subsequently removed from the required scope.
The [artifact store](../../internal/providerstore/store.go) already offers
`PublishExpected`, which compares retained bytes against an expected descriptor
before atomic publication. That is a reuse candidate, not proof of complete
runtime delivery, reachability/cleanup integration or mount-shadow resistance.
No new runtime proof or tests ran for this estimate.

Optional host endpoint publication remains a separately identified dependency
with an unconfirmed exact backlog item; its cost is excluded. Delivery of RP-C6
alone therefore does not claim to unblock all OmegaFlow acceptance.

### Delivery status

- Current priority: milestone E delivery. E2.2 and its prerequisite stack were
  merged; remaining E work has been re-planned. OmegaFlow design remains paused
  at detailed engineering, preserving its existing run and approvals.
- Requirements and HLD are reviewed and approved as the basis for LLD. The HLD
  retains namespace-protection and portable transport/watchdog proof gates.
- Evaluation remains linked from [the backlog](../BACKLOG.md). Detailed delivery
  cuts, implementation, release selection and consumer qualification remain
  unapproved. This checkpoint records existing design work only.
- This is separate from milestone E and does not expand its delivery scope.
- The source handoff reports C6.1 preserved and unaccepted, with two unresolved
  ownership findings. Those campaign conditions have not been independently
  revalidated here. It authorizes no campaign recovery or dependency upgrade.
- After prerequisite delivery and release selection, OmegaFlow must use its
  reviewed dependency-adoption and guarded campaign-recovery path.

## High-level design

Approved architecture for LLD engineering and slice planning. Clean phase output: [OmegaFlow RP-C6 HLD](omegaflow-rp-c6-high-level-design.md). Requirements above remain unchanged; the public-host clarification inherits existing Reploy support. Namespace protection and portable transport/watchdog proofs remain readiness gates. The LLD and implementation are not approved.

### Outcome and existing constraints

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

### Actors and authority

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

### Architecture

#### 1. Retain the selected image through ordinary preparation

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

#### 2. Capture supplied bytes and deliver them in the normal immutable image

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

#### 3. Observe final facts with trusted, non-workload code

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

#### 4. Bind observations to existing generation admission

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

#### 5. Preserve the session contract across existing hosts

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

### End-to-end journeys and failure behavior

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

### Alternatives and proportionality

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

### Requirement coverage

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

### Engineering gates before LLD readiness

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

### Supporting material

- [Source investigation and narrow mechanism proof](supporting/omegaflow-rp-c6-high-level-design/source-investigation.md)
- [Existing public-host contract and qualification evidence](supporting/omegaflow-rp-c6-high-level-design/platform-qualification.md)
- [Observed local versions and filesystem paths](supporting/omegaflow-rp-c6-high-level-design/local-proof-environment.json)
- [Maintainer platform clarification](supporting/omegaflow-rp-c6-high-level-design/platform-contract-clarification.json)
- [Preserved requirements-phase workflow document](supporting/omegaflow-rp-c6-high-level-design/requirements-workflow-preimage.txt)
- [Document and estimate contract](supporting/omegaflow-rp-c6-requirements/design-contract.md)
