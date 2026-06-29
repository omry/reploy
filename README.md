# Reploy

Experimental deployment lifecycle tool. Docker is the first supported target.

Reploy creates and manages deployment directories from app-provided blueprints. A
blueprint declares the application provider, bundle options, runtime commands,
and Docker defaults while Reploy owns the generic deployment machinery.

Current scope:

- Docker deployment stage/update/info/doctor
- staging-local Reploy workflow with an app-named control script
- blueprint shorthands through a JSON blueprint index
- Python-provider bundle roots, wheel builds, and runtime installation bundles
- app command execution inside the staging runtime
- Docker lifecycle commands and native health probe
- direct install from blueprint defaults and staged install/update into a
  deployed host service

## Install

Install the latest release binary from GitHub:

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh
```

By default the installer script installs `reploy` to
`$HOME/.local/bin/reploy` and prints the download URL, target path, installed
version, and a PATH hint when needed.

## Build

The release version lives in `VERSION`. The native binary embeds that value, and
Python wheel metadata reads the same file.

Build the current platform binary:

```bash
tools/build_reploy
```

The binary is written under `dist/GOOS-GOARCH/`. Build all active release
targets:

```bash
tools/build_reploy --all
```

## Python Package

Reploy is packaged as platform-specific Python wheels. Active release wheels are
Linux-only for now. The distribution contains no Python wrapper; installing it
places the native `reploy` executable on `PATH` and also includes the binary at
`reploy/bin/reploy` inside the wheel.

For local development:

```bash
python -m pip install -e packaging/python
reploy --version
```

Build the wheel from the repository root:

```bash
python -m build packaging/python --wheel
```

Build all release wheels, following the same native-client packaging shape:

```bash
tools/build_release_dists --clean
```

Use `--no-isolation` when rehearsing release builds in an environment that
already has the Python build dependencies installed and cannot reach PyPI.

The package build infers the host `GOOS-GOARCH` target and runs
`tools/build_reploy` if the matching binary is missing. Set `REPLOY_TARGET` to
build a specific active target, such as `linux-amd64` or `linux-arm64`. Set
`REPLOY_BINARY` to package an explicit prebuilt binary. Editable installs use a
small launcher that execs the binary in `dist`, so rebuilding Reploy updates the
installed `reploy` command without reinstalling the package. Formal macOS and
Windows support is tracked in the backlog.

## Publish

Publishing is manual and must be run from `main` after CI is green. The workflow
publishes Linux wheels to PyPI, then creates a GitHub Release containing only
Linux binary assets:

```bash
gh workflow run publish.yml --ref main
```

The PyPI project must be configured for GitHub trusted publishing for
`omry/reploy` and `.github/workflows/publish.yml`; leave the PyPI trusted
publisher environment field blank.

## Test

Run the same check suite used by CI:

```bash
nox -s ci
```

See [`docs/MAINTAINING.md`](docs/MAINTAINING.md) for local maintainer
environment setup.

```bash
go test ./...
```

Exercise the CLI against the fixture blueprint and packages under
`tests/e2e/python/packages/`. This path uses Docker to build and check the
Python bundle:

```bash
tools/build_reploy --target linux-amd64 --outdir /tmp/reploy-smoke-bin
python tools/e2e_smoke --reploy /tmp/reploy-smoke-bin/linux-amd64/reploy
```

For a fast planning-only loop that skips Docker-backed bundle builds:

```bash
python tools/e2e_smoke --plan-only --reploy /tmp/reploy-smoke-bin/linux-amd64/reploy
```

When running in a sandbox where the default Go build cache is not writable, set
`GOCACHE` to a writable directory:

```bash
GOCACHE=/tmp/reploy-go-cache go test ./...
GOCACHE=/tmp/reploy-go-cache go build -buildvcs=false ./cmd/reploy
```

## Blueprints

Blueprint shorthands are resolved from a Reploy blueprint index. The default
index is served from this repository and publishes known app blueprint
shorthands such as `arbiter-server`. App providers can also publish their own
indexes, and users can point Reploy at those indexes. Set
`REPLOY_BLUEPRINT_INDEX_URL` to point at another HTTP(S) or `file:` index while
developing or testing.

Validate and cache the index explicitly:

```bash
reploy index update
reploy index search arbiter
reploy index show arbiter-server
```

Shorthands expand to wheel-hosted app blueprints. The index entry is a single
ref template. When it contains `{version}`, `name==VERSION` substitutes that
version, while unpinned `name` substitutes `latest`:

```bash
reploy stage arbiter-server
reploy install arbiter-server --dry-run
```

Without an index, use an explicit Git, source, file, or PyPI ref.

Git refs clone a source checkout with Reploy's built-in Git client, so the
machine running Reploy does not need a `git` executable. `?ref=` accepts a
branch, tag, or commit hash. Reploy resolves moving refs such as `main` to a
commit hash in staging state.

```bash
reploy stage git:https://github.com/org/example-app.git?ref=main
reploy stage git:https://github.com/org/example-app.git#example_app/reploy?ref=v0.1.0
```

Direct PyPI refs must include the exact blueprint file path inside the wheel.
Use the blueprint index for user-facing shortcuts:

```bash
reploy stage pypi://example-app/example_app/reploy/example.blueprint.yaml
reploy stage pypi://example-app/example_app/reploy/example.blueprint.yaml?version=1.2.3
```

For local Python source checkouts, use a source ref. `source:path/to/app` reads
`pyproject.toml`, applies the same `package_name/reploy` blueprint convention,
and builds the provider package from the checkout instead of PyPI. Use
`source:PATH#PATH` only when the blueprint lives somewhere else in the checkout.

```bash
reploy stage source:path/to/app
reploy install source:path/to/app --dry-run
```

For unpublished or local app blueprints, use an explicit file reference:

```bash
reploy stage file:path/to/app/reploy
reploy install file:path/to/app/reploy --dry-run
```

### Blueprint Layout Convention

The common single-blueprint package-data convention is:

```text
package_name/reploy/
  app_name.blueprint.yaml
```

A package that deliberately ships more than one deployment blueprint should add
one named blueprint file per app:

```text
package_name/reploy/
  inbound.blueprint.yaml
  outbound.blueprint.yaml
```

Use the app id as the filename, such as `example.blueprint.yaml`. The blueprint
contains the provider identifier and bundle options directly, which keeps the
single-blueprint case shallow while making multi-blueprint packages obvious.

## Staging and Install Flow

Staging is the full Reploy workspace. Use it when an app needs bundle
selection, generated configuration review, app commands, or pre-install
testing before touching the installed service. Reploy also writes an app-named
control script into the staging directory, such as `arbiterctl`, so operators
can learn the app-local entrypoint before install. The script uses the staging
Docker Compose files directly for runtime and app-control commands; Reploy
still owns staging management such as bundle changes, updates, doctor checks,
and install.

Useful staging commands:

```bash
reploy stage arbiter-server
reploy stage git:https://github.com/org/example-app.git?ref=main
reploy stage source:path/to/app
reploy stage file:path/to/app/reploy
./reploy-staging/examplectl status
./reploy-staging/examplectl config check --live
reploy stage --update
reploy info
reploy doctor
reploy bundle list
reploy bundle list all
reploy bundle list-options
reploy bundle add imap,smtp
reploy bundle remove imap,smtp
reploy bundle upgrade
reploy bundle build   # explicit preflight before install; up builds as needed
reploy bundle check
reploy app config check
reploy app config check --live
reploy up
reploy restart
reploy down
reploy ps
reploy logs
reploy logs --follow
reploy test
reploy install --to /srv/my-app --dry-run
reploy install --to /srv/my-app --replace config --dry-run
reploy install --to /srv/my-app --clean --dry-run
```

Direct install skips the persistent staging workspace and installs from
blueprint defaults. It is useful for simple services and dry-run planning:

```bash
reploy install arbiter-server --dry-run
reploy install source:path/to/app --dry-run
reploy install file:./app.blueprint.yaml --dry-run
reploy install pypi://example-app/example_app/reploy/example.blueprint.yaml --dry-run
```

By default, direct install uses a temporary internal staging-like workspace.
`--in-place` installs directly into the destination to conserve peak disk
space; it is an escape hatch, not the normal path.

Install/update preserves app-owned artifacts declared by the blueprint unless
the operator asks to replace them. `.reploy/` is Reploy-owned generated state
and may be replaced during install/update.

The installed deployment exposes a generated app control script under the
target directory, such as `/opt/arbiter/arbiterctl`, not a full deployed
`reploy` CLI. The control script is for local service operations:

```bash
/opt/arbiter/arbiterctl up
/opt/arbiter/arbiterctl down
/opt/arbiter/arbiterctl restart
/opt/arbiter/arbiterctl status
/opt/arbiter/arbiterctl logs
/opt/arbiter/arbiterctl enable
/opt/arbiter/arbiterctl disable
/opt/arbiter/arbiterctl health
```

Uninstall remains a Reploy host operation:

```bash
reploy uninstall --list-services
reploy uninstall --from /srv/my-app2 --dry-run
reploy uninstall --service-name my-app2 --dry-run
```

The runtime, doctor, install, and uninstall commands are still early migrations.
Install currently supports install-readiness checks, dry-run planning, guarded
copy into the target directory, installed-state marking, and systemd unit
enable/restart.
Install derives collision-resistant Docker identity from the service name and
install target path. Apps with multiple public ports should expose named
blueprint ports; install accepts repeated `--port NAME=HOST_PORT` overrides,
while single-port apps may use `--port HOST_PORT`.
Uninstall uses `--from DIR` to read installed state, stop the systemd service,
remove Docker Compose resources, disable and remove the unit, and reload
systemd. If the target directory was manually deleted, use
`--service-name NAME`; Reploy recovers the Compose project from the systemd unit
when possible and removes Docker containers and networks by Compose labels.
Use `--list-services` to list Reploy-managed systemd services before choosing a
service-only uninstall. The installed target directory is kept unless
`--remove-dir` is set.
When installing from a file-, source-, or git-backed blueprint with local
source packages, staged install rebuilds those wheels in the copied target
deployment before starting the service, so editable checkout changes are
captured without mutating the staging deployment.
Blueprints may declare install lifecycle hooks under
`docker.install.hooks.before_start` and `docker.install.hooks.after_start`.
Hooks support app commands, such as `app: [config, check]`, and health checks,
such as `health_check: {wait: true}`.
Blueprints may also declare post-install success hints under
`docker.install.success`. Success variables can capture app command output, and
success lines can expand those variables after install hooks complete. Use
`server_url: true` for a variable that should expand to the installed service's
externally mapped base URL.

Permanent installs require a non-root install owner. Blueprints declare the
default under `install.owner.user` and `install.owner.group`, plus
`install.owner.on_missing` to choose whether Reploy creates a missing system
owner or fails. Operators can override these with `REPLOY_INSTALL_OWNER` and
`REPLOY_INSTALL_OWNER_ON_MISSING` in `.reploy/docker.env`. Owner values may be
numeric `UID:GID` or host names such as `arbiter:arbiter`; install resolves the
owner, rejects root, creates named system owners when requested, owns the
installed deployment tree with it, and writes the installed container user as
the resolved numeric UID:GID. Owners created by Reploy must use conservative
Linux system account names such as `arbiter` or `arbiter_server`.
