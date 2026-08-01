---
status: Proposed
updated: 2026-08-02
summary: Specific Reploy capability and usability gaps exposed by the Dingo ROS 2 development workspace.
---

# Dingo Gaps

## Purpose

Dingo is a shared ROS 2 Humble development workspace that uses PuppyPi as a
pinned upstream source dependency. It is intended to support collaborative
experimentation first and more serious robot development later.

The Dingo exercise tested whether Reploy can own that environment without a
parallel Dockerfile or Compose definition. Reploy successfully built the base
environment, installed APT dependencies, provided a writable non-root checkout
mount, and ran an interactive container in which all 15 PuppyPi packages built.

This document records the remaining product needs. It distinguishes capabilities
that Reploy cannot currently express from capabilities that work but require
avoidable ceremony. It intentionally describes needs and desired user
experience, not implementation or schema design.

## Evidence boundary

The following findings were observed directly:

- the Dingo checkout remained a host-owned bind mount and was writable by the
  container process running as UID/GID 1000;
- ROS 2 Humble and the required APT packages were materialized successfully;
- PuppyPi was imported at commit
  `dfff97c42331f97497050a5558e650bde41bf54d`;
- rosdep reported that all system dependencies were satisfied;
- all 15 discovered PuppyPi packages built with colcon; and
- the resulting ROS overlay resolved built packages correctly.

Only `linux/amd64` was exercised. ARM64, physical PuppyPi hardware, ROS
networking outside the container, RViz display integration, and remote
deployment were not tested. Findings in those areas are requirements implied by
the intended robot workflow rather than completed runtime observations.

## Needs Reploy cannot currently express

### Portable local-checkout binding

When a user deliberately stages a blueprint from the local filesystem, the
committed blueprint needs a portable way to identify the checkout that contains
it without embedding an absolute host path. Every contributor should be able to
clone Dingo and stage the same local blueprint from an arbitrary location.

Exposing the resolved checkout to the environment remains an explicit
host-access authorization. A remote or packaged blueprint must not infer a
nearby checkout or silently receive access to any host source tree.

Current consequence: Dingo carries a template plus a Python staging wrapper
that replaces a marker with the local absolute checkout path before Reploy can
validate and stage it.

### Direct command execution

`reploy shell` already accepts piped standard input without allocating a TTY,
forwards the standard streams, and preserves the shell's exit status. This
supports non-interactive shell programs supplied through standard input.

Automation, CI, and agents also need to pass a command as a direct argument
vector, select its working directory inside the environment, and receive its
standard streams and exit status without encoding the operation as shell input
or declaring it in the blueprint first.

Current consequence: Dingo can pipe a shell program into `reploy shell` or
expose predeclared app commands, but it cannot directly execute
`./scripts/build.sh` with an explicit argument vector and container working
directory as a one-shot development operation.

### Development-shell selection and initialization

A development environment needs to select an appropriate shell and establish
the base image's expected environment before presenting the prompt. For Dingo,
`ros2` and `ROS_DISTRO` should be available on entry, and Bash-specific
workflows should not unexpectedly run under `/bin/sh`.

Current consequence: Reploy opens `/bin/sh` directly. Dingo users must source
the ROS setup explicitly, and Bash-style `source` failed until the
instructions were changed to the POSIX dot form.

### Pinned development source trees

A workspace needs to declare and materialize a generic upstream source tree at
an exact revision for use as source, not only as an application package or the
location of a Reploy blueprint.

Current consequence: Dingo owns a separate `.repos` manifest and invokes
`vcs import` from a repository bootstrap script. The PuppyPi checkout is
outside Reploy's staging and environment lifecycle.

### ROS dependency resolution

The environment needs to derive its required system packages from the ROS
packages included in the workspace and verify that the resulting image
satisfies them.

Current consequence: rosdep was run separately, missing packages were discovered
through trial runs, and four ROS APT package names were then copied manually
into the blueprint.

### Robot-device access

Robot development needs narrow, explicit access to selected serial and USB
devices, cameras, LiDAR, GPIO-related interfaces, and the host groups or
capabilities required to use them. Access should remain narrower than a
privileged container.

Current consequence: the blueprint cannot express the physical-device boundary
needed to run hardware-facing PuppyPi code inside the Reploy environment.

### ROS-compatible networking

ROS 2 development needs a networking mode that supports DDS discovery,
multicast, and dynamic peer traffic across the container, host, and robot where
required. Fixed application endpoint publication is not sufficient for this
traffic model.

Current consequence: Dingo can build ROS packages in isolation but cannot
describe a portable Reploy boundary for participating in a real ROS graph.

### Verified ARM64 deployment bundle

A development host needs to export a verified, platform-specific deployment
bundle for a compatible PuppyPi target. For `linux/arm64`, that bundle needs
to contain:

- the built `linux/arm64` OCI image;
- its immutable digest and checksums;
- the resolved runtime and deployment description required to launch it;
- build identity and verification evidence; and
- the minimum compatible Reploy version.

The target needs to import and verify the bundle, then install it without
rebuilding the image or resolving the original blueprint. Host-specific
secrets, mounts, devices, and network policy remain explicit installation-time
inputs. The source blueprint may be retained as provenance, but it is not the
deployment payload.

Current consequence: Reploy can select an ARM64 container platform locally, but
it cannot produce or consume this verified deployment unit.

### Per-platform build and verification state

A blueprint may declare several compatible platforms, while each staging
directory currently represents one selected platform and one build lineage.
Tracking all declared platforms therefore requires a new state model capable of
retaining separate platform-specific build and verification lineages. Users
need durable evidence showing which declared platforms have actually been built
and verified without conflating declarations with completed work.

Current consequence: one staging directory cannot track build and verification
lineage for all declared platforms. This is a missing state capability, not
only a presentation or usability gap.

### Multiple persistent workloads

A later Dingo environment may need to operate several persistent cooperating
processes, such as a simulator, bridge, robot nodes, and a supporting UI, with
independent lifecycle and readiness.

Current consequence: one environment permits at most one primary workload.
This is not a present Dingo blocker and should remain deferred until a concrete
multi-process workflow requires it.

## Supported capabilities with usability gaps

### Development lifecycle

Staging, building, and opening a shell all work. The user must nevertheless
understand several separate states and commands, including whether staging or
the built image is absent or stale.

Desired experience: one obvious development entry point reports the current
state, performs only the necessary preparation, and leaves the user in a ready
environment.

### Non-root runtime identity

The checkout bind mount retained its host ownership; it was not remapped to the
container identity. The container process ran as numeric UID/GID 1000 and could
write to that host-owned checkout. Some standard tools did not recognize the
numeric process identity as a named user; sudo reported that the user did not
exist in the passwd database.

Desired experience: ordinary user and home discovery works consistently while
Reploy clearly communicates that runtime package installation and elevation are
not available.

### System-package additions

Tracked blueprint packages and development-only override additions are both
supported. The difficult part is moving from a failing tool's dependency report
to the correct shared or local environment change.

Desired experience: missing system packages are presented as a reviewable
environment requirement, with an explicit choice between local-only
experimentation and shared blueprint intent.

### Local-to-shared intent

Developer overrides support local experimentation without rewriting the
application blueprint. A collaborative repository also needs a clear path from
a successful local choice to a proposed shared change.

Desired experience: users can always tell which choices affect only their
staging directory and which choices are suitable to review and commit for every
contributor.

### Validation depth

Blueprint validation, package resolution, image construction, and application
smoke testing exist as separate capabilities. Lightweight validation succeeded
even though shell initialization and project build problems remained.

Desired experience: validation levels and their evidence are explicit, and a
single final report distinguishes syntax validation, dependency resolution,
image validation, and user-selected workspace checks.

### Progress and diagnostics

The build reported meaningful Reploy-level phases, but dependency resolution
could remain quiet for substantial periods. This makes it difficult for a
human or an automated caller to distinguish slow progress from a stalled
operation.

Desired experience: durable progress identifies the current activity, elapsed
time, cache reuse, and final result in forms suitable for interactive terminals,
redirected logs, and automated clients.

### Desktop and visualization integration

RViz and related packages can be installed in the environment, but the user
must independently arrange display, Wayland or X11, audio, and GPU access on
each host.

Desired experience: Reploy can report the desktop capabilities available to a
development environment and clearly identify missing host integration without
silently granting broad access.

## Product priority

The first general-purpose development-workspace slice should cover:

1. portable local-checkout binding with explicit host-access authorization;
2. direct argument-vector and working-directory execution; and
3. development-shell selection and initialization.

Pinned development-source acquisition should be a separate follow-up slice.
ROS dependency discovery and integration should be another separate follow-up
slice rather than part of the initial general-purpose workspace work.

The first robot-facing slice should cover:

1. narrow device access;
2. ROS-compatible networking; and
3. verified platform-specific deployment bundles, beginning with an ARM64
   PuppyPi target.

Multiple persistent workloads should remain later work until Dingo has a
specific orchestration case. The current non-root identity, immutable image,
explicit dependency, and narrow host-access boundaries should remain product
strengths while these gaps are addressed.
