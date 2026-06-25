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

Build the current platform binary:

```bash
go build -o dist/$(go env GOOS)-$(go env GOARCH)/reploy ./cmd/reploy
```

Build all release targets by setting `GOOS` and `GOARCH` for each target:

```bash
GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/reploy ./cmd/reploy
GOOS=linux GOARCH=arm64 go build -o dist/linux-arm64/reploy ./cmd/reploy
GOOS=darwin GOARCH=amd64 go build -o dist/darwin-amd64/reploy ./cmd/reploy
GOOS=darwin GOARCH=arm64 go build -o dist/darwin-arm64/reploy ./cmd/reploy
GOOS=windows GOARCH=amd64 go build -o dist/windows-amd64/reploy.exe ./cmd/reploy
GOOS=windows GOARCH=arm64 go build -o dist/windows-arm64/reploy.exe ./cmd/reploy
```

## Python Package

Reploy is packaged as platform-specific Python wheels. The distribution contains
no Python wrapper; installing it places the native `reploy` executable on `PATH`
and also includes the binary at `reploy/bin/reploy` inside the wheel.

For local development:

```bash
python -m pip install -e .
reploy --version
```

The package build infers the host `GOOS-GOARCH` target and runs `go build` if the
matching binary is missing. Set `REPLOY_TARGET` to build a specific target, such
as `darwin-arm64` or `windows-amd64`. Set `REPLOY_BINARY` to package an explicit
prebuilt binary. Editable installs use a small launcher that execs the binary in
`dist`, so rebuilding Reploy updates the installed `reploy` command without
reinstalling the package.

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
shorthands expand to wheel-hosted app blueprints, for example:

```bash
reploy init --blueprint pypi:arbiter-suite//arbiter_suite/reploy
```

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
