---
sidebar_label: Script
---

# Install with the Script

The install scripts download a release binary from GitHub and place it in a
user-owned bin directory.

On Linux and macOS:

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh
```

On Windows, run PowerShell:

```powershell
irm https://reploy.yadan.net/install.ps1 | iex
```

By default the Linux/macOS script installs to:

```text
$HOME/.local/bin/reploy
```

By default the Windows installer installs to:

```text
%LOCALAPPDATA%\Programs\Reploy\bin\reploy.exe
```

The installer prints the requested version, detected platform, download URL,
target path, installed binary version, and a PATH hint when the installed
command is not already on `PATH`.

On macOS, initial Reploy release binaries may be unsigned and unnotarized. If
macOS blocks first launch, use the standard Gatekeeper approval flow for a
trusted downloaded command-line tool. Developer ID signing and notarization are
planned as release hardening work.

On Windows, initial Reploy release binaries may be unsigned. Users may see
SmartScreen or enterprise endpoint protection prompts until Authenticode
signing is added.

## Parameters

Use `--to DIR` on Linux/macOS or `-To DIR` on Windows to choose the directory
where the `reploy` executable is installed.

The Linux/macOS installer does not edit shell profile files and does not invoke
`sudo`. The Windows installer does not edit the user PATH unless `-AddToPath`
is passed, and it never edits the machine PATH. Choose a writable directory or
run the command in the privilege context you intend to use.

Use `--version VERSION` on Linux/macOS or `-Version VERSION` on Windows to
install a specific Reploy release. When no version is provided, the installer
reads `VERSION` from the `main` branch and downloads the matching release
asset.

## Example

```bash
curl -fsSL https://reploy.yadan.net/install.sh | sh -s -- --to "$HOME/bin" --version 0.2.0.dev1
```

```powershell
irm https://reploy.yadan.net/install.ps1 | iex
& "$env:LOCALAPPDATA\Programs\Reploy\bin\reploy.exe" --version
```
