---
status: Active
updated: 2026-10-27
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
The maintainer subsequently approved the blueprint identity structure and the
native-process amendment recorded in
[the mail reconciliation](MULTI_IDENTITY_MAIL_RECONCILIATION.md). Trusted one-shot
startup releases the selected untrusted application under explicit workload
authority; no privileged resident Reploy identity supervisor remains beside it.
The blueprint declares identities and capability requests. D records intent and
image evidence; E must qualify actual execution. Portable declarations do not
grant a capability merely because its name is recognized.

Existing A–C code and private supervisor observations retain their historical
meanings. They are not proof of the new native-process boundary. Public mapped
execution remains inhibited until E/F/G and generic-launch succeed. The approved
D input may be implemented behind those guards after its own design/PR gates;
runtime qualification does not become a fictitious prerequisite for parsing data.

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
: One independently isolated Linux-container application unit.

**Workload installation**
: One existing deployment directory and its durable record for a staged test
  workload or an installed workload. Both can own explicit private mappings.
  Its identity and mapping survive restarts and generation changes. This is not
  an installation of the Reploy program itself.
  Multi-identity operates on one such installation; a general multi-service
  composition is a separate feature, not a parent authority required here.

**Generation**
: One immutable, image-locked revision of a workload installation. An update
  creates or selects a generation; it does not create a new workload identity
  or mapping by default.

**Declared identity**
: A container-local UID, GID, or supplementary group that the workload author
  selects for an admitted application entry. The declaration includes the
  image-local account name and numeric
  value. The declaration is configuration and locked-image evidence. Its role
  in the kernel boundary depends on the selected mapping profile.

**Runtime-required identity**
: A container-local UID or GID that Podman or the OCI runtime must map for
  container operation. Reploy must enumerate, justify and explicitly accept
  these accounts as workload authority, verify their image evidence and require
  mapping coverage before release. They cannot be treated as protected from
  identity-changing application code merely because the runtime needs them;
  for example, the current rootless Podman probe required container GID 5 for
  the `devpts` mount.

**Trusted startup boundary**
: Reploy-owned one-shot code that runs before any application image code. After
  current host admission, mapping/storage/network checks, it establishes final
  entry credentials, capability ceiling, descriptor closure and kernel policy,
  then directly executes the selected application. No stronger resident Reploy
  identity process or private control descriptor remains with the workload.

**Native application master**
: An image-provided foreground process, such as Postfix or Dovecot, that remains
  untrusted. It may manage workers and use explicitly admitted capabilities
  inside its selected mapping. With identity-changing rights it can assume other
  mapped identities; its own account policy is not Reploy's enforcement boundary.

**Legacy supervisor binding**
: Historical B/C state may retain a binding from the old resident-supervisor
  model. Preserve its recorded meaning. A non-null incompatible binding rejects
  before effects until an approved migration or a new installation establishes
  the new process model; never silently reinterpret or clear it.

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
: A sparse mapping containing only accepted declared identities, including
  explicitly accepted runtime-required accounts. An unmapped container ID has
  no host mapping and the kernel
  rejects transitions to it.

**Bounded-range mapping profile**
: A contiguous, explicitly sized container-ID range mapped to a private
  subordinate host-ID range. The operator accepts the entire private range as
  workload authority. An application with the relevant capability may select
  otherwise undeclared in-range IDs. Declaring account names does not narrow
  that kernel boundary; out-of-range transitions still reject.

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
  workloads. The initial model uses existing host delegation to the operating
  OS user; the administrator coordinates assignments across installations and
  other services. Reploy introduces no enrollment service or privileged broker.
  Delegation does not give
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

All image-provided code, including native masters, helpers, hooks and workers,
and application-controlled files are untrusted. They may
attempt to:

- use identity-changing system calls, supplementary groups, set-ID files, or
  file capabilities to exceed their admitted authority;
- exploit a leaked trusted setup descriptor or a stronger temporary launch
  process injected into an already occupied namespace;
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
in-range ID is kernel-usable with the relevant capability; that boundary is
an explicit profile property, not exact identity allowlisting.

The protected assets are host authority, other workloads and their data,
stable persistent ownership, delegated subordinate-ID space, and the accuracy
of Reploy's installation state.

The initial model relies on the trusted administrator to coordinate host-ID
reservations across separate installations and unmanaged services, including
concurrent administrative changes. Reploy checks the selected mappings against
host delegation, host identity information and its installation state; known
conflicts or insufficient evidence for a required check cause rejection. It
does not provide an automatic system-wide allocation or coordination guarantee.
Administrator coordination remains a required operating condition, including
while an installation is inactive but its owned data remains.

## Required Security Properties

1. **Profile-specific identity boundary.** Exact maps permit only accepted
   enumerated IDs and reject unmapped transitions. Bounded maps grant their
   whole accepted private range, including otherwise undeclared in-range IDs,
   and reject out-of-range transitions. Neither profile silently substitutes
   for the other; capability requests do not impose a new exact-only rule.
   Every Reploy-controlled entry starts with the declared real, effective,
   saved-set and filesystem UID/GID and exact supplementary groups. Native
   descendants may change credentials within admitted mapping/capability
   authority. Empty-capability entries cannot regain dropped authority. Before
   release, close every descriptor except constructed standard streams and
   explicitly declared application descriptors valid for final authority.
   Private setup files, sockets, pidfds and control descriptors never reach the
   application. No stronger resident Reploy identity supervisor remains after
   direct exec. Live exec must not inject stronger temporary setup authority
   or private control descriptors into the occupied namespace; an unproved path
   rejects before effects. No mapped account is claimed protected from an
   identity-changing native master solely by a different UID or role name.
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
   bounded-range geometry when selected, and established
   container-ID-to-host-ID associations across restart, recreation, update,
   and rollback unless an explicit ownership migration is performed. Its
   application identity declarations may evolve by generation. Under the exact
   profile, identities absent from the current generation are omitted from the
   active mapping but their assignments remain dormant and reserved. Under the
   bounded profile, the accepted fixed range remains the kernel authority.
   Incompatible historical supervisor bindings are rejected, not rewritten.
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
7. **Default-zero capability control.** Empty remains the default. A blueprint
   explicitly requests each capability; naming a user or application grants
   none. D validates recognized Linux names as portable requests, not execution
   authorization. E qualifies an exact supported set for the selected entry,
   image, runtime and effective kernel policy before release. Bounding,
   permitted and effective sets contain only accepted rights; inheritable and
   ambient sets stay empty in the selected initial transition. Native children
   may retain subsets rather than being universally capability-free. Preserve
   ordinary drop-all/NOROOT/no-new-privileges behavior for empty-capability
   entries. An unsupported capability/credential transition rejects before
   effects. No engine defaults or implicit package-specific bundle is allowed.
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

Every Reploy-controlled entry uses fresh host admission and the accepted
generation/role policy: initial start, external command/hook/session, runtime
exec and whole-container replacement. Normalize initial credentials, apply only
accepted capabilities, close private descriptors and verify the final sandbox.
Native forks, execs and worker replacement use already admitted workload
authority; they do not authorize new mappings, mounts, secrets or capabilities.
The required foreground master's exit ends the instance. The host owns fresh
replacement and revocation, fences every restart owner and stops the entire
container. No engine restart policy or raw exec bypass is permitted.

Container-provided executable code and dependencies are read-only by default;
reject writable mounts that shadow protected code paths. Mutation requires
explicit opt-in and gives up replacement protection for the affected code.
Trusted Reploy startup/control code remains protected. Binary-exclusive privilege
inheritance is not promised by immutability: already privileged image code remains
able to use its admitted authority. No host AppArmor/SELinux/IPE dependency is
selected for this stage. Effective syscall policy must independently deny
namespace/handle/mount escapes under the final capability set, with positive
ordinary fork/thread and required identity/chroot controls. A pinned JSON name
alone is not proof of its capability-conditioned effective filter.

## Decisions Frozen by This Contract

### Identity declarations

The workload author, not Reploy, identifies the application accounts required
by the image. The approved `identity_policy` carries image-local names and
numeric UID/GID values, scalar groups and scalar/object users. Same-name primary
groups and empty supplementary groups are explicit after normalization; optional
full names are descriptive metadata. Names and numbers bind image evidence,
entry policy and diagnostics. Under the exact profile,
they contribute to the accepted sparse mapping set. Under the bounded-range
profile, they do not narrow the kernel mapping to an exact allowlist. Reploy
must reject a generation when the declared name-to-number relationship does
not match the locked image or the selected mapping profile.

Reploy resolves runtime-required accounts and requires explicit declaration,
locked name/number evidence and accepted coverage before release. They remain
minimal and receive the same subordinate-host and lifecycle isolation. No new
supervisor identity is declared. A mapped runtime-required account is reachable
workload authority when the application has the relevant identity-changing
capability. An accepted bounded range intentionally includes unused numeric IDs;
they do not need fabricated account names simply because they are mapped.

This creates no corresponding named host accounts. The host observes only the
selected mechanism's private numeric identities.

Application declarations may add or remove identities between generations. A
name that moves to a different numeric UID or GID is the removal of the old
identity and addition of a new one. Reploy validates each generation against
its locked image and selected mapping. It does not require every generation to
declare the same application identities.

Reploy does not scan, rewrite, `chown`, or otherwise migrate persistent files
when declarations change. Files owned by a removed identity keep their old
numeric subordinate UID and GID. They may consequently be inaccessible or
appear under an unmapped overflow identity to the current generation. Cleanup
or ownership repair is the operator's responsibility. Reusing the same numeric
container UID or GID within the installation, whether by a later generation or
rollback, restores the ordinary Unix access implied by that ownership; an
image-local account name does not grant or revoke filesystem authority.

### Mapping ownership

#### Approved coordination and scope boundary — 2026-10-19

The initial model uses operator-supplied mappings for one workload installation,
with existing OS-user delegation and manual, cooperative host coordination.
Host-specific mapping input is separate from portable workload/image authority.
Reploy validates input against existing delegation before use and records accepted
mappings in that installation's durable state.

The earlier 2026-10-16 discussion allowed composition to hold initial mappings.
The maintainer subsequently separated composition from multi-identity delivery.
A future composition may supply this same input, but service membership, aggregate
staging and multi-service orchestration need a separate design. They are not
prerequisites and cannot override established installation mappings.

There is no initial Reploy-wide enrollment, automatic allocator, mandatory shared
registry or privileged broker. State changes within an installation are serialized
and its assignments cannot conflict. The administrator coordinates reservations
across other installations and unmanaged services, including retained data and
concurrent administrative changes. Known conflicts or incomplete required authority
evidence reject; local locks cannot establish a system-wide reservation guarantee.
Detailed mapping input/state syntax still needs its own design approval.

A staged test deployment and an installed destination are separate mapping
owners. Staging can hold explicit mappings and run the supported test lifecycle
after the same execution/security gates as an installed workload. Installing
from staging requires an explicit destination mapping decision; it does not
copy or transfer the source's mapping record, reservations, pending operations
or managed runtime/storage ownership. Source mappings remain reserved while
their resources or retained data exist. Destination mappings must be separately
validated and non-overlapping with the source's live or retained mappings.
Moving mapped test data is a separate checked ownership-translation operation,
not an automatic consequence of install. Plain directory copies cannot create
a second valid mapping owner. These rules preserve stage/run/test/install
without treating a tested source's host authority as portable build content.

One workload installation owns one stable private mapping. Its generations
reference that mapping rather than receiving new mappings. Removal releases
the mapping only after all owned live resources and retained storage are gone
or their ownership has been explicitly migrated.

For the exact profile, the active mapping is generated from the current
generation, but every previously established identity assignment remains part
of the installation's reserved allocation while the installation or its
retained storage exists. A newly declared numeric identity requires an explicit,
validated non-overlapping assignment; a missing assignment fails before start.
Removing an identity makes its assignment dormant rather than reusable.
The bounded profile keeps its fixed private range while the generation's entry
policy changes. Reploy conservatively reserves
the installation's allocation based on retained managed storage; it does not
need to discover or repair every stale owner in that storage.

Changing the mapping profile, process-model binding, or bounded-range geometry
is not an ordinary generation update. It requires a future explicit migration
or installation replacement operation and otherwise fails before mutation.

Initial multi-identity workloads use only image content and Reploy-managed
storage that participates in this retention accounting. Every external `bind`
mount fails before workload mutation. A future input contract may admit a
filtered copy that contains only accepted inert filesystem objects rather than
exposing a live external directory.

Initial mapping input never overrides recorded assignments on update,
recreation or rollback, and causes no automatic ownership migration. A future
composition is only a caller of the same installation boundary, not a second
mapping authority. Sharing still requires explicit declarations and isolation
proof. Host-root or named-host-account bindings remain outside this subordinate-ID
profile and require a separate security design.

### Mapping profiles and backend boundary

Reploy will support two explicit mapping profiles:

- the exact profile maps only accepted declared IDs, including explicitly
  accepted runtime-required accounts; and
- the bounded-range profile maps one explicitly sized private container range.

Neither profile is silently substituted for the other, and this contract does
not choose a default. Both initially require a positively identified Podman
backend. Reploy must reject either profile on Docker Engine before creating or
mutating workload resources, even if a particular Docker configuration can
provide daemon-wide or contiguous user-namespace remapping.

For both profiles, the mapping is private to one workload installation,
non-overlapping, and paired with normalized entry credentials, verified
accepted capability sets, and blocked expansion of authority before application
execution. Podman-specific transport through a Docker-compatible API does not
make the feature a Docker Engine capability.

Capability declarations are opt-in. With no declaration, Reploy requests an
empty capability bounding set and supplies no effective, permitted,
inheritable, or ambient capabilities. A later public contract may expose a
supported runtime policy, but it must not inherit Podman defaults or define an
implicit Postfix-specific bundle. Recognizing and locking a capability request
in D is not proof that E can safely grant it. Unsupported requests reject at
execution admission; runtime grants remain behind launch-design/generic-launch.

Initial image account evidence uses ordinary regular files at standard paths,
with the files-only lookup configuration described in the D design. Unsupported
links, account-path whiteouts and alternate lookup sources produce a clear
unsupported-source error rather than image execution or a guessed projection.
This is an explicitly selected initial compatibility limit, not a statement that
other valid Linux images are inherently unsafe.

### Native-process evidence before activation

E/F/G must qualify the one-shot final exec and live-exec boundary through the
supported rootless Podman path. Require repeated fresh evidence for both
profiles: exact final credentials/groups/capabilities/descriptors; positive
accepted transitions and profile-specific negative transitions; effective
capability-conditioned syscall policy; no stronger resident or temporary Reploy
process with payload access; host/peer isolation and collision admission;
whole-instance replacement/revocation; ownership retention; once-per-instance
secret delivery and narrow interfaces. Native stock mail, runtime exec, storage,
network and lifecycle each retain their independent production proof gates.
The raw kernel fixture and historical private probes do not close these gates.

### Historical private supervisor evidence

The following checklist records the earlier supervisor mechanism's prerequisite
and private conformance criteria. Its three-exact/three-bounded observations
remain historical evidence for their original sources and process model. They
do not reinstate a resident supervisor, block guarded D declarations on native
runtime tests, or qualify the new native-process launch boundary. Current
activation requires the native-process evidence above and the current ledger.

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
- immediately before untrusted exec, the trusted launcher observes exactly the
  declared descriptor allowlist after closing adversarial non-`CLOEXEC`
  supervisor control-file, socket, pidfd, and private-data canaries; the first
  application report confirms the launcher's evidence and the canaries remain
  absent;
- capability-dropped application children cannot change identity, and neither
  set-ID nor file-capability execution can restore identity-changing authority;
- a complete bounded supervisor thread inventory remains stable across
  per-thread inspection and signaling probes. Missing, unreadable, changing,
  or incomplete thread evidence rejects the observation;
- all four fields in every supervisor thread's
  `/proc/<pid>/task/<tid>/status` `Uid:` and `Gid:` entries use its dedicated
  supervisor UID and GID, distinct from every application child. Each thread's
  final `Groups:` vector is empty or contains only that supervisor GID, and
  none of its capability sets exceeds the reviewed allowlist. The privileged
  launch thread retains exactly its required reviewed capability sets; the
  supervisor reports `PR_GET_DUMPABLE` as zero while it retains capabilities;
  its POSIX session ID differs from every application child's session ID; and
  negative same-workload probes, including explicit `SIGCONT` attempts, cannot
  use `kill`, `pidfd_send_signal`, per-thread `tgkill`, `ptrace`, `/proc/<pid>/mem`,
  `process_vm_readv`, or `process_vm_writev` against it from an application
  child;
- the no-capability default has empty bounding, effective, permitted,
  inheritable, and ambient sets, while an opted-in supervisor has no capability
  outside its exact reviewed allowlist;
- the approved seccomp profile is explicitly selected and identifiable in the
  resolved and effective runtime policy, application processes report
  `NoNewPrivs: 1` and seccomp filter mode (`Seccomp: 2`), negative probes for
  syscalls the approved profile must block fail while the same valid operation
  succeeds in an otherwise equivalent unfiltered control, and the container root
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
- the private probe identifies Docker Engine as ineligible and sends it no
  Podman mapping syntax or workload mutation request.

Product-level Docker Engine rejection is separate evidence under BE-01. It
remains required after product integration is authorized.

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

Product evidence for ID-02, ID-03, CAP-01, and SB-02 tests every supported
Reploy-controlled entry, including runtime exec. Test accepted identities,
profile-specific map boundaries, normalized initial credentials/groups, accepted
capability ceilings, descriptor closure and effective sandbox admission. Native
worker transitions are confined by accepted workload authority, not a false
per-fork host admission or capability-free-descendant claim. Old supervisor
results remain historical and do not complete this product evidence.

For a capability-bearing container, secret and interface grants must be
consistent across the whole workload authority. Capability-free roles sharing a
UID also share authority. A negative participant test must use genuinely
unauthorized authority; changing a role name or initial group vector is not
isolation from code that can assume the recipient's mapped identity.

| ID | Required result | Owning milestone | Minimum evidence | Current state |
| --- | --- | --- | --- | --- |
| ID-01 | Every mapped identity, including container root and explicitly accepted runtime accounts, has exclusively delegated subordinate host authority; no host-root, local/NSS-principal or other-delegation alias. | 1 | Complete current delegation/principal inventory, interval collision checks, active-libsubid agreement and UID/GID collision negatives before each admission; exact host/container map inspection for both profiles. | Historical private per-ID observations retained; current product bulk admission and native execution evidence pending |
| ID-02 | Every controlled entry begins with its declared four UID/GID fields and exact groups; native changes stay within the selected map. Exact rejects unmapped IDs; bounded permits its entire accepted range, including undeclared in-range IDs. | 1 | Initial and descendant process/thread credential inventories; declared-transition positives; unmapped/out-of-range negatives; bounded in-range positive controls; both profiles and every controlled entry. | Earlier supervisor-policy passes are historical; native product qualification pending |
| ID-03 | Accepted mapping/capability ceilings cannot expand. No private setup descriptors or stronger resident Reploy identity process remain after release; live exec injects no stronger temporary setup authority. Empty-capability entries cannot regain dropped privilege. | 1 | One-shot exec and live-exec proof/rejection; complete credential/capability/descriptor observations, non-CLOEXEC canaries, namespace/handle/mount negatives, set-ID/file-capability regain controls and positive syscall controls; verify whole-container teardown and process absence. | Earlier dedicated-supervisor isolation passes are historical; one-shot/native and occupied-namespace exec proofs pending |
| ID-04 | Two installations using the same container IDs have distinct host mappings, PID, IPC, network, and mount namespaces, private mount propagation, and private `/dev/shm` and `/dev/mqueue` mounts; they cannot observe, signal, or ptrace each other's processes, see each other's mounts, use each other's System V or named POSIX shared-memory, semaphore, or message-queue objects, connect to each other's Linux abstract Unix sockets, or read each other's private state. | 1 | Concurrent two-workload probe, including a staged test deployment and its separate installed destination, with host mapping, PID-namespace, IPC-namespace, network-namespace, mount-namespace, mount-propagation, `/dev/shm`, and `/dev/mqueue` inspection plus negative cross-workload mount visibility, process visibility, signaling, ptrace, System V IPC, POSIX `shm_open`/`sem_open`, POSIX `mq_open`/send/receive, abstract Unix-socket connection, and private-state access tests. | Private 1B conformance passed 3 exact and 3 bounded trials; product integration pending |
| BE-01 | Both mapping profiles run only on a positively identified Podman backend; Docker Engine rejects them before workload mutation. | 1 | Backend detection plus positive Podman and negative Docker Engine integration tests. | Private Podman-positive and Docker-negative gate passed; product integration pending |
| CAP-01 | Capabilities are empty by default. Only explicit qualified role requests may be granted; D recognition/locking is not runtime authorization. Native descendants retain at most admitted authority. | 1 and 2 | P/E/B/I/A observations at final exec and across native transitions; empty/default and omitted-right negatives; per-entry E qualification, securebits/no-new-privileges and generation-bound effective syscall-policy evidence. | Historical default/supervisor/child passes retained; native capability grants and product qualification pending |
| LC-01 | Files created by declared identities have expected subordinate ownership; ordinary restart and recreation preserve both the stored allocation and host ownership. | 2 | Stored-allocation and host-ownership inspection before and after ordinary stop/start and recreation through Reploy. | Unproven; deferred to lifecycle work |
| LC-02 | Updates preserve accepted numeric ownership and mapping geometry. Exact removed IDs stay reserved but inactive; bounded retains its whole accepted range. Incompatible old supervisor bindings or mechanism changes reject without silent migration. | 2 | Identity addition/removal/renumbering and rollback for both profiles; unchanged host ownership and retained reservations; dormant exact reactivation; pre-effect rejection of incompatible history/profile/geometry changes. | Unproven; lifecycle and explicit incompatible-binding checks pending |
| LC-03 | Backup and restore preserve ownership or use an explicit checked translation. Before writing archived ownership, restore validates the destination allocation and exclusive delegation. Restore uses collision-safe staging or checked translation. Unsafe or unproved destination mappings fail before ownership is written. | 2 | Checksummed offline export/import with no workload attached. Test cross-host UID/GID collisions with local/NSS accounts and another principal's delegation. Prove checks occur before archived ownership is written. Prove failed restore leaves destination state unchanged. | Unproven; deferred to storage work |
| AL-01 | Accepted mappings must not overlap other live or retained installation mappings. Reploy serializes and validates assignment changes within one installation; the administrator coordinates reservations outside it. Live or retained storage keeps its mapping reserved, and external `bind` mounts reject before mutation. | 1 | Duplicate/overlapping input rejection, staged mapping configuration and update preservation, explicit separately mapped installation from that source, source/destination overlap and copied-record rejection, concurrent installation-state operations, crash recovery, retention, removal, safe-reuse and pre-mutation mount-rejection tests; explicit operator responsibility for cross-installation reservations and documented manual-coordination limits. | Unproven; single-installation/manual-coordination direction approved, product mapping state pending |
| RV-01 | Revocation stops and removes live workload resources, prevents service-manager or other autonomous restart, and makes further operations fail closed without mutating retained storage or releasing its mapping. | 1 | Active-workload revocation with runtime-resource absence and restart-prevention inspection, followed by recovery tests with retained owned data and its mapping still reserved. | Unproven; revocation not designed |
| HB-01 | The administrator supplies existing host delegation and coordinated assignments. Reploy checks these before user-level operation without modifying host accounts, delegation or system-wide Reploy configuration. | 1 | Repeated read-only preflight with unchanged host state, successful user operation, and failures for absent or inconsistent preparation, collisions with local or NSS-provided host principals, overlap with another principal's subordinate delegation, and an identity inventory whose completeness cannot be established. | Unproven; existing-delegation direction approved, product preflight pending |
| SB-01 | No workload uses privileged mode or gains host-root authority. | 1 | Runtime inspection plus negative host-authority tests. | Private 1B conformance passed 3 exact and 3 bounded trials; product integration pending |
| SB-02 | Effective generation-bound kernel policy enforces namespaces, devices, network and final capability ceilings. Code is read-only by default; explicit mutation opt-in relinquishes affected replacement protection but cannot expose trusted Reploy code or expand authority. | 1 and 2 | Supported Podman inspection and paired positive/negative syscall, namespace, handle, mount and peer-network controls; NoNewPrivs/Seccomp evidence; code-write/mount-shadow rejection by default and explicit mutation-mode qualification; host-device rejection without mutation. | Historical drop-all sandbox observations retained; native capability-conditioned filter, code policy and supported product integration pending |
| FS-01 | Trusted filesystem preparation cannot follow symlinks, accept unexpected object types, escape the declared root, recursively rewrite application data, or execute application-provided code. | 2 | Adversarial descriptor-safe preparation tests plus executable and hook canaries proving that trusted preparation never invokes application-provided code; process tracing verifies that any later application-provided execution begins only after entering its final child credentials and capability set. | Unproven; deferred to filesystem design |
| CF-01 | Disagreement among declarations, locked image, delegation, allocation, and persistent ownership fails before start without state mutation. | 1 and 2 | Deliberate mismatch matrix with before/after state evidence. | Unproven; initial cases required in milestone 1 |
| SI-01 | Only explicitly admitted producer/consumer workload authority can use a shared Unix socket. Capability-bearing roles share workload authority; same-UID capability-free roles also require consistent grants. | 3 | Positive connection tests for each admitted authority and negatives from genuinely unauthorized authorities/workloads; reject inconsistent per-role grants before effects. | Unproven; shared-interface mechanism remains a substantive gate |
| SI-02 | Participants see only the declared interface path, and private mappings and data remain isolated. | 3 | Mount, traversal, connection, and private-data visibility tests. | Unproven; shared interface not designed |
| SI-03 | Stale sockets and producer restart are reconciled safely without traversing persistent application data. | 3 | Adversarial stale-object and restart tests. | Unproven; shared interface not designed |

## Required Sequence

1. Preserve the approved Podman-only, dual-profile, manually coordinated host
   boundary and delivered A–C evidence without reinterpreting their history.
2. Align and review the approved native-process amendment and D blueprint input;
   refresh affected stamps and complete the design PR cycle.
3. Implement D's declarations/evidence behind unchanged public execution guards
   only through its authorized delivery run and slice/PR gates.
4. Resolve E/F/G production LLD and native mechanism proofs before activation.
   Later lifecycle, restore, secrets, interfaces and stock-mail qualification
   remain independent gates; no private probe is substituted for product proof.

If Podman cannot prove a selected profile and its accepted authority boundary
at acceptable cost, that profile is blocked. Reploy must not substitute Docker
Engine, a privileged container, overlapping or host-aligned mappings, or an
unconfined application process and describe it as equivalent isolation.
