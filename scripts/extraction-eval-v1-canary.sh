#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

source_run_id=${1:-}
run_prefix=${2:-}
if [[ -z "$source_run_id" || -z "$run_prefix" ]]; then
  echo "usage: scripts/extraction-eval-v1-canary.sh <source-run-id> <run-prefix>" >&2
  exit 2
fi

. ./scripts/load-extraction-eval-v1-env.sh

dsn=${EXTRACTION_EVAL_DSN:-${EVAL_V2_POSTGRES_DSN:-}}
if [[ -z "$dsn" ]]; then
  echo "EXTRACTION_EVAL_DSN or EVAL_V2_POSTGRES_DSN is required" >&2
  exit 2
fi
if [[ -z "${TEAM_MEMORY_EXTRACTOR_BASE_URL:-}" || -z "${TEAM_MEMORY_EXTRACTOR_MODEL:-}" ]]; then
  echo "TEAM_MEMORY_EXTRACTOR_BASE_URL and TEAM_MEMORY_EXTRACTOR_MODEL are required" >&2
  exit 2
fi
if [[ -z "${TEAM_MEMORY_EXTRACTOR_API_KEY:-}" ]]; then
  echo "TEAM_MEMORY_EXTRACTOR_API_KEY is required" >&2
  exit 2
fi

manifest=evals/extraction-v1/groupmembench-finance-micro6.manifest.json
fixtures=evals/extraction-v1/groupmembench-finance-micro6.json
profile=evals/extraction-v1/profiles/finance-micro3-quick.json

go run ./cmd/team-memory-extraction-eval-v1 \
  -dsn "$dsn" \
  -manifest "$manifest" \
  -fixtures "$fixtures" \
  -profile "$profile" \
  -source-run-id "$source_run_id" \
  -run-id "$run_prefix-preflight" \
  -preflight-only

for variant in current interaction-slim; do
  run_id="$run_prefix-$variant"
  output_dir="runs/extraction-eval-v1/$run_id"
  resume_args=()
  if [[ -f "$output_dir/report.json" ]]; then
    echo "$variant arm already complete: $output_dir/report.json"
    continue
  fi
  if [[ -d "$output_dir" ]]; then
    resume_args=(-resume)
  fi
  go run ./cmd/team-memory-extraction-eval-v1 \
    -dsn "$dsn" \
    -manifest "$manifest" \
    -fixtures "$fixtures" \
    -profile "$profile" \
    -source-run-id "$source_run_id" \
    -run-id "$run_id" \
    -extractor v2 \
    -v2-variant "$variant" \
    ${resume_args[@]+"${resume_args[@]}"}
done

jq -s '
  map({
    variant: .v2_variant,
    fact_recall: .extraction_summary.fact_recall,
    leakage: .extraction_summary.leakage_items,
    slices: .telemetry.slices,
    provider_calls: .telemetry.provider_calls,
    primary: .telemetry.provider_call_types.primary,
    summary: .telemetry.provider_call_types.summary,
    output_tokens: .telemetry.provider_call_types.primary.output_tokens
  })
' \
  "runs/extraction-eval-v1/$run_prefix-current/report.json" \
  "runs/extraction-eval-v1/$run_prefix-interaction-slim/report.json"
