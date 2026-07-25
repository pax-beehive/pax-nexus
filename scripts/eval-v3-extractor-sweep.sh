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

# Overridable command seams. Tests point these at stub scripts so the sweep
# loop's control flow (reset/up/dsn/runner/down ordering, failure handling,
# override propagation) can be exercised without Docker, the network, or go.
# Default behaviour is unchanged: the real stack script, the real DSN
# resolver, and the real runner.
EVAL_V3_STACK_CMD="${EVAL_V3_STACK_CMD:-./scripts/eval-v3-stack.sh}"
EVAL_V3_RUNNER_CMD="${EVAL_V3_RUNNER_CMD:-go run ./cmd/team-memory-eval-v3}"
EVAL_V3_DSN_CMD="${EVAL_V3_DSN_CMD:-./scripts/eval-postgres-dsn.sh}"

# evals/v3/config.sweep-template.yaml declares arm_set: two_arm_no_mem0, so the
# runner's before_run/consumer invocations of scripts/eval-v3-opencode.sh must
# see the same value here (the executor forwards this process's environment
# to those child commands unchanged) — otherwise the ingest step and the
# config's validity check would disagree about which arms ran.
EVAL_V3_ARM_SET="${EVAL_V3_ARM_SET:-two_arm_no_mem0}"
export EVAL_V3_ARM_SET

if [ ! -f "$manifest" ]; then
  echo "manifest not found: ${manifest}" >&2
  exit 2
fi
if [ ! -f "$template" ]; then
  echo "config template not found: ${template}" >&2
  exit 2
fi

# Reject every unknown or incomplete slug before doing any work, so a typo or
# a malformed fragment costs a second rather than a round of full-domain
# ingest — and so slug 2 being broken can never cost slug 3 or slug 1 a round
# that already ran or was about to. Fragment vars are scoped to a subshell so
# this pre-check can't leak values into (or pick up stale values left by) the
# per-round loop below.
for slug in $slugs; do
  fragment="evals/v3/sweep/extractor-${slug}.env"
  if [ ! -f "$fragment" ]; then
    echo "unknown extractor slug: ${slug}" >&2
    exit 2
  fi
  if ! (
    SWEEP_EXTRACTOR_MODEL=""
    SWEEP_EXTRACTOR_BASE_URL=""
    SWEEP_EXTRACTOR_KEY_ENV=""
    . "./${fragment}"
    [ -n "$SWEEP_EXTRACTOR_MODEL" ] && [ -n "$SWEEP_EXTRACTOR_BASE_URL" ] && [ -n "$SWEEP_EXTRACTOR_KEY_ENV" ]
  ); then
    echo "fragment for ${slug} is incomplete: ${fragment}" >&2
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
  # Completeness was already validated for every requested slug in the
  # up-front pre-check above, before any round started.

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
  # reveal it. `docker compose down` against a project that doesn't exist yet
  # exits 0, so a non-zero exit here is a genuine failure, not a first-round
  # artifact — treat it as such: skip the round entirely (no up, no runner)
  # rather than risk ingesting into a stack that still holds the previous
  # extractor's data with nothing in the artifacts to reveal it.
  reset_status=0
  $EVAL_V3_STACK_CMD reset || reset_status=$?
  if [ "$reset_status" -ne 0 ]; then
    echo "  stack reset failed (exit ${reset_status}); skipping ${slug} to avoid cross-round contamination" >&2
    summary="${summary}${slug}\tFAILED (stack reset exit ${reset_status})\n"
    overall=1
    continue
  fi

  status=0
  reason=""
  if $EVAL_V3_STACK_CMD up "$manifest" "$run_id"; then
    # The eval Postgres port is ephemeral (compose publishes "0:5432" so
    # concurrent stacks don't collide), so it must be resolved fresh after
    # every `up`, before the runner touches the database. A resolution
    # failure is treated exactly like a runner failure: it is reported,
    # counted, and the stack is still torn down below.
    if dsn=$($EVAL_V3_DSN_CMD "$EVAL_V3_COMPOSE_PROJECT" "$EVAL_V3_COMPOSE_FILE"); then
      EVAL_V2_POSTGRES_DSN="$dsn"
      export EVAL_V2_POSTGRES_DSN
      GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" \
        $EVAL_V3_RUNNER_CMD -config "$config" || status=$?
      reason="exit ${status}"
    else
      echo "  DSN resolution failed for ${slug}; skipping runner" >&2
      status=1
      reason="DSN resolution failed"
    fi
  else
    status=1
    reason="exit ${status}"
  fi

  # Teardown failure is reported, not discarded, but it does not fail the
  # round on its own: the next round's reset (down -v) will attempt teardown
  # again before that round's ingest starts.
  down_status=0
  $EVAL_V3_STACK_CMD down || down_status=$?

  if [ "$status" -eq 0 ]; then
    if [ "$down_status" -ne 0 ]; then
      summary="${summary}${slug}\tOK (teardown failed, exit ${down_status})\n"
    else
      summary="${summary}${slug}\tOK\n"
    fi
  else
    if [ "$down_status" -ne 0 ]; then
      summary="${summary}${slug}\tFAILED (${reason}; teardown failed, exit ${down_status})\n"
    else
      summary="${summary}${slug}\tFAILED (${reason})\n"
    fi
    overall=1
  fi
done

echo ""
echo "sweep summary (${prefix}):"
printf "%b" "$summary"
exit "$overall"
