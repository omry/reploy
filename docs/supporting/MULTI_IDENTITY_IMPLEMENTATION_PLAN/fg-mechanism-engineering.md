# F/G mechanism engineering

This is supporting engineering for the
[implementation plan](../../MULTI_IDENTITY_IMPLEMENTATION_PLAN.md).
It preserves the accepted root-mount, storage, mapping and controller decisions.
The maintainer accepted the conservative mount compatibility limit below on
2026-11-02, for the initial mapped cohort only. This document does not mark a delivery slice complete or open a public mapped execution guard.

## Source and proof boundary

Source head: `87899abecced4c5f8b3513ecee66793ecdd2df37`.
The [evidence manifest](mechanism-evidence/manifest.json) binds the actual
disposable sources and results. The Podman tests used a fresh private VFS store,
rootless Podman 4.9.3, crun 1.14.1 and WSL2 kernel
`6.18.54.1-microsoft-standard-WSL2`. Fixture UID 0 mapped to subordinate host
UID 100000. No application was released through Reploy's public path.

These are mechanism proofs, not eligible-engine qualification, a complete
both-profile campaign, native desktop proof, endpoint/peer-isolation acceptance
or completion of F/G. The archived scripts are evidence; replay them in a new
directory rather than overwrite the retained receipts.

## Private startup input

The existing mapped release reader consumes exactly one bounded frame and leaves
all following stdin bytes for the application. Keep that qualified v1 behavior.
The existing private FIFO relay uses another container exec and does not compose
with the mapped final transition. The production mapped private-input guard
therefore remains correct until the new composition is qualified.

The smaller candidate is a distinct internal private-startup mode:

1. Create the fresh container with trusted startup only and attach stdin before
   release. Bind the mode to the frozen plan and exact installation/attempt.
2. Establish the required network policy before waiting for host admission.
3. After all host admission checks pass, persist transfer intent and send a
   bounded private preamble, the unchanged E release frame, then application
   stdin. Never place values in argv, engine configuration or durable state.
4. Trusted startup consumes the exact private length with the existing bounded
   descriptor reader and validates assignments with the existing parser. It then
   validates the unchanged E frame. It must not prefetch application bytes.
5. Supply the parsed environment to the existing native mapped exec transition.
   That transition still owns credentials, capabilities, descriptor closure and
   exec. No stronger process enters an application-occupied namespace.
6. A partial, invalid, stale or cancelled transfer executes no application.
   Reconcile and retire an uncertain instance before admitting a replacement.
   Reattachment to a confirmed surviving process never resends the preamble.

The original admission deadline still bounds the whole startup. Reading private
input must not extend it. A new internal mode avoids changing the meaning of
bytes after a v1 release frame. Ordinary FIFO delivery keeps its existing
compatibility contract; no second mapped relay is introduced.

The disposable Go overlay used the actual private assignment parser, mapped
descriptor reader and release parser. Seven new cases passed: binary stdin
preservation, missing preamble, oversized length, truncated input, invalid
assignments, wrong release binding and cancellation. Existing mapped waiter,
descriptor and native exec-tail checks also passed. Dummy values reached a
child environment without entering its argv. This is pipe/process composition,
not proof of the complete engine or native Windows attachment path.

## Network setup before release

`mappedWaitV1` currently waits before calling `sandboxAndExecApplicationV1`;
the latter installs nftables. F needs effective observations before release.
Move the existing installer into the trusted pre-wait preparation phase and
keep one installer owner. Final credentials/capabilities still follow admission.
Do not leave an in-container supervisor or add a service.

The final disposable proof used two private container network namespaces and
a veth pair. Both in-container setup and a bounded host helper joining the
pinned namespace could install and read actual IPv4/IPv6 kernel tables before
any application release. A valid connection passed before filtering, failed
under deny-local policy and passed under allow-local policy. This establishes
the setup/readback mechanism; it does not qualify full egress, DNS, undeclared
peer denial, IPv6 traffic or endpoint publication.

Use the existing trusted startup for installation. A production observer must
bind readback to the exact held namespace and process, reject stale identity,
and verify rules rather than echo the request. Host setup is a proved fallback
mechanism, not a second selected installer. No host-wide firewall was changed.

The first fixture used loopback as a negative despite the intentional loopback
allow rule. The second lacked the synthetic resolver file and left the first
case's deny table in the shared fixture. These are fixture defects; their
receipts remain. The corrected third attempt passed. No production policy was
changed to obtain the pass. All private containers, images and stores were
removed.

## Writable root and protected startup

The root-mount decision permits an explicitly writable application root while
keeping trusted Reploy code protected. The mount proof established:

- Read-only image root rejects direct image-code writes.
- A separate writable mount can hide and replace image code despite read-only
  root. This successful attack is a negative control, not a product pass.
- A read-only bind at the top-level `/probe` path denied write, unlink and
  rename while ordinary application image files remained writable.
- Protecting only `/opt/reploy/probe` was insufficient when `/opt` remained
  writable: renaming that ancestor allowed a replacement at the original path.

The production design must protect trusted path ancestors too. The existing
top-level `/reploy-probe` location is a smaller protection boundary than adding
a nested path hierarchy. Its read-only backing must be the verified artifact
for the selected generation, retained for the exact owned container lifetime.
An arbitrary host binary or unverified source path is not sufficient.

The original storage receipt reported an empty private engine inventory but
left recreated, caller-owned Podman lock directories. The
[cleanup follow-up](mechanism-evidence/storage-cleanup-followup.json) proves
their removal and absence of matching proof processes. The original receipt
was preserved rather than rewritten.

## Accepted initial image-mount boundary

The maintainer accepted the smaller initial mapped boundary: with the default
read-only application image root, reject an application-declared writable managed
mount if its destination covers any existing image files. Empty, absent or
directory-only image destinations remain usable. This deliberately rejects
prepopulated data-only destinations too. For example, `/var/lib/mail` is usable
when empty; image-supplied default files beneath it cause refusal. Existing
`environment.mounts.root.writable: true` opts into broad application-image
mutation and relinquishes this application replacement protection. Trusted
Reploy startup/control paths remain protected independently. No new flag is
introduced. This limit does not change ordinary installations.

This replaces the unimplemented E interface's assumption that F will discover
a complete general code/dependency closure. A classifier based on executable
bits or ELF dependencies would miss scripts, plugins and dynamic lookup.
Instead, default root read-only policy protects image files and the conservative
mount check prevents writable managed mounts from hiding them. F must change
the request/proof contract and its real provider together. Historical E injected
proofs remain private-interface evidence, not qualification of the new provider.

### Image destination projection

Use the existing verified immutable image-save stream during build preparation.
Extend its metadata collection; do not extract image files, execute image code,
add a general filesystem snapshot, or repeat image export on every launch.
Verify the selected config, platform, ordered layer hashes and rootfs subject
using D's existing immutable-image authority. Apply whiteouts to lower layers
before same-layer additions, merge directories, and remove descendants when a
non-directory replaces a directory. Treat hardlinks and every non-directory
entry as image content, regardless of mode or size. Resolve destination aliases
within the image root, never on the host. Compare path components, not lexical
prefixes. Reject cycles, root aliases, non-directory destinations, ambiguous
layer traversal through non-directory ancestors and unsupported archive forms.

The disposable projection passed 22 cases, including same-layer and opaque
whiteouts, hardlinks, symlink aliases, zero-size/data files, root aliases and
file/directory replacement. Every case also rejects a mismatched rootfs subject.
The first 17-case attempt is retained. The prototype is not the production
bounded reader: F1.1.2 must retain D's parser limits and cancellation behavior
and add aggregate accounting before qualification.

Keep the existing 128-layer, 100,000-header-per-layer, 4,096-byte-path and
64-GiB archive ceilings. Bound the projection across the archive to one million
distinct indexed nodes, 64 MiB of retained path/link metadata and 32 alias hops.
Charge implicit directories and metadata extension records too; count bytes
before allocation, never only after a map insertion. The selected operation
context bounds total work and cancellation interrupts reading. A limit failure
rejects preparation, never switches to a weaker mount policy. These are internal
parser limits, not a new public size option. Existing supported D archive forms
and strict protected-account handling remain unchanged.

The published result is a small closed projection for declared destinations,
not the working index. Bind it to the immutable image/config/platform/rootfs,
normalized mount selection and root policy. Record literal/resolved destination,
absent/directory/non-directory kind and whether image content exists beneath it.
Store it as an optional mapped-only part of the existing build lock/generation;
the lock digest covers it. Absent fields preserve ordinary canonical identity.
Reuse requires the exact image and policy bindings; no caller boolean, changed
mount list, substituted lock or automatic rebinding can supply evidence.
A missing/stale required projection means rebuild or refusal. Changing the image
or mount policy creates new evidence before use. This adds no parallel authority
store and does not make a portable build into a host admission.

### Actual mount and trusted artifact observation

Before release, F's provider pins the inhibited process/root and namespaces and
reads the complete effective mount table. Bind literal and resolved targets,
source identity, read-only state and root policy to the locked plan. Reject
unplanned mounts, stale processes, writable aliases, substituted sources and
overlap with trusted startup/control paths, including their ancestors. Apply
the accepted image-content rule to declared writable managed mounts. Read-only
application mounts retain existing protected-path overlap checks; this approval
does not impose a blanket new image-layout restriction on them. Engine-created
mounts must match the exact qualified runtime contract, not an exemption by name.

For explicit writable root, protect the existing top-level `/reploy-probe` with
a read-only file bind. Use the startup verifier already named by
`ApplicationRuntimeLayerV1.Verifier.Artifact` and `.Size`. Extract the packaged
artifact for the locked platform, verify those bytes against the selected
generation, and retain them in the existing private operation/installation
workspace for the exact owned-container lifetime. The workload cannot access or
rewrite its host backing path. Admission rechecks the retained source and actual
bind; retirement precedes removing its backing. Reuse existing owned-reference
and pending-cleanup machinery. Do not invent a second binary store or allow an
arbitrary host executable. A missing matching artifact refuses before release.
The top-level placement avoids the demonstrated writable-ancestor attack;
all trusted paths still receive ancestor and mount-substitution checks.

## Trusted application-start completion

E's `mappedWorkloadEvidenceV1` is a private requirement, not an implemented
production provider. `StartupComplete` cannot come from an engine-running bit,
a release write, argv or application output. A trustworthy producer is needed
before the controller can claim successful mapped startup.

### Selected bounded mechanism

Use one private exec-status FIFO for the fresh inhibited instance, prepared in
its existing managed runtime workspace. This is not an application input or a
new service. The host opens and pins its read end before the reviewed preparer
sets the FIFO's subordinate setup ownership. Trusted startup alone opens the
write end before application release. F1's descriptor/type/owner/link checks
apply; record the exact resource and attempt before effects. Both ends must
match the same observed FIFO object. Never open a caller-selected external path.

Send a bounded closed startup header over this private channel. Bind it to the
normal installation/operation/generation/revision/image/plan tuple and exact
owned process, using the trusted constructor's binding. This metadata contains
no secret value and is not a portable approval token. Reuse the existing bounded
canonical frame primitives; cap the header at 64 KiB, reject duplicates, unknown
fields and noncanonical/partial/wrong headers. The disposable magic/token header
is only a proof fixture; production uses this exact typed binding.

After the header and descriptor identities match, the host unlinks the FIFO
through its held managed-root descriptor and verifies link count zero while
retaining its read end. No application runs yet. Only then may the normal host
admission/release sequence proceed. Startup retains the write descriptor through
final exec, with explicit raw-descriptor ownership. No private path or writer can be
reopened by the workload. A pipe alone is insufficient if its path remains
available after release.

Open the writer as a raw nonblocking, close-on-exec descriptor, outside Go
`os.File` finalizer ownership. Verify that it is above the standard streams
and has both flags before entering the native tail. Prepare every pointer and
failure buffer before that boundary. The tail performs these raw operations:

1. Detach the current thread's descriptor table with
   `close_range(status_fd + 1, UINT_MAX, CLOSE_RANGE_UNSHARE)`. This preserves
   the status writer and lower descriptors while closing the higher range.
2. If the writer is not descriptor 3, use `dup3(status_fd, 3, O_CLOEXEC)` in
   this now-private table. Do not overwrite descriptor 3 in Go's shared table.
   If the writer is already descriptor 3, retain its verified close-on-exec flag.
3. Close descriptors 4 and above immediately with ordinary `close_range`.
4. Execute the application. Successful kernel exec closes descriptor 3 before
   untrusted code starts and destroys the other Go threads/table references.

On any operation failure, write one fixed four-byte errno record through the
still-owned nonblocking writer, then exit through the raw tail. Track the
writer's actual descriptor until a successful duplication changes it. No error
return, Go runtime call, allocation, logging, callback or cleanup executes after
table detachment. Retry only interrupted bounded writes. This uses E's existing
close-range/UNSHARE operation and ordinary descriptor close-on-exec; it does not
require the newer whole-range `CLOSE_RANGE_CLOEXEC` flag or raise the kernel floor.

Once armed, every normal trusted startup failure must produce a failure record
before closing/exiting. Do not allow an ordinary return/finalizer to close the
writer while an inhibited waiter remains live. Raw descriptor ownership and
prebuilt pointer/buffer liveness must remain explicit in every leader/nonleader
branch. The existing final
UID/GID/groups, P/E/B/I/A, securebits and NNP transition still runs before this
tail. This is a proposed refinement of descriptor closure, not reuse of E's
old close-before-exec qualification as proof of new behavior. It preserves the
required absence of setup descriptors in untrusted code. Change and qualify
the single mapped transition; do not add a resident observer or stronger exec.

### Host acceptance and uncertainty

The host accepts only the exact armed channel, its complete header, clean EOF
with no failure/trailing bytes, and a fresh positive post-EOF kernel live-image
observation through the existing held process-directory/pidfd pin. Bind the
same start identity, namespaces, maps, generation and container observation;
read the actual kernel executable object, not argv or an app marker. An inode
change is not required: a valid application may exec the same binary.
EOF alone, or EOF plus an unready pidfd alone, is ambiguous with process death
and is not success. The qualified observer must distinguish exit teardown from
a live executable image after the channel closes, including nonleader exec.
If the kernel/process permissions or runtime cannot supply this observation,
refuse completion rather than invent `StartupComplete`.

This proves the selected exec handoff, not application health. Run existing
required hooks/readiness afterward before the persistent lifecycle becomes up.
An already exited process does not produce a live startup/handoff claim.
Transient entry completion retains its separate exact-exit semantics; do not
reinterpret a finite command as a persistent master's startup. Detachment needs
its own qualified transport and a fresh matching live-instance observation.

If the controller dies before the existing startup/lifetime transaction records
completion, the status descriptors and unlinked object cannot be reconstructed
as approval. Reconcile and retire that exact uncertain instance before another
admission. Never resend private input or synthesize an acknowledgement from
engine presence. A confirmed recorded surviving process can be reobserved
without another startup protocol or input delivery. Retirement precedes removing
any remaining owned backing; ambiguous cleanup retains inhibition/reservations.

### Proof, alternatives and required qualification

The selected reserved-descriptor refinement passes five disposable process
cases: same-binary nonleader exec with natural and deliberately high source
descriptors, missing executable, failure before exec and cancellation before
release. The compiler output contains six raw syscall call sites and no Go
runtime calls in the tail. The natural writer was descriptor 4; the forced
writer was descriptor 64. The source-descriptor-3 branch remains an explicit
product qualification case. Both success cases observed a live kernel executable
after EOF, no inherited private descriptors and no remaining status path.
The first two failed fixture receipts are retained: the fixture wrongly assumed
Go left descriptor 3 free. The corrected fixture duplicates its raw writer to an
available high descriptor without overwriting Go's shared table.

These are bounded process/FIFO feasibility results on this WSL2 kernel, not
complete mapped credential or adversarial-race qualification. Earlier successful
whole-range-close-on-exec proofs remain historical evidence; they are not the
selected kernel requirement. Exact sources and receipts are listed in the
[mechanism evidence manifest](mechanism-evidence/manifest.json).
Existing pinned executable observation remains useful, but inode change alone
misses same-binary exec. An application marker is untrusted and cannot replace
this channel. A host tracer/privileged service is materially broader and is not
selected. The FIFO reuses F's managed runtime preparation instead of adding a
host listener, durable acknowledgement file or another authority store.

The selected refinement also passes all five cases through raw rootless
Podman 4.9.3, using a fresh isolated VFS store, read-only image, empty capability
set, NNP and fixture map `0 -> host 100000`. The host pins the FIFO before
subordinate ownership, observes the inhibited kernel process, unlinks the
status/canary paths before release and uses non-TTY stdin. Both same-binary
cases acknowledge; all three failure/cancellation cases refuse. No private
descriptor reaches the application. This is not Reploy's compatible-create
path, full credential transition, both profiles or accepted native ABI coverage.

The original container receipt records successful cases and incomplete cleanup.
The [reserved-descriptor cleanup follow-up](mechanism-evidence/startup-status-reserved-fd-container-cleanup-followup.json)
records termination of the exact fixture pause helper through its verified
private environment, UID, command, start identity and pidfd. Caller-owned lock
directories were then removed; no fixture process or private root remained.
The earlier failed follow-up and original receipts are retained. Replays must
remove the engine objects/store, residual lock directories and exact pause
helper; store absence alone does not prove complete helper cleanup.
G1.5.1 must prove the full supported mapped tuple before using its provider:
both profiles and admitted ABIs; actual subordinate FIFO access and unlink;
same-object/type/owner/link/alias checks; GC and nonleader descriptor-table
liveness; source descriptor 3 and high-descriptor branches; complete/partial/stale headers and errno records; invalid frame,
timeout, cancellation, exec and close-range failures; kills and exit-teardown
races before/after arm, release, EOF and kernel observation; exact process/PID
reuse refusals; final credentials/caps and private/high-fd absence. Reuse E's
applicable tests without relabelling their old tail as new qualification.
No success-shaped EOF may promote a dead, inhibited, wrong or uncertain process.
Missing supported runtime evidence leaves the product startup guard in place.

## Effect on readiness and slicing

The private-input composition and network ordering now have concrete,
source-backed candidates. Their product providers, bounded transport,
interruption/reconciliation tests and supported-platform qualification remain
with their G1.0/G1.8 and F3 children. Do not count these prototypes again as
automatically required implementation changes when their exact evidence can
be reused.

The image-layout decision and layered projection mechanism now have evidence.
F1.0 still owns the complete confined-descriptor/trusted-source race matrix on
the eligible engine, F3.0 owns eligible network/endpoint qualification, and G1.0
owns the remaining direct/native transport proofs. The completed disposable
results are reusable inputs, not declarations that those product gates passed.
F1.1.2 separately prices the previously missing image projection/build-lock
producer. G1.5.1 separately owns the actual startup-completion provider and qualification;
its process/FIFO proof does not close that gate. Required review loops and final
LLD acceptance remain. Existing D/E
delivery is unchanged.
