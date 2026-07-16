#!/bin/sh
set -eu

output="${1:-runs/groupmembench-v2-selection}"
domain="${GROUPMEMBENCH_DOMAIN:-Finance}"
seed="${GROUPMEMBENCH_SEED:-pax-eval-v2}"
per_category="${GROUPMEMBENCH_PER_CATEGORY:-5}"
total_cases="${GROUPMEMBENCH_TOTAL_CASES:-0}"
dataset_dir=".build/datasets/groupmembench/${domain}"

./scripts/fetch-groupmembench.sh "${domain}"
revision="$(tr -d '\n' < "${dataset_dir}/REVISION")"
run_selector() {
  if command -v groupmembench-select >/dev/null 2>&1; then
    groupmembench-select "$@"
    return
  fi
  GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" go run ./cmd/groupmembench-select "$@"
}

run_selector \
  -conversation "${dataset_dir}/synthetic_domain_channels_rolevariants_${domain}.json" \
  -questions "${dataset_dir}/questions" \
  -output "${output}" \
  -domain "${domain}" \
  -revision "${revision}" \
  -seed "${seed}" \
  -per-category "${per_category}" \
  -total-cases "${total_cases}" \
  -top-k "${GROUPMEMBENCH_TOP_K:-8}" \
  -neighbor-radius "${GROUPMEMBENCH_NEIGHBOR_RADIUS:-1}" \
  -max-context-messages "${GROUPMEMBENCH_MAX_CONTEXT_MESSAGES:-32}"

echo "Eval v2 smoke manifest: ${output}/manifest.smoke.json"
echo "Eval v2 acceptance manifest: ${output}/manifest.json"
