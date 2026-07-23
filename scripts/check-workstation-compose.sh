#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if (( $# > 1 )); then
  echo "usage: $0 [env-file]" >&2
  exit 2
fi

compose_args=(-f compose.yaml -f deploy/workstation/compose.yaml)
if (( $# == 1 )); then
  compose_args=(--env-file "$1" "${compose_args[@]}")
fi

# Keep the rendered configuration, including resolved secrets, inside the
# pipeline. The validator reports only the violated invariant.
docker compose "${compose_args[@]}" config --format json |
  node scripts/workstation-compose-validator.mjs
