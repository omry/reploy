---
status: Active
updated: 2026-07-06
summary: Current maintainer workflow for local checks, release notes, and publishing.
---

# Maintaining Reploy

## Local Environment

Reploy's maintainer workflow uses Go, Python, Node, and Docker-backed smoke
tests. Match CI as closely as practical:

- Go 1.25.x
- Python 3.12
- Node 22
- Docker, for the full CLI integration path

Create the local Python environment from the repository root:

```bash
python3.12 -m venv .venv
. .venv/bin/activate
python -m pip install --upgrade pip
python -m pip install nox
```

After activation, `nox` is the local entrypoint for CI-equivalent checks:

```bash
nox -s ci
```

List the available sessions with:

```bash
nox -l
```

Useful targeted sessions:

```bash
nox -s go-test
nox -s cli-smoke
nox -s cli-integration
nox -s docker-interrupts
nox -s release-build-smoke
nox -s docs-build
```

For the full local CLI integration test, including Docker-backed bundle checks
and the live staging runtime lifecycle (`up`, `status`, `logs`, `test`, and
`down`), run:

```bash
nox -s cli-integration
```

This integration test is intentionally outside the default CI session. It runs
before publishing and can be triggered manually from the Integration workflow.
On Linux, it covers the real-Docker staging runtime path. On macOS, and on
Windows when run through `tools/e2e/smoke_windows.ps1`, it also covers the
Docker-managed persistent install path with generated control scripts.

For Docker interruption behavior, run the opt-in Compose probe:

```bash
nox -s docker-interrupts
```

The probe starts a unique Compose project, runs the same
`docker compose run --rm --no-deps --name ...` shape used by app commands,
sends an interrupt to the Docker Compose process group, measures how long
Compose takes to return, force-removes the named one-off container, inspects
leftover project containers and networks, and performs best-effort cleanup.
Pass `-- --include-raw-compose` to record the underlying Docker Compose
behavior without Reploy-style targeted cleanup, and pass `-- --include-up` to
also compare `docker compose up` behavior. Before release, keep the summary
lines with the release validation notes.

Observed Linux/WSL2 behavior on 2026-07-06 from `zsh`: raw
`docker compose run --rm --no-deps` returned quickly after `SIGINT` but left
the one-off container running; the Reploy-style named run removed the container
with targeted cleanup; `docker compose up` returned quickly but left an exited
service container until `compose down` cleanup.

Observed Windows Docker Desktop behavior on 2026-07-06 from Windows
PowerShell, running from the WSL UNC checkout with Python from
`C:\Users\omry\miniconda3\envs\reploy\python.exe`: the Reploy-style named run
returned after interrupt in 0.31 seconds with exit code 130, Docker Compose had
already removed the one-off container before targeted cleanup, and the final
summary reported `containers_before=0 containers_after=0 networks_before=1
networks_after=1`.

For a faster CLI smoke loop that skips the Docker-backed bundle build/check
but still runs preinstall and install dry-run checks, pass the smoke helper's
plan-only flag through nox:

```bash
nox -s cli-smoke -- --plan-only
```

For host CLI checks that may use Docker when it is available but should keep
going without it, use:

```bash
nox -s cli-smoke -- --docker-mode optional
```

For host CLI checks that must avoid executing Docker entirely, use:

```bash
nox -s cli-smoke -- --no-docker
```

## Changelog Fragments

Reploy uses [Changie](https://github.com/miniscruff/changie) for release-note
fragments. Install it with Go:

```bash
go install github.com/miniscruff/changie@v1.25.0
```

Add a fragment for user-facing changes:

```bash
changie new --kind Added --body "Added support for example behavior."
```

Use one of the configured kinds: `Added`, `Changed`, `Deprecated`, `Removed`,
`Fixed`, `Security`, or `Docs`.

Pure refactors, test-only changes, and internal cleanup do not need fragments
unless they affect the maintainer or release workflow.

Dev releases include the current unreleased fragments in GitHub Release notes
without consuming them. Final releases batch and merge the fragments into
`CHANGELOG.md`.
