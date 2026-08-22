---
artifact: swe-design-review-attestation
schema_version: 2
scope_key: 4288e5336b3329ba610c14ee8eee2615dff4b85f8037754801b69b02d8cd4547
scope: {"kind": "path", "primary_target": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "selector": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"}
review_content_identity_sha256: 705cf2b7ae989c0360114fdbbac57091968700fb4ec38388504658189f4cb56a
target_content_identity_sha256: 8b67040501af4eb706ce6db70da8884f28e81c95cb64c232d78c5f91173210d0
baseline_content_identity_sha256: dd3801d3a138679925bcf2c96da233f7bd94b340ad293c58f7b088294b071d61
target_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "sha256": "ccff2fda91d29ecc6155a2411d193021232554219d1cf198d0596e62e3837455"}]
baseline_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_DESIGN.md", "repository": "/home/omry/dev/reploy", "sha256": "cc5a7ac158d0bfa72609c991262ae5be9911b3369b39f127b4281ad400a719f1"}]
document_repository: "/home/omry/dev/reploy"
document_path: "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"
document_revision_provenance: "677e8b1ebed497a8876265a71e747ea68877fa8e"
document_sha256: ccff2fda91d29ecc6155a2411d193021232554219d1cf198d0596e62e3837455
verdict: clean
attested_at: 2026-08-22T15:22:53Z
---
<!-- swe-design-review-attestation:v2 -->

# SWE design-review attestation

Review freshness is determined by the target and baseline document bytes
listed in the version-2 header. Revisions are provenance only.

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
