---
status: Active
updated: 2026-08-27
summary: Keep local-source build recipes developer-owned and outside package distribution.
---

# ADR 0001: Keep Local-Source Build Recipes Developer-Owned

- Decision state: Accepted
- Implementation state: Implemented

## Context

Some local projects require tools outside their language ecosystem to build.
OmegaConf, for example, invokes Java and ANTLR when producing its source
distribution. Declaring Java in the consuming application assigns the
requirement to the wrong owner and may leave a build-only tool in the workload
image.

## Decision

An explicitly selected local project may declare its build requirements in a
strict `.reploy.yaml` at its project root:

```yaml
schema: 1
project: omegaconf
type: python
build: setuptools-legacy
requires:
  - tool:java==21
```

- `project` must match the selected normalized package name.
- `type` must match the selected package provider.
- The project developer owns this metadata.
- Reploy owns the portable `tool:` vocabulary and its platform mappings.
- Reploy reads the recipe only for a selected local filesystem override.
- Build tools belong to the local build environment, not the final workload.
- Published packages do not activate local project recipes.

`requires` accepts the same canonical portable-tool request forms as runtime
application tools. An optionless requirement may use compact scalar shorthand,
including an upstream version and, when the tool's version scheme permits it,
an exact definition-revision suffix:

```yaml
requires:
  - tool:java==21
  - tool:java==21~2
```

A request with bindings or selections uses a structured mapping:

```yaml
requires:
  - tool: playwright
    version: "1.61.0"
    definition_revision: 1
    binding: python
    select:
      browser: chromium
```

The recipe supplies build context and an isolated source-builder scope. Reploy
canonically merges repeated same-tool requirements in that scope: version
constraints accumulate, exact revision pins must agree, explicit binding and
selection sets union, omitted bindings retain inference, and `binding: "*"`
dominates other binding demands. Catalog resolution, acquisition, and provider
materialization remain separate downstream responsibilities.

### Supported Python Build Types

The `build` field selects a fixed Reploy-owned build protocol. It is not a
command, shell fragment, backend import path, or arbitrary executable.

`pep517`

- Requires `pyproject.toml` with a `[build-system]` table.
- Uses the project's declared PEP 517 build backend through Reploy's fixed
  Python build frontend.
- Produces an sdist first, retains and validates that artifact, and builds the
  wheel from the retained sdist.

`setuptools-legacy`

- Requires `setup.py` and rejects a project that declares `[build-system]`.
- Uses the standard `setuptools.build_meta:__legacy__` compatibility backend
  through Reploy's fixed Python build frontend; Reploy does not invoke
  `setup.py` as an arbitrary command.
- Permits a `pyproject.toml` used only for unrelated tool configuration, as in
  OmegaConf today.
- Produces an sdist first, retains and validates that artifact, and builds the
  wheel from the retained sdist.

For both build types, `requires` describes portable tools made available for
the complete isolated source-build pipeline, including both sdist and wheel
construction. When `.reploy.yaml` is present, a missing or contradictory build
declaration fails before running project build code. Reploy does not infer a
replacement for the declared type or silently fall back from one type to
another. A local project without `.reploy.yaml` retains the ordinary
provider-owned source build behavior. Future revisions may add equally strict
protocols for other build and packaging systems without changing this
developer-owned recipe model.

## Consequences

This supports cooperative projects developed locally without making Reploy a
source-based package distribution. Project maintainers remain responsible for
publishing outputs through PyPI, npm, APT repositories, container registries,
or other established channels.

Reploy does not maintain central third-party recipes or publish locally built
artifacts under this decision.

## Deferred Work

- General-purpose build environments.
- Shared or remote build caching.
- Central recipe governance and artifact publication.
