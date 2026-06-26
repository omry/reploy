# Reploy Backlog

## Agent instructions

When helping with backlog work, treat this file as the active planning
surface for Reploy. Keep it short, concrete, and easy to scan. Prefer
moving work between queues over growing process, and avoid inventing GitHub
issues unless the user asks for them.

This file is the day-to-day queue for design and implementation gaps.

## How to use this file

- Keep each item small enough for one focused change.
- Put only the most urgent items in `Now`.
- Prefer richer items with brief context and concrete acceptance checks.
- Move completed items out instead of keeping a long archive here.
- Treat install, packaging, deployment, and blueprint items as operator-facing
  product work, not only as internal refactors.
- After each focused phase, run a focused review of the phase diff and commit
  the ready changes before starting the next phase.
- At every phase boundary or pause, state the current action, why work is
  stopping, and whether the next step needs user review, approval, input, or no
  user action.

## Now

- [ ] `P1` Support uninstall for installed Reploy deployments.
      Operators need a first-class cleanup path both when the installed target
      directory still exists and when it has already been deleted. Acceptance
      checks: add an uninstall command with dry-run output; when the target
      exists, stop the systemd service, run deployment cleanup from the target,
      disable and remove the unit, reload systemd, and optionally remove the
      target directory; when the target is missing, clean up the service setup
      from the service name and clearly report any Docker resources that could
      not be verified or removed; record enough install metadata to make future
      uninstall reliable; and add tests for both target-present and
      target-missing flows.

- [ ] `P1` Support side-by-side installs with distinct service identity and
      ports. Operators should be able to install multiple instances from the
      same blueprint without container, network, unit, or port collisions.
      Acceptance checks: make install accept or preserve an explicit service
      name and host port; derive staging Docker identity from the deployment
      path, using a stable slug/hash instead of the raw full path; use the
      install service name plus target path as the installed instance identity
      for the default systemd unit, compose project, container name, and network
      name unless explicitly overridden; write install-specific Docker
      environment overrides without corrupting generated defaults; record
      service, target, unit path, container name, network name, and host port in
      install state; prove two installed instances can coexist; and document the
      side-by-side install and uninstall flow.

## Post-v1
