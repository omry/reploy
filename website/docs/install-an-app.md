---
sidebar_position: 1
---

import PlatformTabs from '@site/src/components/PlatformTabs';
import TabItem from '@theme/TabItem';

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

<PlatformTabs>
  <TabItem value="linux">

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh
```

The installer places `reploy` in `$HOME/.local/bin/reploy` by default and
prints a PATH hint when needed.

  </TabItem>
  <TabItem value="windows">

```powershell
irm https://reploy.yadan.net/install.ps1 | iex
```

From `cmd.exe`, invoke PowerShell explicitly:

```batch
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://reploy.yadan.net/install.ps1 | iex"
```

The installer places `reploy.exe` in
`%LOCALAPPDATA%\Programs\Reploy\bin\reploy.exe` by default and prints a PATH
hint when needed.

  </TabItem>
  <TabItem value="macos">

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh
```

The installer places `reploy` in `$HOME/.local/bin/reploy` by default and
prints a PATH hint when needed.

  </TabItem>
</PlatformTabs>

## 2. Choose Direct or Staged Install

For simple services that work from blueprint defaults, install directly:

```bash
sudo reploy install <app-blueprint-ref>
```

On macOS and Windows, omit `sudo`. Reploy uses a host-appropriate per-user
default install directory unless the blueprint or `--to` provides another
target.

Direct install does not ask install-time configuration questions yet. If the
app needs bundle selection, configuration commands, or pre-install testing, use
staging.

## 3. Create a Staging Deployment

`reploy stage` creates `reploy-staging/` by default and writes the deployment
files there.

```bash
reploy stage <app-blueprint-ref>
```

From this point on, commands run against `reploy-staging/` by default:

```bash
reploy info
```

The staging directory also contains an app-named control script, such as
`examplectl`, for local runtime and app commands:

```bash
./reploy-staging/examplectl status
./reploy-staging/examplectl config check --live
```

For app-specific commands, the control script delegates to the Reploy runtime
embedded in the deployment. This keeps the control script small while preserving
the same command validation and Docker behavior as `reploy app`.

Use `--dir` when you want a different staging directory for this app instance.

## 4. Start and Test Staging

```bash
reploy up
reploy test
```

`reploy up` prepares the selected bundle automatically when the bundle is
missing or out of date. Use `reploy bundle build` when you want to force that
preparation step before starting the service.

If the app exposes configuration commands, run those through `reploy app`. The
exact commands are app-specific.

```bash
reploy app
```

## 5. Install or Update

Linux installs are systemd-backed and are the production permanent-install
path:

```bash
sudo reploy install --to /opt/example --service example
```

macOS and Windows Docker-managed permanent installs use Docker Desktop. They
use the same command surface and default to a per-user Reploy install
directory. Use `--to` when you want a specific target:

```bash
reploy install --to "$PWD/example-installed" --service example
```

macOS and Windows Docker Desktop installs provide weaker isolation than
Linux/systemd installs.
For reboot resistance on macOS and Windows, enable Docker Desktop
start-at-login; Reploy sets a Compose restart policy for the app containers,
but Docker Desktop itself is a user-session dependency.

Installing over an existing deployment updates it from the current staging
state. Managed paths declared by the blueprint are preserved by default when
their update policy is `preserve`. Replace a path only when you intentionally
want a fresh copy:

```bash
sudo reploy install --to /opt/example --replace conf
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

Only commands marked as deployed commands in the blueprint are exposed through
the installed control script.

## 6. Uninstall

```bash
sudo reploy uninstall --from /opt/example
```

On macOS and Windows, uninstall from the installed target without `sudo`:

```bash
reploy uninstall --from "$PWD/example-installed"
```

On Linux, if the target directory was deleted manually, uninstall by service
name:

```bash
sudo reploy uninstall --service-name example
```
