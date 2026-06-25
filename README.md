# Reploy

Experimental deployment lifecycle tool. Docker is the first supported target.

Reploy creates and manages deployment directories from app-provided blueprints. A
blueprint declares the application provider, bundle options, runtime commands,
and Docker defaults while Reploy owns the generic deployment machinery.

Current scope:

- Docker deployment init/update/info/doctor
- deployment-local `reploy` helper
- blueprint shorthands through a JSON blueprint index
- Python-provider bundle roots, wheel builds, and runtime installation bundles
- app command execution inside the deployment runtime
- Docker lifecycle commands and native health probe

## Build

The release version lives in `VERSION`. The native binary embeds that value, and
Python wheel metadata reads the same file.

Build the current platform binary:

```bash
tools/build_reploy
```

The binary is written under `dist/GOOS-GOARCH/`. Build all release targets:

```bash
tools/build_reploy --all
```

## Python Package

Reploy is packaged as platform-specific Python wheels. The distribution contains
no Python wrapper; installing it places the native `reploy` executable on `PATH`
and also includes the binary at `reploy/bin/reploy` inside the wheel.

For local development:

```bash
python -m pip install -e packaging/python
reploy --version
```

Build the wheel from the repository root:

```bash
python -m build packaging/python --wheel
```

Build all release wheels, following the same native-client packaging shape as
`arbiter-client`:

```bash
tools/build_release_dists --clean
```

Use `--no-isolation` when rehearsing release builds in an environment that
already has the Python build dependencies installed and cannot reach PyPI.

The package build infers the host `GOOS-GOARCH` target and runs
`tools/build_reploy` if the matching binary is missing. Set `REPLOY_TARGET` to
build a specific target, such as `darwin-arm64` or `windows-amd64`. Set
`REPLOY_BINARY` to package an explicit prebuilt binary. Editable installs use a
small launcher that execs the binary in `dist`, so rebuilding Reploy updates the
installed `reploy` command without reinstalling the package.

## Test

```bash
go test ./...
```

When running in a sandbox where the default Go build cache is not writable, set
`GOCACHE` to a writable directory:

```bash
GOCACHE=/tmp/reploy-go-cache go test ./...
GOCACHE=/tmp/reploy-go-cache go build -buildvcs=false ./cmd/reploy
```

## Blueprints

Blueprint shorthands are resolved from a Reploy blueprint index. By default
Reploy downloads the index from this repository and caches it locally. Set
`REPLOY_BLUEPRINT_INDEX_URL` to point at another HTTP(S) or `file:` index while
developing or testing.

Validate and cache the index explicitly:

```bash
reploy blueprint-index refresh
```

The default index currently contains Arbiter blueprints:

```bash
reploy init --blueprint arbiter-server
reploy init --blueprint arbiter-suite
```

Use `arbiter-suite==VERSION` or `arbiter-server==VERSION` to pin a release. The
shorthands expand to wheel-hosted app blueprints. Without the index, use an
explicit PyPI package ref:

```bash
reploy init --blueprint pypi:arbiter-suite
```

PyPI package refs default to the `package_name/reploy` blueprint convention, so
`pypi:arbiter-suite` looks for `arbiter_suite/reploy` in the wheel. Use
`pypi:PACKAGE//PATH` only when a package stores its Reploy blueprint somewhere
else.

For unpublished or local app blueprints, use an explicit file reference:

```bash
reploy init --blueprint file:path/to/app/reploy
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

Use the app id as the filename, such as `arbiter.blueprint.yaml`. The blueprint
contains the provider identifier and bundle options directly, which keeps the
single-blueprint case shallow while making multi-blueprint packages obvious.

## Deployment Shape

The generated deployment directory includes:

- `reploy`, a deployment-local wrapper that prefers the vendored tool
- `.reploy/`, Reploy-managed files including Compose config, Docker env, the
  vendored binary, state, manifest, generated runtime requirements projection,
  and installation bundle
- app config and data directories declared by the blueprint

Useful commands:

```bash
reploy init --blueprint arbiter-server
reploy init --blueprint arbiter-suite
reploy update
reploy info
reploy doctor
reploy bundle list
reploy bundle list all
reploy bundle list-options
reploy bundle add --name imap,smtp
reploy bundle add-wheel ./dist/my_component-1.0.0-py3-none-any.whl
reploy bundle add-source ../my_component
reploy bundle remove imap,smtp
reploy bundle upgrade
reploy bundle build
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
```

The runtime, doctor, and install commands are still early migrations. Install
currently supports install-readiness checks, dry-run planning, guarded copy into
the target directory, installed-state marking, and systemd unit enable/restart.
