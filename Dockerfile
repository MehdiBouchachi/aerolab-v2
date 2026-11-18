# syntax=docker/dockerfile:1
# AeroLab V2 - one image, three roles (see docker-compose.yml):
#   target "stack"  : DEMO site = Mosquitto + edge + SIMULATED controllers (all data labelled SIMULATOR)
#   target "cloud"  : cloud API + cloud dashboard (needs PostgreSQL)

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
RUN npm install -g pnpm@10.28.0
WORKDIR /src
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY apps/web ./apps/web
COPY apps/mobile/package.json ./apps/mobile/package.json
# install ONLY the web app's dependencies from the lockfile (the workspace also contains the Expo app:
# installing everything pulled ~568 packages / ~650 MB instead of ~150 / ~190 MB)
RUN pnpm install --frozen-lockfile --filter @aerolab/web \
 && cd apps/web && pnpm build
# ^ one dashboard build for both the Pi and the cloud: it asks the server that served it (GET /api/v1/meta)

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS gobuild
ARG TARGETOS TARGETARCH
ENV CGO_ENABLED=0 GOFLAGS=-trimpath GOOS=$TARGETOS GOARCH=$TARGETARCH
WORKDIR /src
COPY go.work go.work.sum ./
COPY contracts ./contracts
COPY services ./services
COPY simulator ./simulator
COPY tests ./tests
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    mkdir /out \
 && (cd services/edge-api && go build -ldflags "-s -w" -o /out/edge ./cmd/edge) \
 && (cd services/cloud-api && go build -ldflags "-s -w" -o /out/cloud ./cmd/cloud) \
 && (cd simulator && go build -ldflags "-s -w" -o /out/aerolab-sim ./cmd/aerolab-sim \
                  && go build -ldflags "-s -w" -o /out/aerolab-seed ./cmd/aerolab-seed)

FROM alpine:3.22 AS stack
RUN apk add --no-cache mosquitto su-exec tini wget \
 && adduser -D -H -u 10001 aerolab \
 && mkdir /data && chown aerolab /data
COPY --from=gobuild /out/edge /out/aerolab-sim /out/aerolab-seed /usr/local/bin/
COPY --from=web /src/apps/web/dist /srv/web
COPY infrastructure/docker/stack-entrypoint.sh /usr/local/bin/stack-entrypoint.sh
ENV DEVICES=10 HEARTBEAT=5s TELEMETRY=60s AEROLAB_HTTP_ADDR=0.0.0.0:8080 AEROLAB_STATIC_DIR=/srv/web
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/sbin/tini","--","/usr/local/bin/stack-entrypoint.sh"]

FROM alpine:3.22 AS cloud
RUN apk add --no-cache su-exec tini wget && adduser -D -H -u 10001 aerolab
COPY --from=gobuild /out/cloud /usr/local/bin/cloud
COPY --from=web /src/apps/web/dist /srv/web
COPY infrastructure/docker/cloud-entrypoint.sh /usr/local/bin/cloud-entrypoint.sh
ENV AEROLAB_CLOUD_HTTP_ADDR=0.0.0.0:8090 AEROLAB_CLOUD_STATIC_DIR=/srv/web
EXPOSE 8090
USER aerolab
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s CMD wget -qO- http://127.0.0.1:8090/healthz >/dev/null || exit 1
ENTRYPOINT ["/sbin/tini","--","/usr/local/bin/cloud-entrypoint.sh"]
