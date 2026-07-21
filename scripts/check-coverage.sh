#!/bin/sh
set -eu

minimum="${COVERAGE_MIN:-80}"
output_dir=".build"
raw_profile="${output_dir}/coverage.raw.out"
postgres_profile="${output_dir}/coverage.postgres.out"
combined_profile="${output_dir}/coverage.combined.out"
profile="${output_dir}/coverage.out"

mkdir -p "${output_dir}"
go test ./... -count=1 -covermode=atomic -coverprofile="${raw_profile}"
: "${TEAM_MEMORY_TEST_POSTGRES_DSN:?TEAM_MEMORY_TEST_POSTGRES_DSN is required}"
go test ./internal/platform/postgres -count=1 -covermode=atomic -coverprofile="${postgres_profile}"

awk 'NR == 1 || $1 !~ /\/internal\/platform\/postgres\//' "${raw_profile}" > "${combined_profile}"
tail -n +2 "${postgres_profile}" >> "${combined_profile}"

awk '
NR == 1 { print; next }
$1 !~ /\/internal\/teamnote\/transport\/httpapi\/(model|router)\// &&
$1 !~ /\/internal\/eval\/v2\/postgresstore\// && $1 !~ /\/mocks\// &&
$1 !~ /\/cmd\// && $1 !~ /\.gen\.go:/ { print }
' "${combined_profile}" > "${profile}"

total="$(go tool cover -func="${profile}" | awk '/^total:/ { gsub("%", "", $3); print $3 }')"
if [ -z "${total}" ]; then
  echo "unable to calculate unit-test coverage" >&2
  exit 1
fi

awk -v total="${total}" -v minimum="${minimum}" 'BEGIN {
  if (total + 0 < minimum + 0) {
    printf "unit-test coverage %.1f%% is below %.1f%%\n", total, minimum > "/dev/stderr"
    exit 1
  }
  printf "unit-test coverage %.1f%% meets %.1f%% threshold\n", total, minimum
}'
