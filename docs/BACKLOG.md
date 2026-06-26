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
      Implemented foundation: `reploy uninstall` accepts `--from`,
      `--service-name`, `--list-services`, `--remove-dir`, and `--dry-run`;
      target-present uninstall reads installed state, runs Compose cleanup,
      disables/removes the systemd unit, reloads systemd, and optionally removes
      the target directory; target-missing uninstall can recover the Compose
      project from the systemd unit and remove Docker containers/networks by
      Compose labels.
      Remaining acceptance checks: validate on a real installed Arbiter service
      for both target-present and manually-deleted target flows.

- [ ] `P1` Complete side-by-side install validation and docs.
      Implemented foundation: staging Docker identity is derived from the
      deployment path using a stable slug/hash; installed Docker identity is
      derived from service name plus target path; install accepts single-port
      and named `--port` overrides; installed docker.env and state record the
      resolved compose project, container name, network name, and ports. Real
      host validation proved `/opt/arbiter2` and `/opt/arbiter3` can run
      concurrently with separate service names, containers, and ports alongside
      the existing install and staging deployment.
      Remaining acceptance checks: document the side-by-side install/uninstall
      flow and validate uninstall against the recorded install metadata on a
      real host.

- [ ] `P2` Create a Docusaurus documentation site.
      Build a dedicated docs website for Reploy and publish it at
      `reploy.yadan.net`. Acceptance checks: scaffold Docusaurus in the repo
      without disrupting the Go module or Python packaging; move or mirror the
      operator-facing README material into structured docs; add install,
      bundle, side-by-side install, and uninstall pages; configure the site
      title, navbar, footer, and custom domain; and add a local docs build check
      to the normal validation path.

- [ ] `P2` Define and validate formal Windows support.
      Reploy should have explicit Windows behavior instead of accidental
      partial support. Acceptance checks: publish a support matrix for Windows
      staging/development commands versus permanent install/uninstall; build
      and smoke-test `reploy.exe` for init/update/info/bundle/app flows with
      Docker Desktop where applicable; make Linux-only commands such as
      systemd-based install/uninstall fail with clear platform errors; and
      decide whether a Windows service backend is in scope or explicitly
      deferred.

- [ ] `P2` Define and validate formal macOS support.
      Reploy should have explicit macOS behavior instead of assuming Linux-like
      service management. Acceptance checks: publish a support matrix for macOS
      staging/development commands versus permanent install/uninstall; build
      and smoke-test macOS binaries for init/update/info/bundle/app flows with
      Docker Desktop where applicable; make systemd-based install/uninstall fail
      with clear platform errors on macOS; and decide whether a launchd backend
      is in scope or explicitly deferred.

## Post-v1
