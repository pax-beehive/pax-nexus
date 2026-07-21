#!/bin/sh
set -eu

compose_file="tests/onprem-e2e/compose.yaml"
project_name="${TEAM_MEMORY_E2E_PROJECT:-team-memory-onprem-e2e-$(date -u +%Y%m%d%H%M%S)-$$}"
volume_name="${project_name}_postgres-data"

run_compose() {
  docker compose -p "${project_name}" -f "${compose_file}" "$@"
}

cleanup() {
  exit_status="$1"
  trap - EXIT INT TERM
  if [ "${exit_status}" -ne 0 ]; then
    run_compose logs --no-color team-memory mock-extractor postgres >&2 || true
  fi
  if ! run_compose down -v --remove-orphans >/dev/null 2>&1; then
    echo "failed to remove on-prem E2E containers and volumes" >&2
    exit_status=1
  fi
  if docker volume inspect "${volume_name}" >/dev/null 2>&1; then
    echo "temporary PostgreSQL volume remains: ${volume_name}" >&2
    exit_status=1
  fi
  exit "${exit_status}"
}

trap 'cleanup $?' EXIT
trap 'exit 130' INT TERM

run_compose config --quiet
run_compose build team-memory mock-extractor e2e
run_compose up -d postgres mock-extractor team-memory

if ! docker volume inspect "${volume_name}" >/dev/null 2>&1; then
  echo "temporary PostgreSQL volume was not created: ${volume_name}" >&2
  exit 1
fi

run_compose run --rm e2e
echo "on-prem Docker E2E passed; temporary volume will be removed: ${volume_name}"
