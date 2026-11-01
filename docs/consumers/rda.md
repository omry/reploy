---
status: Native-only cancellation fix under verification; independent PR pending
updated: 2026-11-02
summary: Preserve bounded native-application cancellation cleanup and result publication when the actual Reploy launcher is interrupted.
---

# RDA

## Requested outcome

When the operator interrupts the actual Reploy CLI, signal the native container
workload through its init process and retain its output mount while it handles
termination, cleans up and atomically publishes its result. Allow a finite grace
period, then forcibly contain a nonresponsive workload. The interrupted launcher
must exit nonzero and leave no live container.

Source: [retained cancellation hotfix request](supporting/rda/reploy-cancellation-hotfix-request.md),
received on 2026-11-01 from
[the RDA checkout](/home/omry/dev/rda/docs/plans/rda/supporting/implementation-plan/reploy-cancellation-hotfix-request.md).
The retained request is a byte-for-byte copy, SHA-256
`b41bba760e85b4d5804df1f88534596ba98f5efbfa0e563c60be66458cb78f24`.
It is separate from the previously merged stdin hotfix. The source document has
since moved and added later reproductions and RDA's POC disposition. The retained
original stays unchanged; the requested Reploy behavior is the same.

## Consumer-reported evidence

RDA reports the failure against Reploy source
`ae1459aaba98be7223b05f2632504dbf6aced2d6`, using the qualified executable
and hashes recorded in the retained request:

- `headless-cancel-09`: run the native blueprint command through
  `reploy app --dir <stage> --output-dir <fresh-output> run`, with application
  entry `tini -- /usr/local/bin/python -m rda`. After `step: connect`, send SIGINT
  to the actual Reploy launcher PID. The launcher exits `1` after approximately
  `0.414` seconds; `run/result.json` is absent.
- `application-cancel-16`: signalling the application supervisor directly while
  leaving Reploy running produces `reason_code: cancelled`, null action/artifact,
  and exit `130` in approximately `1.76` seconds. This is a positive control for
  application cleanup, not acceptance of launcher interruption.

The app reserves five seconds for owned cleanup and result publication. That is
consumer evidence, not a selected Reploy-wide grace-period default. The raw records are
located under RDA's `temp/delivery-preparation/rda3/`, as listed in the retained
request. Reploy has now inspected those records and reproduced the
failure with an isolated native fixture. Cancellation killed the attached engine
CLI, then forced removal before the application could finalize its output.

## Acceptance checks for the hotfix

Use a credential-free native fixture under `tini` and the normal output mount:

1. Interrupt the actual Reploy CLI, without directly signalling the application.
   Verify signal delivery through init, child cleanup, atomic result publication,
   retained output, a nonzero CLI result and removal of the owned container.
2. Repeat with a workload that ignores graceful termination. Verify forced
   containment completes within a finite bound and leaves no live container.
3. Preserve piped stdin, EOF, native argv and exit-status behavior. Keep genuine
   launcher failures distinguishable from a completed application cancellation.

An external coordinator, direct application signalling, or a prewritten fake
cancellation result does not satisfy the launcher-interruption check. Retain
failed or uncertain cleanup as an explicit failure, rather than reporting
successful containment or inventing an application result.

## Status and next action

The request is recorded in [the backlog](../BACKLOG.md). A native-only lifecycle
fix passes six actual-CLI cases: SIGINT, SIGTERM, terminal process-group SIGINT,
an application that ignores termination, piped input and empty-input EOF. Graceful
cases permit child cleanup and atomic result publication; interrupted commands
exit nonzero and their owned containers are removed. Native argv and exit status
remain intact. Independent review and PR approval remain pending.

Run the repeatable fixture with
`python3 tools/e2e/native_cancellation.py --reploy /path/to/packaged/reploy --output-dir /new/evidence/path`.
It uses a Python image and `tini`, without credentials or an RDA deployment.
The tested setup is Linux amd64 inside WSL2, kernel
`6.18.54.1-microsoft-standard-WSL2`, Docker Desktop `4.93.0 (240920)`,
Docker client/engine `29.8.1`, and Go `1.26.8`. These POSIX signal tests do not
qualify native Windows, macOS or Podman interruption.

The fix requests the engine's ordinary container stop before forced removal.
It preserves the configured stop signal/grace, with a 30-second host limit on
the stop request and a separate five-second removal limit. It does not establish
a five-second application grace default. `--output-dir` keeps application-written
files after cancellation. Failed commands still do not publish `--output-file`.

On 2026-11-01, the maintainer set the continuation order: finish the F/G design
and implementation slices, deliver this hotfix, then deliver milestones F and G.
The F/G design PR is now approved. The hotfix is based on main independently of
that design commit. Preserve the approved F/G work while completing this hotfix.

Complete an independent hotfix PR with its required review and exact-head CI gates. No merge,
release or live-service change is authorized. Design decisions and qualification
gates remain required; the sequence does not pre-approve unresolved behavior.
