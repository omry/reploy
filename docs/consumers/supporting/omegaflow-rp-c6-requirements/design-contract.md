# OmegaFlow RP-C6 design output contract

This maintainer instruction supplements the active `global:swe:design` run.
It governs document preparation and review. It does not approve requirements,
architecture, implementation slices, implementation, publication or release.

## Clean outputs

The feature prefix is `omegaflow-rp-c6`. All three outputs reside in
`docs/consumers/`:

- `omegaflow-rp-c6-requirements.md`
- `omegaflow-rp-c6-high-level-design.md`
- `omegaflow-rp-c6-implementation-plan.md`

Create each document at its responsible workflow phase. Do not publish empty
architecture or slice plans as finished outputs. Each main document describes
only the current agreed requirements or design, observable behavior, constraints
and applicable readiness. Identify unapproved proposals explicitly. Keep history,
investigations, review records and superseded choices in supporting material.

## Supporting material

Use these owning-document directories:

- `supporting/omegaflow-rp-c6-requirements/`
- `supporting/omegaflow-rp-c6-high-level-design/`
- `supporting/omegaflow-rp-c6-implementation-plan/`

Each document links its durable supporting material. Prefixes identify the
consumer and feature so other designs can coexist in the same top-level directory.
Store shared source requirements and this contract under the requirements stem;
other documents may link that same material rather than duplicate it. Keep
investigation evidence with its owning design and sizing evidence with its plan.
Keep secrets and raw sensitive execution data out of tracked supporting material.
Existing private AWD review authorities and runtime records remain in their
original stores; supporting documents may identify their evidence locators and
limitations without copying a stamp or manufacturing approval.

## Workflow continuity

Continue run `run_3k0spmw1cd3w8pmjsq65ggeese` with its existing pinned
`swe:design` definition, original `docs/consumers/omegaflow.md` document argument,
default security selection, and requirements, reviewed-HLD and reviewed-LLD
human gates. Preserve the consumer record, original source preimage, decisions,
reviews, tracking and separate E priority pause. The three clean documents are
additional phase outputs alongside the workflow-required artifact. Use the same
review mechanisms and bind review and approval to the exact current clean output
as well as the required workflow output; a copied clean document is not approved
merely because another artifact was reviewed. Reconcile corresponding content
before claiming a phase ready. Do not hand-edit stamps, runtime graphs or global
AWD configuration.

## Questions and stack agreement

The maintainer's correction supersedes the proposed four-question limit and any
saved-function requirement to close design decisions at a fixed count. There is
no agreed numerical question cap. Keep the existing decision agenda and a running
question/answer record. Reuse sufficient prior answers, ask one concrete question
at a time when needed, and retain unresolved decisions explicitly. Do not reset
consumption history, mark unresolved work settled, or restart the design run to
obtain more allowance. If a runtime batching boundary is reached, preserve the
remaining decisions and continue through supported tracking; it is not a reason
to force a decision or claim completion.

Discuss and agree the stack when a real stack choice exists. An extension to an
existing project inherits its established stack unless evidence justifies a
material change. Do not manufacture an approval question to reconfirm existing
languages, interfaces or primitives. For this Reploy extension, retain the
existing implementation stack as a project constraint. Surface a necessary
material addition or change before adopting it. Ordinary engineering decisions
do not require individual approval.

## Implementation estimates

Each slice and the plan total must provide separate ranges for:

- **Production/configuration LOC:** authored shipped implementation and runtime,
  build or packaging configuration needed for the slice.
- **Test-code LOC:** authored tests, test-only helpers and textual fixtures.

The unit is a changed physical text line: additions plus removals against the
slice's accepted parent, including changed blank and comment lines, not net growth.
A line belongs to one category. Test-only configuration and fixture programs count
as tests. Generated output, vendored dependencies, lockfile churn, binary payloads,
Markdown design documents and review bookkeeping are excluded from these LOC
columns; disclose material excluded work separately. Payload bytes are a separate
size constraint, not code lines. Reuse the delivery helper's supported per-path
role classifications and report any accounting mismatch explicitly.

Count shared foundations once in their owning prerequisite. Show per-slice lower
and upper bounds and totals that sum those bounds. Preserve the repository's
existing production-size limits and coherent slice boundaries. State uncertainty
and source/proof basis. No working-hour estimates. Preliminary consumer estimates
remain historical evidence until replaced by the reviewed bottom-up slice plan.

## Maintainer source

The maintainer instructed this session to use the existing AWD `swe:design` workflow
with three clean documents, supporting material under `supporting/<document-stem>/`,
separate production/configuration and test-code line estimates with totals and a
counting convention, no working-hour estimates, explicit stack discussion and
agreement, and further questions when needed without a fixed question count
forcing premature decisions. Preserve required workflow outputs and tracking.
