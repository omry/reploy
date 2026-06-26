---
sidebar_position: 7
---

# Release

The release version lives in the repository `VERSION` file. The native binary
embeds that value, and Python wheel metadata reads the same source of truth.

Build release distributions locally:

```bash
tools/build_release_dists --clean
```

Publish from GitHub Actions after CI is green:

```bash
gh workflow run publish.yml --ref main
```

The publish workflow reads `VERSION`, builds active Linux wheels, publishes the
wheel artifacts to PyPI, and creates a GitHub Release containing only direct
binary assets:

- `reploy-linux-amd64`
- `reploy-linux-arm64`

The PyPI project must be configured for GitHub trusted publishing for
`omry/reploy` and `.github/workflows/publish.yml`.
