#!/bin/sh
set -eu

action="${1:-}"
manifest="${2:-}"
run_id="${3:-}"
compose_file="evals/v2/compose.yaml"
project_name="pax-nexus-eval-v2"

. ./scripts/load-eval-v2-env.sh

case "${action}" in
  up)
    if [ -z "${manifest}" ] || [ -z "${run_id}" ]; then
      echo "manifest path and run ID are required" >&2
      exit 1
    fi
    TEAM_MEMORY_API_KEYS="$(jq -c --arg run_id "${run_id}" 'reduce .cases[] as $case ({("eval-" + $run_id + "-preflight"): ($run_id + "-preflight")}; .["eval-" + $run_id + "-" + $case.id] = ($run_id + "-" + $case.scope_id))' "${manifest}")"
    export TEAM_MEMORY_API_KEYS
    docker compose -p "${project_name}" -f "${compose_file}" build team-memory opencode mem0 mem0-configure
    docker compose -p "${project_name}" -f "${compose_file}" up -d --wait postgres team-memory mem0-postgres mem0
    docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps mem0-configure
    ;;
  down)
    docker compose -p "${project_name}" -f "${compose_file}" down
    ;;
  reset)
    docker compose -p "${project_name}" -f "${compose_file}" down -v
    ;;
  *)
    echo "usage: $0 up <manifest> <run-id>|down|reset" >&2
    exit 1
    ;;
esac
