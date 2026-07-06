# Reploy

[![CI](https://github.com/omry/reploy/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/omry/reploy/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/omry/reploy)](https://goreportcard.com/report/github.com/omry/reploy)
[![Go version](https://img.shields.io/github/go-mod/go-version/omry/reploy)](go.mod)
[![Release](https://img.shields.io/github/v/release/omry/reploy?include_prereleases)](https://github.com/omry/reploy/releases)
[![Docs](https://img.shields.io/badge/docs-reploy.yadan.net-blue)](https://reploy.yadan.net/)
[![License](https://img.shields.io/github/license/omry/reploy)](LICENSE)

Reploy is an experimental deployment lifecycle tool for services.

It creates a self-contained staging workspace from an app-provided blueprint,
lets you configure and test the app there, and can install the result as a
deployed host service. Docker is the first supported deployment runtime.
Linux is the production permanent-install host with systemd; macOS and Windows
support Docker Desktop-backed staging and Docker-managed permanent installs.

## Install

Install the latest release binary:

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh
```

On Windows, use the PowerShell installer:

```powershell
irm https://reploy.yadan.net/install.ps1 | iex
```

From `cmd.exe`, invoke PowerShell explicitly:

```batch
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://reploy.yadan.net/install.ps1 | iex"
```

See the [installation docs](https://reploy.yadan.net/docs/install-reploy) for
script options and other installation methods.

## Quickstart

Start with a blueprint ref from the app author:

```bash
APP_REF=arbiter-server

# Create a self-contained staging workspace in ./reploy-staging.
reploy stage "$APP_REF"

# Configure the staged app before starting it.
vim reploy-staging/conf/

# Run an app-specific config check exposed by the blueprint.
reploy app config check

# Start and test the staged service.
reploy up
reploy test

# Install the tested staging workspace.
sudo "$(command -v reploy)" install --scope system --to /srv/arbiter

# Tail logs through the installed app control script.
/srv/arbiter/arbiterctl logs --tail=100
```

Read the [app installation guide](https://reploy.yadan.net/docs/install-an-app)
for the full staging and install workflow.

## Documentation

Read the [Reploy documentation](https://reploy.yadan.net/docs/intro) for the
full guide.

Maintainer setup is documented in [`docs/MAINTAINING.md`](docs/MAINTAINING.md).
Design notes live in [`docs/`](docs/).
