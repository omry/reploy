# OmegaConf Inspector

OmegaConf Inspector is a small Reploy demo service. It lets a user create a
project, add YAML config layers, merge them with OmegaConf, and inspect
unresolved and resolved output.

The app is intentionally neutral: it demonstrates Reploy staging, dependency
bundling, config bootstrap, writable runtime data, ports, health checks, app
commands, install, status/logs, update, and uninstall without relying on
Arbiter or another domain-specific service.

## Local Demo Flow

```bash
reploy stage omegaconf-inspector-demo --dir /tmp/reploy-omegaconf-inspector-demo
reploy app config init --dir /tmp/reploy-omegaconf-inspector-demo
reploy app config check --dir /tmp/reploy-omegaconf-inspector-demo
reploy bundle build --dir /tmp/reploy-omegaconf-inspector-demo
reploy up --dir /tmp/reploy-omegaconf-inspector-demo
```

When working on the in-repo example, stage the local blueprint instead:

```bash
reploy stage file:examples/omegaconf-inspector/reploy --dir /tmp/reploy-omegaconf-inspector-demo
reploy app config init --dir /tmp/reploy-omegaconf-inspector-demo
reploy app config check --dir /tmp/reploy-omegaconf-inspector-demo
reploy bundle build --dir /tmp/reploy-omegaconf-inspector-demo
reploy up --dir /tmp/reploy-omegaconf-inspector-demo
```

Then open the staged service URL reported by Reploy.

## Local Python Development

```bash
python -m venv .venv
.venv/bin/python -m pip install -e .
.venv/bin/omegaconf-inspector config init --dir .
.venv/bin/omegaconf-inspector config check --dir .
.venv/bin/omegaconf-inspector serve --dir . --host 127.0.0.1 --port 8076
```
