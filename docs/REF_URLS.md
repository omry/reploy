# Blueprint Ref URLs

This document describes the intended URL-shaped blueprint reference format.
Reploy is unreleased enough that ref syntax can break freely; this design can
replace older compatibility refs such as `pypi:PACKAGE#PATH`, `git:URL`, and
`file:PATH` instead of preserving them as first-class user-facing forms.

## Goals

- Make blueprint refs look like standard locators where possible.
- Avoid shell-hostile syntax such as `#`, which starts a comment in common
  shells unless quoted.
- Make explicit paths point to blueprint files, not blueprint directories.
- Avoid claiming arbitrary `https://` URLs before Reploy has a defined meaning
  for them.
- Keep room for future URL surfaces without compatibility traps.

## PyPI

PyPI refs use the `pypi://` scheme. The package name is the authority, and the
path must point to the blueprint file inside the package.

```text
pypi://example-app/example_app/reploy/example.blueprint.yaml
pypi://example-app/example_app/reploy/example.blueprint.yaml?version=latest
pypi://example-app/example_app/reploy/example.blueprint.yaml?version=1.2.3
```

Pathless direct PyPI refs are unsupported. If users should not have to type
the blueprint file path, publish a Reploy blueprint index entry and have them
use the indexed shorthand instead.

For PyPI refs, `version=latest` means resolve the current latest wheel from
PyPI. Blueprint index entries can use one ref template with `{version}`:

```json
{
  "ref": "pypi://example-app/example_app/reploy/example.blueprint.yaml?version={version}"
}
```

When a user runs `reploy stage example-app`, Reploy substitutes `latest`. When
they run `reploy stage example-app==1.2.3`, it substitutes `1.2.3`.

## Git

`https://` and `ssh://` refs should mean Git only when the host is a recognized
Git provider or otherwise exposes an unambiguous Git interface. This avoids
consuming generic HTTP URLs before Reploy has a broader URL model.

For Git refs, the clone URL remains the base URL. Reploy-specific selection is
carried in query parameters:

```text
https://github.com/org/repo.git?ref=main&path=path/to/app.blueprint.yaml
ssh://git@github.com/org/repo.git?ref=v1.2.3&path=path/to/app.blueprint.yaml
```

`ref` may be a branch, tag, or commit hash. `path` points to the blueprint file
inside the repository.

## Provider Schemes

Provider schemes are host-specific shorthands that normalize into Git clone
URLs plus `ref` and blueprint file `path` metadata. They should exist only for
providers with well-understood URL layouts.

### GitHub

GitHub refs use an explicit `github://` provider scheme. This avoids treating
ordinary GitHub web URLs as if they were Git clone URLs, and it keeps the
provider-specific path structure visible.

```text
github://omry/arbiter/server/src/arbiter_server/reploy/arbiter.blueprint.yaml?ref=main
github://omry/arbiter/server/src/arbiter_server/reploy/arbiter.blueprint.yaml?ref=main&transport=ssh
```

The `transport` query parameter selects the Git transport. It defaults to
`https`; use `transport=ssh` when the checkout should use SSH credentials.

The default HTTPS transport maps to the existing Git provider:

```text
git:https://github.com/omry/arbiter.git#server/src/arbiter_server/reploy/arbiter.blueprint.yaml?ref=main
```

The SSH transport maps to:

```text
git:ssh://git@github.com/omry/arbiter.git#server/src/arbiter_server/reploy/arbiter.blueprint.yaml?ref=main
```

The `github://` path is required and points to the blueprint file. Reploy loads
that exact file from the checked-out repository.

Reploy may also accept the browser URL users copy from GitHub as input sugar:

```text
https://github.com/omry/arbiter/blob/main/server/src/arbiter_server/reploy/arbiter.blueprint.yaml
```

That normalizes to:

```text
https://github.com/omry/arbiter.git?ref=main&path=server/src/arbiter_server/reploy/arbiter.blueprint.yaml
```

The GitHub provider syntax is host-specific. Reploy should not infer this path
structure for arbitrary `https://` hosts.

## Filesystem

Filesystem refs can be `file://` URLs, absolute paths, or relative paths that
start with `.`. Explicit filesystem paths should point to a blueprint file.

```text
file:///abs/path/to/app.blueprint.yaml
/abs/path/to/app.blueprint.yaml
./relative/path/to/app.blueprint.yaml
```

Directory refs may remain useful as compatibility or development shortcuts, but
the explicit URL design should prefer blueprint file paths.

## Legacy Forms

Older development refs may be removed or treated only as temporary migration
aliases:

```text
pypi:example-app#example_app/reploy/example.blueprint.yaml
git:https://github.com/org/repo.git?ref=main
file:./path/to/app.blueprint.yaml
```

The URL forms above are the intended canonical surface. State files, cache keys,
and install reports should use the canonical URL shape rather than preserving
legacy spellings.
