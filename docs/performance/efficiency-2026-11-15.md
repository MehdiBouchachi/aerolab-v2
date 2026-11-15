# Efficiency pass - 2026-11-15 (measured on the x86 development sandbox, NOT a Raspberry Pi)

Go benchmarks in `services/edge-api` (`go test -bench . -benchmem -count 5 ./internal/telemetry/ ./internal/httpapi/`),
20 units x 1 day of 1-minute snapshots (28 800 rows). Raw output: `bench-2026-11-15-before.txt` / `-after.txt`.

| Path | Before | After | Change |
|---|---|---|---|
| `GET /sites/{id}/units` (20 units) - polled by web + phone | ~1.32 ms, 2380 allocs/op | ~0.69 ms, 1930 allocs/op | ~48% faster |
| Sync state snapshot (20 units) | ~1.19 ms | ~0.67 ms (incl. new fingerprint) | ~44% faster |
| Ingest commit, 100 records (`synchronous=FULL`) | ~4.3-4.6 ms | unchanged | prepared statements tried and **reverted**: no gain outside run-to-run noise; the cost is the fsync that backs durable receipts |
| Web dashboard initial JS | 316.6 kB / 101.6 kB gzip | 277.4 kB / 88.4 kB gzip | -13% gzip; unit page + admin (QR library) load on demand |
| Docker web stage install | 568 packages / ~646 MB `node_modules` | 150 packages / ~194 MB | measured by replaying the stage's pnpm commands |

What changed:
* Unit views are built with **two queries per page** (incident counts + latest snapshot per unit via a correlated
  `LIMIT 1` seek on `ix_telemetry_unit_time`) instead of two per unit. `EXPLAIN QUERY PLAN` confirms index seeks only.
* The sync worker uploads the state snapshot only when its fingerprint changes (clock-driven fields such as a
  reading's age are excluded) or every 60 s, and always after a failed upload. The batch is still sent every cycle
  as the cloud's liveness signal. How much this saves end-to-end depends on how often units change; it was not
  measured end-to-end.
