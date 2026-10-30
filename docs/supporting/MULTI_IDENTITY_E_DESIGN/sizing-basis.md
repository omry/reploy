# Remaining E sizing basis

The clean E design owns the one ordered inventory and summed LOC ranges. This
file explains the estimate; it is not a second task list or approval. Counts are
changed physical production/configuration vs test-code lines, including comments
and blanks, under the convention in that document. No working hours.

## Existing implementation and concrete deltas

- The mapped waiter has 288 lines, native process pinning and a one-shot frame
  already implemented. E2.3.2 extends its request/proof fields and the mapped
  create representation; it does not rebuild release transport.
- The strict ordinary create adapter has 287 lines. Keep it strict and add only
  the typed mapped overlay. Ordinary seccomp materialization remains unchanged.
- The admitted transient runner has 301 lines. E3.1.1 reuses queue completion,
  cleanup, endpoint and errors but must preserve mapped release lock ordering.
- Current workload lifecycle has 378 lines and existing persisted lifecycle
  validation/atomic writes. E3.1.2 extends mapped ownership/replacement; it does
  not build a new service manager, watchdog or database.
- Existing per-entry files already own current-build loading, output ownership,
  queueing and role/executable resolution. E4.1.1 centralizes the final private
  mapped contract and E4.1.2 integrates it behind unchanged public guards.
- Existing startup authority/fd payloads and sandbox proof fixtures are reused.
  Child tests cover their changed contract only; E4.2 adds integrated source/ABI/
  profile/runtime conformance and receipts, with no new production feature.

The production range decompositions below explain the clean plan's totals. They
are estimates of changes to real seams, not measured code or guaranteed caps.

| Owning leaf | Production/configuration delta decomposition | Test-code work |
| --- | --- | --- |
| E2.3.1 | 150–240 transform + 50–110 canonical inputs/ABI identity = 200–350 | 450–700 for seed-condition adversaries, native clone flags, compat/unknown ABI rejection, deterministic inputs and ordinary-path preservation. |
| E2.3.2 | 100–160 typed create overlay + 120–180 proof/waiter binding + 80–110 pre-release/compatibility validation = 300–450 | 650–1000 for actual passed bytes/caps, wrong/missing observations, no auto-removal/restart, ABI and engine-profile refusal, existing gate integration. Final entry-plan construction belongs only to E4.1.1. |
| E3.2 | 75–150 for the closed private mode/error boundary using existing guard machinery | 300–500 for no-effect occupied/host refusal and bounded safe-entry attack fixtures; no implementation of safe live exec. |
| E4.1.1 | 150–240 locked snapshot/role/closure consistency + 100–160 canonical immutable plan = 250–400 | 450–700 for complete identity binding, stale snapshots, argument mutation and unsupported tuple/stream cases. |
| E3.1.1 | 100–170 release-to-queue ownership + 150–230 completion/cancellation/cleanup = 250–400 | 550–850 for exact ownership, lock lifetime, master/finite distinction, lost response and cleanup retention. |
| E3.1.2 | 100–160 existing lifecycle/handoff validation + 150–240 fresh retirement/replacement = 250–400 | 500–800 for fresh generation/host authority, failed retirement, restart fencing, uncertain detach/startup acknowledgement. |
| E4.1.2 | 140–245 for entry adapters + 110–155 shared dispatch/guard wiring = 250–400 | 600–900 for each real entry path, both session owners, no-effect guards, ordinary-path preservation and common final checks. |
| E4.2 | 0; qualification/conformance is test-only | 650–1050 for integrated private journey, repeated profiles, exact binary/compiler/runtime evidence and explicit blocked cases. |

The final leaf must not absorb missed implementation as an unnamed catch-all.
A real production defect returns to its owning reviewed responsibility. Shared
fixtures have one owner and the integrated journey reuses them. No estimate
includes production F storage/network/code derivation, G public activation,
OmegaFlow portability or a future live-exec enablement mechanism.

## Adoption of the revised plan

The retained delivery campaign is legacy, with five completed entries and five
unfinished parent entries. The supported guarded `apply-transition` operation
with `transition_kind=campaign-replan` accepts an exact separately approved
replacement design/tasks/queue snapshot while retaining the established handle,
cumulative budgets, history, exclusions and completed/mapped work. Its label
does not grant approval. Design does not apply this transition.

The standardized-owner `replan-slices` command requires version-5 ownership and
is not an implicit migration route for this campaign. Preserve its legacy
interpretation. Before delivery, construct and verify the exact approved
replacement snapshot through the retained helper route; preserve and reconcile
existing work reservations without assuming that additional children are free or
resetting already consumed counters. If an exact authority/preservation check
fails, report that missing proof and do not restart the campaign.
