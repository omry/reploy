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
reploy init arbiter-server
reploy init arbiter-server==0.9.3.dev1
reploy install arbiter-server
```

Shorthands are resolved through the Reploy blueprint index. The index is not
versioned; when an entry includes a version template, `name==VERSION`
substitutes that version into the resolved package ref.

## PyPI Package

```bash
reploy init pypi:example-app
reploy init pypi:example-app==1.2.3
reploy install pypi:example-app
```

By convention, `pypi:example-app` looks for a blueprint under:

```text
example_app/reploy
```

Use an explicit path when a package stores the blueprint elsewhere:

```bash
reploy init pypi:example-app#custom/path
reploy install pypi:example-app#custom/path
```

Use `#path/inside/package` for an explicit blueprint path inside a PyPI
package.

## Local File

```bash
reploy init file:./path/to/app.blueprint.yaml
reploy install file:./path/to/app.blueprint.yaml
```

Local file refs are useful while developing an app blueprint before publishing
it in a package or index.
