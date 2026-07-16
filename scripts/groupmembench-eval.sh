#!/bin/sh
set -eu

if [ -f .env ]; then
  set -a
  . ./.env
  set +a
fi

: "${OPENCODE_MODEL:?OPENCODE_MODEL is required}"
: "${TEAM_MEMORY_EXTRACTOR_API_KEY:?TEAM_MEMORY_EXTRACTOR_API_KEY is required}"
: "${PAXM_SOURCE_DIR:=${HOME}/Documents/memory-adaptor}"
export PAXM_SOURCE_DIR

domain="Finance"
parallelism="${GROUPMEMBENCH_PARALLELISM:-4}"
selection_seed="${GROUPMEMBENCH_SEED:-team-memory-v1}"
categories="${GROUPMEMBENCH_CATEGORIES:-}"
compose_file="evals/opencode/compose.yaml"
run_id="$(date -u +%Y%m%dT%H%M%SZ)"
run_dir="runs/groupmembench-${run_id}"
project_name="team-memory-groupmembench-$(date -u +%s)-$$"
cleanup_done=0
dataset_dir=".build/datasets/groupmembench/${domain}"
conversation="${dataset_dir}/synthetic_domain_channels_rolevariants_${domain}.json"
questions_dir="${dataset_dir}/questions"
selector_binary="${run_dir}/groupmembench-select"
score_binary="${run_dir}/team-memory-eval"
mkdir -p "${run_dir}"

./scripts/fetch-groupmembench.sh "${domain}"
dataset_revision="$(tr -d '\n' < "${dataset_dir}/REVISION")"

go build -trimpath -o "${selector_binary}" ./cmd/groupmembench-select
go build -trimpath -o "${score_binary}" ./cmd/team-memory-eval
"${selector_binary}" \
  -conversation "${conversation}" \
  -questions "${questions_dir}" \
  -output "${run_dir}" \
  -domain "${domain}" \
  -revision "${dataset_revision}" \
  -seed "${selection_seed}" \
  -per-category 2 \
  -top-k 8 \
  -neighbor-radius 1 \
  -max-context-messages 32

if [ -n "${categories}" ]; then
  jq --arg categories "${categories}" \
    '.cases |= map(select(.category as $category | ($categories | split(",") | index($category))))' \
    "${run_dir}/manifest.json" > "${run_dir}/manifest.filtered.json"
  mv "${run_dir}/manifest.filtered.json" "${run_dir}/manifest.json"
fi
case_count="$(jq '.cases | length' "${run_dir}/manifest.json")"
if [ "${case_count}" -eq 0 ]; then
  echo "GroupMemBench category filter selected no cases" >&2
  exit 1
fi

jq -n \
  --arg run_id "${run_id}" \
  --arg model "${OPENCODE_MODEL}" \
  --arg extractor_model "${TEAM_MEMORY_EXTRACTOR_MODEL:-gpt-4.1-mini}" \
  --arg dataset_revision "${dataset_revision}" \
  --arg seed "${selection_seed}" \
  --arg categories "${categories}" \
  --argjson parallelism "${parallelism}" \
  '{run_id:$run_id,model:$model,extractor_model:$extractor_model,dataset_revision:$dataset_revision,seed:$seed,categories:$categories,parallelism:$parallelism,per_category:2,top_k:8,neighbor_radius:1,max_context_messages:32}' \
  > "${run_dir}/run-config.json"

TEAM_MEMORY_API_KEYS="$(jq -c 'reduce .cases[] as $case ({}; .["eval-" + $case.id] = $case.scope_id)' "${run_dir}/manifest.json")"
export TEAM_MEMORY_API_KEYS

run_compose() {
  docker compose -p "${project_name}" -f "${compose_file}" "$@"
}

cleanup() {
  if [ "${cleanup_done}" -eq 1 ]; then
    return
  fi
  cleanup_done=1
  run_compose logs --no-color team-memory > "${run_dir}/team-memory.log" 2>&1 || true
  run_compose exec -T postgres psql -U team_memory -d team_memory -Atc \
    "SELECT 'events=' || COUNT(*) FROM session_events; SELECT 'runs=' || COUNT(*) FROM extraction_runs; SELECT 'candidates=' || COUNT(*) FROM note_candidates; SELECT 'notes=' || COUNT(*) FROM team_notes; SELECT 'deliveries=' || COUNT(*) FROM note_deliveries;" \
    > "${run_dir}/database-counts.txt" 2>&1 || true
  run_compose down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

wait_for_notes() {
  scope_id="$1"
  attempts=0
  while [ "${attempts}" -lt 120 ]; do
    count="$(run_compose exec -T postgres psql -U team_memory -d team_memory -Atc "SELECT COUNT(*) FROM team_notes WHERE scope_id = '${scope_id}'" 2>/dev/null || true)"
    if [ "${count}" -ge 1 ] 2>/dev/null; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  echo "timed out waiting for Team Note extraction in ${scope_id}" >&2
  return 1
}

assert_producer_identity() {
  scope_id="$1"
  expected_user_id="$2"
  expected_agent_id="$3"
  identity="$(run_compose exec -T postgres psql -U team_memory -d team_memory -Atc \
    "SELECT user_id || '|' || agent_id FROM session_events WHERE scope_id = '${scope_id}' ORDER BY sequence LIMIT 1")"
  if [ "${identity}" != "${expected_user_id}|${expected_agent_id}" ]; then
    echo "unexpected producer identity in ${scope_id}: ${identity}" >&2
    return 1
  fi
}

run_case() {
  case_id="$1"
  echo "starting GroupMemBench case ${case_id}" >&2
  case_dir="${run_dir}/cases/${case_id}"
  producer_workspace="${PWD}/${case_dir}/producer"
  consumer_workspace="${PWD}/${case_dir}/consumer"
  question="$(jq -r --arg id "${case_id}" '.cases[] | select(.id == $id) | .question' "${run_dir}/manifest.json")"
  expected="$(jq -r --arg id "${case_id}" '.cases[] | select(.id == $id) | .answer' "${run_dir}/manifest.json")"
  asking_user_id="$(jq -r --arg id "${case_id}" '.cases[] | select(.id == $id) | .asking_user_id' "${run_dir}/manifest.json")"
  scope_id="$(jq -r --arg id "${case_id}" '.cases[] | select(.id == $id) | .scope_id' "${run_dir}/manifest.json")"
  api_key="eval-${case_id}"
  agent_suffix="$(printf '%s' "${case_id}" | tr '_' '-')"
  producer_agent_id="producer-${agent_suffix}"
  control_agent_id="control-${agent_suffix}"
  memory_agent_id="memory-${agent_suffix}"
  producer_prompt="Read source.md. Produce a complete factual handoff of every current decision, date, owner, dependency, and unresolved blocker. Preserve message author identities and exact values. Do not omit facts because they seem unrelated."
  consumer_prompt="${question} Answer directly and concisely without explaining your reasoning. Only if the question requests an exact owner, name, date, time, timestamp, version, count, or value, require the available evidence to state that exact slot for the same subject; if that slot is missing, state that the information is unavailable. For all other question types, answer normally from the available evidence."

  run_compose run --rm --no-deps \
    --volume "${producer_workspace}:/workspace:ro" \
    -e TEAM_MEMORY_API_KEY="${api_key}" \
    -e PAXM_USER_ID="${asking_user_id}" \
    -e PAXM_AGENT_ID="${producer_agent_id}" \
    -e PAXM_RECALL_ENABLED=0 -e PAXM_WRITE_ENABLED=1 producer \
    run --format json --model "${OPENCODE_MODEL}" "${producer_prompt}" \
    > "${case_dir}/producer.jsonl" 2> "${case_dir}/producer-stderr.log" || return 1

  wait_for_notes "${scope_id}" || return 1
  assert_producer_identity "${scope_id}" "${asking_user_id}" "${producer_agent_id}" || return 1
  run_compose exec -T postgres psql -U team_memory -d team_memory -Atc \
    "SELECT json_build_object('note_id', note_id, 'kind', kind, 'subject', subject, 'body', body, 'related_subjects', related_subjects, 'valid_at', valid_at, 'invalid_at', invalid_at) FROM team_notes WHERE scope_id = '${scope_id}' ORDER BY subject" \
    > "${case_dir}/admitted-notes.jsonl" || return 1

  run_compose run --rm --no-deps \
    --volume "${consumer_workspace}:/workspace:ro" \
    -e TEAM_MEMORY_API_KEY="${api_key}" \
    -e PAXM_USER_ID="${asking_user_id}" \
    -e PAXM_AGENT_ID="${control_agent_id}" \
    -e PAXM_RECALL_ENABLED=0 -e PAXM_WRITE_ENABLED=0 consumer \
    run --format json --model "${OPENCODE_MODEL}" "${consumer_prompt}" \
    > "${case_dir}/control.jsonl" 2> "${case_dir}/control-stderr.log" || return 1

  run_compose run --rm --no-deps \
    --volume "${consumer_workspace}:/workspace:ro" \
    -e TEAM_MEMORY_API_KEY="${api_key}" \
    -e PAXM_USER_ID="${asking_user_id}" \
    -e PAXM_AGENT_ID="${memory_agent_id}" \
    -e PAXM_RECALL_ENABLED=1 -e PAXM_WRITE_ENABLED=0 consumer \
    run --format json --model "${OPENCODE_MODEL}" "${consumer_prompt}" \
    > "${case_dir}/extracted-notes.jsonl" 2> "${case_dir}/extracted-notes-stderr.log" || return 1

  run_compose exec -T postgres psql -U team_memory -d team_memory -Atc \
    "SELECT json_build_object('note_id', note.note_id, 'revision', delivery.revision, 'kind', note.kind, 'subject', note.subject, 'body', note.body, 'related_subjects', note.related_subjects, 'valid_at', note.valid_at, 'invalid_at', note.invalid_at, 'delivered_at', delivery.delivered_at) FROM note_deliveries delivery JOIN team_notes note ON note.scope_id = delivery.scope_id AND note.note_id = delivery.note_id WHERE delivery.scope_id = '${scope_id}' AND delivery.recipient_agent_id = '${memory_agent_id}' ORDER BY delivery.delivered_at" \
    > "${case_dir}/delivered-notes.jsonl" || return 1

  "${score_binary}" -arm control -expected "${expected}" \
    -input "${case_dir}/control.jsonl" > "${case_dir}/control-score.json" 2> "${case_dir}/control-score.log" || return 1
  "${score_binary}" -arm extracted_notes -expected "${expected}" \
    -input "${case_dir}/extracted-notes.jsonl" > "${case_dir}/extracted-notes-score.json" 2> "${case_dir}/extracted-notes-score.log" || return 1
  echo "completed GroupMemBench case ${case_id}" >&2
}

run_compose build producer consumer
./scripts/start-local-embedding.sh -p "${project_name}" -f "${compose_file}"
run_compose up -d --build postgres team-memory

running=0
for case_id in $(jq -r '.cases[].id' "${run_dir}/manifest.json"); do
  (
    run_case "${case_id}" || {
      echo "case ${case_id} failed" >&2
      touch "${run_dir}/cases/${case_id}/failed"
    }
  ) &
  running=$((running + 1))
  if [ "${running}" -ge "${parallelism}" ]; then
    wait
    running=0
  fi
done
wait

if find "${run_dir}/cases" -name failed -print | grep -q .; then
  echo "one or more GroupMemBench cases failed; artifacts: ${run_dir}" >&2
  exit 1
fi

: > "${run_dir}/results.jsonl"
for case_id in $(jq -r '.cases[].id' "${run_dir}/manifest.json"); do
  case_dir="${run_dir}/cases/${case_id}"
  jq -cn \
    --argjson metadata "$(jq -c --arg id "${case_id}" '.cases[] | select(.id == $id)' "${run_dir}/manifest.json")" \
    --slurpfile control "${case_dir}/control-score.json" \
    --slurpfile memory "${case_dir}/extracted-notes-score.json" \
    '{id:$metadata.id, category:$metadata.category, asking_user_id:$metadata.asking_user_id, context_messages:$metadata.context_messages, control:$control[0], extracted_notes:$memory[0]}' \
    >> "${run_dir}/results.jsonl"
done

jq -s '{
  cases: length,
  control_exact: ([.[] | select(.control.exact)] | length),
  extracted_notes_exact: ([.[] | select(.extracted_notes.exact)] | length),
  exact_lift: (([.[] | select(.extracted_notes.exact)] | length) - ([.[] | select(.control.exact)] | length)),
  control_mean_token_f1: ((map(.control.token_f1) | add) / length),
  extracted_notes_mean_token_f1: ((map(.extracted_notes.token_f1) | add) / length),
  by_category: (group_by(.category) | map({
    category: .[0].category,
    cases: length,
    control_exact: ([.[] | select(.control.exact)] | length),
    extracted_notes_exact: ([.[] | select(.extracted_notes.exact)] | length),
    control_mean_token_f1: ((map(.control.token_f1) | add) / length),
    extracted_notes_mean_token_f1: ((map(.extracted_notes.token_f1) | add) / length)
  }))
}' "${run_dir}/results.jsonl" > "${run_dir}/summary.json"

echo "GroupMemBench ${case_count}-case eval artifacts: ${run_dir}"
cat "${run_dir}/summary.json"
