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
reploy install <app-blueprint-ref> --scope user
```

Use `--scope user` when you want the install owned by your current user. Reploy
uses a host-appropriate per-user default install directory unless the blueprint
or `--to` provides another target. Use `--scope system` with root/admin
authority when you want a Linux systemd-backed install.

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

Run `reploy` with no arguments inside or beside a staging deployment to see the
active app, useful lifecycle commands, and a small app-command sample. Use
`reploy app` when you want the complete app-specific command list.

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

`stage --update` normally refuses to replace a staging directory belonging to
a different blueprint. To replace it intentionally, provide the new ref and
`--force`:

```bash
reploy stage --update <new-app-blueprint-ref> --force
```

Forced replacement cancels queued runs, stops active runs and the staged
workload, removes the old selected image reference, and records the replacement
as a fresh unbuilt staging deployment. It does not overwrite arbitrary edited
files in the directory.

Remove a staging deployment, including its running workload and Reploy-owned
image reference, with:

```bash
reploy stage --remove --dir <staging-directory>
```

Removal stops the staged workload but refuses to interrupt another active
Reploy operation. Add `--force` to cancel that active work before removal. If
filesystem cleanup cannot finish, Reploy reports the hidden directory
containing whatever could not be removed so it can be cleaned up manually.

### Configure the workload environment

For apps configured through environment variables, create `.env` in the
staging deployment:

```dotenv
API_TOKEN=replace-me
LOG_LEVEL=info
```

Use one `NAME=value` assignment per line. Reploy accepts unquoted,
single-quoted, and double-quoted values, but does not perform shell expansion
or interpolation.

Treat this file as a secret. On Linux and macOS, restrict it to the deployment
owner before starting the workload:

```bash
chmod 600 reploy-staging/.env
```

On Windows, remove inherited access and grant read/write access only to the
deployment owner, SYSTEM, and Administrators. Reploy rejects links, shared
files, and permissions or ACLs that expose the file to other users.

Reploy passes these values only to the launched workload. It does not mount
`.env` or place its names or values in Compose, Docker metadata, image
configuration, Reploy state, or build locks. If a runtime bind exposes the
deployment directory or one of its parents, Reploy masks both `.env` and the
internal `.reploy` directory at every path where that mount would expose them.
This applies to workload, shell, app-command, and lifecycle containers.
Reploy may create an empty owner-only `.env` placeholder to keep that mask
stable; an empty file does not configure any workload values.
Because Docker cannot repeat this private one-shot injection by itself, a
blueprint using an autonomous Docker restart policy cannot be started with
`.env`; use the app control command to restart it.

Installation copies `.env` from staging on the first install and preserves the
installed copy on updates. Use `--replace .env` (or `--clean`) only when you
want to replace the installed values from staging.

## 4. Build, Start, and Test Staging

```bash
reploy build
reploy up
reploy test
```

`reploy build` resolves the selected application and environment packages,
builds the environment image, and validates the resulting image. `up` and
`restart` can perform that build automatically when the selected build is
missing or stale. Staged app
commands require a current build and tell you to run `reploy build` when one is
missing. Use `reploy build --no-cache` when you need to rerun resolution and
image construction explicitly.

If the app exposes configuration commands, run them through the app-named
control command in the staging directory. The exact commands are app-specific.
Use `reploy app` to show the complete app-command list.

```bash
./reploy-staging/examplectl config check
reploy app
```

## 5. Install or Update

Linux system-scope installs are systemd-backed and are the strongest
production permanent-install path:

```bash
sudo reploy install --scope system --to /opt/example --service example
```

User-scope permanent installs are Docker-managed. On macOS and Windows they use
Docker Desktop; on Linux they use the invoking user's Docker runtime. They use
the same command surface and default to a per-user Reploy install directory.
Use `--to` when you want a specific target:

```bash
reploy install --scope user --to "$PWD/example-installed" --service example
```

Linux, macOS, and Windows user-scope installs are Docker-managed Compose
installs. They provide weaker isolation than Linux/systemd installs.
For reboot resistance, the Docker runtime itself must start outside Reploy;
Reploy sets a Compose restart policy for the app containers, but Docker
Desktop or a user Docker session is still an external dependency.

Installing over an existing deployment updates it from the current staging
state. Managed paths declared by the blueprint are preserved by default when
their update policy is `preserve`. Replace a path only when you intentionally
want a fresh copy:

```bash
sudo reploy install --scope system --to /opt/example --replace conf
sudo reploy install --scope system --to /opt/example --clean
```

For side-by-side installs, use a different target directory, service name, and
port.

```bash
sudo reploy install --scope system --to /opt/example2 --service example2 --port 8082
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
