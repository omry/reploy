---
sidebar_position: 9
---

# Version Support

Use this page when choosing `blueprint.requires_reploy`.

`requires_reploy` is a lower-bound constraint. Set it to the newest Reploy
version required by any blueprint field or lifecycle behavior that the app
depends on.

`0.5.0.dev1` predates runtime after-start hooks. Use `>=0.5.1.dev1` when a
blueprint uses `docker.runtime.hooks`.

## Blueprint Feature Versions

| Minimum Reploy | Supported surface | Use this minimum when |
| --- | --- | --- |
| `>=0.5.1.dev1` | Runtime after-start health checks through `docker.runtime.hooks.after_start[].health_check.wait`. `reploy up` verifies that the service is still running, runs configured runtime health checks before success, prints the service URL only after those checks pass, reports startup log snippets for failed starts, and shows exited services in `reploy status`. | The blueprint declares `docker.runtime.hooks`, or the app depends on `reploy up` failing when the service exits immediately after start. |
| `>=0.4.8.dev1` | Current baseline blueprint authoring used by the smoke fixture: Python provider, Docker runtime, managed path mounts, bundle options, Docker command defaults, app commands, install hooks, install success output, and Docker-managed user installs on supported hosts. | The blueprint does not use `docker.runtime.hooks`. |

## Platform Support Versions

| Minimum Reploy | Linux | macOS | Windows |
| --- | --- | --- | --- |
| `>=0.5.1.dev1` | Docker-backed staging, Docker-managed user installs, and systemd system installs. | Docker-backed staging and Docker-managed user installs. | Docker-backed staging and Docker-managed user installs. |
| `>=0.4.8.dev1` | Docker-backed staging, Docker-managed user installs, and systemd system installs. | Docker-backed staging and Docker-managed user installs. | Docker-backed staging and Docker-managed user installs. |

See [Support](/docs/support-matrix) for the current host matrix.

## Runtime Hook Shape

Runtime hooks are intentionally narrower than install hooks. Install hooks can
run app commands or health checks during `reploy install`. Runtime after-start
hooks currently support only health checks during `reploy up`:

```yaml
blueprint:
  requires_reploy: ">=0.5.1.dev1"

docker:
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_HOST_BIND
    port_env: REPLOY_HOST_PORT
    path: /_health_
  runtime:
    hooks:
      after_start:
        - health_check:
            wait: true
```
