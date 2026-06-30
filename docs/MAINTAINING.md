# Maintaining Reploy

## Local Environment

Reploy's maintainer workflow uses Go, Python, Node, and Docker-backed smoke
tests. Match CI as closely as practical:

- Go 1.25.x
- Python 3.12
- Node 22
- Docker, for the full CLI smoke path

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
nox -s release-build-smoke
nox -s docs-build
```

For a faster CLI smoke loop that skips the Docker-backed bundle build, pass the
smoke helper's plan-only flag through nox:

```bash
nox -s cli-smoke -- --plan-only
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
