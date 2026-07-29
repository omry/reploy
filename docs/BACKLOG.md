---
status: Active
updated: 2026-07-29
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

- [ ] `P1` Add blueprint-configured control-runtime modes.
      Let blueprints select `embedded` or `path` independently for staging and
      installation, defaulting both to the current self-contained `embedded`
      behavior. In `path` mode, generate the control script without copying
      Reploy into the deployment; resolve only the fixed `reploy` executable,
      reject unavailable or incompatible runtimes clearly, and use an absolute
      resolved path for installed service definitions rather than depending on
      an interactive user's `PATH`.

## Pre-release

- [ ] `P1` Audit validation for every blueprint field.
      Inventory each field from strict decoding through interpolation and
      resolution to its final host, Docker, or workload sink. Verify structural,
      semantic, cross-field, and trust-boundary validation; explicitly record
      intentional pass-through values; and add focused tests proving invalid
      values are rejected before external side effects. Turn gaps into discrete
      findings and resolve pre-release blockers before publishing.

- [ ] `P2` Add a triggerless default app-command passthrough.
      Allow an application to expose a native command as the generated control
      script's default action, so application arguments can be passed directly
      (for example, `app model=resnet50`) without a synthetic trigger or `--`
      separator.

- [ ] `P2` Eliminate repeated local-source preparation within one build.
      Build profiling showed portable-tool discovery re-entering Python
      preparation and repeating expensive local-source observation, snapshot,
      and copy work for the same source identity. Reuse safe intermediate
      results within one build attempt across graph retries or tool activation,
      while preserving invalidation when relevant inputs change. Verify the
      improvement with `reploy build --profile` on the OmegaConf development
      build and retain focused tests proving the repeated work is skipped.

- [ ] `P1` Complete the remaining local Docker cleanup.
      After the active OmegaConf staging work is no longer needed, remove its
      preserved stopped container and superseded image, then review the
      remaining dangling images, build cache, and unused volumes. Delete only
      resources whose Reploy ownership is established, avoid broad pruning,
      and finish with a fresh Docker resource and disk-usage inventory.

- [ ] `P1` Complete Docker lifecycle crash consistency and reconciliation.
      Build and finalization output now use deployment-scoped temporary
      references whose surviving provider-store workspaces drive exact cleanup
      on the next locked operation. Extend the same deterministic ownership and
      restart reconciliation to the remaining publication, runtime-control,
      and removal resources: candidate images outside this protocol,
      containers, networks, and removal tombstones. Preserve current
      generations and intentional caches, keep cleanup idempotent and
      deployment-scoped, and provide an explicit diagnostic or cleanup path for
      resources whose staging directory no longer exists.

- [ ] `P1` Define and implement the provider-layer verification-cache lifecycle.
      Accepted provider layers receive shared content-addressed
      `reploy/cache/provider-layer:*` Docker references so `reploy verify` can
      inspect complete build lineage, but ordinary environment cleanup and
      `bundle clean` never reclaim them. Define durable ownership and
      reachability for these intentional cache anchors, preserve every
      reference needed by a current or validated build and any in-progress
      operation, and provide safe inspection and reclamation without relying on
      a broad Docker prune or an existing staging directory. Prove repeated
      changed builds do not grow the cache without bound and that cleanup never
      breaks verification of retained builds.

- [ ] `P1` Perform a full Reploy UX review.
      Review the complete user journey across discovery, staging, development
      overrides, validation, builds, runtime control, installation, updates,
      diagnostics, recovery, and removal. Exercise interactive, verbose,
      redirected, and dumb-terminal behavior; identify unclear concepts,
      inconsistent terminology, missing context or next actions, unnecessary
      friction, and backend details leaking through the public surface; record
      concrete findings and prioritize follow-up slices before implementation.

- [ ] `P2` Group the top-level help commands.
      `reploy --help` currently presents one oversized command list. Organize
      commands under clear functional headings such as lifecycle and monitoring
      while preserving the existing command syntax and discoverability.

- [ ] `P2` Complete the public site documentation.
      Build out the documentation site as the maintained user-facing guide for
      discovering, staging, configuring, running, installing, diagnosing, and
      removing Reploy applications.

- [ ] `P2` Create an OmegaFlow introduction video.
      Once OmegaFlow is ready, record an introductory Reploy workflow using it
      as the representative application.

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

- [ ] `P2` Design general-purpose local-source build environments.
      Extend beyond the implemented fixed Python build recipes without
      weakening their declared protocol boundary. Define supported providers
      and build protocols, command representation if arbitrary commands are
      introduced, build dependencies, execution location, artifact contracts,
      isolation, network policy, access to secrets or host files, and target
      platform behavior.

- [ ] `P2` Design shared and cross-run build caching.
      Decide whether verified outputs or intermediate caches survive across
      runs; bind reuse to source, recipe, toolchain, platform, settings, and
      output integrity; define cache scope, permissions, ownership, and cleanup;
      and make caches inspectable, bypassable, invalidatable, and recoverable.
      Keep this separate from deployment-local provider artifact reuse.

- [ ] `P2` Add optional Watchman-backed local-source change detection.
      After a successful local-source wheel build, retain a synchronized
      Watchman clock and use it to prove cheaply that the complete source tree
      has not changed before considering artifact reuse. Treat a missing
      Watchman installation, fresh instance, recrawl, expired or invalid clock,
      query failure, changed non-source build input, or `--no-cache` as a full
      projection and wheel-build fallback. Do not require the daemon or
      silently ignore development directories that a build backend may consume;
      settle explicit-build reuse semantics and measure the no-op improvement
      before enabling wheel reuse.

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
