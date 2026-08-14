---
status: Active
updated: 2026-08-14
summary: Backend-neutral security contract and acceptance ledger for future isolated multi-identity Linux workloads.
---

# Multi-Identity Workload Security Contract

## Status and Scope

This document defines the security properties Reploy must prove before it adds
support for an untrusted Linux-container workload that uses multiple
container-local identities. It normalizes
`migration/reploy-user-namespace-handover.md` from the Flux mail-stack
assessment at Flux commit `47470aad477e44ee0c222d917fd8ce05384aa1a1`.

The contract is approved as the basis for a backend feasibility probe. No
backend has been selected, no public blueprint or composition schema is
defined, and no production runtime behavior is implemented or authorized.
The next step is a disposable probe against candidate runtime mechanisms. A
product change may begin only after that probe demonstrates a viable mechanism
and the backend choice passes an explicit review gate.

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
  says the workload must be able to assume. A later public declaration must
  include both the image-local name and numeric value. Linux authorization is
  based on the numeric value; the name is locked image evidence that Reploy
  verifies rather than a host account Reploy creates.

**Private mapping**
: The association between one workload installation's declared container IDs
  and isolated subordinate host IDs. It is private to that installation and
  stable across its ordinary lifecycle.

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

Application processes, image-provided hooks, and application-controlled files
are untrusted. They may attempt to:

- use identity-changing system calls, supplementary groups, or set-ID files to
  assume an undeclared identity;
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
authorization. Image contents and application code cannot add identities to
the accepted policy. Merely placing an identity in an image does not authorize
it.

The protected assets are host authority, other workloads and their data,
stable persistent ownership, delegated subordinate-ID space, and the accuracy
of Reploy's installation state.

## Required Security Properties

1. **Exact identity boundary.** A workload can assume only its declared
   container-local UIDs, GIDs, and supplementary groups. Every other identity
   transition is unusable, including transitions through set-ID executable
   files.
2. **No host identity.** Container root and every declared application
   identity map only to isolated subordinate host authority. They never become
   host root or an ordinary named host account.
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
7. **Independent capability control.** Any retained Linux capabilities are
   separately declared and narrowly allowlisted. Identity confinement does
   not imply capability approval, and capabilities cannot escape the declared
   identity boundary.
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
numeric UID/GID values. Numeric values are the authorization identity; names
are mandatory locked-image evidence and diagnostics. Reploy must reject a
generation when the declared name-to-number relationship does not match the
locked image.

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

### Enforcement mechanism

The contract selects a property, not a backend: only declared identities are
usable. Exact sparse UID/GID mappings are the preferred candidate because they
express that property directly. Another mechanism is admissible only if it
independently enforces the same boundary in the kernel/runtime and passes the
same negative tests. Mapping a broad private range and relying on application
cooperation is not equivalent.

### Evidence before product changes

Backend viability must be proved before Reploy adds a public schema or changes
production runtime behavior. The feasibility work may use disposable probe
programs and test harnesses, but it must not weaken the contract or modify the
product to make a candidate appear viable.

A candidate mechanism is viable only when at least three consecutive fresh
black-box runs and host-side inspection show all of the following. Each run
must create new probe containers and re-establish the two-workload isolation
case instead of reusing a successful live container:

- container root does not become host root;
- every declared UID, GID, and supplementary-group transition succeeds;
- undeclared UID/GID/group transitions and set-ID execution fail;
- two workloads using the same container IDs receive different host authority
  and cannot access each other's private state;
- created files have the expected subordinate host ownership; and
- the workload has no privileged mode, host namespace, host device, broad host
  bind, or container-runtime socket.

The probe result must identify the exact runtime, host configuration, Reploy
head, probe source, commands, and repeated-run results. A one-off successful
start, runtime documentation, or inspection of a planned configuration is not
conformance evidence.

Passing this feasibility probe permits a backend selection decision. It does
not by itself make the backend supported. Supported conformance additionally
requires the later lifecycle, allocation, storage, capability, preparation,
and shared-interface evidence in the ledger below through Reploy's supported
interfaces.

## Acceptance Ledger

`Unproven` means the contract is approved but no implementation claim is made.
Later slices may refine how evidence is collected, but may not weaken a row
without an explicit security decision.

| ID | Required result | Owning milestone | Minimum evidence | Current state |
| --- | --- | --- | --- | --- |
| ID-01 | Every declared identity has subordinate host authority only; container UID 0 is not host UID 0. | 1 | Host-side mapping inspection plus in-container identity probe. | Unproven; feasibility probe pending |
| ID-02 | Declared UID, primary-GID, and supplementary-group transitions succeed. | 1 | Positive tests through the candidate capability profile. | Unproven; feasibility probe pending |
| ID-03 | Every undeclared identity transition fails. | 1 | Negative tests for `setuid`, `setreuid`, `setresuid`, `setfsuid`, `setgid`, `setregid`, `setresgid`, `setfsgid`, `setgroups`, and set-user/group-ID execution. | Unproven; feasibility probe pending |
| ID-04 | Two installations using the same container IDs have distinct host mappings and cannot read each other's private state. | 1 | Concurrent two-workload isolation probe and host mapping inspection. | Unproven; feasibility probe pending |
| LC-01 | Files created by declared identities have expected subordinate ownership and survive recreation. | 2 | Host ownership inspection before and after recreation through Reploy. | Unproven; deferred to lifecycle work |
| LC-02 | Update and rollback retain the allocation and persistent ownership. | 2 | Exact lifecycle tests across two generations and rollback. | Unproven; deferred to lifecycle work |
| LC-03 | Backup and restore preserve ownership or perform an explicit checked translation. | 2 | Checksummed offline export/import with no workload attached. | Unproven; deferred to storage work |
| AL-01 | Concurrent allocations cannot overlap, and mappings remain reserved while live or retained storage uses them. | 1 | Contended allocation, crash recovery, retention, removal, and safe-reuse tests. | Unproven; allocator not designed |
| RV-01 | Revocation makes ordinary operations fail closed without mutating retained storage or releasing its mapping. | 1 | Revocation and recovery tests with retained owned data. | Unproven; revocation not designed |
| HB-01 | Host bootstrap is explicit, auditable, idempotent, and enables later user-level operation without host root. | 1 | Repeated bootstrap, successful user operation, and absent/inconsistent preparation failures. | Unproven; bootstrap not designed |
| SB-01 | No workload uses privileged mode or gains host-root authority. | 1 | Runtime inspection plus negative host-authority tests. | Unproven; feasibility probe pending |
| SB-02 | Capabilities, `NoNewPrivs`, root-filesystem mutability, namespaces, devices, and mounts match declared policy, with no host control socket or undeclared path. | 1 and 2 | Host/runtime inspection and negative access tests through supported Reploy interfaces. | Unproven; baseline checked by feasibility probe |
| FS-01 | Trusted filesystem preparation cannot follow symlinks, accept unexpected object types, escape the declared root, or recursively rewrite application data. | 2 | Adversarial descriptor-safe preparation tests. | Unproven; deferred to filesystem design |
| CF-01 | Disagreement among declarations, locked image, delegation, allocation, and persistent ownership fails before start without state mutation. | 1 and 2 | Deliberate mismatch matrix with before/after state evidence. | Unproven; initial cases required in milestone 1 |
| SI-01 | Only declared producers and consumers can use a shared Unix socket across otherwise private mappings. | 3 | Positive participant test and negative undeclared-workload test. | Unproven; shared interface not designed |
| SI-02 | Participants see only the declared interface path, and private mappings and data remain isolated. | 3 | Mount, traversal, connection, and private-data visibility tests. | Unproven; shared interface not designed |
| SI-03 | Stale sockets and producer restart are reconciled safely without traversing persistent application data. | 3 | Adversarial stale-object and restart tests. | Unproven; shared interface not designed |

## Required Sequence

1. Freeze this backend-neutral contract.
2. Run the disposable backend feasibility probe without changing public schema
   or production runtime behavior.
3. Stop with exact evidence for a backend-selection decision.
4. Only after explicit approval, design host delegation, durable allocation,
   runtime integration, and later public declarations in separate review
   units.

If no candidate can prove the exact identity boundary at acceptable cost, the
result is a documented blocker. Reploy must not substitute a privileged
container, a broad identity range, or application cooperation and describe it
as equivalent isolation.
