# Edge API (Raspberry Pi) - one Go binary

`cmd/edge` · `serve` | `migrate` | `create-user`. Config is environment-only (`/.env.example`).

| Package | Responsibility |
|---|---|
| `storage` | SQLite (WAL, `synchronous=FULL`, 1 writer + read pool), embedded migrations, disk stats, free-space floor |
| `telemetry` | MQTT ingest (validate → authorise → bounded queue) and the single batch writer that sends receipts **after COMMIT** |
| `devices` | registry of provisioned devices; latest state in memory only for *registered* devices; connection = freshness, not retained presence |
| `incidents` | lifecycle, one active row per (unit, code), plain-language catalogue |
| `commands` | PENDING→SENT→APPLIED/REJECTED/CONFLICT/UNKNOWN/EXPIRED, ≤3 sends, idempotency, reconciliation from heartbeat |
| `auth`, `roles` | argon2id local accounts, hashed opaque sessions, rate limit, the RBAC matrix |
| `httpapi` | REST + SSE; every route authenticated and role-checked; strict JSON; security headers |
| `sse` | bounded fan-out, slow clients are dropped |
| `sync` | outbox retention (sender arrives with the cloud API, Phase 4) |
| `app` | wiring + graceful shutdown + retention worker |

API contract: `contracts/openapi/edge.v1.yaml` (a test fails if the router and the file differ).
Cross-compile for the Pi: `make pi-arm64` (pure Go, no CGO).
