# F/G lifecycle: Reploy-owned recovery and existing integration

This is supporting engineering analysis, not an approved service design or
evidence that public mapped installation works. The owning document is
[the multi-identity implementation plan](../../MULTI_IDENTITY_IMPLEMENTATION_PLAN.md).
The maintainer requires Reploy to own restart and recovery. Systemd is a
candidate host mechanism. The maintainer accepted engineering a lean controller
per persistent installation. Detailed service adapters and user-lingering
prerequisites remain subject to the final design and platform qualification.

The [restart/fencing evaluation](fg-restart-fencing-evaluation.md) records
actual Docker and rootless Podman observations. The maintainer subsequently
approved admission reuse for the same exact unchanged container. The
[controller comparison](fg-controller-evaluation.md) evaluates another mechanism
for Reploy-owned recovery. The controller direction is selected for engineering;
it is not implemented or qualified. See the
[controller draft](fg-controller-engineering.md) for the current state model.

## Required boundary

The [security contract](../../MULTI_IDENTITY_SECURITY_CONTRACT.md#required-security-properties)
requires fresh host admission for new container creation, external entry
and whole-container replacement. An unchanged admitted container may reuse its
approval on a permitted retry; each process still applies its final privilege
and sandbox transition. Reuse does not admit a replacement or changed object.
Revocation must prevent further starts and prove exact runtime cleanup while
retaining mappings and storage. This boundary does not itself require automatic
restart or a particular persistent host process. The maintainer separately
requires Reploy-owned recovery so that engine-specific restart behavior does not
define the product contract.

## Existing implementation

| Evidence | Established behavior | Limit |
| --- | --- | --- |
| `internal/dockerdeploy/platform.go`, `installBackendForScope` | Linux user-scope installs select the Docker-managed backend. | This backend does not install a systemd user unit. |
| `provider_install_host_commands.go` | The managed backend starts detached containers with Compose. | It is not a Reploy restart loop or a production mapped-admission adapter. |
| `podman_mapped_policy.go` | Mapped create explicitly sets `RestartPolicy` to `no` with zero retries. | This disables engine restart; it does not implement automatic Reploy restart. |
| `podman_mapped_waiter.go` | Startup observation checks the engine restart policy and restart count before release. | A generated request alone is not the effective observation. |
| `current_workload_lifecycle_mapped.go` | Private replacement orders exact old retirement before fresh admission; uncertainty blocks the next start. | Its trusted effect/observation provider is still an F/G handoff. |
| `mapped_entry_adapter.go` | The public guard remains; private replacement refuses before retiring the old instance because production composition is missing. | A refusal test is not working replacement qualification. |
| `control_script.go` and `internal/cli/control.go` | Existing installed wrappers dispatch explicit runtime controls through Reploy. | G must connect and qualify the mapped branch; ordinary behavior is not mapped proof. |
| `installed_service_container.go` and `provider_install_systemd.go` | A foreground Reploy runner and systemd restart exist for system installations. | The runner explicitly requires system scope and an unmapped document. Initial mapped system scope is prohibited. It cannot be claimed as an existing mapped user-service backend. |

The earlier architecture says to reuse an existing user-service runner. The
source does not provide that runner for the eligible user-scope backend. The
engineering mismatch is real. Installing a new service is one possible
resolution, not a requirement derived from the security boundary.

## Selected responsibility and mechanism comparison

Reploy owns the recovery decision, its mapping/generation checks and the
start/stop/revocation transaction. A host manager may invoke Reploy again. The
container engine performs the selected workload operations and reports their
results. The approved contract permits unchanged-container retries under
Reploy's policy. The delivered E implementation still enforces zero restarts;
the contract amendment alone does not change that implementation. Compare a
retained-approval/input mechanism with the controller before choosing which new
path to engineer. Both retain stop/preparation/revocation fences.

This distinction is not manual versus automatic invocation. Systemd can invoke
the same checked Reploy entry under the selected non-root account. The current
adapter's system-scope-only restriction is an implementation boundary to
engineer, not a fundamental reason these checks cannot run under systemd.

First evaluate adapting the existing per-application unit rendering and
foreground runner. Do not infer a need for an additional shared daemon, broker
or general service-management framework. The selected manager scope, host
setup, stop/revocation ordering and engine-availability behavior must be
specified and reviewed before dependent implementation.

### Explicit operation ownership

Reuse installed controls for explicit `up`, `down` and `restart`. Each new start
must pass the same locked generation, mapping, storage and sandbox admission.
Keep engine restart disabled. A failed workload remains stopped until another
explicit operation. Do not promise automatic startup after reboot.

This avoids a new service manager dependency. It still requires real G work:
destination ownership, start/replacement composition, detached lifetime handoff,
revocation and exact crash cleanup. It is not a shortcut around those gates.

If a blueprint requests automatic engine restart, fail before effects with an
actionable unsupported-policy error; do not silently discard the requested
behavior. Ordinary non-mapped restart policies keep their existing behavior.

This is not sufficient as the full recovery design. Retain it as the common
checked operation invoked by either an operator or the selected host manager;
do not silently narrow the agreed goal to manual recovery only.

### Automatic host-owned restart

For automatic recovery, host-side execution must invoke Reploy again. It must
participate in installation ownership,
stop/revocation, updates, removal and failure recovery. A systemd user unit is a
candidate mechanism. It would introduce user-service and, for unattended boot
and logout persistence, host user-manager configuration requirements.

Do not select this mechanism merely because an old plan referred to a runner.
Compare adaptation of existing service installation with simpler viable
implementations and size the actual additional work. Do not introduce a shared
daemon or broker.

Engineering must cover these cases consistently. The approved admission reuse
narrows the first item only when a selected mechanism supports an unchanged
container retry; the transaction fences remain:

- A permitted retry reloads the selected installation, current generation,
  retained mapping and fresh host authority before a new workload start.
- Explicit stop and revocation fence the recovery owner before success; an
  engine failure must not be mistaken for permission to undo operator stop.
- An unavailable selected engine produces a bounded readiness wait or diagnosed
  failed attempt. It does not select a different engine, map, profile or owner.
- Loss of observation retains uncertainty and owned-resource records. A retry
  must prove retirement before creating a replacement.
- A host manager schedules invocation. Reploy applies the recovery semantics;
  neither Podman nor Docker defaults define those semantics.

Existing configured restart-policy meanings and their portability need to be
traced before changing defaults or exposing syntax. The design must cover the
existing ordinary Docker install paths as well as mapped execution, and price
any required shared lifecycle changes explicitly. This does not authorize an
unreviewed rewrite of ordinary behavior or add mapped Docker support. Initial
mapped execution still requires eligible local rootless Linux Podman; owning
recovery does not remove that execution dependency.

## Install-scope and operating-system coverage

Reploy-owned recovery is a product responsibility, not a Linux/systemd-only
contract. Specify common outcomes first, then identify the host adapter and
actual prerequisites for each supported installation shape. A candidate systemd
implementation cannot count as a complete cross-platform design.

The current source dispatch and scope validation establish this starting matrix
(`platform.go` and `install_types.go`). These rows describe existing install
support, not qualification of new recovery behavior:

| Host and install scope | Current implementation | Recovery design obligation |
| --- | --- | --- |
| Linux, system | Systemd system unit and foreground Reploy runner | Reuse and assess startup, stop, retry and engine-readiness behavior; distinguish host controller authority from application UID/GID. Mapped system scope remains ineligible. |
| Linux, user | Detached Docker-managed Compose; no Reploy-owned user unit | Engineer recovery under the installation's actual non-root account. Evaluate adaptation of existing per-application integration before adding machinery. |
| WSL2, supported Linux scopes | Linux dispatch inside the distribution | Identify the actual init/service-manager and engine location. Distribution startup, host boot and user login are different events; do not claim native Linux boot behavior from a WSL run. |
| Native macOS, user | Docker-managed backend, including supported local VM-backed engines | Cover host login/reboot, runtime/VM availability and Reploy recovery ownership. Do not treat engine auto-start or Linux systemd behavior as proof. |
| Native Windows, user | Docker Desktop managed backend | Cover Windows session/login/reboot and Desktop/engine readiness through the actual native entry path. WSL execution is not native Windows evidence. |
| Native macOS or Windows, system | Rejected: no native system-service backend | Preserve explicit unsupported behavior. This coverage requirement does not silently promise a new system-install backend. |

Do not infer current Reploy implementation support from the broader substrate
possibilities in `INSTALL_SCOPE_RUNTIME_SUPPORT.md`; that document explicitly
describes host/runtime capability, not CLI implementation status.

For every applicable row, the design and acceptance inventory must cover:

- Install, first start, failure recovery, explicit stop/restart, update/rollback
  interaction and removal; a recovery owner must not undo a recorded stop.
- Host reboot, login/logout and missing host-manager prerequisites, with exact
  supported persistence guarantees and actionable unsupported cases.
- Engine initially absent, slow to become ready, lost during operation, and
  available again. Preserve the selected endpoint and installation identity.
- Concurrent control, controller failure and ambiguous engine results; prove
  owned-resource retirement before replacement and retain uncertainty otherwise.
- Revocation and cleanup where applicable. A new or changed mapped container
  requires fresh admission; same-container reuse needs its selected mechanism
  and fresh per-process transition. Ordinary installs keep their own checks.
- Host permissions and ownership, safe logs/diagnostics, and lack of accidental
  privilege escalation or silent fallback between user/system scopes.

Track common contract tests separately from native integration evidence. A
cross-compiled binary or an injected runner test is not a native reboot/session
or service-manager proof. Identify unavailable environments and pending checks
honestly, and include real adapter/platform work in the slice estimates rather
than pricing Linux as if it covered all supported hosts.

## Docker and Podman independence

The recovery contract must cover both Docker and Podman. Reploy owns restart
decisions, desired running/stopped state, retry handling, admission, cancellation,
revocation where applicable, and uncertain-operation recovery. These semantics
must not be inherited from engine defaults or independently reimplemented with
different meanings in each adapter.

Engine-specific integration translates workload operations such as create,
start, inspect, wait, stop and removal into the selected supported transport.
It returns actual observations, errors and operation identity to the shared
lifecycle. Reuse existing adapter boundaries; this separation does not require
a new generic engine framework or a native Podman backend.

The host manager invokes Reploy; Reploy's lifecycle invokes the selected engine.
An unavailable or restarted engine must not silently change recovery policy,
installation ownership or the selected endpoint. Engine-specific readiness and
error handling remain necessary even when the recovery decision is shared.

Cross the supported OS/install-scope matrix with the actual engine support
matrix. Run common lifecycle contract cases against both engine adapters, and
retain engine-specific integration evidence for readiness, lost observations,
stop/replacement and cleanup. Do not infer Docker behavior from Podman results
or vice versa. Do not invent unsupported combinations to complete a matrix.

Recovery portability is separate from execution-feature support. In particular,
the initial mapped cohort remains local rootless Linux Podman. Docker coverage
must exercise the supported ordinary installation path; it does not imply
mapped Docker execution is available.

## Focused verification

On source head `87899abecced4c5f8b3513ecee66793ecdd2df37`, seven existing test
functions passed in a focused run (package execution 3.692 seconds):

- `TestPlanProviderInstallHostCommandsV1DockerManagedUsesExactInstalledInputs`
- `TestMappedCreatePolicyOverlayPreservesOrdinaryRequestV1`
- `TestMappedRuntimePolicyRefusalBeforeFrameV1`
- `TestMappedWorkloadReplacementOrdersRetirementAndFreshAdmissionV1`
- `TestMappedWorkloadRetirementUncertaintyBlocksReloadV1`
- `TestRunCurrentWorkloadLifecycleV1RejectsFreshPrivateRestartPoliciesV1`
- `TestRunCurrentShellV1OrdersStaleCheckFinalGateAndInteractiveContainer`

These are source-level and injected fixture checks. They do not qualify a live
Podman installation, systemd user services, boot/logout behavior or the final
public inspection/admission/launch path. No production code or host service was
changed by this analysis.

## Readiness

Recovery ownership is resolved: Reploy owns it. The concrete adaptation,
operational prerequisites, state/effect ordering and qualification remain
engineering work. Do not treat the ownership decision as acceptance of an
unexamined systemd design. F's independent storage/network engineering can
proceed, but public activation remains inhibited until all governing F/G proofs
pass. Final slices and revised LOC totals must include the selected real work.
