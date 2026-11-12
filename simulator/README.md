# AeroLab ESP32 simulator

A behavioural model of the ESP32 controller (`esp32/`) that speaks the real MQTT contract to a real
broker. **Everything it emits is `data_source=SIMULATOR`; none of it is a measurement.**

| File | Models firmware component |
|---|---|
| `esp32/device.go` | controller state machine (BOOT_SAFE → SELF_CHECK → NEEDS_SETUP/RUNNING/DEGRADED/PROTECTED_STOP), boot ids, journaling |
| `esp32/commands.go` | command_handler: validation, dedup (last 32), version check, hardware cap, atomic persist, session nonce |
| `esp32/journal.go` | message_buffer: bounded write-ahead log, freed **only** by a Pi receipt, critical-first replay in batches of 20 |
| `esp32/nvs.go` | config_store: atomic programme + previous-revision fallback; corrupt/over-limit programme ⇒ NEEDS_SETUP |
| `esp32/network.go` | mqtt_client: LWT, session nonce, exponential backoff + jitter, presence/state first on reconnect |
| `esp32/sensors.go` | synthetic readings (no light sensor ⇒ `missing`, never 0); injectable faults |
| `seed/` | dev provisioning: random per-device credentials, PBKDF2 hashes for Mosquitto, static `%u` ACL |

Key behaviours (all unit-tested without any network, then re-tested end-to-end in `tests/integration`):
the pump cycle runs from the monotonic clock only; an unconfigured controller never irrigates; low water
(or an invalid level sensor) forces PROTECTED_STOP instantly and leaving it needs a *local* confirmation;
invalid pH/EC only degrades; `require_reservoir_ok:false` is refused; a programme over the board's hard
ON limit is refused; a restart changes `boot_id`; a monotonic-only clock never invents a timestamp.

```bash
make dev-provision DEVICES=10        # writes .dev/ (git-ignored) with random secrets
./bin/aerolab-sim -creds .dev/devices.json -devices 10 -heartbeat 5s -telemetry 60s
# fault injection (loopback only):
curl -X POST 127.0.0.1:19090/devices/esp32-sim-002/faults -d '{"low_water":true}'
curl -X POST 127.0.0.1:19090/devices/esp32-sim-002/recover      # physical confirmation, after the fault is fixed
curl -X POST 127.0.0.1:19090/network -d '{"up":false}'           # all devices lose broker/Wi-Fi
curl 127.0.0.1:19090/devices                                     # status incl. pump_cycles, journal, reconnects
```
