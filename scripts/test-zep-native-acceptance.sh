#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temporary="$(mktemp -d)"
cleanup() {
  rm -rf "${temporary}"
}
trap cleanup EXIT HUP INT TERM

run_directory="${temporary}/run"
artifact_directory="${run_directory}/trials/case-1/zep_native"
mkdir -p "${artifact_directory}"
printf '%s\n' '{"arm":"zep_native","case_id":"case-1","status":"completed","judged":true}' > "${run_directory}/trials.jsonl"
printf '%s\n' '{"provider":"zep","accepted":2}' > "${artifact_directory}/zep-ingest.json"
printf '%s\n' '{"provider":"zep","accepted":2,"episodes":2,"processed":2,"attempts":1}' > "${artifact_directory}/zep-readiness.json"
printf '%s\n' '{"provider":"zep","episodes":1,"processed":1,"context_characters":200}' > "${artifact_directory}/zep-native-summary.json"

"${repo_root}/scripts/verify-zep-native-acceptance.sh" "${run_directory}" >/dev/null

printf '%s\n' '{"provider":"zep","accepted":2,"episodes":2,"processed":1,"attempts":1}' > "${artifact_directory}/zep-readiness.json"
if "${repo_root}/scripts/verify-zep-native-acceptance.sh" "${run_directory}" >/dev/null 2>&1; then
  echo "Zep acceptance unexpectedly passed an incomplete readiness artifact" >&2
  exit 1
fi
