---
sidebar_position: 7
---

# Release

The release version lives in the repository `VERSION` file. The native binary
embeds that value, and Python wheel metadata reads the same source of truth.

Reploy uses Changie fragments for release notes. Before a final release, batch
the unreleased fragments into `CHANGELOG.md`:

```bash
changie batch "$(tr -d '[:space:]' < VERSION)"
changie merge
```

Commit the updated `CHANGELOG.md`, the new `.changes/<version>.md`, and the
removed unreleased fragments before publishing.

Dev releases do not consume fragments. Their GitHub Release notes include a raw
list of the current unreleased fragments since the last final release.

Build release distributions locally:

```bash
tools/build_release_dists --clean
```

Publish from GitHub Actions after CI is green:

```bash
gh workflow run publish.yml --ref main
```

The workflow runs CI and integration checks, then builds and validates the
release artifacts. It uploads the wheels, binaries, checksums, and release
notes as a `release-<commit SHA>` workflow artifact before requesting approval.
Open the workflow run, inspect its commit and artifacts, and use **Review
deployments** to approve the `release` environment. Rejecting the deployment
leaves the artifacts unpublished. This approval is required for dev releases
as well as final releases. After approval, the workflow publishes those same
artifacts to PyPI and GitHub without rebuilding them.

The repository's GitHub **Settings > Environments > release** environment must
have required reviewers configured. Referencing an environment in the workflow
does not itself require approval; GitHub enforces its protection rules outside
the repository. Configure the release maintainer as a required reviewer, allow
self-review if that maintainer also starts the workflow, and disable
administrator bypass so publication requires the approval step.

The publish workflow reads `VERSION`, builds active Linux, macOS, and Windows
wheels, publishes the wheel artifacts to PyPI, and creates a GitHub Release
containing direct binary assets plus checksums:

- `reploy-linux-amd64`
- `reploy-linux-arm64`
- `reploy-darwin-amd64`
- `reploy-darwin-arm64`
- `reploy-windows-amd64.exe`
- `reploy-windows-arm64.exe`
- `SHA256SUMS`

Initial macOS binaries may be unsigned and unnotarized. Developer ID signing
and notarization are separate release-hardening work. Initial Windows binaries
may be unsigned until Authenticode signing is added.

The PyPI project must be configured for GitHub trusted publishing for
`omry/reploy` and `.github/workflows/publish.yml`.
If the trusted publisher specifies an environment, it must match `release`.
