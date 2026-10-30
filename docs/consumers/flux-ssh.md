---
status: Consumer request evaluated; requirements and design not approved
updated: 2026-10-30
summary: Operator-authorized packaged SSH for direct host sessions; separate from the rootless multi-identity campaign.
---

# Flux SSH

## Outcome and source

Flux requests an official Debian 13 OpenSSH daemon managed through Reploy, with
distribution-maintained daemon/authentication libraries and ordinary direct
sessions in a Bullseye host filesystem. Programs entered in that host filesystem
still use its libraries. Host kernel/runtime and native emergency SSH maintenance
remain separate obligations. Per-user environments are deferred.

Source: the 2026-10-30 email titled **[Reploy hotfix] Service-specific host-session
profile blocks packaged SSH qualification**, with `reploy-host-profile-gap.md`,
`packaged-ssh.blueprint.yaml` and `controlled-host-gate-results.json`. The sender
reports the current handover in [Flux commit b991828ce484](https://github.com/omry/flux.firefang.net/commit/b991828ce484)
and raw-engine preflight in [Flux commit 95f01d2210c0](https://github.com/omry/flux.firefang.net/commit/95f01d2210c0).
Those Flux revisions were not independently inspected during this evaluation.
The attachments were read without saving their original bytes in this repository.

The sender labels this P0 for its SSH qualification. That does not change the
maintainer's current Reploy priority: multi-identity E. See [the backlog](../BACKLOG.md).
This record captures a consumer proposal and evaluation, not accepted product
requirements, an approved API, implementation authorization or qualification.

## Requested behavior

- Keep ordinary application defaults. A blueprint cannot grant itself host-root
  authority. Operator authorization must bind the service, admitted component or
  image, and reviewed policy; choosing an approved name is insufficient.
- Carry exact identity, retained capability/bounding sets, selected
  no-new-privileges behavior, seccomp, individual namespace and mount permissions
  through install, exec verification, restart, update, replacement and rollback.
  Reject unauthorized drift and unavailable authority before effects.
- Permit deliberately authorized host-session filesystem access and live account,
  key and host-key sources. Preserve atomic replacement, locking and revocation;
  do not embed credentials in images or graft Bullseye PAM binaries into Debian 13.
- Declare required devices, kernel/runtime trees and systemd/logind/journal
  endpoints individually. Keep stock OpenSSH privilege separation and pre-auth
  sandboxing. Host-root session authority is trusted host authority, without an
  additional containment claim.
- Qualify actual Reploy lifecycle behavior on a disposable Bullseye guest with
  synthetic accounts. Production changes require separate approval and verified
  administrative access independent of guest Podman/Reploy.

## Evidence and current gap

The sender reports 44/44 passing **raw rootful Podman synthetic checks** with the
official Debian OpenSSH package. These do not establish a Reploy deployment,
realistic host integration, ordinary sudo or rootless Podman support. The supplied
VM qualification matrix is explicitly **NOT RUN**. No disposable VM was created.

The supplied blueprint validates package acquisition and an `sshd -V` command;
it does not define a deployable host-session service. Illustrative capability,
NNP, namespace and Compose override fields are rejected, not proposed public API.

Reploy's evaluation independently checked that all five source files below still
match the attachment's hashes from [merged E2.2](https://github.com/omry/reploy/commit/944d564fc3565cf91f3d72af4f4730fbd6155b3a):

| Boundary | Current behavior |
| --- | --- |
| `internal/dockerdeploy/application_sandbox_plan.go` | Requires dropped application capabilities and NNP; prohibits host namespaces/devices and privileged mode. |
| `internal/dockerdeploy/execution_render.go` | Renders dropped capabilities and NNP; setup rights are separate from application rights. |
| `internal/probe/sandbox_exec_linux.go` | Drops the ordinary application's capability bounding set before exec. |
| `internal/probe/startup_verifier.go` | Requires empty capability sets, NNP enabled and seccomp filtering. |
| `internal/dockerdeploy/runtime_host_preflight.go` | Refuses root-runtime host binds and protected host filesystem sources. |

No engine, VM, SSH or live-service probe was run by Reploy for this evaluation.
The gap is a new host-session authority contract across these boundaries;
changing only the parser or generated Compose would not satisfy it.

## Relationship to multi-identity

[E's approved design](../MULTI_IDENTITY_E_DESIGN.md) covers non-root/user-scope
local rootless Podman with subordinate host IDs, NNP and a constrained namespace
policy. It does not authorize actual host-root sessions, rootful/system-scope
execution or arbitrary protected host mounts. Completing E will not itself close
this request.

Reuse explicit identity/capability intent, exact exec/descriptor checks, immutable
generation identity and lifecycle ownership where applicable. A new service
profile needs separate design and qualification; do not weaken E's contract,
reinterpret its proofs or add this scope to its remaining slices.

## Proposed preliminary scope and questions

Before design, ask Flux to distinguish the first required host session from the
eventual complete administrative shell:

1. Can initial qualification cover packaged sshd, real PAM/account restrictions,
   live key/account revocation, supplementary groups, shell/PTY/signals, file
   permissions and Reploy lifecycle, with sudo, rootless Podman/user services and
   broad host namespace/device access explicitly deferred? If those are necessary
   to the initial acceptance, retain them rather than claiming a smaller delivery.
   Can that preliminary scope retain NNP enabled? If not, identify the mandatory
   operation and observed failure that requires disabling it.
2. Provide a requirement-to-resource table: each namespace, mount/source and access
   mode, device/socket, capability and NNP setting; the operation that needs it;
   and the observed failure when omitted. The four synthetic-fixture capabilities
   are not the complete service policy.
3. Use one disposable guest to check stock pre-auth privilege separation/sandbox,
   real PAM policy, atomic account/key replacement and session cleanup. A raw
   engine experiment can reduce design uncertainty, but cannot replace eventual
   actual Reploy install/start/stop/restart/update/rollback qualification.
4. Keep native emergency SSH plus sudo independent of the guest container stack
   for every supported preliminary scope. Preserve the full requested host matrix
   as unfinished acceptance work, including reboot and recovery cases.

These are proposals for consumer agreement. No deferral or weaker acceptance
contract is approved by recording them. The next Reploy step, when prioritized,
is requirements/design work with measured authority needs and coherent slices.
No working-hour or implementation-LOC estimate has been established.
