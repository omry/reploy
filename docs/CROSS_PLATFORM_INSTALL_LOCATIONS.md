---
status: Superseded
updated: 2026-07-11
summary: Superseded by the blueprint environment model.
---

# Cross-Platform Install Locations

This design has been folded into
[`BLUEPRINT_ENVIRONMENT_MODEL.md`](BLUEPRINT_ENVIRONMENT_MODEL.md), under
**Install Scope and Locations**.

The environment model is now authoritative for explicit install scope,
host/backend/scope-aware target defaults, blueprint overrides, semantic host
variables, validation, and system `run_as` behavior. It also updates the schema
from top-level `install` and `app.id` to `environment.install` and
`environment.id`.
