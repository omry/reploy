---
document: BLUEPRINT_ENVIRONMENT_MODEL.md
attestation:
  algorithm: sha256
  digest: a9676f71f683435aaed3dd897c780b49e148549533f96d6fc0b10930f219a695
  reviewed: 2026-08-20
  rounds: 1-3 plus remote rounds 1-3 (Codex, PR 102)
  verdict: clean
---

# Review sidecar — BLUEPRINT_ENVIRONMENT_MODEL.md

Durable review state for `docs/BLUEPRINT_ENVIRONMENT_MODEL.md`: standing
decisions worth later questions, and the attestation of the last reviewed
bytes. Maintained by `swe:deep-design-review` rounds; working review state
lives under `temp/reviews/` and is reconstructible, this file is not.

## Standing decisions

### B1-1 — Runtime Mount Integrity check 2 remains unimplemented

- **Disposition:** accepted (user decision, 2026-08-19, round 1 of
  `swe:deep-design-review-loop`).
- **Finding (as corrected):** the Runtime Mount Integrity section specified
  three validation checks. Check 1 (reserved destinations) is enforced at
  blueprint validation (`internal/blueprint/resolve.go`) and on every
  compiled runtime-plan mount (`internal/deploy/runtime_policy.go`). Check 3
  (protected-set overlap) is enforced by the runtime policy compiler
  (`internal/dockerdeploy/runtime_policy_compile.go`) and recomputed from the
  build lock before runtime containers run
  (`CurrentBuildMatchesRuntimeV1`). Check 2 (destination absent or an empty
  real directory in the exact immutable image) has no implementation, and
  the probe helper protocol lacks the image-side inspection primitive it
  needs.
- **Correction (2026-08-20):** the original finding claimed checks 2 and 3
  were both unimplemented and that the enforcement point had no code. That
  was wrong about check 3 and the enforcement point: the local review's
  case-sensitive searches missed `runtime_policy_compile.go`. A remote
  review round (Codex, PR 102) caught the error after the first fix landed
  the overclaim in the document; all status text was corrected in the same
  PR. Only check 2 remains open.
- **Fix:** the section and the `reploy build` final-image paragraph state
  the per-check enforcement status, keeping the check-2 specification as
  design intent.
- **Implementation:** deferred to the backlog. Owner: `docs/BACKLOG.md`
  entry "Implement runtime mount integrity check 2", which refers back to
  this record. The design text is the specification; when the check lands,
  the status sentences in the section and this entry are the two places to
  update.

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
