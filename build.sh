#!/bin/bash
set -euo pipefail

mkdir -p output/bin
cp script/* output 2>/dev/null
chmod +x output/bootstrap.sh
make build OUTPUT_BIN_DIR=output/bin \
  EXTRACTION_CANDIDATE_STRATEGY="${EXTRACTION_CANDIDATE_STRATEGY:-current}"
