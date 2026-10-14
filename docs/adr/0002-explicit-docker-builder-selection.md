---
status: Active
updated: 2026-10-14
summary: Honor an explicit Docker-compatible builder selection without changing the supported default.
---

# ADR 0002: Honor Explicit Docker Builder Selection

- Decision state: Accepted
- Implementation state: Implemented (HF5)

## Context

Reploy creates verified temporary local image references for generated builds.
A containerized BuildKit worker connected to a Podman compatibility socket cannot
see Podman's local image store. It tries to pull those references from a registry.
The operator's private control showed that Docker CLI with `DOCKER_BUILDKIT=0`
uses Podman's Docker-compatible build API and Buildah's local image store.
Reploy previously overwrote that explicit selection with `1`.

## Decision

Keep the shared ordinary `docker build` command. Default to `DOCKER_BUILDKIT=1`
when the variable is unset or empty. Honor explicit boolean values with Docker's
boolean parsing rules, normalized to `0` or `1`. Reject invalid values before the
build command. This applies to all generated image builds, including provider
layers, finalization, application runtime layers and source-builder environments.
Keep the verified local Docker endpoint, platform, immutable image inspection,
owned temporary references, cancellation and cleanup rules.

An explicit `0` is an experimental compatibility opt-out. It does not grant
support to every Docker-compatible engine or to Docker's classic builder. The
builder must execute the generated offline `RUN --network=none`, read-only bind
mounts and controlled image-config instructions. Unsupported features must fail;
Reploy does not weaken the generated Dockerfile or its validation.

The BuildKit materialization assembly identity remains unchanged. The API mode
uses renderer profile `docker-materialization-v1-docker-api`. In that profile,
`AssemblyKeyV1.DockerfileFrontend` records the digest in the rendered syntax
comment, not an assertion that the API builder executed that frontend. The mode
thus cannot be confused with pinned-frontend execution under the default profile.
This extends the default-only builder descriptions in the APT design documents
for the explicit experimental opt-out.

Assembly identity is an input identity, not a predicted image ID or a full engine
version identity. Accepted results still require inspection by immutable ID and
all existing config and filesystem validation. Current deployment reuse remains
based on its validated image and lock. Changing the builder preference alone
allows reuse; `reploy build --no-cache` requests a fresh build with that preference.
No cache schema migration or shared assembly-image lookup is introduced.

## Limits and qualification

Docker's CLI opt-out for Linux's legacy build route is deprecated. This decision
does not guarantee its continued availability. A future replacement transport
requires its own design; HF5 adds no direct Buildx or Libpod API dependency.

Podman compatibility inspection can omit healthcheck metadata. A native NONE
healthcheck can also trigger a user-systemd timer in the reported runtime.
Those issues remain open. HF5 does not infer NONE from absent metadata, disable
healthchecks, or claim unmodified Podman build/verify/install support.

The operator's evidence used Podman 6.1.4 and diagnostic health metadata/timer
controls. Local qualification must state its actual engine version and distinguish
command-path feature checks from end-to-end acceptance without such controls.

The local Podman 4.9.3 feature check passed local-base builds, offline RUN,
read-only mounts, result loading, image execution and failed-RUN rejection. It
also left an empty `/.reploy-build` directory, which fails Reploy's existing
scratch-absence check. Passing those individual features is not complete image
acceptance. That validation remains unchanged; the reported Podman 6.1.4 still
requires qualification on its own runtime.

See [Docker build](https://docs.docker.com/reference/cli/docker/image/build/),
[Docker deprecations](https://docs.docker.com/engine/deprecated/) and
[Podman build](https://docs.podman.io/en/stable/markdown/podman-build.1.html).
