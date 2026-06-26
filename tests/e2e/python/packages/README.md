# Python E2E Packages

This directory contains reusable fixture packages for Reploy CLI smoke tests.

- `smoke-blueprint` is the Reploy blueprint fixture.
- `smoke-suite` is the primary Python app package installed by default.
- `smoke-imap` is an add-on package selected by `reploy bundle add --name imap`.

The blueprint declares both packages as `app.provider.local_sources` so tests can
exercise local-source projection without publishing wheels.
