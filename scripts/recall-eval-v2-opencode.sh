#!/bin/sh
set -eu

stage="${1:?stage is required}"
arm="${2:-all}"

if [ "${stage}" = "consumer" ]; then
  case "${arm}" in
    no_memory) arm=no_memory_team ;;
    team_note) ;;
    *) echo "unsupported Recall Eval v2 arm: ${arm}" >&2; exit 1 ;;
  esac
fi

exec ./scripts/eval-v3-opencode.sh "${stage}" "${arm}"
