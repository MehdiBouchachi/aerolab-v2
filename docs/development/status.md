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

## Phases 3-5 (added later)
| Item | State | Evidence |
|---|---|---|
| Cloud API (Go) + PostgreSQL schema/migrations | ✅ | 14 DB-backed tests incl. tenant isolation matrix, mutation-checked |
| Idempotent ingest, stale snapshot guard, incident ordering | ✅ | cloud tests; poisoned items isolated per item |
| Per-site edge credentials (hashed, rotatable, revocable) | ✅ | cloud tests |
| Remote programme intents (PENDING_SITE→PENDING_DEVICE→APPLIED only on ESP32 confirm; stale/expired never run) | ✅ | cloud + Pi-worker + end-to-end tests (A06, A21) |
| Edge sync worker (strict priority outbox, backoff+jitter, bounded batches, local opt-in `AEROLAB_REMOTE_PROGRAMS`) | ✅ | 9 worker tests; 5 end-to-end scenarios incl. WAN cut/restore (A01, A12, A18, A19) |
| Cloud shows STALE when the Pi stops syncing | ✅ | end-to-end + browser check |
| Same web app on the cloud origin (`VITE_SOURCE=cloud`) | ✅ | driven in headless Chromium |
| Firmware portable core | ✅ host | 697 checks, ASan/UBSan, fixture parity with Go, power-cut simulation, 3 safety mutants caught |
| Firmware ESP-IDF glue | ⛔ unbuilt | needs ESP-IDF + device |
| Site identity (Ed25519) + pairing codes on the Pi | ✅ | Go tests + cross-language vector |
| Mobile logic (pairing parse, identity, decision table, cache/source isolation) | ✅ | 34 tests incl. real-edge e2e |
| Mobile UI / device adapters | 🟡 typechecked only | never run |

### Known gaps in Phases 3-5
* Cloud: no remote incident acknowledge, no crop/member-invite flows, no organisation management beyond site members, no email/push,
  no rate limiting beyond login, no TLS termination (expects a reverse proxy), no Dockerfile/Compose, programme *contents* are not synced
  (only the controller-reported version), SSE not implemented (polling).
* Edge sync: outbox rows for telemetry are written only when sync is enabled; a site enabled after months offline starts from "now"
  (no backfill of older SQLite history). Incident updates and device events are enqueued; crop records do not exist yet.
* Remote requests: `DEVICE_OFFLINE` at the Pi is reported back as REJECTED (not retried until expiry).
* Firmware: Wi-Fi bring-up and credential provisioning are not written; journal index is not rebuilt after a reboot; only the level input
  has a driver; no OTA; RTC/NTP absent (monotonic only).
* Mobile: no push, no SSE, no crop entry, no local TLS trust story; Expo module versions pinned from npm tags (could not run `expo install`).
* Not done at all (Phase 6): Pi install on real hardware, 24 h soak, backups/restore scripts, TLS everywhere, Terraform, release pipeline.

## Bugs found by the tests while building these phases
`os.Args` aliasing in both `main` functions (subcommand clobbered; never exercised before the cloud harness called `create-user`);
firmware decoder rejected pretty-printed receipt arrays (found by fixture parity); firmware test emitted a truncated buffer (found by the Go validator);
cloud advertised an `ack_incident` capability it had no endpoint for.


## Bug found and fixed while extending the tests
The simulator's network link did not notice a locally dropped socket (only the 10 s MQTT keep-alive would), so after a simulated reset or Wi-Fi drop a device could keep publishing into a dead connection. Fixed; covered by the reboot and Wi-Fi integration tests.
