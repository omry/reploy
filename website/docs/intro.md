---
sidebar_position: 1
slug: /
---

# Reploy

Reploy is an experimental deployment lifecycle tool for apps that publish
portable deployment blueprints. It is meant to separate two jobs that are often
blurred together:

- **App Users** receive a blueprint ref and use it to create, configure,
  test, install, update, and uninstall a deployment.
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
reploy stage <app-blueprint-ref>
reploy up
reploy test
```

Then follow the app-specific commands exposed by the blueprint. Reploy owns the
deployment lifecycle; the app provider owns the app configuration experience.

Simple services can also be installed directly from blueprint defaults:

```bash
sudo reploy install <app-blueprint-ref>
```

Direct install skips the persistent staging directory. Use staging when you
need to select bundle options, run app configuration commands, or test before
installing.

## App Author

For publishing app blueprints, see the [App Author docs](/docs/author-deployments).

## Deployment Directory

`reploy stage` creates a `reploy-staging/` deployment directory by default.
Generated config, bundle artifacts, Docker files, local state, and staging
helpers live there. Staging also writes an app-named control script, such as
`arbiterctl`, for local runtime and app commands. Use `--dir` when you want a
different staging directory for an app instance.

The installed deployment is narrower. It contains generated service wiring and
an app control script such as `arbiterctl`, not the full Reploy CLI.
