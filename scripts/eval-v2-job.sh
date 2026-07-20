#!/bin/sh
set -eu

. ./scripts/load-eval-v2-env.sh

candidate_sha="$(git rev-parse HEAD)"
short_sha="$(git rev-parse --short=12 HEAD)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
run_id="${EVAL_V2_JOB_RUN_ID:-nightly-${timestamp}-${short_sha}}"
seed="${EVAL_V2_SEED:-${run_id}}"
framework_version="${EVAL_FRAMEWORK_VERSION:-pax-eval-v2.6}"
selection_algorithm="${EVAL_SELECTION_ALGORITHM:-stratified-hash-v1}"
output_root="${EVAL_V2_OUTPUT_ROOT:-runs/eval-v2/automated}"
base_config="${EVAL_V2_BASE_CONFIG:-evals/v2/config.example.yaml}"

case "${run_id}" in
  *[!A-Za-z0-9_.-]*) echo "eval job run ID contains unsupported characters: ${run_id}" >&2; exit 1 ;;
esac
if [ -n "$(git status --porcelain)" ] && [ "${EVAL_V2_ALLOW_DIRTY:-0}" != "1" ]; then
  echo "eval job requires a clean candidate checkout" >&2
  exit 1
fi
mkdir -p "${output_root}"
run_directory="${output_root}/${run_id}"
if [ -e "${run_directory}" ]; then
  echo "eval job run directory already exists: ${run_directory}" >&2
  exit 1
fi
selection_directory="${run_directory}/selection"
mkdir -p "${selection_directory}"
run_directory="$(cd "${run_directory}" && pwd -P)"
selection_directory="${run_directory}/selection"
manifest="${selection_directory}/manifest.json"
project_name="eval-${run_id}"
stack_started=0
status=failed
manifest_sha256=unavailable
image_digests=pending

exec 3>&1 4>&2
exec >> "${run_directory}/job.log" 2>&1

escape_html() {
  printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g'
}

resolve_job_postgres_dsn() {
  if [ -n "${EVAL_V2_JOB_POSTGRES_DSN:-}" ]; then
    EVAL_V2_POSTGRES_DSN="${EVAL_V2_JOB_POSTGRES_DSN}"
    export EVAL_V2_POSTGRES_DSN
    return
  fi
  mapping="$(docker compose -p "${project_name}" -f "${EVAL_V2_COMPOSE_FILE:-evals/v2/compose.yaml}" port postgres 5432)"
  port="${mapping##*:}"
  case "${port}" in
    *[!0-9]*|'') echo "eval job could not resolve PostgreSQL host port: ${mapping}" >&2; return 1 ;;
  esac
  EVAL_V2_POSTGRES_DSN="postgres://team_memory:team_memory@host.docker.internal:${port}/team_memory?sslmode=disable"
  export EVAL_V2_POSTGRES_DSN
}

run_acceptance_program() {
  if [ -z "${EVAL_V2_ACCEPTANCE_PROGRAM:-}" ]; then
    return
  fi
  if [ ! -x "${EVAL_V2_ACCEPTANCE_PROGRAM}" ]; then
    echo "eval acceptance program is not executable: ${EVAL_V2_ACCEPTANCE_PROGRAM}" >&2
    return 1
  fi
  "${EVAL_V2_ACCEPTANCE_PROGRAM}" "${run_directory}"
}

cleanup() {
  if [ "${stack_started}" -eq 1 ]; then
    EVAL_V2_COMPOSE_PROJECT="${project_name}" ./scripts/eval-v2-stack.sh reset >/dev/null 2>&1 || true
  fi
  if [ "${status}" = "failed" ] && [ ! -f "${run_directory}/report.html" ]; then
    escaped_candidate="$(escape_html "${candidate_sha}")"
    escaped_seed="$(escape_html "${seed}")"
    escaped_framework_version="$(escape_html "${framework_version}")"
    escaped_manifest_sha256="$(escape_html "${manifest_sha256}")"
    escaped_image_digests="$(escape_html "${image_digests}")"
    printf '%s\n' '<!doctype html><meta charset="utf-8"><title>Eval job failed</title>' \
      "<h1>Eval job failed</h1><p>Run <code>${run_id}</code> did not complete.</p>" \
      "<dl><dt>Candidate Git SHA</dt><dd><code>${escaped_candidate}</code></dd><dt>Framework Git SHA</dt><dd><code>${escaped_candidate}</code></dd><dt>Framework version</dt><dd><code>${escaped_framework_version}</code></dd><dt>Seed</dt><dd><code>${escaped_seed}</code></dd><dt>Manifest SHA-256</dt><dd><code>${escaped_manifest_sha256}</code></dd><dt>Image digests</dt><dd><code>${escaped_image_digests}</code></dd></dl>" \
      '<p>Inspect <code>run.json</code> and <code>job.log</code> for details.</p>' \
      > "${run_directory}/report.html"
  fi
  if [ "${status}" = "failed" ]; then
    jq -n --arg run_id "${run_id}" --arg status failed \
      --arg candidate_git_sha "${candidate_sha}" --arg eval_framework_git_sha "${candidate_sha}" \
      --arg eval_framework_version "${framework_version}" --arg selection_seed "${seed}" \
      --arg selection_algorithm "${selection_algorithm}" --arg manifest_sha256 "${manifest_sha256}" \
      --arg image_digests "${image_digests}" --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      '{run_id:$run_id,status:$status,candidate_git_sha:$candidate_git_sha,eval_framework_git_sha:$eval_framework_git_sha,eval_framework_version:$eval_framework_version,selection_seed:$selection_seed,selection_algorithm:$selection_algorithm,manifest_sha256:$manifest_sha256,image_digests:$image_digests,generated_at:$generated_at}' \
      > "${run_directory}/run.json.tmp" && mv "${run_directory}/run.json.tmp" "${run_directory}/run.json" || true
    ln -sfn "${run_id}" "${output_root}/latest" || true
    printf '%s\n' "eval job failed; report retained at ${run_directory}/report.html" >&4
  fi
}
trap cleanup EXIT HUP INT TERM

if [ -n "${EVAL_V2_PREPARED_SELECTION:-}" ]; then
  cp -R "${EVAL_V2_PREPARED_SELECTION}/." "${selection_directory}/"
  if [ -n "${EVAL_V2_PREPARED_MANIFEST:-}" ]; then
    case "${EVAL_V2_PREPARED_MANIFEST}" in
      */*|.|..) echo "eval job prepared manifest must be a file name: ${EVAL_V2_PREPARED_MANIFEST}" >&2; exit 1 ;;
    esac
    manifest="${selection_directory}/${EVAL_V2_PREPARED_MANIFEST}"
  fi
else
  GROUPMEMBENCH_SEED="${seed}" \
  GROUPMEMBENCH_TOTAL_CASES="${EVAL_V2_TOTAL_CASES:-120}" \
  GROUPMEMBENCH_PER_CATEGORY="${EVAL_V2_PER_CATEGORY:-5}" \
    ./scripts/eval-v2-prepare-groupmembench.sh "${selection_directory}"
fi

if [ ! -f "${manifest}" ]; then
  echo "eval job selection manifest is missing: ${manifest}" >&2
  exit 1
fi
manifest_sha256="$(sha256sum "${manifest}" | awk '{print $1}')"
cp "${base_config}" "${run_directory}/config.source.yaml"
export CANDIDATE_GIT_SHA="${candidate_sha}"
export EVAL_FRAMEWORK_GIT_SHA="${candidate_sha}"
export EVAL_FRAMEWORK_VERSION="${framework_version}"
export EVAL_SELECTION_SEED="${seed}"
export EVAL_SELECTION_ALGORITHM="${selection_algorithm}"
export EVAL_MANIFEST_SHA256="${manifest_sha256}"
export EVAL_V2_COMPOSE_PROJECT="${project_name}"

write_run_record() {
  current_status="$1"
  jq -n \
    --arg run_id "${run_id}" \
    --arg status "${current_status}" \
    --arg candidate_git_sha "${candidate_sha}" \
    --arg eval_framework_git_sha "${candidate_sha}" \
    --arg eval_framework_version "${framework_version}" \
    --arg selection_seed "${seed}" \
    --arg selection_algorithm "${selection_algorithm}" \
    --arg manifest_sha256 "${manifest_sha256}" \
    --arg image_digests "${EVAL_IMAGE_DIGESTS:-pending}" \
    --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{run_id:$run_id,status:$status,candidate_git_sha:$candidate_git_sha,eval_framework_git_sha:$eval_framework_git_sha,eval_framework_version:$eval_framework_version,selection_seed:$selection_seed,selection_algorithm:$selection_algorithm,manifest_sha256:$manifest_sha256,image_digests:$image_digests,generated_at:$generated_at}' \
    > "${run_directory}/run.json.tmp"
  mv "${run_directory}/run.json.tmp" "${run_directory}/run.json"
}

write_run_record running
if [ "${EVAL_V2_JOB_DRY_RUN:-0}" = "1" ]; then
  status=dry_run
  write_run_record "${status}"
  printf '%s\n' "Eval v2 dry run prepared: ${run_directory}"
  exit 0
fi

stack_started=1
./scripts/eval-v2-stack.sh up "${manifest}" "${run_id}"
resolve_job_postgres_dsn
printf '%s\n' "eval job PostgreSQL DSN: ${EVAL_V2_POSTGRES_DSN%%@*}@..."
image_digests="runner=$(docker inspect --format '{{.Image}}' "$(hostname)" 2>/dev/null || printf unknown)"
for service in postgres team-memory qwen-embedding mem0-postgres mem0 mem0-configure opencode; do
  image_id="$(docker compose -p "${project_name}" -f "${EVAL_V2_COMPOSE_FILE:-evals/v2/compose.yaml}" images -q "${service}" | head -n 1)"
  image_digests="${image_digests};${service}=${image_id:-unknown}"
done
export EVAL_IMAGE_DIGESTS="${image_digests}"
write_run_record running

if team-memory-eval-v2 \
  -config "${base_config}" \
  -run-id "${run_id}" \
  -manifest "${manifest}" \
  -output-dir "${run_directory}" \
  -resolved-config-output "${run_directory}/config.resolved.json" \
  -automation-provenance; then
  run_acceptance_program
  status=completed
  write_run_record "${status}"
  ln -sfn "${run_id}" "${output_root}/latest"
  ln -sfn "${run_id}" "${output_root}/latest-success"
  printf '%s\n' "Eval v2 report: ${run_directory}/report.html" >&3
  exit 0
fi

write_run_record "${status}"
exit 1
