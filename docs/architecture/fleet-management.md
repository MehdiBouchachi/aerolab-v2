# Fleet management: many Pis, many units, one crop per unit, no installer visits

Status: implemented and tested on a development machine (real Mosquitto, real PostgreSQL, headless
browser). **Nothing here has run on a Raspberry Pi or an ESP32 yet.**

## What a grower can do without calling anyone

| Step | Where | What happens |
|---|---|---|
| Create an account | Cloud → *Create an account* | Account + organisation (Personal, Farm/company, Public institution). Only when the operator sets `AEROLAB_CLOUD_SIGNUP=open`. |
| Connect a Pi | Cloud → *Connect a Pi* → code; Pi → *Administration → Connect to cloud* | The Pi exchanges a one-time code for its own token and starts syncing. No env files, no restart. |
| Reconnect a Pi | same, with a new code | A Pi disconnected or reinstalled with the same site id is re-linked to **its** organisation and the old token is revoked. Another organisation is refused. |
| Add a unit | Pi → *Overview → Add a unit* | The Pi creates the controller identity and broker password, and shows a setup code once (QR + text). |
| Set up the controller | phone → `AeroLab-Setup-XXXX` Wi-Fi → `http://192.168.4.1` | Farm Wi-Fi + setup code are saved; the controller restarts, joins and appears on the Pi. |
| Say what grows where | Pi → *Crop profiles*; unit page → *Crop profile* | Each unit is watched against its own crop's ranges (tomato unit ≠ lettuce unit). |
| Replace / lose a controller | unit page → *Create a new setup code* | Old password stops working; the new board uses the new code. |
| Retire a unit | unit page → *Retire* (type the name) | Credential withdrawn, unit leaves lists and the cloud; history kept. |
| See every farm | Cloud → *Your farms* | Every Pi you may see, by organisation: units reporting, needing attention, alerts, crops. |

## Crop profiles (no values ship)

A profile is: name, crop, variety, notes, **who supplied the numbers** (required), per-reading
`{min,max}` for any reading name the controller reports, and optionally an irrigation programme.
AeroLab contains **no crop values**: every number is entered by the grower or their agronomist.

* Alerts: 3 consecutive valid readings outside a bound open an advisory incident
  (`RANGE_HIGH:<reading>` / `RANGE_LOW:<reading>`, warning); 3 inside close it. Missing or invalid
  readings never count (sensor faults are reported by the controller itself). Controller heartbeats
  never reconcile these away; removing a bound closes its alert. They run on the Pi, offline.
* Pump limits: a profile's programme is validated like any programme and is only ever sent as a
  normal versioned programme request (PENDING ≠ APPLIED). The controller's own hard cap and the
  reservoir interlock are unchanged by any profile.
* Edits use optimistic versioning (`expected_version`, 409 if someone else saved first).
* Profiles live on the Pi (offline-first). Copying a profile to another Pi is manual today; a cloud
  profile library pushed to Pis is a possible next step.

## Units: how the Pi issues controller credentials

The broker keeps its password file + pattern ACL (topics scoped by `%u`, the controller's own id),
so no ACL change is needed per unit. The edge (`AEROLAB_BROKER_PASSWD_FILE`) rewrites **only the lines
of devices it issued** (`devices.broker_password_hash`), keeps every other line byte for byte, replaces
the file atomically with the old owner and mode, and asks the broker to reload: SIGHUP to
`AEROLAB_BROKER_PID_FILE` (dev, containers) or the systemd path unit `aerolab-broker-reload.path` on
the Pi (the edge needs no privileges over the broker). Only the Mosquitto `$7$` hash is stored; the
password exists in the setup code shown once. The edge re-syncs the file at startup.

Mosquitto does not drop an already connected client whose password was removed; the edge also
ignores everything from a retired device. The setup code format is pinned by
`contracts/examples/setup/valid.code`, checked by both the Pi (Go) and the firmware (C host tests).

Without `AEROLAB_BROKER_PASSWD_FILE` the Pi refuses with `BROKER_NOT_MANAGED` and units are added by
the installer, as before.

## Tenancy for B2C, B2B and B2G

One model for all three: **organisation → sites (Pis) → units**, and authorization on the **site**
membership (Admin / Operator / Viewer), enforced server side.

* Organisation kind `PERSONAL` / `BUSINESS` / `GOVERNMENT` changes wording and defaults, never the
  security model.
* Organisation `OWNER`s become Admin of every Pi connected afterwards; `MEMBER`s see nothing until a
  site admin gives them a role (a cooperative or ministry grants each person only the farms they work on).
* Sign-up is **closed by default** (`AEROLAB_CLOUD_SIGNUP=closed`): government and enterprise
  deployments create accounts themselves; a consumer service sets it to `open`.
* Every Pi still works with no cloud at all; the cloud is a view, never a control dependency.
* Claim codes: 12 characters from an unambiguous alphabet, single use, 24 h, hash stored, attempts
  rate-limited per address; site creation, grants, code consumption and token issuance are one
  transaction.

## Controller setup page (firmware)

While NVS lacks identity, broker credentials or farm Wi-Fi, the controller opens `AeroLab-Setup-XXXX`
(WPA2 if `CONFIG_AEROLAB_SETUP_AP_PASSWORD` is set, otherwise **open**: bench use only) and serves a form
at `http://192.168.4.1`. Decoding/validation is portable C (`setup_code.c`, host-tested with ASan/UBSan
including truncation and hostile bundles). All keys are written with one NVS commit, then it restarts
and joins the farm Wi-Fi with backoff. An unconfigured controller has no programme, so it never
irrigates while in setup. **Compiled with ESP-IDF v5.3, never run on hardware.**

## Not done yet

* Re-entering setup on a configured controller needs a physical trigger (button/jumper): a hardware fact.
* Mobile app: no fleet / crop / add-unit screens yet (the web dashboard works on phones).
* Readings recorded before a Pi is connected are not back-filled to the cloud.
* Organisation invitations need the invitee to have an account (no email delivery).
* TLS for MQTT and for the Pi's dashboard.
