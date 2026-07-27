---
status: Active
updated: 2026-07-27
summary: Applications own their complete package intent; providers own shared physical materialization nodes.
---

# ADR 0002: Separate Applications from Provider Materialization

- Decision state: Accepted
- Implementation state: Complete

## Context

The schema-1 draft models every non-base entry in `environment.components` as
one provider-owned component. That makes a Python application and its Python
layer the same object. It cannot naturally express one application's Python
packages, OS libraries, executable profiles, and optional features together.
It also makes a shared OS transaction look like application ownership was lost.

The schema is unreleased, so compatibility with the component shape would
preserve the wrong boundary.

## Decision

Blueprints model the base, environment packages, and applications separately:

```yaml
environment:
  base:
    image: python:3.11-slim

  packages:
    os: [zsh, htop]

  applications:
    inspector:
      packages:
        os: [default-jre-headless]
        python:
          requirements: [omegaconf-inspector]
      options:
        profiling:
          description: Install profiling support.
          packages:
            os: [linux-perf]
            python:
              requirements: [py-spy]
      executables:
        inspector:
          source: python
          binary: omegaconf-inspector
```

`environment.base` is the unique base-image root. `environment.packages.os`
contains environment-owned OS tools. Every application map key is its stable
application ID. An application's `packages` and `options` keep all of that
application's provider-specific package intent together. The initial provider
keys are `os` and `python`; more packaging systems may be added without changing
application ownership.

An executable profile remains publicly identified as
`<application>.<executable>`. Its required `source` selects one contribution
owned by that application, such as `python` or `os`; `binary` selects an output
from that contribution. Commands never select a physical layer.

## Stable identities

The canonical ownership identities are:

- base root: `base`;
- environment contribution: `environment/<provider>`;
- application: `application/<application>`;
- application contribution: `application/<application>/<provider>`;
- executable profile: `application/<application>/executable/<executable>`.

These are structured canonical identities, not user-authored path syntax.
Application and executable segments use the provider-identifier grammar.
Provider names are registry-owned canonical names; the initial public `os`
contribution is realized by the detected OS provider, currently APT on supported
Debian-derived images.

Physical provider-node identities remain independent:

- `base` for the base root;
- the detected OS provider's node, currently `apt`, for the one shared OS
  transaction;
- `python/application/<application>` for each isolated Python application
  environment.

Every resolved provider contribution carries its ownership identity. A physical
node carries the sorted contribution identities it combines. Provider requests,
bundle manifests, requirement profiles, output provenance, graph edges, build
locks, cache identities, and diagnostics retain those identities. The shared
OS node therefore combines packages without erasing whether each request came
from the environment or a particular application.

Package overrides remain provider/package choices because one selected package
version or local source must be consistent across the environment. Selection
and diagnostics additionally report every owning contribution. Package
additions are environment-owned contributions.

## Consequences

- One application can contribute to several package providers without becoming
  several user-facing components.
- OS packages are resolved and installed once while ownership remains visible.
- Commands and executable profiles follow application identity rather than
  materialization topology.
- The old `environment.components` application schema is rejected rather than
  translated.
- Any change to the combined OS request invalidates the shared OS node and its
  downstream layers; provider-local requests retain their narrower cache keys.
