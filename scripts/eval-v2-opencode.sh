#!/bin/sh
set -eu

stage="${1:?stage is required}"
arm="${2:?arm is required}"
compose_file="evals/v2/compose.yaml"
project_name="pax-nexus-eval-v2"
case_id="${PAX_EVAL_CASE_ID:-preflight}"
eval_user_id="${PAX_EVAL_USER_ID:-eval-owner}"
scope_id="${PAX_EVAL_SCOPE_ID:-preflight}"
api_key="eval-${PAX_EVAL_RUN_ID}-${case_id}"
agent_id="${arm}-${stage}-${case_id}"
mem0_run_id="${PAX_EVAL_RUN_ID}-${case_id}"
team_note_scope_id="${PAX_EVAL_RUN_ID}-${scope_id}"

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
  opencode_agent="build"
  if [ "${consumer_policy}" = "1" ]; then
    opencode_agent="eval-consumer"
  fi
  provider_type="team-memory"
  if [ "${arm}" = "mem0" ]; then
    provider_type="mem0"
  fi
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --volume "${workspace}:/workspace:ro" \
    -e PAXM_PROVIDER_TYPE="${provider_type}" \
    -e TEAM_MEMORY_API_KEY="${api_key}" \
    -e PAXM_USER_ID="${eval_user_id}" \
    -e PAXM_AGENT_ID="${agent_id}" \
    -e MEM0_RUN_ID="${mem0_run_id}" \
    -e MEM0_SCORE_SEMANTICS="${MEM0_SCORE_SEMANTICS:-distance}" \
    -e PAXM_EXPECTED_VERSION="${PAXM_EXPECTED_VERSION:-v0.1.28}" \
    -e PAXM_RECALL_ENABLED="${recall_enabled}" \
    -e PAXM_WRITE_ENABLED="${write_enabled}" \
    -e PAXM_EVAL_CONSUMER_POLICY="${consumer_policy}" \
    -e PAXM_PASSIVE_MIN_RELEVANCE="${PAXM_PASSIVE_MIN_RELEVANCE:--1}" \
    -e PAXM_PASSIVE_MIN_SCORE="${PAXM_PASSIVE_MIN_SCORE:--1}" \
    -e PAXM_INSERTION_MIN_SCORE="${PAXM_INSERTION_MIN_SCORE:-0}" \
    -e PAXM_EVAL_DIAGNOSTICS="${PAXM_EVAL_DIAGNOSTICS:-1}" \
    opencode run --agent "${opencode_agent}" --format json --model "${OPENCODE_MODEL}" "${prompt}"
}

run_memory_ingest() {
  helper_provider="$1"
  helper_api_key="$2"
  helper_agent_id="$3"
  helper_run_id="$4"
  helper_mount="$5"
  helper_file="$6"
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --entrypoint /usr/local/bin/eval-v2-memory \
    --volume "${helper_mount}:/artifact:ro" \
    -e TEAM_MEMORY_BASE_URL=http://team-memory:8080 \
    -e TEAM_MEMORY_API_KEY="${helper_api_key}" \
    -e MEM0_BASE_URL=http://mem0:8000 \
    -e PAXM_USER_ID="${eval_user_id}" \
    -e PAXM_AGENT_ID="${helper_agent_id}" \
    -e MEM0_RUN_ID="${helper_run_id}" \
    opencode -action ingest -provider "${helper_provider}" -text-file "${helper_file}"
}

run_memory_preflight() {
  helper_api_key="$1"
  helper_run_id="$2"
  helper_marker="$3"
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --entrypoint /usr/local/bin/eval-v2-memory \
    -e TEAM_MEMORY_BASE_URL=http://team-memory:8080 \
    -e TEAM_MEMORY_API_KEY="${helper_api_key}" \
    -e MEM0_BASE_URL=http://mem0:8000 \
    -e PAXM_USER_ID="${eval_user_id}" \
    -e PAXM_AGENT_ID=preflight \
    -e MEM0_RUN_ID="${helper_run_id}" \
    opencode -action preflight -marker "${helper_marker}"
}

case "${stage}" in
  producer)
    producer_write_enabled=1
    if [ "${arm}" = "shared" ]; then
      producer_write_enabled=0
    fi
    run_agent "${PAX_EVAL_PRODUCER_WORKSPACE}" 0 "${producer_write_enabled}" \
      "Read source.md. Produce a complete factual handoff of every current decision, date, owner, dependency, and unresolved blocker. Preserve author identities and exact values." 0
    ;;
  ingest)
    shared_dir="$(dirname "${PAX_EVAL_SHARED_PRODUCER_TEXT}")"
    shared_file="$(basename "${PAX_EVAL_SHARED_PRODUCER_TEXT}")"
    shared_absolute="$(cd "${shared_dir}" && pwd -P)"
    run_memory_ingest "${arm}" "${api_key}" "shared-producer-${case_id}" "${mem0_run_id}" \
      "${shared_absolute}" "/artifact/${shared_file}"
    ;;
  preflight)
    preflight_key="eval-${PAX_EVAL_RUN_ID}-preflight"
    preflight_run_id="${PAX_EVAL_RUN_ID}-preflight"
    run_memory_preflight "${preflight_key}" "${preflight_run_id}" "PAX-EVAL-PREFLIGHT-${PAX_EVAL_RUN_ID}"
    ;;
  ready)
    if [ "${arm}" = "team_note" ]; then
      attempts=0
      while [ "${attempts}" -lt 120 ]; do
        ready="$(printf '%s' "SELECT CASE WHEN EXISTS (SELECT 1 FROM session_streams WHERE scope_id = :'scope_id' AND complete AND extraction_cursor >= last_sequence) THEN 1 ELSE 0 END" | docker compose -p "${project_name}" -f "${compose_file}" exec -T postgres psql -U team_memory -d team_memory -v scope_id="${team_note_scope_id}" -At 2>/dev/null || true)"
        if [ "${ready:-0}" -eq 1 ] 2>/dev/null; then
          exit 0
        fi
        attempts=$((attempts + 1))
        sleep 1
      done
      echo "timed out waiting for Team Note extraction" >&2
      exit 1
    fi
    ;;
  consumer)
    consumer_recall_enabled=1
    if [ "${arm}" = "control" ]; then
      consumer_recall_enabled=0
    fi
    run_agent "${PAX_EVAL_CONSUMER_WORKSPACE}" "${consumer_recall_enabled}" 0 "${PAX_EVAL_QUESTION}" 1
    ;;
  *)
    echo "unsupported stage: ${stage}" >&2
    exit 1
    ;;
esac
