---
status: Active
updated: 2026-08-04
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

## Pre-release

- [ ] `P1` Accept APT install transaction records with the optional trailing
      empty marker. Current APT versions may emit valid `Inst` records ending
      in `) []`, which Reploy currently rejects as malformed. Accept exactly
      the known optional ` []` suffix while preserving the existing package,
      version, and architecture checks. Add regression coverage for ordinary
      installs and upgrades, streaming chunk boundaries, and rejection of
      nonempty, duplicated, or otherwise malformed trailing markers.

- [ ] `P2` Add explicit controlled runtime command aliases for validated
      executable outputs. Keep provider exports non-public by default, and let
      a blueprint opt into mapping ordinary command names such as `rustc` to
      qualified executable profiles. Materialize only those aliases in a
      Reploy-owned executable directory, place that directory first on the
      runtime `PATH` while preserving the base-image path, reject unsafe names
      and collisions, and bind the alias table and effective path into final
      validation, runtime-layer identity, build locks, and cache reuse. Cover
      `reploy shell`, blueprint commands, and subprocess lookup without adding
      a general filesystem-link surface or exposing whole package directories.

- [ ] `P2` Allow application executable profiles to consume explicit base-image
      exports. Accept `source: base` as a reference to the reserved base
      contribution so an application command can use a tool guaranteed by the
      selected immutable base without declaring a redundant OS package. Permit
      applications whose only capabilities are base-backed executable profiles,
      retain application-qualified command references such as `builder.rm`, and
      carry the selected base output through final validation, runtime-path
      protection, build locks, cache identity, and actionable missing-export
      diagnostics. Do not add a parallel direct-command form such as `base.rm`.

- [ ] `P1` Complete native Docker Desktop identity evidence.
      The Linux-container contract now creates a real local account, uses the
      invoking Unix UID/GID, and maps native Windows SIDs to stable nonzero
      numeric identities. Add native macOS and Windows Docker Desktop evidence
      across staged and installed current-user workloads and transient
      commands, including account-name resolution, stable Windows mapping, and
      explicit confirmation that no accidental `0:0` identity is selected.

- [ ] `P1` Implement runtime mount integrity checks 2 and 3.
      `BLUEPRINT_ENVIRONMENT_MODEL.md` specifies three mount-plan checks
      against the exact immutable image; only check 1 (reserved destinations)
      exists, at blueprint validation. Implement check 2 (destination absent or
      an empty real directory, validated ancestors, `lstat` plus a one-entry
      read) and check 3 (overlay subtree does not intersect provider roots,
      exclusive leaf claims, or executable link chains), enforced against the
      complete effective mount plan immediately before creating every runtime
      container. Check 2 needs an image-side inspection operation the probe
      helper protocol does not yet define. The design section is the
      specification; the standing review decision is recorded in
      `docs/.review/BLUEPRINT_ENVIRONMENT_MODEL.md` (B1-1) and both status
      notes are updated when this lands.

- [ ] `P1` Define root-safe explicit output-file and output-dir contracts.
      Preserve the prohibition on arbitrary host binds while treating a
      caller-selected output destination as a narrow explicit grant. For
      `--output-file`, perform a focused security review of fresh staging,
      ownership and mode normalization, regular-file and link validation,
      race-free publication without overwrite, interruption recovery, and
      cleanup. For `--output-dir`, require an initially empty dedicated
      directory, define ownership normalization and failure retention, and
      reject unsafe targets before contacting Docker. Add cross-platform
      integration tests proving a root workload can create the requested
      outputs but cannot reach the destination parent or unrelated host data.

- [ ] `P1` Add blueprint-configured control-runtime modes.
      Let blueprints select `embedded` or `path` independently for staging and
      installation, defaulting both to the current self-contained `embedded`
      behavior. In `path` mode, generate the control script without copying
      Reploy into the deployment; resolve only the fixed `reploy` executable,
      reject unavailable or incompatible runtimes clearly, and use an absolute
      resolved path for installed service definitions rather than depending on
      an interactive user's `PATH`.

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

- [ ] `P2` Design explicit remote Docker support.
      Replace today's rejected ambient `DOCKER_HOST` and remote-context behavior
      with an intentional distributed-runtime contract. Define input snapshot
      upload, output-file and output-dir extraction with safe local publication,
      image export or remote placement, port forwarding, remote identity and
      permission semantics, authenticated transport, lifecycle ownership,
      interruption recovery, deterministic cleanup, and a defensible way to
      establish or replace host-path namespace equivalence. Keep local Docker
      Engine and Docker Desktop behavior distinct from generic remote daemons,
      containerized Reploy, and Unix-socket proxies.

- [ ] `P2` Consider a Reploy host configuration surface.
      Keep a standing inventory of concrete operator- or host-owned settings
      that do not belong in blueprints, staging overrides, or installation
      state. Do not add a general configuration file until the accumulated use
      cases justify its scope, precedence, user/system ownership, validation,
      and portability. Initial potential use case: host-owned DNS resolver
      configuration used to provide DNS under the coarse application network
      grants. The
      default local-capable path should use the host's configured resolver so
      VPN and split-DNS behavior remains available; the public-only path should
      use the built-in Google Public DNS profile (`8.8.8.8`, `8.8.4.4`). Allow
      host configuration to override either choice. Resolver selection is
      machine policy, not blueprint policy.

- [ ] `P2` Design and implement a Reploy userland L3 policy gateway. Keep this
      separate from the initial public/local kill switches and controlled
      sessions. Define separate controller and workload network identities, a
      capability-free application network namespace, a one-shot route
      initializer, an isolated data path whose only peer is the gateway,
      private gateway control, root-resistant route invariants, IPv4/IPv6 and
      DNS policy, directional destination and port grants, auditing, resource
      limits, failure behavior, reconciliation, and Docker/Podman plus Desktop
      integration. Treat native engine primitives as fast paths rather than
      exposing backend network modes as product policy.

      Make the gateway the target controlled-session endpoint policy. Permit
      native TCP from the controller only to declared workload addresses and
      ports; deny workload-initiated access to the controller, undeclared
      workload ports, unrelated containers, and ungranted networks. Preserve
      native application traffic while replacing the initial coarse
      two-container shared-network policy after parity is proven. Make every
      gateway rule, address, and network resource lease-owned and reconcile it
      during teardown. Include Docker, Podman, Desktop, hostile-root,
      multi-user-host, concurrent-connection, interruption, and cleanup tests
      proving the workload cannot reverse the route or bypass the endpoint
      grant and unrelated local processes cannot reach the workload endpoint.

      Replace the temporary, discouraged
      `environment.runtime.network.ambiguous: allow` escape hatch with precise
      translated-destination policy and deprecate that coarse override.

- [ ] `P2` Evaluate and prioritize the Dingo development-environment gaps.
      Use `docs/DINGO_GAPS.md` as the needs and evidence record for portable
      checkout binding, development execution, shell initialization, pinned
      source workspaces, ROS dependencies, robot hardware and networking, and
      ARM64 handoff. Confirm each boundary against current behavior, separate
      broadly useful Reploy capabilities from ROS-specific integration, and
      turn accepted priorities into focused follow-up backlog items without
      treating this document as an implementation plan.

- [ ] `P2` Implement Reploy repository schemas and publication tooling.
      Follow `docs/REPOSITORY_DESIGN.md` as the policy source. Define strict
      schemas for the repository descriptor, current publisher authorization,
      shared asset version and revision records, indexes, immutable asset
      targets, publisher DID attestations, repository acceptance records,
      lifecycle events, and portable tool definitions. Use the same SemVer,
      PEP 440, integer, and opaque version implementation for blueprints and
      tools, with a separate positive Reploy revision for each exact upstream
      version. Keep repository records and locks structured even where the CLI
      offers a compact full-pin selector.

      Implement deterministic repository compilation and static validation.
      Current publisher authorization governs new publication only; historical
      releases validate through their retained publisher attestation and
      TUF-authenticated repository acceptance record. Retain immutable lifecycle
      events for yank, archive, delete, publisher revocation, rescission, purge,
      and ownership transfer, and make the current index reference their
      effective state. Reject release-coordinate reuse, implicit ownership
      transfer, and invalid transitions. Permit a Reploy repository to live in
      a declared subdirectory of a larger Git repository without allowing input
      or generated-output paths to escape that root.

      Keep the ordinary `reploy` executable consumption-only. Provide separate
      publisher and repository-maintainer tools for DID signing, authorization,
      validation, lifecycle administration, deterministic compilation, TUF
      signing, and publication. Keep direct BURLs first-class; a present remote
      publisher attestation must validate against the exact asset and a
      currently authorized DID key, while durable historical key evidence is a
      property of repository acceptance.

- [ ] `P2` Implement Reploy repository clients and offline import.
      Integrate a conformant TUF 1.x client; load static HTTPS and filesystem
      repositories plus pretrusted plain-HTTP repositories; share the
      URL-shaped locator model with blueprint sources while preserving typed
      semantics; and implement explicit APT-like `repository trust` and
      `repository update [REPOSITORY]` behavior without automatic network
      refresh during resolution or build. Keep user and system trust separate,
      authorize explicit asset surfaces, preserve mutable repository priority
      and source pins, and fail equal-priority ambiguity.

      Cache authenticated snapshots and immutable objects; bind the exact
      repository, version scheme, upstream version, Reploy revision, asset
      digest, publisher attestation, and repository acceptance record into build
      locks; and validate and atomically import complete repository bundles for
      disconnected systems. Retain the selected asset and both attestations in
      the deployment-owned provider-store closure, and transfer that closed set
      during installation. Keep trusted roots, accepted TUF metadata, accepted
      indexes, and effective lifecycle state as active repository state.
      Treat the separate global immutable-object cache as acceleration only so
      size-, age-, or recency-based eviction never requires discovering staged
      or installed deployments and never breaks lock replay.
      Existing locks must never follow moving repository state, and repository
      failure must not corrupt the last valid local snapshot. Ordinary yank,
      archive, and delete semantics preserve local replay where the design
      allows it; an accepted publisher-security revocation deliberately blocks
      affected locked and cached replay while retaining safe teardown commands.
      A client that has not received the revocation cannot enforce it.

- [ ] `P2` Publish the official Reploy repository and documentation.
      Publish an independently updated official repository using the common
      protocol. Keep only human-authored source and tests on the primary branch.
      Put every generated target, TUF metadata update, signature, and publishing
      commit on a separate persistent, automation-owned `publish` branch after
      deterministic compilation, signing, and validation succeed. Any temporary
      signing branches must be deleted without merging into the primary branch.
      Generate blueprint and tool pages from validated repository records. Tool
      pages show supported operating systems, releases, architectures,
      contributed OS package roots, executables, network behavior, artifacts,
      validation, and final-image placement. Do not embed a fallback index in
      Reploy.

- [ ] `P2` Migrate Java to an official portable tool definition.
      Replace the hard-coded `tool:java` package mapping and validation switches
      with an official repository definition while preserving its existing
      project-owned, build-only behavior and build-lock identity. Add focused
      Debian-derived integration coverage before removing the old path.

- [ ] `P2` Add an official portable Rust toolchain definition.
      Model Rust as one versioned toolchain rather than independent `rustc` and
      Cargo tools so the compiler, Cargo, and Rustdoc remain compatible. Resolve
      and lock exact platform artifacts and provenance, materialize them
      offline beneath a Reploy-owned root, and expose `rustc`, `cargo`, and
      `rustdoc` through the shared controlled runtime-command mechanism rather
      than a Rust-specific `PATH` path. Add version, platform, cache-reuse,
      shell, and Cargo-subprocess coverage, and do not depend on a host
      toolchain or a networked `rustup` bootstrap during materialization.

- [ ] `P2` Add official Playwright portable tool support.
      Add the reviewed resolver primitive and official `tool:playwright`
      definition using the shared asset version and revision model. Resolve the
      compatible browser payload, contribute documented OS package roots, keep
      project source and host credentials out of networked acquisition,
      materialize offline, and lock and validate exact platform, browser,
      artifact, and definition identities. Require explicit browser selection
      and list the target-supported browser values when it is missing or
      unsupported. Prove multi-browser selection produces one order-independent,
      deduplicated OS-requirement union and retains exact identity for each
      browser payload.

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
