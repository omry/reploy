---
sidebar_position: 4
---

# Bundle Selection and Builds

A staged deployment records desired component packages and options. Provider
artifacts live in that deployment's `.reploy/provider-store/`; they are not
shared through a hidden machine-wide Reploy store. Docker owns the resulting
image and layer storage. The deployment state records the exact Docker image
reference it currently uses.

Inspect the available component-qualified options and the current request:

```bash
reploy bundle options
reploy bundle list
```

Select or remove an option by its qualified name:

```bash
reploy bundle add application/imap
reploy bundle remove application/imap
```

Add or remove a direct package request from a specific component:

```bash
reploy bundle add-package application 'rich>=13'
reploy bundle remove-package application 'rich>=13'
reploy bundle add-package system jq
```

These commands update desired state only. They do not resolve packages or build
an image. To build and fully validate the selected environment before running
anything, use:

```bash
reploy build
```

The resulting image is always fully validated. `--validate-layers` additionally
runs the same full validation after every newly created component layer.
`--no-cache` reruns resolvers and image construction instead of reusing the
current verified build.

`reploy up`, `reploy restart`, and staged app commands automatically reuse a
current successful build or run the same build pipeline when it is missing or
stale. `reploy shell` and `reploy test` require a current build. Staging and
direct installation also ensure a current build before publishing the
installation.

Remove deployment-local provider artifacts when they are no longer needed:

```bash
reploy bundle clean
```

Cleaning does not change the blueprint, request overlay, current build record,
or Docker image. The current image remains runnable. A later build may need to
download or reconstruct provider artifacts that were removed.

For deployments staged from remote refs, `reploy stage --update` refreshes the
blueprint source according to its recorded reference. Package selection remains
explicit in the deployment overlay.
