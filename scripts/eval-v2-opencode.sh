#!/bin/sh
set -eu

stage="${1:?stage is required}"
arm="${2:?arm is required}"
compose_file="evals/v2/compose.yaml"
project_name="pax-nexus-eval-v2"
api_key="eval-${PAX_EVAL_RUN_ID}-${PAX_EVAL_CASE_ID}"
agent_id="${arm}-${stage}-${PAX_EVAL_CASE_ID}"
mem0_run_id="${PAX_EVAL_RUN_ID}-${PAX_EVAL_CASE_ID}"
team_note_scope_id="${PAX_EVAL_RUN_ID}-${PAX_EVAL_SCOPE_ID}"

if [ -f .env ]; then
  set -a
  . ./.env
  set +a
fi

run_agent() {
  workspace="$1"
  recall_enabled="$2"
  write_enabled="$3"
  prompt="$4"
  provider_type="team-memory"
  if [ "${arm}" = "mem0" ]; then
    provider_type="mem0"
  fi
  docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
    --volume "${workspace}:/workspace:ro" \
    -e PAXM_PROVIDER_TYPE="${provider_type}" \
    -e TEAM_MEMORY_API_KEY="${api_key}" \
    -e PAXM_USER_ID="${PAX_EVAL_USER_ID}" \
    -e PAXM_AGENT_ID="${agent_id}" \
    -e MEM0_RUN_ID="${mem0_run_id}" \
    -e PAXM_RECALL_ENABLED="${recall_enabled}" \
    -e PAXM_WRITE_ENABLED="${write_enabled}" \
    opencode run --format json --model "${OPENCODE_MODEL}" "${prompt}"
}

case "${stage}" in
  producer)
    run_agent "${PAX_EVAL_PRODUCER_WORKSPACE}" 0 1 \
      "Read source.md. Produce a complete factual handoff of every current decision, date, owner, dependency, and unresolved blocker. Preserve author identities and exact values."
    ;;
  ready)
    if [ "${arm}" = "team_note" ]; then
      attempts=0
      while [ "${attempts}" -lt 120 ]; do
        ready="$(docker compose -p "${project_name}" -f "${compose_file}" exec -T postgres psql -U team_memory -d team_memory -v scope_id="${team_note_scope_id}" -Atc "SELECT CASE WHEN EXISTS (SELECT 1 FROM session_streams WHERE scope_id = :'scope_id' AND complete AND extraction_cursor >= last_sequence) THEN 1 ELSE 0 END" 2>/dev/null || true)"
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
    run_agent "${PAX_EVAL_CONSUMER_WORKSPACE}" 1 0 \
      "${PAX_EVAL_QUESTION} Answer directly and concisely without explaining your reasoning."
    ;;
  *)
    echo "unsupported stage: ${stage}" >&2
    exit 1
    ;;
esac
