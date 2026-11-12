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
mosquitto -c "$D/mosquitto/mosquitto.conf" &
sleep 1
su-exec aerolab edge serve &
sleep 2
su-exec aerolab aerolab-sim -creds "$D/devices.json" -heartbeat "$HEARTBEAT" -telemetry "$TELEMETRY" &
echo "=============================================================="
echo " AeroLab DEMO stack is up - ALL DATA IS SIMULATED"
echo " dashboard : http://localhost:${AEROLAB_PUBLISHED_PORT:-8080}/"
echo " login     : $AEROLAB_BOOTSTRAP_ADMIN_USER / $AEROLAB_BOOTSTRAP_ADMIN_PASSWORD  (random, stored in the volume)"
echo "=============================================================="
# exit (so Docker restarts us) as soon as any process dies
while kill -0 $(jobs -p) 2>/dev/null; do wait -n 2>/dev/null && break || break; done
echo "a component exited; stopping" >&2
kill $(jobs -p) 2>/dev/null || true
exit 1
