# ADR 0001 - Phase 0/1 foundations

Status: accepted · Date: 2026-11-12

## Repository inspection
The repository was empty (no commits, no code, no migrations). Nothing existed to preserve or migrate.
The spec and build prompt were supplied as external files, not committed.

## Decisions
1. **Go for edge + simulator, pure-Go SQLite (`modernc.org/sqlite`).** No CGO → trivial `GOARCH=arm64`
   cross-compile for the Pi and one static binary. Trade-off: slower than CGO SQLite; acceptable at
   ≤ 28 800 snapshots/day, and to be measured on the real Pi.
2. **One shared Go module for the wire contract** (`contracts/mqtt`) imported by both the edge and the
   simulator, plus language-neutral JSON Schemas that a test keeps in agreement with it. Firmware (C) and
   TypeScript (Phase 2) will validate against the schemas/fixtures, not against Go.
3. **Single writer connection (`MaxOpenConns=1`) + 4 read connections**, WAL, `synchronous=FULL`.
   Bulk ingest is batched (≤ 200 records/tx) so FULL durability stays affordable.
4. **Free-space floor applies only to bulk ingest** (`IngestTx`), keeping a reserve so logins,
   acknowledgements and commands still work and the operator can see the storage warning.
5. **Heartbeat-driven incidents.** Device-reported fault sets are authoritative for incident open/resolve;
   `DEVICE_STALE` is edge-owned. Fault *events* are stored durably but do not (yet) retro-create incidents
   for faults that began and ended entirely inside an outage (see status.md, known gap).
6. **Dev broker = the real Mosquitto**, not an in-process fake, so ACL/auth behaviour is genuinely tested.
7. **Simulator is a first-class, separate module** and labels everything `SIMULATOR`.
8. TypeScript tooling (pnpm lockfile, Vite, Expo) is deferred until Phase 2 so no empty/fake JS packages exist.

## Rejected
* MQTT broker embedded in the edge process (would couple the broker's memory/crash domain to the API and
  does not match the Mosquitto budget in the spec).
* Replaying commands after reconnect (explicitly forbidden by the spec).
