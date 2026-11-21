# Gunkly — single entry point for local checks and CI.
GO     ?= go
PKGS   := ./...
PG_URL ?= $(GUNKLY_TEST_POSTGRES_URL)

.PHONY: check fmt vet lint deadcode test test-pg web web-check build fuzz fuzz-check fitness dep-budget e2e openapi-diff

check: fmt vet lint deadcode dep-budget fuzz-check test test-pg fitness web-check e2e build

fmt:
	@test -z "$$(gofmt -l $$(git ls-files '*.go'))" || { gofmt -l $$(git ls-files '*.go'); exit 1; }

vet:
	$(GO) vet $(PKGS)

lint:
	golangci-lint run

deadcode:
	@out="$$($(GO) run golang.org/x/tools/cmd/deadcode@v0.53.0 -test ./...)" || exit 1; test -z "$$out" || { echo "$$out"; exit 1; }

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

# Every Fuzz* target must be in the nightly matrix (.github/workflows/fuzz.yml).
fuzz-check:
	scripts/fuzz-targets.sh

# Frontend architecture fitness: network only via lib/api.ts, no inline styles, logical CSS only.
fitness:
	scripts/fitness-web.sh

# Direct dependency budgets (Go requires, web devDependencies).
dep-budget:
	scripts/dep-budget.sh

# Playwright end to end; builds the web bundle and the binary itself, needs Chromium.
e2e:
	cd web && npm ci && npm run e2e

# Breaking /api/v1 changes vs a base ref (needs oasdiff): make openapi-diff BASE=origin/main
BASE ?= origin/main
openapi-diff:
	scripts/openapi-diff.sh $(BASE)
