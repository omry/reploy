# OmegaFlow RP-C6 consumer adoption reconciliation

This is a Reploy-side contract mapping and handoff. It does not claim that the
OmegaFlow checkout has been edited, reviewed, qualified or adopted. The relayed
corrected request accepts protected copies and supplies the bounded hash
inventory. The approved Reploy architecture chooses ordinary immutable-image
delivery. Older consumer assumptions must be reconciled before adoption.

## Required consumer changes

| Older consumer assumption | Agreed contract | Adoption check |
| --- | --- | --- |
| Final executable runtime must be a read-only source bind at `/omegaflow-runtime`. | Retained bytes are verified against the supplied complete inventory and independent manifest digest, then delivered in the normal immutable final image at those paths with fixed modes. Physical source-bind type is not the acceptance fact. | C6.3 accepts protected image delivery and verifies protected inventory/path/mode facts. Remove the old required physical bind from reserved composition; do not patch composed YAML later. |
| A staged-tree hash establishes the runtime that will launch. | Hash the actual retained bytes against supplied expected hashes and verify the exact final-image inventory. The public prepared identity binds those facts through admission/use. | Pre-capture corruption refuses. Original source mutation/removal after independent capture remains valid. Workload writes and shadowing cannot replace protected contents. |
| Obtain raw Bash/system-rc bytes. | R4 requires bounded resolved path/type/digest/absence facts; no raw file export. | Compare Bash digest to OmegaFlow's qualified build table; require the exact selected system-rc path absent, with errors and dangling links distinguished from absence. |
| Any mutable tag/source change invalidates prior inspection. | Changes to actual admitted state invalidate; irrelevant external references do not. | Moving a tag with the same retained image and changing original files after capture succeeds; actual final image/generation/command/env/identity/mount/endpoints change refuses before bootstrap. |
| Reploy needs a consumer image-dependency inventory. | Reploy protects image code/configuration and lookup namespaces; OmegaFlow inventories only its 18 payload files plus manifest. | Bash, sh, loader and image libraries remain Reploy-owned. Do not add a discovery/snapshot task to OmegaFlow. |
| All runtime paths are protected/immutable. | App data is live; `/run/omegaflow` remains writable tmpfs for sockets/FIFOs. | App data changes remain ordinary runtime behavior; none may shadow protected contents or required absence. |

## Governing sources and acceptance order

The older source is [OmegaFlow part-C C6.3](../../../../../omegaflow/docs/design/part-c-implementation-design.md),
with the historical reserved-workload locator
`src/omegaflow/conf/reploy/envoy/reserved/default.yaml` in that checkout. That
file is absent at checkpoint verification; this is a historical source reference,
not a verified current composition. Resolve the consumer's actual composition
before adoption; no replacement source is inferred here.
Those checkout links are context, not new design authorities or claims of changed
consumer files. The corrected normative request is retained in
[the requirements source](../omegaflow-rp-c6-requirements/consumer-request-and-evaluation.txt)
and [R4/R9 clarification](../omegaflow-rp-c6-requirements/consumer-clarifications.md).
The approved requirements and HLD govern this mapping.

1. Complete reviewed Reploy LLD and readiness proofs; implement only after its
   required approval and separate delivery authorization.
2. Qualify the complete public inspection → admission → launch path and refusal/
   cleanup behavior on the supported hosts, including WSL2/Desktop.
3. Select an explicit compatible Reploy release/source and let OmegaFlow update
   and review its bind-specific C6.3/composition checks through its own workflow.
4. OmegaFlow qualifies its three Bash compatibility profiles and supplied runtime
   against that selected release before claiming RP-C6 unblocked.

Endpoint publication is a separate dependency. This mapping does not resolve it
or restart OmegaFlow's paused campaign or Reploy milestone E.
