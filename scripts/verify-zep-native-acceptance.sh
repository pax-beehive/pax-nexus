#!/bin/sh
set -eu

run_directory="${1:?run directory is required}"
trials_file="${run_directory}/trials.jsonl"

if [ ! -f "${trials_file}" ]; then
  echo "Zep acceptance requires trials.jsonl: ${trials_file}" >&2
  exit 1
fi

zep_trials="$(jq -s '[.[] | select(.arm == "zep_native")]' "${trials_file}")"
zep_count="$(printf '%s' "${zep_trials}" | jq 'length')"
if [ "${zep_count}" -ne 1 ]; then
  echo "Zep acceptance requires exactly one zep_native trial, found ${zep_count}" >&2
  exit 1
fi

case_id="$(printf '%s' "${zep_trials}" | jq -er '.[0].case_id')"
if ! printf '%s' "${zep_trials}" | jq -e '.[0] | .status == "completed" and .judged == true' >/dev/null; then
  echo "Zep acceptance requires a completed and judged trial: ${case_id}" >&2
  exit 1
fi

artifact_directory="${run_directory}/trials/${case_id}/zep_native"
ingest_file="${artifact_directory}/zep-ingest.json"
readiness_file="${artifact_directory}/zep-readiness.json"
search_file="${artifact_directory}/zep-native-summary.json"

for artifact in "${ingest_file}" "${readiness_file}" "${search_file}"; do
  if [ ! -f "${artifact}" ]; then
    echo "Zep acceptance artifact is missing: ${artifact}" >&2
    exit 1
  fi
done

if ! jq -e '(.accepted // 0) > 0' "${ingest_file}" >/dev/null; then
  echo "Zep acceptance requires at least one ingested episode" >&2
  exit 1
fi
if ! jq -e '(.accepted // 0) > 0 and (.episodes // 0) >= .accepted and (if has("processed") then .processed == .accepted else true end)' "${readiness_file}" >/dev/null; then
  echo "Zep acceptance requires all ingested episodes to be processed" >&2
  exit 1
fi
if ! jq -e '(.episodes // 0) > 0 and (.context_characters // 0) > 0 and (if has("processed") then .processed > 0 else true end)' "${search_file}" >/dev/null; then
  echo "Zep acceptance requires non-empty processed native context" >&2
  exit 1
fi

printf '%s\n' "Zep native acceptance passed for ${case_id}"
