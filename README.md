# Reploy

[![CI](https://github.com/omry/reploy/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/omry/reploy/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/omry/reploy)](go.mod)
[![Release](https://img.shields.io/github/v/release/omry/reploy?include_prereleases)](https://github.com/omry/reploy/releases)
[![Docs](https://img.shields.io/badge/docs-reploy.cli.dev-blue)](https://reploy.cli.dev/)
[![ecosystem: cli.dev](https://cli.dev/img/badges/cli-dev-ecosystem.svg)](https://cli.dev)
[![License](https://img.shields.io/github/license/omry/reploy)](LICENSE)

Reploy is an experimental deployment lifecycle tool for services.

Reploy turns an environment blueprint into a local staging workspace, lets you
configure and test it, then installs its optional workload. Docker is the first
supported runtime.

| Host OS | Docker | Staging | User install | System install |
| --- | --- | --- | --- | --- |
| Linux | Docker Engine | ✅ | ✅ | ✅ |
| macOS | Docker-compatible | ✅ | ✅ | — |
| Windows | Docker Desktop | ✅ | ✅ | — |

Generated builds select their builder from the chosen local engine. Docker uses
BuildKit. For experiments with a local Linux Podman Docker-compatible socket,
Reploy selects the compatible build API and checks local-image access, offline
RUN, read-only mounts and result loading before generated builds. Users do not
need to set `DOCKER_BUILDKIT`.

When Podman's compatible inspection omits healthcheck metadata, Reploy reads
its image export through the same local endpoint and verifies the actual config
against the immutable image ID. Absent metadata is never treated as disabled.
Verified metadata is reused only within the current invocation.

This fallback streams the full image archive without extracting it. Client
limits are 64 GiB, five minutes, 4 MiB for config JSON, 1 MiB for the manifest,
and 10,000 archive entries. Podman first creates a full server-side temporary
archive; client limits do not bound that disk cost. Large images can therefore
make the first metadata check expensive. Docker uses normal inspection.

For Podman, Reploy prepares newly created containers through the same local
engine's native update API before initialization/start, so disabled image
healthchecks cannot create timers. Image metadata and validation stay intact.
Compose startup prepares each new container, including replacements. An old
container with a disabled check and a positive timer interval is removed before
recreation, while Podman can still clean up its existing timer. No global timer
setting or separately installed Podman CLI is required.

This is a temporary compatibility path for disabled-check scheduling. Full
Podman compatibility remains unqualified.

Application identity follows the selected host account's primary UID/GID by
default. On local rootless Podman, Reploy uses `keep-id` internally so managed
config/data and explicit application outputs remain accessible to that account.
The invoking account, engine owner, and application account must match; Reploy
rejects a mismatch instead of changing file ownership. No Podman-specific flag
or environment variable is required. Rootful engine behavior and internal
provider build identities are unchanged. Explicit isolated service mappings
remain separate multi-identity work.

Same-ID support covers transient commands, controlled sessions without granted
endpoints, and Compose recreation. Network-enabled Podman controlled sessions
remain unsupported. Reploy retains its network identity and fixed-address
checks before startup; this storage fix does not bypass them.

This path targets the Podman 6.1.4 API and is tested on the development engine
with the inline seccomp support required above; it does not establish stock
6.1.4 compatibility. `keep-id` preserves primary IDs, but container supplementary
groups still map to subordinate host groups. Host files requiring supplementary
group access can remain inaccessible. Reploy does not import additional host
group authority or repair existing subordinate-owned data automatically.

Linux system service units retain the local engine endpoint selected during
installation for both start and stop. Startup allows 90 seconds in total for the
deployment lock, initial setup, and engine readiness. Time spent waiting for the
lock leaves less time for the engine. It then allows 60 seconds for the complete
application startup sequence,
including validation, custom `before_start` and `after_start` events, private
environment delivery, and readiness checks. These are separate time limits.
Timeout errors report lock/setup, engine wait, startup time, and individual
startup events by command name, without command arguments or private values.

The generated systemd unit has a 180-second outer startup limit. This covers the
two limits plus the existing 30-second failed-start cleanup allowance; cleanup
does not extend the application's startup budget. Restart and stop timing are
unchanged. Reinstall existing system services to regenerate their units.

If you select a custom engine endpoint, configure its engine to start at boot.
Reploy does not infer a host service from an arbitrary socket path. This startup
handling does not establish Docker Desktop on WSL or full Podman compatibility.

## Install

Install the latest release binary:

```bash
curl -fsSL https://reploy.cli.dev/install.sh | sh
```

On Windows, use the PowerShell installer:

```powershell
irm https://reploy.cli.dev/install.ps1 | iex
```

From `cmd.exe`, invoke PowerShell explicitly:

```batch
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://reploy.cli.dev/install.ps1 | iex"
```

See the [installation docs](https://reploy.cli.dev/docs/install-reploy) for
script options and other installation methods.

## Quickstart

Try Reploy with the included OmegaConf Inspector demo:

OmegaConf Inspector is a small browser app for merging YAML config layers and
inspecting the OmegaConf result. It is useful as a Reploy demo because it has
real Python dependencies, service config, a browser UI, health checks, logs,
control commands, and writable project data, but stays neutral enough to try
without learning a domain-specific app first.

```bash
APP_REF=omegaconf-inspector-demo

# Create a self-contained staging workspace in ./reploy-staging.
reploy stage "$APP_REF"

# Resolve packages, build the image, and validate it.
reploy build

# Configure and operate the app through its staging-owned command.
./reploy-staging/appctl config init
./reploy-staging/appctl config check

# Start and test the staged service.
./reploy-staging/appctl up
reploy test

# Install the tested staging workspace for the current user.
reploy install --scope user --to "$PWD/omegaconf-inspector-installed"

# Tail logs through the installed app control script.
./omegaconf-inspector-installed/appctl logs --tail=100
```

Read the [app installation guide](https://reploy.cli.dev/docs/install-an-app)
for the full staging and install workflow.

## Documentation

Read the [Reploy documentation](https://reploy.cli.dev/docs/intro) for the
full guide.

Maintainer setup is documented in [`docs/MAINTAINING.md`](docs/MAINTAINING.md).
Design notes live in [`docs/`](docs/).
The public controlled-session host and controller boundaries are documented in
[`website/docs/controlled-sessions.md`](website/docs/controlled-sessions.md).
