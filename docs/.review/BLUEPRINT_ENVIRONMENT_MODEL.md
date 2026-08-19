---
document: BLUEPRINT_ENVIRONMENT_MODEL.md
attestation:
  algorithm: sha256
  digest: 74b644d945657d70f3b8a30da901fc54105805bae8cbebbe3d123663b2ded1f4
  reviewed: 2026-08-19
  rounds: 1-3
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
  intent.
- **Implementation:** deferred to the backlog. Owner: `docs/BACKLOG.md`
  entry "Implement runtime mount integrity checks 2 and 3", which refers
  back to this record. The design text is the specification; when the checks
  land, the status sentences in the section and this entry are the two
  places to update.
