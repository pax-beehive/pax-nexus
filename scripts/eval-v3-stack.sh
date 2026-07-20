#!/bin/sh
set -eu

action="${1:-}"
manifest="${2:-}"
run_id="${3:-}"
. ./scripts/load-eval-v3-env.sh

if { [ "${action}" = "down" ] || [ "${action}" = "reset" ]; } && [ -z "${TEAM_MEMORY_API_KEYS:-}" ]; then
  TEAM_MEMORY_API_KEYS='{}'
  export TEAM_MEMORY_API_KEYS
fi

case "${action}" in
  up)
    if [ -z "${manifest}" ] || [ -z "${run_id}" ]; then
      echo "manifest path and run ID are required" >&2
      exit 1
    fi
    if [ "$(jq -r '.protocol // empty' "${manifest}")" != "multi-agent-groupmembench-v3" ]; then
      echo "Eval v3 requires a full-domain GroupMemBench v3 manifest" >&2
      exit 1
    fi
    domain_scope="$(jq -r '.cases[0].scope_id // empty' "${manifest}")"
    if [ -z "${domain_scope}" ]; then
      echo "Eval v3 manifest has no domain scope" >&2
      exit 1
    fi
    TEAM_MEMORY_API_KEYS="$(jq -cn --arg run_id "${run_id}" --arg domain_scope "${domain_scope}" '{("eval-" + $run_id + "-preflight"): ($run_id + "-preflight"), ("eval-" + $run_id + "-domain"): ($run_id + "-" + $domain_scope)}')"
    export TEAM_MEMORY_API_KEYS
    docker compose -p "${EVAL_V3_COMPOSE_PROJECT}" -f "${EVAL_V3_COMPOSE_FILE}" build team-memory team-memory-hint opencode mem0 mem0-configure
    ./scripts/start-local-embedding.sh -p "${EVAL_V3_COMPOSE_PROJECT}" -f "${EVAL_V3_COMPOSE_FILE}"
    docker compose -p "${EVAL_V3_COMPOSE_PROJECT}" -f "${EVAL_V3_COMPOSE_FILE}" up -d --wait postgres team-memory mem0-postgres mem0
    docker compose -p "${EVAL_V3_COMPOSE_PROJECT}" -f "${EVAL_V3_COMPOSE_FILE}" run --rm --no-deps mem0-configure
    ;;
  down)
    docker compose -p "${EVAL_V3_COMPOSE_PROJECT}" -f "${EVAL_V3_COMPOSE_FILE}" down
    ;;
  reset)
    docker compose -p "${EVAL_V3_COMPOSE_PROJECT}" -f "${EVAL_V3_COMPOSE_FILE}" down -v
    ;;
  *)
    echo "usage: $0 up <manifest> <run-id>|down|reset" >&2
    exit 1
    ;;
esac
