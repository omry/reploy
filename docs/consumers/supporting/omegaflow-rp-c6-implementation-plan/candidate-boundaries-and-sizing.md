# Engineering sizing assessment before the LLD

This is a source-based candidate responsibility map, not a canonical slice
inventory, reviewed estimate or implementation authorization. Actual IDs and
coherent cuts belong to the LLD authoring stage. Open readiness proofs can change
these ranges; a material mechanism or guarantee change returns to architecture
review. No production code has been written for these responsibilities.

The historical 1300–2050 production / 2700–4350 test estimate is withdrawn as a
current forecast. It omitted real portable-channel/watchdog prerequisites and
did not settle image namespace protection. It remains historical evidence in the
approved preimage; the current consumer record must replace its table when the
full LLD is authored and reviewed. This assessment is not that final replacement.

## Cost and compatibility audit disposition

The [source/contract audit](scope-and-multi-identity-compatibility.md) found
substantial existing broker/protocol, protected-path, build/store and cleanup
behavior to reuse. The rows below are responsibility envelopes, not evidence
that each subsystem must be implemented again. In particular, the transport row
needs a container-private peer relay and host integration, not another public
controller broker. Existing namespace checks need complete evidence/alias
extensions, not an independent OmegaFlow policy compiler.

The ranges and arithmetic below remain the pre-audit candidate assessment. They
are retained for traceability and are **not an accepted delivery forecast**.
No lower replacement total is justified until missing interfaces/proofs yield
concrete implementation deltas. Shared E/F foundations will have one owner in
the canonical LLD; backend adapters and qualification remain separate.

## Counting convention

Changed physical text lines means additions plus removals against the accepted
parent, including comments and blank lines. Production includes shipped runtime,
build and packaging configuration. Test code includes test helpers, test-only
configuration and textual fixtures. A line is counted once. Generated/vendor/
lockfile changes, binaries, Markdown, review bookkeeping, release execution and
OmegaFlow-side changes are excluded. Native test access and evidence collection
are material non-LOC prerequisites, not free proof inferred from these figures.
No working-hour estimate is provided.

## Candidate responsibility ranges

Shared foundations have one owner here. Dependent rows reuse those foundations.
Ranges reflect the inspected current modules and the unproven work below, not
measurements or a fixed PR count. The 450–650 broker row exceeds the 500 target
at its upper bound but stays below the 750 hard maximum; refine its coherent
cut after the transport proof, rather than claim a 500-line cap was met.

| Responsibility | Production/configuration LOC | Test-code LOC | Concrete change basis |
| --- | ---: | ---: | --- |
| Namespace path resolution | 300–500 | 650–1000 | Component-aware path/absence and ELF metadata observations in internal/probe; retain current inspection protocol compatibility. |
| Namespace mount protection | 300–500 | 700–1100 | Derived image execution/configuration roots; alias/ancestor/automatic-volume checks in dockerdeploy, not just lexical Bash overlap. |
| Container-private broker | 450–650 | 800–1300 | Packaged Linux helper peer credentials, one claim and bounded frame relay; reuse protocol validation. |
| Host portable channel integration | 300–500 | 700–1100 | Pinned local-engine attached stream and lifecycle integration; replace host Unix channel after parity proof, not a second default route. |
| macOS watchdog adapter | 80–160 | 250–450 | Reuse Unix inherited descriptor model and existing shared exact-manifest cleanup; native survival and receipt tests. |
| Windows watchdog adapter | 300–500 | 700–1100 | Explicit inherited private handles, detached child survival, readiness/disarm/EOF and incident target; reuse shared cleanup and boot identity. |
| Native safe supplied-file opening | 200–350 | 550–850 | Reuse providerstore expected-byte publication; descriptor-safe regular-file opening, alias refusal and source independence across native filesystems. |
| Selected image and environment provenance | 300–450 | 450–700 | Public selection retained into current ordinary provider build; separate inherited/application/final environment; no tag fallback. |
| Protected input contract and capture | 350–500 | 650–1000 | Bounded complete inventory and independent manifest expectation; validation, cancellation and generation-owned capture publication. |
| Normal image payload delivery | 350–500 | 650–1000 | Deterministic archive through current normal build, fixed modes, trusted collision check and exact final inventory verification. |
| Bounded public observations | 300–500 | 700–1100 | No-healthcheck trusted create profile, bounded requested facts, types and absence; reuse namespace resolver rather than duplicate. |
| Retained comparison record | 250–400 | 500–800 | Versioned opaque public identifier, private projection under existing generation ownership, record bounds and replay/deletion behavior. |
| Locked admission binding | 350–500 | 700–1100 | Expected identity on existing session entry; compare again after queue waits, pin through use, no caller-report trust or alias fallback. |
| Reachability and recovery | 250–400 | 500–850 | Capture/record/image generation reachability, cancellation and exact-owned cleanup/incident continuation. |
| Public interface composition | 250–400 | 450–750 | Existing native blueprint and structured CLI/JSON integration; user docs and Changie fragment excluded from LOC. |
| Integrated acceptance and adoption evidence | 150–300 | 1000–1600 | Full public path and refusal/cleanup matrix on inherited supported hosts plus WSL2 Desktop; three Linux/amd64 consumer profiles. |
| **Candidate total** | **4480–7110** | **9950–15800** | Sum of the above lower/upper bounds. |

The first seven shared prerequisites subtotal **1930–3160 production/configuration LOC** and **4350–6900 test-code LOC**. Namespace enforcement, transport and native watchdogs are
substantive production work. They cannot be hidden in a test-only final slice.

## Inspected reuse and uncertainty

At current source, `controlledsession/channel.go` has 294 lines and its Linux
transport 179. The existing engine attach adapter has 219. Reuse bounded frame
validation and local-engine endpoint dialers; an engine non-TTY exec stream still
needs its own multiplexing, claim and cancellation handling, not a copied PTY
assumption. The broker and host rows include removal/adjustment of the old route.
The shared watchdog has 561 lines, its Linux launch adapter 231; retain the
algorithm instead of implementing cleanup separately per host. Darwin can reuse
Unix descriptor semantics; native proof is still required. Windows Go exposes
explicit inherited handles, but inherited process-job behavior and parent-loss
receipt ownership remain unproven.

Current probe inspection has 230 lines but only resolves final symlinks. It is
not a complete safe ancestor resolver or namespace derivation module. The normal
payload image helper has 964 lines and staging 593; reuse the build/store graph,
not its image-shell destination checks. Providerstore has 676 lines and already
compares expected digests before publication. It does not supply native alias
checks, a complete supplied-input contract or generation reachability alone.

The namespace rows remain the largest feasibility uncertainty: runtime lookup
paths, loader/configuration state, required absence, mount aliases and engine
mounts must be protected without a new generic dependency-analysis service.
The three cached image inspections establish loader/library presence only, not
that the proposed namespace derivation is sufficient or the Bash builds qualify.
If these requirements cannot fit a bounded trusted-helper extension, return to
architecture review before accepting the inventory or implementing it.

Endpoint publication remains separate. The existing E campaign remains paused
before E2.3. No part of this estimate allocates E work to OmegaFlow.
