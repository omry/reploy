# RP-C6 HLD source investigation

This is supporting design evidence, not the HLD or implementation approval.
Requirements were reviewed clean and remain unchanged. No product source changed.

## Existing primitives and gaps

| Responsibility | Existing source | Reuse and missing work |
| --- | --- | --- |
| Selected immutable image | `internal/deploy/image.go`, `internal/dockerdeploy/docker_image_inspect.go`, `provider_base_prepare.go` | Existing descriptor binds config/rootfs/platform. Add public selection facts and carry the exact selection through normal build; current cached author-reference resolution is not the new retained-selection contract. |
| Environment provenance | `internal/dockerdeploy/docker_image_inspect.go` | Raw image Env is normalized into sorted unique BaseConfig entries. Retain inherited/application/final layers for consumer evaluation and keep private values outside reports. |
| Non-workload inspection | `internal/dockerdeploy/probe_run.go`, `probe_workspace.go`, `internal/probe/inspect_linux.go` | Existing read-only, networkless validation container starts the packaged fixed Go helper, not the image entrypoint. Extend bounded path/type/hash/absence observations; current resolver does not by itself establish complete ancestor-link or required-absence semantics. |
| Supplied-byte capture | `internal/providerstore/store.go` | PublishExpected compares streamed retained bytes before publication. OpenVerifiedArtifact preserves the opened file across pathname replacement. A protected tree still needs inventory, independent file materialization and publication/reachability ownership. |
| Runtime delivery | `internal/dockerdeploy/controlled_session_plan.go`, `current_runtime_files.go` | Current session already requires read-only root, drops capabilities and renders explicit mounts, private metadata masks and fixed startup verifier. Reuse protected read-only bind projection with independent capture, no writable alias and no shadowing; mode changes alone are insufficient. |
| Admission | `internal/dockerdeploy/current_controlled_session_run.go` | Loads verified current runtimes under sorted deployment locks. Queue await can release/reacquire ownership. Expected prepared state must be checked again after waiting and before effects, not only at initial load. |
| Lifetime and cleanup | Existing generation references, provider store reachability and session leases | Bind captures and inspection records to existing generation/session ownership; add their exact reachability rather than a second authority service. |

## Smallest alternatives under investigation

1. A verified private copy, mounted read-only through the existing runtime plan,
   is the smallest candidate for supplied runtime files. Reuse the authored mount
   target and replace only the physical delivery source with retained bytes.
   Keep expected inventory/manifest binding generic; no OmegaFlow names in Reploy.
2. Baking payload into the normal generated image is an existing packaging
   candidate, but adds image/build-lock integration and destination checks. It is
   not needed merely to obtain independent copied bytes; prefer the direct copy
   if the complete alias, mount, lifetime and qualification proofs hold.
3. A general filesystem snapshot service, inspection daemon or new receipt
   authority has no demonstrated need and is excluded by the requirements.

Image files remain Reploy-owned. Read-only root is useful but does not by itself
prevent a mutable mount from hiding image code or creating relevant startup
files. HLD/LLD must define a generic image-code/lookup namespace boundary and
mount-overlap checks, including source aliases and automatic engine mounts. Do
not ask OmegaFlow for a loader/library inventory. Do not claim the existing
executable probe or the narrow proof below solves this boundary.

## Bounded evidence actually run

Two existing production-primitive tests passed with count=1:

- TestOpenVerifiedArtifactKeepsTheVerifiedFileAfterPathReplacement
- TestStorePublishExpectedRejectsChangedBytesBeforeBlobPublication

Command: `go test ./internal/providerstore -run '^(TestOpenVerifiedArtifactKeepsTheVerifiedFileAfterPathReplacement|TestStorePublishExpectedRejectsChangedBytesBeforeBlobPublication)$' -count=1`

A disposable scratch image contained only a trusted static Go fixture. Host
preparation copied a dummy file to a distinct inode, verified its expected digest
and then changed the original. A read-only bind into a read-only-root container
preserved the retained digest. Root inside that container, with all capabilities
dropped and no-new-privileges, received EROFS for overwrite, chmod, rename and
creation, and EPERM for remount. Exact owned container and image cleanup passed.

Proof environment: Docker 29.8.1, Linux/x86_64 daemon, **Docker Desktop
(containerized)**, local Unix socket. This is not native Linux or rootless,
Podman, remote-engine or full Reploy public-path qualification. The fixture does
not prove shadowing/alias rejection, image lookup protection, crash cleanup or
capture-race behavior. It does not change Reploy platform support.

Private original result: `/home/omry/.local/state/awd/reploy-E-resume-20261028/OmegaFlow-hld-protected-copy-proof-result.json`
Private proof source: `/home/omry/.local/state/awd/reploy-E-resume-20261028/OmegaFlow-hld-protected-copy-proof.py`

## Finite work for the HLD and later LLD

- Select the actual initial engine/host cohort with the maintainer. The existing
  controlled-session design explicitly requires evidence before Desktop,
  rootless and Podman support; the current local proof environment is Desktop.
- Design selection retention through final preparation and prove no tag
  re-resolution/fallback changes the selected image.
- Define the generic protected image-code namespace and safe inspection path
  resolution. Compare a simpler mechanism if protecting loaders/libraries would
  require a new generic dependency-analysis service.
- Resolve protected capture source opening, exact bytes/modes, inventory bounds,
  atomic publication, mount aliases/shadowing and cleanup reachability.
- Define the public opaque checked-state binding as an extension of existing
  generation/admission ownership, excluding run-specific ephemeral coordinates.
  Bind command and relevant environment/mount/identity/endpoint facts, verify
  after queue waits and retain resources through use.
- Qualify the public inspection/launch pair on the agreed cohort with both
  unchanged-retained-state success and actual-bound-change refusal controls.

No HLD, public syntax, platform expansion, final slices or estimates have been
approved. No implementation readiness is claimed.

Original result SHA256: `5f7e1c729ac94fa12fce1868dba1157c7274c4e55405b4f8712dddd494014c67`.

## HLD review result and current decision

The original workflow document's HLD loop completed two local current-agent
attempts (trail rounds 2 and 3). One high design finding required explicit
suppression of inherited image healthchecks during inspection. The next pass
verified the design-contract correction; no runtime change is claimed. The clean
HLD received its own exact current-agent review against the approved requirements
with zero findings. Both helper-generated document stamps and their dependency
closure were verified. No independent agent or remote review ran.

Original private results:
- `/home/omry/.local/state/awd/reploy-E-resume-20261028/OmegaFlow-hld-workflow-review-result.json`
- `/home/omry/.local/state/awd/reploy-E-resume-20261028/OmegaFlow-clean-hld-review-result.json`

Public host coverage inherits Reploy's existing commitment; the earlier cohort
question was an agent framing error. See [the correction](platform-contract-clarification.json)
and [full-path qualification contract](platform-qualification.md). The proposed
architecture uses normal immutable-image delivery for protected supplied inputs;
the earlier copy-bind fixture remains evidence for that narrow alternative only.
Private channel/watchdog portability and protected image namespace enforcement
remain substantive engineering proof gates. The historical estimate is not a
current delivery estimate. HLD human acceptance, LLD/slice engineering and product
implementation remain separate.
