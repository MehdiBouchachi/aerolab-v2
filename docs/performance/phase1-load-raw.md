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

