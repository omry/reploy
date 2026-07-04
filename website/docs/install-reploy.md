---
sidebar_position: 1
---

# Install Reploy

Install Reploy when you want the `reploy` command on a machine that will stage,
test, install, or manage services.

Reploy is distributed as a native command-line executable. Today there are two
supported installation methods:

- [Install with the script](/docs/install-script) downloads the release binary
  directly and installs it into a user-owned bin directory. Use the shell
  script on Linux/macOS and the PowerShell installer on Windows.
- [Install from PyPI](/docs/install-pypi) installs a platform-specific wheel
  into a Python environment and exposes the same native `reploy` executable.

The install script is the most direct path for hosts that do not otherwise need
a Python environment. The PyPI package is useful when you already manage tools
inside a virtual environment.

Additional installation surfaces may be added in the future, such as package
managers (apt, yum, Homebrew, Chocolatey, etc.).
