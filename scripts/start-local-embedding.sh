#!/bin/sh
set -u

local_url="http://qwen-embedding:8080"
embedding_url="${TEAM_MEMORY_EMBEDDING_BASE_URL-${local_url}}"

if [ "${embedding_url}" != "${local_url}" ]; then
  exit 0
fi

if ! docker compose "$@" up -d --wait qwen-embedding; then
  echo "warning: local Qwen embedding failed to start; Team Memory will use lexical recall" >&2
fi
