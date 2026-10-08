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

For the full local CLI integration test, including Docker-backed environment build checks
and the live staging runtime lifecycle (`up`, `status`, `logs`, `test`, and
`down`), run:

```bash
nox -s cli-integration
```

This integration test is intentionally outside the default CI session. It runs
before publishing and can be triggered manually from the Integration workflow.
On Linux, it covers the real-Docker staging runtime and Docker-managed
persistent-install path. On GitHub-hosted macOS runners, it covers the same
runtime and persistent-install path through Colima. On Windows, when run
through `tools/e2e/smoke_windows.ps1`, it covers the Docker-managed persistent
install path with generated control scripts.

For Windows release follow-up coverage, use the focused PowerShell wrapper:

```powershell
& '\\wsl$\ubuntu-24.04\home\omry\dev\reploy\tools\e2e\smoke_windows_followups.ps1'
```

By default it runs:

- `PathSpaces`: full runtime and Docker-managed persistent-install smoke from a
  normal Windows temp path whose directory name contains spaces.
- `PortConflict`: starts the smoke app on an intentionally occupied localhost
  port and expects `reploy up` to fail cleanly.

Run an individual case with `-Case`:

```powershell
& '\\wsl$\ubuntu-24.04\home\omry\dev\reploy\tools\e2e\smoke_windows_followups.ps1' -Case PathSpaces
& '\\wsl$\ubuntu-24.04\home\omry\dev\reploy\tools\e2e\smoke_windows_followups.ps1' -Case PortConflict
```

To check the Docker Desktop unavailable failure path, first stop Docker Desktop,
then run:

```powershell
& '\\wsl$\ubuntu-24.04\home\omry\dev\reploy\tools\e2e\smoke_windows_followups.ps1' -Case DockerUnavailable
```

The follow-up wrapper records host, architecture, Docker command, Docker server
metadata when available, the exact smoke case, and the per-case working
directory. It defaults to non-color evidence output so PowerShell transcripts
and `Tee-Object` logs do not expand progress spinner frames into repeated
lines. Pass `-Color` only for interactive debugging.

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

For host CLI checks that may use Docker when it is available but should keep
going without it, use:

```bash
nox -s cli-smoke -- --docker-mode optional
```

For host CLI checks that must avoid executing Docker entirely, use:

```bash
nox -s cli-smoke -- --no-docker
```

## Controlled-session Contract Proof

The default Go suite runs the Linux executable/PTY proof with a test-owned
terminal. It requires no recorder download. For the direct Docker terminal and
browser proof, acquire the two package URLs from
`testdata/controlled-session/controlled-session-contract-v1.json` and verify
their recorded SHA-256 digests before setting the fixture paths:

```bash
REPLOY_DOCKER_INTEGRATION=1 \
REPLOY_PLAYWRIGHT_FIXTURE=/path/to/playwright.tgz \
REPLOY_PLAYWRIGHT_CORE_FIXTURE=/path/to/playwright-core.tgz \
go test -v -count=1 -timeout 15m ./internal/dockerdeploy \
  -run '^TestReployControlledSessionContractDockerIntegration$'
```

Linux CI and the Linux AMD64 Integration job acquire and verify these fixtures
and run the proof. Missing fixture paths fail an enabled Docker proof. The
controller image pins Playwright/Chromium; util-linux supplies the PTY allocator.
The fixture proves Reploy's terminal, browser endpoint, lifecycle, retained
artifact, and owned cleanup contracts. Consumer recording formats, adapters,
redaction, and media rendering remain outside this proof.

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
