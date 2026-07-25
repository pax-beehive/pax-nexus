#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

usage() {
  echo "usage: $0 [--dry-run] <run-id-prefix> [slug...]" >&2
  exit 2
}

dry_run=false
if [ "${1:-}" = "--dry-run" ]; then
  dry_run=true
  shift
fi

prefix="${1:-}"
[ -n "$prefix" ] || usage
shift

if [ "$#" -gt 0 ]; then
  slugs="$*"
else
  slugs="deepseek-v4-flash gemini-3.5-flash-lite gemini-3.6-flash"
fi

manifest="${EVAL_V3_SWEEP_MANIFEST:-runs/groupmembench-v3-micro-canary-v1/manifest.five.json}"
template=evals/v3/config.sweep-template.yaml
sweep_root="runs/eval-v3-sweep/${prefix}"

if [ ! -f "$manifest" ]; then
  echo "manifest not found: ${manifest}" >&2
  exit 2
fi
if [ ! -f "$template" ]; then
  echo "config template not found: ${template}" >&2
  exit 2
fi

# Reject every unknown slug before doing any work, so a typo costs a second
# rather than a round of full-domain ingest.
for slug in $slugs; do
  if [ ! -f "evals/v3/sweep/extractor-${slug}.env" ]; then
    echo "unknown extractor slug: ${slug}" >&2
    exit 2
  fi
done

# Load .env / .env.eval-v2 so provider credentials are resolvable here. Child
# scripts source them again; that is harmless because the _OVERRIDE variables
# exported below are applied after each of those loads.
. ./scripts/load-eval-v3-env.sh

mkdir -p "${sweep_root}/configs"

summary=""
overall=0

for slug in $slugs; do
  SWEEP_EXTRACTOR_MODEL=""
  SWEEP_EXTRACTOR_BASE_URL=""
  SWEEP_EXTRACTOR_KEY_ENV=""
  . "./evals/v3/sweep/extractor-${slug}.env"

  if [ -z "$SWEEP_EXTRACTOR_MODEL" ] || [ -z "$SWEEP_EXTRACTOR_BASE_URL" ] || [ -z "$SWEEP_EXTRACTOR_KEY_ENV" ]; then
    echo "fragment for ${slug} is incomplete" >&2
    exit 2
  fi

  key_value=$(eval "printf '%s' \"\${${SWEEP_EXTRACTOR_KEY_ENV}:-}\"")
  if [ -n "$key_value" ]; then
    key_state=present
  else
    key_state=missing
  fi

  run_id="${prefix}-${slug}"
  output_dir="${sweep_root}/${slug}"
  config="${sweep_root}/configs/${slug}.yaml"

  sed -e "s|__RUN_ID__|${run_id}|g" \
      -e "s|__OUTPUT_DIR__|${output_dir}|g" \
      -e "s|__MANIFEST__|${manifest}|g" \
      "$template" > "$config"

  echo "round slug=${slug} model=${SWEEP_EXTRACTOR_MODEL} base_url=${SWEEP_EXTRACTOR_BASE_URL} key_env=${SWEEP_EXTRACTOR_KEY_ENV} key=${key_state} run_id=${run_id} output_dir=${output_dir} config=${config}"

  if [ "$key_state" = missing ]; then
    echo "  ${SWEEP_EXTRACTOR_KEY_ENV} is empty; set it in .env.eval-v2" >&2
    summary="${summary}${slug}\tSKIPPED (no ${SWEEP_EXTRACTOR_KEY_ENV})\n"
    overall=1
    continue
  fi

  if [ "$dry_run" = true ]; then
    summary="${summary}${slug}\tPLANNED\n"
    continue
  fi

  TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE="$SWEEP_EXTRACTOR_MODEL"
  TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE="$SWEEP_EXTRACTOR_BASE_URL"
  TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE="$key_value"
  export TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE
  export TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE
  export TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE

  # down -v before every round. Surviving Team Note rows from the previous
  # extractor would corrupt all three arms with nothing in the artifacts to
  # reveal it.
  ./scripts/eval-v3-stack.sh reset || true

  status=0
  if ./scripts/eval-v3-stack.sh up "$manifest" "$run_id"; then
    GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" \
      go run ./cmd/team-memory-eval-v3 -config "$config" || status=$?
  else
    status=1
  fi

  ./scripts/eval-v3-stack.sh down || true

  if [ "$status" -eq 0 ]; then
    summary="${summary}${slug}\tOK\n"
  else
    summary="${summary}${slug}\tFAILED (exit ${status})\n"
    overall=1
  fi
done

echo ""
echo "sweep summary (${prefix}):"
printf "%b" "$summary"
exit "$overall"
