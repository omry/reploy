# Private milestone E conformance

Run from the Reploy checkout with a native Go toolchain. Use a new output
directory under an existing private evidence parent:

```sh
python3 tools/conformance/multiidentity/e_conformance.py --output-dir /absolute/new/evidence
python3 -m unittest discover -s tools/conformance/multiidentity -p test_e_conformance.py
```

The runner creates no engine resources. Its existing production-startup tests
use disposable Linux user/network namespaces and bounded child processes.
They need `cc`, `unshare`, `newuidmap`, `newgidmap` and delegated subordinate
IDs. Missing prerequisites remain visible test skips, not qualification.
Mapped runtime support remains limited by the approved Linux/Podman cohort;
running the portable fixture suites elsewhere does not expand that cohort.

`report.json` binds source bytes and file modes, Go configuration and compiler
and linker hashes, host kernel/architecture, fixture helpers and the pinned
OCI seccomp seed. It checks source/toolchain stability after collection.
Each suite has its exact command, elapsed time, JSON event log digest and
complete terminal test statuses. Private fresh journeys also emit their exact
closed plan, including profile, maps, immutable image, role, generation,
runtime/kernel/network policies and stream mode. These are fixture facts.
The actual production exec tests identify their compiled source/toolchain
and verify the production exec tail; this is mechanism evidence, not a
retained launched-image binary or live-engine receipt.

The integrated private journey repeats workload, application, runtime-test and
hook cases three times for each of `exact` and `bounded`. It checks one release,
separate application streams, owned completion/cleanup and retained mapping
history. Invalid authority and interrupted proof/release paths check absence of
application effects. Existing suites cover native atomic admission, canonical
policy, capabilities, private/high/concurrent descriptors, occupied refusal,
replacement refusal, cancellation and cleanup ownership.

Collection exit zero means the required private suites passed, their fresh
matrix is complete, no selected test failed and the source/toolchain stayed
stable. A partially skipped production-exec suite remains `partial`, with
each gap listed; it cannot become a full kernel qualification. A missing
required test, failed test, timeout, malformed log or changed source fails
collection. Existing evidence directories are never overwritten.

No result from this runner qualifies a live engine or public mapped execution.
`mapped_runtime_qualified` is always false. Production F code/mount/network
observations, an exact engine/OCI-policy receipt, unresolved kernel tuples and
G activation remain separate gates. Working replacement and occupied exec
remain refused. The older `conformance.py` Podman probe is a distinct lower
level experiment and cannot stand in for this integrated E evidence.
The opt-in `TestMappedPodmanCreateLiveV1` is explicitly excluded from this
private runner, even when its enabling environment is set. The exact exclusion
is retained in the report and command; live-engine qualification stays open.
