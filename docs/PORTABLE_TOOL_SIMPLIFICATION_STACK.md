---
status: Proposed
updated: 2026-09-22
summary: Five reviewable corrections between PTD-23.3.7 and PTD-24 that reduce portable-tool bootstrap complexity without weakening external repository boundaries.
implements: docs/PORTABLE_TOOL_DEFINITION_DESIGN.md
---

# Portable Tool Simplification Stack

## Authority and invocation

This plan implements the [Portable Tool Definition Design](PORTABLE_TOOL_DEFINITION_DESIGN.md)
under the repository trust and consumption contract in
[Repository Design](REPOSITORY_DESIGN.md). It is a corrective prerequisite to
PTD-24 in the [PTD implementation plan](PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md),
not a replacement for that campaign. The next delivery call is:

```text
global:swe:deliver-design-stack(docs/PORTABLE_TOOL_SIMPLIFICATION_STACK.md, all)
```

Start from the approved PTD-23.3.7 head. Deliver the five slices below in
order. Each owns one focused change, one commit and PR, a local independent
review stamp, all required checks, and its own remote PR cycle at the exact PR
head. Do not begin PTD-24 until this stack retains current-head approval.
Changing an earlier head invalidates affected descendant review evidence.

The stack changes no external-repository transport, TUF trust, publisher
attestation, lifecycle, target support claim, artifact content, or arbitrary
installer-code policy. Existing records and locks may change identity before
Reploy's first release; no reader for unreleased intermediate schemas is
required. Persisted lock entry points still validate untrusted or retained
bytes fully. Keep the initial Java and Playwright matrices fixed.

## Delivery queue

| ID | Slice | Depends on |
| --- | --- | --- |
| PTD-S1 | Name Python binding and wheel schemas | PTD-23.3.7 |
| PTD-S2 | Emit Python CLI aliases directly into the layer | PTD-S1 |
| PTD-S3 | Introduce validated Python binding handoffs | PTD-S2 |
| PTD-S4 | Derive redundant provider operations and authorities | PTD-S3 |
| PTD-S5 | Narrow validation scheduling and evidence plumbing | PTD-S4 |

### PTD-S1: Name Python binding and wheel schemas

Scope: replace the generic `portable-tool-binding-v1` and
`portable-tool-binding-artifact-v1` record kinds with the Python binding and
wheel artifact kinds in the normative design. Update the single shared record
contract, embedded definitions, generated canonical bytes, reference checks,
closure identities, lock tests, and documentation. Preserve generic binding
selection in release and target contracts. Do not invent a schema for another
ecosystem.

Acceptance: strict decoders reject the retired kinds; the regenerated catalog,
selected closures, and locks are deterministic; Java and Playwright selection,
wheel eligibility, acquisition, and locked replay pass their focused checks.

### PTD-S2: Emit Python CLI aliases directly into the layer

Scope: derive sorted tar symlinks from validated destination, computed target,
and binding-identity claims. Remove the host staging alias transaction and its
Unix and Windows publication mechanisms. Preserve source-image destination
and ancestor checks, deterministic archive metadata, image configuration
checks, and exact final-image link and executable validation.

Acceptance: collisions, overlapping paths, unsafe names, wrong targets,
interrupted layer build, and substituted final-image links fail closed; a
Windows build host produces the same Linux layer semantics without privileged
host symlinks. No host staging entry is a materialization authority.

### PTD-S3: Introduce validated Python binding handoffs

Scope: create construction-controlled immutable handles for a validated
selected binding and verified acquired wheel. Derive provider projection,
fresh acquisition, locked replay, and materialization inputs from those
handles. Remove copies whose only purpose is to compare independently
constructible plan, closure, projection, record, and acquisition values.

Acceptance: fresh and locked paths reject omitted, duplicate, substituted, or
misattributed bindings before network or materialization; source authorization,
descriptor verification, wheel inspection, selected-closure identity, and
zero-network replay remain mandatory. Repository and persisted-lock decoding
retain full validation. Tests exercise exact handoff construction and misuse
at those boundaries rather than mirroring internal field copies.

### PTD-S4: Derive redundant provider operations and authorities

Scope: inventory every consumer of the canonical provider DAG and lock. Keep
scope domain ownership, deterministic semantic and filesystem conflicts, the
acquisition barrier, and binding-materialization-to-export-to-capability
ordering. Derive operation fields and authority projections that are pure
functions of retained selected inputs rather than storing and cross-validating
them as independent lock authority. Revise the unreleased lock schema only
where needed; do not drop information required for replay verification.

Acceptance: plan and locked replay reject missing or conflicting ownership,
ambiguous shared domains, collisions, changed ordering, and injected or
substituted records. Equivalent input order produces identical canonical
locks. A measured code and serialized-shape comparison shows which redundant
operations or fields were removed.

### PTD-S5: Narrow validation scheduling and evidence plumbing

Scope: derive each selected image/scope schedule from its validated lock and
feed the fixed executor through one construction-controlled scheduled-profile
view. Reduce duplicate wrappers, cloning, decoding, and attribution checks
that exist only to reconcile mutable intermediate schedules. Preserve the
locked profile reference, runtime environment, image/rootfs identity,
networkless resource-bounded execution, ownership-safe cleanup, and durable
passing evidence. Bind evidence identity to the exact runtime projection and
coalesce observations only when profile, image subject, and runtime inputs
are identical.

Acceptance: every required probe runs once for each selected case, including
cases sharing a profile; failures, timeouts, truncated output, attribution
drift, and cleanup errors cannot yield passing evidence. Locked replay never
consults the embedded catalog or network for a validation profile.

## Exit gate

Every slice has its own current-head approved PR and all jobs produced by
`.github/workflows/ci.yml` for that exact head have succeeded, including Linux
CI and every target smoke job. The assembled stack passes focused fresh-build
and offline-replay checks. Any lock schema or identity changes are recorded in
the relevant PR, and the final design and implementation plan describe the
delivered behavior. The next ordinary PTD delivery item is PTD-24.
