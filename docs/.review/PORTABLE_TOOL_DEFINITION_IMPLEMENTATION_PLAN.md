---
artifact: swe-design-review-attestation
schema_version: 2
scope_key: 4288e5336b3329ba610c14ee8eee2615dff4b85f8037754801b69b02d8cd4547
scope: {"kind": "path", "primary_target": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "selector": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"}
review_content_identity_sha256: f76188f2e84a90c1c461726ee858bc95f0f7e253c633fcba422164d46fc2f18b
target_content_identity_sha256: 48ca3d6aad3baddb99ac7a439c9bff95115744ec51e31957d95b3b46cf5e111a
baseline_content_identity_sha256: e5abf6444afe897ae2d0d14ff24b6770eb0981d7fe64c014e65ff7aabcc3e073
target_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "sha256": "ef4f599f04a6a86ae5cd05f4ab27f006929267a0e8a1294ab67498238c86ad33"}]
baseline_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_DESIGN.md", "repository": "/home/omry/dev/reploy", "sha256": "a0099df6fff638626322e16a9f0f2c1dc7ca8a298b64f1213af35130dd9472a4"}]
document_repository: "/home/omry/dev/reploy"
document_path: "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"
document_revision_provenance: "c7c019b0827e482842f5787238db9e6815181b69"
document_sha256: ef4f599f04a6a86ae5cd05f4ab27f006929267a0e8a1294ab67498238c86ad33
verdict: clean
attested_at: 2026-08-22T14:59:43Z
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
