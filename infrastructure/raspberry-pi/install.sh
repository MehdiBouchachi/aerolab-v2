#!/usr/bin/env bash
# Pi installer sketch. STATUS: UNTESTED on real hardware - review before running.
# Installs the prebuilt arm64 binary + unit files. Does NOT enable any pump output
# (outputs live on the ESP32s) and creates NO default credentials.
set -euo pipefail
[ "$(id -u)" -eq 0 ] || { echo "run as root"; exit 1; }
BIN="${1:?usage: install.sh path/to/edge-linux-arm64}"
id aerolab >/dev/null 2>&1 || useradd --system --home /var/lib/aerolab --shell /usr/sbin/nologin aerolab
install -d -o root -g root /opt/aerolab/bin /etc/aerolab
install -d -o aerolab -g aerolab -m 0750 /var/lib/aerolab /var/lib/aerolab/backups
# Controller credentials: written by the edge (aerolab), read by the broker (group mosquitto).
# setgid keeps the group on every file the edge replaces.
install -d -o aerolab -g mosquitto -m 2750 /var/lib/aerolab-broker
[ -f /var/lib/aerolab-broker/passwd ] || install -o aerolab -g mosquitto -m 0640 /dev/null /var/lib/aerolab-broker/passwd
install -m 0755 "$BIN" /opt/aerolab/bin/aerolab-edge
HERE="$(cd "$(dirname "$0")" && pwd)"
install -m 0644 "$HERE/systemd/aerolab-edge.service" "$HERE/systemd/aerolab-broker-reload.path" "$HERE/systemd/aerolab-broker-reload.service" /etc/systemd/system/
install -d /etc/systemd/system/mosquitto.service.d
install -m 0644 "$HERE/systemd/mosquitto.service.d/resources.conf" /etc/systemd/system/mosquitto.service.d/
install -m 0644 "$HERE/mosquitto/aerolab.conf" /etc/mosquitto/conf.d/aerolab.conf
if [ ! -f /etc/aerolab/edge.env ]; then
  install -m 0600 -o root -g aerolab /dev/null /etc/aerolab/edge.env
  echo "Created empty /etc/aerolab/edge.env - fill it from .env.example (secrets stay out of Git)."
fi
systemctl daemon-reload
echo "Next: add the edge's own MQTT login to /var/lib/aerolab-broker/passwd (mosquitto_passwd), create /etc/mosquitto/aerolab.acl,"
echo "      edit /etc/aerolab/edge.env (AEROLAB_BROKER_PASSWD_FILE=/var/lib/aerolab-broker/passwd lets growers add units from the dashboard),"
echo "      run: sudo -u aerolab AEROLAB_NEW_PASSWORD=... /opt/aerolab/bin/aerolab-edge create-user -username admin -role ADMIN"
echo "      then: systemctl enable --now mosquitto aerolab-edge aerolab-broker-reload.path"
