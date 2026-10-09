# Portable Tool Cutover Evidence

PTD-29 records the delivered implementation against the accepted
[design](PORTABLE_TOOL_DEFINITION_DESIGN.md) and
[implementation plan](PORTABLE_TOOL_DEFINITION_IMPLEMENTATION_PLAN.md).
It introduces no tool, target, trust policy, or execution mechanism.

## Qualified Support

The current catalog derives 19 cases: four Java build cases, three Playwright
Python/Chromium runtime cases, and twelve Bash build/runtime cases. Each was
matched through `MatchPortableToolCaseEvidenceV1` against its canonical external
validation record from successful [CI run 37889434362, attempt 1](https://github.com/omry/reploy/actions/runs/37889434362).
The run belongs to PR #227 head
`83875fb7d6d966f8e3dfb46bfe8bace0bf205570`; the actual tested merge revision is
`e769ca18e38de54ed1afc90cda737b1b94dd262e`. These identities remain distinct.
The current selected definitions are unchanged from that qualified revision.

The match verifies manifest and selected-closure digests, exact target/context,
binding/selection sets, fixture, validator, image and original probe output.
The existing twelve-case retained-evidence integration gate also verifies both
native runner records and the runtime Bash executable provenance handoff.
The public table in `website/docs/support-matrix.md` is derived from this case
and evidence join, rather than a hand-maintained claim of catalog availability.

| Tool | Qualified scope | Native cases |
| --- | --- | --- |
| Java `21`, revision `1` | Temurin JDK `21.0.12+8`; build; Debian 12/13 and Ubuntu 25.10/26.04 AMD64 | 4 |
| Playwright `1.61.0`, revision `1` | Runtime, Python, Chromium; Debian 12 and Ubuntu 25.10/26.04 AMD64 | 3 |
| Bash `5.2.15`, revision `1` | Debian 12; build/runtime; AMD64/ARM64 | 4 |
| Bash `5.2.37`, revision `1` | Debian 13; build/runtime; AMD64/ARM64 | 4 |
| Bash `5.3.9`, revision `1` | Ubuntu 26.04; build/runtime; AMD64/ARM64 | 4 |

## Design Goals

| Accepted goal | Disposition and implementation evidence |
| --- | --- |
| Tool version independent of OS release | Implemented: `toolrequest` normalizes tool versions; release manifests and exact target leaves carry separate identities. |
| Exact OS/architecture support without duplicated whole definitions | Implemented: `toolcatalog/definitions/*/releases/*/targets/` reference shared records; `integration_cases.go` derives exact support tuples. |
| Maintainable version families | Implemented: bounded first-party imports and single-parent authoring extension in `toolcatalog/authoring_loader.go`; canonical records contain neither construct. |
| Mixed native, upstream and binding acquisition | Implemented: Java pinned JDK payload, Playwright wheel/browser payloads plus APT package sets, and native Bash package sets; the qualified cases exercise the ordinary build paths. |
| Unsupported combinations fail before acquisition | Implemented: `toolcatalog/candidates.go`, `solver.go`, graph validation and portable-tool planning reject unmatched targets, contexts and features. |
| Deterministic, lockable resolution without upstream installers | Implemented: shared `portabletool` contracts, canonical records, selected closures, `portable_tool_plan.go`, provider request bindings and `deploy/build_lock_portable_test.go`. |
| Ordinary blueprint integration across base images | Implemented: manifest-derived fixtures and validation profiles drive complete Java, Playwright and Bash native suites in `dockerdeploy/portable_tool_case_integration_test.go`. |
| Open to other managers and distributions | Implemented extension boundary: manager-typed records and resolver primitives; the embedded support claim remains the qualified APT tuples above. Other managers require their own delivery and evidence. |

## Non-goals Preserved

| Accepted non-goal | Disposition |
| --- | --- |
| Repository transport, trust, publication or lifecycle | Deferred to `REPOSITORY_DESIGN.md`; the embedded bridge supplies none of those protocols. |
| Third-party definition code or arbitrary installers | Excluded: strict data contracts, reviewed materializers and fixed local-source build protocols; no executable definition hooks. |
| Literal package-name normalization | Excluded: native package names remain manager and target specific. |
| Architecture support from OCI build capability alone | Excluded: only exact native execution and current matching records contribute support. Java and Playwright ARM64 remain deferred. |
| General/runtime inheritance, templating or overrides | Excluded from canonical records; only the bounded authoring-time imports and conflict-only extension are resolved before canonical emission. |
| Every future tool category | Deferred: the embedded catalog remains Java, Playwright and the specified Bash releases. |
| Asciinema tool, OmegaFlow adapter or recorder in Reploy | Excluded: PTD-33 replaced recorder fixtures with direct terminal/browser proof; consumer-owned capture and adapter qualification remain separate responsibilities. |

## Migration Dispositions

| Design migration step | Disposition and evidence |
| --- | --- |
| 1. Replace flat loader; no compatibility reader | Complete: strict shared `portabletool` record contracts and `toolcatalog` reference graph are the active loader. No unreleased flat-format fallback is retained. |
| 2. Split Java/Playwright shared metadata | Complete: tool, revisioned release manifest and release contract are separate records under `toolcatalog/definitions/`. |
| 3. Separate Playwright Python contract and wheel | Complete: binding contract and architecture-specific binding artifact records; the three runtime cases exercise the Python binding. |
| 4. Separate browser payload, source provenance and native libraries | Complete: selection payload records, manifest-owned source mapping and manager-typed package sets; source locators do not replace byte verification. |
| 5. Exact target leaves | Complete: small Debian/Ubuntu leaves select explicit shared references and exact supported context/feature tuples. |
| 6. Canonical recipe requests and identity propagation | Complete: scalar/structured forms in `dockerdeploy/local_source_recipe.go`; full canonical demands in `source_builder_portable_tools.go`; catalog-backed environment, provider and build-lock evidence. ADR 0001 now describes that behavior. |
| 7. Explicit Java payload instead of distribution default | Complete: Java `21` selects the exact Temurin `21.0.12+8` payload; no default-Java-package fallback. |
| 8. Preserve verified Debian/Ubuntu behavior | Complete for the exact qualified matrices above; successful native records match current selected closures. Generic APT capability does not expand portable-tool support. |
| 9. ARM64 only after complete native contract | Complete for six Bash ARM64 build/runtime cases; Java and Playwright ARM64 remain deferred. |
| 10. Retire flat definitions, aggregate identity and obsolete tests | Complete: one explicit record hierarchy, selected-closure identities and current graph/identity tests remain. Negative tests rejecting obsolete input are contract protection, not compatibility support. |

## Security and Identity Boundaries

`internal/portabletool` owns strict record decoding and local validation;
catalog composition and provider/lock authorization remain separate consumers.
References are explicit and digest checked. Authoring paths cannot escape the
approved first-party tree, and imports/extensions do not survive canonical
composition. Selected-closure identity excludes unrelated targets and mutable
source locators, while the release manifest authorizes each acquisition source.

Provider-store acquisition verifies exact byte size and SHA-256 before use.
Archive materialization validates paths, links and collisions; definition
records cannot supply arbitrary installer commands. Failed validation cannot
publish a passing support record. Validation records remain external to tool
definition identity and retain original executor output.

Runtime Bash provenance joins the exact lock, selected closure and immutable
image with package-owned executable and consumer `/bin/bash` observations.
Both paths resolve to the same regular terminal bytes. An alias does not gain
invented APT ownership. OmegaFlow decides adapter compatibility from this
handoff; it is not an Envoy or recorder qualification claim.

The direct Linux PTY and Docker terminal/browser proofs retain raw/canonical
input, binary ordering, resize, Ctrl-C, success/partial-failure finalization,
acknowledgement and owned cleanup. Removing the recorder dependency did not
remove the generic terminal attachment or broker APIs.

## Remaining Deferrals and Approval Checks

Repository consumption/publication, TUF and publisher lifecycle policy remain
separate work. Additional Java versions or runtime Java, Playwright bindings
or browsers, and Java/Playwright ARM64 need explicit definition, acquisition,
materialization and native execution evidence before support can expand.
ADR 0001 continues to defer general-purpose build environments, shared/remote
build caching, central recipe governance and artifact publication. Consumer
recording formats, adapter qualification and compatibility tables remain
consumer-owned.

The seven bullets under `Explicit Deferrals` in the implementation plan map to
the following evidence statements:

| Explicit plan deferral | Cutover evidence statement |
| --- | --- |
| Repository transport, federation, TUF, signing and lifecycle | Repository consumption/publication, TUF and publisher lifecycle policy remain separate work. |
| Java runtime context or versions beyond the accepted JDK 21 release | Additional Java versions or runtime Java require their own definition, acquisition, materialization and native execution evidence. |
| Playwright bindings other than Python or selections other than Chromium | Other Playwright bindings, browser selections or browsers remain deferred until their exact support evidence exists. |
| Java and Playwright ARM64, or targets outside the accepted initial matrices | Java/Playwright ARM64 and any additional target require complete artifact, package and native execution proof before support expands. |
| Package managers and distributions beyond the accepted initial APT targets | Other package managers and distributions remain outside the qualified support claim until separately evidenced. |
| Third-party definition code, installer scripts, runtime/general-purpose inheritance or broader authoring behavior | These remain excluded; the accepted boundary stays with reviewed data contracts, fixed protocols, local imports and conflict-only single-parent extension. |
| Automatic definition updates independent of a Reploy binary release | This remains deferred; embedded definitions stay coupled to Reploy binary releases. |

PTD-29 approval requires the complete local Go, Docker, packaging, CLI, release,
documentation and hygiene checks, a clean current-chat review plus fresh
independent review, and all eight `ci.yml` jobs successful at its exact PR head.
The predecessor evidence above qualifies unchanged tool definitions; it does
not substitute for those new-head approval checks.
