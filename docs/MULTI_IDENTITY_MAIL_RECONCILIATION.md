---
status: Selected architecture and public input; final document review and stamps pending; no implementation or runtime qualification
updated: 2026-10-27
summary: Preserve the stock mail process model inside an explicitly bounded workload authority, without trusting image-provided masters as Reploy supervisors.
---

# Multi-identity mail process model reconciliation

## Authority and decision boundary

This is a proposed amendment to the
[security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md),
[campaign architecture](MULTI_IDENTITY_IMPLEMENTATION_PLAN.md), and
[D LLD](MULTI_IDENTITY_D_DESIGN.md). The maintainer decisions below select the
amended direction; the contract and plan now carry that direction. Their changed
bytes and this detailed reconciliation still require fresh review and stamps.
Mapped execution remains inhibited. This proposal implements no product behavior.

The recommendation in this document replaces the D draft's recommendation for
supervisor ownership, UID 0 and capability declarations. It does not silently
claim old stamps cover changed bytes. The D draft is aligned to the selected
direction; complete affected review and stamps before publication. There are no
two supported identity APIs or competing resident supervisor implementations.

The starting Reploy head is `9c2c34391a9b4ebbf152c241bc9cdee28b4fe12f`.
Exact baseline SHA256 values are:

| Document | SHA256 |
| --- | --- |
| Security contract | `7be51c521d8613331a6002506a5777ed3aafff5f5d7451b3b4eb4d5d2b369b28` |
| Campaign plan | `4f237ce0ae8a081c1d82721c4c070688010ef9e5eedc4aae3fd978f73614fbdd` |
| D LLD | `e93c24e8ec83d16696df4cd5411be4f3ff071c9302a6dd55c70c0ad2436b556d` |

The 2026-10-26 approval explicitly accepted a Reploy-owned identity supervisor,
empty application capabilities and whole-container replacement. That approval
remains valid for its recorded bytes. It does not approve this different process
model. In this reconciliation, the maintainer subsequently approved the one-shot
startup and native application authority model, with process identities and
required capabilities declared in the blueprint. The maintainer also selected
immutable container-provided code by default, with an explicit opt-in for
mutation, and directed that the agreed mapping profiles be preserved unless a
concrete reason requires a change. The maintainer then approved the public
`identity_policy` structure: scalar groups, scalar/object users, optional
`full_name`, same-name primary-group default with override, optional supplementary
groups, named capability roles and executable role selection. These decisions
do not grant every recognized Linux capability at runtime. The maintainer then
selected the simple initial files-backed image reader after its compatibility
limits were explained. D records capability requests; E must qualify actual
grants. Changed document bytes need fresh review; earlier clean review evidence
remains historical.

## Existing use case and the gap

Flux's [identity handover](https://github.com/omry/flux.firefang.net/blob/6fe3ddeee2c7ed267fdcaf2dd20a2dececa5eff6/migration/reploy-user-namespace-handover.md)
requires stock application identity separation, subordinate-only host mappings,
stable ownership and narrow explicit interfaces. All image-provided application
code remains untrusted. Its masters need bounded workload authority, not a
trusted Reploy preparation phase.

The existing disposable cases report the following. They used Docker scaffolds,
not the future supported rootless-Podman product path. These are sufficient
synthetic profiles, not production minima or complete per-thread observations.

| Workload | Observed/configured process model | Reported retained set |
| --- | --- | --- |
| Postfix | `postfix start-fg`; root `master`; observed children at image `postfix` UID/GID | `SETUID`, `SETGID`, `DAC_READ_SEARCH` |
| Dovecot | `dovecot -F`; root master and root-role services; package service accounts; SQL-selected synthetic `vmail` | `CHOWN`, `SETUID`, `SETGID`, `SYS_CHROOT` |

See the [Postfix assessment](https://github.com/omry/flux.firefang.net/blob/6fe3ddeee2c7ed267fdcaf2dd20a2dececa5eff6/migration/postfix-reploy-assessment.md)
and [Dovecot assessment](https://github.com/omry/flux.firefang.net/blob/6fe3ddeee2c7ed267fdcaf2dd20a2dececa5eff6/migration/dovecot-reploy-assessment.md).
The checked-in scripts test a root master and child identity classes. They do not
retain a full PPID/thread/capability inventory of every descendant. A complete
stock-binary process inventory remains mail qualification work.

The Dovecot quota-warning case matters: the fixed warning service invokes
`dovecot-lda`, which transitions to `vmail`. Giving privilege only to an outer
Reploy process would not reproduce that path. Precreating directories also did
not eliminate Postfix's steady-state identity-transition and queue-read needs.

Dovecot's synthetic interface exposes only auth, LMTP and quota-status sockets
to a capability-free consumer. Maildir, the private runtime directory and the
Postfix queue stay private. That direct-Docker control does not prove the later
shared-interface mechanism across disjoint rootless mappings.

## Alternatives and recommendation

1. Keep the privileged Reploy supervisor and require all application children to
   remain capability-free. This preserves the current contract but needs a
   demonstrated adaptation of the stock mail process model. An outer wrapper
   alone cannot supply the missing in-application transitions. Reimplementing
   package process management is outside the selected delivery estimate.
2. Designate stock masters as trusted supervisors. An immutable image identifies
   bytes; it does not establish trust. This would require separate audits and
   adaptation of master/helper credentials, capabilities, sessions, descriptors
   and restart behavior. It also changes Flux's treatment of application code.
3. Add mediation, nested namespaces or a broker around those masters. No such
   implementation is selected or proved. It adds another authority and lifecycle.
   A process with `SETUID`/`SETGID` can select a supposedly protected supervisor ID
   mapped in its own namespace; another account alone is not a boundary.
4. **Recommended:** admit a bounded application authority on the host, use trusted
   one-shot launch code, then let the stock application manage its internal
   processes. Keep the entire image-provided workload untrusted. No privileged
   Reploy identity supervisor remains in that user namespace after release.

Use one mapped launch architecture, with empty capability sets by default and
explicit reviewed capability requests. Do not implement two competing resident
supervisors or preserve an unimplemented path merely for compatibility with a
draft. Existing ordinary single-identity behavior is unchanged.

## Proposed process and trust boundary

The existing CLI/foreground/user-service owner remains outside the application
container. It checks current host delegation, reservations, installation and
generation authority. The existing runtime creates the exact owned container
with trusted admission/startup code only. No image entrypoint runs yet.

After the required mapping, namespace, storage and network checks, trusted code
sets the accepted entry credentials, closes private descriptors, applies the
accepted kernel policy and directly executes the selected application. It has
no persistent privileged Reploy sibling after payload release. Postfix or
Dovecot supplies its own application process management.

```text
existing host operation / user-service owner
  -> pinned local rootless Podman and admitted private mapping
     -> trusted startup code, before application release
        -> exec stock foreground master under accepted workload authority
           -> application-managed workers and identity-changing helpers
```

The diagram is a proposed control flow, not a runtime result. E must prove the
transition. Reuse `internal/probe/sandbox_exec_linux.go` and the common sandbox
plan as context; neither currently supports retained application capabilities.
The current helper drops every bounding capability and locks `NOROOT`.
Extending it requires a proved transition, not removing checks opportunistically.

The prerequisite fixture covers capability-bearing UID/GID `0:0` execution.
This is proof coverage, not a root-only portable D declaration rule. E must
qualify each supported entry's actual credentials and transition before release.
Container root is mapped exclusively to subordinate host authority. The launch
must preserve only the explicitly accepted effective/permitted/bounding set
across the final exec, with empty inheritable/ambient sets and
`no_new_privileges`. Normal UID-0 exec semantics are allowed only within that
accepted bounding set. E must prove securebits and exec behavior, including
negative controls. No file-capability, set-ID privilege gain or ambient route is
selected. The ordinary capability-free path retains its locked `NOROOT` and
drop-all behavior.

The package may drop privileges or retain a subset in its descendants. Reploy
does not promise that every native descendant has empty capabilities or retains
its initial UID/group vector. It promises that the namespace and capability
ceiling confine the entire workload. Internal package separation is application
behavior, not a Reploy guarantee against a compromised privileged master.

## Mappings and accepted identity authority

Preserve both explicitly selected profiles from the agreed design. An exact map
exposes only the accepted enumerated IDs. A bounded-range map exposes the entire
accepted private range. Neither silently substitutes for the other, and this
proposal selects no new default. A nonempty application capability request does
not by itself disqualify the bounded profile. Each supported combination still
requires production qualification before activation.

Resolve runtime-required accounts before admission. Require explicit acceptance,
locked image name/number evidence and mapping coverage for declared application
and runtime-required accounts. An identity reachable through `SETUID`/`SETGID`
cannot be described as protected from application code. Reject an unaccepted or
unproved required account before release; do not insert assignments or enlarge
the selected geometry automatically. Unused numeric IDs inside an accepted range
do not need invented image-account declarations merely because they are mapped.

There is no supervisor-only numeric identity in this proposed model. UID/GID 0
may be an explicitly declared application identity; its host mapping is never
host root or an ordinary host account. Every active exact assignment must be
explained by current accepted workload authority. Dormant retained assignments
remain reserved but absent from the active exact map.

An untrusted identity-changing process in a bounded map can select otherwise
undeclared in-range IDs. This is authority granted by explicitly accepting the
whole range, not an escape from that range. Account declarations do not narrow
the kernel map, and Reploy does not claim to restrict a native privileged master
to those declared accounts. The administrator reserves a non-overlapping host
range; Reploy validates its delegation, availability and recorded ownership
before execution. A collision or unavailable host inventory blocks admission.
Under the exact profile, unmapped IDs remain unavailable. Both profiles retain
their out-of-map boundary, private assignments and host/peer isolation.

Mappings remain explicit, private and non-overlapping across installations.
The administrator coordinates other installations and unmanaged services.
No global allocator, registry, enrollment, broker or composition API is added.
Stored assignments, accepted geometry and retained ownership are not rewritten.
An already accepted non-null supervisor binding from another process model
cannot be silently reinterpreted; reject until an approved migration or new
installation establishes the different authority. Delivered B/C records remain
historical evidence with their original meanings.

## Proposed capability contract

Empty remains the default. A blueprint must request each retained application
capability explicitly; selecting a user, role or package grants none.
The existing mail cases motivate these explicit runtime requests:

| Capability | Motivating operation | Important authority consequence |
| --- | --- | --- |
| `CAP_SETUID` | Package switches application UID | Can assume every accepted mapped UID |
| `CAP_SETGID` | Package selects GIDs and supplementary groups | Can select every accepted mapped GID |
| `CAP_DAC_READ_SEARCH` | Postfix mixed-owner queue traversal | Unix file modes alone cannot hide other mounted workload data |
| `CAP_CHOWN` | Dovecot's reported runtime profile | Can change ownership where its namespace and mount authority permit |
| `CAP_SYS_CHROOT` | Dovecot's reported runtime profile | Changes process root; requires closed outside descriptors and confined mounts |

This vocabulary is not a default bundle or proof that every combination works.
E and mail-compatible must establish each minimal sufficient set for an exact
image/config and its effective kernel policy. This table is not D's complete
portable catalog. D recognizes and locks target-Linux names as requests; E must
reject any request its reviewed product path cannot enforce. Merely recognizing
`CAP_SETPCAP`, `CAP_SYS_ADMIN` or `CAP_NET_ADMIN` does not authorize granting it.
No host devices, privileged mode, engine defaults or broader runtime authority
become permitted through a portable lock.

The [Linux capability documentation](https://man7.org/linux/man-pages/man7/capabilities.7.html)
defines these rights; the
[user-namespace documentation](https://man7.org/linux/man-pages/man7/user_namespaces.7.html)
explains why container and host identity differ. Kernel isolation does not
remove the need for restricted mounts, descriptors, syscall policy and host
delegation. Namespace-scoped capabilities are not permission to access arbitrary
host data.

For this cohort, bind the effective application syscall policy to the accepted
workload capability set and immutable generation. It must not inherit broader
conditional allows merely because trusted startup temporarily needs setup
rights. The pinned Docker JSON is a seed, not sufficient evidence of this
effective policy: its `CAP_DAC_READ_SEARCH` condition allows
`open_by_handle_at`, and its `CAP_SYS_ADMIN` condition allows namespace changes.

Before application release, require `unshare` and `setns` denial, deny namespace
flags in `clone` using the correct argument position on each supported ABI, and
return `ENOSYS` for `clone3` so normal libraries can use the checked `clone`
fallback. Preserve ordinary fork/thread creation as a positive control.
Explicitly deny `open_by_handle_at`, `name_to_handle_at` and mount escape paths;
do not rely on filesystem pathname checks after granting a handle-based bypass.
Retain only reviewed required identity and chroot operations. A missing or
unverifiable effective filter blocks release; the ordinary profile remains
unchanged. E/F must prove the kernel-enforced result and its generation binding.
Bounding sets and no-new-privileges alone do not establish this property: creating
a new user namespace can obtain capabilities in that new namespace.

## Roles, secrets and interfaces

A role remains a named selection of an entry user and explicit capability set.
Commands inherit their executable's role. It is not a new Linux principal.
The selected design retains this naming layer rather than adding a second
selector while solving the process-model gap. The maintainer approved its public
syntax and defaults as recorded above.

Within a capability-bearing container, treat all roles as one workload authority
for secret/interface grant consistency. This conservative rule avoids pretending
that names, initial group vectors or file modes protect a recipient from code
that can assume its UID or bypass permissions. Reject asymmetric recipient or
interface access claims in that container. Deliver only explicitly selected
files; do not copy secrets into every environment or role merely because the
workload can exercise the same authority. Distinct installations remain isolated.

Capability-free workloads retain the existing same-UID authority rule and
recipient identity checks. An explicit shared socket remains a separately
admitted cross-installation interface. Keep private mapping reservations disjoint;
J must prove its narrow interface mechanism or independently enforceable access
right. This proposal does not grant general shared storage or declare an
overlapping host-ID exception.

## Lifetime, replacement and runtime exec

The foreground stock master is the required workload entry. Its exit ends that
container instance. The existing host owner may recreate it only through fresh
admission; no engine restart policy bypasses that check. Revocation stops the
whole container, including package-managed descendants, and fences replacement.

The application may fork, exec, change among accepted identities, and replace
its own workers within its already admitted workload authority. Those internal
actions do not need a host authorization for every fork. This explicitly changes
the old architecture's prohibition on autonomous privileged child replacement.
They cannot acquire a new mapping, mount, capability, host interface or secret
source. Secret preparation still runs once per admitted runtime instance, not
once per package worker. An external Reploy command, hook or shell is a new host
operation and still requires fresh admission.

Runtime exec into a live capability-bearing container has an additional hazard:
untrusted code may assume a temporary launcher's UID. Do not inject stronger
trusted setup authority, private control descriptors or a privileged helper into
that occupied namespace. The compatible runtime/OCI path must establish the
final requested credentials and capability ceiling without such an intermediate
process. A verifier may have only the authority already accepted for that entry.
No `exec --privileged`, protected in-container socket or raw-entry fallback is
allowed. Until E proves this path for a requested entry, reject before effects.
Mail startup proof cannot be presented as runtime-exec qualification.

## Proposed D LLD amendments

The following are the approved replacements for the corresponding parts of the
D draft. The aligned D LLD records their full input and evidence requirements.

- Remove the `supervisor` declaration and its evidence/lock member. There is no
  application-selected Reploy supervisor account or capability list.
- Declare groups as `group_name: gid`, without an additional object or display
  name. User map keys are Linux account names. An omitted `primary_group`
  selects the declared group with the same name; an explicit value selects
  another declared group. `supplementary_groups` lists additional groups beyond
  the selected primary group and defaults to an empty array when omitted.
  Accept a user as `username: uid` or an object with required `uid` and optional
  user properties. The scalar has the same primary-group default and no
  supplementary groups. Normalize both forms to one canonical user object with
  explicit primary and supplementary groups before encoding and locking. Do
  not create a missing group or infer extra memberships from the image.
  User objects may also include optional descriptive `full_name`; it is not a
  second account name and grants no authority.
- Permit literal UID/GID 0 as explicitly declared workload authority; continue
  rejecting the unchanged-ID sentinel and malformed/noncanonical values.
  Reuse the ordinary root identity validator rather than inventing another
  numeric representation. Verify declared `root` and runtime-required groups
  from the immutable image just like every other account.
- Allow roles to request recognized target-Linux capabilities explicitly. Omission
  means empty. The raw prerequisite fixture proves UID0 execution only; E must
  qualify the selected entry's actual credentials and capability transition.
  Do not turn that fixture limit into a root-only portable D declaration rule.
  Preserve the selected exact or bounded mapping profile and its stated
  authority; requesting capabilities does not silently change that selection.
  No supervisor-transition capability is inferred or made mandatory for every
  policy-bearing blueprint.
- Lock the full policy and executable-role inventory as before. Include the
  accepted capability lists and zero IDs in canonical encoding, policy digests,
  image evidence, read/reuse comparisons and generation diagnostics.
- D4 checks declared accounts against retained reservations. E separately adds
  no identities: it resolves runtime requirements and verifies they were
  explicitly accepted and covered. A missing declaration or assignment rejects.
- Keep offline bounded image reading, no image/NSS execution, current-blueprint
  binding, stale-evidence rejection, and all pre-effect public mapped guards.
  The narrow initial account-file support limits were separately selected, not
  inferred from the mail process model. Public input spelling and its shorthand/
  default rules are approved as recorded above.

Illustrative policy fragment for Postfix; ordinary build fields are omitted.
The numbers are synthetic. The final policy must cover the selected image's
complete accepted account set and the runtime-required identities proved in E.

```yaml
environment:
  runtime:
    identity_policy:
      schema: 1
      groups:
        root: 0
        postfix: 200
        postdrop: 201
      users:
        root: 0
        postfix:
          uid: 200
      roles:
        mail-master:
          user: root
          capabilities: [CAP_SETUID, CAP_SETGID, CAP_DAC_READ_SEARCH]
  applications:
    mail:
      executables:
        serve:
          source: mail
          binary: postfix
          role: mail-master
  commands:
    serve:
      executable: mail.serve
      argv: [start-fg]
```

The list is an explicit authority request. It does not mark the image/config
supported. In particular, this fragment cannot execute if E requires another
mapped GID that has not also been declared, verified and reserved.

## Security-contract and campaign amendment map

These are exact intended semantic replacements, not retroactive edits to old
observations or approval records. Apply them coherently after approval.

| Current requirement/section | Proposed replacement |
| --- | --- |
| Declared identity; supervisor identity; trusted workload supervisor | Declared workload identities bound image names/numbers; one-shot trusted launcher and host lifecycle owner; image-provided masters remain untrusted application code |
| ID-02 and profile-specific identity boundary | Preserve exact and bounded profiles: exact exposes the accepted enumerated IDs; bounded grants the accepted entire private range, including otherwise undeclared in-range IDs; no mapped ID is protected from identity-changing application code; untrusted package policy is not the enforcement boundary |
| ID-03 and dedicated all-thread supervisor isolation | Prove no extra privileged Reploy resident process after release, no leaked setup descriptors, and no temporary stronger launch authority on live exec; preserve applicable ordinary drop-all and privilege-regain controls |
| CAP-01 and default-zero capability control | Empty default; explicit reviewed bounded application rights; inspect maximum sets across initial exec and package transitions without claiming all native children are capability-free |
| Stable lifecycle and LC-02 | Retain accepted mapping geometry/assignments and ownership; remove new supervisor binding from this model; reject incompatible pre-existing bindings; no silent migration |
| D contract and D1-D4 | Apply the D LLD replacements above; make capability and UID0 evidence part of the portable lock; host mapping remains deployment input |
| E contract and launch-design | Host admission plus one-shot trusted release, final capability exec transition, verified effective syscall policy, whole-workload authority and live-exec proof/rejection; no generic application process manager |
| E3 and G lifecycle | Package-internal worker replacement uses existing authority; top-level container replacement, external entries and revocation remain host-owned |
| I recipient secrets | Capability-bearing workload is one authority for grant consistency; no false per-role confidentiality; once-per-instance file delivery and export exclusions remain |
| J interfaces | Apply workload authority closure to participant grants; retain explicit narrow cross-installation interface admission and private mapping isolation |
| K2/K3 and mail-compatible | Qualify unmodified package process behavior against the selected mapping profile and minimal explicit capabilities; do not rename a stock master as trusted Reploy code |

Keep old conformance observations and accepted PRs as historical evidence. Add
new acceptance checks and applicability notes; do not relabel old supervisor
probe passes as application-native launch passes or erase failed candidates.
Do not change delivered A-C code or stamps as a substitute for new proof.

## Proof and delivery gates

### Disposable prerequisite result

On 2026-10-27, a trusted C fixture ran eight short-lived Linux user-namespace
cases under the existing operating user's subordinate delegation. The controller
read back each kernel UID/GID map and waited for every process. It created no
container, network resource or persistent subordinate-owned data and changed no
host configuration. This was a raw kernel probe, not a Podman or mail run.

The cases paired exact and bounded mappings with empty capabilities, the
reported Postfix set, the reported Dovecot set, and an omitted-identity-capability
control. All eight matched their explicit expected results:

- UID0 direct exec retained only the requested effective/permitted/bounding set,
  with zero inheritable/ambient capabilities, empty supplementary groups and
  no-new-privileges. The empty-set case used locked `NOROOT`.
- With the relevant capability, transitions to mapped application IDs succeeded.
  Transitions to a mapped numeric ID labelled as a separate control identity also
  succeeded. A label cannot protect that ID from identity-changing application
  code in the same namespace.
- An undeclared in-range UID succeeded in the bounded case and failed when
  absent from the exact map. Out-of-range/unmapped UID/GID attempts failed.
- Omitting the identity-changing capabilities denied the corresponding
  transitions, including the supplementary-group control.

Retained local evidence is under
`~/.local/state/awd/reploy-mail-design-run_5yye79jpd9dbcb98rg135yrv00/`:
`kernel-proof-result.json`, `kernel-probe.c`, and `run-kernel-proof.py`.
The C source SHA256 is
`b866b7df193d7e0b8fe5af83b153002bdf5ff0c71a0d498994321e0db40a93c7`;
the controller SHA256 is
`db969c3e953fa968078cd68b6e5006d59b0c3abf6c97eeb7923208ce6b415388`.
The first CLI mapping attempt failed before capability checks; its failure
receipt is retained. The corrected controller used the existing mapping helpers
and verified the maps. These observations do not prove host inventory
exclusivity, production descriptor closure, seccomp, live exec, all-thread
behavior, rootless-Podman integration or stock-mail qualification.

### Remaining production gates

Before calling this a supported mechanism, E/F/G and mail-compatible must prove:

1. Positive final UID0 exec with only the requested capabilities, zero
   inheritable/ambient sets, correct groups, the accepted map for each supported
   profile and no-new-privileges.
   Omitted/disallowed capabilities and all unmapped UID/GID transitions fail,
   including `setgroups`, filesystem-ID variants and `chown`. Reject the
   unchanged-ID sentinel at declaration/lock parsing. Kernel calls using their
   documented no-change sentinel may succeed; inspect the resulting complete
   credentials and distinguish no change from an unauthorized transition rather
   than requiring an incorrect errno.
2. No hidden supervisor identity or unaccepted runtime account appears. Exact
   mappings reject unmapped transitions. Bounded mappings permit otherwise
   undeclared in-range transitions when the process has the relevant capability,
   and reject out-of-range transitions. Verify complete host-range admission and
   host/peer isolation for both profiles. Capability-free controls still deny
   unauthorized identity changes. Never substitute one profile for the other.
3. Application code cannot reach host/peer authority, mounts, handles, private
   descriptors, devices, namespaces or protected runtime paths. Keep positive
   controls so missing objects/processes are not mistaken for denied access.
   Prove effective syscall restrictions after capability-conditioned rule
   resolution: namespace creation/join, clone variants, mount and handle escape
   negatives, with ordinary thread/fork and required identity/chroot positives.
4. Runtime exec either proves final requested authority without temporary setup
   privilege in an occupied namespace, or rejects before effects. Native child
   replacement cannot enlarge the admitted namespace/capability ceiling.
5. Container replacement, revocation, ownership retention, once-per-instance
   secrets and narrow interface lifecycle work under the rootless product path.
6. Exact locked Postfix and Dovecot images/configs pass the synthetic mail
   contracts, including queue ownership, Dovecot quota-warning/LDA, SQL-selected
   identities and shared socket behavior. Capture complete process/credential/
   capability inventories and minimize each accepted set. These are not current
   passes and no production cutover follows from them.

The reconciliation run may reuse existing exact evidence or perform narrow
disposable prerequisite probes. Missing production gates stay explicit and
block activation; a reviewed design is not a claim those tests ran.

The approved public input and process-model changes retain the seven D PR
responsibilities. The updated D draft conditionally estimates 2,050–3,350
production and 3,350–5,400 test/fixture LOC; these are planning ranges, not measured
results. Revisit affected D/E/K estimates when remaining support limits and
runtime mechanisms are settled. Keep the 500 production LOC target and 750 hard
maximum. This design run stops before implementation.
