---
sidebar_position: 1
slug: /
---

# Reploy

Reploy is an experimental deployment lifecycle tool for apps that publish
portable deployment blueprints. It is meant to separate two jobs that are often
blurred together:

- **App Users** receive a blueprint ref and use it to create, configure,
  test, install, and uninstall a deployment.
- **App Authors** publish the blueprint, bundle metadata, runtime commands,
  health checks, and install hooks that make the app deployable.

The Reploy CLI is distributed as a statically linked native binary, so the
executable itself does not need separate runtime libraries.

Docker is the first supported runtime target. Python is the first supported app
bundle backend. Linux with systemd is the first supported permanent install
target.

## App User

Start with the app author's blueprint ref:

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh
reploy init --blueprint <app-blueprint-ref>
reploy bundle build
reploy up
reploy test
```

Then follow the app-specific commands exposed by the blueprint. Reploy owns the
deployment lifecycle; the app provider owns the app configuration experience.

## App Author

For publishing app blueprints, see the [App Author docs](/docs/author-deployments).

## Deployment Directory

`reploy init` creates a `reploy-staging/` deployment directory by default.
Generated config, bundle artifacts, Docker files, local state, and the
deployment-local `reploy` helper live there. Use `--dir` when you want a
different staging directory for an app instance.
