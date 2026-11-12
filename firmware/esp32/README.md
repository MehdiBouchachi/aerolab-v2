# ESP32 firmware (ESP-IDF)

**Status: portable core TESTED ON THE HOST; ESP-IDF glue WRITTEN BUT NEVER BUILT; nothing has run on an ESP32.**
No ESP-IDF toolchain was available while this was written, so `idf.py build` has not been run. Do not flash
this to hardware connected to a pump without the steps in "Before energising anything".

## What is real and tested (`make fw-test`, plain gcc, strict warnings + AddressSanitizer/UBSan)
| Component | Tested behaviour |
|---|---|
| `controller` + `safety` | BOOT_SAFE → SELF_CHECK → NEEDS_SETUP/RUNNING; no programme ⇒ no irrigation; cycle starts in the OFF phase; low water or an invalid level sensor ⇒ PROTECTED_STOP **within one tick**; leaving it needs the fault cleared **and** a local confirmation (not reachable from the network); pH/EC only degrade; pump ON bounded by programme, `max_continuous_on` and the board hard cap |
| `protocol` | Replays the **same fixtures** as the Go validators/JSON Schemas (`contracts/examples`); 20 000 random mutations + every truncation + garbage never crash it; oversize/over-deep payloads rejected; manual pump test is not a command type; encoders write `null` (never 0) for missing readings and never invent a timestamp; **Go validates the C encoders' output** (`tests/contracts_firmware_test.go`) |
| `config_store` | Two-slot A/B with CRC32 + generation: a power cut mid-write leaves the previous programme; bit rot, invalid or over-cap programmes are discarded (device stays NEEDS_SETUP); protection-off programmes are refused |
| `message_buffer` | Bounded; evicts oldest non-critical and **counts** the gap; frees only on a Pi receipt for the matching `boot_id`; critical-first replay in batches of 20 with an in-flight cap and resend after the receipt timeout |
| `command_handler` | Same rules as the simulator: idempotency (last 32), session-nonce binding, expected version, hardware cap, persist-then-swap, persist failure changes nothing, last applied command survives a reboot |

Mutation checks were run on the safety code: disabling the low-water stop, auto-releasing PROTECTED_STOP, and
trusting a bad CRC each make the suite fail.

## What is written but NOT built or run (needs ESP-IDF and a device)
`main/app_main.cpp` (tasks), `actuators` (GPIO + independent hard ON cap), `sensors` (only the level input;
other sensors report `missing`), `mqtt_client/src/net.c` (esp-mqtt, LWT, per-connection nonce, backoff+jitter),
`config_store_nvs.c`, `journal_littlefs.c` (note: uses the SPIFFS API on a `spiffs`-type partition; **rebuilding the
journal index after a reboot is not implemented**), Wi-Fi bring-up and provisioning (not written: needs decisions
about how credentials get onto the device). No OTA (out of MVP scope).

## Before energising anything (blocking facts only the project owner can supply, spec §22)
1. ESP32 model/flash size; pump relay polarity and power; reservoir level input polarity.
2. Real pin numbers and the **maximum safe pump ON time** → fill `components/board_profile/src/board_profile.c`.
3. Which interlocks are essential on this rig; real pH/EC/temperature/light sensor models and drivers.
4. Then, and only then, set `CONFIG_AEROLAB_WIRING_VERIFIED=y` after a water-only bench test.

Until then `board_outputs_permitted()` is false: the pump pin is **never driven**, the level input fails safe
(⇒ PROTECTED_STOP), and `ctl_set_program` refuses every programme because the hard cap is 0.

## Known design points to validate on hardware
Control-task jitter vs the 50 ms tick and watchdog; flash wear of the journal over a 6 h outage; NVS commit latency
(it blocks only the command task, never the control tick, because the handler locks only around the live swap);
heap use with TLS; reconnect behaviour of esp-mqtt with `disable_auto_reconnect`; clock quality (no RTC ⇒ monotonic only).
