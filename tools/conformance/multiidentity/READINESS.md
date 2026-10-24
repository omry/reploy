# A1 readiness disposition

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
The six current trials and current audit use the same frozen probe bytes.
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

The refreshed audit above used the new probe source. The following observations
retain the earlier audit context and its product limits.

`operation_audit.py` completed on 2026-10-14 against an attempt-owned Podman 4.9.3
compatibility socket, Docker API 1.41. It restored container, image, volume and
network inventories, removed its labeled resources and stopped its own service.
Native Podman built only the offline scratch fixture and supplied independent
observations. No native fallback converted a compatible-API failure to success.
Zero exit establishes audit completion and cleanup, not universal compatibility.

| Reploy operation seam | Current observation | Product disposition |
| --- | --- | --- |
| Engine/endpoint (`docker_endpoint.go`) | Positive Podman engine identity through the scoped API | Reuse endpoint pinning; add explicit eligible-engine admission in C1. |
| Create/map (`config.go`, controlled-session create planning) | Direct compatible create accepted `auto:size=4096`; Docker CLI cannot express that userns syntax | A narrow compatible create request is required; no native backend is implied. |
| Fixed bounded mapping | `auto:size=4096,uidmapping=0:1:4096,gidmapping=0:1:4096` produced exactly host `100000:4096` for both ID kinds | One host-specific reservation request passed. Concurrent durable reservations, removal/recreation, recovery and revocation remain unproved and owned by B/C. |
| Sparse mapping | The candidate `HostConfig.IDMappings` plus `UsernsMode=private` was rejected with HTTP 500: no UID/GID mapping supplied | This candidate projection failed; it does not prove every compatible projection impossible. C3 must establish an exact-profile request before activation. |
| Image/reference (`docker_image_inspect.go`) | Compatible image inspect accepted the immutable local fixture ID | Reuse is plausible for this local fixture. Registry manifest/content-digest and generated-image publication contracts are not established. |
| Build (`buildkit_probe.go`, `materialization_build.go`) | The actual exported `ProbeBuildKitCapabilities` rejected Podman 4.9.3 because it requires Docker Engine >=24 | C2 must address engine-specific admission and independently prove Reploy's actual frontend/RUN-mount/IID-file contract. No successful product build is claimed. |
| Inspect/exec/wait/logs (`command_execution.go`) | Compatible lifecycle commands worked; the admitted capability-free control exited successfully | Reuse these transport seams subject to owning production tests. |
| Volume (`provider_install_path_updates.go`) | Compatible create/inspect and exact labeled removal worked | CRUD reuse is plausible; preparation/ownership/retention policy remains B/E work. |
| Explicit restart (`config.go`) | Compatible restart preserved the observed UID/GID maps | Transport evidence only. Service replacement, Compose ownership, supervisor launch admission and revocation fencing remain blocked on C-design/C6. |
| Network/endpoint (`controlled_session_network.go`) | Internal network create/inspect/remove worked | Reploy endpoint membership, nftables enforcement, egress denial and cleanup parity remain unproved; G-design must resolve them. |
| Cleanup (`config.go`, recovery paths) | Compatible removal of exact labeled test objects restored the recorded inventories | Production crash/recovery cleanup and allocation retirement remain separate C/B obligations. |

The audit calls `ProbeBuildKitCapabilities`, which rejects this Podman 4.9.3
engine. The capability observation and the separate builder-safety blocker remain
independent: a successful probe is never described as a rejection. Production
generated-build paths currently do not call that probe.
The audit proves neither a successful production build nor permission to launch
a shared privileged BuildKit builder. Each deterministic
create name is recorded before the request; ambiguous responses are reconciled
by exact name and attempt label. Removal verifies absence, preserves foreign
collisions and distinguishes HTTP 500 errors from a true HTTP 404.

C1–C7 estimates remain unresolved where an owning design must select a supported
request, builder/supervisor boundary or policy implementation. A narrow passing
operation does not assign a numeric budget to an unresolved design. The initial
500-production-LOC target and 750 hard limit apply to each implementation PR.
Native Podman/build execution remains a separate feature.

## Separate Podman 6.1.4 operator report

The Reploy dev3 operator report on Debian 11 established Docker-compatible
execution, Compose and registry-based BuildKit building with result loading.
Reploy generated layers still failed: the containerized builder could not
resolve its daemon-local base tags. C2 must prove local-base transfer, output
loading and the full materialization/finalization path. The Podman 4.9.3 audit
probe rejection does not establish the cause on that newer host.

HF3 [#241](https://github.com/omry/reploy/pull/241) fixes temporary build-reference
cleanup. HF4 [#242](https://github.com/omry/reploy/pull/242), a separate draft PR,
fixes the remaining environment publication and legacy-generation cleanup paths.
Neither changes the generated-build backend. The latest operator follow-up
completed build, verify and execution only through diagnostic native-build and
truthful health-metadata adapters. Compatibility inspection omitted actual
HEALTHCHECK NONE metadata, and NONE startup attempted a healthcheck timer. Those
remain separate blockers. Remaining conmon monitors have no confirmed cause and
require separate disposable reproduction. No adapter or timer workaround is
selected for production by A1.
No matching-server test or native backend support is claimed here.

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
become the accepted supervisor. C-design/mail-compatible must select and verify
an image-locked supervisor role, minimal capability set, dedicated credentials,
non-dumpability, separate child sessions and descriptor closure. Otherwise
I2/I3 remain blocked. No custom supervisor implementation is selected here.

## Next work

HF1 and HF2 were implemented against main and published beneath this stack.
Both hotfix PRs are merged on main and published in 0.8.0.dev3.
The four remaining open PRs are reviewed bottom-up on their current heads.
Complete A1's design and PR checks, then stop before B as requested.

B-design still needs the host-authority and registry decisions before B1–B5.
The later request to put the plan in implementation order remains after B.
No public identity/schema, allocator default or native runtime is selected here.
I1 remains an independent optional readiness slice; it is not added implicitly.
