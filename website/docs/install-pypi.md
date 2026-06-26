---
sidebar_label: PyPI
---

# Install from PyPI

Reploy is published as platform-specific Python wheels:

```bash
python -m pip install reploy
```

To install a specific release, pin the package version:

```bash
python -m pip install reploy==0.2.0.dev1
```

The wheel does not install a Python API for deploying apps. It installs the
native `reploy` executable into the selected Python environment's scripts
directory, so the command is on `PATH` when that environment is active.
