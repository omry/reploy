# Feedback for the modified SWE design workflow

Collected during the OmegaFlow RP-C6 design run. This is input for a later AWD
Config improvement, not a request to change global helpers now. Append concrete
observations as the design continues. Separate observed behavior, agent mistakes,
new maintainer requirements and proposed improvements.

## Context and evidence

- The active design run is `run_3k0spmw1cd3w8pmjsq65ggeese`.
- Its pinned `swe:design` fingerprint is
  `sha256:9cdd5fda8ffc9a91f74ea6a5599fa1e4dfb4c3be8d2628befa38ccf20d368ba8`.
- Its pinned interview fingerprint is
  `sha256:9fbd130f1e35c44d278181928885045e8dde774ed8319fbbd18c1060d8cc96e9`.
- The maintainer's additional output requirements are retained in the
  [local design contract](design-contract.md).
- The unnecessary stack question and its withdrawal are recorded in
  [interview corrections](interview-corrections.md).
- The [original consumer request and evaluation](consumer-request-and-evaluation.txt)
  remains a source snapshot, not an approved requirements/HLD/LLD result.
- At collection, no requirements, HLD or LLD phase approval has been recorded.
  The stack question is withdrawn, with no answer or approval fabricated.

## 1. Ask about an actual decision, not a checklist heading

**Classification: agent error; corrected locally.**

The agent treated “discuss and agree on the stack” as a mandatory question about
using Go, the existing Reploy CLI, YAML and JSON. This extends an existing
application. No credible alternative or necessary technology change had been
identified. The maintainer asked whether this was even a relevant question.

The existing interview already requires materiality, evidence and reuse of prior
decisions. The agent passed that check for the wrong reason: the general
instruction mentioned stack agreement. The workflow did not require this
particular bad question.

**Suggested improvement:** make stack discussion conditional on a real choice.
For an existing project, record its established stack as a constraint. Ask when
a new project needs a selection, or evidence justifies a material addition or
change. Require a concrete consequence that differs between plausible answers.
An instruction heading alone does not establish decision value.

**Acceptance example:** an extension to an existing Go CLI with no proposed new
runtime reaches engineering investigation without asking permission to retain
Go. A proposed new service or runtime produces a concrete decision brief before
adoption. Withdrawing a pointless question does not record stack approval.

## 2. Question counts must not become readiness criteria

**Classification: saved-workflow limitation; explicit maintainer override active.**

The pinned interview requires an agreed finite allowance of at most eight design
questions and a setup gate for that allowance. The agent proposed four questions
before establishing that four owner decisions existed. The maintainer instructed
the agent to ask further questions when needed and not let a fixed count force
premature decisions.

**Suggested improvement:** track the decision agenda, actual questions, answers,
withdrawals and unresolved work. Close questioning because the needed owner
decisions are settled or because useful further questions need engineering
evidence. Do not close it merely because a count is reached. Preserve bounded
execution/recovery and cumulative history without presenting a transport batch
limit as the owner's decision allowance or requiring a ritual budget approval.

**Acceptance example:** supplied requirements can be reused without inventing
questions to fill a quota. If more than eight genuine decisions emerge, retain
and address them without silently resetting history or claiming readiness.
Further questions must still pass relevance and materiality checks.

## 3. Make the three phase documents native outputs

**Classification: additional maintainer requirement; interim contract recorded.**

The saved composition uses one document for requirements, HLD and LLD. The
maintainer now requires `<name>-requirements.md`, `<name>-high-level-design.md`
and `<name>-implementation-plan.md`, each describing the current agreed design.
The interim instruction preserves the workflow artifact alongside these files.
That adds a risk of duplicated statements and drift; no drift failure is claimed
to have occurred yet.

**Suggested improvement:** resolve and retain one canonical output per phase,
with explicit phase-to-phase references and exact review/approval identities.
Keep orchestration state separate from design prose. The supported transition
from the interim arrangement must preserve existing decisions, review evidence
and continuation points instead of restarting a run.

**Acceptance example:** a requirements change identifies the affected downstream
design and plan. Each phase approval covers its actual current output. Copying
a reviewed file or an approval stamp cannot approve another document. An
unwritten HLD or unestimated plan is not reported as complete.

## 4. Keep supporting material durable and namespaced

**Classification: additional maintainer requirement; layout adopted locally.**

The interview uses ignored `temp/design-interviews` working state, while the
consumer record already mixes requirements, investigation notes, preliminary
estimates and progress. The maintainer wants durable supporting material under
`supporting/<document-stem>/` and clean main documents.

**Suggested improvement:** retain compact executor state where the runtime owns
it, but place durable human-facing sources, investigations, review records,
superseded choices and progress under the owning document's supporting directory.
Link them from that document. Preserve original trusted review authorities in
their existing stores; a tracked copy must not become approval authority.

Use a feature-specific name, such as `omegaflow-rp-c6`, rather than a shared
`requirements.md` or `supporting/reviews/` directory.

**Acceptance example:** two designs coexist in one top-level directory without
overwriting sources or evidence. Their main documents contain the current
requirements/design rather than a discussion transcript. Supporting links remain
valid; source snapshots preserve their identity and original path context.

## 5. Define estimate categories and units in the plan

**Classification: additional maintainer requirement; not a measured sizing result.**

The maintainer requires production/configuration and test-code estimates for
each implementation slice and totals, with a counting convention and no
working-hour estimates. The local contract defines the convention; the actual
bottom-up implementation slices have not been designed or approved yet.

**Suggested improvement:** make the plan template require separate lower/upper
columns for production/configuration and tests, summed totals, and explicit
rules for additions/removals, fixtures, generated files and excluded work.
Count shared foundations once. Align the stated convention with supported
delivery measurement classifications; report a mismatch instead of quietly
changing units between planning and delivery.

**Acceptance example:** totals equal the sums of the slice bounds. Test-only
configuration is not also counted as production. Payload bytes are not LOC.
Historical preliminary ranges cannot stand in for reviewed slice estimates.
The plan contains no working-hour estimates.

## 6. Distinguish unresolved engineering from owner decisions

**Classification: execution guidance to reinforce.**

The current open work is how to protect retained payload bytes through use, bind
inspection to the admitted state, and expose the smallest sufficient public
interface. Those are engineering investigations under the agreed requirements.
They do not justify asking the maintainer to design the mechanism before the
agent has inspected existing primitives and gathered evidence.

**Suggested improvement:** investigate first. Ask only when evidence exposes a
material choice about behavior, authority, compatibility, scope or significant
cost. Present the consumer scenario, the consequences, viable choices and a
recommendation. Keep questions one at a time; do not present dense labels that
the owner must reconstruct into a decision.

**Acceptance example:** the agent investigates protected capture and admission
using existing store/session mechanisms. If none can satisfy the agreed boundary
without a material change, it presents that concrete tradeoff. Ordinary internal
choices continue autonomously. An unresolved engineering prerequisite remains
visible and prevents a false implementation-ready claim.

## 7. Name the purpose of each requirements approval

**Classification: observed workflow sequencing; clarity to preserve in the update.**

The interview closeout requests confirmation of the requirements baseline before
the composition's formal requirements review. The later composition also checks
for exact current requirements approval. The formal review has not run yet, so
no duplicate approval interaction is claimed here.

**Suggested improvement:** state clearly whether the owner is confirming the
intended requirements, accepting reviewed requirements, choosing architecture or
authorizing implementation. Reuse an exact existing approval when its scope and
artifact remain current; request renewed approval for material changes. Do not
make the owner guess which gate a generic “approve?” refers to.

**Acceptance example:** the baseline-confirmation brief names the observable
consumer outcome, constraints and remaining engineering work. After review,
unchanged exact requirements retain their approval; changed major requirements
are presented with their concrete consequences. Neither approval authorizes
implementation.

## 8. Separate required facts from transport and invalidation mechanisms

**Classification: consumer scope clarification; not an implementation finding.**

OmegaFlow clarified that its Bash compatibility check needs a qualified digest,
resolved path/type and system-rc absence, rather than raw file-byte export. It
also clarified that moving an original tag must not invalidate a retained
immutable image that Reploy still launches. Both corrections came from the
consumer; the earlier byte-export request was an actual source requirement,
not an agent-invented capability.

**Suggested improvement:** verify the observable consumer need before carrying
a transport mechanism into the minimum delivery. Define which prepared facts an
identity binds and distinguish changes to them from changes to irrelevant source
references. Trace both valid continuation and required refusal in acceptance.
Keep earlier estimates and decisions as history instead of silently rewriting
their basis or claiming measured savings.

**Acceptance example:** no raw file-export API is designed without a concrete
need. Original-tag movement remains valid when all retained admitted state is
unchanged; selection of a different image or protected payload refuses before
bootstrap. A source clarification updates the existing pending baseline and
preserves the original approval request rather than inventing approval or
restarting the interview.

## Preserve these controls

- Keep requirements, reviewed HLD and reviewed detailed-plan approvals distinct.
- Reuse sufficient existing decisions; do not infer approval from silence, a
  general workflow correction or rejection of an irrelevant question.
- Keep the saved run, approval evidence and actual consumption history through
  corrections. Do not fake a pass or restart to avoid a limit.
- Preserve the separation between design, implementation and publication.
- Keep required review and tracking. Reduce clerical burden without weakening
  the exact artifact and evidence boundaries.

## Later follow-up

The suggestions above are collected, not implemented in AWD Config. Continue
appending observed cases here. Assess the document/review integration again when
the first clean requirements document reaches review; that path has not run yet.

## 9. Integrate phase outputs with exact review selectors

Observed in this run: the pinned design function reviews the whole original
consumer document exactly. The added clean requirements output therefore needed
a separate runtime-backed design review and its own helper stamp. Both reviews
completed one clean round; neither target could borrow the other stamp. This is
an interim-contract integration cost, not evidence that either review may be
skipped. Proposed improvement: native named phase outputs and exact multi-artifact
review/approval bindings, while retaining the original whole-document lineage
and preventing copied files from inheriting approval. The current run and pins
were preserved; no global workflow changes were made.

## 10. Inherit product host support before posing cohort choices

Agent error: the agent treated the current WSL2/Desktop proof environment and
Linux-only controlled-session implementation as grounds to ask whether public
host coverage could be narrowed. README already states Linux/macOS/Windows
product support. The maintainer corrected the frame: inherit that commitment,
include WSL2/Desktop and qualify the complete public path with unchanged
integrity. Specific channel/watchdog gaps are delivery dependencies, not a new
product-platform decision. Improvement: distinguish existing product commitment,
feature-path implementation gaps and actual qualification evidence before asking
an owner. This does not remove real scope gates or authorize false compatibility
claims. No global workflow change was made.

## 11. Separate proof access, mechanism evidence and final acceptance

Observed during LLD investigation: a WSL2/Desktop scratch relay can prove one
engine-stdio/peer-credential mechanism while native Windows/macOS watchdog
survival remains unproved. Current source tests exercise the shared cleanup
algorithm, not actual native child/handle behavior. Existing smoke CI skips
Windows Docker. A source-based estimate that assumes a portable channel already
exists materially undercounts shared production prerequisites.

Suggested improvement: record existing product support, current implementation
gaps, proof execution targets and downstream full-path acceptance separately at
HLD/LLD handoff. Own and size shared prerequisites explicitly. Distinguish a
missing execution target from a new product-cohort decision, and request only
the target/access information needed. Retain negative attempts and narrow proof
limits. Do not turn a provisional responsibility estimate into approved slices
or use a fixture to claim the final public inspection/admission/launch path.
No global workflow changes were made.

## 12. Audit existing behavior and adjacent campaign ownership before sizing

Agent planning weakness: the candidate estimate named a new container-private
broker without clearly separating it from the existing controller application
broker/protocol and the genuinely missing engine/peer transport. Likewise, new
namespace rows did not identify the existing protected-path compiler and E2.2's
F-owned live-proof contract prominently enough. The code has useful reuse, but
its existing socket client is not already portable and E supplies no production
F observation provider. Neither a complete rewrite nor claiming prerequisites
already exist is justified.

Suggested improvement: require a concrete existing/missing delta and an adjacent
plan ownership crosswalk before presenting a delivery forecast. Count shared
foundations once, preserve backend-specific proof/authority and qualification,
and label unresolved estimates explicitly. The user asked for this reassessment
before implementation. The audit retained earlier estimates and corrected their
interpretation without claiming a lower measured cost or fixing runtime gaps.
No global workflow changes were made.
