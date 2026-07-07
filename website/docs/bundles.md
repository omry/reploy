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
`reploy bundle clean` cleans the bundle.

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
