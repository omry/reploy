---
sidebar_position: 1
---

# App User

This page is for the person installing and managing an app with Reploy. You
should not need to understand the app author's build system or deployment
internals.

You need one thing from the app provider:

```text
<app-blueprint-ref>
```

That ref may be an indexed shorthand, a PyPI package ref, or a local file while
the app is still being developed.

## 1. Install Reploy

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh
```

The installer places `reploy` in `$HOME/.local/bin/reploy` by default and
prints a PATH hint when needed.

## 2. Choose Direct or Staged Install

For simple services that work from blueprint defaults, install directly:

```bash
sudo reploy install <app-blueprint-ref>
```

Direct install does not ask install-time configuration questions yet. If the
app needs bundle selection, configuration commands, or pre-install testing, use
staging.

## 3. Create a Staging Deployment

`reploy init` creates `reploy-staging/` by default and writes the deployment
files there.

```bash
reploy init --blueprint <app-blueprint-ref>
```

From this point on, commands run against `reploy-staging/` by default:

```bash
reploy info
```

Use `--dir` when you want a different staging directory for this app instance.

## 4. Build and Test Staging

```bash
reploy bundle build
reploy up
reploy test
```

If the app exposes configuration commands, run those through `reploy app`. The
exact commands are app-specific.

```bash
reploy app
```

## 5. Install or Update Permanently

Permanent install is currently a Linux/systemd flow.

```bash
sudo reploy install --to /opt/example --service example
```

Installing over an existing deployment updates it from the current staging
state. App-owned artifacts declared by the blueprint are preserved by default.
Use named replacement only when you intentionally want a fresh copy of an
artifact:

```bash
sudo reploy install --to /opt/example --replace config
sudo reploy install --to /opt/example --clean
```

For side-by-side installs, use a different target directory, service name, and
port.

```bash
sudo reploy install --to /opt/example2 --service example2 --port 8082
```

After install, operate the service through the generated app control script
inside the target directory, such as `/opt/example/examplectl`:

```bash
/opt/example/examplectl status
/opt/example/examplectl logs
/opt/example/examplectl restart
```

## 6. Uninstall

```bash
sudo reploy uninstall --from /opt/example
```

If the target directory was deleted manually, uninstall by service name:

```bash
sudo reploy uninstall --service-name example
```
