#!/usr/bin/env bash
# Starts a complete DEV site: Mosquitto (per-device auth + ACL) + edge + simulated
# controllers. Everything it produces is SIMULATED. Requires `make dev-provision`.
set -euo pipefail
cd "$(dirname "$0")/.."
[ -f .dev/edge.env ] || { echo "run 'make dev-provision' first"; exit 1; }
command -v mosquitto >/dev/null || { echo "mosquitto not installed (apt install mosquitto)"; exit 1; }
set -a; . .dev/edge.env; set +a
export AEROLAB_HTTP_ADDR="${HTTP_ADDR:-127.0.0.1:18080}"
# serve the built web app (make web-build) the same way the Pi will
[ -d apps/web/dist ] && export AEROLAB_STATIC_DIR="$PWD/apps/web/dist"
pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; }
trap cleanup EXIT INT TERM

mosquitto -c .dev/mosquitto/mosquitto.conf >.dev/mosquitto.log 2>&1 & pids+=($!)
sleep 1
./bin/edge serve >.dev/edge.log 2>&1 & pids+=($!)
sleep 1.5
./bin/aerolab-sim -creds .dev/devices.json -heartbeat "${HEARTBEAT:-5s}" -telemetry "${TELEMETRY:-60s}" >.dev/sim.log 2>&1 & pids+=($!)

echo "AeroLab DEV stack is up (ALL DATA IS SIMULATED)"
echo "  web UI     : http://${AEROLAB_HTTP_ADDR}/   ${AEROLAB_STATIC_DIR:+(built app)}${AEROLAB_STATIC_DIR:-(run: make web-build for the dashboard)}"
echo "  edge API   : http://${AEROLAB_HTTP_ADDR}   (login: ${AEROLAB_BOOTSTRAP_ADMIN_USER} / see .dev/edge.env)"
echo "  fault API  : http://127.0.0.1:19090/devices   (loopback; POST /devices/{id}/faults ...)"
echo "  logs       : .dev/{mosquitto,edge,sim}.log"
wait
