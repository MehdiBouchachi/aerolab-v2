# AeroLab V2 - reproducible commands. Needs: Go >= 1.24, mosquitto (dev/tests).
# Windows: use WSL, or run the underlying `go` commands shown here from PowerShell.
SHELL := /bin/bash
DEVICES ?= 10
BROKER_PORT ?= 18830
HTTP_ADDR ?= 127.0.0.1:18080

.PHONY: fw-test web-build-cloud web-build web-test help build test test-race test-integration load lint dev-provision dev-stack dev-stop clean pi-arm64

help:
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | column -t -s $$'\t'

build: ## build edge + simulator binaries into ./bin
	mkdir -p bin
	cd services/edge-api && go build -trimpath -o ../../bin/edge ./cmd/edge
	cd services/cloud-api && go build -trimpath -o ../../bin/cloud ./cmd/cloud
	cd simulator && go build -trimpath -o ../bin/aerolab-sim ./cmd/aerolab-sim && go build -trimpath -o ../bin/aerolab-seed ./cmd/aerolab-seed

web-build: ## build the dashboard into apps/web/dist (served by the edge)
	cd apps/web && pnpm install --frozen-lockfile=false && pnpm build

fw-test: ## portable firmware core on the host (gcc + ASan/UBSan) + Go validates its output
	cd firmware/esp32/test/host && make test
	cd tests && go test -count=1 -run TestFirmwareEncoded .

web-build-cloud: ## build the dashboard for the CLOUD origin into apps/web/dist-cloud
	cd apps/web && pnpm install --frozen-lockfile=false && VITE_SOURCE=cloud pnpm build --outDir dist-cloud

web-test: ## dashboard typecheck + unit tests
	cd apps/web && pnpm install --frozen-lockfile=false && pnpm typecheck && pnpm test

pi-arm64: ## cross-compile the edge for Raspberry Pi 5 (pure Go, no CGO)
	mkdir -p bin
	cd services/edge-api && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o ../../bin/edge-linux-arm64 ./cmd/edge

lint: ## gofmt + go vet on every module
	@test -z "$$(gofmt -l contracts services simulator tests)" || (gofmt -l contracts services simulator tests; exit 1)
	cd contracts/mqtt && go vet ./... && cd ../../services/edge-api && go vet ./... && cd ../../simulator && go vet ./... && cd ../tests && go vet ./...

test: ## fast tests: contracts, edge unit/handler tests, simulator unit tests
	cd contracts/mqtt && go test -count=1 ./...
	cd services/edge-api && go test -count=1 ./...
	cd services/cloud-api && go test -count=1 ./...   # needs PostgreSQL binaries (skips otherwise)
	cd simulator && go test -count=1 ./...

test-race: ## same, with the race detector
	cd contracts/mqtt && go test -race -count=1 ./...
	cd services/edge-api && go test -race -count=1 ./...
	cd services/cloud-api && go test -race -count=1 ./...
	cd simulator && go test -race -count=1 ./...

test-integration: ## real Mosquitto + real edge binary + simulated fleet (~1 min)
	cd tests && go test -count=1 -timeout 15m -v ./integration/

load: ## 10 and 20 simulated devices; prints measured RSS/CPU/recovery (opt-in, minutes)
	cd tests && AEROLAB_LOAD=1 AEROLAB_LOAD_DEVICES=10 go test -count=1 -timeout 30m -run TestLoad -v ./integration/
	cd tests && AEROLAB_LOAD=1 AEROLAB_LOAD_DEVICES=20 go test -count=1 -timeout 30m -run TestLoad -v ./integration/

dev-provision: ## generate dev broker config + random secrets into .dev/ (git-ignored)
	cd simulator && go run ./cmd/aerolab-seed -out ../.dev -devices $(DEVICES) -port $(BROKER_PORT)

dev-stack: build dev-provision ## broker + edge + $(DEVICES) SIMULATED controllers (Ctrl-C to stop)
	HTTP_ADDR=$(HTTP_ADDR) ./scripts/dev-stack.sh

dev-stop: ## stop leftovers of dev-stack
	-pkill -f 'bin/aerolab-sim' ; -pkill -f 'bin/edge' ; -pkill -f '.dev/mosquitto/mosquitto.conf'

clean:
	rm -rf bin .dev
