---
sidebar_position: 6
---

# Uninstall

Use `reploy uninstall` to remove installed service wiring and stop the Docker
objects Reploy created for an installed deployment.
For Docker-managed installs, uninstall also removes Reploy's generated Python
runtime cache volume for that installed deployment. If `REPLOY_RUNTIME_DIR` was
overridden to a filesystem path under the install target, that bind-mounted
cache is removed only when uninstall also removes the target directory.

When the deployment directory still exists, uninstall from the directory:

```bash
sudo reploy uninstall --from /opt/example
```

User-scope Docker-managed permanent installs are uninstalled from the installed
target without `sudo`:

```bash
reploy uninstall --from "$PWD/example-installed"
```

On Linux, if the directory was already deleted, uninstall by service name:

```bash
sudo reploy uninstall --service-name example
```

List known Reploy services:

```bash
reploy services list
```

Preview without making changes:

```bash
reploy uninstall --from /opt/example --dry-run
reploy uninstall --service-name example --dry-run
```

Remove the target directory as part of uninstall:

```bash
sudo reploy uninstall --from /opt/example --remove-dir
```

The service-name flow is intended for recovery when a target directory was
manually deleted but Docker or system service state still exists. Docker
Desktop-backed macOS uninstall requires the installed deployment state at
`--from`.
