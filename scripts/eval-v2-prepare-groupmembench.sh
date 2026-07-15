#!/bin/sh
set -eu

output="${1:-runs/groupmembench-v2-selection}"
domain="${GROUPMEMBENCH_DOMAIN:-Finance}"
seed="${GROUPMEMBENCH_SEED:-pax-eval-v2}"
per_category="${GROUPMEMBENCH_PER_CATEGORY:-5}"
dataset_dir=".build/datasets/groupmembench/${domain}"

./scripts/fetch-groupmembench.sh "${domain}"
revision="$(tr -d '\n' < "${dataset_dir}/REVISION")"
GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" go run ./cmd/groupmembench-select \
  -conversation "${dataset_dir}/synthetic_domain_channels_rolevariants_${domain}.json" \
  -questions "${dataset_dir}/questions" \
  -output "${output}" \
  -domain "${domain}" \
  -revision "${revision}" \
  -seed "${seed}" \
  -per-category "${per_category}" \
  -top-k "${GROUPMEMBENCH_TOP_K:-8}" \
  -neighbor-radius "${GROUPMEMBENCH_NEIGHBOR_RADIUS:-1}" \
  -max-context-messages "${GROUPMEMBENCH_MAX_CONTEXT_MESSAGES:-32}"

echo "Eval v2 manifest: ${output}/manifest.json"
