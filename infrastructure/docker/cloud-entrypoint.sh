#!/bin/sh
# Cloud API: migrations run on every start. Optional first-run bootstrap of an admin + site.
# No default password exists anywhere: if AEROLAB_CLOUD_ADMIN_PASSWORD is not set, a random one is generated
# on first start and printed ONCE to the container log.
set -eu
if [ -n "${AEROLAB_CLOUD_ADMIN_EMAIL:-}" ]; then
  if [ -z "${AEROLAB_NEW_PASSWORD:-}" ]; then
    AEROLAB_NEW_PASSWORD="$(head -c 18 /dev/urandom | base64 | tr -d '/+=' | cut -c1-20)"
    export AEROLAB_NEW_PASSWORD
    GENERATED=1
  fi
  if cloud create-user -email "$AEROLAB_CLOUD_ADMIN_EMAIL" >/dev/null 2>&1; then
    if [ "${GENERATED:-0}" = 1 ]; then
      echo "=============================================================="
      echo " cloud admin created: $AEROLAB_CLOUD_ADMIN_EMAIL / $AEROLAB_NEW_PASSWORD"
      echo " (random, shown once - change it or set AEROLAB_CLOUD_ADMIN_PASSWORD)"
      echo "=============================================================="
    fi
  fi  # already exists on later starts: nothing printed, nothing changed
  if [ -n "${AEROLAB_CLOUD_SITE_ID:-}" ]; then
    # prints the per-site edge token once, on first creation only
    cloud create-site -id "$AEROLAB_CLOUD_SITE_ID" -name "${AEROLAB_CLOUD_SITE_NAME:-$AEROLAB_CLOUD_SITE_ID}" -admin "$AEROLAB_CLOUD_ADMIN_EMAIL" 2>/dev/null || true
  fi
fi
exec cloud serve
