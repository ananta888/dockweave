# DockWeave — Makefile
# Requires: Go >= 1.27, Docker Engine

GO        ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
GOFLAGS   ?=
BIN_DIR   := bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build install test test-race test-integration vet lint clean docker-image install-tools

all: build

build:
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/dockweave ./cmd/dockweave
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/dockweave-tui ./cmd/dockweave-tui
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/dockweaved ./cmd/dockweaved

install: build
	install -Dm755 $(BIN_DIR)/dockweave $(HOME)/.local/bin/dockweave
	install -Dm755 $(BIN_DIR)/dockweave-tui $(HOME)/.local/bin/dockweave-tui
	install -Dm755 $(BIN_DIR)/dockweaved $(HOME)/.local/bin/dockweaved
	@echo "installed to $(HOME)/.local/bin (make sure it is on your PATH)"

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-integration:
	docker run --rm --privileged \
		-v "$(CURDIR):/src" \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /src \
		golang:1.27 \
		bash -c "go test -tags integration -v ./tests/integration/..."

vet:
	$(GO) vet ./...

lint: vet
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed; skipped"

clean:
	rm -rf $(BIN_DIR)

docker-image:
	docker build -t dockweave/dockweaved:$(VERSION) -f deployments/Dockerfile .

install-tools:
	$(GO) install honnef.co/go/tools/cmd/staticcheck@latest
