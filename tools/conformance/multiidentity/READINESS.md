# A1 readiness disposition

## Current standing — 2026-10-25

A1 and B1–B5 are landed. B supplies explicit blueprint/override mapping input,
host preflight, geometry and durable authority/recovery. It does not enable
mapped payload execution. Existing declaration-or-retained-state guards stay.

C-design is proposed in the implementation plan. C1 adds scoped engine admission
and a bounded compatible create seam; C2 reuses existing build/image authority.
Both are internal foundations. The public launch, storage and service gates
remain later work. No native backend or composition API is added.
The C proposal distinguishes active geometry from retained dormant reservations
and read-only preparation from dispatch under B's exact `effects-pending`
operation. It reuses the pinned Podman info read for parent metadata. These are
design requirements, not implemented C admission or a new lifecycle coordinator.

The C draft was reconstructed after its temporary checkout disappeared. Its
original raw feasibility receipts are unavailable. A fresh narrow projection
proof is recorded below; source-bound C-design review remains required. Historical
failed projection records remain failures; the new projection does not erase them.
This current standing does not claim a new full mechanism or operation-audit run.

### Fresh compatible projection proof — 2026-10-25

A private rootless Podman 4.9.3 / crun 1.14.1 endpoint accepted the proposed
`auto:size=1` plus explicit UID/GID entries through API 1.44. Two exact trials
and two bounded trials, including removal/recreation, matched the complete live
kernel maps before a trusted waiter released the capability-free fixture.
The bounded map was container `0:4096` to host `100000:4096` for both ID kinds.
The exact UID map was `(0,100000,1), (100,100001,1), (1001,100002,1)`;
its GID map was `(0,100000,1), (5,100001,1), (100,100002,1),
(2001,100003,1), (3001,100004,2)`. All four payload reports passed.
The run took 61.380 seconds. Its private container/image/volume/network
inventories were restored and its API service stopped.

Raw receipts and frozen fixture sources are retained locally under
`temp/reviews/c-milestone-20261025/proof-03/` in the author's worktree.
They are untracked, gitignored and not part of this PR. The local
`receipt-manifest.json` SHA-256 is `fc01b459c0d4e4357f8bbdbd06a05de5008d7aa114e5308508dce437a9870656`.
The driver is also local: `temp/reviews/c-milestone-20261025/mapping_proof.py`.
These paths and hashes record local provenance; they do not provide a published
receipt bundle or a reproducible artifact available from a fresh clone.
The first attempt stopped at the files-only account-source check before building.
The second stopped at image-ID representation comparison and cleaned its objects;
compatible inspect prefixes the same immutable digest with `sha256:`. The third
compared canonical digest identities and passed. Failed attempts remain retained.

This host uses `files systemd` principal lookup. The private fixture used observed
inventories and bounded numeric collision checks; it did not satisfy or bypass
B's production files-only host admission. The fixture mounted no host data.
Wrong-host and surplus-map comparator controls are private-harness checks, not
production rejection tests. This is not a full conformance rerun, Podman 6.1.4
qualification or permission for public mapped execution. C1 must still prove
its own fresh host admission and negative controls through the implemented seam.

## Historical standing — 2026-10-22

The source baseline for this design review is `main` at
`4f1debfa61585ce2a1194ed8eb8047979aecc5a2`, including HF3–HF12 and intervening
main changes. The maintainer-authorized rebase completed on 2026-10-22 and
preserves the slice patches and commit descriptions. Native stack #238 and its
owning PR state records hold the current published heads and approvals. Earlier
2026-10-19 heads and equivalence observations are historical; neither those
observations nor an approval label alone replace current exact-head review
and all eight required CI jobs. That rebase did not rerun the mechanism trials. The separate approved
supervisor-thread repair below has fresh mechanism and operation-audit evidence.

The private mechanism checkpoint below passed. The merged hotfixes address
the reported generated-build, healthcheck and application-sandbox gaps, as
described below. Flux reports that Podman looks OK and is continuing its
evaluation. That is preliminary operator feedback, not final qualification
or multi-identity acceptance.

On 2026-10-19, the maintainer accepted separating general composition from
multi-identity. The active design adds operator-supplied mapping input and durable
state/recovery to one existing workload installation. Mapping ownership belongs
to each explicitly configured staged test deployment or installed destination.
Both retain their own reservations. G1 must qualify real staged run/test and
installation with explicit independent destination mappings; it cannot copy
source mapping authority or mapped storage ownership. Source mappings remain
reserved, and destination identity binds before effects and survives retry/update.
Stage updates and uninstall that retains the directory preserve the mapping owner.
Installed facts and the active service binding clear atomically with restoration
of the existing staging marker; existing staged readers must work unchanged.
Unknown uninstall effects
remain inhibited until exact recovery. Copied records reject. Cleanup retains
reservations until owned resources and mapped data are proved absent. Service lists, aggregate
staging and multi-service orchestration are separate future work. Manual host-ID
coordination and existing OS-user delegation remain; system-wide Reploy enrollment
and automatic global allocation remain deferred. The narrower concrete public
mapping contract still requires B-design review and maintainer approval.
The maintainer subsequently accepted mappings in the blueprint, deployment
overrides and `???` for required values. The proposed first syntax places them
under `environment.runtime.identity_mapping`, with a typed sparse override in
the existing sidecar shape. It adds no mandatory separate mapping file, global
configuration or general configuration merge system. Required mapping leaves
may remain unresolved through portable builds but must resolve before mapping
configuration or mapping-dependent effects. Original blueprint/build evidence
remains intact; deployment overrides cannot replace retained accepted authority.
An unresolved declaration still requests multi-identity and cannot fall back to
ordinary execution. B2 is split into B2.1 (declaration/required values plus the
common immediate execution guard) and B2.2 (operation-specific overrides) to
keep production changes below the per-PR limit. B2.1 must reject every existing
payload/install entry for complete and unresolved declarations before effects;
that includes `RunCurrentShellV1`, shell wait and the independent
`RunInstalledServiceContainerV1` start/restart route. Public parsing cannot
precede that protection. B4 extends the same guard to
retained state even after declaration removal. B5 retests these inherited guards,
rather than introducing them after earlier slices have exposed opt-in input.
These are design requirements, not implemented support or a closed evidence gate.
The proposed initial subordinate-delegation backend is enumerable `files`,
checked against active `getsubids`/libsubid results; other subordinate backends
are rejected. This recommendation is part of the pending B-design decision.
The proposed initial runtime cohort is non-root staging and user-scope installation
under the same positively verified rootless engine account. System-scope mapped
install rejects; the existing root-run system unit is not that account. Caller,
engine and restart-account mismatch must reject before effects. System-scope
multi-identity needs separate design/qualification; ordinary behavior is unchanged.
Principal collision checks require complete authoritative bulk inventory and
interval comparisons, without per-ID expansion or query fallback. A host whose
configured identity sources cannot establish that completeness is rejected.
Every execution admission must refresh that inventory and rerun collision checks;
staging or prior-start evidence cannot substitute. Ownership-changing
preparation and restore also require fresh complete authority before writes.
An unused pending candidate can be corrected or cancelled only with proved
no-effect intent and no ownership use; established or uncertain reservations stay.
Assignment geometry is checked in B3; identity completeness waits for D4/E1's
resolved identity evidence. E1 rejects surplus new exact assignments; only
previously execution-admitted assignments can have retained dormant authority.
B metadata configuration cannot launch workloads. ID-01 now requires bulk collision/admission
evidence; the private trials below retain their earlier per-ID-check scope.
Production mapping,
supervisor admission, network enforcement and stock-mail acceptance remain
later work. No product evidence gate is closed by this direction decision.

## Private mechanism evidence

The refreshed mechanism checkpoint passed on 2026-10-24.
Three fresh exact and three fresh bounded trials passed in 600.888 seconds, including setup and cleanup.
Each observation inspected every supervisor thread and proved child-to-thread
`tgkill` signal-0 and `SIGCONT` denial with successful same-thread controls.
Complete thread inventories agreed before and after each application probe.
Missing or changing evidence rejects; leader-only inspection is insufficient.
All four roles passed admission before payload execution.
Docker was rejected before mutation, and attempt-owned cleanup restored the engine inventory.
The probe source SHA-256 is
`0a9afbcbde0e645018cd1b0597d2589e63eab69e9e496f6801bbbf6e8d2150f9`.
The effective UID resolved canonically to `omry`.
Hostile login-name and real/effective UID mismatch regressions fail closed.
The baseline repairs belong to [1B / PR 69](https://github.com/omry/reploy/pull/69).
1B owns atomic release publication and all three mapped-root rejection cases.
A1 adds the audit failure/restart repairs.
Restart compatibility requires unchanged mappings and fresh admission; failures
block dependent payload release and remain explicit audit results.
Failed bounded setup or restart records dependent lifecycle seams as blocked;
a verified restart remains compatible if later exec, wait or logs fail.
If mapping cleanup is unproved, later mapping candidates stay blocked while
independent network evidence continues and final cleanup retries owned resources.
It owns the audit, readiness and plan follow-up.
Its delivery review and exact-head checks remain a separate gate in
[PR 239](https://github.com/omry/reploy/pull/239).

[Current evidence](EVIDENCE.md) records the durable raw receipt directory,
source/receipt hashes, exact test-only runtime configuration and failure history.
The before/after source snapshots bind the repair.
The six recorded trials and recorded audit use the same frozen probe bytes.
The refreshed audit command took 61.183 seconds; its internal timer, including preflight,
reported 60.938 seconds. It recorded nine compatible observations and five explicit
blockers, restored all inventories and stopped its owned API service. Failure regressions preserve the original error and
durable receipts when cleanup or inventory fails. Unknown inventory fails closed.
The mechanism qualification reproduces and corrects two private probe defects:
filesystem-ID syscalls now inspect the same locked calling thread, and the
set-ID and file-capability fixtures retain separate strict metadata checks.
A disposable Docker control exercised successful file-capability execution
without privilege; the native Podman trials exercised execution denial.
The current audit uses the same corrected source. Its first attempt rejected
an overlong Unix socket path, restored the engine inventory and stopped its
owned service. A retry with a shorter private path completed.
Exact audit sources and both attempts are retained at
`~/.local/state/awd/reploy-mi-recovery/20261024-a1-readiness-audit/`.
Its `receipt-manifest.json` SHA-256 is
`e4be00e72d7c9bb1c0d04e2cd23313ce39a689ec0c5532d3297faf5daffcd80b`.
Earlier source pairings remain historical and do not establish current conformance.
The earlier 2026-10-12 live failed-RUN regression passed in 6.914 seconds and restored both image
and external build-container inventories.

## Existing-backend operation audit

The observations below are historical. The product dispositions use the current
slice IDs from the implementation plan. Its
[crosswalk](../../../docs/MULTI_IDENTITY_IMPLEMENTATION_PLAN.md#previous-to-current-slice-crosswalk)
maps older evidence and milestone IDs to those current slices; do not translate
the disposition IDs a second time.

`operation_audit.py` completed on 2026-10-14 against an attempt-owned Podman 4.9.3
compatibility socket, Docker API 1.41. It restored container, image, volume and
network inventories, removed its labeled resources and stopped its own service.
Native Podman built only the offline scratch fixture and supplied independent
observations. No native fallback converted a compatible-API failure to success.
Zero exit establishes audit completion and cleanup, not universal compatibility.
These observations describe that recorded host and source. The rebase did not
rerun the audit; merged fixes below do not change its historical results.

| Reploy operation seam | Recorded Podman 4.9.3 observation | Current product disposition |
| --- | --- | --- |
| Engine/endpoint (`docker_endpoint.go`) | Positive Podman engine identity through the scoped API | Reuse endpoint pinning; add explicit eligible-engine admission in C1. |
| Create/map (`config.go`, controlled-session create planning) | Direct compatible create accepted `auto:size=4096`; Docker CLI cannot express that userns syntax | A narrow compatible create request is required; no native backend is implied. |
| Fixed bounded mapping | `auto:size=4096,uidmapping=0:1:4096,gidmapping=0:1:4096` produced exactly host `100000:4096` for both ID kinds | One host-specific reservation request passed. Concurrent durable reservations, removal/recreation, recovery and revocation remain unproved and owned by B/C. |
| Sparse mapping | The candidate `HostConfig.IDMappings` plus `UsernsMode=private` was rejected with HTTP 500: no UID/GID mapping supplied | This candidate projection failed; it does not prove every compatible projection impossible. C1 must establish an exact-profile request before activation. |
| Image/reference (`docker_image_inspect.go`) | Compatible image inspect accepted the immutable local fixture ID | Reuse is plausible for this local fixture. Registry manifest/content-digest and generated-image publication contracts are not established. |
| Build (`buildkit_probe.go`, `materialization_build.go`) | The actual exported `ProbeBuildKitCapabilities` rejected Podman 4.9.3 because it requires Docker Engine >=24 | HF5 now selects and checks the builder by engine. This audit result does not test that path. C2 must assess the merged implementation against its multi-identity requirements. |
| Inspect/exec/wait/logs (`command_execution.go`) | Compatible lifecycle commands worked; the admitted capability-free control exited successfully | Reuse these transport seams subject to owning production tests. |
| Volume (`provider_install_path_updates.go`) | Compatible create/inspect and exact labeled removal worked | CRUD reuse is plausible; durable reservations belong to B, preparation to F1/F2 and ownership lifecycle/retention to H. |
| Explicit restart (`config.go`) | Compatible restart preserved the observed UID/GID maps | Transport evidence only. Supervisor launch admission belongs to E; service replacement, Compose/restart ownership and revocation fencing remain G/service-design work. |
| Network/endpoint (`controlled_session_network.go`) | Internal network create/inspect/remove worked | Reploy endpoint membership, nftables enforcement, egress denial and cleanup parity remain unproved; F3/F4 and network-design must resolve them. |
| Cleanup (`config.go`, recovery paths) | Compatible removal of exact labeled test objects restored the recorded inventories | Production crash/recovery cleanup belongs to G3, durable recovery to B5 and reservation retirement to H4. |

The audit calls `ProbeBuildKitCapabilities`, which rejects this Podman 4.9.3
engine. The capability observation and the separate builder-safety blocker remain
independent: a successful probe is never described as a rejection. Production
generated-build paths currently do not call that probe.
The recorded audit proves neither a successful production build nor permission
to launch a shared privileged BuildKit builder. HF5 supplies a separate
engine-selected build path; this audit does not validate it. Each deterministic
create name is recorded before the request; ambiguous responses are reconciled
by exact name and attempt label. Removal verifies absence, preserves foreign
collisions and distinguishes HTTP 500 errors from a true HTTP 404.

The recorded audit did not resolve the former C1–C7 implementation estimates.
The revised plan now gives conditional ranges to their current owning slices
and separates transport, declaration, launch and service integration. Those
ranges do not prove a supported request, supervisor boundary or policy. The
500-production-LOC target and 750 hard limit apply to each implementation PR;
an unresolved mechanism must be proved and re-estimated before implementation.
Full native Podman support remains a separate feature. HF5's scoped compatible
build path is already merged; it does not establish general native support.

## Operator report and merged Podman fixes

The original Reploy dev3 operator report on Debian 11 established Docker-compatible
execution, Compose and registry-based BuildKit building with result loading.
Reploy generated layers failed because the containerized builder could not
resolve its daemon-local base tags. Later diagnostic adapters enabled build,
verify and execution, but did not establish unmodified Reploy acceptance.
The Podman 4.9.3 audit rejection is a separate observation.

The following fixes are merged into the stack's stated main baseline:

| Fix | Result | Remaining qualification |
| --- | --- | --- |
| HF3 [#241](https://github.com/omry/reploy/pull/241), HF4 [#242](https://github.com/omry/reploy/pull/242) | Image cleanup accepts repeated identical IDs while rejecting distinct or incorrect IDs; covers temporary references, environment publication and legacy-generation cleanup. | Unexplained conmon monitors remain a separate investigation; image-ID fixes do not establish their cause. |
| HF5 [#243](https://github.com/omry/reploy/pull/243) | Selects BuildKit for Docker and the compatible build API for identified local Linux Podman. Checks local-image access, offline RUN, read-only mounts and result loading. No user-set `DOCKER_BUILDKIT` is required. | C2 must reuse and assess this implementation against its requirements. The old shared-builder local-image failure is no longer an unimplemented fix. |
| HF6 [#244](https://github.com/omry/reploy/pull/244) | Recovers omitted healthcheck metadata from an image export through the selected endpoint and verifies the config against the immutable image ID. Missing metadata is never interpreted as disabled. | The full archive has server-side disk and client-side time costs; see the [product notes](../../../README.md). |
| HF7 [#245](https://github.com/omry/reploy/pull/245) | Prepares new Podman containers before startup through the selected engine's native update API, preventing disabled healthchecks from creating timers. Includes Compose replacements. | Final Flux lifecycle evaluation is pending. This does not diagnose every reported conmon monitor. |
| HF8 [#246](https://github.com/omry/reploy/pull/246) | Supplies pinned Docker seccomp data on eligible Podman engines and prepares private home tmpfs ownership in the trusted helper. All setup capabilities are dropped before application execution. | Requires upstream inline-profile support and rebuilt runtime images; details below. |
| HF9 [#247](https://github.com/omry/reploy/pull/247) | Preserves effective base overrides through build finalization and validation. | Reuse the merged implementation; mapped-workload integration remains to be qualified. |
| HF10 [#248](https://github.com/omry/reploy/pull/248) | Binds Podman base pulls to returned image IDs and authoritative image metadata. | This establishes ordinary image-reference handling, not multi-identity admission. |
| HF11 [#249](https://github.com/omry/reploy/pull/249) | Preserves ordinary operator identity for rootless Podman managed storage. | Private subordinate mappings are separate; network-enabled Podman controlled sessions remain deferred. |
| HF12 [#267](https://github.com/omry/reploy/pull/267) | Restores pinned Playwright ffmpeg acquisition through an official mirror in definition revision 2. | Consumers must select the new definition revision; this adds no multi-identity qualification. |

HF8 still requires an engine containing upstream
[#29726](https://github.com/podman-container-tools/podman/pull/29726).
Stock Podman 6.1.4 cannot transport the inline seccomp profile. The documented
fixture uses upstream commit `5866b09c1f064775ab5e049c120ffe51c1cf1623`;
no minimum released version has been established. Runtime images must be
rebuilt to include the updated helper.

The tmpfs ownership workaround addresses Podman's rejected `uid`/`gid` mount
options, tracked in [#29979](https://github.com/podman-container-tools/podman/issues/29979).
It preserves the fixed mount's size, mode and restrictions, validates the mount,
and changes only its root ownership before dropping setup authority.
The [policy and workaround notes](../../../internal/dockerdeploy/seccomp/README.md)
define engine scope and removal conditions. Docker behavior is unchanged.

Flux's preliminary report does not identify a complete test matrix or final
acceptance receipt. Retain its final engine/build identities and results when
available. The 2026-10-22 baseline update did not rerun the private mechanism trials.
Fresh 2026-10-24 mechanism and audit evidence is recorded above for its exact
source and host. No matching-server qualification or multi-identity product
acceptance is claimed.

HF9–HF12 are present in the source baseline above. Assess reuse against those
merged implementations. HF11's ordinary same-primary-ID policy does not implement
private subordinate multi-identity mappings. The historical private trials and
audit remain evidence for their recorded source and host; this baseline update
does not create fresh mechanism or product qualification.

## Stock mail disposition: blocked

This is a source-grounded compatibility blocker, not a stock-binary runtime pass
or a claim that adaptation is impossible. No locked stock-mail image/config and
approved stock-master supervisor role have been selected for the frozen model.
A1 does not choose those policies or approve application identity-changing caps.

Postfix's documented privileged master launches services that drop authority
and includes delivery helpers that retain authority for selecting target users.
Its unprivileged-master alternative weakens privilege separation and cannot
satisfy the multi-identity/local-delivery goal by merely using one UID.
See the primary [Postfix process configuration](https://www.postfix.org/master.5.html)
and [unprivileged-master analysis](https://www.postfix.org/UNPRIVILEGED_README.html).

Dovecot documents a privileged master launching service processes and user
mail processes; its rootless configuration uses a single system user.
See the primary [Dovecot process design](https://doc.dovecot.org/2.4.0/developers/design/processes.html)
and [rootless configuration](https://doc.dovecot.org/2.4.0/core/admin/guides/rootless.html).

Against the frozen contract, a stock master cannot silently become an
application child retaining identity-changing authority, nor can it silently
become the accepted supervisor. Current launch-design/mail-compatible must select and verify
an image-locked supervisor role, minimal capability set, dedicated credentials,
non-dumpability, separate child sessions and descriptor closure. Otherwise
K2/K3 (formerly I2/I3) remain blocked. No custom supervisor implementation is selected here.

## Historical next-work disposition — 2026-10-22

HF1 and HF2 are merged and published in 0.8.0.dev3. HF3–HF12 are included
in the stated source baseline. Earlier four-PR publication, CI and deferred
approval observations are historical. Read current heads, checks and approvals
from native stack #238 and the owning PR state records. Preserve the recorded
mechanism evidence without describing it as a new run on this baseline.

B-design follows the [approved scope boundary](../../../docs/MULTI_IDENTITY_IMPLEMENTATION_PLAN.md#approved-scope-boundary--2026-10-19).
Review the proposed single-installation mapping/state design and obtain approval
before B1–B5. B6/B7 from the superseded composition proposal are deferred, not
renumbered implementation work. The existing deployment lock and state remain
the authority; there is no composition generation or service-list loader.
The design checkpoint is [PR #250](https://github.com/omry/reploy/pull/250).
Its earlier independent review proceeded under an explicit ancestor-recovery
deferral. That historical deferral does not describe current approval status.
This is not formal stack approval. Public mapping syntax and concrete files-backed
host policy remain proposed. Both profiles are explicit; no default is inferred.
Supervisor authority, full native support and cross-installation interface
participation retain separate later gates. Do not resume the superseded
enrollment/global-allocator or composition-delivery proposals.
K1 (formerly I1) remains an independent optional readiness slice; it is not added implicitly.
