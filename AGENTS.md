# Agent Notes

## Source Control

This repository uses Sapling (`sl`) for source-control workflows.

## Pull Request Approval Checks

Before recording PR-cycle approval or adding the `approved` label, require every
job produced by `.github/workflows/ci.yml` for the exact PR head to complete
successfully. This includes the Linux CI job and every target smoke matrix job.
Missing, pending, cancelled, and failed jobs block approval. Supply those job
names as the AWD PR-cycle helper's required checks; an empty required-check
list is invalid while this workflow is enabled. GitHub branch protection does
not define this repository's full approval gate.

## Changelog Fragments

User-facing changes should include a Changie release-note fragment under
`.changes/unreleased/`.

Use one of the configured kinds: `Added`, `Changed`, `Deprecated`, `Removed`,
`Fixed`, `Security`, or `Docs`.

Pure refactors, test-only changes, and internal cleanup do not need fragments
unless they affect the maintainer or release workflow.
