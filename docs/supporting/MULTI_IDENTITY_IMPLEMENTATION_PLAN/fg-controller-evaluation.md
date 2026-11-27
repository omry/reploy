# Per-installation lifecycle controller evaluation

Status: current-agent engineering evaluation, 2026-11-01. This document supports
the [campaign plan](../../MULTI_IDENTITY_IMPLEMENTATION_PLAN.md). It proposes a
direction that the maintainer accepted for detailed engineering. This is not
reviewed LLD approval, platform qualification, ordinary-install migration or a
production behavior change. The current draft is in
[controller engineering](fg-controller-engineering.md).

## Recommendation

Use the existing foreground installed-service runner as the basis for one
Reploy lifecycle controller per persistent installation. Let the host manager
supervise Reploy. Let Reploy reconcile the workload and execute its selected
recovery policy. Disable independent engine retries for installations using
this controller. Ordinary one-off commands do not need an installed service.

This recommendation follows present requirements: automatic recovery, private
startup input, stop/revocation ordering and exact-object recovery. Possible
future features do not justify additional interfaces or a general framework.
There is no proposed shared privileged daemon, callback server, broker, or new
network endpoint. Use existing installed control commands and engine transports.

The approved alternative remains valid: an unchanged admitted container may
reuse admission for engine retries. The controller would normally run the
existing admission path when it starts another process, so it need not exercise
that permission. Adopting a controller does not require reversing the approval
lifetime decision or claiming host-administrator edits are a new attack case.

The controller direction has a better reuse argument than engine retries plus
retained private inputs. It is **not yet demonstrated to cost fewer lines across
all platforms**. Native manager integration and real crash recovery are the
largest remaining uncertainties. Do not adopt it on an asserted cost saving.

## Concrete behavior

For a persistent mail installation:

1. The host manager starts the installation's embedded Reploy executable under
   the installation account. It supplies paths and identifiers, never secrets.
2. Reploy reads the installation, current activation and independent enablement under the operation lock.
   It pins the configured engine and observes the exact owned container.
3. If the same confirmed process is already running, Reploy resumes monitoring.
   It does not inject secrets or replay startup hooks.
4. If the old process is conclusively stopped and policy permits another
   attempt, Reploy uses the checked start path. A new mapped start applies the
   required identity, capability, descriptor and sandbox transition. Existing
   private input delivery runs once for that process. For the proposed mapped
   controller path, retire the failed container and create/admit a replacement.
   E's one-shot waiter cannot simply release a restarted old container: its
   create-time deadline, consumed frame, PID and zero-restart checks still apply.
   Preserve data and reservations through the existing replacement contract.
5. If Reploy crashes, the host manager starts it again. Step 2 decides whether
   the workload survived; a manager restart is not proof of workload death.
6. An explicit stop records the stopped current activation before fencing
   recovery and stopping the exact workload. Controller recovery honors that
   stop. Independent enablement controls the next actual boot/login activation.

These are proposed acceptance semantics, not claims that the current runner
implements all six steps. In particular, activation state, separate enablement and controller ownership
need engineering beyond the existing process-start record.

## Source-backed reuse and gaps

Paths below are relative to the source checkout at
`87899abecced4c5f8b3513ecee66793ecdd2df37`.

| Source | Reuse | Missing qualification or adaptation |
| --- | --- | --- |
| `internal/dockerdeploy/installed_service_container.go` | Foreground host runner; operation lock; engine readiness; build checks; hooks/readiness; private injection; monitoring after releasing the operation lock. | Explicitly requires system scope and an unmapped document. Eligible mapped user installations cannot use it unchanged. |
| `internal/dockerdeploy/provider_install_systemd.go` | Per-installation embedded runtime, unit rendering, readiness notification and `Restart=on-failure`. | Only system scope. Stop directly runs Compose. No systemd user unit or controller heartbeat is implemented here. |
| `internal/dockerdeploy/installed_service_wait.go` | Exact-container wait; separates nonzero failure from manager stop intent. | Stop evidence is hard-coded to systemd system scope. Exit zero returns success; it does not implement `always` recovery. Engine observation failure is not proof of exit. |
| `internal/dockerdeploy/installed_service_engine_wait.go` and `installed_service_startup.go` | Bounded engine preparation, startup and cleanup. | Bound each attempt without exhausting recovery merely because the engine starts later. Define manager retry exhaustion and diagnostics. |
| `internal/dockerdeploy/workload_lifecycle.go` and `internal/deploy/workload_lifecycle.go` | Durable endpoint/runtime/container/start-time binding, startup completion and transfer claim. Confirmed process reuse avoids another injection. | A process record is not current activation intent, separate enablement, a retry counter, or exclusive lifetime ownership of a controller. Incomplete startup must be reconciled. |
| `internal/deploy/control_lease.go` and `internal/dockerdeploy/control_admission.go` | Existing locks, owner leases and queued control admission are useful primitives. | Leases belong to queue entries; they do not already establish one installation controller. Admission currently refuses mapped documents. Prove the ownership scheme and lock ordering rather than copying a second state store. |
| `internal/dockerdeploy/private_workload_environment*.go` | Defensive `.env` readers and private once-per-process relay, including Windows file validation. | Mapped execution still refuses private environment. Integrate that real gap; the ordinary feature is already implemented. |
| `internal/dockerdeploy/current_workload_lifecycle_mapped.go` | Exact retirement before replacement and refusal on uncertainty. | Production effect/observation composition remains an F/G obligation. A common controller does not provide the missing provider. |
| `internal/dockerdeploy/controlled_session_watchdog*.go` | Exact-resource cleanup and parent-loss handling for controlled sessions. | Linux only. It is not a workload restart service and must not be removed merely because a persistent controller exists. |

## Competing mechanisms and actual cost drivers

| Obligation | Engine retries with retained approval/input | Per-installation Reploy controller |
| --- | --- | --- |
| Restart scheduling | Engine implementation and effective-policy checks; portability differences remain. | Shared Reploy policy for workload attempts; host manager restores Reploy itself. |
| Mapped startup | New retained approval protocol, binding, publication and per-incarnation verification. | Adapt the existing checked start/admission composition; retain one-shot release unless a real requirement needs replacement. |
| Private `.env` on another start | Capture protected retained bytes, recipient delivery, lifecycle and cleanup; current relay cannot be called by the engine alone. | Reuse the existing private relay on a new checked start. No new retained-secret snapshot is needed solely for retries. |
| Controller crash | Native retries continue, but later host operations still reconcile exact object/process and fence preparation. | Restarted Reploy must reconcile a surviving process before creating or injecting anything. |
| Stop/revocation | Fence all engine retries and host replacement owners; prove exact retirement. | Persist stop intent and serialize it with all recovery starts; fence the manager and prove exact retirement. |
| Host boot/login | Engine availability and restart semantics; source/input and retained approval availability remain relevant. | New user/desktop manager adapters and startup prerequisites, plus existing engine readiness. |
| Engine unavailable | Engine owns its retries; Reploy cannot claim retirement or finish protected operations without evidence. | Record unknown state, retry observation with bounded attempts and backoff; never create a duplicate or switch endpoint. |
| Ongoing host resource cost | No additional persistent Reploy process solely for retries; current host entry paths can still retain Docker CLI children. | One Reploy process per persistent installation. Memory savings require replacing long-lived CLI children with in-process engine communication; measure the complete process tree. |

Shared costs must be counted once: exact identity, activation intent, manager enablement, stop fencing,
ambiguous engine outcomes, mapping retention, update/rollback, and cleanup are
required by either architecture. A global controller service is a third design
with additional authority and availability costs; there is no present need for
it.

The existing draft F/G estimates price the retained-approval/input candidate.
They are not a valid forecast for this alternative. Before publishing revised
totals, replace those candidate slices with source-grounded controller work and
price the platform adapters and qualification separately. Do not subtract the
whole private-environment implementation: it exists and both alternatives need
mapped integration. No working-hour forecast is used.

## Persistent Docker CLI memory overhead

The maintainer reports excessive memory from a Docker process retained for the
container's lifetime. Source confirms concrete lifetime coupling:

- `internal/dockerdeploy/installed_service_wait.go` invokes `docker wait` from
  the already resident installed Reploy runner. Both processes remain involved
  until workload exit or interruption.
- `internal/dockerdeploy/live_run_mapped.go`, `waitMappedLiveExitV1`, also invokes
  the CLI `wait` operation. Its foreground attachment has separate stream and
  cancellation obligations.

A controller that continues spawning `docker wait` merely keeps the same
overhead. Evaluate an in-process request to the selected engine for exact-object
waiting, and inventory any lifetime-long attach/log/exec children separately.
Both [Docker's Engine API](https://docs.docker.com/reference/api/engine/version/v1.40/)
and [Podman's compatible API](https://docs.podman.io/en/latest/_static/api.html?version=v5.0)
document container waiting. Existing local API plumbing is reusable evidence,
but the Podman-specific client in `podman_healthcheck.go` rejects Docker and
non-Linux identification. It is not already a portable general controller
transport. Reuse the existing Windows named-pipe dialer in
`docker_pty_attach_windows.go` and Unix dialer in `docker_pty_attach_unix.go`.
The complete wait/stream behavior on those endpoints still needs qualification.

This need is independent of choosing a persistent controller. The current
runner's CLI wait could also be replaced directly. The controller comparison
must count that smaller alternative, and must not credit the controller with
savings achieved only by an unrelated transport change. Reuse the same narrow
transport work if the controller is selected; a full Docker SDK or wholesale
replacement of short-lived CLI operations is not a prerequisite.

Acceptance should measure steady idle memory and CPU for the entire Reploy
process tree, including CLI/plugin children and any newly resident process.
Record versions, workload/stream mode, installation count, RSS and PSS/private
memory where available. Check that memory stays bounded during sustained output,
engine disconnection/reconnection and repeated workload recovery. Preserve exact
exit status, cancellation, stdin/EOF, stream backpressure and ownership checks.
These cases must not silently absorb or substitute for the separate RDA
cancellation hotfix.

The subsequent [disposable wait proof](controller-evidence/wait-memory-summary.json)
measured ten idle samples on WSL2/Docker Desktop 4.93.0, Engine 29.8.1, using
the Unix socket. Median proportional-set memory was 26,904 KiB for `docker wait`
and 6,683 KiB for a minimal standalone Go API waiter. Both observed the same
real exit status 7. Cancelling another API wait left the workload running. The
exact labelled probe container was removed afterward.

This measures an isolated transport client, not the complete Reploy controller
or the maintainer's reported workload. No leak, controller-wide saving or
resource limit is established. The first harness attempt mishandled cancellation
while reading a response body; its failed result and successful cleanup are
retained under the raw evidence directory's `attempt-01/`. The corrected probe
handles cancellation during both request setup and body reading. No production
transport changed.

## Supervision and failure boundary

The OS manager restores a dead Reploy process. Reploy reconciles and restores
its workload. This ends at the existing OS and engine trust boundary; it does
not require an infinite sequence of additional supervisors.

However, process supervision alone does not detect a controller that remains
alive but makes no progress. Existing bounded engine calls reduce that risk;
they do not prove recovery from arbitrary deadlock. Systemd offers
[`WatchdogSec=`](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html).
No equivalent portable heartbeat implementation has been established for this
proposal. Do not advertise bounded arbitrary-hang recovery on all platforms.
Price any stronger promise separately before adopting another watcher.

Required failure semantics for the proposed controller:

- Controller loss during confirmed running state does not by itself revoke the
  unchanged workload. Reattachment requires exact ownership and process proof.
  This availability policy needs the architecture decision below.
- Controller loss during admission/private transfer leaves an incomplete
  attempt. Never blindly resend secrets. Reconcile and stop/retire an uncertain
  attempt before a fresh admitted start.
- A failed `wait` or unavailable engine preserves uncertainty. A running old
  object and a missing observation must never lead to a second workload.
- Persist current-activation stop before acknowledgement. A controller crash
  cannot erase it; a genuine enabled boot/login activation is a separate event.
  Failed cleanup and security inhibition remain pending across either event.
- Do not hold the operation lock while waiting for a manager stop whose cleanup
  needs the same lock. Record intent, release for the external action, then
  reacquire and verify the result.
- Startup/retry policy must distinguish successful workload exit, failed exit,
  intentional stop and controller failure. Preserve current public restart
  meanings until an explicit compatibility decision changes them.
- Controller authority remains the installation account and selected endpoint.
  It is not application identity and gains no new host privilege implicitly.
  The controller stays outside the workload's namespaces. It does not reinstate
  a stronger resident Reploy identity supervisor inside the container, weaken
  descriptor closure, or change same-UID roles sharing authority.
- Private inputs remain in the existing protected source and one-shot delivery
  path. Missing or invalid input prevents the next start; it must not silently
  remove the setting or reuse unproved bytes. A surviving confirmed process
  keeps its existing environment.

Controlled sessions have an additional parent-loss cleanup contract. Persistent
installation reattachment must not silently weaken it. Reuse common operations
where suitable, but retain its exact cleanup owner until equivalent behavior is
proved. Likewise, transient shell/command execution does not acquire a permanent
service just to make the architecture uniform.

## Platform work and prerequisites

These are candidate mechanisms, not claims of implemented Reploy support.
Unsupported native Windows/macOS system installs stay unsupported. Initial
mapped execution stays local rootless Linux Podman; ordinary Docker coverage
does not expand that cohort.

| Existing installation shape | Candidate adaptation | Principal gate |
| --- | --- | --- |
| Linux system | Adapt existing systemd unit/runner. | Checked stop rather than raw Compose; exact reattachment; stop intent; engine outage; successful-exit policy. |
| Linux user | Systemd user unit using the same executable and account. | New installation/control adapter. [Lingering](https://www.freedesktop.org/software/systemd/man/latest/loginctl.html) starts the user manager at boot and retains it after logout; it is an explicit prerequisite if that persistence is promised, not a silent host change. |
| WSL2 | Linux adapter when the distribution actually runs the required manager. | Distribution startup/shutdown differs from Windows boot; engine may be Desktop outside the distribution. Do not promise a stopped distribution starts itself. |
| macOS user | Evaluate a per-installation launchd user agent. | New adapter; engine/VM readiness; removal and stop intent; actual native evidence. Apple's [launchd guidance](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html) limits a user agent to the logged-in user session. This is not pre-login system support. |
| Native Windows user | Evaluate Task Scheduler under the existing user identity. | New adapter and native proof. A [logon trigger](https://learn.microsoft.com/en-us/windows/win32/taskschd/starting-an-executable-when-a-user-logs-on) is not boot-before-login support. [Restart count](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-restartcount), [duplicate-instance policy](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-multipleinstances), and the [default execution time limit](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-executiontimelimit) require deliberate configuration and tests. |

Manager crash retries and application retries are different. In particular,
Windows Task Scheduler's [restart interval](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-restartinterval)
has a documented minimum of one minute. Do not delegate ordinary workload retry
timing to that facility and claim parity with a shared fast retry policy.
The minimal common design keeps workload retry accounting inside Reploy, with
durable limits across controller restarts, while each host manager restores
Reploy after its own failure. Exact policy translation remains LLD work.

Native platform proofs must cover installation, readiness, workload exit,
controller death, engine outage/recovery, explicit stop, update/rollback and
removal. Reboot/login/logout qualification must use the actual supported scope
and prerequisites. Documentation and cross-compilation are not that evidence.

## Evidence and readiness

Nineteen top-level existing tests passed, with no skips, in 3.943 seconds wall
time. They exercised production functions through fixtures: engine readiness
and cancellation, system manager stop identity, nonzero/zero wait results,
monitoring lifetime, bounded failed-start cleanup, fresh process claims,
confirmed-process reuse, observed shutdown, interrupted lifecycle recovery,
journal failure, and controlled-session parent-loss cleanup.

The [test summary](controller-evidence/summary.json) retains the exact command,
test names and output hashes. Raw events remain in
`temp/reviews/fg-controller-evaluation-20261101/`.
These establish useful existing primitives. They do not
qualify a new controller, real controller death, native managers, reboot, live
mapped execution, or universal hang recovery. The earlier
[engine experiments](fg-restart-fencing-evaluation.md) remain evidence for the
alternative; no live-engine experiment was repeated for this comparison.

Current-agent review checked the comparison against the security contract,
existing public decisions and source boundaries. There is no independent or
remote review, design stamp, implementation acceptance, or PR approval for this
proposal. Full F/G LLD review still follows completed engineering.

The maintainer accepted engineering this per-installation controller as the
persistent-workload recovery direction. The 2026-11-02 systemd-model decision
separates current start/stop, automatic activation enablement and exit retry.
Earlier durable-stop wording is superseded; security fences remain durable. Complete the interfaces, policy translation, failure
proofs and slice sizing before implementation. Stop and return for
direction if native supervision needs a privileged service, weaker integrity,
or broader platform support than described here.
