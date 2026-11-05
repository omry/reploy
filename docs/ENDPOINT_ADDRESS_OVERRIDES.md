# Endpoint publication address overrides

Use `overrides.yaml` beside a staged deployment to change a named Docker
endpoint's host address without editing its blueprint:

```yaml
environment:
  id: arbiter
  package_overrides: {}
docker:
  workload:
    endpoints:
      https:
        publish:
          address: 0.0.0.0
```

The endpoint must exist in `docker.workload.endpoints`. Only its published host
address changes. Container binding, scheme, container port, readiness settings,
and staging/deployed ports keep their blueprint values. For Arbiter, this keeps
staging port 18075 and deployed port 8075. Other endpoints remain unchanged.

`0.0.0.0` publishes on all IPv4 interfaces. Use `127.0.0.1` for loopback or an
explicit host IP such as `192.0.2.42`. IPv6 addresses use the existing bare or
bracketed forms, such as `::1` or `[::]`. Wildcard probe targets use loopback
(`0.0.0.0` → `127.0.0.1`, `::` → `::1`). Empty addresses, hostnames, URLs,
address/port pairs, `*`, unknown endpoints, and unrelated Docker fields are
rejected. Remove the endpoint override to restore the blueprint address.

Run `reploy overrides --dir DIR` and press **N** to edit endpoint addresses.
The editor shows each endpoint's effective address, **From blueprint** or
**Explicit override**, and both phase ports. Select an endpoint and press
**Enter** to edit; press **D** to reset that endpoint to **From blueprint**.
**Esc** cancels the input. Existing preview, save, and concurrent-edit checks
apply. Saving only writes the sidecar. It does not start/restart workloads,
install a deployment, change host networking, or run a trial build. Press **V**
explicitly to opt into the existing trial validation/build behavior.

A local blueprint's sidecar seeds a new stage. Updating a stage preserves its
existing sidecar and does not reimport changes from the blueprint directory.
Remote references retain their existing sidecar-import behavior. If an updated
blueprint removes an overridden endpoint, remove that override before updating.

Build and apply edits through the existing lifecycle. `reploy up` and staged
restart check changed inputs and build when needed. `reploy install --dir DIR
--to TARGET --scope user` applies the staged address to an installation using
its deployed ports. An installation records the address in its build lock;
installed restarts and control commands use that recorded value. Health checks
also retain the running workload’s recorded private launcher mode. To change or
reset an installed address, edit the stage and reinstall. Editing or resetting a
stage cannot silently change a running installation. Staged status, logs, shell,
commands, health checks, and stop use the generation's recorded address even
after a sidecar edit. Starting or restarting a staged workload requires a build
that matches the current address override; the CLI builds changed inputs first.
Explicit build verification also refuses a stale address override.

Package/source choices, variables, identity overrides, base image overrides,
and blueprint compatibility metadata keep their existing meanings. Endpoint
address intent participates in validation and build-lock identity, so changing
it cannot reuse a generation as though the prior address were still selected.
This setting does not configure TLS, DNS, firewall rules, or authentication.
