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

Build the selected bundle:

```bash
reploy bundle build
```

Check that the bundle can be prepared:

```bash
reploy bundle check
```

Use verbose output when diagnosing build or dependency resolver behavior:

```bash
reploy bundle check --verbose
reploy bundle build --verbose
```

For deployments staged from PyPI package refs, `reploy update` refreshes
the blueprint source according to the pinning rules recorded in the deployment
manifest.
