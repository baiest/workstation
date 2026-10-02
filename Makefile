.PHONY: help deps web build run dev test lint install clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN     := bin/workstation
PREFIX  ?= /usr/local

help: ## Show this help
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-9s %s\n", $$1, $$2}'

deps: ## Install frontend dependencies
	pnpm -C web install --frozen-lockfile

web: deps ## Build the frontend (it gets embedded into the binary)
	pnpm -C web build

build: web ## Build the single self-contained binary: bin/workstation
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) ./cmd/workstation

run: build ## Build and run on http://127.0.0.1:7420
	./$(BIN)

dev: ## Backend only; run `pnpm -C web dev` in another terminal for hot reload
	go run ./cmd/workstation

test: ## Go and frontend tests
	go test ./...
	pnpm -C web test

lint: ## gofmt, go vet, vue-tsc
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt needed"; exit 1; }
	go vet ./...
	pnpm -C web typecheck

install: build ## Install the binary to $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/workstation

clean: ## Remove build output
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete
