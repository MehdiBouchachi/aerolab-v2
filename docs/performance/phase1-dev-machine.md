# Phase 1 measured results - development sandbox (NOT a Raspberry Pi)

Machine: 4 vCPU Intel Xeon @ 2.10 GHz, amd64, 16 GB RAM. Real Mosquitto 2.0.18, real `edge` binary
(plain `go build`), simulated controllers in-process. Reproduce: `make load`. Raw output below.

## How to read this honestly

* **These do not validate the Pi 5 / 2 GB budget.** Different CPU architecture, memory and storage. Only a run
  on the real Pi (`docs/performance/` should get a new dated file) can do that.
* RSS: edge peaked at **~58 MiB** and Mosquitto at **~8 MiB** with 20 simulated devices, far below the proposed
  256 / 64 MiB caps. Not measured: 24-hour growth, multiple browsers, large history queries, a populated
  30-day database (the DB in these runs is ~1 MB).
* "Accelerated stress" offers 30-60 msg/s (spec baseline: 2.2 / 4.3 msg/s). The real-rate run is the 4.3 msg/s case.
* **"Pi outage -> all records durable" (~14.5 s) is dominated by the test's 15 s device receipt-timeout**
  (records published while the edge was down were lost at the broker and are resent after that timeout). It
  measures the resend policy, not edge throughput. In the real-rate run the figure (0 s) is vacuous:
  almost nothing accumulated in 20 s.
* The "Ingest" counters restart with the edge process (it was SIGKILLed mid-test), so they only count the
  post-restart replay; the no-gaps/no-duplicates check is done against SQLite itself.
* Single run per configuration; no variance estimate. Edge CPU (1-2% of one core) is a sandbox figure; the Pi's
  cores are slower.

## Raw results

### ACCELERATED STRESS (state 0.5 s = 10x, snapshot 1 s = 60x the spec rate) - 10 simulated controllers (data_source=SIMULATOR)

| Metric | Value |
|---|---|
| Machine | 4 CPUs, Intel(R) Xeon(R) Processor @ 2.10GHz, amd64 |
| Offered MQTT load | state every 500ms + snapshot every 1s per device = **30.0 msg/s** (spec baseline: 2.2 msg/s for this N) |
| Steady-state window | 45 s |
| Telemetry rows persisted in window | 460 (10.2/s) |
| Edge RSS (start / end / peak HWM) | 65.1 / 54.7 / **56.0 MiB** (budget: 192 soft, 256 hard) |
| Mosquitto RSS (start / end / peak HWM) | 8.2 / 8.2 / **8.2 MiB** (budget 64) |
| Edge CPU (avg over window) | 1.5% of one core |
| Mosquitto CPU (avg over window) | 0.3% of one core |
| API p50 / p95 / max (GET units, N=10) | 2.013ms / 2.76ms / 5.653ms |
| 20 s Pi outage (SIGKILL) -> all units ONLINE | 500ms |
| Pi outage -> every buffered record durable (no gaps/dups) | 14.5s |
| Edge peak RSS after replay (HWM) | 56.0 MiB |
| DB size / WAL | 0.8 MiB / 4.0 MiB |
| Ingest: persisted / dup-ignored / rejected / queue-drops / commit errors | 350 / 0 / 0 / 0 / 0 |
| Last batch commit | 876 us |

### ACCELERATED STRESS (state 0.5 s = 10x, snapshot 1 s = 60x the spec rate) - 20 simulated controllers (data_source=SIMULATOR)

| Metric | Value |
|---|---|
| Machine | 4 CPUs, Intel(R) Xeon(R) Processor @ 2.10GHz, amd64 |
| Offered MQTT load | state every 500ms + snapshot every 1s per device = **60.0 msg/s** (spec baseline: 4.3 msg/s for this N) |
| Steady-state window | 45 s |
| Telemetry rows persisted in window | 920 (20.4/s) |
| Edge RSS (start / end / peak HWM) | 65.5 / 24.9 / **57.7 MiB** (budget: 192 soft, 256 hard) |
| Mosquitto RSS (start / end / peak HWM) | 8.2 / 8.2 / **8.2 MiB** (budget 64) |
| Edge CPU (avg over window) | 2.2% of one core |
| Mosquitto CPU (avg over window) | 0.4% of one core |
| API p50 / p95 / max (GET units, N=20) | 2.937ms / 4.325ms / 6.852ms |
| 20 s Pi outage (SIGKILL) -> all units ONLINE | 500ms |
| Pi outage -> every buffered record durable (no gaps/dups) | 14.6s |
| Edge peak RSS after replay (HWM) | 57.7 MiB |
| DB size / WAL | 1.4 MiB / 4.1 MiB |
| Ingest: persisted / dup-ignored / rejected / queue-drops / commit errors | 700 / 0 / 0 / 0 / 0 |
| Last batch commit | 1418 us |

### REAL-RATE (state 5 s, snapshot 60 s) - 20 simulated controllers (data_source=SIMULATOR)

| Metric | Value |
|---|---|
| Machine | 4 CPUs, Intel(R) Xeon(R) Processor @ 2.10GHz, amd64 |
| Offered MQTT load | state every 5s + snapshot every 1m0s per device = **4.3 msg/s** (spec baseline: 4.3 msg/s for this N) |
| Steady-state window | 120 s |
| Telemetry rows persisted in window | 60 (0.5/s) |
| Edge RSS (start / end / peak HWM) | 54.6 / 20.5 / **54.3 MiB** (budget: 192 soft, 256 hard) |
| Mosquitto RSS (start / end / peak HWM) | 8.2 / 8.2 / **8.2 MiB** (budget 64) |
| Edge CPU (avg over window) | 0.9% of one core |
| Mosquitto CPU (avg over window) | 0.1% of one core |
| API p50 / p95 / max (GET units, N=20) | 2.525ms / 3.664ms / 4.841ms |
| 20 s Pi outage (SIGKILL) -> all units ONLINE | 5s |
| Pi outage -> every buffered record durable (no gaps/dups) | 0s |
| Edge peak RSS after replay (HWM) | 54.3 MiB |
| DB size / WAL | 0.0 MiB / 1.7 MiB |
| Ingest: persisted / dup-ignored / rejected / queue-drops / commit errors | 0 / 0 / 0 / 0 / 0 |
| Last batch commit | 0 us |

