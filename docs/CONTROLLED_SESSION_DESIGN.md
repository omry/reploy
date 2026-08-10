---
status: Active
updated: 2026-08-09
summary: Capability-scoped execution sessions that inherit Reploy's global container sandbox.
---

# Controlled Execution Session Design

## Status

- Decision state: Focused review complete; high-level decisions approved
- Implementation state: Initial global sandbox prerequisites, trusted
  application-startup verification, controlled-session authorization, the
  framed protocol, lifecycle state machine, and immutable two-environment
  Docker planning are implemented. The lease-private Linux session channel is
  also implemented: it creates a fresh controller-owned Unix socket directory,
  exposes it through a read-only controller-only bind, verifies the claiming
  process's kernel-reported UID/GID, accepts one connection, removes the socket
  pathname, sends the frozen `opened` authorization, and provides bounded,
  serialized protocol I/O with distinct before-claim and after-claim failure
  observations. The planner binds exact controller and
  workload builds, runtime identities, mounts, masks, commands, and inert
  lifecycle commands while networking remains disabled. Private environment
  injection is rejected until its controlled-session launch path is wired.
  The lifecycle state machine includes its output-finalization barrier and
  timeout outcome. The synthetic PTY output pump
  now preserves byte order with one bounded flow-control chunk at a time,
  charges time already spent stopping the workload against the host-owned absolute
  finalization deadline, cancels blocked delivery, closes the source, and
  produces an immutable drained-or-failed status for the lifecycle barrier
  before returning.
  The Docker workload PTY adapter is implemented: it creates the frozen
  workload plan inert, establishes the Engine attachment before start, applies
  the initial and later dimensions through the Engine API, preserves exact
  input and output bytes, independently observes the container exit code, and
  exposes graceful and forced stop operations. The Docker controller adapter
  is also implemented: it requires the exact lease-private channel socket
  before container creation, creates the frozen controller plan inert, starts
  it at most once, independently observes its exit, exposes graceful and forced
  stop operations, captures the full container ID returned by creation, and
  pins all later lifecycle operations to that exact container. The
  backend-neutral session I/O bridge is implemented: it dispatches typed
  controller requests to an injected lifecycle handler, applies only
  lifecycle-accepted input and resize effects, forwards exact ordered PTY
  output as protocol events, gives lifecycle events a separate prioritized
  bounded write admission path, and reports request, backpressure, and
  disconnect failures without owning the channel or containers. A failed event
  write makes the framed transport terminal so a later event cannot be appended
  to a potentially partial frame. Full lifecycle orchestration and
  controlled-session networking remain later slices.
- Initial runtime: Linux containers under Docker
- Motivating clients: OmegaFlow recording, sandboxed AI agents, security
  inspection, and untrusted-code execution

This document records the decisions for a reusable Reploy controlled-session
capability and the global Reploy container sandbox policy on which it depends.
It does not define OmegaFlow recording syntax, browser actions, media formats,
or publication behavior.

## Decision Summary

Reploy will let a controller such as OmegaFlow control one host-created
session between exact controller and workload environment generations without
receiving Docker or arbitrary host authority.

The controller owns the session from its perspective. A host Reploy operation
performs the authorized Docker work, records the runtime resources under a
lease, and independently attests their termination. It does not understand
OmegaFlow beats, browser handoffs, or recording state.

A controlled session provides:

- one persistent PTY and shell;
- ordered input and output bytes;
- terminal resize;
- recorded terminal interrupts such as ordinary Ctrl-C;
- administrative termination and cancellation;
- structured exit and failure events;
- access to explicitly granted workload endpoints;
- generation-bound admission, lifecycle, cleanup, and recovery.

These are global Reploy container defaults, not additional treatment for
controlled sessions:

- no public or local network access;
- no host application-data paths unless explicitly granted by a runtime
  contract;
- no inherited host environment;
- no Docker, runtime, or daemon socket;
- no privileged container;
- no Linux capabilities unless separately and explicitly designed;
- verified seccomp and `no-new-privileges`;
- no host namespaces or host devices;
- runtime identity inherited from the Reploy execution scope for application
  containers;
- read-only project source access only when explicitly granted.

This baseline applies to every container Reploy creates. Provider-resolution,
build, validation, and materialization helpers may receive narrowly scoped
root, network, capability, or filesystem grants required by their provider
contract. Those are explicit construction authorities, not implicit exceptions
or authorities inherited by application containers.

Every application image receives a container-local account. Its blueprint name
is `environment.runtime.user`, defaulting to `reploy`; it is not a host account
selector or an authority grant. Staged and installed user-scope containers use
the invoking Unix user's numeric identity, while native Windows maps the
invoking SID to a stable nonzero Linux UID/GID. Installed system-scope
containers use the host account selected by
`environment.install.system.account`. The current Linux-container backend
materializes the local account through Linux account databases; other target-OS
backends may realize the same contract differently.

Root remains an explicit runtime identity, but selecting it does not produce a
generic runtime warning. Reploy instead rejects prohibited combinations with
precise diagnostics. Root does not implicitly grant capabilities, host input
or shared-state mounts, network access, privileged mode, or daemon access.
Root-safe `--output-file` and `--output-dir` are separate global runtime
contracts and remain rejected until their focused pre-release review and
implementation are complete.

## Context

Controlled execution separates two environments:

1. A trusted controller environment containing orchestration, policy, capture,
   inspection, or agent-supervision software.
2. An isolated workload environment containing untrusted source, dependencies,
   commands, services, and declared endpoints.

OmegaFlow maps these roles to a controller containing OmegaFlow, asciinema,
Playwright, Chromium, ffmpeg, codecs, narration, and publishing tools, and a
workload containing the recorded project. A sandboxed agent or security
inspection controller can instead supervise untrusted code without changing
the Reploy session architecture.

Giving a controller a host Docker socket would also give it authority over
unrelated containers, images, mounts, secrets, and host paths. Installing every
controller implementation inside its workload would erase the trust boundary
and burden workload blueprints with controller-specific details.

Distribution of portable controller dependencies is a separate concern defined
in [`REPOSITORY_DESIGN.md`](REPOSITORY_DESIGN.md).
Controlled sessions consume a prepared controller environment; they do not
define tool repositories, Playwright browser acquisition, or application
blueprint distribution.

Reploy already has useful foundations:

- staged desired state and exact generation identity;
- provider build locks and current-build verification;
- attached transient shell execution;
- live-run admission, cancellation, generation checks, and cleanup;
- managed mounts, private workload environment injection, and endpoint
  publication;
- deployment-scoped crash-recovery state.

The missing capability is a programmable, capability-scoped session contract
that a containerized controller can use without emulating Reploy internals.
OmegaFlow is the first concrete consumer, but the same primitive supports
sandboxed agents, prompt-injection policy components inside a controller,
security inspection, and execution of untrusted code without granting Docker
or unrelated host authority.

`reploy validate` already performs basic blueprint syntax and semantic
validation without creating staging state, contacting Docker, resolving
providers, or building. Remote references may update the source cache. A
controlled-session API must not describe this existing command as absent or
claim that basic validation proves provider resolution or build success.

## Goals

1. Let a trusted controller drive one isolated staged environment without raw
   Docker authority.
2. Preserve a long-lived shell and PTY across multiple controller operations.
3. Keep terminal behavior faithful enough for interactive tools and recording.
4. Bind every operation to one admitted run and exact controller and workload
   environment generations.
5. Make runtime termination independently verifiable by host Reploy.
6. Deny workload code access to the host-controlled session channel.
7. Keep network, mount, identity, secret, and output authority explicit.
8. Reuse the primitive for sandboxed AI agents, security inspectors, untrusted
   code harnesses, and other controllers where the same trust model applies.
9. Keep OmegaFlow recording semantics entirely outside Reploy.

## Non-goals

The initial controlled-session work does not:

- expose Docker, Docker Compose, or arbitrary runtime operations;
- define OmegaFlow beats, terminal-to-browser handoff, capture timelines,
  screenshots, casts, narration, diagnostics, or publication;
- implement arbitrary remote execution;
- provide a permanent Reploy daemon;
- support reconnecting or transferring ownership of a session;
- provide unrestricted numeric signal forwarding;
- design general Internet, DNS, domain, or proxy policy;
- implement a portable disposable writable-source filesystem;
- introduce shared or persistent workload caches;
- guarantee containment against a container-runtime or kernel escape;
- support privileged workload containers.

## Actors and Terminology

**Host Reploy operation**
: A normal, attached Reploy process running with the existing authorized Docker
  access. It performs runtime operations, owns the resource lease, and observes
  Docker state. It is not a permanent daemon.

**Controller**
: Trusted software that requests and drives a session. OmegaFlow is the
  motivating controller.

  A controller may internally compose an orchestrator, policy or
  prompt-injection detection subagents, recorders, and inspectors. Reploy sees
  one controller connection and one fixed capability set; that internal
  composition does not create additional session owners or authorities.

**Controller container**
: The trusted controller environment. For OmegaFlow it contains orchestration,
  asciinema, the PTY proxy, Playwright, Chromium, and media tools. Other
  profiles may contain an agent orchestrator, policy subagent, security
  inspector, or test harness.

**Workload container**
: The isolated Reploy application environment containing the untrusted shell,
  source, tools, services, and declared endpoints. For OmegaFlow this is the
  recorded application; for an agent controller it is the agent workspace.

**External session supervisor**
: The attached Host Reploy operation that owns the Docker TTY attachment,
  session protocol, workload signaling, lifecycle state machine, and
  authoritative runtime observation. No trusted Reploy process runs inside the
  workload container.

**Session watchdog**
: A short-lived host Reploy child process scoped to one live lease. It receives
  an immutable cleanup manifest, watches a private parent pipe, and removes the
  exact leased resources if the attached Host Reploy operation disappears. It
  has no listener, accepts no later resource selection, and is not a permanent
  daemon.

**Lease**
: Host Reploy's binding between one controller connection, admitted run,
  controller and workload generations, capability set, and the runtime
  resources created for it.

**Session handle**
: An opaque identifier valid only within its lease. It never conveys arbitrary
  deployment selection.

**Session channel**
: A private channel between the controller and the attached Host Reploy
  operation. It is created for one already planned lease and carries PTY data,
  bounded controller requests, and host-observed lifecycle events. It grants no
  session-creation or general host-runtime authority and is never mounted into
  the workload container.

## Architecture

```text
Host attached Reploy operation
├── validates one complete immutable session plan
├── creates the lease and inert Docker resources
├── records their exact identities and starts the session watchdog
├── starts the controller and workload containers
├── owns the Docker TTY attachment and framed session protocol
├── creates the lease-owned controller/workload network
├── monitors and cleans every leased runtime resource
└── independently observes lifecycle completion
         ⇅
         ⇅ private session channel
         ⇅
Controller container
├── controller orchestration
├── optional policy, inspection, or capture components
└── native clients for granted workload endpoints

Workload container
├── untrusted workload shell on the Docker-managed PTY
└── declared services and endpoints
```

The host invocation selects separate controller and workload staging
directories, validates both exact current generations and complete runtime
plans, and resolves one declared controller command plus its forwarded
arguments before either container starts. Host Reploy admits one live run,
creates both containers, and binds the session channel to the resulting lease.
The workload begins as an ordinary persistent shell without caller-supplied
workload arguments. The controller never requests creation of a deployment or
container and cannot select mounts, identity, environment, output destinations,
networks, or another generation through the channel.

Startup is synchronized. Host Reploy creates the leased resources without
starting untrusted workload execution, establishes the controller channel and
Docker TTY attachment, then starts the workload process and reports the
session ready. After that point the runtime plan is immutable. The channel
accepts only operations on the already-created session.

Host Reploy remains in both the PTY and lifecycle paths. It transports terminal
bytes without interpreting them, applies resize through Docker, performs
termination through container lifecycle operations, and independently observes
the exact containers. Workload code never receives the session channel,
even when its effective runtime identity is root.

## Trust Model

### Trusted

- the host Reploy executable and its state;
- the Docker runtime within its ordinary trust boundary;
- the controlled-session protocol implementation;
- the intended controller and controller image.

### Untrusted

- workload source and dependencies;
- workload commands and subprocesses;
- terminal output and escape sequences;
- network services exposed by the workload;
- files supplied by the project;
- the workload container after untrusted code starts.

The design also considers a compromised controller. It can
control and disclose everything explicitly granted to its session. It must not
thereby gain access to unrelated host data, Docker, other deployments,
undeclared paths, or undeclared networks.

### Protected Assets

- host Docker and runtime authority;
- unrelated deployments and their resources;
- host paths not explicitly granted;
- deployment `.env` and internal `.reploy` state;
- secrets not granted to this session;
- local and public networks not granted to this session;
- session-control integrity and lifecycle truth;
- private outputs and diagnostics until their owning client publishes them.

## Global Reploy Container Sandbox

Controlled sessions do not receive a special sandbox tier. Every container
Reploy creates starts with the same deny-oriented baseline:

- verified seccomp and `no-new-privileges`;
- no Docker or runtime socket;
- no privileged mode;
- no host namespaces or host devices;
- no inherited host environment;
- no undeclared mount, network, capability, or secret access.

Purpose-specific container classes then receive only their declared authority.
The application-runtime class includes:

- staged workloads, commands, and shells;
- installed user-scope workloads, commands, and shells;
- installed system-scope workloads, commands, and shells;
- controlled-session workload containers.

The effective blueprint, installation scope, and host invocation determine
identity, mounts, environment, endpoints, and other explicit grants before
startup. The controller cannot expand or change them or select arbitrary host
paths, Docker resources, identities, or networks.

Every application runtime container:

- uses an explicit numeric runtime identity rather than the image's configured
  `USER`;
- drops all Linux capabilities unless a separately designed feature grants a
  specific capability;
- enables `no-new-privileges`;
- uses a verified seccomp filter;
- receives no privileged mode, host namespace, host device, or runtime socket;
- uses a read-only container root filesystem plus only declared writable
  storage;
- receives no public or local network access unless explicitly granted;
- receives only declared mounts and private environment inputs;
- is subject to bounded process, memory, CPU, temporary-storage, and output
  policy where supported.

Reploy explicitly requests Docker's built-in seccomp profile. Before launching
untrusted application code, the trusted runtime bootstrap verifies that filter
mode and `no-new-privileges` are active and that the effective, permitted, and
bounding capability sets are empty. An engine or platform that cannot prove
this baseline cannot run Reploy application containers under this policy; it
does not silently degrade only because the selected identity is non-root.

Provider-resolution, build, validation, and materialization containers execute
package managers and other purpose-specific operations. Their provider
contracts must separately declare and justify any root identity, network,
capability, writable filesystem, or host-input access. Controlled sessions and
application containers never inherit those construction authorities.

## Controlled-Session Delta

A controlled session adds orchestration, not a stronger or weaker workload
sandbox. Its workload consumes the ordinary Reploy application-runtime policy.
Relative to an ordinary Reploy shell or application command, it adds:

- a controller-owned lease bound to exact controller and workload generations
  and one workload live run;
- a persistent PTY and typed host-mediated protocol;
- a private controller-to-host session channel;
- an immutable, prevalidated runtime plan;
- controller-loss teardown and independent host-observed termination;
- a session-scoped cleanup watchdog;
- bounded output transport and structured session diagnostics.

It does not add another runtime identity, broader storage or network access, a
Docker socket, or a privileged container mode. OmegaFlow recording and an AI
agent controller consume the same generic session mechanism and the same global
runtime sandbox.

## Capability and Authorization Model

Before starting either container, Host Reploy produces a host-side authorization
record containing:

- the controller and workload deployment identities;
- both exact current generation references and build identities;
- the opaque session handle and proposed live-run identity;
- both effective runtime identities inherited from their execution scopes;
- digests that bind both complete effective runtime plans, including commands,
  environment, network, mounts, masks, and inert lifecycle commands;
- the permitted session operations and logical endpoint identities.

The authorization record is portable immutable data; it does not serialize a
live connection or make ownership transferable. Host Reploy binds the record
to its admitted live-run lease, permits exactly one controller connection to
claim that lease, and treats that connection as the owner until it closes or
Host Reploy cancels the session. Connection loss ends the lease. The initial
protocol has no reconnect, ownership transfer, or operation that can extend the
lease by presenting an authorization digest again.

The controller does not receive a generic session-creation capability. After
creation, protocol operations do not accept a deployment name, mount, identity,
environment key, output destination, network, or raw endpoint destination.
They act only on the session and logical endpoint identities established by the
host-created plan. A generation change invalidates admission of a pending
session; it does not retarget a live session.

Logical endpoint identities are the exact names declared by the resolved
blueprint. Blueprint resolution and authorization validation share one
Docker-style single path-component grammar: lowercase alphanumeric segments
separated by `.`, `_`, `__`, or one or more `-`, with a 128-byte maximum. Full
image-reference syntax is not accepted. This keeps the immutable capability
record aligned with every blueprint that can reach runtime planning.

A unique private endpoint and opaque handle prevent accidental cross-session
use, but secrecy is not the sole security boundary. Isolation relies on:

- a session endpoint made available only to the intended controller;
- transport and process permissions;
- a typed handshake bound to host-created session state;
- server-side capability checks;
- exact generation and run identity;
- independent host lifecycle observation.

## Session Protocol

The protocol is versioned, typed, length-framed, and binary-safe. Terminal
bytes are never parsed as protocol messages.

### Controller Requests

- `input(bytes)`: write exact bytes to the PTY.
- `resize(columns, rows)`: set the PTY window size.
- `terminate`: request bounded graceful session termination.
- `complete`: after Host Reploy has emitted `workload_outputs_finalized`, declare
  that the controller has finalized its client-owned results. It does not stop
  an active workload and is rejected before workload output reaches a terminal
  state.
- `acknowledge_terminated`: confirm receipt of the authoritative `terminated`
  event. This payload-free protocol handshake is mandatory housekeeping, not a
  granted capability, and is accepted only after Host Reploy has successfully
  emitted the terminal result.

Admission cancellation is host-owned and is not a session-protocol request.
Host-terminal Ctrl-C while waiting removes the caller's queued operation. Once
admitted, host cancellation terminates that caller's session and cleans its
lease. The global admission queue reserves newly available capacity with an
internal `ready` state. The owning caller atomically chooses under the operation
lock between removing that unstarted reservation after cancellation and
claiming it as active. That `ready`-to-`active` claim is the authoritative
admission point; cancellation observed afterward uses normal active-operation
cleanup and no canceled request is replayed.

### Session Events

- `opened`: reports the effective dimensions, both runtime identities and
  generations, fixed session capabilities, and workload-output-finalization
  timeout.
- `output(bytes)`: ordered PTY output bytes.
- `workload_exit(status, reason)`: reports host-observed workload-shell
  exit.
- `terminating(cause)`: reports that the host-owned terminal transition began.
- `diagnostic(code, message)`: reports protocol, runtime, or cleanup failure
  without embedding secret values.
- `workload_outputs_finalized(status, reason)`: establishes that no further
  workload output can arrive. `status` is `drained` when every byte was
  delivered and `failed` when bounded finalization had to close an incomplete
  output surface.

Host Reploy emits the authoritative lease lifecycle result:

- `terminated(cause, workload_status, workload_output_finalization_status,
  runtime_observation_status, controller_finalization_status, cleanup_status,
  recovery_action)`.

`runtime_observation_status` is `maintained` only when Host Reploy retained
authoritative runtime observation through terminal-result creation. It is
`lost` when observation failed at any earlier point, including after workload
outputs were finalized or the controller completed. This independent monotonic
fact prevents a late observation failure from being hidden by an earlier
termination cause or otherwise successful statuses. A `lost` status always
makes the session invalid, regardless of every other terminal field.

`controller_finalization_status` reports the controller protocol outcome:
`completed`, `lost`, `finalization-timeout`, `not-completed`, or
`startup-failed`. It is never the controller process or container exit state;
the controller remains alive to receive and acknowledge this event. Its actual
exit state becomes available only after the result channel closes and is
reported with delivery-tail cleanup in the invoking host operation's
post-teardown result.

The reported cleanup status covers the workload and all lease resources that
can be removed while the result channel remains available. It cannot truthfully
cover the controller, private session channel, or other delivery-tail resources
needed to deliver that same event. Their cleanup is verified afterward and is
part of the invoking host operation's final result.

### Ordering and Backpressure

Output frames preserve the byte order read from the PTY master. The protocol
uses bounded buffers. A slow controller applies backpressure; Reploy does not
silently drop or reorder terminal bytes. Limits and timeout diagnostics are
explicit.

PTY and lifecycle streams use independent bounded flow-control windows.

Host Reploy owns workload-output finalization; it never waits indefinitely for
workload cooperation. Once termination begins, it rejects new output surfaces,
performs bounded graceful shutdown followed by forced container stop, and
continues draining the PTY. The immutable session plan carries a finite
output-finalization deadline. Protocol v1 defines an initial host-owned default
of 30 seconds; the effective value is reported by `opened` and applies to
workload shutdown, final buffered-byte delivery, and controller backpressure.

If every final byte is delivered and every output surface reaches EOF before
the deadline, Host Reploy emits `workload_outputs_finalized(drained)` only after
all earlier output has been consumed through its flow-control window. If the
deadline expires, or a runtime error makes complete delivery unverifiable,
Host Reploy forcibly closes the remaining surfaces, records the failed outcome,
and emits `workload_outputs_finalized(failed, reason)` while the controller
transport remains intact. Any event-frame write failure is terminal: the frame
header or payload may have been written partially, so Host Reploy closes the
connection and never appends another event. That failure latches
`controller_lost`; the output-finalization failure remains in the invoking host
operation's result even though the damaged channel cannot carry it. The
multiplexing layer guarantees that no output frame can follow a successfully
emitted finalization outcome. Failure cannot be converted into successful
completion by `complete` or terminal acknowledgement.

The barrier initially covers the PTY. A future workload output-file or
output-directory contract joins the same barrier after its files are closed,
validated, and published or have recorded an explicit failure; protocol v1
does not otherwise speculate about file payloads. Native network traffic is not
session output and does not pass through this barrier.

A PTY merges standard output and standard error. The contract does not pretend
to recover separate streams.

### Resize

The initial dimensions are part of session creation. A resize request applies
the platform PTY resize operation. Normal terminal behavior, including
`SIGWINCH` delivery to the foreground process group, follows from that
operation.

### Ctrl-C and Signals

Ctrl-C that belongs in a recording is ordinary PTY input byte `0x03`. The
remote terminal driver handles it normally, so echo such as `^C`, foreground
process signaling, shell behavior, and resulting output are observable and
recordable.

Administrative cancellation is distinct from recorded input. The public
protocol exposes bounded termination, not arbitrary numeric signals. Host
Reploy uses a fixed grace period followed by forced container termination.

### Session Ownership

The initial protocol has one controller, no detach, no reconnect, and no
ownership transfer. Loss of the controller connection begins bounded
termination. Future reconnection would require a separate authorization and
output-replay design.

## Protecting the Session Channel

Untrusted workload code must not be able to read, write, inherit, duplicate,
or impersonate the control connection.

The session channel exists only between the intended controller and the
attached Host Reploy operation. It is never mounted into the workload
container. Its handshake binds the connection to the host-created lease, exact
controller and workload generations, protocol version, fixed capability set,
and expected controller identity. Server-side checks enforce every operation;
an opaque or private endpoint is not treated as authorization by itself.

The initial Linux transport is one Unix-domain socket in a fresh,
lease-private host directory mounted read-only and only into the controller.
Filesystem ownership and mode restrict access to the effective controller
identity. The mount is a narrow protocol capability rather than a host input or
shared-state grant, including when the controller runs as root. The controller
establishes one multiplexed connection, after which Host Reploy may remove the
socket pathname. No endpoint path, token, or descriptor appears in the workload
container, image metadata, or workload environment.

Host Reploy owns the Docker TTY attachment, keeps control framing separate from
terminal bytes, and performs all signaling and process-tree teardown. The
workload container contains no trusted session shim and no control descriptor
for same-UID or root workload code to inspect or interfere with.

Hostile terminal output remains opaque bytes. JSON text, terminal escape
sequences, fake exit messages, and protocol-looking output cannot create
control or lifecycle events.

Root workload code has more authority inside its own container, but it still
cannot reach the host-controlled session channel. A forged terminal message or
workload exit cannot become an authoritative successful termination.

## Runtime Identity

### Effective Runtime Identity

Every Reploy application runtime container uses the ordinary Reploy runtime
identity:

- staged execution uses the invoking host user's numeric identity;
- installed user-scope execution uses the invoking host user's numeric
  identity;
- installed system-scope execution uses the host account explicitly selected
  by `environment.install.system.account`, resolved to its numeric identity.

Using the invoking Unix identity for user-scope execution preserves ordinary
host file ownership and avoids predictable permission failures. Native Windows
instead derives a stable nonzero Linux UID/GID from the invoking SID. The
container image's configured `USER` is not the runtime authority. Reploy passes
the effective numeric `UID:GID` and applicable supplementary GIDs, supplies its
ordinary transient writable home, and adds a real local account named by
`environment.runtime.user` (default `reploy`) to the final runtime layer. The
name and numeric identity are locked build inputs. A non-root account with a
root primary or supplementary group is rejected rather than importing
privileged group membership into the container.

A controlled-session client inherits this identity and cannot override it. A
different system-scope identity is an installation configuration decision, not
a session capability.

The local account is an OS-neutral blueprint concept with target-specific
realization. The initial Linux-container backend writes `/etc/passwd` and
`/etc/group`; runtime mounts may not overlap those generated account-database
paths. A future native target backend may use its own account mechanism.
If an installation selects a different numeric account, Reploy preserves the
provider layers and rebuilds the final runtime-account layer for the installed
generation rather than changing the staged generation.

### Root Runtime Identity

Root applies when the effective runtime UID is `0`: because staged or
user-scope Reploy was invoked as root, or because a system-scope installation
explicitly selected root. It is never inherited merely from the base image's
configured `USER`.

A root runtime identity does not emit a generic warning. With the global
sandbox enforced, its additional authority is limited to container-scoped
root-owned image content, declared persistent storage, and processes using the
same identity. Prohibited combinations fail with diagnostics that identify the
specific rejected authority. If a future capability grants root broader
authority, that capability's explicit opt-in surface must disclose the added
risk rather than making ordinary root execution noisy.

A root runtime identity does not imply:

- Docker or daemon access;
- privileged container mode;
- additional capabilities;
- host input or shared-state mounts;
- public or local networking;
- access to other sessions or deployments.

Additional Linux capabilities, if ever supported for application runtime
containers, are separate explicit grants and require their own threat analysis.

## Source and Filesystem Access

No host path is visible by default.

Non-root application containers may receive explicitly declared read-only or
writable host binds from the effective Reploy runtime plan. Writable binds
support declared configuration, data, and output paths. They are not arbitrary
paths selected through a command or session protocol, and Reploy validates that
the effective runtime identity can use them safely.

The ordinary source grant is an explicit read-only bind mount rooted at an
approved project directory. A client cannot turn it into an arbitrary host-path
selector through the session protocol. Original project source is never exposed
through a writable bind.

An explicit host directory bind grants access to every unmasked entry below
that directory. Read-only mode prevents ordinary file mutation, but does not
neutralize Unix sockets, device nodes, FIFOs, or nested mount points. Reploy
does not recursively scan a live source tree: such a scan has unbounded launch
cost and provides only a race-prone point-in-time observation. A caller that
requires stronger isolation must use no host bind or a future filtered-copy
workspace. This is a deliberate narrowing of the direct-bind security
contract, not a claim that active host objects have been confined.

Direct host binds also trust the selected host pathname namespace to remain
stable until Docker establishes the mount. Reploy does not defend against a
separate host-side actor retargeting the source path during launch or using
Docker daemon access to alter the container. Such actors already hold authority
outside the controller/workload isolation boundary. The launched workload
cannot create this race itself because Docker establishes its mounts before
starting the workload process.

Generic remote Docker daemons are unsupported. Reploy requires a local Unix
socket or Windows named-pipe endpoint, including the local endpoint presented
by Docker Desktop. This prevents local validation and output contracts from
silently applying to paths, ports, images, identities, and lifecycle state on
another machine. A future remote-Docker design requires explicit input upload,
output extraction and local publication, image placement, port forwarding,
authentication, cleanup, and recovery semantics rather than inherited Docker
context behavior.

On native Linux, the operator must run Reploy directly in the host namespace
served by the local Docker Engine. Running Reploy inside a container with a
host socket mounted, or placing a local Unix-socket proxy in front of another
daemon, is unsupported because the Docker API cannot prove that Reploy and the
daemon resolve host paths in the same mount namespace. Unix-socket and
named-pipe classification rejects ordinary remote Docker configuration; it is
not a security attestation for an operator-controlled socket. Docker Desktop
is the intentional exception because its native client integration supplies the
supported host-path sharing and port-forwarding contract.

Ordinary host binds reject the host filesystem root and canonical sources at
or below `/proc`, `/dev`, or `/sys`, including symlink aliases. On Linux,
Reploy also rejects bind mounts that expose the same filesystem root and checks
filesystem identity so procfs, sysfs, cgroup hierarchies, device filesystems,
and kernel control or observation filesystems remain prohibited when exposed
through another path. Linux proc magic links are rejected before canonical
resolution, including when procfs is reached through a symlink alias, so they
cannot resolve differently for Reploy and Docker. Where Linux reports mount
identity, Reploy rejects aliases of every mount rooted below `/proc`, `/dev`,
or `/sys` while preserving unrelated mounts of the same filesystem type. On
Linux kernels without no-magic-link path resolution, direct paths remain
available but symlinked host sources fail closed. On macOS, native devfs and
procfs sources are likewise rejected.
Containers keep Docker's container-scoped `/proc` and restricted `/dev`; those
are not host binds. Hardware or host-observation access, if later justified by
a compelling use case, requires a separately designed explicit capability
rather than an ordinary mount.

Root inside any Reploy application container may not receive host input or
shared-state binds, including read-only binds. Read-only prevents modification
but does not make exposed content confidential from container root. Reploy
validates the complete effective mount plan and rejects the application-runtime
launch before Docker can create a container with a prohibited bind source. A
separately validated output-only bind is a narrow explicit result grant, not
general host filesystem authority.

Root application containers may use image content, Docker-managed volumes,
tmpfs, or a disposable copied workspace because those do not expose the
original host path. An existing Docker-managed volume is allowed only when the
effective environment plan already declares it; no command or session request
may select an arbitrary volume by name. Root can read, mutate, and change
ownership throughout an authorized persistent volume. Fresh scratch storage is
Reploy-owned and scoped to the operation or lease.

Until disposable copied workspaces are implemented, a root operation that
needs local project source is unsupported rather than weakened with a host
bind.

This is an explicit global application-runtime policy, independent of the
Docker daemon's UID mapping. Additional bind categories require a later design
decision justified by a compelling use case; they are not implementation escape
hatches.

The original project source is never writable by a Reploy application runtime
container. Workloads, recordings, or agents that require source mutation must
use an explicitly requested disposable writable copy. A portable writable-copy
implementation is a separate design. Likely implementations include an
ephemeral copied volume, a temporary image plus container layer, or a
platform-specific copy-on-write filesystem.

### Explicit Runtime Outputs

The existing `reploy app --output-file` and `--output-dir` contracts remain
supported. They are intentional, caller-authorized result channels and are not
replaced by the provider store or disposable session scratch.

The controller container is a Reploy application runtime and uses the same
contract for controller-owned artifacts such as asciinema casts, screenshots,
and rendered media. Its immutable session plan carries the prevalidated output
destination into the controller runtime plan; the workload does not receive
that mount. OmegaFlow finalizes and closes those files before sending
`complete`, and ordinary container teardown leaves the host output intact. The
session protocol therefore needs no artifact-transfer operation or separate
publication mechanism.

For a non-root runtime identity, the existing direct output bind remains an
explicit host-filesystem grant. A root-safe output-only contract is separate
global pre-release work rather than controlled-session behavior. Until that
work lands, Reploy rejects root with either output option before contacting
Docker.

The target root `--output-file` contract retains fresh private staging,
single-regular-file validation, race-free atomic publication without overwrite,
and interruption recovery. It additionally requires a focused review of link
behavior plus ownership and mode normalization before publication. The target
root `--output-dir` contract permits a direct bind only to an explicitly
selected, initially empty dedicated directory and defines ownership
normalization and failure retention. Neither output exception grants access to
the destination parent, source, configuration, staging state, or unrelated host
data.

### Sensitive Path Masks

Every source grant supports exclusion masks. Reploy always protects any exposed
deployment `.env` and `.reploy` path using its existing private-runtime mask
rules. A project-source grant additionally masks `.env` and `.reploy` at the
granted source root by default. The effective runtime plan may add validated
relative file or directory masks for project-specific sensitive material.

Mask planning must:

- reject absolute and escaping paths;
- resolve host symlinks defensively;
- distinguish files and directories;
- apply masks to every visible alias of a parent bind;
- reject conflicting nested mount types;
- snapshot and revalidate realized mount sources immediately before creation;
- fail closed when a mask cannot be enforced.

Read-only source does not replace masking: root inside a container may read a
read-only sensitive file.

## Secrets and Environment

Host process environment is never inherited wholesale.

Blueprint variables remain interpolation values, not automatic workload
environment variables. Deployment-local `.env` values continue to use
Reploy's private one-shot environment injection. The host file is not mounted
and is masked from every visible parent bind.

A runtime operation may narrow explicitly declared environment inputs but
cannot invent additional ones. Names and values must not appear in image
metadata, container configuration, Docker command lines, build locks, Reploy
state, or generated diagnostics. Workload output is untrusted; Reploy cannot
prevent a workload from printing values it receives.

OmegaFlow remains responsible for deciding which capture artifacts may be
published. Reploy provides bounded private output mechanisms, not
media-specific allowlisting.

## Network and Endpoints

All Reploy application runtime containers default to:

- public Internet disabled;
- local network disabled.

These are independent policy switches. A controlled workflow applies them
separately to the controller and workload environments. Local denial includes
host-loopback redirection, private and link-local address ranges, IPv6 local
ranges, and infrastructure metadata endpoints. Translation and tunneling
ranges that can represent either public or local destinations require both
grants by default. The initial coarse classifier follows the destination
address; topology-resistant peer and gateway confinement belongs to the
deferred L3 gateway design.

The initial Linux/Docker implementation enforces this coarse public/local
policy with IPv4 and IPv6 nftables rules inside each application container's
network namespace. A trusted Reploy helper begins with only the setup
capabilities needed to install those rules and assume the planned application
identity. It then empties every capability set and the capability bounding
set, locks securebits and `no-new-privileges`, verifies seccomp and the final
kernel state, and executes the exact application argv. Reploy-issued execs use
the same guarded authority-drop path. A raw Docker daemon client remains a
trusted host operator outside this sandbox boundary.

`public` classifies globally routable IP destinations. `local` classifies
private, link-local, multicast, reserved, and infrastructure metadata
destinations. `ambiguous` covers predefined translation and tunneling ranges;
its default `require-both` admits them only when both ordinary classes are
allowed. IPv4-mapped IPv6 socket addresses use the class of their embedded IPv4
destination because Linux emits them as IPv4 packets. The temporary
`ambiguous: allow` escape hatch admits the remaining ambiguous class
independently. It is intentionally discouraged because an apparently public
translated address may reach a local destination, and it should be deprecated
once the deferred L3 gateway can enforce the underlying destination policy.
Container-local loopback remains available and cannot address host loopback
through the container network namespace. The backend configures the
container's DNS path from the same two grants:

| Public | Local | DNS path |
| --- | --- | --- |
| deny | deny | no DNS |
| deny | allow | the host's configured resolver, preserving local, VPN, and split-DNS behavior |
| allow | deny | the built-in Google Public DNS profile (`8.8.8.8`, `8.8.4.4`) |
| allow | allow | the host's configured resolver, which normally provides both local and public resolution |

For Docker, the local-capable path leaves DNS selection to Docker so it derives
the container's resolver path from the host. The public-only path passes the
selected Google Public DNS profile through Docker's per-container DNS
configuration. Docker writes the resulting container resolver configuration:
the default bridge normally receives host-derived resolver addresses, while a
custom network exposes Docker's embedded resolver at `127.0.0.11` and forwards
to the selected upstreams. Before installing the packet filter, the trusted
startup helper reads those engine-authored resolver addresses and admits TCP
and UDP port 53 only to them whenever either network class is granted. This is
an engine-owned DNS exception, not general access to the resolver's address
class.

Resolver selection is host policy, not blueprint policy. Future Reploy host
configuration may override the default local and public resolver choices. The
backend does not classify or filter DNS answers. A local resolver may return a
public address and a public resolver may return a private address, but every
subsequent connection still passes the ordinary destination policy. When both
ordinary classes are enabled, egress is unrestricted, but the packet filter
remains in place to admit new inbound connections only on declared endpoint
ports. The helper always applies the same identity and authority-drop invariant.
A backend that cannot install and verify the policy fails closed.

This slice does not introduce the deferred userland L3 gateway and must not
claim domain-, URL-, DNS-content-, general outbound destination-port-, or
audit-level policy.

A controller may receive an explicit session-local grant to a declared
workload endpoint. Endpoint declarations remain the stable intent and the
future enforcement unit, but the initial direct-network backend does not claim
to enforce each declaration as a precise capability boundary.

The first OmegaFlow prototype needs only:

```text
controller browser -> lease-private Docker network -> workload HTTP endpoint
```

The initial implementation attaches only the controller and workload to one
fresh lease-owned, engine-internal Docker network. The immutable plans carry a
separate session-network grant that admits that lease network inside both
containers; it does not set or imply general `local: allow`. The initial backend
admits the complete lease network in both directions. The controller uses
ordinary native TCP to the workload's session-local network identity and
declared port. Endpoint coordinates are resolved before startup as part of the
immutable controller and workload plans; endpoint traffic never enters the
private session channel and no workload port is published on the host.

This is intentionally a coarse pre-gateway boundary. Membership in the private
network gives the controller reachability to workload ports beyond the declared
endpoint and gives the workload network reachability toward the controller.
The containers still receive no route to unrelated containers, the host local
network, or the public Internet unless separately granted, and the controller
must not expose sensitive listeners on its session-network interface. Reploy
reports this limitation rather than describing endpoint declarations as fully
enforced capabilities.

The target L3 policy gateway hardens this direct native transport. It gives the
controller and workload separate session network identities and permits TCP
only from the controller to exact declared workload addresses and ports. It
denies workload-initiated connections to the controller, undeclared workload
ports, unrelated containers, and ungranted networks.

Gateway policy, addresses, and network resources are lease-owned. Host Reploy
installs them before either application can use the route, verifies the
root-resistant policy, and removes or reconciles them during session teardown.
After gateway parity is proven on the supported Docker, Podman, and Desktop
backends, it replaces the coarse shared-network policy without changing how
applications use native TCP. PTY, lifecycle, termination, and diagnostic
traffic remain on the private session channel.

General public/local network denial remains a separate prerequisite; neither
endpoint backend is a general HTTP policy engine or domain-aware firewall.

General network isolation and auditability are a separate design surface.
Future work may include an HTTP/HTTPS proxy, destination and DNS-content
policy, and agent-sandbox audit records. HTTPS `CONNECT` can filter and audit a
destination hostname without TLS interception, but cannot inspect encrypted
URLs or content. Direct egress must be blocked to prevent proxy bypass.
Redirects, DNS rebinding, CDNs, WebSockets, QUIC, and workload-to-network policy
require explicit treatment.

Until that design is implemented, rough public and local kill switches must
fail closed and must not be described as domain-level isolation.

## Browser and Terminal Placement

For the OmegaFlow profile:

- orchestration runs in the controller container;
- asciinema and the session client run in the controller container;
- Playwright and Chromium run in the controller container;
- Host Reploy owns the Docker TTY attachment and external session supervision;
- the shell runs on the Docker-managed PTY in the workload container;
- the demonstrated web service runs in the workload environment;
- Chromium reaches that service over the lease-private native network; the
  initial backend has the documented coarse shared-network gap and the target
  gateway backend enforces the declared endpoint direction and port.

Terminal-to-browser handoff is an OmegaFlow orchestration concern inside the
controller. Reploy does not model beats, handoffs, browser actions, or capture
state. It provides only the PTY, endpoint, network, and lifecycle primitives.

## Asciinema Integration

OmegaFlow retains asciinema as the initial terminal recorder. It records a
local Reploy session-client command:

```text
asciinema
└── Reploy session client in the controller
    ⇄ private session channel
       ⇄ Host Reploy external supervisor
          ⇄ Docker TTY attachment
             ⇄ workload shell
```

The proxy forwards input bytes, output bytes, resize operations, and terminal
completion. This keeps asciinema and recording dependencies out of workload
images while preserving the existing cast format and controller ownership.

The prototype must test:

- absence of double input echo;
- raw and canonical terminal modes;
- ordinary Ctrl-C behavior and recording;
- initial dimensions and resize propagation;
- byte ordering and timing;
- headless capture;
- large-output backpressure;
- abrupt loss of the controller, workload, Host Reploy, or Docker
  observation.

OmegaFlow may later write casts directly from session events, but that is not
required by this design.

## Lifecycle and Verified Termination

Host Reploy serializes every session through one state machine:

```text
preparing -> active -> terminating -> terminated
```

The first accepted termination cause is latched and never rewritten. Causes
include controller-requested termination, workload exit, host cancellation,
controller loss, Docker-observation loss, and startup failure. Later events
remain diagnostic observations. Workload status, workload-output-finalization
status, runtime-observation status, controller finalization status, and
pre-delivery cleanup success are reported separately in the session result, so
a late observation or cleanup failure can fail the operation without hiding
its original cause. Controller exit and delivery-tail cleanup are reported
separately by the invoking host operation after teardown.

Channel closure is never successful completion. A controller granted the
`complete` operation must explicitly send `complete` after receiving
`workload_outputs_finalized` and finalizing its client-owned results; for
OmegaFlow these include the recording artifacts. Host Reploy does not open a
controller-finalization wait when `complete` was not granted and records that
controller as `not-completed` in the terminal result.
Repeated terminate or host cancel operations are idempotent. Input and resize
are rejected after `terminating` begins. A single `complete` remains valid
during termination while Host Reploy is waiting for controller finalization. A
`failed` workload-output result makes the session fail regardless of whether
the controller preserves and finalizes partial artifacts.

Normal completion is:

1. Host Reploy observes workload exit, or a controller or host operation
   requests termination.
2. Host Reploy atomically latches the cause and enters `terminating`.
3. Host Reploy performs bounded graceful termination followed by forced
   termination when necessary.
4. Host Reploy independently observes the exact workload container stopped.
5. Host Reploy drains and closes every declared workload-output surface under
   the finite output-finalization deadline, then emits the one ordered
   `workload_outputs_finalized` outcome.
6. When the live controller was granted `complete`, Host Reploy gives it a
   bounded finalization period in which to close its client-owned output and
   send `complete`. Without that grant, Host Reploy skips the wait and records
   `not-completed`. A failed output outcome remains a session failure even when
   partial client artifacts are finalized.
7. Host Reploy removes the workload container, temporary mounts, networks, and
   every other lease resource not required to deliver the final result. It
   keeps the controller and private session channel alive.
8. Host Reploy records the original cause, workload status,
   workload-output-finalization status, runtime-observation status,
   controller-finalization status, and pre-delivery cleanup result, then emits
   the one authoritative `terminated` event. Only successful event delivery
   arms the acknowledgement wait.
9. Host Reploy waits for a bounded `acknowledge_terminated` response. Channel
   closure is not an acknowledgement. Timeout or disconnect does not block
   teardown.
10. Host Reploy closes the channel, stops and removes the controller, removes the
   remaining delivery-tail resources, and independently verifies that cleanup.
   A failure here fails the invoking host operation and is persisted for
   reconciliation; it cannot be reported over the channel being removed.

A controller disconnect latches `controller_lost` and starts the same teardown.
A workload exit is reported to the still-live controller so it can finalize
its client-owned results; the session cannot succeed if the controller
disappears before that finalization. Neither terminal output nor an
application-level connection close can substitute for host-observed Docker
state.

### Session Watchdog

Host Reploy starts one short-lived watchdog for each live controlled session.
It first creates inert Docker resources and durably records their exact
identities. Before starting either container, it passes the watchdog an
immutable cleanup manifest containing the exact lease, container, network,
volume, and host boot identities. The attached operation retains one
end of a private parent pipe. A crash during inert resource creation leaves no
untrusted code running and is handled by ordinary next-operation
reconciliation.

Successful verified delivery-tail cleanup disarms the watchdog. Unexpected
process death, including `SIGKILL`, closes the pipe in the operating system and
causes the watchdog to stop, forcibly terminate when needed, remove, and verify
only the manifested resources. The watchdog exposes no listener, accepts no
later resource selection, and exits after verified cleanup. Although its
underlying Docker connection has ordinary trusted-host authority, its code path
is limited to the immutable resource set.

If Docker is unavailable, the watchdog retries until Docker returns or the host
reboots. If both the attached operation and watchdog are killed, durable labels
and deployment-scoped live-run state let the next locked Reploy operation
reconcile the abandoned resources. This final fallback is eventual rather than
immediate.

### Docker Restart and Host Reboot

Controlled-session containers use no Docker restart policy. That alone does not
end a session during a Docker daemon restart because Docker live-restore may
keep both containers executing while management, attach, events, input, or
networking are unavailable.

Loss of authoritative Docker observation therefore immediately latches
`runtime_observation_lost`, fails the recording, and closes its session and
network. The session is never resumed or accepted as valid after observation
returns. Host Reploy and the watchdog retry Docker access and forcibly remove
any survivors when control returns. Immediate termination while Docker itself
is unreachable is not promised.

A real host reboot ends the processes. The no-restart policy prevents their
automatic return, and prior-boot queue entries are discarded under Reploy's
existing boot-session admission rules.

## Staging and Generation Semantics

Basic preflight validation uses the existing `reploy validate` behavior.
Creating a session requires successfully staged and built current controller
and workload generations. The session is pinned to both generations for its
complete lifetime.

Updating a staging directory with the same environment follows ordinary
stage-update behavior. Attempting to stage a different environment into the
same directory is rejected by default.

An explicit forced replacement:

- stops and removes Reploy-managed runtime resources for the old environment;
- replaces Reploy-managed staging state;
- stages the new environment;
- preserves user-owned files such as overrides, private environment
  configuration, and unrelated paths;
- never treats force as permission to recursively delete the staging
  directory.

A controller or workload generation update never retargets a live controlled
session.

## Audit and Diagnostics

Reploy records security-relevant facts without recording secret values:

- session and lease identity;
- deployment and generation;
- effective runtime identity and whether it is root;
- capability names;
- mount targets and mask identities, without secret file contents;
- network policy class and granted endpoint identities;
- lifecycle transitions, cancellation, timeout, and recovery actions;
- exit status and structured failure codes.

Terminal content belongs to the controller's private output stream and is not
duplicated into Reploy audit metadata.

Diagnostics identify which operation failed, what Reploy attempted, whether
the session channel or Docker lifecycle was observed, what cleanup ran, and the
safe next action.

## Resource and Timeout Policy

Controlled sessions have explicit limits for:

- startup and handshake;
- idle or total session duration when requested by policy;
- termination grace;
- buffered terminal output;
- controller request size;
- process, memory, CPU, and temporary-disk resources where supported.

Limit failures produce a structured diagnostic. A timeout or session-wide
resource failure that makes safe continuation impossible produces bounded
teardown. A timeout never converts into successful completion.

## Implementation Plan

The initial implementation uses Linux containers under Docker, but it is not
one prototype megaslice. Each slice receives focused assertions, tests, review,
and a separate commit.

### Slice 1: Global Sandbox Prerequisites

Apply the approved identity, seccomp, `no-new-privileges`, capability,
namespace, device, mount, mask, secret, network, and root rules consistently to
ordinary Reploy application containers. Prove staged workloads, installed
workloads, transient commands, shells, and later controlled sessions consume
the same baseline. This is global runtime work, not controlled-session code.

Implementation status: the canonical application sandbox plan and its identity
and kernel baseline are implemented for persistent Compose workloads and
transient application commands. Reploy now imports canonical supplementary
groups, rejects root-group membership for non-root identities, starts transient
commands through the trusted setup helper, drops all capabilities before the
application starts, enables
`no-new-privileges`, explicitly selects Docker's built-in seccomp profile, and
prohibits privileged mode, host namespaces, and host devices in the common
plan. Live Docker tests inspect both runtime paths. Trusted production startup
verification is also implemented: Reploy packages the platform-specific probe
in a final runtime layer, creates the locked container-local account there,
records that layer outside the provider graph, and uses its fixed
sandbox-and-exec contract as the outermost process for persistent
workloads, transient commands, shells, and lifecycle commands. The verifier
fails closed unless the calling thread's `/proc/thread-self/status` (or the
compatible `/proc/self/task/<tid>/status`) reports seccomp filtering,
`no-new-privileges`, and empty inheritable, effective, permitted, bounding, and
ambient capability
sets, then directly executes the exact application argv. Private-environment
workloads use one additional fixed Reploy step: after verification, the probe
executes the environment injector, which imports the private variables and then
executes the unchanged application argv. The same helper now installs the
default-deny application-network policy for persistent and transient containers
while preserving exact declared inbound endpoints. Live Docker coverage
exercises all four public/local combinations over IPv4 and IPv6, strict and
escaped ambiguous-range handling, a globally reachable exception nested inside
a reserved parent range, a root default-denial case, non-root authority removal,
guarded exec, and host-loopback denial. Resource limits remain a
separate prerequisite slice. Root host authority is now enforced at runtime:
host sources are classified as input, shared state, or explicit output; UID 0 is
rejected for all three before container creation; and root output options are
rejected before host-path preparation. Docker-managed volumes and tmpfs remain
available to root. Ordinary binds also reject canonical host root, `/proc`,
`/dev`, and `/sys` sources plus equivalent protected filesystem mounts detected
through native filesystem identity. Explicit non-root directory binds
intentionally grant access to their remaining unmasked contents, including
nested active objects; this narrowed contract avoids representing a recursive
launch-time scan as durable confinement.

### Slice 2: Controlled-Session Lifecycle Core

Using synthetic controller and workload images with networking disabled:

- validate one immutable controller/workload plan pair and admit both exact
  generations;
- start both containers without giving the controller Docker access;
- establish the private controller channel and external Docker TTY supervision;
- implement ordered terminal bytes, initial dimensions, resize, ordinary
  Ctrl-C, bounded termination, exit status, and bounded backpressure;
- implement the host-owned state machine and authoritative terminal result;
- prove hostile terminal output and connection closure cannot forge lifecycle
  success.

### Slice 3: Crash Containment

Add the session-scoped watchdog, immutable cleanup manifest, labels,
reconciliation, and idempotent teardown. Test controller death, workload
death, Host Reploy `SIGKILL`, watchdog interruption, Docker daemon restart with
live-restore, Docker unavailability, and host-reboot recovery without touching
unrelated resources.

### Slice 4: Direct Session Networking

Create one lease-private Docker network containing only the controller and
workload. Resolve the declared workload endpoint into the immutable session
plans and prove Chromium can use ordinary HTTP and WebSocket connections
without host publication or access to unrelated containers, host-local
networks, or the public Internet. Test and document the initial broader mutual
reachability inside that two-container network. Keep precise directional and
per-port enforcement, general proxy, DNS, domain, redirect, QUIC, and audit
policy in the deferred L3 gateway slice.

### Slice 5: OmegaFlow Proof

Integrate OmegaFlow, asciinema, Playwright, and Chromium only after the generic
session slices pass. Prove one persistent shell across multiple operations,
faithful Ctrl-C recording, resize, terminal-to-browser handoff, explicit
recording finalization into the controller's declared Reploy output destination,
survival of that output across teardown, and actionable failure diagnostics.

### Slice 6: User-Facing Documentation

After the generic runtime and at least one integration profile are proven,
publish user-facing documentation before public release. Explain the
controller/workload model and trust boundary, how to create and run a
controlled session, the capability, endpoint, output, lifecycle, and failure
contracts, and the security defaults and limitations. Include focused examples
for OmegaFlow recording, sandboxed agents, and security inspection without
presenting any one profile as the controlled-session abstraction itself.

### Independent Pre-release Runtime Fixes

The admission promotion-versus-cancellation invariant and root-safe
`--output-file`/`--output-dir` contracts are separate global backlog slices.
Controlled sessions consume their completed behavior and do not grow private
variants. Until root output work lands, that combination is rejected clearly.

The initial implementation does not include domain filtering, remote
execution, reconnection, persistent caches, writable source copies, or multiple
concurrent sessions per controller.

## Deferred Designs

### Reploy Repositories and Portable Tools

Define and implement the independently updated, federated repository mechanism
and its portable tool surface for capabilities such as Java and Playwright in
[`REPOSITORY_DESIGN.md`](REPOSITORY_DESIGN.md).
This is a controller packaging dependency, not part of the session transport or
lease protocol.

### Network Isolation and Audit

After the implemented coarse public/local kill switches, define a separate
Reploy userland L3 policy gateway for finer network control. Its design should
cover separate controller and workload network identities, a capability-free
application network namespace, one-shot route initialization, an isolated data
path whose only peer is the gateway, private gateway control, root-resistant
route invariants, direct-egress prevention, directional destination and port
grants, DNS and IPv6 policy, metadata protection, auditing, resource limits,
failure behavior, reconciliation, and portable Docker/Podman integration.

The gateway becomes the target controlled-session endpoint backend. It permits
native controller-to-declared-workload TCP while denying the reverse direction
and every undeclared destination. Migration requires functional parity on every
supported backend and replaces the initial coarse shared-network policy without
changing application traffic from native TCP.

### Disposable Writable Workspaces

Define portable snapshot or copy semantics, cost, ownership, exclusions,
cleanup, persistence boundaries, and failure recovery. The original source
must remain immutable.

### Cross-platform Session Transport

The first protocol uses Linux/Docker primitives. Windows named pipes, macOS and
Docker Desktop behavior, rootless runtimes, and Podman require platform
evidence before becoming supported contracts.

### Elevated Application Capabilities

Root identity is supported globally, but additional Linux capabilities and
administrative demonstrations need separate explicit grants and threat
analysis. Privileged application containers remain outside this design.

## Consequences

- Controllers can drive exact Reploy environments without receiving general
  host authority.
- OmegaFlow retains ownership of recording behavior and dependencies.
- Reploy gains a primitive useful for agent sandboxes and other controlled
  execution clients.
- Controlled sessions reuse the global application-runtime sandbox instead of
  defining an OmegaFlow-only security tier.
- Host Reploy remains in both the PTY and lifecycle paths; the containers do not
  receive a direct control connection to one another.
- Staged and installed user-scope application containers retain practical host
  ownership through their effective numeric identity and receive a predictable
  container-local account name.
- Installed system-scope application containers use the configured host
  service account's numeric identity under the blueprint's container-local
  account name.
- Root application containers are possible but receive no implicit additional
  authority; prohibited combinations fail with precise diagnostics.
- Root application containers never receive host input or shared-state binds;
  local source requires the separately designed disposable-copy capability.
  Explicit root output-only binds remain unavailable until their separate
  pre-release contract is reviewed and implemented.
- Strong default network denial and arbitrary sensitive-path masks require new
  implementation work.
- Disposable writable source is intentionally more expensive and deferred.
- Verified termination requires independent runtime observation; session
  protocol success alone is never sufficient.
