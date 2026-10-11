---
status: Active
updated: 2026-08-15
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

Resource-limit values, defaults, supported-host requirements, enforcement,
and conformance are not defined by this identity contract. Multi-identity
workloads inherit the ordinary application-runtime resource policy and must
not weaken it. Resource-limit design and conformance remain a separate later
slice; milestone 1B does not test limits that have not been defined.

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

**Supervisor identity**
: The explicitly enumerated container-local UID and GID used only by the
  trusted workload supervisor. It is an accepted mapped control-plane identity
  in the resolved execution plan, not a declared application identity or
  application authority. Its mapping is private to the workload installation
  and follows the same delegation, collision, and lifecycle rules as every
  other mapped identity.

**Trusted workload supervisor**
: The narrowly scoped workload process explicitly authorized to retain
  `CAP_SETUID` and `CAP_SETGID` and select only accepted declared application
  identities. A mapped runtime-required or otherwise undeclared identity is not
  application authority. The supervisor may retain its reviewed capability set
  for the workload lifetime so that it can replace failed or exited children.
  It is trusted only for its declared control-plane duties inside its own
  workload. It receives no host identity or authority over another workload,
  and application children do not inherit its capabilities. Its real,
  effective, saved-set, and filesystem UIDs and GIDs all use the dedicated
  supervisor UID and GID that no application child uses. Its final
  supplementary-group vector is empty or contains only that dedicated
  supervisor GID. It remains non-dumpable while it retains capabilities and
  occupies a POSIX session distinct from every application child so that the
  same-session `SIGCONT` exception cannot bypass its identity boundary. It
  cannot be signaled, inspected, or controlled by application children.

**Private mapping**
: The association between one workload installation's approved container IDs
  and isolated subordinate host IDs. It may be an exact mapping set or a
  bounded range. It is private to that installation, does not overlap another
  live or retained installation, and is stable across its ordinary lifecycle.

**Dormant identity assignment**
: A container-ID-to-host-ID association retained by a workload installation
  after that application identity leaves the current generation. The exact
  profile does not place a dormant identity in the active mapping, but Reploy
  does not give its host ID to another installation. Reintroducing the same
  numeric container ID, including through rollback, reactivates the same
  association.

**Exact mapping profile**
: A sparse mapping containing only accepted declared identities, the explicitly
  enumerated supervisor identity, and explicitly enumerated runtime-required
  identities. An unmapped container ID has no host mapping and the kernel
  rejects transitions to it.

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
  runtime authority. The delegated host IDs must be exclusive to that
  recipient: they must not alias any local or NSS-provided host principal or
  overlap a subordinate UID/GID range delegated to another host principal. If
  host preparation cannot authoritatively establish that exclusivity, Reploy
  must fail closed rather than use the range.

**Revocation**
: Withdrawal of that delegated authority. Revocation is not complete while a
  workload still exercises the delegated authority: Reploy must stop and
  remove its live runtime resources and prevent service-manager or other
  autonomous restart before reporting success. Revocation prevents further
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
- inspect or take control of the capability-bearing supervisor from an
  application child;
- escape the exact mapped set or bounded range selected for the workload;
- turn a container identity into host root, a local or NSS-provided host
  principal, or subordinate authority delegated to another host principal;
- access another workload's processes, IPC objects, private storage, or private
  mapping;
- exploit stale, overlapping, revoked, or prematurely reused allocations;
- exploit disagreement among the blueprint, locked image, host delegation,
  stored allocation, and persistent ownership;
- obtain broader authority through capabilities, mounts, devices, host
  namespaces, a container-runtime socket, or privileged mode; or
- communicate with another workload through a shared network namespace,
  including through Linux abstract Unix sockets; or
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
   permits only the accepted declared identities, supervisor identity, and
   runtime-required identities, and rejects every unmapped identity.
   Under the bounded-range profile, the kernel permits any identity inside the
   explicit range and rejects every identity outside it; the trusted supervisor
   policy controls which in-range application identities are assigned. In both
   profiles, application children enter with real, effective, saved-set, and
   filesystem UIDs and GIDs all normalized to their declared application
   identities and a supplementary-group vector containing exactly their
   declared groups, lose identity-changing capabilities before executing
   untrusted work, and cannot regain authority through set-ID executable files
   or file capabilities. Privileged, runtime-required, and otherwise
   undeclared supplementary groups are not inherited. Before untrusted exec,
   a `close_range`-based or equivalently complete boundary closes every file
   descriptor except explicitly constructed child standard streams and
   declared application descriptors whose authority is valid for the child's
   final identity;
   supervisor control, socket, pidfd, and private-data descriptors are never
   inherited. No application child shares any of the supervisor's real,
   effective, saved-set, or filesystem UIDs; the supervisor remains
   non-dumpable while it retains capabilities, occupies a POSIX session
   distinct from every application child, and denies child signaling and access
   through `ptrace`, `/proc/<pid>/mem`, and equivalent process-memory
   interfaces.
2. **No host identity or authority alias.** Container root and every mapped
   identity resolve only to exclusively delegated subordinate host authority.
   They never become host root, a local or NSS-provided host principal, or an
   ID inside a subordinate range delegated to another host principal. A
   collision, or inability to prove the host identity and delegation inventory
   is complete enough to exclude one, fails before workload start.
3. **Per-installation isolation.** Two workload installations may use the same
   container-local IDs, but their private host mappings do not overlap and
   they use distinct PID, IPC, network, and mount namespaces with private mount
   propagation; neither can access the other's processes, IPC objects, mounts,
   private state, or network-namespace-scoped interfaces.
4. **Stable lifecycle.** A workload installation retains its mapping profile,
   supervisor identity, bounded-range geometry when selected, and established
   container-ID-to-host-ID associations across restart, recreation, update,
   and rollback unless an explicit ownership migration is performed. Its
   application identity declarations may evolve by generation. Under the exact
   profile, identities absent from the current generation are omitted from the
   active mapping but their assignments remain dormant and reserved. Under the
   bounded profile, the supervisor admits only the current generation's
   declared identities even though the fixed range remains mapped.
5. **Retention blocks reuse.** A mapping is not reusable while a live resource
   or retained Reploy-managed storage still carries its ownership. The initial
   profiles reject every external `bind` mount. A read-only external directory
   can expose active objects or nested mounts, while a writable one can leave
   subordinate-owned files outside Reploy's retention accounting. A later
   contract may allow read-only inputs through filtered materialization and
   writable inputs only through an explicit reservation and migration boundary.
6. **Fail closed without mutation.** Missing, revoked, overlapping, stale, or
   inconsistent delegation and identity state fails before workload start.
   Failure does not silently repair, rewrite, or delete persistent state. A
   supported revocation operation also removes active workload resources and
   prevents autonomous restart before it reports success, while retained
   ownership continues to reserve the mapping.
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
   paths. Until the shared-interface contract is implemented, workloads have
   no shared network namespace or peer-reachable network attachment and cannot
   connect to each other's TCP, UDP, or Linux abstract Unix-socket services.

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

Reploy also resolves exactly one supervisor identity for the workload. It is
explicit in resolved state, diagnostics, and the kernel mapping, is disjoint
from every declared application identity, and is usable only by the trusted
workload supervisor.

This creates no corresponding named host accounts. The host observes only the
selected mechanism's private numeric identities.

Application declarations may add or remove identities between generations. A
name that moves to a different numeric UID or GID is the removal of the old
identity and addition of a new one. Reploy validates each generation against
its locked image and configures the supervisor from that generation's accepted
set; it does not require every generation to declare the same application
identities.

Reploy does not scan, rewrite, `chown`, or otherwise migrate persistent files
when declarations change. Files owned by a removed identity keep their old
numeric subordinate UID and GID. They may consequently be inaccessible or
appear under an unmapped overflow identity to the current generation. Cleanup
or ownership repair is the operator's responsibility. Reusing the same numeric
container UID or GID within the installation, whether by a later generation or
rollback, restores the ordinary Unix access implied by that ownership; an
image-local account name does not grant or revoke filesystem authority.

### Mapping ownership

One workload installation owns one stable private mapping. Its generations
reference that mapping rather than receiving new mappings. Removal releases
the mapping only after all owned live resources and retained storage are gone
or their ownership has been explicitly migrated.

For the exact profile, the active mapping is generated from the current
generation, but every previously established identity assignment remains part
of the installation's reserved allocation while the installation or its
retained storage exists. A newly declared numeric identity receives a new
non-overlapping assignment; removing it makes that assignment dormant rather
than reusable. The bounded profile keeps its fixed private range while the
generation-specific supervisor policy changes. Reploy conservatively reserves
the installation's allocation based on retained managed storage; it does not
need to discover or repair every stale owner in that storage.

Changing the mapping profile, supervisor identity, or bounded-range geometry
is not an ordinary generation update. It requires a future explicit migration
or installation replacement operation and otherwise fails before mutation.

Initial multi-identity workloads use only image content and Reploy-managed
storage that participates in this retention accounting. Every external `bind`
mount fails before workload mutation. A future input contract may admit a
filtered copy that contains only accepted inert filesystem objects rather than
exposing a live external directory.

A future composition-level model may become the durable parent for workload
installations and shared interface declarations. For this isolated profile,
literal host IDs remain private Reploy allocation state rather than portable
blueprint values. Future explicit host-ID bindings for mixed systems require a
separate security design and validation path.

### Mapping profiles and backend boundary

Reploy will support two explicit mapping profiles:

- the exact profile maps only accepted declared IDs, the accepted supervisor
  identity, and runtime-required IDs; and
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

- every selected host UID and GID belongs exclusively to the probe's delegated
  subordinate range, does not collide with a local or NSS-provided host
  principal or any subordinate range delegated to another host principal, and
  container root does not become host root;
- the resolved plan, diagnostics, and kernel mapping contain exactly one
  dedicated supervisor UID and GID, disjoint from every declared application
  identity, and no application child can assume either identity;
- the trusted supervisor can perform every required declared UID, GID, and
  supplementary-group transition and rejects every mapped-but-undeclared
  application identity, including runtime-required identities that are not
  also declared;
- every application child's final supplementary-group vector contains exactly
  its declared groups, with no inherited privileged, runtime-required, or
  otherwise undeclared group;
- all four fields in every application child's `/proc/<pid>/status` `Uid:` and
  `Gid:` entries match its declared application UID and GID before untrusted
  execution;
- under the exact profile, every unmapped transition fails and no unenumerated
  supervisor or runtime-required identity appears in the mapping;
- under the bounded-range profile, a raw mechanism probe kept separate from
  the production supervisor confirms that representative other in-range
  transitions succeed and every out-of-range transition fails;
- a child probe at its first untrusted instruction sees exactly its declared
  descriptor allowlist, while adversarial non-`CLOEXEC` supervisor control-file,
  socket, pidfd, and private-data canaries are absent;
- capability-dropped application children cannot change identity, and neither
  set-ID nor file-capability execution can restore identity-changing authority;
- all four fields in the capability-bearing supervisor's
  `/proc/<pid>/status` `Uid:` and `Gid:` entries use its dedicated supervisor
  UID and GID, distinct from every application child; its final `Groups:`
  vector is empty or contains only that dedicated supervisor GID; the
  supervisor reports `PR_GET_DUMPABLE` as zero while it retains capabilities;
  its POSIX session ID differs from every application child's session ID; and
  negative same-workload probes, including explicit `SIGCONT` attempts, cannot
  use `kill`, `pidfd_send_signal`, `ptrace`, `/proc/<pid>/mem`,
  `process_vm_readv`, or `process_vm_writev` against it from an application
  child;
- the no-capability default has empty bounding, effective, permitted,
  inheritable, and ambient sets, while an opted-in supervisor has no capability
  outside its exact reviewed allowlist;
- the approved seccomp profile is explicitly selected and identifiable in the
  resolved and effective runtime policy, application processes report
  `NoNewPrivs: 1` and seccomp filter mode (`Seccomp: 2`), negative probes for
  syscalls the approved profile must block fail, and the container root
  filesystem is read-only except for declared writable storage;
- two workloads using the same container IDs receive different host authority,
  run in separate PID, IPC, network, and mount namespaces with private mount
  propagation, cannot see each other's mounts, cannot observe, signal, or
  ptrace each other's processes, cannot use each other's System V
  shared-memory, semaphore, or message-queue objects, use private `/dev/shm`
  and `/dev/mqueue` mounts, cannot open each other's named POSIX shared-memory,
  semaphore, or message-queue objects, and cannot access each other's private
  state;
- the workloads have no peer-reachable network attachment, and negative TCP
  and UDP probes and a negative Linux abstract Unix-socket connection probe
  cannot reach the other workload;
- created files have the expected subordinate host ownership; and
- the workload has no privileged mode, host namespace, host device, external
  `bind` mount, or container-runtime socket; and
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
| ID-01 | Every mapped identity, including the dedicated supervisor identity, has exclusively delegated subordinate host authority only; it does not alias host root, a local or NSS-provided host principal, or a subordinate range delegated to another host principal. | 1 | Resolved-state and host-side mapping inspection for exactly one dedicated supervisor UID/GID plus authoritative collision checks against the host identity inventory and every other subordinate UID/GID delegation, explicit collision-failure cases, and an in-container identity probe for each profile. | Unproven; repeated conformance probe pending |
| ID-02 | The trusted supervisor can perform every required declared transition and rejects mapped-but-undeclared UIDs, GIDs, and supplementary groups; every application child's real, effective, saved-set, and filesystem UIDs and GIDs match its declared application identities, and its final supplementary-group vector contains exactly its declared groups; a separate raw bounded-range mechanism probe permits representative other in-range transitions. | 1 | Positive declared-identity and negative mapped-but-undeclared tests through the production supervisor policy, all four `/proc/<pid>/status` `Uid:` and `Gid:` fields matching the declared application IDs, final `Groups:` inspection that excludes inherited privileged, runtime-required, and otherwise undeclared groups, plus an independently identified raw range-mechanism probe. | Unproven; repeated conformance probe pending |
| ID-03 | Exact mappings reject every unmapped transition; bounded ranges reject every out-of-range transition; capability-dropped children cannot change identity, inherit supervisor or undeclared descriptors, regain authority through set-ID or file-capability execution, or signal, inspect, or control the capability-bearing supervisor. | 1 | Profile-specific boundary and post-drop tests for `setuid`, `setreuid`, `setresuid`, `setfsuid`, `setgid`, `setregid`, `setresgid`, `setfsgid`, `setgroups`, set-user/group-ID execution, and file-capability execution; first-instruction child descriptor enumeration against an exact allowlist plus adversarial non-`CLOEXEC` supervisor control-file, socket, pidfd, and private-data canaries; all four supervisor `Uid:` and `Gid:` fields using its dedicated child-disjoint UID and GID; a final supervisor `Groups:` vector containing no GID other than its dedicated GID; a supervisor `PR_GET_DUMPABLE` result of zero; supervisor/child POSIX session-ID inspection proving separation; and negative same-workload child-to-supervisor `kill` and `pidfd_send_signal` probes that explicitly include `SIGCONT`, plus negative `ptrace`, `/proc/<pid>/mem`, `process_vm_readv`, and `process_vm_writev` probes. | Unproven; repeated conformance probe pending |
| ID-04 | Two installations using the same container IDs have distinct host mappings, PID, IPC, network, and mount namespaces, private mount propagation, and private `/dev/shm` and `/dev/mqueue` mounts; they cannot observe, signal, or ptrace each other's processes, see each other's mounts, use each other's System V or named POSIX shared-memory, semaphore, or message-queue objects, connect to each other's Linux abstract Unix sockets, or read each other's private state. | 1 | Concurrent two-workload probe with host mapping, PID-namespace, IPC-namespace, network-namespace, mount-namespace, mount-propagation, `/dev/shm`, and `/dev/mqueue` inspection plus negative cross-workload mount visibility, process visibility, signaling, ptrace, System V IPC, POSIX `shm_open`/`sem_open`, POSIX `mq_open`/send/receive, abstract Unix-socket connection, and private-state access tests. | Unproven; repeated conformance probe pending |
| BE-01 | Both mapping profiles run only on a positively identified Podman backend; Docker Engine rejects them before workload mutation. | 1 | Backend detection plus positive Podman and negative Docker Engine integration tests. | Unproven; product integration not implemented |
| CAP-01 | Capability sets are empty by default; an opted-in workload control plane receives only its exact reviewed allowlist, and ordinary children receive none. | 1 and 2 | Inspect bounding, effective, permitted, inheritable, and ambient sets for the default, supervisor, and child processes; verify omitted capabilities fail. | Unproven; capability contract not implemented |
| LC-01 | Files created by declared identities have expected subordinate ownership; ordinary restart and recreation preserve both the stored allocation and host ownership. | 2 | Stored-allocation and host-ownership inspection before and after ordinary stop/start and recreation through Reploy. | Unproven; deferred to lifecycle work |
| LC-02 | Generation updates may add, remove, or renumber application identities without rewriting persistent ownership. Exact mappings contain only the current accepted set while removed assignments remain reserved and reactivate on rollback; bounded mappings retain their fixed geometry while the supervisor admits only the current set. Mapping-profile, supervisor-identity, and bounded-geometry changes fail before mutation. | 2 | Lifecycle tests across identity addition, removal, renumbering, and rollback for both profiles; host ownership inspection proving no automatic rewrite; exact-map and supervisor-policy inspection for each generation; dormant-assignment reservation and reactivation checks; and pre-mutation rejection tests for mechanism changes. | Unproven; deferred to lifecycle work |
| LC-03 | Backup and restore preserve ownership or perform an explicit checked translation. | 2 | Checksummed offline export/import with no workload attached. | Unproven; deferred to storage work |
| AL-01 | Concurrent allocations cannot overlap, mappings remain reserved while live or retained managed storage uses them, and all external `bind` mounts are rejected before mutation. | 1 | Contended allocation, crash recovery, retention, removal, safe-reuse, and pre-mutation mount-rejection tests. | Unproven; allocator not designed |
| RV-01 | Revocation stops and removes live workload resources, prevents service-manager or other autonomous restart, and makes further operations fail closed without mutating retained storage or releasing its mapping. | 1 | Active-workload revocation with runtime-resource absence and restart-prevention inspection, followed by recovery tests with retained owned data and its mapping still reserved. | Unproven; revocation not designed |
| HB-01 | Host bootstrap is explicit, auditable, idempotent, validates that its delegated IDs are exclusive and non-overlapping, and enables later user-level operation without host root. | 1 | Repeated bootstrap, successful user operation, and failures for absent or inconsistent preparation, collisions with local or NSS-provided host principals, overlap with another principal's subordinate delegation, and an identity inventory whose completeness cannot be established. | Unproven; bootstrap not designed |
| SB-01 | No workload uses privileged mode or gains host-root authority. | 1 | Runtime inspection plus negative host-authority tests. | Unproven; repeated conformance probe pending |
| SB-02 | The approved seccomp profile is explicitly selected and identifiable in the resolved and effective runtime policy; application processes have `NoNewPrivs: 1` and seccomp filter mode (`Seccomp: 2`); syscalls the approved profile must block fail; the container root filesystem is read-only except for declared writable storage; and namespaces, devices, mounts, and network attachment match declared policy, with distinct network namespaces and no host control socket, undeclared path, or peer-reachable network. | 1 and 2 | Resolved-policy and host/runtime inspection through the supported Podman path plus negative approved-policy syscall, path, cross-workload TCP, cross-workload UDP, and cross-workload Linux abstract Unix-socket tests through supported Reploy interfaces. | Unproven; baseline checked by one-off probe; repeated conformance pending |
| FS-01 | Trusted filesystem preparation cannot follow symlinks, accept unexpected object types, escape the declared root, recursively rewrite application data, or execute application-provided code. | 2 | Adversarial descriptor-safe preparation tests plus executable and hook canaries proving that trusted preparation never invokes application-provided code; process tracing verifies that any later application-provided execution begins only after entering its final child credentials and capability set. | Unproven; deferred to filesystem design |
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
