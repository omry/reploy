---
artifact: swe-design-review-attestation
schema_version: 3
scope_key: 7100b69dca07b6c2fb228754c96111cc838dcd9f52beca891187b08269876d6d
scope: {"kind": "pr", "primary_target": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "selector": "pr-135"}
review_content_identity_sha256: 2b7692083a4e8be3aad292c429226170f3d0f6458b9773da7a0d66fe8f98fa65
target_content_identity_sha256: f106c2ecdb36cd3aadc6e99c4dfab81ff52efc75e8b3f75a6d52059fb97e9015
baseline_content_identity_sha256: dae0bab05a19ca44b34a2678c28ec00b315d23fdffd43035c6228ff18c3a3c14
target_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md", "repository": "/home/omry/dev/reploy", "sha256": "8347a79f49ca0b2d8b363d4f7a0131d0f783876f34a019c00c9cc1f22fbffc12"}]
baseline_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_DESIGN.md", "repository": "/home/omry/dev/reploy", "sha256": "bd02f5450b2940b3a960f6419994aa9c4e5e65ccb4dc5365b4d17ac233b49d9d"}]
design_dependency_documents: [{"path": "docs/PORTABLE_TOOL_DEFINITION_DESIGN.md", "repository": "/home/omry/dev/reploy", "sha256": "bd02f5450b2940b3a960f6419994aa9c4e5e65ccb4dc5365b4d17ac233b49d9d"}]
document_repository: "/home/omry/dev/reploy"
document_path: "docs/PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md"
document_revision_provenance: "bbf92cf0eff7850aff69a623e43137d61e0b936f"
document_sha256: 8347a79f49ca0b2d8b363d4f7a0131d0f783876f34a019c00c9cc1f22fbffc12
verdict: clean
attested_at: 2026-09-03T21:10:35Z
---
<!-- swe-design-review-attestation:v3 -->

# SWE design-review attestation

Review freshness is determined by the target and baseline document bytes
listed in the version-3 header. Revisions are provenance only.

## Durable review state

## Standing decisions

### R3-1 — rejected — The provider-input recheck does not state the required abort transition

- Reason: Reject the finding's provider-input-change model. Blueprint and Reploy constraints plus all direct and indirect constraints introduced by candidate tool definitions are jointly solved from one immutable operation snapshot. Providers satisfy the selected complete constraint set; acquisition executes that selected solution. Conflicting constraints in the same resolution scope make a candidate ineligible, while genuinely isolated scopes may coexist. Remove the undefined provider-input recheck from both design and plan rather than specifying an abort transition for it.
- Actor: owner
- Decided: 2026-09-03T21:09:16Z
- Owner: omry
- Trigger: Owner confirmed immutable joint constraints, including tool-introduced constraints and scope isolation.

### R3-2 — rejected — The binding-default advertisement invariant has no owning acceptance case

- Reason: Reject the singular default-binding invariant. Model bindings as independently selectable contributions that may reference shared components of the same tool. When omitted, select bindings matching active application providers; allow an explicit binding list or explicit all; if several bindings remain possible without an active-provider match, require an explicit choice. Support typed intra-tool component dependencies now, while deferring general cross-tool dependencies until a concrete requirement justifies their solver complexity.
- Actor: owner
- Decided: 2026-09-03T21:09:16Z
- Owner: omry
- Trigger: Owner approved the Debian-like binding model and scoped dependencies to intra-tool composition.
