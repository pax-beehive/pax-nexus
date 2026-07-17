#!/bin/sh
set -eu

stage="${1:?stage is required}"
arm="${2:-all}"
. ./scripts/load-eval-v3-env.sh

compose_file="${EVAL_V3_COMPOSE_FILE}"
project_name="${EVAL_V3_COMPOSE_PROJECT}"
run_id="${PAX_EVAL_RUN_ID:?PAX_EVAL_RUN_ID is required}"
domain_api_key="eval-${run_id}-domain"
domain_mem0_run_id="${run_id}-domain"

run_memory_ingest() {
  provider="$1"
  batches_directory="$2"
  batches_file="$3"
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --entrypoint /usr/local/bin/eval-v2-memory \
    --volume "${batches_directory}:/artifact:ro" \
    -e TEAM_MEMORY_BASE_URL=http://team-memory:8080 \
    -e TEAM_MEMORY_API_KEY="${domain_api_key}" \
    -e MEM0_BASE_URL=http://mem0:8000 \
    -e PAXM_USER_ID="${MEM0_EVAL_USER_ID}" \
    -e PAXM_AGENT_ID="${MEM0_EVAL_AGENT_ID}" \
    -e MEM0_RUN_ID="${domain_mem0_run_id}" \
    opencode -action ingest -provider "${provider}" -session-batches-file "/artifact/${batches_file}"
}

ingest_domain() {
  manifest="${PAX_EVAL_MANIFEST:?PAX_EVAL_MANIFEST is required}"
  output_directory="${PAX_EVAL_OUTPUT_DIR:?PAX_EVAL_OUTPUT_DIR is required}"
  manifest_directory="$(cd "$(dirname "${manifest}")" && pwd -P)"
  batches_relative="$(jq -r '.domain_session_batches // empty' "${manifest}")"
  if [ -z "${batches_relative}" ]; then
    echo "Eval v3 manifest is missing domain_session_batches" >&2
    exit 1
  fi
  batches_path="${manifest_directory}/${batches_relative}"
  batches_directory="$(cd "$(dirname "${batches_path}")" && pwd -P)"
  batches_file="$(basename "${batches_path}")"
  output_directory="$(mkdir -p "${output_directory}" && cd "${output_directory}" && pwd -P)"
  marker_directory="${output_directory}/memory"
  private_directory="${marker_directory}/private"
  mkdir -p "${marker_directory}" "${private_directory}"

  if [ ! -f "${marker_directory}/team-note.complete" ]; then
    run_memory_ingest team_note "${batches_directory}" "${batches_file}" > "${marker_directory}/team-note-ingest.json"
    : > "${marker_directory}/team-note.complete"
  fi
  if [ ! -f "${marker_directory}/mem0.complete" ]; then
    run_memory_ingest mem0_messages "${batches_directory}" "${batches_file}" > "${marker_directory}/mem0-ingest.json"
    : > "${marker_directory}/mem0.complete"
  fi
  if [ ! -f "${marker_directory}/private-sqlite.complete" ]; then
    docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
      --entrypoint node \
      --volume "${batches_directory}:/artifact:ro" \
      --volume "${private_directory}:/private-memory" \
      opencode /opt/team-memory/ingest-private-sqlite.mjs "/artifact/${batches_file}" /private-memory \
      > "${marker_directory}/private-sqlite-ingest.json"
    : > "${marker_directory}/private-sqlite.complete"
  fi

  scope_id="${run_id}-$(jq -r '.cases[0].scope_id' "${manifest}")"
  attempts=0
  readiness_attempts="${PAX_EVAL_READINESS_ATTEMPTS:-1200}"
  while [ "${attempts}" -lt "${readiness_attempts}" ]; do
    ready="$(printf '%s' "SELECT CASE WHEN EXISTS (SELECT 1 FROM session_streams WHERE scope_id = :'scope_id') AND NOT EXISTS (SELECT 1 FROM session_streams WHERE scope_id = :'scope_id' AND (NOT complete OR extraction_cursor < last_sequence)) THEN 1 ELSE 0 END" | docker compose -p "${project_name}" -f "${compose_file}" exec -T postgres psql -U team_memory -d team_memory -v scope_id="${scope_id}" -At 2>/dev/null || true)"
    if [ "${ready:-0}" -eq 1 ] 2>/dev/null; then
      printf '{"full_domain_ready":true,"scope_id":"%s"}\n' "${scope_id}"
      return
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  echo "timed out waiting for full-domain Team Note extraction" >&2
  exit 1
}

run_preflight() {
  preflight_key="eval-${run_id}-preflight"
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --entrypoint /usr/local/bin/eval-v2-memory \
    -e TEAM_MEMORY_BASE_URL=http://team-memory:8080 \
    -e TEAM_MEMORY_API_KEY="${preflight_key}" \
    -e MEM0_BASE_URL=http://mem0:8000 \
    -e PAXM_USER_ID="${MEM0_EVAL_USER_ID}" \
    -e PAXM_AGENT_ID=preflight \
    -e MEM0_RUN_ID="${run_id}-preflight" \
    opencode -action preflight -marker "PAX-EVAL-V3-PREFLIGHT-${run_id}"
}

run_consumer() {
  answering_agent="${PAX_EVAL_ANSWERING_AGENT_ID:?PAX_EVAL_ANSWERING_AGENT_ID is required}"
  asking_user="${PAX_EVAL_ASKING_USER_ID:-${PAX_EVAL_USER_ID:?PAX_EVAL_USER_ID is required}}"
  workspace="${PAX_EVAL_CONSUMER_WORKSPACE:?PAX_EVAL_CONSUMER_WORKSPACE is required}"
  output_directory="$(cd "${PAX_EVAL_OUTPUT_DIR:?PAX_EVAL_OUTPUT_DIR is required}" && pwd -P)"
  private_directory="${output_directory}/memory/private"
  provider_type=team-memory
  provider_user_id="${asking_user}"
  provider_agent_id="${answering_agent}"
  recall_enabled=1
  extra_mount=""
  private_path=""

  case "${arm}" in
    no_memory_team)
      recall_enabled=0
      ;;
    groupmembench_mem0)
      provider_type=mem0
      provider_user_id="${MEM0_EVAL_USER_ID}"
      provider_agent_id="${MEM0_EVAL_AGENT_ID}"
      ;;
    private_sqlite_plus_team_note)
      provider_type=team-memory-sqlite
      private_file="${answering_agent}.sqlite"
      if [ ! -f "${private_directory}/${private_file}" ]; then
        echo "private SQLite memory is missing for ${answering_agent}" >&2
        exit 1
      fi
      extra_mount="--volume ${private_directory}:/private-memory"
      private_path="/private-memory/${private_file}"
      ;;
    *)
      echo "unsupported Eval v3 arm: ${arm}" >&2
      exit 1
      ;;
  esac

  prompt="Answering teammate: ${answering_agent}
Original asking user: ${asking_user}

Answer the following question on behalf of the original asking user. Preserve the question's first-person semantics.

${PAX_EVAL_QUESTION}"

  # extra_mount contains exactly one internally constructed --volume pair.
  # shellcheck disable=SC2086
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    ${extra_mount} \
    --volume "${workspace}:/workspace:ro" \
    -e PAXM_PROVIDER_TYPE="${provider_type}" \
    -e TEAM_MEMORY_API_KEY="${domain_api_key}" \
    -e PAXM_USER_ID="${asking_user}" \
    -e PAXM_AGENT_ID="${answering_agent}" \
    -e PAXM_PROVIDER_USER_ID="${provider_user_id}" \
    -e PAXM_PROVIDER_AGENT_ID="${provider_agent_id}" \
    -e PAXM_PRIVATE_SQLITE_PATH="${private_path}" \
    -e MEM0_RUN_ID="${domain_mem0_run_id}" \
    -e PAXM_RECALL_ENABLED="${recall_enabled}" \
    -e PAXM_WRITE_ENABLED=0 \
    -e PAXM_EVAL_CONSUMER_POLICY=1 \
    -e PAXM_EVAL_RECALL_MODE=passive \
    -e PAXM_PASSIVE_MIN_RELEVANCE="${PAXM_PASSIVE_MIN_RELEVANCE}" \
    -e PAXM_PASSIVE_MIN_SCORE="${PAXM_PASSIVE_MIN_SCORE}" \
    -e PAXM_PASSIVE_PROVIDER_TIMEOUT="${PAXM_PASSIVE_PROVIDER_TIMEOUT}" \
    -e PAXM_INSERTION_MIN_SCORE="${PAXM_INSERTION_MIN_SCORE}" \
    -e PAXM_EVAL_DIAGNOSTICS="${PAXM_EVAL_DIAGNOSTICS}" \
    opencode run --agent eval-consumer --format json --model "${OPENCODE_MODEL}" "${prompt}"
}

case "${stage}" in
  ingest-domain) ingest_domain ;;
  preflight) run_preflight ;;
  consumer) run_consumer ;;
  *) echo "unsupported Eval v3 stage: ${stage}" >&2; exit 1 ;;
esac
