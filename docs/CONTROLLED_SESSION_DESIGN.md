---
status: Active
updated: 2026-08-02
summary: Capability-scoped execution sessions that inherit Reploy's global container sandbox.
---

# Controlled Execution Session Design

## Status

- Decision state: Focused review complete; high-level decisions approved
- Implementation state: Initial global sandbox prerequisites and trusted
  application-startup verification implemented in the current slice;
  controlled-session authorization, protocol, lifecycle, and Docker
  orchestration remain later slices
- Initial runtime: Linux containers under Docker
- Motivating clients: OmegaFlow recording, sandboxed AI agents, security
  inspection, and untrusted-code execution

This document records the decisions for a reusable Reploy controlled-session
capability and the global Reploy container sandbox policy on which it depends.
It does not define OmegaFlow recording syntax, browser actions, media formats,
or publication behavior.

## Decision Summary

Reploy will let a controller such as OmegaFlow control one
host-created session in an exact staged environment generation without
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

Reploy does not configure a second container-local username. Staged and
installed user-scope containers run as the invoking host user's numeric UID,
GID, and supplementary GIDs. Installed system-scope containers run as the host
account explicitly selected by `environment.install.system.account`, using
that account's numeric identity inside the container. The setting selects the
installation's host account; it is not a second container-user setting.

If the effective runtime user is root, Reploy emits a precise warning that the
application can interfere with more of its container. Root does not implicitly
grant capabilities, host input or shared-state mounts, network access,
privileged mode, or daemon access. Root-safe `--output-file` and `--output-dir`
are separate global runtime contracts and remain rejected until their focused
pre-release review and implementation are complete.

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
4. Bind every operation to one admitted run and exact environment generation.
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
  generation, capability set, and the runtime resources created for it.

**Session handle**
: An opaque identifier valid only within its lease. It never conveys arbitrary
  deployment selection.

**Session channel**
: A private channel between the controller and the attached Host Reploy
  operation. It is created for one already planned lease and carries PTY data,
  bounded controller requests, declared endpoint streams, and host-observed
  lifecycle events. It grants no session-creation or general host-runtime
  authority and is never mounted into the workload container.

## Architecture

```text
Host attached Reploy operation
├── validates one complete immutable session plan
├── creates the lease and inert Docker resources
├── records their exact identities and starts the session watchdog
├── starts the controller and workload containers
├── owns the Docker TTY attachment and framed session protocol
├── forwards only predeclared workload endpoints
├── monitors and cleans every leased runtime resource
└── independently observes lifecycle completion
         ⇅
         ⇅ private session channel
         ⇅
Controller container
├── controller orchestration
├── optional policy, inspection, or capture components
└── optional controller-local endpoint adapter

Workload container
├── untrusted workload shell on the Docker-managed PTY
└── declared services and endpoints
```

The host invocation selects the staged generation and complete runtime plan
before either container starts. Host Reploy validates that fixed plan, admits
one live run, creates both containers, and binds the session channel to the
resulting lease. The controller never requests creation of a deployment or
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

- a controller-owned lease bound to one deployment generation and live run;
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

- the deployment identity;
- the exact current generation reference and build identity;
- the admitted live-run identity;
- the effective runtime plan;
- the effective runtime identity inherited from the execution scope;
- the network and endpoint grants;
- the mount and source grants;
- the permitted session and endpoint operations;
- the lease lifetime and owner connection.

The controller does not receive a generic session-creation capability. After
creation, protocol operations do not accept a deployment name, mount, identity,
environment key, output destination, network, or raw endpoint destination.
They act only on the session and logical endpoint identities established by the
host-created plan. A generation change invalidates admission of a pending
session; it does not retarget a live session.

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
- `open_endpoint(request_id, endpoint_id)`: request one byte stream to a logical
  endpoint fixed in the immutable session plan. `request_id` correlates the
  result with this request; it is not the stream identity.
- `acknowledge_terminated`: confirm receipt of the authoritative `terminated`
  event. This payload-free protocol handshake is mandatory housekeeping, not a
  granted capability, and is accepted only after Host Reploy has successfully
  emitted the terminal result.

Admission cancellation is host-owned and is not a session-protocol request.
Host-terminal Ctrl-C while waiting removes the caller's queued operation. Once
admitted, host cancellation terminates that caller's session and cleans its
lease. The exact promotion-versus-cancellation boundary is a global admission
queue invariant tracked as separate pre-release work.

### Session Events

- `opened`: reports the effective dimensions, identity, generation, and fixed
  session capabilities.
- `output(bytes)`: ordered PTY output bytes.
- `workload_exit(status, reason)`: reports host-observed workload-shell
  exit.
- `terminating(cause)`: reports that the host-owned terminal transition began.
- `diagnostic(code, message)`: reports protocol, runtime, or cleanup failure
  without embedding secret values.
- `endpoint_opened(request_id, endpoint_id, stream_id)`: reports a successful
  endpoint open and assigns its host-generated session-unique stream identity.
- `endpoint_open_failed(request_id, endpoint_id, code, message)`: reports an
  unsuccessful endpoint open without assigning a stream identity.
- `endpoint_closed(stream_id, endpoint_id, reason)`: reports one forwarded
  endpoint stream ending.
- `workload_outputs_finalized(status, reason)`: establishes that no further
  workload output can arrive. `status` is `drained` when every byte was
  delivered and `failed` when bounded finalization had to close an incomplete
  output surface.

Every well-formed `open_endpoint` request receives exactly one correlated
`endpoint_opened` or `endpoint_open_failed` outcome. Request IDs are nonzero
controller-generated 64-bit values and must be unique while a request is
pending. Stream IDs are nonzero host-generated 64-bit values and are unique for
the session. Protocol-invalid frames are handled as protocol errors rather than
endpoint-open outcomes.

Host Reploy emits the authoritative lease lifecycle result:

- `terminated(cause, workload_status, controller_finalization_status,
  cleanup_status, recovery_action)`.

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

PTY, endpoint, and lifecycle streams use independent bounded flow-control
windows. A stalled browser transfer cannot indefinitely block terminal output,
termination, or the authoritative lifecycle result.

Host Reploy owns workload-output finalization; it never waits indefinitely for
workload cooperation. Once termination begins, it rejects new output surfaces,
performs bounded graceful shutdown followed by forced container stop, and
continues draining the PTY plus every existing endpoint stream. The immutable
session plan carries a finite output-finalization deadline. The initial
implementation may use a fixed host-owned value, but the effective value is
reported by `opened` and applies to workload shutdown, final buffered-byte
delivery, and controller backpressure.

If every final byte is delivered and every output surface reaches EOF before
the deadline, Host Reploy emits `workload_outputs_finalized(drained)` only after
all earlier output has been consumed through its flow-control window. If the
deadline expires, or a runtime error makes complete delivery unverifiable,
Host Reploy forcibly closes the remaining surfaces and emits
`workload_outputs_finalized(failed, reason)`. The multiplexing layer guarantees
that no output frame can follow either outcome. Failure is explicit and cannot
be converted into successful completion by `complete` or terminal
acknowledgement.

The barrier covers every workload-originated output surface declared by the
session plan. Initially these are the PTY and forwarded endpoint streams. A
future workload output-file or output-directory contract joins the same
barrier after its files are closed, validated, and published or have recorded
an explicit failure; protocol v1 does not otherwise speculate about file
payloads.

Every endpoint byte frame in either direction carries its `stream_id`. Host
Reploy assigns that ID only after the endpoint connection succeeds, never
reuses it within the session, and rejects data or closure operations for an
unknown or already closed ID. The logical `endpoint_id` remains the capability
being exercised; it is not sufficient to distinguish concurrent connections
to that endpoint.

The initial implementation also enforces fixed Host Reploy maxima of 32 active
streams per logical endpoint, 64 active endpoint streams per session, and 64
new endpoint streams per second per session with a burst of 128. Reploy reserves
capacity before dialing or allocating stream state and releases it when the
stream closes. An excess `open_endpoint` request is not queued or dialed; it is
rejected immediately with a correlated `endpoint_open_failed` event whose code
is `resource_exhausted`.
Blueprints and controllers cannot raise these host-owned limits. A future
general Reploy configuration surface may make them operator-configurable after
concrete use cases justify that surface.

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
generation, protocol version, fixed capability set, and expected controller
identity. Server-side checks enforce every operation; an opaque or private
endpoint is not treated as authorization by itself.

The initial Linux transport is one Unix-domain socket in a fresh,
lease-private host directory mounted only into the controller. Filesystem
ownership and mode restrict access to the effective controller identity. The
controller establishes one multiplexed connection, after which Host Reploy may
remove the socket pathname. No endpoint path, token, or descriptor appears in
the workload container, image metadata, or workload environment.

Host Reploy owns the Docker TTY attachment, keeps control framing separate from
terminal bytes, and performs all signaling and process-tree teardown. The
workload container contains no trusted session shim and no control descriptor
for same-UID or root workload code to inspect or interfere with.

Hostile terminal output remains opaque bytes. JSON text, terminal escape
sequences, fake exit messages, and protocol-looking output cannot create
control or lifecycle events.

Root workload code has more authority inside its own container, but it still
cannot reach the host-controlled session channel. A forged terminal message,
workload exit, or closed endpoint stream cannot become an authoritative
successful termination.

## Runtime Identity

### Effective Runtime Identity

Every Reploy application runtime container uses the ordinary Reploy runtime
identity:

- staged execution uses the invoking host user's numeric identity;
- installed user-scope execution uses the invoking host user's numeric
  identity;
- installed system-scope execution uses the host account explicitly selected
  by `environment.install.system.account`, resolved to its numeric identity.

Using the invoking identity for user-scope execution preserves ordinary host
file ownership and avoids predictable permission failures. The container
image's configured `USER` is not the runtime authority, and the image does not
need a matching named account. Reploy passes the effective numeric `UID:GID`
and the host account's supplementary GIDs, then supplies its ordinary transient
writable home. A non-root account with a root primary or supplementary group is
rejected rather than importing privileged host group membership into the
container.

A controlled-session client inherits this identity and cannot override it. A
different system-scope identity is an installation configuration decision, not
a session capability.

### Root Runtime Identity

Root applies when the effective runtime UID is `0`: because staged or
user-scope Reploy was invoked as root, or because a system-scope installation
explicitly selected root. It is never inherited merely from the base image's
configured `USER`.

A root runtime identity emits a warning equivalent to:

> The application will run as root inside its container. Root can bypass
> application-level file permissions. Host input and shared-state mounts are
> prohibited. Explicit root output contracts require their separately reviewed
> safeguards. Network access and Linux capabilities remain restricted unless
> separately granted.

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

Root inside any Reploy application container may not receive host input or
shared-state binds, including read-only binds. Read-only prevents modification
but does not make exposed content confidential from container root. Reploy
validates the complete effective mount plan and rejects the operation before
contacting Docker if a prohibited bind source would be visible. A separately
validated output-only bind is a narrow explicit result grant, not general host
filesystem authority.

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
separately to the controller and workload environments. Local
denial includes host gateways, Docker peers outside the granted operation,
loopback redirection, private and link-local address ranges, IPv6 local ranges,
and infrastructure metadata endpoints.

A controller may receive an explicit session-local grant to a declared
workload endpoint. That grant is not treated as general local-network
access.

The first OmegaFlow prototype needs only:

```text
controller browser -> Host Reploy -> one declared workload HTTP endpoint
```

The controller and workload do not share a Docker network. Docker publishes
only the declared workload port to an ephemeral host-loopback port. A
controller-local adapter accepts Chromium connections on controller
loopback and multiplexes their byte streams over the private session channel.
Host Reploy maps the fixed logical endpoint identity to the loopback-published
port. The protocol accepts no raw host, IP address, port, or URL destination.
The workload cannot use this one-way forwarding path to reach the controller.

The loopback-published port is never disclosed to the controller, but any local
host process that discovers it may connect while the session is active. The
initial implementation accepts this host-local exposure; its session grant
constrains the controller, not unrelated host processes. It must not claim
per-lease endpoint privacy on a multi-user host. The port, adapter streams, and
associated Docker state remain lease-owned and are removed with the session.
The future L3 policy gateway must eliminate this direct host reachability or
enforce equivalent per-lease access control. General public/local network denial
remains a separate prerequisite; this endpoint forwarding path is not a general
router, HTTP policy engine, or domain-aware firewall.

General network isolation and auditability are a separate design surface.
Future work may include an HTTP/HTTPS proxy, destination policy, controlled
DNS, and agent-sandbox audit records. HTTPS `CONNECT` can filter and audit a
destination hostname without TLS interception, but cannot inspect encrypted
URLs or content. Direct egress must be blocked to prevent proxy bypass.
Redirects, DNS rebinding, CDNs, WebSockets, QUIC, and workload-to-network
policy require explicit treatment.

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
- Chromium reaches that service only through its controller-local adapter and
  the one granted Host Reploy endpoint stream.

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
remain diagnostic observations. Workload status, controller finalization
status, and pre-delivery cleanup success are reported separately in the session
result, so a cleanup failure can fail the operation without hiding its original
cause. Controller exit and delivery-tail cleanup are reported separately by the
invoking host operation after teardown.

Channel closure is never successful completion. The controller must explicitly
send `complete` after receiving `workload_outputs_finalized` and finalizing its
client-owned results; for OmegaFlow these include the recording artifacts.
Repeated terminate or host cancel operations are idempotent. Input, resize, and
new endpoint streams are rejected after `terminating` begins. A single
`complete` remains valid during termination while Host Reploy is waiting for
controller finalization. A `failed` workload-output result makes the session
fail regardless of whether the controller preserves and finalizes partial
artifacts.

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
6. Host Reploy gives the live controller a bounded finalization period in which
   to close its client-owned output and send `complete`. A failed output outcome
   remains a session failure even when partial client artifacts are finalized.
7. Host Reploy removes the workload container, endpoint publication, temporary
   mounts, networks, and every other lease resource not required to deliver the
   final result. It keeps the controller and private session channel alive.
8. Host Reploy records the original cause, controller-finalization result,
   workload status, controller protocol status, and pre-delivery cleanup result,
   then emits the one authoritative `terminated` event. Only successful event
   delivery arms the acknowledgement wait.
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
endpoint-stream close can substitute for host-observed Docker state.

### Session Watchdog

Host Reploy starts one short-lived watchdog for each live controlled session.
It first creates inert Docker resources and durably records their exact
identities. Before starting either container, it passes the watchdog an
immutable cleanup manifest containing the exact lease, container, endpoint,
network, volume, and host boot identities. The attached operation retains one
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
endpoint streams. The session is never resumed or accepted as valid after
observation returns. Host Reploy and the watchdog retry Docker access and
forcibly remove any survivors when control returns. Immediate termination while
Docker itself is unreachable is not promised.

A real host reboot ends the processes. The no-restart policy prevents their
automatic return, and prior-boot queue entries are discarded under Reploy's
existing boot-session admission rules.

## Staging and Generation Semantics

Basic preflight validation uses the existing `reploy validate` behavior.
Creating a session requires a successfully staged and built current generation.
The session is pinned to that generation for its complete lifetime.

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

A generation update never retargets a live controlled session.

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
- active endpoint streams and endpoint-open rate;
- process, memory, CPU, and temporary-disk resources where supported.

Limit failures produce a structured diagnostic. Endpoint-admission limits
reject only the excess request as specified above; a timeout or session-wide
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
commands directly as the final identity, drops all capabilities, enables
`no-new-privileges`, explicitly selects Docker's built-in seccomp profile, and
prohibits privileged mode, host namespaces, and host devices in the common
plan. Live Docker tests inspect both runtime paths. Trusted production startup
verification is also implemented: Reploy packages the platform-specific probe
in a final runtime layer, records that layer outside the provider graph, and
uses its fixed verify-and-exec contract as the outermost process for persistent
workloads, transient commands, shells, and lifecycle commands. The verifier
fails closed unless `/proc/self/status` reports seccomp filtering,
`no-new-privileges`, and empty effective, permitted, and bounding capability
sets, then directly executes the exact application argv. Private-environment
workloads use one additional fixed Reploy step: after verification, the probe
executes the environment injector, which imports the private variables and then
executes the unchanged application argv. Mount/root authority, network denial,
and resource limits remain separate prerequisite slices.

### Slice 2: Controlled-Session Lifecycle Core

Using synthetic controller and workload images with networking disabled:

- validate one immutable plan and admit its exact generation;
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

### Slice 4: Declared Endpoint Forwarding

Add the controller-local adapter and Host Reploy forwarding to one exact
host-loopback-published workload endpoint. Prove Chromium can use HTTP and
WebSocket streams while the workload cannot reach the controller and neither
container receives unrelated local or public access. Keep general proxy, DNS,
domain, redirect, QUIC, and audit policy outside this slice.

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

Define general public and local kill switches, direct-egress enforcement, proxy
behavior, DNS control, IPv6, metadata protection, and auditability as a
separate Reploy/agent-sandbox design. The one-way, exact endpoint forwarding
used by the initial controlled session is intentionally narrower than that
future surface.

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
  ownership by using the invoking host identity, which may be unnamed inside
  the image.
- Installed system-scope application containers use the configured host
  service account's numeric identity without requiring a corresponding
  container-local username.
- Root application containers are possible but visibly weaker.
- Root application containers never receive host input or shared-state binds;
  local source requires the separately designed disposable-copy capability.
  Explicit root output-only binds remain unavailable until their separate
  pre-release contract is reviewed and implemented.
- Strong default network denial and arbitrary sensitive-path masks require new
  implementation work.
- Disposable writable source is intentionally more expensive and deferred.
- Verified termination requires independent runtime observation; session
  protocol success alone is never sufficient.
