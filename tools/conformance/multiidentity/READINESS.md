# A1 readiness disposition

The repaired mandatory mechanism checkpoint passed on 2026-10-11: three exact
and three bounded trials in 547 seconds, all four roles admitted before payload
execution, Docker rejected before mutation, and attempt-owned cleanup verified
against frozen source
`882ecc0a1402a5772573c5bd7da069e305a78f47a4d0509609c3ff97e4e2e840`. The
effective UID resolved canonically to `omry`; hostile login-name and
real/effective UID mismatch regressions fail closed. A1 delivery review and
exact-head PR checks remain pending.

[Current evidence](EVIDENCE.md) records the durable raw receipt directory,
source/receipt hashes, exact test-only runtime configuration and failure history.
The before/after source snapshots bind the repair, and the six current trials
and 12.787-second audit use the same frozen repaired source. The earlier
`ddd560bfb...` six-trial source pairing remains historical. The live failed-RUN
regression failed with the old build arguments and passed with the fix,
restoring image and external build-container inventories.

## Existing-backend operation audit

`operation_audit.py` completed against an attempt-owned Podman 4.9.3
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

The actual product build preflight still rejects this engine. The audit never
bypasses it to launch a shared privileged BuildKit builder. Each deterministic
create name is recorded before the request; ambiguous responses are reconciled
by exact name and attempt label. Removal verifies absence, preserves foreign
collisions and distinguishes HTTP 500 errors from a true HTTP 404.

C1–C7 estimates remain unresolved where an owning design must select a supported
request, builder/supervisor boundary or policy implementation. A narrow passing
operation does not assign a numeric budget to an unresolved design. The initial
500-production-LOC target and 750 hard limit apply to each implementation PR.
Native Podman/build execution remains a separate feature.

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

After A1 is committed, complete the authorized system-install hotfix against
main and rebase this stack onto it. B-design must resolve host authority and
registry decisions before delivering B1–B5. Then reorganize the remaining plan
into implementation order, review it locally and remotely, and proceed with the
new group C. These delivery instructions do not select public identity/schema,
host authority, allocator defaults or a native runtime. I1 remains an independent
optional readiness slice; it is not added to this campaign implicitly.
