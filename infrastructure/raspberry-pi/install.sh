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
install -m 0755 "$BIN" /opt/aerolab/bin/aerolab-edge
HERE="$(cd "$(dirname "$0")" && pwd)"
install -m 0644 "$HERE/systemd/aerolab-edge.service" /etc/systemd/system/
install -d /etc/systemd/system/mosquitto.service.d
install -m 0644 "$HERE/systemd/mosquitto.service.d/resources.conf" /etc/systemd/system/mosquitto.service.d/
install -m 0644 "$HERE/mosquitto/aerolab.conf" /etc/mosquitto/conf.d/aerolab.conf
if [ ! -f /etc/aerolab/edge.env ]; then
  install -m 0600 -o root -g aerolab /dev/null /etc/aerolab/edge.env
  echo "Created empty /etc/aerolab/edge.env - fill it from .env.example (secrets stay out of Git)."
fi
systemctl daemon-reload
echo "Next: create /etc/mosquitto/aerolab.passwd + aerolab.acl, edit /etc/aerolab/edge.env,"
echo "      run: sudo -u aerolab AEROLAB_NEW_PASSWORD=... /opt/aerolab/bin/aerolab-edge create-user -username admin -role ADMIN"
echo "      then: systemctl enable --now mosquitto aerolab-edge"
