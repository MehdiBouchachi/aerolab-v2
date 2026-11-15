#!/bin/sh
# DEMO site in one container: Mosquitto (per-device auth + ACL) + edge + SIMULATED controllers.
# First start generates random credentials into the /data volume (never baked into the image).
set -eu
D=/data
if [ ! -f "$D/edge.env" ]; then
  echo "first start: provisioning $DEVICES SIMULATED devices"
  aerolab-seed -out "$D" -devices "$DEVICES" -port 1883 >/dev/null
  chown aerolab "$D" "$D/devices.json"
fi
set -a; . "$D/edge.env"; set +a
mosquitto -c "$D/mosquitto/mosquitto.conf" & BROKER=$!
sleep 1
su-exec aerolab edge serve & EDGE=$!
sleep 2
su-exec aerolab aerolab-sim -creds "$D/devices.json" -heartbeat "$HEARTBEAT" -telemetry "$TELEMETRY" & SIM=$!
echo "=============================================================="
echo " AeroLab DEMO stack is up - ALL DATA IS SIMULATED"
echo " dashboard : http://localhost:${AEROLAB_PUBLISHED_PORT:-8080}/"
echo " login     : $AEROLAB_BOOTSTRAP_ADMIN_USER / $AEROLAB_BOOTSTRAP_ADMIN_PASSWORD  (random, stored in the volume)"
echo "=============================================================="
# Exit (so Docker restarts the container) as soon as ANY component dies. Plain POSIX polling:
# `wait -n` is a bash extension and is not reliable in Alpine's /bin/sh.
stop() { kill "$SIM" "$EDGE" "$BROKER" 2>/dev/null || true; }
trap 'stop; exit 0' TERM INT
while kill -0 "$BROKER" 2>/dev/null && kill -0 "$EDGE" 2>/dev/null && kill -0 "$SIM" 2>/dev/null; do sleep 2; done
echo "a component exited; stopping the stack" >&2
stop
exit 1
