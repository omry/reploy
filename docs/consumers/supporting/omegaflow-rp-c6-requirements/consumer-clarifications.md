# OmegaFlow consumer clarifications

The maintainer relayed these OmegaFlow questions after the first clean
requirements draft. They refine the pending baseline; they are not requirements,
HLD, LLD, implementation or release approval. The earlier handoff and preserved
source remain historical evidence. Reploy records the clarification here;
OmegaFlow owns reconciliation of its own design documents.

## R4: facts suffice for the qualified Bash check

OmegaFlow checks Bash's qualified digest, resolved path/type and required
system-rc absence. It identified no additional need to export raw Bash or
system-rc bytes and stated that the earlier byte-export request could be removed.

The pending Reploy draft therefore requires bounded metadata/hash/absence evidence
instead. Reploy must compute the digest from the actual inspected file and bind
that evidence through admission/use. Missing, present, non-regular, link, error
and unsupported-size cases need explicit semantics; an inspection failure is not
absence. No raw file-export interface is required by this minimum delivery.

This removes a consumer interface requirement. It does not remove reading/hashing
files internally, expected-hash verification of supplied runtime payload, or the
requirement that the inspected executable is the one delivered for launch.

## R9: invalidate changes to the bound prepared state

OmegaFlow asked to distinguish actual admitted-state changes from movement of
external references that no longer affect the retained state. The same principle
already permits source changes after an independent protected capture.

The pending draft now makes these cases explicit:

- Original tag changes; the inspected immutable image and all other bound state
  remain retained and are used: the binding remains valid.
- Original supplied-file source changes; the independent verified protected copy
  and all other bound state remain retained and are used: the binding remains valid.
- Unprotected application data changes: ordinary runtime behavior/failure, not
  automatic invalidation of the protected capture.
- Reploy selects a different bound image, protected contents, relevant runtime
  facts or generation: reject the stale expected identity before bootstrap.
- A retained image/capture is missing or cannot be verified: refuse; do not
  re-resolve a mutable reference and silently substitute different state.

Integrated acceptance must exercise both continued validity and stale-state
refusal. Rejecting every external change would not satisfy this contract.

## Impact and remaining work

The R4 interface is smaller. R9 states the intended identity boundary more
precisely. Neither change implements or proves runtime protection/admission.
The preliminary estimate that included byte export is historical; re-estimate
from the actual HLD/LLD rather than claim a measured saving. The baseline still
needs explicit maintainer approval at its existing gate, followed by the saved
workflow's review/approval sequence.
