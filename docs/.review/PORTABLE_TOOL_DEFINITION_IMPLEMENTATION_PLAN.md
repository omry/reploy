---
artifact: swe-design-review-attestation
schema_version: 3
scope_key: 4288e5336b3329ba610c14ee8eee2615dff4b85f8037754801b69b02d8cd4547
scope: {"kind": "path", "primary_target": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "selector": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"}
review_content_identity_sha256: 2aa5109b253b7445e49c8ab8825df7967f0e9b1b66124a1eee21e24c56fa6961
target_content_identity_sha256: 6a49b47bc7de73e0af1094bd3b7ec96ee622830032c19a3e9a669d67aab472a7
baseline_content_identity_sha256: dae0bab05a19ca44b34a2678c28ec00b315d23fdffd43035c6228ff18c3a3c14
target_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "sha256": "52422a63c0ef05f4e95b9d1223da95ca74db87fe546cd1454f45a8e646779928"}]
baseline_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_DESIGN.md", "repository": "/home/omry/dev/reploy", "sha256": "bd02f5450b2940b3a960f6419994aa9c4e5e65ccb4dc5365b4d17ac233b49d9d"}]
design_dependency_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_DESIGN.md", "repository": "/home/omry/dev/reploy", "sha256": "bd02f5450b2940b3a960f6419994aa9c4e5e65ccb4dc5365b4d17ac233b49d9d"}]
document_repository: "/home/omry/dev/reploy"
document_path: "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"
document_revision_provenance: "5d3e49f375e0"
document_sha256: 52422a63c0ef05f4e95b9d1223da95ca74db87fe546cd1454f45a8e646779928
verdict: clean
attested_at: 2026-09-01T10:18:56Z
---
<!-- swe-design-review-attestation:v3 -->

# SWE design-review attestation

Review freshness is determined by the target and baseline document bytes
listed in the version-3 header. Revisions are provenance only.

## Durable review state

## Standing decisions

### R1-5 — rejected — The provider-input recheck does not state the required abort transition

- Reason: Reject the finding's provider-input-change model. Blueprint and Reploy constraints plus all direct and indirect constraints introduced by candidate tool definitions are jointly solved from one immutable operation snapshot. Providers satisfy the selected complete constraint set; acquisition executes that selected solution. Conflicting constraints in the same resolution scope make a candidate ineligible, while genuinely isolated scopes may coexist. Remove the undefined provider-input recheck from both design and plan rather than specifying an abort transition for it.
- Actor: owner
- Decided: 2026-08-21T04:00:48Z
- Owner: omry
- Trigger: Owner confirmed immutable joint constraints, including tool-introduced constraints and scope isolation.

### R1-7 — rejected — The binding-default advertisement invariant has no owning acceptance case

- Reason: Reject the singular default-binding invariant. Model bindings as independently selectable contributions that may reference shared components of the same tool. When omitted, select bindings matching active application providers; allow an explicit binding list or explicit all; if several bindings remain possible without an active-provider match, require an explicit choice. Support typed intra-tool component dependencies now, while deferring general cross-tool dependencies until a concrete requirement justifies their solver complexity.
- Actor: owner
- Decided: 2026-08-21T04:36:53Z
- Owner: omry
- Trigger: Owner approved the Debian-like binding model and scoped dependencies to intra-tool composition.
