#!/bin/sh
set -eu

output="${1:-runs/groupmembench-v3-selection}"
domain="${GROUPMEMBENCH_DOMAIN:-Finance}"
seed="${GROUPMEMBENCH_SEED:-pax-eval-v3}"
per_category="${GROUPMEMBENCH_PER_CATEGORY:-5}"
total_cases="${GROUPMEMBENCH_TOTAL_CASES:-0}"
dataset_dir=".build/datasets/groupmembench/${domain}"

./scripts/fetch-groupmembench.sh "${domain}"
revision="$(tr -d '\n' < "${dataset_dir}/REVISION")"

if command -v groupmembench-select >/dev/null 2>&1; then
  selector=groupmembench-select
else
  selector="go run ./cmd/groupmembench-select"
fi

# shellcheck disable=SC2086
GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" ${selector} \
  -mode full-domain \
  -conversation "${dataset_dir}/synthetic_domain_channels_rolevariants_${domain}.json" \
  -questions "${dataset_dir}/questions" \
  -output "${output}" \
  -domain "${domain}" \
  -revision "${revision}" \
  -seed "${seed}" \
  -per-category "${per_category}" \
  -total-cases "${total_cases}"

echo "Eval v3 full-domain batches: ${output}/domain/producer/session-batches.json"
echo "Eval v3 smoke manifest: ${output}/manifest.smoke.json"
echo "Eval v3 acceptance manifest: ${output}/manifest.json"
