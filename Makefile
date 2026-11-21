# Gunkly — single entry point for local checks and CI.
GO     ?= go
PKGS   := ./...
PG_URL ?= $(GUNKLY_TEST_POSTGRES_URL)

.PHONY: check fmt vet lint deadcode test test-pg web web-check build fuzz

check: fmt vet lint deadcode test test-pg web-check build

fmt:
	@test -z "$$(gofmt -l $$(git ls-files '*.go'))" || { gofmt -l $$(git ls-files '*.go'); exit 1; }

vet:
	$(GO) vet $(PKGS)

lint:
	golangci-lint run

deadcode:
	@out="$$($(GO) run golang.org/x/tools/cmd/deadcode@v0.39.0 -test ./...)" || exit 1; test -z "$$out" || { echo "$$out"; exit 1; }

test:
	$(GO) test -race -count=1 $(PKGS)

test-pg:
	@test -n "$(PG_URL)" || { echo "GUNKLY_TEST_POSTGRES_URL not set"; exit 1; }
	GUNKLY_TEST_DB=postgres GUNKLY_TEST_POSTGRES_URL="$(PG_URL)" $(GO) test -race -count=1 $(PKGS)

web:
	cd web && npm ci && npm run build

web-check:
	cd web && npm ci && npm run check && npm run licenses && npm run build && npm run budget

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o bin/gunkly ./cmd/gunkly

fuzz:
	$(GO) test ./internal/schedule -run='^$$' -fuzz=FuzzParseRRule -fuzztime=30s
