# Python E2E Packages

This directory contains reusable fixture packages for Reploy CLI smoke tests.

- `smoke-blueprint` is the Reploy blueprint fixture.
- `smoke-suite` is the primary Python app package installed by default.
- `smoke-imap` is an add-on selected by
  `reploy bundle add application/imap`.
- `git-source-app` is a tiny source-checkout fixture with an in-tree Reploy
  blueprint for `git:` ref staging tests.

The smoke runner writes a staging-only `package-overrides.yaml` after staging.
It maps both distributions to these local projects so the provider tests can
exercise explicit, demand-driven local overrides without publishing wheels.
The published smoke blueprint contains no development checkout paths.
