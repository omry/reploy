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

Bundle build also warms the generated Python runtime cache in the target
runtime image. That moves Python virtualenv setup and bundle installation into
the build step so the first app command does not pay that cost. Use
`--no-warm-runtime` when you only want to build and validate the bundle
artifacts and leave runtime setup to the next app command.

Check that the bundle can be prepared. This builds first when needed:

```bash
reploy bundle check
```

Use verbose output when diagnosing build or dependency resolver behavior:

```bash
reploy bundle check --verbose
reploy bundle build --verbose
```

For deployments staged from PyPI package refs, `reploy stage --update` refreshes
the blueprint source according to the pinning rules recorded in the deployment
manifest.
