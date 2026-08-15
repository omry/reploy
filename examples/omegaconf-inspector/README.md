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
STAGING=/tmp/reploy-omegaconf-inspector-demo
reploy stage omegaconf-inspector-demo --dir "$STAGING"
reploy build --dir "$STAGING"
"$STAGING/appctl" config init
"$STAGING/appctl" config check
"$STAGING/appctl" up
reploy test --dir "$STAGING"
```

When working on the in-repo example, stage the local blueprint instead:

```bash
STAGING=/tmp/reploy-omegaconf-inspector-demo
reploy stage file:examples/omegaconf-inspector/reploy --dir "$STAGING"
reploy overrides --dir "$STAGING"
reploy build --dir "$STAGING"
"$STAGING/appctl" config init
"$STAGING/appctl" config check
"$STAGING/appctl" up
reploy test --dir "$STAGING"
```

Staging the local blueprint imports its adjacent development override, which
selects the current `examples/omegaconf-inspector` checkout. The override editor
can inspect or change that choice; the published blueprint remains
source-independent.

Then open the staged service URL reported by Reploy.

Install the tested staging state, then use the control command in the installed
directory:

```bash
INSTALL="$PWD/omegaconf-inspector-installed"
reploy install --dir "$STAGING" --scope user --to "$INSTALL"
"$INSTALL/appctl" status
"$INSTALL/appctl" config show
"$INSTALL/appctl" logs
```

## Local Python Development

```bash
python -m venv .venv
.venv/bin/python -m pip install -e .
.venv/bin/omegaconf-inspector config init --dir .
.venv/bin/omegaconf-inspector config check --dir .
.venv/bin/omegaconf-inspector serve --dir . --host 127.0.0.1 --port 8076
```
