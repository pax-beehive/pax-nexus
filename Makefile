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
COVERAGE_MIN := 80
OUTPUT_BIN_DIR ?= output/bin
EXTRACTION_CANDIDATE_STRATEGY ?= source-clause-v1
EXTRACTION_CANDIDATE_STRATEGIES := current interaction-slim evidence-fidelity-v1 source-clause-v1 source-clause-implicit-state-v1 typed-2 source-span-v1 source-span-v2 claim-card-v1 claim-card-v2
EXTRACTION_CANDIDATE_LDFLAG := -X $(MODULE)/internal/teamnote/extractor.buildDefaultCandidateStrategy=$(EXTRACTION_CANDIDATE_STRATEGY)
RECALL_CANDIDATE_STRATEGY ?= passive-v1
RECALL_CANDIDATE_STRATEGIES := passive-v1 hint-v1-selective
RECALL_CANDIDATE_LDFLAG := -X $(MODULE)/internal/teamnote.buildDefaultRecallCandidateStrategy=$(RECALL_CANDIDATE_STRATEGY)
RECALL_EVAL_FIXTURE ?= evals/stage/replay/team-note-optimization-30-c20fdd7-team_note.json
RECALL_EVAL_OUTPUT ?= runs/recall-eval-v1/current
RECALL_EVAL_SEMANTIC_THRESHOLD ?= 0.50
RECALL_EVAL_CANDIDATE_LIMIT ?= 16

.PHONY: all build validate-extraction-candidate-strategy validate-recall-candidate-strategy tools generate-init generate mocks fmt format-check lint test test-unit test-scripts coverage integration-test onprem-e2e recall-eval-v1 recall-eval-v2 recall-eval-v2-up recall-eval-v2-down docker-eval groupmembench-data groupmembench-eval eval-v2-prepare eval-v2-up eval-v2 eval-v2-smoke-up eval-v2-smoke eval-v2-acceptance-up eval-v2-acceptance eval-v2-down eval-v2-reset eval-v2-job-image eval-v2-job eval-v2-zep-canary eval-v3-prepare eval-v3-up eval-v3 eval-v3-down eval-v3-reset up down logs db-up db-down clean

all: lint test

validate-extraction-candidate-strategy:
	@case "$(EXTRACTION_CANDIDATE_STRATEGY)" in current|interaction-slim|evidence-fidelity-v1|source-clause-v1|source-clause-implicit-state-v1|typed-2|source-span-v1|source-span-v2|claim-card-v1|claim-card-v2) ;; \
		*) echo "unsupported EXTRACTION_CANDIDATE_STRATEGY=$(EXTRACTION_CANDIDATE_STRATEGY); expected one of: $(EXTRACTION_CANDIDATE_STRATEGIES)" >&2; exit 2 ;; \
	esac

validate-recall-candidate-strategy:
	@case "$(RECALL_CANDIDATE_STRATEGY)" in passive-v1|hint-v1-selective) ;; \
		*) echo "unsupported RECALL_CANDIDATE_STRATEGY=$(RECALL_CANDIDATE_STRATEGY); expected one of: $(RECALL_CANDIDATE_STRATEGIES)" >&2; exit 2 ;; \
	esac

build: validate-extraction-candidate-strategy validate-recall-candidate-strategy
	mkdir -p $(OUTPUT_BIN_DIR)
	CGO_ENABLED=0 GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go build -trimpath \
		-ldflags "$(EXTRACTION_CANDIDATE_LDFLAG) $(RECALL_CANDIDATE_LDFLAG)" -o $(OUTPUT_BIN_DIR)/hertz_service .

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

test: coverage test-scripts integration-test

test-unit:
	GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go test ./... -count=1

test-scripts:
	./scripts/test-eval-v2-job.sh
	./scripts/test-extraction-candidate-builds.sh
	./scripts/test-recall-candidate-builds.sh
	./scripts/test-zep-native-acceptance.sh

integration-test: db-up
	TEAM_MEMORY_TEST_POSTGRES_DSN=postgres://team_memory:team_memory@127.0.0.1:$${TEAM_MEMORY_POSTGRES_PORT:-55432}/team_memory?sslmode=disable \
		GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go test -p 1 ./internal/platform/postgres ./internal/teamnote/extractionqueue ./internal/eval/stagecapture ./internal/eval/v2/postgresstore -count=1

onprem-e2e:
	./scripts/onprem-e2e.sh

recall-eval-v1:
	GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go run ./cmd/team-memory-recall-replay \
		-fixtures $(RECALL_EVAL_FIXTURE) \
		-semantic-threshold $(RECALL_EVAL_SEMANTIC_THRESHOLD) \
		-candidate-limit $(RECALL_EVAL_CANDIDATE_LIMIT) \
		-dedup -degrade-related -output-dir $(RECALL_EVAL_OUTPUT)

up:
	./scripts/start-local-embedding.sh
	docker compose up -d --build --wait postgres team-memory

down:
	docker compose down

logs:
	docker compose logs -f postgres team-memory

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

coverage: db-up
	TEAM_MEMORY_TEST_POSTGRES_DSN=postgres://team_memory:team_memory@127.0.0.1:$${TEAM_MEMORY_POSTGRES_PORT:-55432}/team_memory?sslmode=disable \
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
	@. ./scripts/load-eval-v2-env.sh; \
		manifest="$(MANIFEST)"; manifest="$${manifest:-$${EVAL_V2_MANIFEST:-}}"; \
		run_id="$(RUN_ID)"; run_id="$${run_id:-$${EVAL_V2_RUN_ID:-}}"; \
		test -n "$$manifest" || (echo "MANIFEST or EVAL_V2_MANIFEST is required" >&2; exit 1); \
		test -n "$$run_id" || (echo "RUN_ID or EVAL_V2_RUN_ID is required" >&2; exit 1); \
		./scripts/eval-v2-stack.sh up "$$manifest" "$$run_id"

eval-v2:
	@. ./scripts/load-eval-v2-env.sh; \
		config="$(CONFIG)"; config="$${config:-$${EVAL_V2_CONFIG:-}}"; \
		test -n "$$config" || (echo "CONFIG or EVAL_V2_CONFIG is required" >&2; exit 1); \
		GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go run ./cmd/team-memory-eval-v2 -config "$$config"

eval-v2-smoke-up: eval-v2-prepare
	$(MAKE) eval-v2-up MANIFEST=runs/groupmembench-v2-selection/manifest.smoke.json RUN_ID=groupmembench-finance-v2-deepseek-v4-flash-smoke

eval-v2-smoke:
	$(MAKE) eval-v2 CONFIG=evals/v2/config.smoke.example.yaml

eval-v2-acceptance-up: eval-v2-prepare
	$(MAKE) eval-v2-up MANIFEST=runs/groupmembench-v2-selection/manifest.json RUN_ID=groupmembench-finance-v2-deepseek-v4-flash

eval-v2-acceptance:
	$(MAKE) eval-v2 CONFIG=evals/v2/config.example.yaml

eval-v2-down:
	./scripts/eval-v2-stack.sh down

eval-v2-reset:
	./scripts/eval-v2-stack.sh reset

eval-v3-prepare:
	./scripts/eval-v3-prepare-groupmembench.sh

eval-v3-up:
	@. ./scripts/load-eval-v3-env.sh; \
		manifest="$(MANIFEST)"; manifest="$${manifest:-$${EVAL_V3_MANIFEST:-runs/groupmembench-v3-selection/manifest.json}}"; \
		run_id="$(RUN_ID)"; run_id="$${run_id:-$${EVAL_V3_RUN_ID:-groupmembench-finance-v3}}"; \
		./scripts/eval-v3-stack.sh up "$$manifest" "$$run_id"

eval-v3:
	@. ./scripts/load-eval-v3-env.sh; \
		config="$(CONFIG)"; config="$${config:-$${EVAL_V3_CONFIG:-evals/v3/config.local.yaml}}"; \
		GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go run ./cmd/team-memory-eval-v3 -config "$$config"

eval-v3-down:
	./scripts/eval-v3-stack.sh down

eval-v3-reset:
	./scripts/eval-v3-stack.sh reset

recall-eval-v2-up:
	@manifest="$${MANIFEST:-runs/groupmembench-v3-selection/manifest.json}"; \
		run_id="$${RUN_ID:-groupmembench-finance-recall-v2}"; \
		./scripts/eval-v3-stack.sh up "$$manifest" "$$run_id"

recall-eval-v2:
	@. ./scripts/load-eval-v3-env.sh; \
		config="$${CONFIG:-evals/recall-v2/config.local.yaml}"; \
		GOCACHE=$${GOCACHE:-/tmp/team-memory-go-cache} go run ./cmd/team-memory-recall-eval-v2 -config "$$config"

recall-eval-v2-down:
	./scripts/eval-v3-stack.sh down

eval-v2-job-image:
	docker build -f evals/v2/docker/runner/Dockerfile -t team-memory-eval-v2-runner:local .

eval-v2-job: eval-v2-job-image
	@. ./scripts/load-eval-v2-env.sh; \
		zep_api_key="$${ZEP_API_KEY:-}"; \
		if [ -z "$$zep_api_key" ] && command -v paxm >/dev/null 2>&1; then \
			zep_config_path="$${PAXM_CONFIG_PATH:-$$(paxm config path)}"; \
			zep_api_key="$$(awk 'BEGIN { in_zep=0 } /^[[:space:]]{2}zep:/ { in_zep=1; next } in_zep && /^[[:space:]]{2}[A-Za-z0-9_-]+:/ { exit } in_zep && /^[[:space:]]{4}api_key:/ { sub(/^[[:space:]]*api_key:[[:space:]]*/, ""); print; exit }' "$$zep_config_path")"; \
		fi; \
		extra_mount=""; \
		paxm_source_dir="$${PAXM_SOURCE_DIR:-}"; \
		git_common_dir="$$(git rev-parse --git-common-dir)"; \
		prepared_selection_mount=""; \
		eval_env_mount=""; \
		base_env_mount=""; \
		if [ -n "$${EVAL_V2_PREPARED_SELECTION:-}" ]; then prepared_selection_mount="-v $${EVAL_V2_PREPARED_SELECTION}:$${EVAL_V2_PREPARED_SELECTION}:ro"; fi; \
		base_env_path="$${EVAL_V2_BASE_ENV_FILE:-}"; \
		if [ -n "$${EVAL_V2_ENV_FILE:-}" ] && [ -f "$${EVAL_V2_ENV_FILE}" ]; then eval_env_mount="-v $${EVAL_V2_ENV_FILE}:$${EVAL_V2_ENV_FILE}:ro"; if [ -z "$$base_env_path" ]; then base_env_path="$$(dirname "$${EVAL_V2_ENV_FILE}")/.env"; fi; fi; \
		if [ -n "$$base_env_path" ] && [ -f "$$base_env_path" ]; then base_env_mount="-v $$base_env_path:$$base_env_path:ro"; fi; \
		if [ -z "$$paxm_source_dir" ] && [ -n "$$base_env_path" ] && [ -f "$$base_env_path" ]; then paxm_source_dir="$$(awk -F= '$$1 == "PAXM_SOURCE_DIR" {print $$2; exit}' "$$base_env_path")"; fi; \
		if [ -n "$$paxm_source_dir" ]; then extra_mount="-v $$paxm_source_dir:$$paxm_source_dir:ro"; fi; \
		docker run --rm \
			--add-host host.docker.internal:host-gateway \
			-v /var/run/docker.sock:/var/run/docker.sock \
			-v "$(CURDIR):$(CURDIR)" \
			-v "$$git_common_dir:$$git_common_dir:ro" \
			$$extra_mount \
			$$prepared_selection_mount \
			$$eval_env_mount \
			$$base_env_mount \
			-w "$(CURDIR)" \
			-e EVAL_V2_ENV_FILE -e EVAL_V2_BASE_ENV_FILE="$$base_env_path" \
			-e EVAL_V2_JOB_RUN_ID -e EVAL_V2_SEED -e EVAL_V2_TOTAL_CASES -e EVAL_V2_PER_CATEGORY \
			-e EVAL_V2_OUTPUT_ROOT -e EVAL_V2_BASE_CONFIG -e EVAL_V2_JOB_TIMEOUT -e EVAL_V2_JOB_POSTGRES_DSN \
			-e EVAL_FRAMEWORK_VERSION -e EVAL_SELECTION_ALGORITHM -e EVAL_V2_COMPOSE_FILE -e EVAL_V2_STACK_MODE \
			-e EVAL_V2_JOB_DRY_RUN -e EVAL_V2_PREPARED_SELECTION -e EVAL_V2_PREPARED_MANIFEST -e EVAL_V2_ALLOW_DIRTY \
			-e EVAL_V2_ACCEPTANCE_PROGRAM \
			-e EVAL_V2_ZEP_BINARY=/usr/local/bin/eval-v2-zep -e ZEP_API_KEY="$$zep_api_key" \
			team-memory-eval-v2-runner:local -c 'mkdir -p .build; exec flock -n .build/eval-v2-job.lock timeout "$${EVAL_V2_JOB_TIMEOUT:-24h}" ./scripts/eval-v2-job.sh'

eval-v2-zep-canary:
	EVAL_V2_TOTAL_CASES=1 EVAL_V2_PER_CATEGORY=1 \
	EVAL_V2_BASE_CONFIG=evals/v2/config.interaction-slim-passive10-zep-native.local.yaml \
	EVAL_V2_OUTPUT_ROOT=runs/eval-v2/automated-zep \
	EVAL_V2_STACK_MODE=zep-native \
	EVAL_V2_ACCEPTANCE_PROGRAM=./scripts/verify-zep-native-acceptance.sh \
	$(MAKE) eval-v2-job

clean:
	rm -rf .build
