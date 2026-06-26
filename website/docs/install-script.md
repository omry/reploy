---
sidebar_label: Script
---

# Install with the Script

The install script downloads a release binary from GitHub and places it in a
user-owned bin directory:

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh
```

By default the script installs to:

```text
$HOME/.local/bin/reploy
```

The installer script prints the requested version, detected platform, download
URL, target path, installed binary version, and a PATH hint when
`$HOME/.local/bin` is not already on `PATH`.

## Choose a Directory

```bash
curl -fsSL https://reploy.yadan.net/install.sh \
  | sh -s -- --to "$HOME/bin"
```

The installer does not edit shell profile files and does not invoke `sudo`.
Choose a writable directory or run the command in the privilege context you
intend to use.

## Install a Specific Version

```bash
curl -fsSL https://reploy.yadan.net/install.sh \
  | sh -s -- --version 0.2.0.dev1
```

When no version is provided, the script reads `VERSION` from the `main` branch
and downloads the matching release asset.
