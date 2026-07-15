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

compose_file="evals/opencode/compose.yaml"
run_id="$(date -u +%Y%m%dT%H%M%SZ)"
run_dir="runs/${run_id}"
project_name="team-memory-eval-$(date -u +%s)-$$"
cleanup_done=0
mkdir -p "${run_dir}"

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
  attempts=0
  while [ "${attempts}" -lt 60 ]; do
    count="$(run_compose exec -T postgres psql -U team_memory -d team_memory -Atc "SELECT COUNT(*) FROM team_notes" 2>/dev/null || true)"
    if [ "${count}" -ge 1 ] 2>/dev/null; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  echo "timed out waiting for Team Note extraction" >&2
  return 1
}

run_compose up -d --build postgres team-memory

producer_prompt="Read brief.md. Report the exact release approval code and why it matters."
consumer_prompt="What is the exact July release approval code? Answer only the code."

run_compose run --rm producer \
  run --format json --model "${OPENCODE_MODEL}" "${producer_prompt}" \
  > "${run_dir}/producer.jsonl" 2> "${run_dir}/producer-stderr.log"

wait_for_notes

producer_identity="$(run_compose exec -T postgres psql -U team_memory -d team_memory -Atc \
  "SELECT user_id || '|' || agent_id FROM session_events ORDER BY sequence LIMIT 1")"
if [ "${producer_identity}" != "eval-owner|opencode-producer" ]; then
  echo "unexpected producer identity: ${producer_identity}" >&2
  exit 1
fi

run_compose run --rm \
  -e PAXM_RECALL_ENABLED=0 -e PAXM_WRITE_ENABLED=0 consumer \
  run --format json --model "${OPENCODE_MODEL}" "${consumer_prompt}" \
  > "${run_dir}/control.jsonl" 2> "${run_dir}/control-stderr.log"

run_compose run --rm \
  -e PAXM_RECALL_ENABLED=1 -e PAXM_WRITE_ENABLED=0 consumer \
  run --format json --model "${OPENCODE_MODEL}" "${consumer_prompt}" \
  > "${run_dir}/extracted-notes.jsonl" 2> "${run_dir}/extracted-notes-stderr.log"

go run ./cmd/team-memory-eval -arm control -expected ORBIT-731 \
  -input "${run_dir}/control.jsonl" > "${run_dir}/control-score.json"
go run ./cmd/team-memory-eval -arm extracted_notes -expected ORBIT-731 \
  -input "${run_dir}/extracted-notes.jsonl" > "${run_dir}/extracted-notes-score.json"

cat > "${run_dir}/manifest.json" <<EOF
{
  "run_id": "${run_id}",
  "opencode_version": "${OPENCODE_VERSION:-1.17.20}",
  "model": "${OPENCODE_MODEL}",
  "extractor_model": "${TEAM_MEMORY_EXTRACTOR_MODEL:-gpt-4.1-mini}",
  "expected": "ORBIT-731",
  "arms": ["control", "extracted_notes"]
}
EOF

echo "eval artifacts: ${run_dir}"
