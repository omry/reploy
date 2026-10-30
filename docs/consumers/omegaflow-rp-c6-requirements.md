# OmegaFlow RP-C6 requirements

Approved requirements baseline. This document states the consumer outcome and
constraints; it does not select a public API or claim implementation readiness.

## Outcome

OmegaFlow can inspect the exact workload Reploy will launch, evaluate its own
policy, and require that same prepared state at controlled-session admission.
Reploy rejects relevant mismatches before workload or bootstrap execution.
The delivery must satisfy the full requirements below; reducing an implementation
estimate does not authorize dropping an obligation.

## Ownership and scope

Reploy owns generic preparation facts, workload-image delivery, protected supplied
inputs, launch admission and resource cleanup. Extend its existing implementation,
preparation, immutable-image selection, build verification, inspection and session
machinery. Do not introduce a parallel image selector, inspection daemon, receipt
authority or build pipeline.

OmegaFlow defines its blueprint and owns reserved-environment policy, qualified
Bash compatibility checks, supplied runtime contents, mount acceptance policy and
recording behavior. It does not enumerate or snapshot files supplied by the image,
including Bash, `/bin/sh`, loaders and libraries. Reploy delivers and enforces
those image contents.

The initial consumer workload is Linux/amd64 with the consumer's qualified Bash
profiles: Debian 12/5.2.15, Debian 13/5.2.37 and Ubuntu 26.04/5.3.9. Qualification
must identify the exact image/build tuple. An ARM64 artifact or controller build
does not establish ARM64 workload qualification. The design must state the exact
supported engine/host cohort and reject unsupported cases rather than silently
weaken a guarantee.

## Required behavior

### R1. Select and retain the image used for preparation

Expose the selected image's immutable identity, platform and inherited environment
through public preparation machinery before OmegaFlow adds reserved composition
values. Permit OmegaFlow to evaluate that image plus application environment.
Final preparation must use that same selected image. If it selects a different
image, reject and require a fresh preparation view; do not silently rewrite the
consumer's retained YAML or reuse checks for another image.

### R2. Keep environment sources observable and private values private

Distinguish image-inherited, application and final effective environment facts.
Overriding a value during composition must not erase inherited facts needed for
policy evaluation. Report uninspectable required facts as failure rather than
silently omitting them. Do not disclose configured private environment values in
reports, identities or diagnostics. This requirement does not add private
environment injection to controlled sessions.

### R3. Inspect the actual prepared workload without starting it

Expose the prepared generation's final image/platform, effective environment,
numeric execution identity, effective mounts and declared endpoint bindings.
Mount facts include targets and read/write/tmpfs semantics. Inspection must not
start workload/bootstrap code, execute untrusted workload code or grant the
controller container-engine access.

### R4. Provide bounded executable and required-absence evidence

Expose the resolved `/bin/bash` path, file type and SHA-256 digest needed by the
consumer's qualified-build check. Compute that digest from the actual inspected
file and bind the evidence to the prepared state that will be admitted. Report
the selected system-rc path's absence, or its presence/type if present. OmegaFlow's
current profiles require `/etc/bash.bashrc` to be absent. Raw Bash and system-rc
file-byte export is not required for this delivery.

Specify safe path resolution, symbolic-link handling, regular/non-regular file
behavior, required-absence semantics and errors. Bound requests, responses and
file hashing. An unreadable, uninspectable or over-limit file must produce an
explicit failure, not an absence claim. Do not mistake hash equality for proof
of path, type, mode, absence or mount integrity.

### R5. Capture supplied runtime files against expected hashes

Accept a complete bounded inventory of consumer-supplied runtime files with
expected SHA-256 values. The supplied manifest's expected digest is bound
separately; an attacker-controlled replacement manifest cannot supply its own
expectations. Verify the bytes actually retained against those expectations.
Computing fresh observed hashes and treating them as expected values is
insufficient.

The current consumer inventory contains 18 payload files plus `manifest.json`,
approximately 7.9 MiB total:

- `bin/envoy` and `bin/awsh`; helper commands use the same Awsh executable;
- `etc/awsh-bashrc`, `etc/inputrc` and `etc/bash-builds.json`;
- one terminal database entry and 12 locale files; and
- `manifest.json`, with its expected digest supplied separately.

These inventory and size facts are consumer inputs, not independently measured
qualification evidence. Preserve the declared paths under `/omegaflow-runtime`.
The supplied files have no symbolic-link or hardlink aliases. Deliver directories
and executables as `0555` and data files as `0444`. The workload needs read/execute
access; retaining the source owner's numeric UID/GID is unnecessary. The adapter
rcfile sources no additional startup files.

### R6. Protect the retained payload through admission and use

Prevent source mutation after verification, workload writes and mount shadowing
from replacing protected supplied contents. File permissions alone do not
establish that boundary. A verified independent protected copy is acceptable;
the design need not introduce a general filesystem snapshot service.

After such a capture exists, later changes to the original source need not
invalidate it. Set explicit capture size limits that support the required
inventory, and reject unsupported sizes instead of weakening protection.

### R7. Keep application data and session resources live

Videos and other unprotected application data may remain live. Mutation,
disappearance and processing errors are ordinary runtime failures. Do not require
copying or hashing arbitrary large application inputs merely to prevent those
errors. `/run/omegaflow` remains writable for live sockets and FIFOs; these are not
protected startup payload.

### R8. Bind inspected facts to public launch admission

Return one public immutable identity that binds the relevant prepared facts and
protected capture. Accept the expected identity at the existing controlled-session
entry. Validate it against Reploy's actual selected state under existing admission
ownership before bootstrap starts. A caller-written report is not launch authority.
Pinning only the image is insufficient.

Keep private internal generation/build/plan digests out of the public session
stream as required by the existing session contract. The design must define the
public binding's representation, lifetime, replay and invalidation rules without
creating a second launch-authorization system.

### R9. Reject stale or malformed state before effects

Invalidate the expected identity when the state actually selected for admission
differs from the state bound by inspection. This includes changes to the selected
immutable image, inspected executable/image contents, protected payload, bound
blueprint/environment/mount facts or selected generation. Prevent a relevant
replacement race between successful validation and use.

Changes to external references that no longer affect that retained state do not
by themselves invalidate it. If an original mutable image tag moves while Reploy
retains and launches the same inspected immutable image, the binding remains
valid. Likewise, changes to the original supplied-file source after an independent
verified protected capture, or to unprotected application data, need not
invalidate that capture or binding.

Admission must use the retained bound image and capture, not re-resolve a mutable
reference and silently select a replacement. If the required retained state is
unavailable or cannot be verified, refuse rather than fall back to different
contents. A rebuild or generation replacement that selects a different bound
state still requires a new inspection identity.

Reject malformed, oversized, unknown-version and forged identities. Define public
schema versions, limits and error behavior. Each stale-state refusal must prove
workload/bootstrap execution did not begin.

### R10. Preserve cancellation, ownership and cleanup

Reuse existing session ownership and resource cleanup. Interrupted inspection or
admission must not leave an active workload or leaked reservation. Define retained
capture reachability, cancellation and concurrent replacement behavior. Preserve
the distinction between preparation evidence, admission ownership and runtime
session resources.

### R11. Qualify the integrated public path

Retain public-interface tests for nominal inspection and launch, forbidden
inherited environment, qualified Bash and system-rc evidence, mount/identity facts,
protected payload/manifest substitution, capture races, size limits, malformed and
stale identities, concurrent rebuild/replacement and interrupted cleanup. Include
positive launch controls showing that original-tag movement with the same retained
immutable image, and original-source changes after protected capture, do not
invalidate unchanged admitted state. Pair them with negative controls for actual
bound-state changes. Every relevant refusal must verify that bootstrap did not
start. Exercise the public
inspection and launch interfaces together; isolated internal helper tests do not
establish consumer acceptance.

Retain the exact release/source identity, public contract and native evidence.
OmegaFlow must qualify against an explicitly selected compatible release before
the consumer is reported unblocked. A reviewed design or approved implementation
PR alone is insufficient.

## Boundaries and dependencies

Optional host publication for private endpoints is a separate dependency. Link
its existing backlog item when identified; do not duplicate it. RP-C6 does not
alone establish all OmegaFlow acceptance, and this work does not recover the
OmegaFlow campaign or adopt a release for it.

The delivery does not activate mapped execution, implement remaining multi-identity
E slices, introduce hostile-host-root containment or strengthen the existing
same-identity isolation guarantee. New platform/engine qualification requires its
own explicit evidence. Unsupported cases must fail closed.

## Supporting material

- [Consumer source and earlier evaluation](supporting/omegaflow-rp-c6-requirements/consumer-request-and-evaluation.txt)
- [Consumer clarification of inspection and invalidation](supporting/omegaflow-rp-c6-requirements/consumer-clarifications.md)
- [Document and estimation contract](supporting/omegaflow-rp-c6-requirements/design-contract.md)
- [Engineering follow-up from the interview](supporting/omegaflow-rp-c6-requirements/interview-synthesis.md)
- [Workflow feedback](supporting/omegaflow-rp-c6-requirements/design-workflow-feedback.md)
