---
sidebar_position: 3
---

# Blueprints

A blueprint is app-owned deployment metadata. It tells Reploy where app bundles
come from, which options users can choose, what app commands exist, and how the
Docker deployment should be shaped.

Blueprints can be referenced in three common ways.

## Indexed Shorthand

```bash
reploy stage omegaconf-inspector-demo
reploy stage arbiter-server
reploy stage arbiter-server==0.9.3.dev1
reploy install arbiter-server --scope <user|system>
```

Shorthands are resolved through the Reploy blueprint index. The index entry is
a single blueprint ref. When the user writes `name==VERSION`, Reploy appends
the scheme-appropriate pin: `version=VERSION` for PyPI refs and `ref=VERSION`
for Git/GitHub refs. Unpinned names use the provider default, such as the latest
PyPI release or the Git repository's default branch.
`omegaconf-inspector-demo` is the neutral Reploy demo app and resolves to the
example blueprint in this repository.

## PyPI Package

```bash
reploy stage pypi://example-app/example_app/reploy/example.blueprint.yaml
reploy stage pypi://example-app/example_app/reploy/example.blueprint.yaml?version=1.2.3
reploy install pypi://example-app/example_app/reploy/example.blueprint.yaml --scope <user|system>
```

Direct PyPI refs include the exact blueprint file path inside the package. Use
the Reploy blueprint index when users should be able to type a short app name
instead of the full package path.

## Local File

```bash
reploy stage file:./path/to/app.blueprint.yaml
reploy install file:./path/to/app.blueprint.yaml --scope <user|system>
```

Local file refs are useful while developing an app blueprint before publishing
it in a package or index.
