# F/G restart and preparation fencing evaluation

This records the approved unchanged-container retry direction for
[the multi-identity implementation plan](../../MULTI_IDENTITY_IMPLEMENTATION_PLAN.md),
with mechanism evidence and remaining engineering gates. The maintainer approved
the scoped approval-lifetime amendment. No production restart policy, startup
code, host service or governing stamp was changed.

## Recommendation and boundary

Evaluate engine-native retry for the same previously admitted container, while
Reploy owns lifecycle policy, explicit stop/revocation and preparation fences.
Do not introduce a callback service solely to repeat host checks after a trusted
administrator changes the system. The existing trust model assigns coordination
of those changes to the administrator.

Container creation/replacement and changed image, mapping or mount configuration
still need normal Reploy admission. A proposed permission to retry an unchanged
container must not become transferable authority for another container,
installation, engine, generation, role or configuration.

The governing text is amended to reflect that scoped decision and still needs
affected review. The behavior is not implemented by enabling a restart flag or
reusing a release frame.

## What was tested

The probe used an existing local BusyBox image, pinned by full image ID, with no
pulls, host bind mounts, published ports or production container changes.
Both engine probes used the Docker-compatible interface. Rootless Podman ran
against a new temporary VFS store and a short-lived private API process, not
the user's existing Podman store or a configured host service.

| Environment | Observed version and location |
| --- | --- |
| Host | WSL2, kernel `6.18.54.1-microsoft-standard-WSL2` |
| Docker substrate | `Docker Desktop (containerized)`, Linux amd64; Engine 29.8.1, API 1.56 |
| Podman substrate | Local Linux Podman 4.9.3, rootless; separate VFS store under `/tmp/reploy-fg-podman-pg0j371k/store`, runtime under its sibling `runtime` directory |
| Podman test connection | Private Unix socket under the same temporary directory; Docker client API fixed to 1.41 for the compatibility probe |
| Image | `sha256:b116e155074440ffd9e449559433feb4cd2341eb3554b1da1c638c976e56451d` |
| Raw local evidence/scripts | `temp/reviews/fg-restart-fence-20261101/` |

| Observation | Docker | Podman 4.9.3 |
| --- | --- | --- |
| Same container automatically restarts after its main process exits, without Reploy running | Passed; same full ID and restart count increased | Passed; same full ID and restart count increased |
| Change an existing policy to `no`, then kill the process and observe no retry | Passed | Compatible update returned an error; policy remained `always`. This is not a supported fence on this tested path. |
| Remove the exact container while it is automatically restarting/running | Passed; absent afterward | Passed; absent afterward |
| Explicitly stop an `always` container and observe it stay stopped for three seconds | Passed | Passed |
| Remove the stopped container and observe it remain absent | Passed | Passed |
| Container removal preserves a separate named volume and its sentinel contents | Passed | Passed |

All disposable containers and volumes were cleaned up. The temporary Podman API
process exited, and its isolated pause namespace was retired after checking that
the temporary store had no containers or volumes. The existing Podman store and
configured services were not used.

The first harness attempts encountered absence-message handling errors; they
are retained as failed attempts. The final runs used explicit container inspect
and recorded unsupported policy update separately. No failed capability was
silently converted into a positive result.

The [compact evidence inventory](restart-fence-evidence/summary.json) identifies
the actual final results, limits and hashes of the local raw files/scripts.

## What the observations support

Removing the owned container is a common candidate fence: that engine object
cannot execute another retry once it is absent. Its separate retained data need
not be removed. This resembles existing Reploy `down`/recreation behavior and
avoids depending on a runtime policy update that the tested Podman path lacks.

Removal alone is not a complete F/G transaction or proof that all processes and
storage attachments are gone. It also does not fence an independent host unit
that could create a replacement container. Those remain explicit owning checks.

The candidate transaction is:

1. Under the existing installation lock, persist the inhibited operation and
   exact owned resources before any effect.
2. Fence every registered recovery owner that could create a replacement.
   A manager stop must not deadlock against an operation lock needed by its
   own cleanup path.
3. Stop and remove the exact owned container without deleting retained
   application storage. Use independently validated ownership, not a name-only
   lookup or application-reported identity.
4. Prove exact runtime retirement and that the relevant storage is unattached.
   An API success, stopped PID or absence message alone is insufficient.
5. Only then prepare or modify declared storage.
6. On a later permitted start, create a new container through full admission.
   Restore allowed recovery only after the transaction's completion checks.

If a process, network request or engine fails during these steps, preserve the
pending operation, ownership and reservations. Do not report success, mutate
storage early, or enable retries against ambiguous preparation state. Recovery
reconciles exact resources under the same transaction before continuing.

Reploy stop/revocation is complete only after its fence is established. A
persisted intent is not itself an engine-side stop. This is distinct from
promising instantaneous interruption at the moment an operator requests stop.

Container removal discards its writable layer. The default immutable image and
separate managed data fit that model, but the explicit writable-image-root
option must document this boundary. Do not promise preservation of image-root
edits when Reploy removes/recreates a container.

## Approved startup direction and mechanism boundary

The current E implementation intentionally prevents direct engine retry:

- `internal/probe/mapped_wait.go` requires a fresh frame on stdin and a fixed
  deadline embedded in the create arguments.
- `internal/dockerdeploy/podman_mapped_waiter.go` consumes one release attempt,
  verifies the pinned process and requires restart count zero and policy `no`.
- `internal/mappedlaunch/release.go` binds the frame to a particular operation,
  revision, image, plan and container. It is not a reusable approval protocol.

There are two distinct choices:

1. **Fresh permission for every start.** Retain the per-start requirement. A
   host runner or reachable callback receiver must provide it. A callback moves
   the availability requirement; it does not remove it.
2. **Reuse approval for the unchanged container.** Permit the engine to restart
   that exact previously admitted object without contacting Reploy. A new or
   changed container still requires admission. This needs a deliberately
   designed and proven way to distinguish first admission from permitted retry;
   neither a writable application flag nor replaying the old frame is a proof.

The maintainer selected the second direction. The original engine-fencing
probes did not prove its startup mechanism. A subsequent bounded
[startup-input proof](fg-startup-input-engineering.md) tested a host-controlled,
read-only record on both engines. That candidate is not production integration
or a signing service, general admission cache or callback endpoint.

## Compatibility and required next proofs

- Review the affected amendment before dependent implementation. Affected text
  includes the security contract's per-entry admission
  and engine-restart prohibition; the campaign's E/G lifetime sections; mail
  lifetime reconciliation; and the E handoff's zero-restart assumptions.
- Keep credential/capability reduction, private descriptor closure, trusted-code
  protection and effective sandbox checks on every actual process start. Reusing
  host admission must not mean skipping the trusted local startup transition.
- Resolve startup hooks, readiness, private environment injection and secrets
  for retry. A restarted process does not necessarily retain ephemeral tmpfs
  data; these probes did not establish those paths. Preserve the once-per-instance
  delivery contract unless an explicit amendment changes it.
- Qualify the full mapped startup/retry/fence sequence on the eligible Podman
  backend, including process loss and each transaction crash boundary. An
  ordinary rootless container is not mapped qualification.
- Cover registered system/user recovery owners and actual supported OS/engine
  combinations. A WSL2 Docker Desktop run is not native Windows, macOS or native
  Linux service-manager/reboot evidence.
- Test daemon/VM restart, host reboot and crash durability in disposable
  environments. No live host or existing engine was restarted by these probes.
- Normalize policy meanings explicitly. Podman 4.9.3 documents `unless-stopped`
  as identical to `always`; do not promise Docker's reboot semantics by passing
  the same spelling to both engines.

The documentation baseline is
[Docker restart policy behavior](https://docs.docker.com/engine/containers/start-containers-automatically/),
[Podman 4.9.3 restart policy behavior](https://docs.podman.io/en/v4.9.3/markdown/podman-run.1.html#restart-policy)
and [Podman 4.9.3 update scope](https://docs.podman.io/en/v4.9.3/markdown/podman-update.1.html).
These are contract inputs, not replacements for the actual versioned proof.

This investigation does not finalize F/G slices or revised line forecasts. Any
startup-protocol adaptation, policy compatibility and cross-platform proof work
must be counted in its actual owning slices. It does not justify silently adding
a new service framework or removing a required positive acceptance journey.
