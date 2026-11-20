# Lean controller engineering draft

This draft supports the [F/G implementation plan](../../MULTI_IDENTITY_IMPLEMENTATION_PLAN.md).
The maintainer accepted the lean per-installation controller direction for
detailed engineering. This is not a reviewed implementation plan or approval
to migrate existing ordinary installations. The comparison is in
[the evaluation](fg-controller-evaluation.md).

## Responsibility and scope

Reuse the embedded Reploy executable and extend the internal
`_service-container` entry. Install one foreground controller per persistent
installation under its existing host account. The OS manager supervises this
process. Reploy owns workload recovery policy and uses the selected engine for
effects and observations. The engine restart policy is `no` for this path.
There is no shared daemon, extra network listener, broker or new service account.

The common core handles installation state, attempts, admission, private input,
readiness, exact workload monitoring and checked retirement. Host adapters
install/start/stop/remove the controller and report actual manager state. They
do not independently choose workload restart semantics. Engine adapters report
actual object identity, state, status and errors; endpoint loss never selects a
different engine.

New mapped installations use this path after generic-launch qualification.
Ordinary installations retain their behavior until their applicable compatibility
and migration checks pass and the migration is explicitly accepted. Native
Windows/macOS system installs remain unsupported. Initial mapped execution
remains non-root local Linux Podman. Foreground staged/test/command execution
shares the narrow transport and admission operations without installing services.

Secret-manager access and clipboard integration are possible separate consumers
of this host-side lifetime. No such feature, credential source, permission, IPC
interface or plugin framework belongs to this F/G delivery.

## Data ownership and interfaces

The existing deployment state is authoritative. Extend its machine-local
deployment section with optional controller facts rather than creating a
second authority store. Bind the facts to the installation instance, selected
engine endpoint/identity and accepted generation. Keep them outside blueprint
resolution, provider identity and portable build evidence.

The controller facts need these logical fields:

- Current activation intent: running or stopped, independent of observed process
  state, bound to its activation epoch and actual host/session identity.
- Automatic boot/login enablement: separate manager/installation configuration.
  It is not inferred from the last `up`/`down` or from the workload retry policy.
  Observe the existing manager setting; do not create a competing mutable mirror.
- Monotonic control epoch: changes on stop, restart, revocation and replacement
  intent so a stale owner cannot commit another admitted start.
- Current controller owner ID and its held lease identity, using existing
  kernel-backed lease primitives. A PID or a manager unit name alone does not
  establish exclusive ownership.
- Current attempt ID, operation/pending intent and exact resource ownership.
  Reuse the existing mapping/native operation records and startup receipt for
  their respective facts instead of duplicating their authority.
- Retry accounting sufficient to preserve the selected policy across controller
  crashes: failed attempt count, outcome and next eligible attempt. Do not let
  repeated controller restarts reset a finite workload retry allowance.

`WorkloadLifecycleV1` remains the receipt for one process startup. Its `up`,
transfer-claimed and completed fields do not prove activation intent or another
process's startup. Readers and writers must preserve new control facts when
updating generation, installation or mapping state. Unknown/inconsistent facts
fail closed; no automatic adoption or downgrade by an old runtime is allowed.

Internal calls carry typed installation, epoch, generation, engine and full
container bindings. Do not add public receipt files or caller-supplied
qualification booleans. Short-lived existing CLI operations can remain where
they preserve bounded startup/cleanup and private input semantics; eliminate
CLI children that merely wait or carry streams for the workload lifetime.

## Ownership and control ordering

Acquire the installation operation lock before examining or publishing ownership.
If the retained controller lease is held, another invocation must not become a
second recovery owner. If it is abandoned, retain and reconcile its exact
pending attempt before publishing a new owner. Acquire the new owner lease
before publishing the owner record. Hold the lease over monitoring lifetime;
release the operation lock while only monitoring. Reuse the current lease
primitive without pretending a queue-entry lease already provides this protocol.

Public `up`, `down` and `restart` retain existing control admission/queue behavior.
For an installed controller, use its host-manager adapter rather than starting
an unmanaged competing loop. The control transaction publishes current activation intent,
epoch and pending intent under the operation lock. It releases that lock around
an external manager call that may require Reploy cleanup, then reacquires and
verifies actual state. It must not deadlock against its own stop child.

Starting/admitting an attempt holds the existing admitted operation ownership
through the bounded effects and release. Before every protected effect, verify
the epoch, owner and inhibition state. If an engine result is ambiguous, persist
uncertainty and retain the exact attempt/create binding. A late response or
manager restart cannot clear the pending intent or authorize another create.

## Recovery journeys

| Durable intent and observation | Required action |
| --- | --- |
| Stopped current activation, no owned runtime | Keep stopped during recovery of that activation. `up`/`restart` can request another activation; an enabled actual boot/login activation is separate. |
| Stopped activation with surviving runtime | Complete checked stop/retirement before any new activation; preserve uncertainty until verified. |
| Running current activation and same confirmed process | Reattach observation after endpoint, generation, container ID and process-start binding checks. Do not reinject input or replay startup hooks. |
| Current activation has interrupted admission or transfer | Reconcile the exact incomplete attempt. Never resend merely because the controller restarted. Retire an uncertain process before a new admitted attempt. |
| Confirmed workload failure and retry permitted | Retire all required descendants and attachments; retain data/reservations; create/admit a replacement for mapped execution; confirm its startup before reporting up. |
| Clean workload exit | Apply the selected policy. A process exit alone is not explicit operator stop. |
| Engine unavailable, wait disconnected or state inconsistent | Keep unknown/pending state. Retry bounded observations with backoff. Do not infer death, duplicate the workload, switch endpoint or claim stop success. |
| Revocation or security inhibition | Inhibit starts and all replacement owners before cleanup. Suppress untrusted hooks where required by the existing contract. Verify exact retirement before success. |

E's mapped waiter is one-shot and bound to a create-time deadline, process and
zero restart count. Reconnecting to a surviving process does not release it
again. A failed mapped workload uses a fresh container/admission; simply
restarting the old engine object cannot reuse E's one-shot frame. This design
therefore needs neither a retained admission file nor secret snapshots for
engine-only retries. The earlier permission for qualified unchanged-container
engine retries remains an available contract permission, not a second mechanism
to implement alongside this controller.

An OS manager restores a dead controller. Workload retries happen in Reploy,
with durable accounting, so desktop manager retry timing does not define the
application policy. All engine calls except intentional lifetime observation
have bounded contexts; lifetime observations are cancellable and reconciled
after disconnect. Process supervision is not proof of arbitrary-hang recovery.
Any stronger cross-platform hang guarantee requires separate evidence.

## Private inputs and authority

Reuse `.env` validation and the existing FIFO relay for ordinary compatibility.
Mapped fresh startup uses the distinct bounded private preamble defined in the
[mechanism contract](fg-mechanism-engineering.md#private-startup-input), before
the unchanged E frame and exact application stdin. Its parsed environment feeds
the existing final native transition. Do not adapt the occupied-exec FIFO relay
into a stronger mapped helper. Retain the mapped guard until composition,
credential/descriptor closure and actual engine transport qualify.

Refresh sources only for a new admitted process. A confirmed surviving process
retains its environment without reinjection. Missing or invalid input prevents
the next required delivery. Partial/ambiguous transfer requires exact retirement
before replacement. Never put values in state, engine configuration, argv,
units, task XML, logs or output. No durable secret snapshot is introduced.

The host controller stays outside application namespaces under the installation
account. It does not restore a stronger resident Reploy identity supervisor
inside the container or weaken same-UID roles sharing authority.

## Narrow engine transport

Reuse `dialLocalDockerEndpointV1`: Unix sockets on Linux/macOS and the existing
`go-winio` named-pipe dialer on Windows. Reuse endpoint pinning and direct PTY
attachment. The Podman identity client remains Podman-specific; do not weaken
its engine checks to create a misleading shared client.

Add only the lifetime operations actually needed:

1. Exact-container HTTP wait with bounded status/error decoding, request/body
   cancellation, selected API version and lost-observation classification.
2. Direct attachment where the existing lifetime-long CLI is replaced. Preserve
   startup-before-application ordering, stdin/EOF and TTY behavior. Demultiplex
   non-TTY stdout/stderr incrementally with bounded buffers and backpressure.
   Reject malformed/truncated frames and prove half-close on each supported
   local transport. Keep exit status independent of attachment EOF.
3. Exact-object reinspection after wait/stream loss. Never turn an HTTP failure,
   missing observation or arbitrary 404 into successful retirement without the
   existing ownership and cleanup checks.

Do not pull in a full engine SDK or rewrite all short-lived create/inspect/exec
calls merely to remove a persistent wait child. Do not automatically retry
create or secret transfer after an uncertain effect. Preserve private error
redaction and no redirect or remote-endpoint fallback.

The [wait/memory proof](controller-evidence/wait-memory-summary.json) establishes
API waiting, real exit code and cancellation on the current WSL2/Docker Desktop
Unix socket. It does not establish attachment, native named-pipe behavior, Podman
or the complete Reploy controller. Windows's existing dialer is reusable code;
that is different from qualified native stream/EOF behavior.

## Platform allocation and acceptance

Price shared state/recovery and engine transports once. Price systemd user
installation separately from adapting the existing system unit. Price macOS
user launchd and Windows user Task Scheduler adapters separately. Preserve the
existing system-scope limits and host account; no elevation or login-session
substitution. WSL distribution lifetime and Desktop readiness require their
own evidence. Boot-before-login and login-session lifetime are not interchangeable.

Qualification must cover two invocations competing for ownership; controller
death at every startup/transfer/stop boundary; surviving-process reattachment;
engine outage before and after effects; finite retry accounting; explicit stop;
revoke; update/rollback and uninstall; source input disappearance/change; and
retained mappings/data. Manager removal must not report success while its
owned workload remains uncertain. Existing controlled-session parent-loss
cleanup and foreground cancellation retain their separate contracts.

Measure the full Reploy process tree at idle and during bounded streaming,
disconnect/recovery and repeated workload replacement. Record peak and steady
RSS/PSS/private memory where available, CPU, versions and installation count.
The minimal Go waiter is a feasibility baseline, not the controller's forecast.

## Start/stop, automatic activation and retry policy

The maintainer selected the systemd separation. These are three independent
controls, implemented through the existing Reploy installation/control surface:

- `up`/`start` and `down`/`stop` control the current activation. They do not
  enable or disable automatic boot/login activation. `restart` requests a
  checked stop and a fresh start; it does not change enablement either.
- Existing `enable`/`disable` controls govern automatic manager activation.
  They do not implicitly start or stop the current workload. Reuse the existing
  installed systemd commands in `internal/cli/control.go`; extend checked
  adapter dispatch where the new supported user managers require it. No new
  command family, blueprint field or combined `--now` option is selected here.
- `environment.workload.restart` governs retry after workload exit during an
  activation: `no` (default), `on-failure[:N]`, or `always`. `N` is a positive
  decimal retry count, not a total start count. Explicit stop suppresses these
  retries; it is not an instruction to disable the next boot/login activation.
  No additional `unless-stopped` value is selected.

For example, an enabled mail installation with `restart: "no"` starts when its
qualified boot/login activation runs. A workload exit leaves it stopped without
retry. `down` also stops it for the current activation. It starts at the next
eligible boot/login if still enabled. To keep it stopped there too, use the
separate existing `disable` control. Conversely, `up` can start a disabled
installation now without enabling it for later. `enable` alone does not mean
the workload is already running.

Linux system installation already enables its unit independently of its start
command. Preserve that existing installation behavior. New user adapters must
expose and verify manager enablement through the same control distinction,
record their supported boot/login prerequisites, and never silently change an
existing ordinary installation's activation settings. Installation activation
and update preserve the selected enablement. Missing/ambiguous manager state
cannot be replaced by a desired-state boolean. WSL distribution startup,
Linux user-manager lingering and desktop user login retain their actual limits.

### Activation and crash recovery

Persist the current activation epoch, running/stopped intent, exact controller
owner/lease and attempt/retry facts in existing deployment/operation state.
This supports recovery; it is not a permanent on/off preference. A controller
crash or reconnect within the same activation does not undo `down`, reset an
exhausted retry allowance, or fabricate an explicit start. A fresh `up`/`restart`
or a genuine enabled boot/login activation can establish a new epoch only
after prior uncertain operations have been reconciled. A surviving exact process
is reobserved without startup hooks or private-input replay.

The owning adapter must distinguish an actual activation from restoration of
the same controller using the existing controlled operation, host/session and
real manager evidence. A changed PID, invocation ID or controller restart alone
is not sufficient. No public caller boolean can assert a boot/login activation.
The native qualification gate must prove this classification for each adapter;
an unsupported or ambiguous case remains inhibited rather than bypass retry
policy. The Linux system control's existing direct `enable`/`disable` path is a
reuse seam, not proof that new user/native adapters already satisfy it.

An explicit start begins its retry allowance. Consume retries only after a
confirmed workload failure and persist consumption before issuing the next
start effect. Engine unavailability before any workload start does not consume
it; fatal admission/configuration refusal remains blocked, not an automatic
create loop. A healthy interval does not silently reset the allowance. A genuine
new activation begins a new allowance; controller-only recovery preserves it.
Use bounded, cancellable observation/backoff, and qualify interruption before
and after retry consumption. Exact scheduling constants remain internal; they
cannot change which exits, activations or attempts are eligible.

### Stop and security fences

`down` records stop intent for the current activation before fencing the manager
and runtime. Releasing the operation lock around manager stop avoids cleanup
deadlock. Report success only after exact retirement is verified; a crash keeps
its pending stop and ownership recoverable. `disable` changes future activation
without being evidence that a currently running workload has stopped.

Revocation, preparation inhibition and unresolved cleanup are durable security
constraints. Boot/login, `enable`, `up` or a new control epoch cannot clear them.
Only their existing checked recovery/authority transitions can do so. Every new
container still requires fresh host admission. This preserves the security
contract without turning ordinary stop into permanent disablement.

Existing ordinary `docker.workload.restart` stays compatible; specifying it
together with the new field is an error. New mapped execution cannot fall back
to engine-owned retry. Ordinary installations without the new controller retain
their existing behavior until explicit reviewed migration. An unqualified
adapter must refuse the new policy rather than silently delegate it.

## Readiness

Concrete manager adapters, exact policy parsing/translation, native stream
proofs and F's storage/network observations remain engineering/qualification
work. This draft assigns their responsibility; it does not claim those gates
closed or that the whole F/G slice inventory is implementation-ready.
