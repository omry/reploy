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

## 2. Create a Staging Deployment

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

## 3. Build and Test

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

## 4. Install Permanently

Permanent install is currently a Linux/systemd flow.

```bash
sudo reploy install --to /opt/example --service example
```

For side-by-side installs, use a different target directory, service name, and
port.

```bash
sudo reploy install --to /opt/example2 --service example2 --port 8082
```

## 5. Uninstall

```bash
sudo reploy uninstall --from /opt/example
```

If the target directory was deleted manually, uninstall by service name:

```bash
sudo reploy uninstall --service-name example
```
