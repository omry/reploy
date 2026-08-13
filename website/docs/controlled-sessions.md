---
sidebar_position: 1
title: Controlled sessions
---

Reploy controlled sessions let a trusted controller drive the persistent shell
of a separate workload deployment. The controller can automate a terminal,
reach explicitly selected workload endpoints, and retain its own artifacts
without receiving implicit Docker or host authority.

The initial controlled-session runtime requires a Linux host running Docker;
Docker Desktop on macOS or Windows is not supported for this capability.
Reploy supports Linux `amd64` and `arm64` controller images and installs the
matching `reploy-session-client` executable into a derived controller image.
The workload image is not modified.

## Controller, workload, and host

A controlled session has three roles:

- Host Reploy selects exact current controller and workload generations,
  creates the private channel and PTY, supervises both containers, and owns
  cleanup.
- The controller is trusted orchestration software. It receives the session
  client, the selected endpoint coordinates, and any controller-only output
  destination.
- The workload contains the project, tools, services, and persistent shell
  being controlled. It does not receive the session socket, session client,
  controller output destination, or Docker socket.

Terminal content is untrusted workload data. It travels on a byte-only path
separate from structured lifecycle messages, so terminal escape sequences or
printed JSON cannot forge control events.

## Prepare the two deployments

Create and build separate staging deployments for the controller and workload.
For example:

```bash
reploy stage ./controller.blueprint.yaml --dir ./controller-staging
reploy build --dir ./controller-staging

reploy stage ./workload.blueprint.yaml --dir ./workload-staging
reploy build --dir ./workload-staging
```

The session starts `/bin/sh` in the exact workload image, so a shell-only
session does not require an `environment.workload` declaration. When granting
an endpoint, declare its service and endpoint under `environment.workload`; see
[Blueprint structure](/docs/blueprint-structure#endpoints-and-readiness).
Pass the two staging directories to `controlled-session run`. A session is
bound to their exact current generations and is never retargeted by a later
deployment update. The controller and workload must be distinct deployments;
Reploy rejects two paths that resolve to the same deployment.

The controller blueprint should contain the controller program and its own
dependencies. Do not add `reploy-session-client`: Reploy supplies the matching
Linux executable at session preparation time and places it on `PATH` only in
the derived controller image. Declare the controller entry point as a native
command; for example, if `controller.main` is the qualified executable profile
for the controller program:

```yaml title="controller.blueprint.yaml (excerpt)"
environment:
  commands:
    run-controller:
      executable: controller.main
      native_command: true
```

## Run a session

```text
reploy controlled-session run \
  --controller-dir DIR --workload-dir DIR \
  [--endpoint ID ...] --columns N --rows N \
  [--output-file FILE | --output-dir DIR] \
  [TIMEOUT OPTIONS] -- CONTROLLER_COMMAND [ARG ...]
```

For example:

```bash
reploy controlled-session run \
  --controller-dir ./controller-staging \
  --workload-dir ./workload-staging \
  --endpoint web \
  --columns 120 --rows 40 \
  --output-dir ./session-artifacts \
  -- run-controller
```

`--endpoint` is optional and repeatable. Each value must name an endpoint
declared by the exact workload generation. Terminal dimensions are required
and must be from 1 through 65535. The command and every argument after `--` run
in the controller; the workload always runs `/bin/sh` in its exact image.

The optional timeouts are:

| Option | Default | Accepted range |
| --- | ---: | ---: |
| `--startup-timeout` | `30s` | `15s` through `5m` |
| `--termination-grace` | `5s` | `100ms` through `1m` |
| `--controller-finalization-timeout` | `5m` | `1s` through `1h` |
| `--result-acknowledgement-timeout` | `10s` | `1s` through `1m` |
| `--cleanup-timeout` | `30s` | `1s` through `5m` |

The workload-output-finalization timeout is a host-owned 30 seconds. The
controller receives that value in the `opened` event; it is not a host CLI
option. Expiration never becomes successful completion.

### Session operations

The current `opened.operations` grant contains `input`, `resize`, `terminate`,
and `complete`. Interactive `input` and resize normally travel through the
terminal attachment. The structured stream also exposes `resize`, `terminate`,
and `complete` requests; it adds no operation that can change the admitted
deployments, endpoint grants, mounts, or network policy.

### Endpoint grants

For every selected endpoint, `opened` reports its logical `id`, `scheme`,
lease-local `host`, and container `port`. The controller connects with an
ordinary native client. Reploy does not proxy application traffic through the
session protocol, and it does not publish a workload port on the host.

Endpoint IDs are stable intent, but the initial Docker implementation uses one
private network shared by exactly the controller and workload. That network
currently permits bidirectional traffic between them and permits the
controller to reach undeclared workload ports. It does not provide routes to
unrelated containers, host-local networks, or the public Internet unless the
corresponding ordinary network access was separately declared. Do not treat an
endpoint grant as destination-port isolation in this release, and do not expose
sensitive controller listeners on the session network.

### Controller outputs

The output options are mutually exclusive and never expose the host
destination to the workload:

- `--output-dir` creates the host directory if needed, mounts it only into the
  controller, and sets `REPLOY_OUTPUT_DIR` to its controller-visible path. Its
  contents remain directly visible across successful and failed teardown
  outcomes. Use it for multiple files or partial failure evidence.
- `--output-file` requires an existing parent directory and a destination that
  does not already exist. It sets `REPLOY_OUTPUT_FILE` to one staged
  controller-visible path, which the controller must create as a regular file.
  Reploy publishes the host destination only after controller finalization
  completes; otherwise it discards the unpublished staging file.

The initial output contract rejects `--output-dir` and `--output-file` when the
controller application runtime uses UID 0. Use a non-root controller runtime
until the separate root-safe output contract is implemented.

The controller must close and finalize its artifacts before sending
`complete`. Reploy bounds and reports output handling but does not interpret,
redact, or validate controller artifacts. The controller remains responsible
for secrets and privacy in recordings, screenshots, logs, and rendered media.

## Controller client

`reploy-session-client` has two modes inside the controller:

```text
reploy-session-client client
reploy-session-client attach --socket PATH
```

`client` is the supported structured integration boundary. It consumes the
controller-private `REPLOY_SESSION_SOCKET` implicitly and exchanges UTF-8 JSON
Lines on stdin and stdout. Human-readable diagnostics go to stderr.

`attach` connects to the private terminal socket reported by `client`. Its
stdin and stdout contain terminal bytes only. It forwards terminal resize
events and ordinary Ctrl-C input and exits after ordered terminal output has
been drained. Only one attachment is accepted, and v1 has no reconnect or
replacement attachment.

The host-side framed protocol behind `REPLOY_SESSION_SOCKET` and the private
broker-to-attachment transport are internal contracts between matching Reploy
components. Controllers integrate with the JSON Lines stream and the
`attach` command, not those private transports.

### Structured stream

Every structured message contains:

```json
{"schema":"reploy-controlled-session-client-v1","type":"ready"}
```

The stream accepts exactly one newline-terminated JSON object per message, up
to 1 MiB including the newline. V1 rejects malformed JSON, invalid UTF-8,
duplicate or unknown fields, unknown message types, and trailing data.
`diagnostic.code` and `client-error.code` are open machine-code enums; handle
an unknown well-formed code as a generic diagnostic.

The controller receives these events in lifecycle order:

| Event | Meaning |
| --- | --- |
| `broker-ready` | The private `terminal_socket` exists; start the attachment. |
| `opened` | Reports granted operations, endpoints, dimensions, and the output-finalization timeout. |
| `ready` | The workload is started and session operations may begin. |
| `workload-exit` | Reports the observed workload process status. |
| `terminating` | Reports the first latched termination cause. |
| `diagnostic` | Reports a non-terminal or lifecycle diagnostic. |
| `workload-outputs-finalized` | Reports `drained` or `failed` terminal-output finalization. |
| `terminated` | Carries the authoritative structured session result. |
| `client-error` | Reports a fatal local broker or public-stream failure when possible. |

`broker-ready` is first, then `opened`, then `ready` or startup failure. If
workload exit causes termination, `workload-exit` precedes `terminating`; for
controller termination, host cancellation, or another observed failure,
`terminating` precedes any later `workload-exit`.

Start the attachment promptly: it must connect to `terminal_socket` within 10
seconds after `broker-ready`. This fixed broker deadline is independent of the
host startup timeout. Missing it ends the client with `attach_timeout`; v1 has
no host option to extend it.

After `ready`, the controller may write:

```json
{"schema":"reploy-controlled-session-client-v1","type":"resize","columns":100,"rows":30}
{"schema":"reploy-controlled-session-client-v1","type":"terminate"}
{"schema":"reploy-controlled-session-client-v1","type":"complete"}
{"schema":"reploy-controlled-session-client-v1","type":"acknowledge-terminated"}
```

Interactive input and resize normally travel through `attach`; structured
`resize` supports headless orchestration. `terminate` requests workload
termination. `complete` says controller artifacts are finalized. After reading
`terminated`, send `acknowledge-terminated` and continue reading until the
broker closes cleanly.

There is no request-accepted response in v1. A consumed request proves local
validation and transport submission; later trusted events and `terminated`
are authoritative. Premature, duplicate, malformed, or unauthorized requests
are fatal, except repeated `terminate` and valid `resize` remain idempotent.

The `client` process exits `0` only after it forwards `terminated`, forwards
its acknowledgement, and observes clean host-channel closure. `attach` exits
`0` after a drained or clean no-output terminal end. Never infer workload
success from the attachment, recorder, or broker process exit status; read the
structured result.

## Lifecycle and failure handling

The host lifecycle is `preparing`, `active`, `terminating`, then `terminated`.
The first termination cause is latched. Controller status, workload status,
output finalization, runtime observation, controller finalization, cleanup,
result delivery, and result acknowledgement remain separate facts.

A robust controller follows this sequence:

1. Start `reploy-session-client client` and read `broker-ready`.
2. Start one terminal attachment, optionally under a recorder.
3. Read `opened` and `ready`, then perform controller actions.
4. On `terminating`, keep consuming terminal output until the attachment ends.
5. Finalize and close every controller artifact.
6. Send `complete` even when terminal-output finalization failed, if the broker
   remains available.
7. Read `terminated`, preserve its full result, and send
   `acknowledge-terminated`.
8. Treat a non-clean broker close or any unsuccessful result field as failure.

If terminal-output finalization fails, retain and close partial artifacts,
send `complete`, consume and acknowledge `terminated`, and still report the
failure. Abandoning the broker loses result-delivery evidence and cannot turn
the session into a success.

Host stdout is exactly one JSON object followed by one newline after valid
argument parsing. Its schema is
`reploy-controlled-session-run-result-v1`; human notices and diagnostics use
stderr. The object always contains `schema`, `ok`, `error`, `session_result`,
`result_delivered`, `result_acknowledged`, `controller_status`,
`controller_output`, `delivery_tail_cleanup_status`, and
`delivery_tail_recovery_action`. Phases that did not produce a value are JSON
`null`.

Controller output reports `not-requested`, `directory-retained`,
`file-published`, `file-discarded`, or `failed`. `ok` is true only when the
termination cause is controller-requested or a zero-code workload exit and all
finalization, observation, cleanup, delivery, acknowledgement, and requested
output handling succeeded.

The host command exits:

- `0` exactly when `ok` is true;
- `1` for another parsed operational outcome;
- `2` for a usage error, with no stdout object; and
- `130` or `143` on Unix `SIGINT` or `SIGTERM`, after bounded cancellation,
  cleanup, and structured result output.

An abrupt host death such as `SIGKILL` cannot emit a result. Reploy's watchdog
performs bounded cleanup and records a private incident receipt when needed.
An unacknowledged result or a cleanup recovery action is failure evidence, not
successful completion.

## Security defaults and limitations

Controlled sessions use Reploy's ordinary application-container sandbox. They
do not create a special weaker or stronger security tier. Both application
containers use an explicit runtime identity, a read-only root filesystem plus
declared writable storage, seccomp, `no-new-privileges`, and empty Linux
capability sets. They receive no privileged mode, host namespaces, host
devices, inherited host environment, or Docker socket.

Authority remains declaration-driven:

- The controller cannot select another workload generation, arbitrary host
  paths, raw endpoint destinations, runtime resources, or additional network
  access after admission.
- The workload cannot access the private session channel or controller output.
- Controller compromise can disclose everything explicitly granted to that
  controller, including workload terminal data, endpoint traffic, and its own
  artifacts. Those grants are not safe from the controller itself.
- Public and local networking default to deny, but separately declared
  controller or workload networking remains available under the ordinary
  blueprint policy.

Initial limitations include Linux hosts running Docker only;
one controller and one attachment per session; no reconnect, ownership
transfer, remote execution, persistent controller cache, writable source-copy
feature, multiple concurrent sessions per controller, domain filtering, URL
filtering, or content inspection. A controller or workload deployment with a
configured private environment is also rejected because controlled-session
private-environment injection is not implemented. The current private endpoint
network has the coarse reachability described above. A backend that cannot
establish the required Linux sandbox fails closed instead of silently
degrading.

## Integration profiles

These profiles use the same generic boundary. They do not change the session
abstraction or grant profile-specific host authority.

### OmegaFlow recording

OmegaFlow runs in the controller and starts the broker. After
`broker-ready(terminal_socket)`, it must start the byte-only attachment within
the 10-second attachment deadline. The controller container itself has no TTY,
so OmegaFlow allocates one for unmodified asciinema 3.x. This equivalent Bash
example quotes the generated commands and uses util-linux `script` as the PTY
wrapper:

```bash
printf -v attach_command '%q ' \
  reploy-session-client attach --socket "$terminal_socket"
printf -v record_command '%q ' \
  asciinema record --quiet --window-size 120x40 --return \
  --command "$attach_command" "$REPLOY_OUTPUT_DIR/session.cast"
script --quiet --return --echo never \
  --command "$record_command" /dev/null
```

Use a safely quoted socket value obtained from the decoded event; do not parse
it from terminal output. A controller with its own PTY process API can allocate
the terminal directly instead of using `script`; no asciinema modification or
control-socket support is required. OmegaFlow can use the granted endpoint
coordinates for Playwright or Chromium while the same workload shell stays
alive. After the attachment exits, it finalizes the cast, screenshots,
diagnostics, and rendered media, sends `complete`, reads and stores
`terminated`, then acknowledges it.

Reploy owns PTY bytes, endpoint coordinates, lifecycle truth, and bounded
artifact retention. OmegaFlow continues to own command-completion detection,
cwd reporting, action markers, terminal-to-browser handoff, browser actions,
recording policy, redaction, and media rendering.

### Sandboxed agent

Place the agent runtime and policy code in the controller deployment and the
project under test in the workload deployment. Drive its shell through the
attachment or use structured resize for a headless terminal. Grant only named
workload endpoints the agent needs and write transcripts or reports to a
controller output directory.

The split keeps project code and terminal output untrusted without giving the
agent a Docker socket or host process authority. It does not make the trusted
agent controller safe from the data and endpoints explicitly granted to it,
and it does not provide domain-level egress policy.

### Security inspection

Place scanners and inspection orchestration in the controller and the target
application in the workload. Use named endpoints for dynamic inspection and a
controller output directory for findings and partial evidence. Treat terminal
output, files, and service responses as hostile input, and retain the full
structured result with the report so cleanup or observation failures remain
visible.

This profile supplies process separation and deny-oriented container defaults;
it is not a network IDS, HTTP policy engine, malware containment guarantee, or
content-sanitization layer. The controller/workload private-network limitation
still applies.

## Compatibility fixtures

The public golden fixtures in
[`testdata/controlled-session`](https://github.com/omry/reploy/tree/main/testdata/controlled-session)
contain every controller-stream event and request, malformed request cases,
and nullable and complete host-result shapes. Use them as conformance inputs
for a controller implementation. The public v1 stream remains compatible
under Reploy's normal public compatibility policy; a future incompatible
version requires an explicit version selection and a different schema.

The deeper rationale and private implementation design are documented in the
[controlled-session design](https://github.com/omry/reploy/blob/main/docs/CONTROLLED_SESSION_DESIGN.md).
