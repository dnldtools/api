# ============================================================
# rest-api - Makefile
# ============================================================

APP_NAME := rest-api
BIN_DIR  := bin
BIN      := $(BIN_DIR)/$(APP_NAME)

GO      ?= go
GOFLAGS ?=

# Integration-test configuration (see docs/TESTING.md). These defaults target
# a dedicated test database/Redis DB so development data is never touched.
TEST_DATABASE_URL ?= postgres://root@localhost:5432/rest-api_test?sslmode=disable
TEST_REDIS_ADDR   ?= localhost:6379
TEST_REDIS_DB     ?= 15

.PHONY: help build run test test-race test-integration vet fmt fmtcheck check tidy clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

build: ## Build the API binary into ./bin
	$(GO) build $(GOFLAGS) -o $(BIN) ./cmd/api

run: ## Run the API locally
	$(GO) run ./cmd/api

test: ## Run unit tests (integration tests skip without TEST_* env)
	$(GO) test ./...

test-race: ## Run tests with the race detector (requires CGO + a C compiler)
	$(GO) test -race ./...

test-integration: ## Run all tests incl. PostgreSQL+Redis integration tests (serialized)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
	TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" \
	TEST_REDIS_DB="$(TEST_REDIS_DB)" \
	$(GO) test -p 1 ./...

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format all Go source files
	$(GO) fmt ./...

fmtcheck: ## Fail if any Go file is not gofmt-clean
	@files="$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))"; \
	if [ -n "$$files" ]; then \
		echo "gofmt required on:"; echo "$$files"; exit 1; \
	fi

check: ## Full gate: formatting + vet + unit tests + build
	$(MAKE) fmtcheck
	$(MAKE) vet
	$(MAKE) test
	$(MAKE) build

tidy: ## Tidy the module dependencies
	$(GO) mod tidy

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
