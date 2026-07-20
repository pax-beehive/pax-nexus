#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

package=github.com/pax-beehive/pax-nexus/internal/teamnote
for strategy in passive-v1 hint-v1-selective; do
  TEAM_MEMORY_TEST_BUILD_DEFAULT_RECALL_CANDIDATE_STRATEGY="$strategy" \
    GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" \
    go test -ldflags "-X ${package}.buildDefaultRecallCandidateStrategy=${strategy}" \
      ./internal/teamnote \
      -run 'TestRecallCandidateStrategySuite/TestBuildDefaultMatchesInjectedValue$' \
      -count=1
  GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" \
    go test -ldflags "-X ${package}.buildDefaultRecallCandidateStrategy=${strategy}" \
      . \
      -run 'TestConfigSuite/TestLoadsNoopConfiguration$' \
      -count=1
done

for strategy in unknown "passive-v1 hint-v1-selective"; do
  if make -s validate-recall-candidate-strategy RECALL_CANDIDATE_STRATEGY="$strategy" >/dev/null 2>&1; then
    echo "invalid recall candidate strategy unexpectedly passed: $strategy" >&2
    exit 1
  fi
done
