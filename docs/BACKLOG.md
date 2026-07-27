---
status: Active
updated: 2026-07-27
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

No active items.

## Pre-release

- [x] `P1` Restore private workload environment injection.
      Support an optional deployment-local `.env` as a Reploy-owned runtime
      input rather than blueprint or Docker image configuration. Acceptance
      checks: require a real owner-readable host file with no group or other
      access, or the supported-host equivalent; open and validate it without
      following links or replacement races; transmit its values through a
      one-shot private Reploy launcher channel only into the workload process
      environment; keep the file, variable names, and values out of container
      mounts, Docker image and container metadata, Compose content, argv, state,
      locks, and Reploy-generated diagnostics; reject runtime mounts that would
      expose the file or an ancestor directory; copy it on initial install when
      needed, preserve it by default, and honor the managed-path replacement
      overrides without silently deleting it when the staging source is absent;
      and prove the isolation and install behavior with focused and real-Docker
      tests.

- [x] `P1` Model applications separately from package materialization layers.
      Keep each application's OS packages, language packages, executables, and
      related declarations in one self-contained blueprint block. Allow
      environment-level OS tools such as `zsh` and `htop`, then combine those
      packages with application-owned OS dependencies in the shared OS package
      transaction while retaining application ownership in diagnostics and
      overrides. Replace the current assumption that each blueprint component
      is owned by exactly one package provider without preserving the unreleased
      schema, and define stable identities for applications, provider
      contributions, executables, build locks, and cached results before
      implementation.

- [x] `P1` Minimize the required environment blueprint shape.
      Make environment nodes optional whenever Reploy can supply an
      unambiguous default or infer the value safely. Acceptance checks: audit
      every required environment node; retain required fields only where
      omission would be ambiguous or unsafe; add parser and validation tests
      for the smallest useful blueprint; and make the blueprint documentation
      start with that minimal example before introducing optional features.

## Post-v1

- [ ] `P2` Generate a maintained official base-image index.
      Add a server-side tool that clones `docker-library/official-images` and
      produces one deterministic, versioned index file containing the official
      operating-system image names, maintained tag groups, and supported
      platforms. Run it periodically in GitHub Actions, update the published
      file only when its semantic content changes, and retain upstream source
      provenance so generated changes are reviewable.

- [ ] `P2` Add cached official base-image discovery.
      Let the base-image override editor search the generated official-image
      index instead of querying Docker Hub or the local Docker daemon. Fetch
      and cache the index on first use, periodically probe for updates without
      blocking ordinary editor use, retain a usable cached copy across
      transient failures, and filter results against the staged platform.

- [ ] `P2` Design local-source build environments and cross-run caching.
      Local source packages should use an explicit build command. Define its
      argv-versus-shell representation, input and output artifact contract,
      execution location, build dependencies, isolation, network policy, and
      access to secrets or host files. Decide whether verified outputs or
      intermediate caches survive across runs; bind reuse to source, toolchain,
      platform, settings, and output integrity; define cache scope, permissions,
      ownership, and cleanup; and make caches inspectable, bypassable,
      invalidatable, and recoverable. Keep this separate from deployment-local
      provider artifact reuse and the current override-validation work.

- [ ] `P2` Design portable environment export and import.
      Separate exact offline transfer from instruction-based rebuilds and model
      application configuration as opaque managed assets before promising a
      portable staged application. Define boundaries for secrets, unmanaged
      binds, persistent data, and user-edited preserved files; then settle the
      archive, lock-replay, compatibility, and public CLI contracts.

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
