---
sidebar_position: 4
---

# Bundles

Bundles are the app artifacts that Reploy prepares for the deployment runtime.
For Python app providers, Reploy can build wheels from local package roots or
resolve packages from PyPI.

List available bundle options:

```bash
reploy bundle list-options
```

Reploy prepares the selected bundle automatically when a staging command needs
it, such as `reploy up`, `reploy app ...`, or `reploy bundle check`. Adding,
removing, cleaning, or updating bundle inputs marks the prepared bundle out of
date.

Install expects the staging bundle to already be current. If the bundle is out
of date, run `reploy bundle build`, retest staging, then install again.

Explicitly build the selected bundle when you want an early preflight:

```bash
reploy bundle build
```

Bundle build prepares and validates dependency artifacts, then warms the staging
Python runtime so the virtual environment is ready before the app starts.
App startup checks are still handled by runtime and install commands.
When a blueprint declares mounted managed files, warmup may create empty
placeholders for missing files so Docker can mount them.

For Docker deployments, the warmed Python runtime cache lives in a generated
Docker named volume by default. Reploy names that volume from the Docker
deployment identity, reuses it across staging commands, and removes it when
`reploy bundle clean` cleans the bundle. Operators can override
`REPLOY_RUNTIME_DIR` to a deployment-relative host path when they need a
bind-mounted runtime cache instead of the default named volume.

The runtime cache mount is configured in `.reploy/docker.env`:

```env
REPLOY_RUNTIME_DIR=<docker-identity>-runtime
```

`REPLOY_RUNTIME_DIR` is interpreted as a Docker named volume when it is a bare
volume name, such as `example-runtime`. It is interpreted as a host bind mount
when it is a path, such as `./.reploy/runtime`. Installed deployments require
the host-path form to stay under the install target. Reploy mounts the selected
storage at `/reploy-runtime` inside the container.

For named-volume runtime caches, Reploy materializes the generated Compose file
with an external volume declaration:

```yaml
volumes:
  example-runtime:
    name: example-runtime
    external: true
```

Reploy creates the volume before using it, prepares `/reploy-runtime` for the
configured container user, and removes the volume when `reploy bundle clean` or
installed-service uninstall cleans the generated runtime cache.

You can also warm the staging Python runtime directly:

```bash
reploy bundle warm-runtime
```

This builds the selected bundle first when needed, materializes runtime Compose,
and exits after the Python runtime is ready.

Check that the bundle can be prepared. This builds first when needed:

```bash
reploy bundle check
```

Use verbose output when diagnosing build or dependency resolver behavior:

```bash
reploy bundle check --verbose
reploy bundle build --verbose
reploy bundle warm-runtime --verbose
```

For deployments staged from PyPI package refs, `reploy stage --update` refreshes
the blueprint source according to the pinning rules recorded in the deployment
manifest.
