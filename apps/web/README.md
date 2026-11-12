# Web application (React + Vite)

**Status: NOT STARTED (Phase 2).** No code exists here yet; this folder only reserves the structure from the spec.

Planned: the local dashboard (login, overview, units, history charts, incidents, programme editor with
Draft → Pending → Applied states, crop journal, users, exports), built once and served by the Pi, with a
second cloud build. It consumes the edge API in `contracts/openapi/edge.v1.yaml` and the SSE stream
(`/api/v1/sites/{siteId}/events`). All JS/CSS/fonts must ship locally (no CDN) so it works with no internet.
