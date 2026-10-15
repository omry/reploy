# Docker seccomp data for Podman compatibility

`docker-v0.2.4.json` is unchanged data from
[moby/profiles, tag seccomp/v0.2.4](https://github.com/moby/profiles/blob/seccomp/v0.2.4/seccomp/default.json).
SHA256: `785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334`.
The upstream Apache 2.0 license is included in `LICENSE`.

Reploy's strict logical sandbox still requires `seccomp=builtin`. Docker receives
that option unchanged. For an identified local Linux Podman engine, application
renderers supply this pinned Docker profile explicitly. Reploy does not inherit
the Podman daemon's custom/default profile and never falls back to unconfined.
This pins a Docker policy revision; it does not claim to reproduce every Docker
release's changing built-in profile. Profile updates require a reviewed data,
digest and provenance update plus real startup and security regression checks.

The generated read-only profile is stored under the user's cache directory at
`reploy/seccomp/`. Its stable content-addressed path prevents Compose from
recreating containers just because Reploy ran again. Existing data is checked
before use; unsafe directories, symlinks, altered permissions or altered bytes
cause an error. Temporary translated Compose files are removed after use.

## Engine scope

Stock Podman 6.1.4 is affected: `builtin` is treated as a filename, and Docker
CLI's inline profile JSON is also treated as a filename. It cannot use this
translation. The engine must contain the inline-profile fix from upstream
[#29726](https://github.com/podman-container-tools/podman/pull/29726).
The compatibility fixture uses exact upstream merge commit
`5866b09c1f064775ab5e049c120ffe51c1cf1623`; a minimum released version is not yet
established. Backported support must be tested, not inferred from a version
string. An incompatible engine fails at creation, before application startup.

This translation can be removed when Podman's Docker-compatible API implements
Docker's explicit `builtin` semantics and the full application, Compose and
controlled-session security tests pass without translation. Inline JSON support
alone is a prerequisite for this workaround, not a reason to remove it.

## Temporary home ownership

Podman 6.1.4 and the tested upstream merge also reject Docker tmpfs `uid` and
`gid` options ([#29979](https://github.com/podman-container-tools/podman/issues/29979)).
For generated application containers only, Reploy keeps the 64 MiB limit,
`0700`, `noexec`, `nosuid` and `nodev`, and asks its trusted startup helper to
set the planned owner of the fixed `/mnt/reploy-home` tmpfs mount root. It
checks the kernel mount identity, filesystem, mode, size and restrictions
before changing ownership through a pinned descriptor. It never walks files
or accepts an application-selected path. Invalid setup stops before app exec.

This adds `CHOWN` to the trusted setup capabilities on Podman. The existing
authority drop clears every capability, including the bounding and ambient
sets, before the app starts. Docker rendering and the logical sandbox plan
remain unchanged. Existing runtime images must be rebuilt to embed the updated
helper. This workaround can be removed after an engine with the upstream
ownership fix passes the same UID/GID, permissions and security checks using
the original mount options. Inline-profile support from #29726 is still required.
