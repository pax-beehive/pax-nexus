#!/usr/bin/env bash
set -euo pipefail

data_root="${1:-.build/datasets/llmwiki/raw}"
dataset="${2:-all}"
with_screenshots="${WITH_LMEMEVAL_V2_SCREENSHOTS:-0}"

longmemeval_revision="98d7416c24c778c2fee6e6f3006e7a073259d48f"
longmemeval_v2_revision="f152293e235517d504809563c833d7190b8c713b"
locomo_revision="3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376"

download() {
  local url="$1"
  local destination="$2"

  if [[ -s "${destination}" ]]; then
    echo "Present: ${destination}"
    return
  fi

  mkdir -p "$(dirname "${destination}")"
  curl -L --fail --show-error "${url}" -o "${destination}.part"
  mv "${destination}.part" "${destination}"
}

fetch_longmemeval() {
  local longmemeval_root="${data_root}/longmemeval"
  download \
    "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/${longmemeval_revision}/longmemeval_s_cleaned.json?download=true" \
    "${longmemeval_root}/longmemeval_s_cleaned.json"
}

fetch_longmemeval_v2() {
  local v2_root="${data_root}/longmemeval-v2"
  for filename in \
    DATA_CARD.md \
    LICENSE \
    README.md \
    SCHEMA.md \
    checksums.sha256 \
    questions.jsonl \
    trajectories.jsonl \
    haystacks/lme_v2_small.json \
    haystacks/lme_v2_medium.json
  do
    download \
      "https://huggingface.co/datasets/xiaowu0162/longmemeval-v2/resolve/${longmemeval_v2_revision}/${filename}?download=true" \
      "${v2_root}/${filename}"
  done

  while read -r _ path; do
    if [[ "${path}" == question_screenshots/* ]]; then
      download \
        "https://huggingface.co/datasets/xiaowu0162/longmemeval-v2/resolve/${longmemeval_v2_revision}/${path}?download=true" \
        "${v2_root}/${path}"
    fi
  done < "${v2_root}/checksums.sha256"

  if [[ "${with_screenshots}" == "1" ]]; then
    for filename in \
      web_screenshots.tar.gz \
      enterprise_screenshots_base.tar.gz
    do
      download \
        "https://huggingface.co/datasets/xiaowu0162/longmemeval-v2/resolve/${longmemeval_v2_revision}/trajectory_screenshots/${filename}?download=true" \
        "${v2_root}/trajectory_screenshots/${filename}"
    done
  else
    echo "Skipped LongMemEval-V2 trajectory screenshots (about 5.9 GB)."
    echo "Set WITH_LMEMEVAL_V2_SCREENSHOTS=1 to download the complete visual corpus."
  fi
}

fetch_locomo() {
  download \
    "https://raw.githubusercontent.com/snap-research/locomo/${locomo_revision}/data/locomo10.json" \
    "${data_root}/locomo/locomo10.json"
}

case "${dataset}" in
  all)
    fetch_longmemeval
    fetch_longmemeval_v2
    fetch_locomo
    ;;
  longmemeval)
    fetch_longmemeval
    ;;
  longmemeval-v2)
    fetch_longmemeval_v2
    ;;
  locomo)
    fetch_locomo
    ;;
  *)
    echo "Unsupported dataset: ${dataset}" >&2
    exit 2
    ;;
esac

cat <<EOF
Session dataset raw files are ready under ${data_root}.

Pinned revisions:
  LongMemEval:    ${longmemeval_revision}
  LongMemEval-V2: ${longmemeval_v2_revision}
  LoCoMo:         ${locomo_revision}
EOF
