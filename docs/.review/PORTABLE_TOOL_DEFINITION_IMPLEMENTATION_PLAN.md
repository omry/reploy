---
artifact: awd-swe-design-review-attestation
schema_version: 2
scope_key: 4288e5336b3329ba610c14ee8eee2615dff4b85f8037754801b69b02d8cd4547
scope: {"kind": "path", "primary_target": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "selector": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"}
review_content_identity_sha256: 165a3c0370484153a99e7b29726c6e85e07a7af6e33cb6425ad1bcb2bec583c1
target_content_identity_sha256: 425855ff21c0183053e1c8ea94344260339de1558d37b182e5b18b206e2de82c
baseline_content_identity_sha256: 1499092f32d4c5250d107d20c8e2fbde134ed69b1f139349c7663a59419d018d
target_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "sha256": "ef6c4682a677b8cea08f6492e76006163fbb7950a6a08c687f861c031201dff3"}]
baseline_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_DESIGN.md", "repository": "/home/omry/dev/reploy", "sha256": "2fdd03655a9d6fe64bd3214fef99de4b980623f98ba096f318e1fd98876ed20f"}]
document_repository: "/home/omry/dev/reploy"
document_path: "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"
document_revision_provenance: "72770c44986ccecd2c8f22b349795779edf92080"
document_sha256: ef6c4682a677b8cea08f6492e76006163fbb7950a6a08c687f861c031201dff3
verdict: clean
attested_at: 2026-08-21T15:39:12Z
---
<!-- awd-swe-design-review-attestation:v2 -->

# AWD SWE design-review attestation

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
