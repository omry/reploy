# Reploy Python Package

This package distributes the native Reploy command-line binary as
platform-specific Python wheels.

Building or installing this package from a Reploy source checkout rebuilds the
selected native binary from that checkout, even when `dist/<target>/reploy`
already exists. Release tooling can select an exact prebuilt binary explicitly
with `REPLOY_BINARY`; an arbitrary existing `dist` binary is never treated as
an implicit package input.

See the repository README for usage and development instructions.
