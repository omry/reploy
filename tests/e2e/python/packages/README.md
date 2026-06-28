# Python E2E Packages

This directory contains reusable fixture packages for Reploy CLI smoke tests.

- `smoke-blueprint` is the Reploy blueprint fixture.
- `smoke-suite` is the primary Python app package installed by default.
- `smoke-imap` is an add-on package selected by `reploy bundle add --name imap`.
- `git-source-app` is a tiny source-checkout fixture with an in-tree Reploy
  blueprint for `git:` ref staging tests.

The smoke blueprint declares both smoke packages as
`app.provider.local_sources` so tests can exercise local-source projection
without publishing wheels.
