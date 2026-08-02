#!/usr/bin/env bash
set -euo pipefail
# usage: ./upload.sh <out-dir> <hf-repo-id>   e.g. ./upload.sh out/finance user/groupmembench-agent-sessions
OUT_DIR="$1"; REPO="$2"
hf repo create "$REPO" --repo-type dataset -y || true
hf upload "$REPO" "$OUT_DIR" finance --repo-type dataset
hf upload "$REPO" evals/groupmembench-sessions/README.md README.md --repo-type dataset
