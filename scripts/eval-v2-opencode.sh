#!/bin/sh
set -eu

stage="${1:?stage is required}"
arm="${2:?arm is required}"
compose_file="${EVAL_V2_COMPOSE_FILE:-evals/v2/compose.yaml}"
project_name="${EVAL_V2_COMPOSE_PROJECT:-pax-nexus-eval-v2}"
case_id="${PAX_EVAL_CASE_ID:-preflight}"
eval_user_id="${PAX_EVAL_USER_ID:-eval-owner}"
scope_id="${PAX_EVAL_SCOPE_ID:-preflight}"
api_key="eval-${PAX_EVAL_RUN_ID}-${case_id}"
agent_id="groupmembench-${eval_user_id}"
mem0_run_id="${PAX_EVAL_RUN_ID}-${case_id}"
team_note_scope_id="${PAX_EVAL_RUN_ID}-${scope_id}"
if [ "${arm}" = "team_note_hybrid" ]; then
  api_key="${api_key}-team-note-hybrid"
  team_note_scope_id="${team_note_scope_id}-team-note-hybrid"
fi

. ./scripts/load-eval-v2-env.sh

# Compose validates required variables for every service even though eval agent
# calls use --no-deps and never recreate the already configured Team Memory
# service. The real key map is applied by eval-v2-stack.sh during stack startup.
TEAM_MEMORY_API_KEYS="${TEAM_MEMORY_API_KEYS:-{}}"
export TEAM_MEMORY_API_KEYS

run_agent() {
  workspace="$1"
  recall_enabled="$2"
  write_enabled="$3"
  prompt="$4"
  consumer_policy="$5"
  recall_mode="$6"
  opencode_agent="build"
  if [ "${consumer_policy}" = "1" ]; then
    opencode_agent="eval-consumer"
  fi
  provider_type="team-memory"
  memory_user_id="${eval_user_id}"
  memory_agent_id="${agent_id}"
  if [ "${arm}" = "mem0" ] || [ "${arm}" = "mem0_messages" ] || [ "${arm}" = "mem0_chunks" ]; then
    provider_type="mem0"
    memory_user_id="${MEM0_EVAL_USER_ID}"
    memory_agent_id="${MEM0_EVAL_AGENT_ID}"
  fi
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --volume "${workspace}:/workspace:ro" \
    -e PAXM_PROVIDER_TYPE="${provider_type}" \
    -e TEAM_MEMORY_API_KEY="${api_key}" \
    -e PAXM_USER_ID="${memory_user_id}" \
    -e PAXM_AGENT_ID="${memory_agent_id}" \
    -e MEM0_RUN_ID="${mem0_run_id}" \
    -e MEM0_SCORE_SEMANTICS="${MEM0_SCORE_SEMANTICS:-distance}" \
    -e MEM0_SEARCH_SCOPE_PAYLOAD="${MEM0_SEARCH_SCOPE_PAYLOAD:-top_level}" \
    -e PAXM_EXPECTED_VERSION="${PAXM_EXPECTED_VERSION:-v0.1.29}" \
    -e PAXM_RECALL_ENABLED="${recall_enabled}" \
    -e PAXM_WRITE_ENABLED="${write_enabled}" \
    -e PAXM_EVAL_CONSUMER_POLICY="${consumer_policy}" \
    -e PAXM_EVAL_RECALL_MODE="${recall_mode}" \
    -e PAXM_ACTIVE_RECALL_MAX_CALLS="${PAXM_ACTIVE_RECALL_MAX_CALLS:-1}" \
    -e PAXM_PASSIVE_MIN_RELEVANCE="${PAXM_PASSIVE_MIN_RELEVANCE:--1}" \
    -e PAXM_PASSIVE_MIN_SCORE="${PAXM_PASSIVE_MIN_SCORE:--1}" \
    -e PAXM_PASSIVE_PROVIDER_TIMEOUT="${PAXM_PASSIVE_PROVIDER_TIMEOUT:-2s}" \
    -e PAXM_INSERTION_MIN_SCORE="${PAXM_INSERTION_MIN_SCORE:-0}" \
    -e PAXM_EVAL_DIAGNOSTICS="${PAXM_EVAL_DIAGNOSTICS:-1}" \
    opencode run --agent "${opencode_agent}" --format json --model "${OPENCODE_MODEL}" "${prompt}"
}

run_memory_ingest() {
  helper_provider="$1"
  helper_api_key="$2"
  helper_user_id="$3"
	  helper_agent_id="$4"
	  helper_run_id="$5"
	  helper_mount="$6"
	  helper_file="$7"
	  helper_require_write="$8"
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --entrypoint /usr/local/bin/eval-v2-memory \
    --volume "${helper_mount}:/artifact:ro" \
    -e TEAM_MEMORY_BASE_URL=http://team-memory:8080 \
    -e TEAM_MEMORY_API_KEY="${helper_api_key}" \
    -e MEM0_BASE_URL=http://mem0:8000 \
    -e PAXM_USER_ID="${helper_user_id}" \
    -e PAXM_AGENT_ID="${helper_agent_id}" \
    -e MEM0_RUN_ID="${helper_run_id}" \
    opencode -action ingest -provider "${helper_provider}" -session-batches-file "${helper_file}" \
      -require-write="${helper_require_write}"
}

run_memory_preflight() {
  helper_api_key="$1"
  helper_run_id="$2"
  helper_marker="$3"
	preflight_action="$4"
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --entrypoint /usr/local/bin/eval-v2-memory \
    -e TEAM_MEMORY_BASE_URL=http://team-memory:8080 \
    -e TEAM_MEMORY_API_KEY="${helper_api_key}" \
    -e MEM0_BASE_URL=http://mem0:8000 \
    -e PAXM_USER_ID="${eval_user_id}" \
    -e PAXM_AGENT_ID=preflight \
    -e MEM0_RUN_ID="${helper_run_id}" \
    opencode -action "${preflight_action}" -marker "${helper_marker}"
}

run_raw_bm25() {
  batches_dir="$(dirname "${PAX_EVAL_SESSION_BATCHES_FILE}")"
  batches_file="$(basename "${PAX_EVAL_SESSION_BATCHES_FILE}")"
  batches_absolute="$(cd "${batches_dir}" && pwd -P)"
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --entrypoint /usr/local/bin/eval-v2-bm25 \
    --volume "${batches_absolute}:/artifact:ro" \
    opencode \
      -session-batches-file "/artifact/${batches_file}" \
      -query "${PAX_EVAL_QUESTION}" \
      -candidate-limit "${PAX_EVAL_BM25_CANDIDATE_LIMIT:-8}" \
      -token-budget "${PAX_EVAL_BM25_TOKEN_BUDGET:-500}" \
      -chunk-events "${PAX_EVAL_BM25_CHUNK_EVENTS:-4}" \
      -temporal-cutoff "${PAX_EVAL_BM25_TEMPORAL_CUTOFF:-}"
}

zep_api_key() {
  if [ -n "${ZEP_API_KEY:-}" ]; then
    printf '%s' "${ZEP_API_KEY}"
    return
  fi
  zep_config_path="${PAXM_CONFIG_PATH:-$(paxm config path)}"
  awk 'BEGIN { in_zep=0 } /^[[:space:]]{2}zep:/ { in_zep=1; next } in_zep && /^[[:space:]]{2}[A-Za-z0-9_-]+:/ { exit } in_zep && /^[[:space:]]{4}api_key:/ { sub(/^[[:space:]]*api_key:[[:space:]]*/, ""); print; exit }' "${zep_config_path}"
}

run_zep() {
  zep_key="$(zep_api_key)"
  if [ -z "${zep_key}" ]; then
    echo "Zep API key is unavailable from ZEP_API_KEY or paxm config" >&2
    exit 1
  fi
  if [ -n "${EVAL_V2_ZEP_BINARY:-}" ]; then
    ZEP_API_KEY="${zep_key}" "${EVAL_V2_ZEP_BINARY}" "$@"
    return
  fi
  ZEP_API_KEY="${zep_key}" GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" \
    go run ./cmd/eval-v2-zep "$@"
}

write_zep_artifact() {
  artifact_name="$1"
  artifact_payload="$2"
  mkdir -p "${PAX_EVAL_ARTIFACT_DIR}"
  printf '%s\n' "${artifact_payload}" > "${PAX_EVAL_ARTIFACT_DIR}/${artifact_name}"
}

write_zep_failure_artifact() {
  artifact_name="$1"
  action="$2"
  exit_code="$3"
  stdout_payload="$4"
  stderr_file="$5"
  mkdir -p "${PAX_EVAL_ARTIFACT_DIR}"
  jq -n \
    --arg action "${action}" \
    --argjson exit_code "${exit_code}" \
    --arg stdout "${stdout_payload}" \
    --rawfile stderr "${stderr_file}" \
    '{status:"failed",action:$action,exit_code:$exit_code,stdout:$stdout,stderr:$stderr}' \
    > "${PAX_EVAL_ARTIFACT_DIR}/${artifact_name}"
}

run_zep_with_artifact() {
  artifact_name="$1"
  action="$2"
  shift 2
  if [ -z "${PAX_EVAL_ARTIFACT_DIR:-}" ]; then
    run_zep "$@"
    return
  fi
  mkdir -p "${PAX_EVAL_ARTIFACT_DIR}"
  stderr_file="${PAX_EVAL_ARTIFACT_DIR}/${artifact_name%.json}.stderr.log"
  if zep_result="$(run_zep "$@" 2>"${stderr_file}")"; then
    write_zep_artifact "${artifact_name}" "${zep_result}"
    printf '%s\n' "${zep_result}"
    return
  fi
  exit_code=$?
  write_zep_failure_artifact "${artifact_name}" "${action}" "${exit_code}" "${zep_result:-}" "${stderr_file}"
  echo "Zep ${action} request failed; artifact=${PAX_EVAL_ARTIFACT_DIR}/${artifact_name}" >&2
  return "${exit_code}"
}

case "${stage}" in
  producer)
    producer_write_enabled=1
    if [ "${arm}" = "shared" ]; then
      producer_write_enabled=0
    fi
    run_agent "${PAX_EVAL_PRODUCER_WORKSPACE}" 0 "${producer_write_enabled}" \
      "Read source.md. Produce a complete factual handoff of every current decision, date, owner, dependency, and unresolved blocker. Preserve author identities and exact values." 0 passive
    ;;
  ingest)
    batches_dir="$(dirname "${PAX_EVAL_SESSION_BATCHES_FILE}")"
    batches_file="$(basename "${PAX_EVAL_SESSION_BATCHES_FILE}")"
    batches_absolute="$(cd "${batches_dir}" && pwd -P)"
    ingest_user_id="${eval_user_id}"
    ingest_agent_id="${agent_id}"
    ingest_provider="${arm}"
    ingest_require_write=0
    if [ "${arm}" = "mem0" ] || [ "${arm}" = "mem0_messages" ] || [ "${arm}" = "mem0_chunks" ]; then
      ingest_user_id="${MEM0_EVAL_USER_ID}"
      ingest_agent_id="${MEM0_EVAL_AGENT_ID}"
      ingest_require_write=1
    fi
    if [ "${arm}" = "team_note_hybrid" ]; then
      ingest_provider="team_note"
    fi
    if [ "${arm}" = "zep_native" ]; then
      zep_user_id="zep-eval-${PAX_EVAL_RUN_ID}-${case_id}"
      run_zep_with_artifact zep-ingest.json ingest -action ingest -user-id "${zep_user_id}" -session-batches-file "${batches_absolute}/${batches_file}"
      exit $?
    fi
    run_memory_ingest "${ingest_provider}" "${api_key}" "${ingest_user_id}" "${ingest_agent_id}" "${mem0_run_id}" \
      "${batches_absolute}" "/artifact/${batches_file}" "${ingest_require_write}"
    ;;
  preflight)
    if [ "${arm}" = "zep_native" ]; then
      zep_user_id="zep-eval-${PAX_EVAL_RUN_ID}-preflight"
      run_zep_with_artifact zep-preflight.json preflight -action preflight -user-id "${zep_user_id}"
      exit $?
    fi
    preflight_key="eval-${PAX_EVAL_RUN_ID}-preflight"
    preflight_run_id="${PAX_EVAL_RUN_ID}-preflight"
    preflight_action="preflight"
    if [ "${arm}" = "mem0" ] || [ "${arm}" = "mem0_messages" ] || [ "${arm}" = "mem0_chunks" ]; then
      preflight_action="preflight-mem0"
    fi
    run_memory_preflight "${preflight_key}" "${preflight_run_id}" "PAX-EVAL-PREFLIGHT-${PAX_EVAL_RUN_ID}" "${preflight_action}"
    ;;
  ready)
    if [ "${arm}" = "team_note" ] || [ "${arm}" = "team_note_hybrid" ]; then
      attempts=0
      readiness_attempts="${PAX_EVAL_READINESS_ATTEMPTS:-480}"
      while [ "${attempts}" -lt "${readiness_attempts}" ]; do
        ready="$(printf '%s' "SELECT CASE WHEN EXISTS (SELECT 1 FROM session_streams WHERE scope_id = :'scope_id') AND NOT EXISTS (SELECT 1 FROM session_streams WHERE scope_id = :'scope_id' AND (NOT complete OR extraction_cursor < last_sequence)) THEN 1 ELSE 0 END" | docker compose -p "${project_name}" -f "${compose_file}" exec -T postgres psql -U team_memory -d team_memory -v scope_id="${team_note_scope_id}" -At 2>/dev/null || true)"
        if [ "${ready:-0}" -eq 1 ] 2>/dev/null; then
          exit 0
        fi
        attempts=$((attempts + 1))
        sleep 1
      done
      echo "timed out waiting for all Team Note session streams after ${readiness_attempts} attempts" >&2
      exit 1
    fi
    if [ "${arm}" = "zep_native" ]; then
      attempts=0
      readiness_attempts="${PAX_EVAL_ZEP_READINESS_ATTEMPTS:-480}"
      zep_user_id="zep-eval-${PAX_EVAL_RUN_ID}-${case_id}"
      ingest_result_file="${PAX_EVAL_ARTIFACT_DIR}/ingest.log"
      if [ ! -f "${ingest_result_file}" ]; then
        echo "Zep ingest result is missing: ${ingest_result_file}" >&2
        exit 1
      fi
      accepted="$(jq -er '.accepted' "${ingest_result_file}")"
      if [ "${accepted}" -le 0 ]; then
        echo "Zep ingest accepted no episodes: artifact=${ingest_result_file}" >&2
        exit 1
      fi
      readiness_artifact="${PAX_EVAL_ARTIFACT_DIR}/zep-readiness.json"
      while [ "${attempts}" -lt "${readiness_attempts}" ]; do
        stderr_file="${PAX_EVAL_ARTIFACT_DIR}/zep-readiness.stderr.log"
        if readiness_result="$(run_zep -action ready -user-id "${zep_user_id}" 2>"${stderr_file}")"; then
          :
        else
          exit_code=$?
          jq -n --arg user_id "${zep_user_id}" --argjson accepted "${accepted}" --argjson attempts "${attempts}" \
            --argjson exit_code "${exit_code}" --rawfile stderr "${stderr_file}" \
            '{status:"failed",action:"ready",user_id:$user_id,accepted:$accepted,attempts:$attempts,exit_code:$exit_code,stderr:$stderr}' > "${readiness_artifact}"
          echo "Zep readiness request failed: user_id=${zep_user_id} attempts=${attempts} accepted=${accepted} artifact=${readiness_artifact}" >&2
          exit 1
        fi
        if ! episodes="$(printf '%s' "${readiness_result}" | jq -er '.episodes')"; then
          jq -n --arg response "${readiness_result}" --argjson accepted "${accepted}" --argjson attempts "${attempts}" \
            '{status:"failed",action:"ready",accepted:$accepted,attempts:$attempts,response:$response}' > "${readiness_artifact}"
          echo "Zep readiness response lacks episodes: artifact=${readiness_artifact}" >&2
          exit 1
        fi
        if processed="$(printf '%s' "${readiness_result}" | jq -er '.processed')"; then
          processing_reported=1
        else
          processed=0
          processing_reported=0
        fi
        attempts=$((attempts + 1))
        if [ "${processing_reported}" -eq 1 ]; then
          printf '%s' "${readiness_result}" | jq --argjson accepted "${accepted}" --argjson attempts "${attempts}" \
            '. + {accepted: $accepted, attempts: $attempts}' > "${readiness_artifact}"
        else
          printf '%s' "${readiness_result}" | jq --argjson accepted "${accepted}" --argjson attempts "${attempts}" \
            '. + {accepted: $accepted, attempts: $attempts, processing_status: "unreported"}' > "${readiness_artifact}"
        fi
        if [ "${episodes}" -ge "${accepted}" ] && { [ "${processing_reported}" -eq 0 ] || [ "${episodes}" -eq "${processed}" ]; }; then
          exit 0
        fi
        sleep 1
      done
      echo "timed out waiting for Zep graph processing: accepted=${accepted} episodes=${episodes} processed=${processed} attempts=${attempts} artifact=${readiness_artifact}" >&2
      exit 1
    fi
    ;;
  consumer)
    consumer_recall_enabled=1
    consumer_recall_mode=passive
    consumer_prompt="${PAX_EVAL_QUESTION}"
    if [ "${arm}" = "control" ]; then
      consumer_recall_enabled=0
    fi
    if [ "${arm}" = "direct_context" ]; then
      consumer_recall_enabled=0
      consumer_recall_mode=direct
      source_file="${PAX_EVAL_PRODUCER_WORKSPACE}/source.md"
      if [ ! -f "${source_file}" ]; then
        echo "direct context source is missing: ${source_file}" >&2
        exit 1
      fi
      consumer_prompt="$(printf 'Asking user: %s\n\nQuestion:\n%s\n\nRetrieved conversation passages:\n' "${eval_user_id}" "${PAX_EVAL_QUESTION}"; cat "${source_file}")"
    fi
    if [ "${arm}" = "raw_bm25" ]; then
      consumer_recall_enabled=0
      consumer_recall_mode=direct
      bm25_result="$(run_raw_bm25)"
      mkdir -p "${PAX_EVAL_ARTIFACT_DIR}"
      printf '%s\n' "${bm25_result}" > "${PAX_EVAL_ARTIFACT_DIR}/raw-bm25.json"
      bm25_context="$(printf '%s' "${bm25_result}" | jq -er '.context')"
      consumer_prompt="$(printf 'Asking user: %s\n\nQuestion:\n%s\n\nRetrieved conversation passages:\n%s' "${eval_user_id}" "${PAX_EVAL_QUESTION}" "${bm25_context}")"
    fi
    if [ "${arm}" = "zep_native" ]; then
      consumer_recall_enabled=0
      consumer_recall_mode=direct
      zep_user_id="zep-eval-${PAX_EVAL_RUN_ID}-${case_id}"
      mkdir -p "${PAX_EVAL_ARTIFACT_DIR}"
      if zep_result="$(run_zep -action search -user-id "${zep_user_id}" -query "${PAX_EVAL_QUESTION}" -max-characters "${PAX_EVAL_ZEP_MAX_CHARACTERS:-2000}" 2>"${PAX_EVAL_ARTIFACT_DIR}/zep-native.stderr.log")"; then
        :
      else
        exit_code=$?
        write_zep_failure_artifact zep-native.json search "${exit_code}" "${zep_result:-}" "${PAX_EVAL_ARTIFACT_DIR}/zep-native.stderr.log"
        echo "Zep search request failed; artifact=${PAX_EVAL_ARTIFACT_DIR}/zep-native.json" >&2
        exit "${exit_code}"
      fi
      write_zep_artifact zep-native.json "${zep_result}"
      zep_context="$(printf '%s' "${zep_result}" | jq -er '.context')"
      printf '%s' "${zep_result}" | jq '{provider, episodes, context_characters: (.context | length)} + if has("processed") then {processed} else {} end' \
        > "${PAX_EVAL_ARTIFACT_DIR}/zep-native-summary.json"
      consumer_prompt="$(printf 'Asking user: %s\n\nQuestion:\n%s\n\nRetrieved conversation passages:\n%s' "${eval_user_id}" "${PAX_EVAL_QUESTION}" "${zep_context}")"
    fi
    if [ "${arm}" = "team_note_hybrid" ]; then
      consumer_recall_mode=hybrid
    fi
    run_agent "${PAX_EVAL_CONSUMER_WORKSPACE}" "${consumer_recall_enabled}" 0 "${consumer_prompt}" 1 "${consumer_recall_mode}"
    ;;
  *)
    echo "unsupported stage: ${stage}" >&2
    exit 1
    ;;
esac
