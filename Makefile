BINARY=existora
VERSION?=dev
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LD_VARS=-X github.com/erenalidal/existora2pg/internal/cli.Version=$(VERSION) \
        -X github.com/erenalidal/existora2pg/internal/cli.Commit=$(COMMIT) \
        -X github.com/erenalidal/existora2pg/internal/cli.BuildDate=$(BUILD_DATE)
LDFLAGS=-ldflags "$(LD_VARS)"

.PHONY: build test lint clean docker-up docker-down ui build-all desktop desktop-dev

# CLI build
build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/existora/

# Web UI build (for embedded server mode)
ui:
	cd web/frontend && npm install && npm run build

build-all: ui build

# Desktop (Wails) builds
desktop-ui:
	cd web/frontend && npm install && WAILS_BUILD=1 npm run build:wails

desktop: ui desktop-ui
	CGO_LDFLAGS="-framework UniformTypeIdentifiers -Wl,-rpath,/opt/homebrew/lib" go build -tags production $(LDFLAGS) -o bin/$(BINARY)-desktop ./cmd/desktop/

desktop-dev:
	cd web/frontend && npm install
	wails dev -browser

# Cross-platform desktop builds (need both desktop-ui AND ui because cmd/desktop imports pkg/api which embeds static/*)
desktop-darwin-arm64: ui desktop-ui
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -tags production $(LDFLAGS) -o bin/$(BINARY)-desktop-darwin-arm64 ./cmd/desktop/

desktop-darwin-amd64: ui desktop-ui
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -tags production $(LDFLAGS) -o bin/$(BINARY)-desktop-darwin-amd64 ./cmd/desktop/

desktop-linux-amd64: ui desktop-ui
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags production $(LDFLAGS) -o bin/$(BINARY)-desktop-linux-amd64 ./cmd/desktop/

# Generate Windows resource (.syso) files with version info, icon, and manifest
windows-resource-desktop:
	cd cmd/desktop && goversioninfo -64 -o resource_windows_amd64.syso

windows-resource-cli:
	cd cmd/existora && goversioninfo -64 -o resource_windows_amd64.syso

desktop-windows-amd64: ui desktop-ui windows-resource-desktop
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -tags production -ldflags "-H windowsgui $(LD_VARS) -X main.appVersion=$(VERSION)" -o bin/$(BINARY)-desktop-windows-amd64.exe ./cmd/desktop/

desktop-windows-amd64-debug: ui desktop-ui windows-resource-desktop
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -tags production -ldflags "-H windowsgui $(LD_VARS) -X main.debugMode=true -X main.appVersion=$(VERSION)" -o bin/$(BINARY)-desktop-windows-amd64-debug.exe ./cmd/desktop/

# Windows CLI build
build-windows: ui windows-resource-cli
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build $(LDFLAGS) -o bin/$(BINARY)-windows-amd64.exe ./cmd/existora/

# Full Windows distribution package
dist-windows: clean-dist desktop-windows-amd64 desktop-windows-amd64-debug build-windows
	@echo "=== Creating Windows distribution package ==="
	@mkdir -p dist/existora2pg-windows-amd64/lib/oracle
	@cp bin/$(BINARY)-desktop-windows-amd64.exe dist/existora2pg-windows-amd64/existora-desktop.exe
	@cp bin/$(BINARY)-desktop-windows-amd64-debug.exe dist/existora2pg-windows-amd64/existora-desktop-debug.exe
	@cp bin/$(BINARY)-windows-amd64.exe dist/existora2pg-windows-amd64/existora.exe
	@if ls dist/oracle-dlls/*.dll 1>/dev/null 2>&1; then cp dist/oracle-dlls/*.dll dist/existora2pg-windows-amd64/lib/oracle/; echo "Oracle DLLs copied."; else echo "Warning: No Oracle DLLs found in dist/oracle-dlls/ — package will not include Oracle client."; fi
	@cd dist && zip -r existora2pg-windows-amd64.zip existora2pg-windows-amd64/
	@echo ""
	@echo "=== Windows package ready ==="
	@ls -lh dist/existora2pg-windows-amd64.zip
	@echo ""
	@echo "Contents:"
	@ls -lhR dist/existora2pg-windows-amd64/

# Clean dist artifacts only
clean-dist:
	rm -rf dist/existora2pg-windows-amd64 dist/existora2pg-windows-amd64.zip
	rm -rf cmd/desktop/frontend/dist pkg/api/static
	rm -f bin/$(BINARY)-*windows*.exe bin/$(BINARY)-*debug*.exe
	rm -f cmd/desktop/resource_windows_amd64.syso cmd/existora/resource_windows_amd64.syso

# Tests
test:
	go test ./...

test-v:
	go test -v ./...

test-integration:
	go test -v -tags=integration ./...

lint:
	golangci-lint run

clean:
	rm -rf bin/ dist/existora2pg-windows-amd64 dist/existora2pg-windows-amd64.zip
	rm -rf cmd/desktop/frontend/dist pkg/api/static

# Docker
docker-up:
	docker compose up -d
	@echo "Waiting for Oracle to be ready (this may take a few minutes)..."
	@docker compose exec oracle bash -c 'until healthcheck.sh; do sleep 5; done' 2>/dev/null
	@echo "Oracle and PostgreSQL are ready."

docker-down:
	docker compose down

docker-reset:
	docker compose down -v
	docker compose up -d
