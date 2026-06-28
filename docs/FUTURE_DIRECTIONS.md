# Future Directions

Ideas that are intentionally outside the current direct/staged install
redesign. This document keeps longer-term product directions visible without
turning them into near-term acceptance criteria.

## Operating Systems

Reploy has two separate OS dimensions:

- binary targets: where the `reploy` CLI can run
- deployment targets: where Reploy can install and operate a managed service

Future work should define support levels for each combination. For example,
macOS and Windows may be useful CLI/staging hosts before they are supported as
permanent deployment targets. Linux/systemd can remain the first deployed
runtime target while other service managers are evaluated.

Open questions:

- Which OSes should get official CLI binaries?
- Which OSes should support staging workflows?
- Which OSes should support permanent install/update/uninstall?
- How should unsupported deployment operations fail on otherwise supported CLI
  platforms?

## Blueprint Index

The blueprint index may evolve toward an `apt-get update` style workflow: a
cached local catalog used for shorthand resolution, discovery, search, and
possibly trust metadata.

Possible commands:

```text
reploy index update
reploy index search arbiter
reploy index show arbiter-server
```

Open questions:

- Should the index remain a simple JSON catalog or become a richer signed
  metadata source?
- Should index entries include app ids, descriptions, supported platforms,
  latest versions, and blueprint package refs?
- Should search work offline from the cached index?
- What trust or verification model is needed before installing from indexed
  shorthand names?

## Deployment Environments

The current redesign focuses on local host deployment, initially Linux/systemd.
Other deployment environments may become useful later.

### AWS

AWS support could mean several different things: EC2/systemd installs, ECS,
Lambda-style workers, or generated infrastructure. Those are materially
different products and should not be hidden behind a vague `--aws` flag.

### Kubernetes

Kubernetes overlaps with Reploy but does not replace it. Kubernetes is a runtime
orchestration platform; Reploy's value is packaging an app-owned blueprint into
a repeatable install/update experience with staging, bundle selection, app
commands, generated config, and operator-safe controls.

A Kubernetes backend could make Reploy generate and apply Kubernetes resources
from the same blueprint model. In that world, "deployment" might mean a
namespace, Helm-like release, or generated manifests instead of a systemd
service. The generated control surface would likely wrap `kubectl`-level
operations such as status, logs, restart/rollout, and health checks.

Open questions:

- Is Kubernetes a backend for the same blueprint model, or a separate
  integration layer?
- Should Reploy generate raw manifests, Helm charts, Kustomize overlays, or use
  a native API client?
- How do staging and direct install map to Kubernetes namespaces or clusters?
- What is the equivalent of the generated `<app-id>ctl` script for Kubernetes?

## Additional Bundle Providers

The current provider direction starts with Python/PyPI. Source repositories and
system packages should be treated as additional bundle providers, not separate
one-off install modes.

### Source Provider

Reploy now has an initial generic Git source provider for HTTPS repositories.
It fetches source with a built-in Git client, resolves branch and tag refs to a
commit hash in staging state, locates the blueprint by the Python
`package_name/reploy` convention unless `#PATH` is supplied, and builds the
provider package from the checked-out source.

Current refs:

```text
reploy stage git:https://github.com/org/repo.git?ref=main
reploy install git:https://github.com/org/repo.git#package_name/reploy?ref=v1.2.3
```

Open questions:

- Should a future blueprint index map Reploy versions or app versions to
  upstream commit hashes?
- Should GitHub-specific shorthand such as `github:org/repo` exist, or should
  the generic `git:https://...` form remain the only source-repo spelling?
- Which build steps are blueprint-declared versus provider-specific?
- What build dependencies are required, and how are they declared?
- What build environment is used?
- Can builds run isolated from the host OS to improve reproducibility and
  stability?
- How is the target OS and architecture selected for the build?
- Can the staging build target differ from the host running Reploy?
- How is source provenance recorded in deployed state?

### System Package Provider

Bundles may eventually support system packages such as `dpkg` artifacts as a
provider alongside Python/PyPI packages.

This could help apps that need native tools, service-side utilities, or
distribution-packaged dependencies. It also raises privilege, platform, and
dependency-resolution questions that are different from Python wheel bundles.

Open questions:

- Are `.deb` packages copied into the bundle, referenced from apt repositories,
  or both?
- Does Reploy install system packages on the host or inside generated runtime
  images?
- How are package sources, signatures, and trust handled?
- How does this interact with non-Debian deployment targets?
- How is the package target OS and architecture represented?

## Install Profiles

A future workflow may allow a staging environment to export a portable install
profile that can later be directly installed on another system.

Conceptual flow:

```mermaid
flowchart LR
  B[Blueprint]
  S[Staging]
  P[Install profile]
  D[Deployed]

  B -->|stage| S
  S -->|export| P
  P -->|direct install| D
```

This should not be treated as rewriting the app-owned blueprint. A blueprint is
owned by the app author; a profile exported from staging would be an
operator-prepared install source derived from that blueprint.

An install profile might include:

- base blueprint ref and version
- selected bundle options
- resolved package or artifact versions
- portable generated configuration
- default direct-install automation
- upgrade policy metadata
- generated control script metadata

It should exclude secrets and host-specific state by default. Any future support
for including sensitive or host-specific values needs explicit policy and clear
reporting.
