# Verified-build JSON

Run `reploy verify --dir STAGE --format json` (or `--format=json`) to obtain
verified facts about the current staged build. Omit `--format` for the existing
text output. The only explicit format is `json`; missing, unknown or repeated
format options are usage errors (exit 2). The option is specific to `verify`.

JSON mode performs the same full audit, including reachable artifact body
hashing, image inspection, provider validation, runtime/account/policy checks
and temporary-resource cleanup. It does not use cache verification stamps
instead of body hashing. The report uses facts from that audit while the
existing deployment operation lock is held. It does not reread unlocked state.

After successful audit, report encoding and unlock, stdout contains one UTF-8
JSON object and one newline. Diagnostics go to stderr. Audit, report, size or
unlock failures return exit 1 without a success object; cancellation retains
exit 130. A failed output write can leave partial stdout. Consumers must require
exit 0 and one complete object. The maximum serialized output is 1 MiB,
including the newline. Overflow fails before publication, without truncation.
This limit does not constrain text verification.

## Closed version-1 schema

All listed fields are required. Arrays are present even when empty. Object
member order and whitespace are insignificant. The producer emits no duplicate
keys. A schema addition or semantic change requires a new `schema_version`.
Digests are `sha256:` followed by 64 lowercase hexadecimal characters. Sizes
and numeric user/group IDs are canonical decimal **strings**. The schema
version and check counts are JSON integers; policy flags are booleans.

| Object | Members |
| --- | --- |
| top level | `schema` = `reploy:verified-build`, `schema_version` = `1`, `environment`, `platform`, `build`, `inputs`, `base`, `image`, `artifacts`, `validation`, `runtime`, `checks` |
| `platform` | string `os`, `architecture`, `variant` (empty when unspecified) |
| `build` | digest `lock_digest`, string `image_reference`, string `local_image_id` |
| `inputs` | digests `blueprint_digest`, `resolved_request_digest` |
| `base` | string `immutable_reference`, string `local_image_id`, digest or null `manifest_digest`, ordered digest array `rootfs_diff_ids` |
| `image` | ordered digest array `rootfs_diff_ids`, including all final image layers |
| `artifacts` | arrays `provider_bundles`, `portable_acquisitions` with entry shapes below |
| `validation` | strings `schema`, `runtime_policy_schema`; digests `record_digest`, `runtime_policy_digest` |
| `runtime` | `account`, `startup_verifier`, `planned_sandbox` |
| `runtime.account` | strings `schema`, `name`, `uid`, `gid`, `home` |
| `runtime.startup_verifier` | strings `schema`, `recipe_version`, `path`, `size`; digest `artifact` |
| `runtime.planned_sandbox` | strings `uid`, `gid`, `temporary_home`; numeric-ascending unique decimal string array `supplementary_gids`; boolean `read_only_root`; objects `network`, `kernel` |
| `runtime.planned_sandbox.network` | policy strings `public`, `local`, `ambiguous` |
| `runtime.planned_sandbox.kernel` | booleans `drop_all_capabilities`, `no_new_privileges`, `privileged`; string `seccomp_profile`; string arrays `host_namespaces`, `host_devices` |
| `checks` | integer `provider_store_objects`, `images`, `commands` |

Provider bundle entries contain string `node_id`, `logical_path`, `kind`, `size`
and digest `sha256`. They are sorted by `(node_id, logical_path)`.

Portable acquisition entries contain string `scope`, `tool`, `artifact_id`,
`logical_path`, `kind`, `size` and digests `artifact_digest`, `sha256`. They are
sorted by `(scope, tool, artifact_id, artifact_digest)`. Distinct valid owners
remain distinct even when they share a logical path or identical blob. Duplicate
complete ownership keys fail reporting. Base-preinstalled packages without an
acquired artifact do not create inventory entries.

`local_image_id` is an engine-native local handle. It is not a promise of a raw
OCI config digest across image stores. `record_digest` is the already verified
PrefixValidation identity. It binds its existing versioned rootfs, profile,
policy and declared executable evidence; it is not an inventory of every image
file or an independent signature.

## Snapshot boundary and confidentiality

`planned_sandbox` describes the reconstructed staged-runtime baseline, not
command-specific overlays, mapped-identity launches or live kernel attestation.
The report is a snapshot, not a lease or an atomic guarantee for a later launch.
A same-user process or local-engine administrator can change stage/image state
between commands. Consumers must apply their own ownership and launch contract.

The report excludes raw environment variables, labels, provider payloads,
credential values, source locators/acquisition outcomes, host mount paths and
artifact bytes. Public names (including environment/account/artifact names),
image references and policy identifiers remain visible and can themselves be
sensitive. The format does not automatically redact those names.

The report does not promise cross-build equality. Lock/image identities and
validation records can contain per-build inputs. Image creation/history
reproducibility and the separate harness identity blocker remain unresolved.
