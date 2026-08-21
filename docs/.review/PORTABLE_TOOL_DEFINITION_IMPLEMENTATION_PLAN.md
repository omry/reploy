---
artifact: swe-design-review-attestation
schema_version: 2
scope_key: 4288e5336b3329ba610c14ee8eee2615dff4b85f8037754801b69b02d8cd4547
scope: {"kind": "path", "primary_target": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "selector": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"}
review_content_identity_sha256: fd57334e318bdf6b9a92343729fdf46ae2e860563221d5f03a8ce26be56432d1
target_content_identity_sha256: d946ba8bc34d359e6450cdf596697fa699e50ceec9cc12d4fedc1ac0b023ab6d
baseline_content_identity_sha256: c220e3bd164dfd697950ebf08034112cbefbf83c57bec3afa02d9e1441bf79d4
target_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "sha256": "bbb301c8f2e8ba4c17496df364c30cdc61ca41b2619eb20b12cfc2730f30aeca"}]
baseline_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_DESIGN.md", "repository": "/home/omry/dev/reploy", "sha256": "153f3d9971ec5e2fc110ed495fda58686481228548065b158e5ac41cbb44c94e"}]
document_repository: "/home/omry/dev/reploy"
document_path: "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"
document_revision_provenance: "29b3892e6028357d324951875bf38e8fe134a2f3"
document_sha256: bbb301c8f2e8ba4c17496df364c30cdc61ca41b2619eb20b12cfc2730f30aeca
verdict: clean
attested_at: 2026-08-22T12:13:26Z
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
