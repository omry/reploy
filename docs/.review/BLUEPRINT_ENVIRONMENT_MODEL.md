---
document: BLUEPRINT_ENVIRONMENT_MODEL.md
attestation:
  algorithm: sha256
  digest: 40864c60b313f6c06a91422e8a4c3c10b06a050ec3e72937f0a879a06a02a48d
  reviewed: 2026-08-20
  rounds: 1-3 plus remote (Codex, PR 102)
  verdict: clean
---

# Review sidecar — BLUEPRINT_ENVIRONMENT_MODEL.md

Durable review state for `docs/BLUEPRINT_ENVIRONMENT_MODEL.md`: standing
decisions worth later questions, and the attestation of the last reviewed
bytes. Maintained by `swe:deep-design-review` rounds; working review state
lives under `temp/reviews/` and is reconstructible, this file is not.

## Standing decisions

### B1-1 — Runtime Mount Integrity checks 2 and 3 are unimplemented

- **Disposition:** accepted (user decision, 2026-08-19, round 1 of
  `swe:deep-design-review-loop`).
- **Finding:** the Runtime Mount Integrity section specified three
  validation checks in the present tense. Check 1 (reserved destinations) is
  implemented at blueprint validation (`internal/blueprint/resolve.go`).
  Check 2 (destination absent or an empty real directory in the exact
  immutable image) and check 3 (overlay subtree does not intersect the
  protected runtime set: provider roots, exclusive leaf claims, executable
  link chains) have no implementation, and the probe helper protocol lacks
  the image-side inspection primitive check 2 needs. The documented
  enforcement point (immediately before creating every runtime container)
  has no code at all.
- **Fix:** the section now states this enforcement status explicitly at both
  sites that stated the rule, keeping the full specification as design
  intent. A remote review (Codex, PR 102) found a fourth site outside the
  section — the `reploy build` final-image paragraph claimed compiled mount
  destinations are validated against the image — now corrected under the same
  disposition.
- **Implementation:** deferred to the backlog. Owner: `docs/BACKLOG.md`
  entry "Implement runtime mount integrity checks 2 and 3", which refers
  back to this record. The design text is the specification; when the checks
  land, the status sentences in the section and this entry are the two
  places to update.

### E-2 — Private environment values do not reach transient commands

- **Disposition:** accepted (user decision, 2026-08-20; found by remote review,
  Codex, PR 102).
- **Finding:** the one-shot command paragraph promised "the same application
  configuration as the workload container", but private `.env` assignments are
  FIFO-injected only into the persistent workload
  (`startAndInjectPrivateWorkloadEnvironmentV1` callers); app-command and
  lifecycle paths call only `preparePrivateWorkloadEnvironmentV1` — validate,
  never inject — and see the masked placeholder.
- **Fix:** the paragraph now states the workload-only boundary.
- **Deferred decision:** whether transient commands gain an equivalent
  injection contract. Owner: `docs/BACKLOG.md` entry "Decide private
  environment injection for transient commands".

### E-3 — Endpoint address grammar and omission defaults were unspecified

- **Disposition:** accepted (user decision, 2026-08-20; found by remote review,
  Codex, PR 102).
- **Finding:** the model recommended loopback but never defined valid address
  forms or omission behavior; the renderer chose `0.0.0.0` for an omitted bind
  and `127.0.0.1` for an omitted publication (`execution_plan.go`), making
  security-relevant behavior implementation-invented.
- **Fix:** the implemented omission defaults are now stated in the endpoints
  section.
- **Deferred decision:** the canonical accepted-address grammar. Owner:
  `docs/BACKLOG.md` entry "Define a canonical endpoint address grammar".
