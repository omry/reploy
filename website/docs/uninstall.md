---
sidebar_position: 6
---

# Uninstall

Use `reploy uninstall` to remove installed service wiring and stop the Docker
objects Reploy created for an installed deployment.

When the deployment directory still exists, uninstall from the directory:

```bash
sudo reploy uninstall --from /opt/example
```

On macOS, Docker-managed permanent installs are uninstalled from the installed
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
reploy uninstall --list-services
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
