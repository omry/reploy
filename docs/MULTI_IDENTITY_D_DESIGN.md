---
status: Proposed D LLD; public syntax and initial account-source limits await review and maintainer approval; implementation not started
updated: 2026-10-26
summary: Portable identity declarations, explicit capability policy, offline image evidence and reservation coverage for milestone D.
---

# Multi-Identity D Design

## Authority and stopping point

This document implements the D contract in the
[implementation plan](MULTI_IDENTITY_IMPLEMENTATION_PLAN.md#d-contract-identities-roles-and-generation-evidence)
and conforms to the authoritative
[security contract](MULTI_IDENTITY_SECURITY_CONTRACT.md).
These two documents are its explicit design dependencies. Source files, engine
specifications and disposable receipts below are implementation context.

The campaign architecture was approved on 2026-10-26. That approval settles
supervisor separation, empty application capabilities, retained reservations
and the mapped execution boundary. It does not approve the new public spelling
or image-source support limits proposed here. This document covers D only.
D produces locked declarations and evidence. It cannot admit, create, start,
restart or exec a mapped workload. E–G retain their own design and proof gates.
Public mapped execution stays inhibited until generic-launch after G3.

## Direction and existing primitives

Use the existing strict blueprint resolver and resolved-document envelope.
Add an optional field to the existing build lock. Use the existing final-image
publication, operation lock, mapping geometry and reservation retention.
Extend the bounded image-save archive reader in
`internal/dockerdeploy/docker_image_health.go`; do not introduce a second
export pipeline or a host filesystem extraction tool. Read account files from
image layers without running image NSS, hooks, entrypoints or application code.

The other considered mechanism was a never-started container plus copy/export.
It would add container ownership and cleanup, and require proof about volumes,
parent links and mutable filesystem state. Image-save avoids that lifecycle.
A full OCI filesystem unpacker would support more images, but D needs evidence
for three fixed paths. A bounded account-path projection is the smaller design.
It rejects transformations it does not implement instead of approximating them.
These choices introduce no service, allocator, cache, command or alternate build
backend. The supported application image remains Linux. Existing portable
builds retain their selected builder and endpoint.

## Proposed public input

There is one policy at `environment.runtime.identity_policy`, beside the
existing deployment-only `identity_mapping`. Example:

```yaml
environment:
  runtime:
    identity_policy:
      schema: 1
      users:
        worker:
          uid: 200
          primary_group: workers
          supplementary_groups: [shared]
      groups:
        workers: {gid: 200}
        shared: {gid: 201}
      supervisor:
        user: supervisor
        uid: 100
        group: supervisor
        gid: 100
        capabilities: [CAP_SETUID, CAP_SETGID, CAP_SETPCAP]
      roles:
        web:
          user: worker
          capabilities: []
  applications:
    app:
      executables:
        serve:
          source: app
          binary: server
          role: web
  commands:
    serve:
      executable: app.serve
```

The application contribution fields omitted from this syntax fragment must
still satisfy the ordinary application schema. This is a policy fragment,
not a complete buildable blueprint.

D1 adds `role` only to an accepted executable profile. Commands inherit the
role of their qualified executable. Commands, workload declarations, argv,
environment variables, native wrappers and deployment overrides cannot replace
that role. Every executable profile in a policy-bearing document must name
one existing role, including profiles not currently selected by a command.
The resolved command-to-executable-to-role inventory is deterministic. It is
input to E's future admission boundary, not permission to run an entry.
A shell or raw lifecycle path without an accepted definition stays unsupported
and rejects before effects; D does not invent a default shell role.

User and group map keys are image-local names, using the existing portable
lowercase Unix name grammar and 32-byte limit. Role names use that same grammar.
UIDs and GIDs are literal YAML decimal integers from 1 through 4294967294.
Quoted numbers, signs, leading zeroes, hexadecimal, floats, booleans, null,
`???`, interpolation and deployment overrides are not accepted in this policy.
Names, role references and capability names are literal strings with no
interpolation. This portable policy has no operation-time unresolved state.
Container ID 0 remains a separately justified runtime-required identity in E;
it is not an application or supervisor declaration in the initial cohort.

There must be at least one application user, application group and role, and
exactly one supervisor. Each user has one primary-group reference and a required
array of supplementary-group references. Empty arrays are valid. References
must exist. Duplicate supplementary groups or repetition of the primary group
reject. Application user names and UIDs are unique; group names and GIDs are
unique. The supervisor's user name/UID and group name/GID cannot alias an
application declaration. A user's primary and supplementary groups cannot
include the supervisor group. UID and GID number spaces remain separate.

Each role selects one user and inherits that user's exact group vector.
It has no second group-selection field. Roles selecting the same user share
credentials, process authority and ownership authority. Future secret and
interface grants must be consistent across them. D creates no grants or
independent security principal for a role label.

Unknown and duplicate YAML fields reject at every new mapping. Explicit null
rejects. Each required array must actually be an array, even when empty.
Reject duplicate map keys before constructing Go maps. Preserve numeric source
types until their strict validation; generic string interpolation cannot turn
a quoted or floating-point value into an ID. Limit the combined number of
users, groups and roles to 1,024 and each supplementary-group vector to 64.
These are proposed initial parser limits, not host resource policy.

`identity_policy` may be staged and built without a complete host mapping.
Existing mapping-only templates remain stageable and buildable. A role field
without a policy rejects. An explicitly supplied `runtime.user` together with
a policy rejects, since two application identity selectors would be ambiguous.
When omitted, the existing resolver's `reploy` build-account default remains
an internal compatibility input to the ordinary build pipeline. It is not an
application role or supervisor default. Do not change or multiply the existing
runtime-account installation mechanism in D. The final image must contain the
declared accounts; D never creates a missing declared account. An inactive
build account needs no mapping or authority merely because it exists.

## Canonical model and validation boundaries

Add `IdentityPolicy *IdentityPolicyV1` with `omitempty` to `EnvironmentRuntime`,
and `Role string` with `omitempty` to `Executable`. Absent new fields preserve
old canonical resolved-document bytes. Raw syntax keeps presence information
for the explicit `runtime.user` conflict. Under a policy the resolved legacy
runtime user must equal its unchanged build default and has no runtime authority.

The canonical policy has schema string `identity-policy-v1`, maps keyed by
validated names, numeric IDs as canonical decimal strings, sorted supplementary
group names, and explicit empty capability arrays. Application groups and
supervisor declarations remain distinct. Canonical role capability arrays are
always empty; supervisor capabilities are unique and sorted by their exact
`CAP_` names. YAML order is not policy authority.

One validator owns referential, disjointness and capability rules. Call it from
raw resolution and resolved-document encoding/decoding. After applications and
commands resolve, validate the complete executable role inventory. Canonical
JSON decode must reject unknown fields, wrong types, noncanonical order, nil
required arrays and invalid role references rather than trust a persisted
struct. Use the existing resolved envelope and canonical comparison, not a new
serialization or a permissive migration path.

Check raw policy literals and every raw executable `role` before
`resolveSyntaxVariables` runs. Temporarily exclude the policy from that generic
expansion, as the existing mapping declaration already is excluded. Reject an
interpolation token in a raw role selector before it could become a valid role
name. Resolved validation repeats the policy and complete-inventory invariants;
it cannot substitute for this source-type and presence check.

Preserve policy and role fields through application-contribution copying,
provider reconstruction and blueprint digesting. Audit every `Executable`
construction and every resolved-document copy. A complete document digest binds
the new portable input. Mapping overrides still change only deployment input.

## D2 capability policy

Application roles default to an empty array and accept only that array.
Any application capability, including `CAP_SETUID`, rejects. Unknown names,
case aliases, duplicates and engine-default requests reject. Supervisor
capabilities also default to empty; identity declarations never add them.
An explicitly requested set may contain only `CAP_SETUID`, `CAP_SETGID` and
`CAP_SETPCAP`. `CAP_SETPCAP` is for dropping the child's capability bounding
set, as exercised by the retained private probe; it is not package authority.
No capability is inferred from a username or application type.

Persist the exact accepted capability policy with the portable declarations.
A policy must declare both `CAP_SETUID` and `CAP_SETGID` to describe the
supervisor-to-application transitions, because the identities and primary
GIDs are disjoint. Without them, policy resolution rejects with an actionable
missing-transition-capability diagnostic. Thus an omitted supervisor capability
array resolves to empty and then fails this necessary-transition check. It
never silently gains privileges. E must still prove whether its implementation
needs `CAP_SETPCAP` and verify the exact final sets on every relevant thread.
D's static check is necessary policy validation, not proof of a launch mechanism
or sufficient authorization for execution.

Keep the ordinary `ApplicationSandboxPlanV1` drop-all policy. Do not widen its
setup capabilities or render supervisor flags through ordinary app rendering.
No engine defaults, ambient capabilities or file capabilities become accepted
application authority through a D lock.

## D3 immutable image evidence

### Export and bounds

Reuse the selected generated-builder endpoint and the existing pinned
`docker image save <immutable-config-id>` command. Never select a tag, pull an
image, run a container or call host account lookup. Keep cancellation, bounded
stderr, complete stdout consumption and successful command exit before accepting
evidence. Inherit the existing five-minute export deadline, 64-GiB archive
limit, 4-MiB config limit, 1-MiB manifest limit, 10,000 outer entries,
8-MiB aggregate outer-name limit and 4,096-byte path limit. These bound client
processing. Podman prepares a server-side archive; these limits do not bound
its temporary disk use. D does not claim host disk isolation.
D reports only a fixed failure category, operation and exit status on export
failure. It must not return the captured engine stderr or account/config bytes
through an error wrapper. Preserve the existing healthcheck caller's separate
behavior while sharing the bounded transport and parser primitives.

Refactor only enough of `readDockerImageHealthcheckArchive` to support optional
layer summaries. Keep its healthcheck behavior and regression coverage intact.
D callers use the same outer framing, duplicate-name, sparse-file, metadata
key, config-hash, terminator and zero-padding checks. Outer links may exist as
unused legacy aliases in a Podman save. Never follow them. The manifest's
selected config and layer members must themselves be regular files.

Process regular `.tar` layer candidates as they stream, before the manifest
may arrive. Record a bounded summary and SHA-256 of each complete layer byte
stream, including tar padding. At EOF require exactly one manifest record,
exact immutable config hash, Linux platform matching the selected image,
`rootfs.type = layers`, one nonempty ordered `diff_ids` array, no duplicate
JSON keys or ambiguous case aliases, and a one-to-one matching ordered layer
list. Each selected member exists, is regular, was completely parsed and hashes
to the corresponding DiffID. Compute the existing `RootFSSubject` over that
ordered list and require the selected image's locked value. A filename alone
is never evidence. Unreferenced legacy links grant nothing.

Support at most 128 layer candidates, 100,000 headers per layer, 8 MiB of
aggregate names per layer, and 16 MiB of selected account content across the
whole archive. Each selected file is at most 1 MiB; each line is at most
64 KiB. Bound total summary storage by these aggregate limits. Reuse the
existing sparse-header rejection and require complete layer framing and
zero-only trailers. Reject compressed layers in this Docker-save reader.
Do not buffer whole layers, create extraction directories or write image paths
on the host. The initial format is the uncompressed regular layers exported by
the existing compatible save operation.

### Account-path projection

Only `etc/passwd`, `etc/group` and `etc/nsswitch.conf` supply account authority.
Normalize one leading `./` and a directory trailing slash; accept `.` only
for an actual root-directory entry. Reject empty, absolute, escaping,
noncanonical, overlong or backslash paths. Reject duplicate normalized paths
within each layer and sparse members. Unrelated normal links need not be
followed or interpreted.

The root and `etc`, whenever present, must be directory entries. Selected
account paths must be regular files, not links, devices, directories or FIFOs.
Reject descendants under an account-file path, and hardlinks whose source or
target aliases a selected account file or its ancestors. Reject any whiteout
that removes `etc` or a selected file, or makes root/`etc` opaque. This rejection
applies even if a later layer supplies a replacement. D does not implement
those account-path transformations. Unrelated whiteouts are inert to this
projection. Among supported layers, the last regular version of each selected
file wins in manifest order. Require all three final files.

This intentionally rejects some valid images. Supporting linked account
sources or account-path whiteouts later requires design, mechanism tests and
approval of the broader support claim. Do not add a fallback to `docker cp`,
image execution, host NSS or a guessed lower-layer file.

### Files-backed relationships

Parse immutable file bytes with bounded lines. Reject NUL, malformed records,
noncanonical or sentinel numeric IDs, duplicate names, numeric aliases and
conflicting relationships. Account/password fields and unrelated image config
values must not appear in diagnostics, evidence or logs. Ignore empty lines
and comments. A passwd record has exactly seven colon-separated fields, a
group record exactly four. Required declared names use the portable grammar;
unrelated names need valid nonempty record syntax, not that restricted grammar.
Reject `+`/`-` compat-source records. Require each account's primary GID and each
named group member to resolve uniquely in the files. Supplementary membership
for every declared user must exactly match its declaration after excluding its
primary group. The supervisor must match its dedicated name/UID/GID and have
no supplementary authority outside its dedicated group. Inactive accounts do
not become declared users or acquire mappings.
Verify every declared application group name/GID, including a declared group
that no user currently selects. An undeclared image group stays inactive.

`nsswitch.conf` must have exactly one `passwd` and one `group` directive, each
with `files` as its sole provider and no action clauses. Reject missing,
duplicate, continued, dynamic or alternate providers for those databases.
Other database directives are outside this identity relationship. Do not
execute a libc or assume an image's NSS module behavior. Numeric application
credentials remain authoritative; these rules prove the initial files-backed
name/number relationship only.

Output the closed `ImageIdentityEvidenceV1` object with exactly these fields:

| Field | Canonical value and reader check |
| --- | --- |
| `schema` | Literal `image-identity-evidence-v1` |
| `image` | Exact `providers.RealizedImageV1`; equal to the lock's final image |
| `platform` | Validated existing platform object; equal to the lock platform |
| `policy_digest` | `canonical.Sum("identity-policy", "identity-policy-v1", policy)` over the complete canonical portable policy |
| `files` | Exactly the keys `etc/passwd`, `etc/group`, `etc/nsswitch.conf`; each value has exactly `digest` (canonical SHA-256) and `size` (canonical positive decimal string within the file limit) |
| `users` | Array of existing `runtimeidentity.IdentityV1` records, exactly one per declared application user, sorted numerically by UID |
| `groups` | Map of every declared application group name to its canonical decimal GID string, including unused declarations |
| `supervisor` | One `runtimeidentity.IdentityV1` with the declared dedicated name/UID/GID and an empty supplementary array after excluding its primary GID |

Convert each user's primary and supplementary group names through the verified
group records. Sort supplementary GIDs numerically and exclude the primary
GID, as the existing identity type requires. Validate all identity records,
exact array/map membership, declaration equality and supervisor disjointness.
The archive writer must derive these values from the observed records and
compare them with the policy, not merely copy declarations into an evidence
struct. The reader repeats all structural, ordering and policy equality checks.
Reject unknown, duplicate and case-alias JSON keys; missing fields, null arrays,
noncanonical IDs and mismatched digests/image/platform reject. Retain no
password fields, raw file content or image environment. Role/capability policy
is referenced by its digest, not invented by the archive. File hashes retain
source provenance; they do not independently prove a relationship without the
verified writer and immutable enclosing build lock.

### Disposable proof and limits of the result

On 2026-10-26, an isolated rootless Podman 4.9.3 VFS store loaded two synthetic
images without executing image code. Their entrypoint was `/must-not-run` and
their config declared `/etc` as a volume. Compatible API image-save preserved
the exact config hash, ordered DiffIDs, regular account-file replacement,
account symlink and opaque-directory marker. No container was created. A
second isolated store confirmed the existing pinned Docker CLI save route.
Both private engine inventories returned to their initial values.

An in-memory projection prototype passed 15 positive/negative controls,
including last-file selection, related links, opaque markers after replacement,
traversal, duplicate paths, unexpected types and oversized account content.
This proves feasibility of the proposed narrow projection. It is not the
production Go reader, a production security qualification, or E runtime proof.
The first CLI fixture attempt stopped before image load because it reused a
store with a different runroot. The failed read is retained; the successful
retry used its own store and runroot. No live engine was changed.

Receipts and exact scripts are retained under
`~/.local/state/awd/reploy-mi-D-run_68f2fnm4p924n48te4kjxhdmnc/d-preparation/`:
`image-save-proof.json`, `cli-save-proof.json`,
`cli-save-attempt1-failure.json` and `account-projection-proof.json`.
The receipt manifest binds their exact hashes. Product tests must cover the
actual Go framing, parser, transport cancellation and publication boundaries.

The layer rules are grounded in the OCI
[layer specification](https://github.com/opencontainers/image-spec/blob/v1.1.0/layer.md).
Its required whiteout handling is why D rejects unsupported account-path
transformations rather than treating archives as flat files.

## D4 generation locking and coverage

Add optional `identity_policy` to `deploy.BuildLockV1`, containing the canonical
portable policy and its `ImageIdentityEvidenceV1`. Its closed object has exactly
`schema: identity-policy-lock-v1`, `policy` and `evidence`; both nested objects
are required and validated as specified above. Omit it entirely for ordinary
builds so their existing canonical lock bytes remain stable. Keep `lock-v1`;
old binaries already reject unknown policy-bearing fields and cannot consume
them as an ordinary lock. Do not add a separate mutable acceptance record or
copy image evidence into the deployment mapping ledger.

After `ValidateAndFinalizeBuild` produces the exact final image and before
`AssembleBuildLock` or publication, collect and verify D3 evidence. Bind it to
that final image, not the upstream image or runtime-account layer. On mismatch,
cancel publication and use the existing candidate-cleanup ownership. On read,
validate the policy, evidence shape, matching policy digest, final image and
platform. The existing build-lock digest in `EnvironmentGenerationState`
transitively locks it. An absent policy-bearing lock for a policy-bearing
blueprint rejects as missing evidence; it is never upgraded by guessing.

Update `CurrentBuildMatches`, lock rebinding and current-build verification:
reusing an image requires exact identity-policy equality, including all roles
and capability sets, and exact equality of the complete qualified
executable-to-role inventory. This inventory includes unreferenced profiles.
For a policy-bearing current generation, require the digest of the selected
`StateV1.Blueprint` to equal the lock's `BlueprintDigest` before using its
policy or command inventory. The existing loader does not already prove that
equality. A command's selected executable is therefore bound by the complete
document digest, not a caller-supplied role name.

Ordinary metadata rebinding cannot attach a different policy or executable-role
inventory to old evidence. Either change requires a new verified D3 evidence
pass before producing its new lock. A changed final image always requires
fresh evidence. A policy-bearing lock paired with a policy-free resolved
blueprint rejects too. Keep ordinary no-policy reuse unchanged. The installed
single-account adaptation path remains behind the mapped/policy execution
guard; it cannot transplant or adapt D evidence.
Do not modify `AdmittedGeneration` or replace an admitted supervisor through
this build path. E's admission must compare the candidate supervisor against
the prior admitted generation's retained lock and preserve its stable identity;
D's candidate build is not permission to change that authority.

D4 validates application UID/GID coverage against the existing retained numeric
reservation geometry. Include every declared application's UID, primary GID
and supplementary GID, and every declared application group's GID even if
currently unused; do not count names or undeclared inactive image accounts. Reuse
interval membership for exact and bounded profiles, without enumerating host
ranges. Return sorted missing-ID diagnostics with the declaration path and
selected generation identity. Never allocate, remap, chown, accept an intent
or modify reservations to make coverage pass.

Keep this a pure validation function in `internal/deploy`, supplied the
validated policy/evidence and canonical retained geometry by the existing
locked build/mapping owner. In `dockerdeploy`, use `LoadRecordedCurrentBuildV1`
and the held operation lock to load the selected lock before coverage at the
staged mapping-configuration boundary and mapped current-build verification.
Reject a policy-bearing coverage request without its selected locked evidence.
Mapping-only staging before a policy/build exists retains B's incomplete-map
status; it cannot claim D coverage. Pure blueprint resolution/build does not
require complete host mapping values. Coverage does not turn an inhibited
reservation intent into accepted assignments.

The complete execution set, active map compilation, surplus new exact-assignment
rejection and reactivation of dormant admitted assignments belong to E1.
D's reservation check cannot substitute for them. Preserve every existing
host/principal/delegation check and retained-assignment rule.

Extend the current pre-effect guards to recognize `identity_policy` even when
`identity_mapping` is absent. Exercise run, test, shell, command, controlled
session, install and lifecycle branches. Policy-only staging and explicit build
remain available; payload paths remain blocked. No new bypass may use the
legacy `runtime.user`, an ordinary build account or a generated wrapper.

## Delivery slices and acceptance

These are conditional estimates from source inspection, not measured LOC.
The original D estimate omitted much of the bounded layer reader and reuse
invalidation work. The revised total is 1,950–3,150 production and 3,200–5,150
test/fixture LOC. The proposed decimal splits preserve the original D1–D4
responsibilities. Each PR targets at most 500 production LOC and has a hard
750 cap. If a change exceeds the cap, return to slicing before publication.

| Slice | Independently verifiable result | Production LOC | Test/fixture LOC | Prerequisite |
| --- | --- | ---: | ---: | --- |
| D1.1 | Strict users/groups/supervisor input, canonical roundtrip and policy-only execution inhibition | 350–550 | 550–850 | D-design; C2 |
| D1.2 | Complete executable-to-role validation and preserved resolved command inventory | 250–400 | 400–650 | D1.1 |
| D2 | Exact default-empty capability policy and necessary-transition rejection | 150–250 | 300–450 | D1.2 |
| D3.1 | Bounded files-backed relationship validator producing redacted, image-bound identity evidence from supplied immutable file bytes | 300–450 | 500–800 | D2 |
| D3.2 | Verified Docker-save layers to D3.1 evidence, with no extraction or image execution | 400–650 | 650–1,050 | D3.1 |
| D4.1 | Final-image evidence collection, build-lock publication and correct current-build reuse invalidation | 300–500 | 450–800 | D3.2 |
| D4.2 | Locked application reservation coverage, diagnostics and all-path inhibition regressions | 200–350 | 350–550 | D4.1 |

D1.1 deliberately cannot finish a policy-bearing build without the later
image-evidence writer. Add an explicit D1.1 rejection in the existing
policy-bearing build-completion/publication path before finalization, assembly
or publication can produce an evidence-free lock. Also reject policy-bearing
reuse/rebinding there while the evidence-aware reuse logic is absent. Tests
must prove that the finalize/publish callbacks and reference writes do not run.
D4.1 replaces that rejection with verified final-image evidence in the same
path; it does not add a second build route. Policy-only explicit build is
available at D completion, not during those intermediate slices. D1.1 delivers
strict portable identity input and canonical roundtrip with concrete safety
behavior; D1.2 adds complete executable role assignment. Neither permits
execution. D3.1's public-in-package boundary independently verifies
and redacts immutable byte inputs; D3.2 adds their authenticated image source.
Do not publish disconnected schema-only PRs or defer the declared acceptance
of a slice to another PR. D4.2 completes D4; no E implementation follows in
this delivery request.

For every slice: use Sapling, a full slice-prefixed subject and a concrete body;
add a Changie fragment for user-visible behavior; run focused Go and relevant
repository validation; obtain local review and exact-head remote PR review;
require every current `ci.yml` job, including Linux, Bash, all target smokes
and both Windows hosts, before approval. Derive the check names from the
workflow at that head. A cancelled or missing job blocks approval. Reuse
existing architecture approval; unresolved public input or material changes
return to the maintainer. Never merge or release.

Required product verification includes strict YAML and canonical JSON hostile
inputs; old no-policy byte compatibility; contribution reconstruction; role
resolution without implicit fallback; all forbidden capability combinations;
complete/truncated/oversized archives and zero/nonzero trailers; reordered
members; config/layer tampering; regular-vs-linked manifest members; malformed
NSS/accounts; aliases and exact group membership; final-image evidence timing;
failed export cancellation and no publication; stale/missing evidence; reuse
and policy-change rebinding; exact/bounded coverage and retained IDs; and
pre-effect denial across each application entry with no policy/mapping bypass.
No runtime admission or supervisor qualification is claimed by these tests.

## Decisions still requiring approval

After local and remote design review are clean, present only:

1. Public `identity_policy` and executable `role` syntax, literal numeric input,
   one fixed group vector per user, and conflict with explicit `runtime.user`.
2. Initial nonzero files-only image cohort, including mandatory regular
   `nsswitch.conf`, sole `files` providers, rejection of account-path links or
   whiteouts, and the stated bounded parser limits.
3. Revised conditional estimates and the seven decimal delivery slices above.

Everything else here selects internal mechanisms under the already approved
architecture. Approval of this document will not open E/G execution gates.
