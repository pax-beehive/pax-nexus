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
export MEM0_OPENAI_API_KEY=test-mem0-key

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

printf '%s\n' '{"dataset":"GroupMemBench","dataset_revision":"revision","domain":"Finance","seed":"smoke-seed","cases":[{"id":"case-1","category":"temporal","question":"q","answer":"a","asking_user_id":"u","scope_id":"s"}]}' > "${selection}/manifest.single.json"
EVAL_V2_ALLOW_DIRTY=1 \
EVAL_V2_JOB_DRY_RUN=1 \
EVAL_V2_JOB_RUN_ID=prepared-manifest-smoke \
EVAL_V2_SEED=smoke-seed \
EVAL_V2_PREPARED_SELECTION="${selection}" \
EVAL_V2_PREPARED_MANIFEST=manifest.single.json \
EVAL_V2_OUTPUT_ROOT="${temporary}/runs" \
  ./scripts/eval-v2-job.sh >/dev/null
jq -e '.status == "dry_run" and (.manifest_sha256 | length == 64)' \
  "${temporary}/runs/prepared-manifest-smoke/run.json" >/dev/null

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

mock_bin="${temporary}/bin"
mkdir -p "${mock_bin}"
printf '%s\n' '#!/bin/sh' 'case "$*" in' '  *"port postgres 5432"*) printf "%s\\n" "0.0.0.0:65432" ;;' 'esac' > "${mock_bin}/docker"
printf '%s\n' '#!/bin/sh' 'printf "%s\\n" "${EVAL_V2_POSTGRES_DSN}" > "${EVAL_DSN_CAPTURE}"' > "${mock_bin}/team-memory-eval-v2"
printf '%s\n' '#!/bin/sh' 'printf "%s\\n" "$1" > "${EVAL_ACCEPTANCE_CAPTURE}"' > "${mock_bin}/acceptance-pass"
printf '%s\n' '#!/bin/sh' 'exit 1' > "${mock_bin}/acceptance-fail"
chmod +x "${mock_bin}/docker" "${mock_bin}/team-memory-eval-v2" "${mock_bin}/acceptance-pass" "${mock_bin}/acceptance-fail"

EVAL_DSN_CAPTURE="${temporary}/dynamic-dsn" \
EVAL_ACCEPTANCE_CAPTURE="${temporary}/acceptance-run-directory" \
PATH="${mock_bin}:${PATH}" \
EVAL_V2_ALLOW_DIRTY=1 \
EVAL_V2_JOB_RUN_ID=dynamic-port-smoke \
EVAL_V2_SEED=smoke-seed \
EVAL_V2_PREPARED_SELECTION="${selection}" \
EVAL_V2_OUTPUT_ROOT="${temporary}/runs" \
EVAL_V2_ACCEPTANCE_PROGRAM="${mock_bin}/acceptance-pass" \
  ./scripts/eval-v2-job.sh >/dev/null
grep -q 'host.docker.internal:65432' "${temporary}/dynamic-dsn"
expected_run_directory="$(cd "${temporary}/runs/dynamic-port-smoke" && pwd -P)"
IFS= read -r acceptance_run_directory < "${temporary}/acceptance-run-directory"
test "${acceptance_run_directory}" = "${expected_run_directory}"
jq -e '.status == "completed"' "${temporary}/runs/dynamic-port-smoke/run.json" >/dev/null

EVAL_DSN_CAPTURE="${temporary}/external-dsn" \
PATH="${mock_bin}:${PATH}" \
EVAL_V2_ALLOW_DIRTY=1 \
EVAL_V2_JOB_RUN_ID=external-dsn-smoke \
EVAL_V2_SEED=smoke-seed \
EVAL_V2_PREPARED_SELECTION="${selection}" \
EVAL_V2_OUTPUT_ROOT="${temporary}/runs" \
EVAL_V2_JOB_POSTGRES_DSN='postgres://external/example' \
  ./scripts/eval-v2-job.sh >/dev/null
IFS= read -r external_dsn < "${temporary}/external-dsn"
test "${external_dsn}" = 'postgres://external/example'

if EVAL_DSN_CAPTURE="${temporary}/failure-dsn" \
  PATH="${mock_bin}:${PATH}" \
  EVAL_V2_ALLOW_DIRTY=1 \
  EVAL_V2_JOB_RUN_ID=acceptance-failure-smoke \
  EVAL_V2_SEED=smoke-seed \
  EVAL_V2_PREPARED_SELECTION="${selection}" \
  EVAL_V2_OUTPUT_ROOT="${temporary}/runs" \
  EVAL_V2_ACCEPTANCE_PROGRAM="${mock_bin}/acceptance-fail" \
  ./scripts/eval-v2-job.sh >/dev/null 2>&1; then
  echo "eval job unexpectedly accepted a failed acceptance gate" >&2
  exit 1
fi
jq -e '.status == "failed"' "${temporary}/runs/acceptance-failure-smoke/run.json" >/dev/null
