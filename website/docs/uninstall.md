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

Remove the target directory as part of uninstall:

```bash
sudo reploy uninstall --from /opt/example --remove-dir
```

The service-name flow is intended for recovery when a target directory was
manually deleted but Docker or system service state still exists. Docker
Desktop-backed macOS uninstall requires the installed deployment state at
`--from`. Recovery trusts only an exact, regular systemd unit marked as managed
by Reploy, verifies its recorded service, target, and Compose project, and
refuses to run if the target directory still exists. Because the deleted
directory also deletes its run queue, admission mode flags have no additional
effect on this recovery-only path.

By default, uninstall fails while app commands or shell sessions are active or
waiting. Use `--wait` to join the queue, `--drain` to let active runs finish and
cancel queued runs, or `--force` to stop active runs and cancel queued runs.
