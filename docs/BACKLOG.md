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

- [ ] `P1` Redesign the direct/staged install and update CLI shape.
      Reploy should support direct install from blueprint defaults for any
      bundle and a staging environment as the configured full app workflow
      surface. Some services may install directly but still not work usefully
      without staging-time configuration. Deployment should be an installed
      runtime created directly from blueprint defaults or installed/updated
      from staging, with only a narrow service-control surface exposed through
      a generated app control script such as `arbiterctl`; the full Reploy
      binary should not be included in the deployment. Design note:
      `docs/STAGING_DEPLOYMENT_DESIGN.md`.

      Acceptance checks: define direct install behavior as available for every
      bundle through positional app refs such as `reploy install APP_REF`,
      default-only, with no install-time user configuration for now, and with
      optional blueprint-declared default automation such as bundle defaults
      and non-interactive app commands; implement direct install through a
      temporary internal staging-like workspace by default, with a
      low-prominence `--in-place` flag for conserving peak disk space;
      keep the normal `reploy ...` command surface as staging-only, resolved by
      cwd detection, explicit `--dir`, or the default staging directory; add
      blueprint-declared defaults for the installed target path, deployed port
      bindings, staging port bindings, and installed owner user/group, with
      operator overrides preserved; define generated app control script naming
      and the deployed control command menu; define install/update semantics
      for first install, updating an existing deployment, side-by-side installs,
      dry-run planning, and
      Linux/root/systemd requirements; add blueprint-declared upgrade policy
      for named app-owned artifact classes; preserve installed config/artifacts
      by default; treat `.reploy/` as fully Reploy-owned generated state that
      can be replaced at will; replace hardcoded config replacement flags with
      artifact overrides such as `--replace ARTIFACT`, `--replace all`, and
      `--clean`, where `--clean` behaves like deleting the deployment directory
      and installing fresh; keep normal install/update output concise while
      showing detailed artifact plans for `--dry-run`, `--verbose`,
      `--replace`, and `--clean`; add indexed search output; and update docs,
      CLI help, and tests to match.

- [ ] `P1` Complete side-by-side install validation and docs.
      Implemented foundation: staging Docker identity is derived from the
      deployment path using a stable slug/hash; installed Docker identity is
      derived from service name plus target path; install accepts single-port
      and named `--port` overrides; installed docker.env and state record the
      resolved compose project, container name, network name, and ports. Real
      host validation proved `/opt/arbiter2` and `/opt/arbiter3` can run
      concurrently with separate service names, containers, and ports alongside
      the existing install and staging deployment. Uninstall is implemented for
      target-present and target-missing flows through `reploy uninstall --from`,
      `--service-name`, `--list-services`, `--remove-dir`, and `--dry-run`.
      Remaining acceptance checks: document the side-by-side install/uninstall
      flow and validate uninstall against the recorded install metadata on real
      installed services.

- [ ] `P2` Create a Docusaurus documentation site.
      Build a dedicated docs website for Reploy and publish it at
      `reploy.yadan.net`. Acceptance checks: scaffold Docusaurus in the repo
      without disrupting the Go module or Python packaging; move or mirror the
      operator-facing README material into structured docs; add install docs
      with side-by-side install notes, plus bundle and uninstall pages; configure
      the site title, navbar, footer, and custom domain; and add a local docs
      build check to the normal validation path.

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
