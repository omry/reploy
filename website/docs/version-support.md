---
sidebar_position: 9
---

# Version Support

Use `blueprint.requires_reploy` to declare the oldest Reploy version that
understands every field and behavior the blueprint depends on.

The schema-1 environment model documented here is the current public blueprint
surface and requires Reploy `>=0.6.0.dev1` during its development cycle:

```yaml
blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.6.0.dev1"
```

This release makes a hard cut from the earlier prototype blueprint shape. It
does not accept the old top-level `app`, `bundle`, and `install` nodes or their
aliases. Use `environment` and `docker` as described in
[Blueprint Structure](/docs/blueprint-structure).

`requires_reploy` is independent of the Linux distribution in the base image.
APT support is determined from the selected image at build time: the image must
provide a compatible Debian-family APT/dpkg toolchain. The implementation does
not hard-code Debian or Ubuntu release numbers, so future or older releases can
work when their schema and required capabilities are compatible.

Reploy validates the declared constraint when loading the blueprint. Increase
the lower bound when adopting a field or behavior introduced by a newer Reploy
release. Do not increase it merely because a newer Debian, Ubuntu, Python, or
Docker version is used.

See [Support](/docs/support-matrix) for the current host and release-target
matrix.
