---
status: Active
updated: 2026-07-06
summary: Active planning surface for Reploy design and implementation gaps.
---

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

## Pre-release

- [ ] `P1` Add Docker command interruption integration coverage.
      Reploy should explicitly test what happens when long-running Docker and
      Docker Compose commands are interrupted, especially on Windows Docker
      Desktop where Ctrl+C can leave the parent CLI waiting or one-off
      containers behind. Acceptance checks: add an opt-in integration test that
      starts a unique Compose project with a long-lived one-off command, sends
      an interrupt, measures time to return, inspects for leftover containers
      and networks, and always runs idempotent cleanup; cover
      `docker compose run --rm --no-deps` as used by app commands, compare
      `docker compose up` if useful, and document the Windows PowerShell versus
      Linux shell behavior observed by the test.

- [ ] `P1` Make bundle-build pip bootstrap progress bounded and useful.
      `reploy bundle build` can sit at `upgrading pip` while preparing the
      build container, with no clear progress and no quick failure if the pip
      bootstrap is blocked by network or index behavior. Acceptance checks:
      decide whether upgrading pip belongs in every bundle build or should be
      cached/pinned; show useful quiet-mode progress for pip bootstrap and
      wheelhouse build without dumping raw Docker output; add a bounded timeout
      or similarly actionable failure path; keep verbose timing breakdowns for
      bootstrap versus wheelhouse work; and verify the behavior on a clean
      Docker image with uncached Python dependencies.

- [ ] `P1` Add bounded and actionable progress for Python runtime preparation.
      `reploy app config check`, install hooks, and first service start can
      sit at `Preparing Python runtime...` while the generated Compose command
      creates a venv and installs requirements inside the container. Reploy now
      shows a quiet-mode spinner for runtime actions, but the slow path still
      needs bounded, diagnosable behavior. Acceptance checks: keep quiet mode
      readable without dumping pip logs; add a timeout or similarly actionable
      failure path for blocked runtime preparation; preserve verbose mode for
      raw command output; avoid speculative per-package noise; and verify the
      experience on Windows Docker Desktop and Linux.

- [ ] `P2` Remove old verbose Docker command argv compatibility.
      Reploy currently supports both full `container.argv` commands and the
      newer `command_defaults.container.argv_prefix` plus per-command
      `container.argv_suffix` form. Before release, choose the compact command
      schema as the only supported authoring style or define the narrow
      exception for full command overrides. Acceptance checks: update parser
      validation and errors; update blueprint docs and examples; migrate local
      fixtures and known app blueprints; and keep tests that prove the chosen
      schema produces the same generated runtime commands.

- [ ] `P1` Reposition the homepage and intro around Reploy as a cross-platform
      app installer.
      Reploy's differentiator is not merely running Docker or installing the
      Reploy CLI; it is a portable app-install contract that maps one blueprint
      onto host-appropriate staging, dependencies, config, ports, lifecycle
      controls, health checks, install targets, and success output. Acceptance
      checks: update the homepage and intro to say this plainly; describe
      blueprints as semantic app intent rather than Unix path templates; align
      Linux, macOS, and Windows positioning with the current support matrix; and
      avoid implying that package managers such as Homebrew, winget, or Scoop
      solve the same problem. Cross-platform install location design lives in
      `docs/CROSS_PLATFORM_INSTALL_LOCATIONS.md`.

- [ ] `P2` Add Windows path and Docker Desktop failure-mode smoke follow-ups.
      Core Windows Docker Desktop staging and Docker-managed persistent-install
      evidence is recorded in `docs/archive/WINDOWS_PORT.md`. Remaining
      follow-up coverage should target cases not proven by the successful
      PowerShell smoke: project paths with spaces, normal drive-letter project
      paths, Docker Desktop unavailable or wrong-container-mode failures,
      bind-mount failures, and port conflicts. Acceptance checks: keep these as
      focused follow-up smokes rather than blocking the core Windows
      Docker-managed install evidence; record any support-boundary changes in
      the archived Windows port appendix or support matrix.

- [ ] `P2` Add first-class install scope and per-scope target defaults.
      Make `user` and `system` install scope an explicit required choice
      instead of inferring intent from paths, host platform, backend, or
      privileges. Acceptance checks: define the CLI and installed-state shape
      for the explicit scope; validate that explicit scope against backend
      capabilities; add per-scope, per-OS target defaults such as
      `install.target.default_paths.user.windows` and
      `install.target.default_paths.system.linux`; preserve the simple
      platform-aware default path behavior for blueprints that do not need
      scope-specific overrides; require root/admin or a clear privilege path
      for every supported system scope; and document unsupported combinations
      with actionable errors. Design notes live in
      `docs/CROSS_PLATFORM_INSTALL_LOCATIONS.md`.

## Post-v1

- [ ] `P2` Document blueprint structure and feature semantics.
      Audit the current blueprint authoring docs against parser validation and
      generated Compose behavior, then close concrete gaps. Acceptance checks:
      cross-check top-level sections, install owner/ports/managed paths, bundle
      options, Docker service/runtime settings, commands, app/deployed command
      exposure, managed config directories and single-file paths, generated
      mount paths, bootstrap creation behavior for writable app commands, and
      strict start/install preflights; update the realistic blueprint example;
      and remove or correct any docs that describe obsolete schema behavior.

- [ ] `P2` Find a convincing Reploy demo app.
      Identify a useful Python service that is genuinely awkward to run well
      with plain Docker, and use it to show why Reploy's staging, bundle,
      install, and control-script flow helps. Acceptance checks: shortlist a
      few candidate services; pick one with realistic configuration,
      dependencies, persistence, and operational commands; define the demo
      storyline from stage to install to operate; and capture what docs/video
      assets the demo should produce.

- [ ] `P2` Consider an app-author blueprint template UX.
      Explore a command or documented flow that generates an initial blueprint
      skeleton for app authors. Acceptance checks: define the target command
      shape, inputs, generated files, and defaults; include a minimal Python
      service example; decide how much app/runtime detection is appropriate;
      and make the generated blueprint usable as a starting point without
      implying it is production-ready.

- [ ] `P2` Add a Homebrew release path for macOS.
      Make Reploy installable through Homebrew once macOS artifacts are ready.
      Acceptance checks: decide whether to use a tap or submit to homebrew-core;
      define formula ownership and update flow; wire checksums to GitHub Release
      artifacts; document the install command; and smoke-test install, upgrade,
      and uninstall on both Apple Silicon and Intel macOS where practical.

- [ ] `P2` Evaluate Podman as a uniform userland backend.
      Investigate whether rootless Podman on Linux plus Podman Machine on macOS
      and Windows can provide a shared user-scope install/control/uninstall
      backend with better cross-platform smoke parity. Acceptance checks:
      compare Quadlet/user systemd, `podman generate systemd`, and
      `podman compose`; define required Linux rootless preflights such as user
      namespaces, subuid/subgid, cgroup v2, rootless networking, user systemd,
      and linger; document VM-backed host semantics on macOS and Windows; and
      decide whether this belongs as a first-class backend beside Docker.
      Design notes live in `docs/FUTURE_DIRECTIONS.md`.
