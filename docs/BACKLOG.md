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

- [ ] `P0` Revert hosted macOS persistent-install CI policy change.
      Revert the CI/docs change that downgraded hosted macOS integration to
      runtime-only after the runner exposed a non-Docker-Desktop backend.
      Acceptance checks: persistent-install coverage is restored or the workflow
      fails clearly at the unsupported runner boundary; the remaining runtime
      smoke fix is kept only if it still reflects the intended smoke contract;
      and any decision to change macOS install coverage is made explicitly by
      the maintainer, not hidden inside a fix.

## Pre-release

No active pre-release items.

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
