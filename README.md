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
