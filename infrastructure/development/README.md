# Development infrastructure

## One command (Docker)

```
docker compose up --build            # demo site: Mosquitto + edge + 10 SIMULATED controllers
docker compose --profile cloud up --build   # plus PostgreSQL + cloud API/dashboard
```

* Dashboard: http://localhost:8080 - the login (random, generated on first start and kept in the
  `aerolab-data` volume) is printed in the container log: `docker compose logs aerolab`.
* Cloud (profile `cloud`): http://localhost:8090, admin `admin@example.com` / `change-me-please-12`
  (override with `AEROLAB_CLOUD_ADMIN_EMAIL` / `AEROLAB_CLOUD_ADMIN_PASSWORD`; dev defaults only).
  The edge token for linking a site is printed once in `docker compose logs cloud`.
* Options: `DEVICES=20`, `AEROLAB_PORT`, `AEROLAB_CLOUD_PORT`, `POSTGRES_PASSWORD`.
* Reset everything: `docker compose --profile cloud down -v`.
* Everything shown is SIMULATED (`data_source=SIMULATOR`). Secrets are generated into the volume, never into
  the image or Git. The demo container runs broker + edge + simulator together for convenience;
  the Pi install (`infrastructure/raspberry-pi`) is the production layout.
* Windows: run `docker compose` from a shell whose path uses `C:\` upper case (Compose 5 + buildx
  otherwise fails with "additional privileges requested").

## Without Docker

`make dev-stack` runs the host's `mosquitto`, the `edge` binary and N simulated controllers with
generated credentials in the git-ignored `.dev/` directory.
