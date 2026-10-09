---
sidebar_position: 8
---

# Support

Reploy currently supports Docker-backed staging and installs.

For feature support by Reploy release, see [Version Support](/docs/version-support).

| Host OS | Release targets | Docker runtime | Staging | User install | System install |
| --- | --- | --- | --- | --- | --- |
| Linux | `linux-amd64`, `linux-arm64` | Docker Engine | ✅ | ✅ Docker-managed | ✅ systemd |
| macOS | `darwin-amd64`, `darwin-arm64` | Docker-compatible | ✅ | ✅ Docker-managed | — |
| Windows | `windows-amd64`, `windows-arm64` | Docker Desktop | ✅ | ✅ Docker-managed | — |

## Portable Tools

Portable-tool support is qualified separately from the host release targets
above. The current embedded catalog supports these exact tuples; every row
below was derived from current catalog cases and matched to passing native
validation records from [CI run 37889434362](https://github.com/omry/reploy/actions/runs/37889434362).
The qualified definition revision is `1` for each release.

| Tool request | Context | Exact base-image distributions | OCI architectures | Binding / selection | Qualified cases |
| --- | --- | --- | --- | --- | --- |
| `tool:java==21~1` | build | Debian 12, Debian 13, Ubuntu 25.10, Ubuntu 26.04 | `linux/amd64` | None | 4 |
| `tool:playwright==1.61.0~1` | runtime | Debian 12, Ubuntu 25.10, Ubuntu 26.04 | `linux/amd64` | Python / Chromium | 3 |
| `tool:bash==5.2.15~1` | build, runtime | Debian 12 | `linux/amd64`, `linux/arm64` | None | 4 |
| `tool:bash==5.2.37~1` | build, runtime | Debian 13 | `linux/amd64`, `linux/arm64` | None | 4 |
| `tool:bash==5.3.9~1` | build, runtime | Ubuntu 26.04 | `linux/amd64`, `linux/arm64` | None | 4 |

Java `21` selects Eclipse Temurin JDK `21.0.12+8` and exports `java` and
`javac` only in the source-builder environment. For Playwright, explicitly
select the `python` binding and `browser: chromium` in a structured request.
Bash uses the target distribution's pinned native package, with separate build
and runtime evidence for each architecture.

Other Java versions, runtime Java, Playwright bindings or browsers, and Java
or Playwright ARM64 are not qualified. Cross-compilation and static catalog
validation alone do not establish portable-tool support.

Runtime Bash evidence identifies the exact lock, selected closure, immutable
image, package-owned export, and consumer `/bin/bash` bytes. This handoff
supports downstream qualification; OmegaFlow owns its adapter compatibility
decision. Reploy does not provide a recorder or an asciinema tool.
