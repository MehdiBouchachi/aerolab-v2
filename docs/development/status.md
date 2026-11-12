# Implementation status (honest checklist)

Legend: ✅ done and tested here · 🟡 done, partially verified · ⛔ not started · ❓ blocked on hardware/decision

## Phase 0 / 1
| Item | State | Evidence |
|---|---|---|
| Monorepo skeleton, go.work, Makefile, CI files, .env.example | ✅ (CI workflow files never executed on GitHub) | `make lint test-race test-integration` |
| MQTT contract: schemas + Go validators + fixtures | ✅ | `contracts/mqtt` tests (schema≡Go on 43+ fixtures) |
| REST contract: OpenAPI ≡ router | ✅ | `TestRoutesMatchOpenAPIExactly` |
| SQLite WAL, migrations, durable receipts only after COMMIT | ✅ | writer tests incl. real SQLITE_FULL; mutation-checked |
| Dedup `(device,boot,seq)`, gaps/duplicates after outages | ✅ | integration: broker outage, SIGKILL of edge, disk floor |
| Incident lifecycle (dedupe, ack≠resolve, stale) | ✅ heartbeat-driven | incidents tests + A08 integration |
| Command state machine (PENDING≠APPLIED, ≤3 sends, idempotent, conflict, expiry, reconcile) | ✅ | commands tests, A05 integration |
| Local auth (argon2id, hashed sessions, rate limit incl. per-IP), RBAC matrix, last-admin guard | ✅ | httpapi/auth tests |
| SSE hub + stream handler | ✅ | sse tests (slow-client drop, caps), handler test, live-events integration test |
| Reboot / clock honesty (A24) | ✅ | integration: new boot_id, no fabricated timestamps, RTC rows labelled |
| **Phase 2 web dashboard** (login, overview, unit, history, programme editor, alerts, admin) | ✅ for these screens | 14 vitest tests; driven in headless Chromium against the live simulated stack (login, programme Pending→Applied, live critical alert, acknowledge, mobile 375px no overflow) |
| Crop batches/journal/harvest, exports, calibration, pairing | ⛔ | no backend yet |
| Broker auth + per-device ACL (A25) | ✅ vs Mosquitto 2.0.18 (plain MQTT, no TLS) | `TestA25_…` |
| ESP32 simulator, 10/20 devices | ✅ | sim tests, integration, `make load` |
| Load: RSS/CPU/recovery | 🟡 measured on x86 sandbox only | `docs/performance/phase1-dev-machine.md` |
| Pi systemd/Mosquitto/install files | 🟡 written, **never run on a Pi** | – |
| Docker Compose dev stack | ⛔ removed (would not have worked); `make dev-stack` is the path | – |

## Known gaps / limitations in Phase 1 (deliberate or discovered)
* A fault that starts **and** ends entirely during an outage is stored in `device_events` but does not retro-create an
  incident (incidents follow heartbeats). Needs an in-order event→incident reconciler.
* History/exports: history API exists; CSV/JSON **export** endpoint, crop batches/journal/harvest, calibration,
  pairing, backups endpoint, `/storage` retention jobs beyond the hourly delete: not implemented.
* Edge does not probe the WAN: `site_wan_reachable` / `cloud_api_reachable` are reported `null` (unknown).
* TLS for MQTT/HTTP not implemented; plain MQTT only on loopback in dev. Mosquitto password file is managed out of band.
* `GET /units` runs ~2 queries per unit (fine to the 100-unit page cap; measured p95 ≈ 4 ms at 20 units).
* Login returns the bearer token in the JSON body as well as an HttpOnly cookie (for mobile); XSS exposure of that
  body is a Phase 2 review item.
* The dev admin password is random and written to `.dev/edge.env`; no password-reset/recovery workflow yet.
* Not tested: clock-skew with an RTC-equipped board end-to-end, SSE slow-client drop, 24 h soak, Windows.

## Not started
Phase 2 remainder (crop/harvest/export + their API), Phase 3 ESP-IDF firmware (needs pin map, sensor models, relay polarity, approved
limits - spec §22), Phase 4 cloud API/sync sender, Phase 5 mobile, Phase 6 Pi hardening/soak tests, A14/A15/A16 real-hardware tests.

## Bug found and fixed while extending the tests
The simulator's network link did not notice a locally dropped socket (only the 10 s MQTT keep-alive would), so after a simulated reset or Wi-Fi drop a device could keep publishing into a dead connection. Fixed; covered by the reboot and Wi-Fi integration tests.
