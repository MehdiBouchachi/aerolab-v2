#!/bin/sh
# Cloud API: migrations run on every start. Optional first-run bootstrap of an admin + site.
set -eu
if [ -n "${AEROLAB_CLOUD_ADMIN_EMAIL:-}" ] && [ -n "${AEROLAB_NEW_PASSWORD:-}" ]; then
  cloud create-user -email "$AEROLAB_CLOUD_ADMIN_EMAIL" 2>/dev/null || true
  if [ -n "${AEROLAB_CLOUD_SITE_ID:-}" ]; then
    cloud create-site -id "$AEROLAB_CLOUD_SITE_ID" -name "${AEROLAB_CLOUD_SITE_NAME:-$AEROLAB_CLOUD_SITE_ID}" -admin "$AEROLAB_CLOUD_ADMIN_EMAIL" 2>/dev/null || true
  fi
fi
exec cloud serve
