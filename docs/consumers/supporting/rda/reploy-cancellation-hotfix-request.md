# Reploy hotfix request: allow native app cancellation cleanup

## Observed problem

Interrupting the qualified Reploy launcher during an RDA connection stops the
run before the application supervisor can publish its cancellation result.
The launcher exits nonzero and contains the container, but `run/result.json`
does not exist. This prevents acceptance of RDA's application cancellation
contract through the actual launcher.

This is separate from the previously merged stdin fix. Reploy has not been
modified for this investigation.

## Reproduction evidence

- Reploy source: `ae1459aaba98be7223b05f2632504dbf6aced2d6`.
- Qualified executable SHA-256:
  `32a39bfbb83470dbd30080b5c19f45151842ff08d817045aa18dd02bafbc1530`.
- Command: the native `run` command from `runtime/rda.blueprint.yaml`, launched
  with `reploy app --dir <stage> --output-dir <fresh-output> run`.
- Application entry: `tini -- /usr/local/bin/python -m rda`.
- Case: `headless-cancel-09`, isolated Windows mock only.
- Injection: SIGINT sent to the actual Reploy launcher PID after the app
  published `step: connect`. No application PID was signalled directly.
- Observed launcher exit: `1`, approximately `0.414` seconds after SIGINT.
- Observed application result: absent. No verified artifact was claimed.
- Local records:
  `temp/delivery-preparation/rda3/headless-cancel-09-verification.json` and
  `temp/delivery-preparation/rda3/headless-cancel-09.log`.

The RDA supervisor installs SIGINT/SIGTERM handlers, uses an independent
execution clock, and reserves five seconds for owned cleanup and atomic result
publication. Its offline cancellation tests pass. A live positive control,
`application-cancel-16`, sent SIGINT directly to the application supervisor
while leaving Reploy running. That control published `reason_code: cancelled`
with null action/artifact and exited `130` in about `1.76` seconds. Its record
is `temp/delivery-preparation/rda3/application-cancel-16-verification.json`.
Direct application signalling does not resolve or substitute for the actual
launcher-SIGINT failure. The launcher test does not establish that the app's
handlers were reached.

## Requested behavior

On launcher interruption, deliver a graceful termination signal to the native
container workload through its init process. Keep its output mount available
while the application handles the signal, cleans up and publishes its result.
Allow a finite grace period, then forcibly contain a nonresponsive workload.
Preserve a nonzero interrupted launcher result and leave no live container.

The exact lifecycle implementation belongs to Reploy. The current evidence
suggests cancellation or teardown completes before application finalization;
it does not identify the precise failing internal call.

## Suggested regression checks

Use a credential-free native test app under `tini`. Its termination handler
should clean up a child and atomically write a small result under the normal
output mount. Interrupt the actual Reploy CLI. Verify the signal is delivered,
the final result survives, the CLI exits nonzero, and the container is removed.
Also test a workload that ignores termination: forced cleanup must remain
bounded. Keep piped stdin, EOF, native argv and exit-status checks passing.

## RDA disposition

External gap in graceful launcher cancellation; no acceptance of that behavior
is claimed. Whether it blocks the POC delivery is awaiting owner disposition,
because the plan also recognises launcher failures and unsupported interruption
exit handling. This classification does not fix the missing application result.
The RDA implementation
continues to use its one in-container supervisor and worker. An extra external
coordinator or a prewritten fake cancellation result is not a truth fix.
Other failure, timeout, headless and debug checks can proceed independently.
