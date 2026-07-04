---
status: Active
updated: 2026-07-04
summary: Lightweight lifecycle rules for Reploy internal documentation.
---

# Document Lifecycle

This repo uses a lightweight lifecycle for internal documentation.

The goal is to make current guidance easy to distinguish from design history
without turning Reploy into a paperwork project.

## Statuses

- `Draft`: useful work-in-progress notes that are not yet the default reference
- `Active`: current guidance
- `Superseded`: replaced by a newer doc or decision, but kept nearby as context
- `Archived`: historical context only

Not every repo doc needs lifecycle metadata.

In practice:

- design docs should usually carry status metadata
- maintainer runbooks and planning docs may carry status metadata when it
  clarifies whether they are current
- website docs and release notes can stay lightweight unless a stronger signal
  is helpful

## Metadata

For new or materially updated internal docs, use:

```md
---
status: Draft|Active|Superseded|Archived
updated: YYYY-MM-DD
summary: One-line purpose
supersedes: optional path
superseded_by: optional path
---
```

Use only the fields that help. Do not retrofit metadata everywhere just for
consistency points.

## Superseded And Archived Docs

If a doc is superseded, say what replaced it.

That can live in:

- the metadata block
- a short note near the top of the file

Archived docs are historical only. They should use `status: Archived` in
frontmatter and a short archive note near the top of the file.

They should not be treated as current guidance unless a newer doc points back
to them for context.

## Update Expectations

If a change affects architecture, invariants, interfaces, rollout, platform
support, or maintainer operations, update the relevant internal docs in the
same change.
