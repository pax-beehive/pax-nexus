SHELL := /bin/sh

MODULE := github.com/pax-beehive/pax-nexus
IDL := idl/team_memory.thrift
TOOLS_DIR := $(CURDIR)/.tools/bin
HZ := $(TOOLS_DIR)/hz
MOCKGEN := $(TOOLS_DIR)/mockgen
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint
GOLANGCI_LINT_CACHE ?= /tmp/team-memory-golangci-cache

# hz is versioned as the github.com/cloudwego/hertz/cmd/hz submodule. Its
# releases do not use the Hertz runtime's version number.
HZ_VERSION := v0.9.7
THRIFTGO_VERSION := v0.4.3
MOCK_VERSION := v0.6.0
GOLANGCI_LINT_VERSION := v2.11.3
COVERAGE_MIN := 75

.PHONY: all tools generate-init generate mocks fmt format-check lint test test-unit coverage integration-test docker-eval groupmembench-data groupmembench-eval eval-v2-prepare eval-v2-up eval-v2 eval-v2-down eval-v2-reset up down logs db-up db-down clean

all: lint test

tools: $(HZ) $(TOOLS_DIR)/thriftgo $(MOCKGEN) $(GOLANGCI_LINT)

$(TOOLS_DIR):
	mkdir -p $(TOOLS_DIR)

$(HZ): | $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) go install github.com/cloudwego/hertz/cmd/hz@$(HZ_VERSION)

$(TOOLS_DIR)/thriftgo: | $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) go install github.com/cloudwego/thriftgo@$(THRIFTGO_VERSION)

$(MOCKGEN): | $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) go install go.uber.org/mock/mockgen@$(MOCK_VERSION)

$(GOLANGCI_LINT): | $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Run once when the Hertz transport slice is first implemented. hz records the
# chosen paths so later updates regenerate into the same layout.
generate-init: tools
	$(HZ) new --module $(MODULE) --idl $(IDL) --out_dir . \
		--handler_dir internal/teamnote/transport/httpapi/handler \
		--model_dir internal/teamnote/transport/httpapi/model \
		--router_dir internal/teamnote/transport/httpapi/router \
		--sort_router --handler_by_method

generate: tools
	$(HZ) update --module $(MODULE) --idl $(IDL) --out_dir . \
		--handler_dir internal/teamnote/transport/httpapi/handler \
		--model_dir internal/teamnote/transport/httpapi/model \
		--sort_router --handler_by_method

mocks: $(MOCKGEN)
	PATH=$(TOOLS_DIR):$$PATH go generate ./...

fmt: $(GOLANGCI_LINT)
	GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) fmt

format-check: $(GOLANGCI_LINT)
	@diff="$$(GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) fmt --diff)" || exit $$?; \
		if [ -n "$$diff" ]; then printf '%s\n' "$$diff"; exit 1; fi

lint: $(GOLANGCI_LINT)
	GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) run ./...

test: coverage integration-test

test-unit:
	GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go test ./... -count=1

integration-test: db-up
	TEAM_MEMORY_TEST_POSTGRES_DSN=postgres://team_memory:team_memory@127.0.0.1:$${TEAM_MEMORY_POSTGRES_PORT:-55432}/team_memory?sslmode=disable \
		GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go test ./internal/platform/postgres ./internal/teamnote/extractionqueue ./internal/eval/v2/postgresstore -count=1

up:
	docker compose up -d --build --wait postgres team-memory

down:
	docker compose down

logs:
	docker compose logs -f postgres team-memory

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

coverage:
	COVERAGE_MIN=$(COVERAGE_MIN) GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} ./scripts/check-coverage.sh

docker-eval:
	./scripts/docker-e2e.sh

groupmembench-data:
	./scripts/fetch-groupmembench.sh Finance

groupmembench-eval:
	./scripts/groupmembench-eval.sh

eval-v2-prepare:
	./scripts/eval-v2-prepare-groupmembench.sh

eval-v2-up:
	@test -n "$(MANIFEST)" || (echo "MANIFEST is required" >&2; exit 1)
	@test -n "$(RUN_ID)" || (echo "RUN_ID is required" >&2; exit 1)
	./scripts/eval-v2-stack.sh up "$(MANIFEST)" "$(RUN_ID)"

eval-v2:
	@test -n "$(CONFIG)" || (echo "CONFIG is required" >&2; exit 1)
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
		GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go run ./cmd/team-memory-eval-v2 -config "$(CONFIG)"

eval-v2-down:
	./scripts/eval-v2-stack.sh down

eval-v2-reset:
	./scripts/eval-v2-stack.sh reset

clean:
	rm -rf .build
