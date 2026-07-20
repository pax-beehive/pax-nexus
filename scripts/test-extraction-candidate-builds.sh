#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

package=github.com/pax-beehive/pax-nexus/internal/teamnote/extractor
for strategy in current interaction-slim typed-2 source-span-v1 source-span-v2 claim-card-v1 claim-card-v2; do
  TEAM_MEMORY_TEST_BUILD_DEFAULT_CANDIDATE_STRATEGY="$strategy" \
    GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" \
    go test -ldflags "-X ${package}.buildDefaultCandidateStrategy=${strategy}" \
      ./internal/teamnote/extractor \
      -run 'TestCandidateStrategySuite/TestBuildDefaultMatchesInjectedValue$' \
      -count=1
done

for strategy in unknown "current typed-2"; do
  if make -s validate-extraction-candidate-strategy EXTRACTION_CANDIDATE_STRATEGY="$strategy" >/dev/null 2>&1; then
    echo "invalid extraction candidate strategy unexpectedly passed: $strategy" >&2
    exit 1
  fi
done
