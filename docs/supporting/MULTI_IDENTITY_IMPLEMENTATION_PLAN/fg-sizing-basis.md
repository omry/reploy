# F/G sizing basis

This supports the canonical inventory in
[the implementation plan](../../MULTI_IDENTITY_IMPLEMENTATION_PLAN.md#fg-engineering-and-slice-inventory--draft).
All values are conditional forecasts, not measured implementation, budgets,
working-hour estimates or guarantees that a mechanism will fit. The accepted
production target is 500 lines per implementation PR and the hard limit is 750.

## Unit and current source

Count added or rewritten handwritten physical LF lines, including comments,
blank lines, helpers and shipped configuration. Count moved code at destination;
report deletions and mechanical moves separately. Tests/fixtures have their own
column. Exclude prose, generated files, dependencies and raw observations.
This matches the governing plan's convention. Delivered C/E gross additions
plus removals are a different metric and must not be compared directly.

The inspection used source head
`87899abecced4c5f8b3513ecee66793ecdd2df37`. Selected current implementation
footprints, including comments and blanks, were:

| Inspected seams | Existing physical lines | Interpretation |
| --- | ---: | --- |
| Installed runner, wait and engine-readiness functions | 403 | Existing lifecycle core to adapt; not 403 new lines. |
| Systemd/host-command/platform/scope adapters | 627 | Linux system integration exists; native user adapters are missing. |
| Direct PTY, Unix/named-pipe dialers and endpoint pinning | 451 | Reuse instead of acquiring a full SDK or another transport family. |
| Deployment/process state, leases and private input handoff | 1,282 | Reuse state and validation; reconcile activation intent, enablement and process receipts. |
| Mapped proof/entry/wait/exec and nftables integration | 1,383 | Private interfaces and refusal paths are not production F observations. |

The complete file list and counts are retained in
`temp/reviews/fg-controller-evaluation-20261101/source-footprint.json`. These
groups are source-reading references, not a sum of planned changes.

## Production/configuration allocations

Each row decomposes its range into concrete incremental responsibilities.
Existing helpers remain shared and are not charged as full new modules. A zero
production qualification row carries test/fixture/evidence work rather than
invented product code. The canonical slice descriptions carry its prerequisites
and observable completion check.

| Slice | Incremental allocations, low–high physical lines | Sum |
| --- | --- | ---: |
| F1.1.1 | Syntax/model 80–130; metadata/canonical resolution 80–120; intrinsic-root rendering 120–180 | 280–430 |
| F1.1.2 | Layer/index projection 140–220; closed evidence/validation 100–160; build-lock/publication/reuse integration 110–170 | 350–550 |
| F1.2 | Descriptor primitives 120–200; path/alias validation 100–150; create/verify 80–130 | 300–480 |
| F1.3 | Setup protocol 80–130; identity binding 80–120; cancellation/cleanup 80–150 | 240–400 |
| F2.1 | Resource state 90–140; writers/pending effects 120–200; freshness 70–120 | 280–460 |
| F2.2 | Owner fence 80–130; retirement observations 100–170; cleanup recording 60–120 | 240–420 |
| F3.1.1 | Setup phase 70–130; frozen plan/call ordering 70–120; bounded failures 40–80 | 180–330 |
| F3.1.2 | Image/mount observation 120–200; effective kernel evidence 120–200; exact process/proof binding 60–100 | 300–500 |
| F3.2 | Network topology 80–130; peer/capability constraints 100–170; resolver/interface rules 80–140 | 260–440 |
| F4.1 | Typed publication 100–170; effective observation 100–160; exact cleanup 60–100 | 260–430 |
| G1.1 | Schema/phase mapping 120–190; retained-state guards 80–130; typed override phase/consumer wiring 80–130 | 280–450 |
| G1.2 | Dispatch 60–100; argument validation 50–90; authority/errors 90–150 | 200–340 |
| G1.3 | Destination transaction 120–200; independent immutable reuse 100–160; failure recovery 100–160 | 320–520 |
| G1.4 | Policy syntax/canonical form 100–160; activation/control facts 100–160; leases/retry accounting 100–160 | 300–480 |
| G1.5.1 | Managed channel/typed binding 110–190; native tail/liveness 70–120; pinned post-exec observation 90–150; lifecycle/failure cleanup 80–140 | 350–600 |
| G1.5.2 | Controller loop 120–200; exact reattachment 120–200; retry handling 60–100 | 300–500 |
| G1.6 | API wait 80–140; decode/cancel 40–80; lifecycle wiring 40–70 | 160–290 |
| G1.7 | Non-TTY framing 120–200; stdin/half-close 80–140; entry wiring 60–100 | 260–440 |
| G1.8 | Input parsing/startup 80–140; final transition 80–140; host binding 70–130 | 230–410 |
| G1.9 | User unit 80–140; controls 60–100; scope validation 40–80 | 180–320 |
| G1.10 | User agent definition 80–150; controls 80–140; binding 60–110 | 220–400 |
| G1.11 | Task/principal definition 100–180; controls 90–160; ownership/removal 60–100 | 250–440 |
| G1.12 | Control/enablement dispatch 100–170; manager transaction 100–170; hooks/completion 100–160 | 300–500 |
| G1.13 | Actual entry classification 100–170; adapter wiring 80–140; output/status 80–130 | 260–440 |
| G2.1 | Revoke admission 80–140; owner fences 80–140; observed retirement 100–160 | 260–440 |
| G3.1 | Recovery cases 80–140; resource cleanup 100–160; bounded diagnostics 80–140 | 260–440 |

F1.0, F3.0, F4.2, G1.0, G1.14 and G3.2 are zero-production proof/qualification
candidates. They may reuse valid evidence. A failed mechanism proof invalidates
dependent readiness and may require a new engineering estimate; it is not a
reason to pad production or remove a required check.

## Tests, integration and failure work

Each canonical row has a separate test/fixture range. These costs cover different
work rather than mirroring implementation:

- F1 owns shared filesystem attack fixtures and subordinate-owner checks.
  Include path replacement, link/mount/type aliases, stale authority, foreign
  objects, contextual defaults and failures without protected mutation.
- F2 owns attachment/preparation concurrency and interruption matrices, including
  success-shaped engine errors and uncertain manager cleanup. Reuse existing
  operation-lock and resource fixture builders.
- F3/F4 own real kernel/network/publication integration and explicit transport
  proof. Ordinary nftables tests do not replace eligible rootless positive and
  negative journeys. F4.2 owns the combined parent journey, not a full duplicate
  of every child assertion.
- G1.4 owns the shared controller state/lease fixture. G1.5.1–G1.8 add actual startup evidence, policy,
  surviving-process, stream, private input and final-transition fault cases using
  that fixture; they do not each recreate it.
- G1.9–G1.11 price native adapter tests independently. G1.14 owns the cross-platform
  lifecycle/session qualification, including versions/locations and complete
  process-tree resource observations. A cross-compiled binary or WSL pass is
  insufficient native evidence.
- G2/G3 own active revocation and interruption across actual assembled effects.
  G3.2 owns the complete public exact/bounded acceptance matrix and exact-head
  CI/review closeout. Missing native or engine access remains visible, not a pass.

Docs, Changie prose and review bookkeeping are separate work excluded from both
line columns. Necessary fixture scripts count as tests. Observed timing and
tokens are reported only when actually available; no working-hour forecast is
included.

## Interpretation of the change

The original logical-parent F/G ranges were 2,400–4,200 production and
4,300–7,050 tests. The current controller draft is 6,820–11,450 production and
17,550–29,050 tests across 26 implementation and six qualification candidates.
These are candidate boundaries, not a promise of 32 PRs.

The increase primarily exposes missing ownership, effective sandbox and native
integration work. The new Linux-user/macOS-user/Windows-user adapters alone
account for 650–1,160 production and 1,350–2,350 test lines. Controller state,
loop, wait/stream and mapped private-input integration account for
1,600–2,720 production and 3,450–5,700 test lines, with shared work charged once.
These are component totals, not arithmetic claims that every line is added
scope relative to the original overlapping parents.

The intermediate engine-retry candidate required retained approval/private
snapshots instead. It remains historical alternative analysis and used a
different rule for unchanged moves. Do not publish a precise normalized saving
against it. The lean-controller direction has demonstrated a smaller isolated
wait client, not yet a smaller complete cross-platform implementation.

## Image-projection refinement

F1.1.2 prices a missing producer separately from mount declarations. The
disposable metadata prototype reuses D's archive/immutable binding; its source
and result are in [mechanism evidence](mechanism-evidence/manifest.json).
Production must add aggregate allocation limits, typed canonical evidence and
build/publication/reuse integration. The 350–550 production and 700–1,100 test
range covers these incremental obligations, not another complete image reader.
F3.1.2 consumes that result and owns actual host observations, so the producer
is not counted there again. Its existing range remains conditional on the
eligible-engine proof; review can require a split before implementation.

## Startup-completion producer refinement

G1.5 was relying on E's private `StartupComplete` interface as though its real
producer existed. G1.5.1 now owns that missing producer and qualification before
G1.5.2 consumes it. The 350–600 production range prices managed channel/binding,
the raw-tail/liveness refinement, actual process observation and recovery
integration; 800–1,300 tests price both-profile/native/exit-race and private-fd
controls. The four-case process proof is reusable feasibility evidence, not
full engine/credential qualification. A failed required proof blocks the provider
and dependent loop; it cannot be closed by a completion boolean or weaker up.
