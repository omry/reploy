---
status: Active
updated: 2026-07-07
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

- [ ] `P1` Make bundle-build bootstrap failures bounded and actionable.
      `reploy bundle build` now has quieter pip output, verbose timing, uv build
      backend support, and a Docker named-volume runtime cache, but bootstrap
      and wheelhouse preparation can still block on network or index behavior
      without a clear next action. Acceptance checks: decide what timeout or
      failure boundary applies to pip/uv bootstrap and wheelhouse work; report
      the phase that is blocked without dumping raw Docker output in quiet mode;
      preserve verbose timing/output for diagnosis; and verify the behavior on a
      clean Docker image with uncached Python dependencies.

- [ ] `P1` Add bounded and actionable failure handling for Python runtime
      preparation.
      `reploy app config check`, install hooks, and first service start can
      sit at `Preparing Python runtime...` while the generated Compose command
      creates or reuses the named-volume venv and installs requirements inside
      the container. Reploy now has a quiet-mode spinner and persistent Docker
      runtime cache, but the first-prep slow path still needs bounded,
      diagnosable failure behavior. Acceptance checks: keep quiet mode readable
      without dumping pip logs; add a timeout or similarly actionable failure
      path for blocked runtime preparation; preserve verbose mode for raw
      command output; avoid speculative per-package noise; and verify the
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

- [ ] `P2` Improve bare `reploy` output when an app is installed.
      Top-level unknown app commands now suggest `reploy app ...`, and generic
      short usage is better, but the no-argument app summary still centers the
      full app subcommand list. Acceptance checks: design a shorter installed or
      staged app summary that identifies the active app and context; show the
      most useful general Reploy commands alongside a small app-command sample
      or pointer; keep full app command discovery available through explicit
      `reploy app`; and update tests/docs so bare `reploy` reads as a Reploy
      entry point, not only as an app command menu.

- [ ] `P2` Create a neutral demo service blueprint.
      Build or adopt a small Python service that can showcase Reploy without
      anchoring the story to Arbiter or another domain-specific app. The demo
      should be realistic enough to exercise staging, bundle preparation,
      configuration, persistence, ports, health checks, install, status/logs,
      update, and uninstall. Acceptance checks: pick the demo service shape;
      define the blueprint and app commands; make the service useful as a docs
      and smoke/demo target; and capture the stage-to-install-to-operate
      storyline.

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
