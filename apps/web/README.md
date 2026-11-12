# Web dashboard (React + TypeScript + Vite)

Built once into `dist/` and served by the Pi's edge binary (`AEROLAB_STATIC_DIR`); no CDN, fonts or remote
assets, so it works with no internet. All data comes from the edge API + SSE stream; the server enforces roles.

```bash
make web-build        # -> apps/web/dist (then `make dev-stack` serves it at the edge URL)
make web-test         # typecheck + vitest (14 tests)
cd apps/web && pnpm dev   # Vite dev server, proxies /api to the edge (AEROLAB_EDGE=http://127.0.0.1:18080)
```

Screens: Login · Overview (summary tiles, active alerts, unit cards) · Unit detail (status, readings with quality,
history charts with explicit gaps, applied programme, programme editor Draft→Review→Pending→Applied/Rejected/
Conflict/Unknown/Expired) · Alerts (acknowledge ≠ resolve) · Administration (storage health, controllers, local
accounts, audit). Every page shows source (LOCAL), live-stream state, freshness and SIMULATED labels.

Not built yet: crop batches/journal/harvest, CSV/JSON export, calibration records, dark-mode toggle (follows the OS),
i18n. The backend endpoints for crops/exports do not exist yet, so no placeholder screens were added.
