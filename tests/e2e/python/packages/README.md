# Python E2E Packages

This directory contains reusable fixture packages for Reploy CLI smoke tests.

- `smoke-blueprint` is the Reploy blueprint fixture.
- `smoke-suite` is the primary Python app package installed by default.
- `smoke-imap` is an add-on selected by
  `reploy bundle add application/imap`.
- `git-source-app` is a tiny source-checkout fixture with an in-tree Reploy
  blueprint for `git:` ref staging tests.

The smoke blueprint declares both distributions under
`environment.workspace.packages.python` so tests can exercise explicit local
workspace overrides without publishing wheels.
