# DockWeave — Makefile
# Requires: Go >= 1.27.3, Docker Engine

GO        ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
GOFLAGS   ?=
BIN_DIR   := bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build web install test test-wrapper test-race test-integration vet lint openapi-check clean docker-image install-tools

all: build

build:
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/dockweave ./cmd/dockweave
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/dockweave-tui ./cmd/dockweave-tui
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/dockweaved ./cmd/dockweaved
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/dockweave-broker ./cmd/dockweave-broker

web:
	cd ui/web && npm ci && npm run build
	rm -rf internal/webui/dist
	mkdir -p internal/webui/dist
	cp -r ui/web/dist/dockweave-web/browser/. internal/webui/dist/
	@echo "web assets copied to internal/webui/dist (rebuild dockweave to embed them)"

install: build
	install -Dm755 $(BIN_DIR)/dockweave $(HOME)/.local/bin/dockweave
	install -Dm755 $(BIN_DIR)/dockweave-tui $(HOME)/.local/bin/dockweave-tui
	install -Dm755 $(BIN_DIR)/dockweaved $(HOME)/.local/bin/dockweaved
	install -Dm755 $(BIN_DIR)/dockweave-broker $(HOME)/.local/bin/dockweave-broker
	@echo "installed to $(HOME)/.local/bin (make sure it is on your PATH)"

test: test-wrapper
	$(GO) test ./...

# Paperclip wrapper helpers (pipeline precheck/routing, blocked-check) with
# faked dockweave/paperclipai/curl binaries; needs only python3 and sh.
test-wrapper:
	python3 -B -m unittest discover -s deployments/paperclip/tests

test-race:
	$(GO) test -race ./...

test-integration:
	bash scripts/test-integration.sh

vet:
	$(GO) vet ./...

lint: vet
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed; run make install-tools" >&2; exit 1; fi

# Real handler responses must match docs/api/openapi.yaml (needs jsonschema, pyyaml).
openapi-check:
	python3 -B scripts/validate_openapi.py

clean:
	rm -rf $(BIN_DIR)

docker-image:
	docker build -t dockweave/dockweaved:$(VERSION) -f deployments/Dockerfile .

STATICCHECK_VERSION ?= v0.8.2

install-tools:
	$(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
