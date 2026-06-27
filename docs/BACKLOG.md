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

No active backlog items.

## Post-v1

- [ ] `P2` Find a convincing Reploy demo app.
      Identify a useful Python service that is genuinely awkward to run well
      with plain Docker, and use it to show why Reploy's staging, bundle,
      install, and control-script flow helps. Acceptance checks: shortlist a
      few candidate services; pick one with realistic configuration,
      dependencies, persistence, and operational commands; define the demo
      storyline from stage to install to operate; and capture what docs/video
      assets the demo should produce.

- [ ] `P2` Define and validate formal Windows support.
      Reploy should have explicit Windows behavior instead of accidental
      partial support. Acceptance checks: publish a support matrix for Windows
      staging/development commands versus permanent install/uninstall; build
      and smoke-test `reploy.exe` for stage/update/info/bundle/app flows with
      Docker Desktop where applicable; make Linux-only commands such as
      systemd-based install/uninstall fail with clear platform errors; and
      decide whether a Windows service backend is in scope or explicitly
      deferred.

- [ ] `P2` Define and validate formal macOS support.
      Reploy should have explicit macOS behavior instead of assuming Linux-like
      service management. Acceptance checks: publish a support matrix for macOS
      staging/development commands versus permanent install/uninstall; build
      and smoke-test macOS binaries for stage/update/info/bundle/app flows with
      Docker Desktop where applicable; make systemd-based install/uninstall fail
      with clear platform errors on macOS; and decide whether a launchd backend
      is in scope or explicitly deferred.
