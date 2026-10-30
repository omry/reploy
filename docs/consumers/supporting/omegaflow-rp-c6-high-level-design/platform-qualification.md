# Public-host qualification contract

Reploy already supports Linux, macOS and Windows as product hosts; WSL2 with
Docker Desktop is also an explicitly required first-delivery case. This design
inherits that commitment. It does not present those hosts as a new product-scope
expansion. See [the maintainer clarification](platform-contract-clarification.json).

The first OmegaFlow workload profiles remain Linux/amd64. Host architecture,
container architecture and emulation are separate facts. A Linux guest or WSL
invocation does not establish native Windows or native macOS client support.
Linux-container engines remain the runtime model. Podman, rootless, remote
engines and Windows-container workloads are not added by this request.

## Required public acceptance

Every supported host/engine case must qualify the public inspection -> expected
state admission -> launch path, including nominal completion, malformed/stale
refusals before bootstrap, capture substitution and mount-shadow refusal,
concurrent replacement, cancellation, parent loss, interrupted cleanup and exact
owned-resource recovery. Do not lower integrity guarantees to obtain a pass.
The protected-copy fixture proves one mechanism only and is not acceptance.

Record exact Reploy source/release, host OS/build/kernel, client and server engine
versions, Desktop version where applicable, host/container architecture and
emulation, selected immutable images, and the observed filesystem locations and
types for source inputs, private retained storage, staging/deployments and
container paths. State whether locations are WSL ext4, Windows-backed/DrvFS,
Windows native storage, macOS storage or engine-managed Linux storage. Do not
claim one filesystem result for another. A location class needs its real path
semantics/alias/permission/cleanup evidence; supported paths cannot silently fall
back to weaker verification. Preserve existing install-scope differences.

Current CI has native Linux/macOS target smoke and Windows host smoke, but
Windows smoke explicitly skips Docker. Existing CI success therefore does not
establish this new complete public path on Desktop. Plan explicit native-host
engine qualification; unavailable execution access remains an evidence gap,
not permission to drop the host.

## Observed local proof environment

See [the exact observed record](local-proof-environment.json). The dummy proof
ran from WSL2 Ubuntu 24.04.4, kernel 6.18.54.1-microsoft-standard-WSL2, with Docker
client/engine 29.8.1 and Docker Desktop 4.93.0 (240920), Linux/amd64 containers.
Its source and retained copy were on WSL ext4 under
`/home/omry/dev/reploy/temp/omegaflow-hld-copy-0bzst26e/`, at `original` and
`capture/payload`; container path was `/runtime/payload`. The local endpoint was
`unix:///var/run/docker.sock`; Docker reported `/var/lib/docker` inside its engine
storage. This does not test Windows-backed paths, native Windows or macOS.

`wsl.exe --version` and `cmd.exe /c ver` failed through the current interop with
`UtilAcceptVsock ... accept4 failed 110`. WSL package and Windows host build
versions were not obtained; do not invent them. Record them in final public
qualification through a working native path.

## Existing path-specific engineering gaps

- `internal/controlledsession/channel_unsupported.go` and
  `internal/dockerdeploy/controlled_session_controller_channel_unsupported.go`
  currently reject non-Linux host private channels. The Linux path relies on a
  host Unix socket, POSIX ownership and peer credentials crossing a bind mount.
- `internal/dockerdeploy/controlled_session_watchdog_unsupported.go` rejects
  non-Linux watchdog launch. The existing Linux child uses inherited files and
  a detached process session. Preserve parent-loss cleanup on native hosts.
- Local endpoint selection already understands Unix sockets and Windows named
  pipes; boot-session identities already exist for Linux, macOS and Windows.
  Reuse them rather than creating replacements.

These are concrete prerequisites in this feature path, not evidence that Reploy
has no general Windows/macOS support. Container-internal Linux helpers are not
host portability gaps merely because their source files have Linux build tags.
