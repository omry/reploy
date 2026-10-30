# Interview synthesis and engineering follow-up

The requirements draft carries the corrected consumer scope. Existing project
constraints determine the implementation stack. No owner decision about a new
stack, public syntax, protection mechanism or slice plan has been approved.
The unnecessary stack question was withdrawn; see
[the correction record](interview-corrections.md).

## Coherent responsibility and trust boundaries

OmegaFlow authors the blueprint, expected runtime inventory and compatibility
policy. Reploy selects and delivers the image, verifies supplied retained bytes,
reports generic prepared facts and admits only the inspected state. The workload
can access declared live data and session resources but cannot replace protected
payload through its writes or a shadowing mount. Hostile host root and stronger
same-identity containment remain outside this request.

The normal journey is image selection and inherited-fact inspection, consumer
composition, final preparation, final metadata/hash/absence inspection, consumer policy check,
then expected-state admission through existing session ownership. A relevant
mismatch refuses before bootstrap. Cancellation and interrupted preparation must
preserve existing cleanup ownership. An independent verified payload capture
does not depend on later survival of the original source. Moving an original image
tag is also irrelevant when the same inspected immutable image is retained and
launched. See the [consumer clarification](consumer-clarifications.md).

These are responsibility boundaries and required journeys, not a selected HLD
mechanism or proof that existing internal checks already satisfy them.

## Bounded source observations

- `internal/cli/controlled_session.go` and
  `internal/dockerdeploy/current_controlled_session_run.go` have no public
  expected-preparation input. Existing admission loads verified controller and
  workload runtime under sorted operation locks.
- `internal/providerstore/store.go` has `PublishExpected`, which streams retained
  bytes, compares the descriptor and only then publishes the digest-addressed
  object. This is useful capture machinery, not proof of final runtime delivery
  or mount protection.
- `internal/probe/protocol.go` returns resolved executable metadata and hashes.
  The corrected consumer scope needs bounded metadata/hash/absence evidence, not
  raw file-byte export. Public integration and general absence/error semantics
  still need design; this observation does not establish their implementation.
- `internal/dockerdeploy/docker_image_inspect.go` retains image metadata and
  normalizes environment entries into a map. The public consumer handoff must
  separately establish inherited/application/final facts rather than assume a
  final normalized environment satisfies the whole requirement.
- The consumer's Part C design states the first workload is Linux/amd64 with
  three qualified Bash profiles. No broader workload support follows from an
  ARM64 controller or artifact build.

These are source observations at the retained E2.2 checkout. No new runtime
proof, installed-release test or complete feasibility assessment ran for this
interview synthesis.

## Engineering investigations for HLD and LLD

| Work | Depends on | Required result |
| --- | --- | --- |
| Image selection and environment provenance | Existing image inspection, preparation and consumer composition | Same selected immutable image through final preparation; inherited facts cannot be hidden by final composition; private values remain private. |
| Non-executing file inspection | Existing probe/image/runtime paths | Smallest safe bounded metadata/hash/absence mechanism, with path/type/link/error semantics and no untrusted workload execution; no raw file export. |
| Protected payload delivery | Expected-hash artifact publication and existing runtime mount planning | Retained bytes match supplied expectations and remain protected from source changes, workload writes and mount shadowing through use; explicit size bounds and cleanup. |
| Expected-state admission | Final observation identity and existing locked session admission | Binding lifetime, replay/invalidation and concurrent replacement behavior; invalidate actual bound-state changes, retain validity for external-reference changes that do not affect retained state; stale refusals before bootstrap; no separate authority system. |
| Integrated qualification and slice plan | The selected architecture and load-bearing mechanism evidence | Full requirement-to-slice/test coverage, supported cohort, bottom-up production/configuration and test LOC estimates with totals, and release/adoption gates. |

Investigate ordinary engineering choices autonomously. If evidence establishes a
material behavior, trust, compatibility, scope or cost tradeoff, return a concrete
decision brief to the maintainer at the responsible stage. Do not invent that
choice before the investigation, silently drop a requirement or claim LLD
readiness while a prerequisite is unproved.
