---
status: Active
updated: 2026-08-14
summary: Podman-gated security contract and acceptance ledger for future isolated multi-identity Linux workloads.
---

# Multi-Identity Workload Security Contract

## Status and Scope

This document defines the security properties Reploy must prove before it adds
support for an untrusted Linux-container workload that uses multiple
container-local identities. It normalizes
`migration/reploy-user-namespace-handover.md` from the Flux mail-stack
assessment at Flux commit `47470aad477e44ee0c222d917fd8ce05384aa1a1`.

The initial product boundary requires Podman for both planned mapping
profiles. Docker Engine is not an eligible backend for this capability, even
where daemon-level remapping can approximate part of the bounded-range profile.
No public blueprint or composition schema is defined, and no production runtime
behavior is implemented or authorized. One-off probes have established a
credible mechanism; repeated disposable conformance evidence remains required
before product implementation begins.

This contract covers the isolated subordinate-identity profile. A future
mixed host/container profile may need explicit bindings to selected host IDs.
That is a separate, higher-trust contract and is not prohibited or designed
here.

## Terms

**Workload**
: One independently isolated Linux-container application unit. A workload is
  not an entire future multi-workload composition.

**Workload installation**
: The installed deployment: Reploy's durable record for one installed copy of
  a workload. It survives restarts and generation changes and owns the
  workload's private mapping. This is not an installation of the Reploy
  program itself. A future composition-level blueprint may own or reference
  this record without changing its stability requirements.

**Generation**
: One immutable, image-locked revision of a workload installation. An update
  creates or selects a generation; it does not create a new workload identity
  or mapping by default.

**Declared identity**
: A container-local UID, GID, or supplementary group that the workload author
  says a trusted workload supervisor must assign to an application process. A
  later public declaration must include both the image-local name and numeric
  value. The declaration is configuration and locked-image evidence. Its role
  in the kernel boundary depends on the selected mapping profile.

**Runtime-required identity**
: A container-local UID or GID that Podman or the OCI runtime must map for
  container operation but that is not an application identity. Reploy must
  enumerate and justify these identities as part of the resolved execution
  plan. They are part of the kernel mapping and cannot be treated as absent;
  for example, the current rootless Podman probe required container GID 5 for
  the `devpts` mount.

**Trusted workload supervisor**
: The narrowly scoped workload process explicitly authorized to retain
  `CAP_SETUID` and `CAP_SETGID` and select application identities permitted by
  the workload's mapping profile. It may retain its reviewed capability set for
  the workload lifetime so that it can replace failed or exited children. It is
  trusted only for its declared control-plane duties inside its own workload.
  It receives no host identity or authority over another workload, and
  application children do not inherit its capabilities.

**Private mapping**
: The association between one workload installation's approved container IDs
  and isolated subordinate host IDs. It may be an exact mapping set or a
  bounded range. It is private to that installation, does not overlap another
  live or retained installation, and is stable across its ordinary lifecycle.

**Exact mapping profile**
: A sparse mapping containing only accepted declared identities and explicitly
  enumerated runtime-required identities. An unmapped container ID has no host
  mapping and the kernel rejects transitions to it.

**Bounded-range mapping profile**
: A contiguous, explicitly sized container-ID range mapped to a private
  subordinate host-ID range. Every ID in the range is kernel-usable by the
  trusted supervisor, so its locked policy—not the mapping alone—limits which
  application identities it assigns.

**Shared interface identity**
: A deliberately coordinated identity, or an independently enforceable
  equivalent access right, used only for a declared cross-workload interface
  such as a Unix socket. It is not permission to align or expose either
  workload's private mapping.

**Retained storage**
: Reploy-managed storage kept after a workload generation or installation is
  no longer active. Its ownership continues to reserve the associated mapping
  until the storage is removed or explicitly migrated.

**Delegation**
: Host-admin authorization that makes a bounded subordinate UID/GID facility
  or another narrowly scoped identity mechanism available for Reploy-managed
  workloads. A later design must choose the delegation recipient: an OS user,
  a Reploy installation identity, or a narrow broker. Delegation does not give
  the workload host root, a named host account, arbitrary host IDs, or general
  runtime authority.

**Revocation**
: Withdrawal of that delegated authority. Revocation prevents further
  workload operations but does not silently release mappings, rewrite
  ownership, delete retained data, or make another workload inherit the old
  authority.

## Threat Model

Application children, image-provided hooks other than an explicitly accepted
identity supervisor, and application-controlled files are untrusted. They may
attempt to:

- use identity-changing system calls, supplementary groups, set-ID files, or
  file capabilities to regain identity-changing authority after the supervisor
  drops it;
- escape the exact mapped set or bounded range selected for the workload;
- turn a container identity into host root or a named host account;
- access another workload's processes, private storage, or private mapping;
- exploit stale, overlapping, revoked, or prematurely reused allocations;
- exploit disagreement among the blueprint, locked image, host delegation,
  stored allocation, and persistent ownership;
- obtain broader authority through capabilities, mounts, devices, host
  namespaces, a container-runtime socket, or privileged mode; or
- trick trusted preparation code into following links, escaping a declared
  storage root, or running application code with elevated authority.

The host administrator, Reploy's trusted implementation, the selected resolved
blueprint, and the kernel/runtime enforcement mechanism are inside the trust
boundary. Selecting and accepting the blueprint is an operator and repository
trust decision; validating it against the image proves consistency, not
authorization. Image contents and application code cannot enlarge the accepted
mapping or change the selected profile. Merely placing an identity in an image
does not authorize it. Under the bounded-range profile, however, every mapped
in-range ID is kernel-usable by the trusted supervisor; that weaker boundary is
an explicit profile property, not exact identity allowlisting.

The protected assets are host authority, other workloads and their data,
stable persistent ownership, delegated subordinate-ID space, and the accuracy
of Reploy's installation state.

## Required Security Properties

1. **Profile-specific identity boundary.** Under the exact profile, the kernel
   permits only the accepted mapped set and rejects every unmapped identity.
   Under the bounded-range profile, the kernel permits any identity inside the
   explicit range and rejects every identity outside it; the trusted supervisor
   policy controls which in-range application identities are assigned. In both
   profiles, application children enter with normalized real, effective, and
   saved credentials, lose identity-changing capabilities before executing
   untrusted work, and cannot regain authority through set-ID executable files
   or file capabilities.
2. **No host identity.** Container root and every mapped identity resolve only
   to isolated subordinate host authority. They never become host root or an
   ordinary named host account.
3. **Per-installation isolation.** Two workload installations may use the same
   container-local IDs, but their private host mappings do not overlap and
   neither can access the other's private state.
4. **Stable lifecycle.** A workload installation retains its private mapping
   across restart, recreation, update, and rollback unless an explicit
   ownership migration is performed.
5. **Retention blocks reuse.** A mapping is not reusable while a live resource
   or retained Reploy-managed storage still carries its ownership.
6. **Fail closed without mutation.** Missing, revoked, overlapping, stale, or
   inconsistent delegation and identity state fails before workload start.
   Failure does not silently repair, rewrite, or delete persistent state.
7. **Default-zero capability control.** Reploy removes every Linux capability
   unless the workload explicitly opts into a reviewed capability. The trusted
   workload supervisor receives only the minimal allowlist required for its
   declared control-plane duties, and may retain that set to replace children.
   The bounding set contains no capability outside the allowlist; effective,
   permitted, inheritable, and ambient sets contain only the subset required at
   each execution phase. Application children have empty capability sets unless
   a separate, explicit declaration is later approved. Identity confinement
   does not imply capability approval, and retained capabilities cannot escape
   the selected mapping boundary.
8. **No elevated application phase.** Reploy does not solve preparation by
   running application-provided code with broader authority than the final
   workload. Trusted preparation remains descriptor-safe and confined to
   declared, unattached Reploy-managed storage.
9. **Narrow sharing only.** Cross-workload access is limited to an explicitly
   declared interface identity or equivalent access right. It never exposes a
   complete private mapping, general shared storage, or unrelated runtime
   paths.

## Decisions Frozen by This Contract

### Identity declarations

The workload author, not Reploy, identifies the application accounts required
by the image. A later public declaration will carry both image-local names and
numeric UID/GID values. Names and numeric values are mandatory locked-image
evidence, supervisor configuration, and diagnostics. Under the exact profile,
they contribute to the accepted sparse mapping set. Under the bounded-range
profile, they do not narrow the kernel mapping to an exact allowlist. Reploy
must reject a generation when the declared name-to-number relationship does
not match the locked image or the selected mapping profile.

Reploy separately derives and validates runtime-required identities. They must
be explicit in resolved state and diagnostics, remain minimal, and receive the
same subordinate-host and lifecycle isolation as declared identities.

This creates no corresponding named host accounts. The host observes only the
selected mechanism's private numeric identities.

### Mapping ownership

One workload installation owns one stable private mapping. Its generations
reference that mapping rather than receiving new mappings. Removal releases
the mapping only after all owned live resources and retained storage are gone
or their ownership has been explicitly migrated.

A future composition-level model may become the durable parent for workload
installations and shared interface declarations. For this isolated profile,
literal host IDs remain private Reploy allocation state rather than portable
blueprint values. Future explicit host-ID bindings for mixed systems require a
separate security design and validation path.

### Mapping profiles and backend boundary

Reploy will support two explicit mapping profiles:

- the exact profile maps only accepted declared and runtime-required IDs; and
- the bounded-range profile maps one explicitly sized private container range.

Neither profile is silently substituted for the other, and this contract does
not choose a default. Both initially require a positively identified Podman
backend. Reploy must reject either profile on Docker Engine before creating or
mutating workload resources, even if a particular Docker configuration can
provide daemon-wide or contiguous user-namespace remapping.

For both profiles, the mapping is private to one workload installation,
non-overlapping, and paired with normalized child credentials, verified
capability drop, and blocked privilege regain before untrusted application
execution. Podman-specific transport through a Docker-compatible API does not
make the feature a Docker Engine capability.

Capability declarations are opt-in. With no declaration, Reploy requests an
empty capability bounding set and supplies no effective, permitted,
inheritable, or ambient capabilities. A later public contract may expose a
conservative allowlist, but it must not inherit Podman defaults or define an
implicit Postfix-specific bundle.

### Evidence before product changes

Podman viability for each mapping profile must be proved before Reploy adds a
public schema or changes production runtime behavior. The conformance work may
use disposable probe programs and test harnesses, but it must not weaken the
contract or modify the product to make a candidate appear viable.

A candidate mechanism is viable only when at least three consecutive fresh
black-box runs and host-side inspection show all of the following. Each run
must create new probe containers and re-establish the two-workload isolation
case instead of reusing a successful live container:

- container root does not become host root;
- the trusted supervisor can perform every required UID, GID, and
  supplementary-group transition;
- under the exact profile, every unmapped transition fails and no unenumerated
  runtime identity appears in the mapping;
- under the bounded-range profile, representative other in-range transitions
  succeed and every out-of-range transition fails;
- capability-dropped application children cannot change identity, and neither
  set-ID nor file-capability execution can restore identity-changing authority;
- the no-capability default has empty bounding, effective, permitted,
  inheritable, and ambient sets, while an opted-in supervisor has no capability
  outside its exact reviewed allowlist;
- two workloads using the same container IDs receive different host authority
  and cannot access each other's private state;
- created files have the expected subordinate host ownership; and
- the workload has no privileged mode, host namespace, host device, broad host
  bind, or container-runtime socket; and
- Docker Engine rejects the capability before workload mutation rather than
  receiving Podman-specific mapping syntax.

The probe result must identify the exact runtime, host configuration, Reploy
head, probe source, commands, and repeated-run results. A one-off successful
start, runtime documentation, or inspection of a planned configuration is not
conformance evidence.

Passing this conformance probe permits product-integration design for the
already selected Podman backend. It does not by itself make the capability
supported. Production support additionally requires the later lifecycle,
allocation, storage, capability, preparation, and shared-interface evidence in
the ledger below through Reploy's supported interfaces.

## Acceptance Ledger

`Unproven` means the contract is approved but no implementation claim is made.
Later slices may refine how evidence is collected, but may not weaken a row
without an explicit security decision.

| ID | Required result | Owning milestone | Minimum evidence | Current state |
| --- | --- | --- | --- | --- |
| ID-01 | Every mapped identity has subordinate host authority only; container UID 0 is not host UID 0. | 1 | Host-side mapping inspection plus in-container identity probe for each profile. | Unproven; repeated conformance probe pending |
| ID-02 | The trusted supervisor can perform every required transition; the bounded-range profile additionally permits representative other in-range transitions. | 1 | Positive UID, primary-GID, and supplementary-group tests through the candidate supervisor capability profile. | Unproven; repeated conformance probe pending |
| ID-03 | Exact mappings reject every unmapped transition; bounded ranges reject every out-of-range transition; capability-dropped children cannot change identity or regain authority through set-ID or file-capability execution. | 1 | Profile-specific boundary and post-drop tests for `setuid`, `setreuid`, `setresuid`, `setfsuid`, `setgid`, `setregid`, `setresgid`, `setfsgid`, `setgroups`, set-user/group-ID execution, and file-capability execution. | Unproven; repeated conformance probe pending |
| ID-04 | Two installations using the same container IDs have distinct host mappings and cannot read each other's private state. | 1 | Concurrent two-workload isolation probe and host mapping inspection. | Unproven; repeated conformance probe pending |
| BE-01 | Both mapping profiles run only on a positively identified Podman backend; Docker Engine rejects them before workload mutation. | 1 | Backend detection plus positive Podman and negative Docker Engine integration tests. | Unproven; product integration not implemented |
| CAP-01 | Capability sets are empty by default; an opted-in workload control plane receives only its exact reviewed allowlist, and ordinary children receive none. | 1 and 2 | Inspect bounding, effective, permitted, inheritable, and ambient sets for the default, supervisor, and child processes; verify omitted capabilities fail. | Unproven; capability contract not implemented |
| LC-01 | Files created by declared identities have expected subordinate ownership and survive recreation. | 2 | Host ownership inspection before and after recreation through Reploy. | Unproven; deferred to lifecycle work |
| LC-02 | Update and rollback retain the allocation and persistent ownership. | 2 | Exact lifecycle tests across two generations and rollback. | Unproven; deferred to lifecycle work |
| LC-03 | Backup and restore preserve ownership or perform an explicit checked translation. | 2 | Checksummed offline export/import with no workload attached. | Unproven; deferred to storage work |
| AL-01 | Concurrent allocations cannot overlap, and mappings remain reserved while live or retained storage uses them. | 1 | Contended allocation, crash recovery, retention, removal, and safe-reuse tests. | Unproven; allocator not designed |
| RV-01 | Revocation makes ordinary operations fail closed without mutating retained storage or releasing its mapping. | 1 | Revocation and recovery tests with retained owned data. | Unproven; revocation not designed |
| HB-01 | Host bootstrap is explicit, auditable, idempotent, and enables later user-level operation without host root. | 1 | Repeated bootstrap, successful user operation, and absent/inconsistent preparation failures. | Unproven; bootstrap not designed |
| SB-01 | No workload uses privileged mode or gains host-root authority. | 1 | Runtime inspection plus negative host-authority tests. | Unproven; repeated conformance probe pending |
| SB-02 | Capabilities, `NoNewPrivs`, root-filesystem mutability, namespaces, devices, and mounts match declared policy, with no host control socket or undeclared path. | 1 and 2 | Host/runtime inspection and negative access tests through supported Reploy interfaces. | Unproven; baseline checked by one-off probe; repeated conformance pending |
| FS-01 | Trusted filesystem preparation cannot follow symlinks, accept unexpected object types, escape the declared root, or recursively rewrite application data. | 2 | Adversarial descriptor-safe preparation tests. | Unproven; deferred to filesystem design |
| CF-01 | Disagreement among declarations, locked image, delegation, allocation, and persistent ownership fails before start without state mutation. | 1 and 2 | Deliberate mismatch matrix with before/after state evidence. | Unproven; initial cases required in milestone 1 |
| SI-01 | Only declared producers and consumers can use a shared Unix socket across otherwise private mappings. | 3 | Positive participant test and negative undeclared-workload test. | Unproven; shared interface not designed |
| SI-02 | Participants see only the declared interface path, and private mappings and data remain isolated. | 3 | Mount, traversal, connection, and private-data visibility tests. | Unproven; shared interface not designed |
| SI-03 | Stale sockets and producer restart are reconciled safely without traversing persistent application data. | 3 | Adversarial stale-object and restart tests. | Unproven; shared interface not designed |

## Required Sequence

1. Freeze this Podman-only, dual-profile contract.
2. Run repeated disposable conformance probes for both mapping profiles without
   changing public schema or production runtime behavior.
3. Stop with exact evidence for an implementation-authorization decision.
4. Only after explicit approval, design backend detection, host delegation,
   durable allocation,
   runtime integration, and later public declarations in separate review
   units.

If Podman cannot prove either profile and the shared capability-drop boundary
at acceptable cost, that profile is blocked. Reploy must not substitute Docker
Engine, a privileged container, overlapping or host-aligned mappings, or an
unconfined application process and describe it as equivalent isolation.
