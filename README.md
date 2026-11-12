# AeroLab V2

Autonomous, locally operable aeroponic monitoring and control. **The ESP32 controls the physical unit;
the Raspberry Pi manages the site; the cloud is optional; web and mobile are interfaces, not control
dependencies.** Full specification: `AEROLAB_V2_FULL_PROJECT_ARCHITECTURE.md` (supplied separately;
keep a copy at the repo root). This README says what actually exists today.

> **Current state: Phases 1-5 implemented to the extent stated below; NOTHING has run on a Raspberry Pi, an ESP32 or a phone.**
> Backend, cloud and the web UI are tested end to end on a development machine with a real Mosquitto and PostgreSQL.
> Firmware logic is tested on the host (the ESP-IDF glue is unbuilt). The mobile app's logic is tested; its UI is only typechecked. Nothing here has run on a Raspberry Pi
> or an ESP32. See [`docs/development/status.md`](docs/development/status.md) for the honest checklist.

## Status by phase
| Phase | State |
|---|---|
| 1 Edge + simulator + contracts | ✅ tested (unit, race, real-broker integration, load on x86) |
| 2 Web dashboard (local) | ✅ screens listed in `apps/web/README.md`; crop/harvest/export **not built** |
| 3 ESP32 firmware | 🟡 portable core tested on host (gcc + ASan/UBSan); ESP-IDF glue **never built**; outputs disabled until wiring is verified |
| 4 Cloud API + sync | ✅ tested against real PostgreSQL + a real Pi-side worker, incl. WAN loss/recovery and remote requests; not deployed anywhere |
| 5 Mobile | 🟡 logic tested incl. against a real edge; UI typechecked only, never run on a device |
| 6 Hardening/ops | ⛔ not done: no Pi install test, no 24 h soak, no TLS, no backups/restore |

## What works now

* A Go **edge service** (one binary, pure Go SQLite) that ingests MQTT from a real Mosquitto broker,
  stores telemetry/events durably, **acknowledges a record only after the SQLite commit**, runs the incident
  lifecycle, tracks controller freshness, sends versioned programme commands (PENDING ≠ APPLIED), and serves
  an authenticated, role-checked REST + SSE API with offline local accounts.
* A **multi-device ESP32 simulator** (2, 10 or 20+ controllers, each with its own identity, boot, programme,
  journal and reconnect jitter) that keeps its irrigation cycle running with no network at all.
* **Shared contracts**: MQTT JSON Schemas + Go validators + fixtures (kept in agreement by a test), and an
  OpenAPI file kept in lock-step with the router by a test.
* Per-device broker credentials and a topic ACL verified against a real Mosquitto 2.0.18.

## Quick start (Linux/macOS; Windows: use WSL)

Requirements: Go ≥ 1.24, `mosquitto` (`apt install mosquitto`). No cloud account, no internet needed.

```bash
make test               # contracts + edge + cloud (needs PostgreSQL binaries) + simulator
make fw-test            # firmware portable core (gcc) + Go validates its output
make web-test           # dashboard typecheck + tests (pnpm)
make test-race          # same with the race detector
make test-integration   # real Mosquitto + real edge binary (SIGKILL-able) + simulated fleet (~1 min)
make web-build         # build the dashboard (needs pnpm)
make dev-stack          # 10 SIMULATED controllers + broker + edge + dashboard at http://127.0.0.1:18080/ ; Ctrl-C to stop
docker compose up --build   # ONE command, no toolchain needed: same demo in Docker at http://localhost:8080 (see infrastructure/development/README.md)
```

`make dev-stack` prints the API URL and where the random dev admin password lives (`.dev/edge.env`,
git-ignored). Then, for example:

```bash
. .dev/edge.env
TOKEN=$(curl -s localhost:18080/api/v1/local/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$AEROLAB_BOOTSTRAP_ADMIN_PASSWORD\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
curl -s localhost:18080/api/v1/sites/site-sim-01/units -H "Authorization: Bearer $TOKEN"
# give unit 4 its first programme (generic test timings, NOT agronomic values) - returns SENT, not APPLIED:
curl -s localhost:18080/api/v1/units/unit-sim-004/program-requests -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"expected_program_version":0,"program":{"mode":"TIMED_CYCLE","on_duration_seconds":10,"off_duration_seconds":290,"max_continuous_on_seconds":20,"require_reservoir_ok":true}}'
# make unit 2 run out of (simulated) water:
curl -X POST 127.0.0.1:19090/devices/esp32-sim-002/faults -d '{"low_water":true}'
```

Other targets: `make build`, `make pi-arm64` (cross-compile for the Pi, pure Go), `make lint`,
`make load` (10 and 20 simulated devices; opt-in, minutes). More: [`simulator/README.md`](simulator/README.md),
[`services/edge-api/README.md`](services/edge-api/README.md), [`docs/architecture/mqtt-contract.md`](docs/architecture/mqtt-contract.md).

PowerShell (no `make`): the targets are thin wrappers over `go build|test` run inside each module directory
(`contracts/mqtt`, `services/edge-api`, `simulator`, `tests`); `go run ./cmd/aerolab-seed -out ../.dev` from `simulator/`
provisions the dev site. The shell script `scripts/dev-stack.sh` has no PowerShell twin yet.

## Layout (spec §16; folders marked ▫ are reserved, with no code yet)

```
contracts/mqtt        Go module + JSON Schemas for the device wire contract      (done, tested)
contracts/examples    valid / invalid / invalid-semantic fixtures                (done)
contracts/openapi     edge.v1.yaml                                               (done, parity-tested)
services/edge-api     Pi service: storage, telemetry, incidents, commands, auth, httpapi, sse
simulator             esp32/ (device model), seed/ (dev provisioning), cmd/
tests/integration     real-broker scenarios + opt-in load test
infrastructure/       raspberry-pi/ (systemd + Mosquitto config: UNVALIDATED on a Pi), development/
docs/                 decisions/ (ADR), architecture/, development/status.md, performance/
apps/web (dashboard; local + cloud builds)  apps/mobile (Expo)  services/cloud-api (Go + PostgreSQL)  firmware/esp32  contracts/sync
▫ packages (shared TS packages: not extracted yet; web and mobile each carry small copies of the API types)  ▫ infrastructure/cloud (no Compose/Docker yet)
```

## Safety and honesty rules baked into the code

* Irrigation authority and interlocks live on the controller; the Pi cannot start a pump, and manual pump
  tests are not a command type at all. `require_reservoir_ok:false` is rejected everywhere.
* **No tomato irrigation values exist in this repository.** Programme numbers in tests/examples are generic
  protocol-range test data.
* Missing readings are `null` + `missing`, never `0`. A stale controller is shown `UNKNOWN_STALE`, never as
  "Running". A monotonic-only controller never invents a timestamp. Simulator data is always labelled
  `SIMULATOR`, and a production edge (`AEROLAB_ALLOW_SIMULATED_DATA=false`) refuses it.
* Secrets: none in Git. Dev credentials are random, generated into `.dev/`. `.env.example` has no values.

## Measured results

See [`docs/performance/phase1-dev-machine.md`](docs/performance/phase1-dev-machine.md). Those figures come
from a development sandbox (x86-64), **not** a Raspberry Pi 5, and say nothing yet about the Pi's 2 GB budget.
