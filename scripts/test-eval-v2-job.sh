#!/bin/sh
set -eu

for script in scripts/eval-v2-job.sh scripts/eval-v2-stack.sh scripts/eval-v2-opencode.sh scripts/eval-v2-prepare-groupmembench.sh; do
  sh -n "${script}"
done

temporary="$(mktemp -d)"
cleanup() {
  rm -rf "${temporary}"
}
trap cleanup EXIT HUP INT TERM

selection="${temporary}/selection"
mkdir -p "${selection}/cases/case-1/producer" "${selection}/cases/case-1/consumer"
printf '%s\n' '{"dataset":"GroupMemBench","dataset_revision":"revision","domain":"Finance","seed":"smoke-seed","cases":[{"id":"case-1","category":"temporal","question":"q","answer":"a","asking_user_id":"u","scope_id":"s"}]}' > "${selection}/manifest.json"

EVAL_V2_ALLOW_DIRTY=1 \
EVAL_V2_JOB_DRY_RUN=1 \
EVAL_V2_JOB_RUN_ID=cron-smoke \
EVAL_V2_SEED=smoke-seed \
EVAL_V2_PREPARED_SELECTION="${selection}" \
EVAL_V2_OUTPUT_ROOT="${temporary}/runs" \
  ./scripts/eval-v2-job.sh >/dev/null

jq -e '.status == "dry_run" and .selection_seed == "smoke-seed" and (.manifest_sha256 | length == 64)' \
  "${temporary}/runs/cron-smoke/run.json" >/dev/null
test -f "${temporary}/runs/cron-smoke/job.log"
test -f "${temporary}/runs/cron-smoke/config.source.yaml"

if EVAL_V2_ALLOW_DIRTY=1 EVAL_V2_JOB_DRY_RUN=1 EVAL_V2_JOB_RUN_ID=cron-smoke \
  EVAL_V2_PREPARED_SELECTION="${selection}" EVAL_V2_OUTPUT_ROOT="${temporary}/runs" \
  ./scripts/eval-v2-job.sh >/dev/null 2>&1; then
  echo "eval job unexpectedly reused an existing run directory" >&2
  exit 1
fi
jq -e '.status == "dry_run"' "${temporary}/runs/cron-smoke/run.json" >/dev/null

broken_selection="${temporary}/broken-selection"
mkdir -p "${broken_selection}"
if EVAL_V2_ALLOW_DIRTY=1 EVAL_V2_JOB_RUN_ID=failed-smoke \
  EVAL_V2_PREPARED_SELECTION="${broken_selection}" EVAL_V2_OUTPUT_ROOT="${temporary}/runs" \
  ./scripts/eval-v2-job.sh >/dev/null 2>&1; then
  echo "eval job unexpectedly accepted a missing manifest" >&2
  exit 1
fi
jq -e '.status == "failed"' "${temporary}/runs/failed-smoke/run.json" >/dev/null
test -f "${temporary}/runs/failed-smoke/report.html"
test -f "${temporary}/runs/failed-smoke/job.log"
grep -q 'Image digests' "${temporary}/runs/failed-smoke/report.html"
