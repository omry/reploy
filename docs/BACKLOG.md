---
status: Active
updated: 2026-07-05
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

- [ ] `P2` Remove old verbose Docker command argv compatibility.
      Reploy currently supports both full `container.argv` commands and the
      newer `command_defaults.container.argv_prefix` plus per-command
      `container.argv_suffix` form. Before release, choose the compact command
      schema as the only supported authoring style or define the narrow
      exception for full command overrides. Acceptance checks: update parser
      validation and errors; update blueprint docs and examples; migrate local
      fixtures and known app blueprints; and keep tests that prove the chosen
      schema produces the same generated runtime commands.

- [ ] `P2` Audit the macOS port-plan appendix.
      The macOS port is already implemented, so post-implementation corrections
      should not rewrite the historical design plan as if it were still being
      designed. Acceptance checks: keep the original macOS port plan body as
      historical design context; put settled terminology and status corrections
      in a clearly labeled appendix; make current user-facing docs use
      "Docker-managed permanent install" and "OS service install" consistently;
      and avoid duplicating the same correction across multiple doc surfaces.

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
      solve the same problem.

## Post-v1

- [ ] `P2` Document blueprint structure and feature semantics.
      Make the blueprint authoring docs cover the actual supported structure,
      not only examples. Acceptance checks: document top-level sections,
      install owner/ports/managed paths, bundle options, Docker service/runtime
      settings, commands, app/deployed command exposure, managed config
      directories and single-file paths, generated mount paths, bootstrap
      creation behavior for writable app commands, and strict start/install
      preflights; include a realistic blueprint example and cross-check the
      docs against parser validation and generated Compose behavior.

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

- [ ] `P2` Define and validate formal Windows support.
      Reploy should have explicit Windows behavior instead of accidental
      partial support. Acceptance checks: publish a support matrix for Windows
      staging/development commands versus permanent install/uninstall; build
      and smoke-test `reploy.exe` for stage/update/info/bundle/app flows with
      Docker Desktop where applicable; make Linux-only commands such as
      systemd-based install/uninstall fail with clear platform errors; and
      decide whether a Windows service backend is in scope or explicitly
      deferred; document WSL as officially supported through the Linux path,
      not as a native Windows backend; and distinguish Docker-managed permanent
      install from Windows Service install. Planning details live in
      `docs/WINDOWS_PORT.md`.

- [ ] `P2` Define and validate formal macOS support.
      Reploy should have explicit macOS behavior instead of assuming Linux-like
      service management. Acceptance checks: publish a support matrix that
      positions macOS as a development/staging host; build and smoke-test macOS
      binaries for stage/update/info/bundle/app/runtime/test flows with Docker
      Desktop where applicable; define and smoke-test normal install/uninstall
      as a Docker-managed permanent install with a warning about weaker
      macOS/Windows Docker-runtime security; make Linux/systemd OS service
      guarantees clear; and keep launchd OS service install as a future design
      topic. Planning details live in `docs/MACOS_PORT.md`.

- [ ] `P2` Add a Homebrew release path for macOS.
      Make Reploy installable through Homebrew once macOS artifacts are ready.
      Acceptance checks: decide whether to use a tap or submit to homebrew-core;
      define formula ownership and update flow; wire checksums to GitHub Release
      artifacts; document the install command; and smoke-test install, upgrade,
      and uninstall on both Apple Silicon and Intel macOS where practical.
