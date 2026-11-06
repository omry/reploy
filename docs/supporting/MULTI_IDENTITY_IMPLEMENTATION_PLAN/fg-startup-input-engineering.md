# F/G startup inputs and unchanged-container retries

This is engineering for the approved unchanged-container retry direction in the
[campaign plan](../../MULTI_IDENTITY_IMPLEMENTATION_PLAN.md). It defines a
candidate for review, not a completed production mechanism. The initial mapped
cohort, capabilities, secret rules and public activation guards remain unchanged.

This is the unselected engine-retry alternative in the
[controller comparison](fg-controller-evaluation.md). Retained approval and
private-input snapshots are not prerequisites for a host controller that
creates and admits replacements and reuses the existing one-shot delivery. Do
not implement or size both alternatives as mandatory work. The maintainer
selected lean controller engineering; this alternative retains useful protocol
analysis but is not the selected delivery mechanism.

## Reuse and mechanism choice

Retain the existing immutable trusted startup, exact engine-object ownership,
deployment transaction, private runtime masking, confined preparation and final
native exec transition. Replace the invocation-only stdin release on the new
mapped public path with a bounded host-controlled startup record that survives
ordinary process restarts of that container. Do not carry two public mapped
startup mechanisms for the same purpose or reinterpret old one-shot evidence as
proof of the new mechanism. E's recorded evidence remains historical.

An always-reachable callback receiver would retain per-start host availability
and introduce a service/transport requirement. It is not needed for the approved
same-container approval lifetime. A writable application flag would be smaller
but is not authoritative before first admission. The candidate uses a narrowly
mounted, read-only host-controlled record instead. No general broker, engine
socket, shared daemon, signing service or allocator is introduced.

## Required records and ownership

The existing deployment transaction remains authoritative. It retains the
installation and exact engine endpoint/object ID, create nonce, accepted
generation/image/runtime-policy identities, mapping geometry, retry policy,
owned startup-input handles and operation progress. Startup material is only a
projection for that exact container; it cannot configure or admit an object.

Separate the immutable container identity from a process start:

- Exact container: selected engine plus full container ID and independently
  verified immutable create binding. A name or application claim is insufficient.
- Process incarnation: freshly observed PID/start time and namespace identity.
  These change on engine retry and are never reused as current process proof.
- Startup completion: applies to the observed incarnation. An old `up` record
  or handoff claim does not prove a new process completed startup.

`WorkloadLifecycleV1` currently binds one process start. G must extend or version
its mapped receipt without silently weakening ordinary one-shot readers. A
locked host operation may reconcile a new observed start only after proving it
is the exact admitted unchanged container. No object adoption by name, stale PID
or copied lifecycle file is permitted.

## First-admission protocol

1. Hold the installation operation lock; read current generation and host
   authority through existing readers. Persist an inhibited intent and fresh
   create nonce before effects.
2. Prepare the startup record under the private host runtime area using held
   descriptors and exclusive creation. Its initial state grants nothing. Prepare
   only required per-recipient startup inputs through the confined preparer.
3. Create the selected container with immutable trusted startup arguments,
   expected nonce/plan binding and narrowly scoped read-only input mounts. Never
   mount the deployment directory, state store, source `.env`, engine socket or
   a directory containing other recipients' material into the workload.
4. Record the returned full container ID. Start only trusted Reploy setup code.
   It prepares the local sandbox/network state and remains inhibited; no image
   entrypoint, application hook or application code runs with setup authority.
5. Verify the exact owned process and F's effective mount/network/kernel
   observations. Complete the existing mapping/generation admission transaction.
6. After the exact-object admission is durable, publish its bounded startup
   record through the held file descriptor and flush it. A malformed, partial or
   mismatched record grants nothing. An uncertain publish keeps the exact owner
   and inhibited recovery record; it never triggers blind permission transfer.
7. Trusted startup accepts only its fixed path and immutable expected binding,
   closes the record descriptor and performs the final credential/capability,
   descriptor and sandbox transition before directly executing the application.

The startup record must be regular bounded data with validated ownership,
identity and mount protection. The host must verify the same inode is mounted
and written; atomic pathname replacement alone does not update a single-file
bind mount. No symlink, hardlink alias or application-selected source path is
accepted. The prototype's short comparison string is not the production wire
contract; the candidate production contract is below.

### Candidate record and publication format

Use the existing closed canonical JSON and digest helpers. The binding contains
the installation ID, positive engine-identity digest, fresh 256-bit create nonce,
generation/build-lock digest, immutable image digest and canonical entry-plan
digest. The plan already binds role, initial credentials/groups/capabilities,
arguments, mappings, mount policy and network policy. It excludes mutable PID,
start time and engine-assigned container ID. Freeze that binding in trusted
create arguments. It is not selected through application arguments or env.

The record has exactly `schema`, `state`, `binding`, `container_id`,
`initial_operation` and `initial_revision`. Schema is
`mapped-container-approval-v1`; the only committed state is `admitted`.
`container_id` is the engine's full lowercase hexadecimal ID. Operation and
revision identify the actual admission commit, not a revision to be refreshed
on every retry. Reject unknown/duplicate keys and noncanonical encodings.

The file consists of the eight-byte magic `RPLYAP1\n`, a four-byte big-endian
JSON length, 1–65,536 canonical JSON bytes, and a 32-byte SHA-256 digest of
`mapped-container-approval-v1\0` followed by those JSON bytes. Its exact length
must be `44 + JSON length`; no trailing data is accepted. The digest detects
partial/inconsistent publication. It is not a signature or authentication key.
Authority comes from the validated host-controlled inode, read-only view and
exact-object transaction, not from anyone knowing the non-secret binding.

An initial empty/invalid record grants nothing. The sole host writer first
writes body/digest to its held inode, then the commit header and length, and
flushes the file. It does this only after the admission state is durably
committed. The reader accepts only a full valid snapshot matching the frozen
binding, with a regular single-link file and the expected read-only mount.
Host-side ownership and exact source identity are checked before attachment;
an overflow UID inside the private map is not treated as proof of host ownership.

Permission is immutable for that container. A different plan, nonce or engine
requires a new container and pending record. A torn/uncertain first publication
retains the exact owner. Recovery may reconcile/reconstruct only that exact
committed projection after fresh host verification; it cannot adopt an object,
replay a one-shot secret transfer or infer approval from file existence.

The trusted host creator must generate a new nonce and record for every create,
bind the returned ID before commit and reject copied authority. The startup
process cannot independently learn its full engine object ID without engine
access; the design does not pretend that matching a submitted ID authenticates
the caller. This trusted creation/association duty is an explicit proof target.

## Retry protocol

The engine restarts that same container under Reploy's configured permitted
policy. Trusted startup repeats local sandbox setup, validates the fixed
read-only record and its immutable create binding, then repeats the final
authority reduction and exec. It does not need a new stdin frame, expired
creation deadline, host inventory scan or reachable callback.

Waiting for an initial uncommitted record has a bounded per-process deadline;
it must not reuse an absolute deadline from the original creation. Missing,
corrupt, substituted or inconsistent material fails before payload execution.
A new container receives a fresh binding and unapproved record even if image,
application name and logical configuration are identical.

A Reploy-controlled stop/revocation/preparation transaction fences every
registered replacement owner and removes or otherwise positively fences the
exact engine object before protected effects. Owned record cleanup follows
proved runtime retirement. It is not a substitute for that retirement. A
runtime-held inode can remain readable after host unlink, so unlinking a record
must never be the sole revocation mechanism.

## Private environment and secret constraints

The existing private workload file is `.env`, represented by
`PrivateWorkloadEnvironmentFileName`; `.env.private` was an inaccurate name in
the initial discussion. The current relay sends parsed values once per process
startup and removes its FIFO names. The mapped transition currently rejects that
transport. A permission record does not recreate private environment values.

The candidate extension prepares a private runtime snapshot of only the parsed
values intended for the persistent workload, not raw `.env` comments or a whole
deployment directory. It is separate from the non-secret approval record and
from portable image/state/lock/artifact metadata. Values must not enter engine
environment inspection, arguments, manifests, hashes published in metadata,
logs, review packets or application output. Retain current `.env` validation,
limits and masking.

For the private mapped Linux cohort, prepare recipient-owned files through the
same confined subordinate-identity preparer required by F, with only that
recipient's access. A world-readable secret mount is not an acceptable shortcut.
Same-UID roles and a master permitted to assume that UID retain their existing
authority; the mechanism must not promise new isolation between them.

Trusted startup reads the recipient input without retaining private descriptors
or broader credential authority across exec. Reuse E's last native transition;
prepare all environment buffers before its descriptor-table detach/raw exec.
Do not put ordinary Go code after the audited raw transition or retain a new
privileged reader beside the application. The exact read/transition integration
needs its native proof before the guard is removed. The candidate Linux reader
uses the already locked setup thread and accepted identity-setting authority to
read as the recipient filesystem identity, then verifies restoration before
preparing the existing last-transition buffers. It must not add a DAC-bypass
grant or retain a stronger reader process. If that exact transition cannot be
proved, keep the guard and return to engineering rather than making the secret
file readable to other identities.

Each new process receives its captured input once. Repeated `up` must not resend
to a live process. Engine retry uses the same captured inputs; an explicit
replacement prepares a new snapshot. Rotation/source refresh belongs to its
explicit lifecycle operation, not silent per-retry host-file rereading. Runtime
snapshots stay private, are excluded from portable copies/export, and remain
owned until exact runtime/input cleanup succeeds. This must not become a new
general secret-reference or rotation feature; I retains that work.

Other supported host/engine paths retain their actual private-environment
guarantees. Do not infer POSIX recipient ownership from Windows ACLs, Desktop
file sharing or a WSL result. Existing unsupported private-environment restart
combinations remain explicit until their input path is qualified; never claim
new cross-platform support from this Linux proof.

## Bounded observations and limitations

The [actual startup evidence](restart-fence-evidence/startup-summary.json) records:

- Docker Desktop 29.8.1 and isolated rootless Podman 4.9.3: startup remained
  blocked before host approval, then ran and retried with no new host grant.
- Both engines: root with an explicit DAC-bypass capability could not modify
  the approval through the read-only bind. A separate writable-mount control
  allowed a change despite file mode `0444`.
- Both engines: a different launch nonce did not match the earlier record.
  Fresh nonce selection and exact-container association remain host duties.
- WSL2 subordinate-only namespace probe: the trusted preparer created a dummy
  recipient file at UID/GID 200, observed externally as 100200/100200. Recipient
  200 could read it; identity 201 could not. All subordinate-owned test objects
  were removed. No account/delegation edits, image code or real secrets were used.

The engine fixtures use trusted BusyBox/shell code. They prove the storage and
retry transport properties, not the production Go startup boundary. The
namespace probe is not complete preparation/race or secret-delivery proof.
Neither establishes crash-safe permission publication, live private-map
qualification, per-recipient engine mounts, cold boot or native Windows/macOS
behavior. Those checks must have explicit owning prerequisites/slices.
