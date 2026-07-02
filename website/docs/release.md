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

The publish workflow reads `VERSION`, builds active Linux and macOS wheels,
publishes the wheel artifacts to PyPI, and creates a GitHub Release containing
direct binary assets plus checksums:

- `reploy-linux-amd64`
- `reploy-linux-arm64`
- `reploy-darwin-amd64`
- `reploy-darwin-arm64`
- `SHA256SUMS`

Initial macOS binaries may be unsigned and unnotarized. Developer ID signing
and notarization are separate release-hardening work.

The PyPI project must be configured for GitHub trusted publishing for
`omry/reploy` and `.github/workflows/publish.yml`.
