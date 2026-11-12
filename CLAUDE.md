# AeroLab V2 - agent instructions

Source of truth: `AEROLAB_V2_FULL_PROJECT_ARCHITECTURE.md` (put it at the repo root; it was supplied
out-of-band). Current state and gaps: `docs/development/status.md`.

Non-negotiables (from the spec): the ESP32 owns irrigation and safety; nothing from Pi/cloud/UI may be
required for an approved cycle; `PENDING` is never `APPLIED`; a receipt is sent only after the SQLite
commit; never invent readings, timestamps, tomato irrigation values, or hardware facts; simulator data
must always carry `data_source=SIMULATOR`; no secrets in Git; server-side RBAC only.

Commands: `make test`, `make test-race`, `make test-integration` (needs `mosquitto`), `make lint`,
`make dev-stack`. Wire contracts: `contracts/mqtt` (Go validators + JSON Schemas + fixtures) and
`contracts/openapi/edge.v1.yaml` (a test fails if the router and the spec drift).
Report only measurements that were actually run.
