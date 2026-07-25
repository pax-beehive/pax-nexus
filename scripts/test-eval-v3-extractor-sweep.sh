#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

failures=0

fail() {
  echo "FAIL: $1" >&2
  failures=$((failures + 1))
}

expect_eq() {
  if [ "$2" != "$3" ]; then
    fail "$1: expected [$3], got [$2]"
  fi
}

expect_contains() {
  case "$2" in
    *"$3"*) ;;
    *) fail "$1: expected output to contain [$3], got [$2]" ;;
  esac
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat > "$tmp/base.env" <<'ENVEOF'
TEAM_MEMORY_EXTRACTOR_MODEL=from-base-env
TEAM_MEMORY_EXTRACTOR_BASE_URL=https://base.example.com
TEAM_MEMORY_EXTRACTOR_API_KEY=base-key
TEAM_MEMORY_API_KEYS={}
PAXM_SOURCE_DIR=/nonexistent
ENVEOF
: > "$tmp/eval.env"

read_extractor='. ./scripts/load-eval-v2-env.sh; printf "%s|%s|%s" "$TEAM_MEMORY_EXTRACTOR_MODEL" "$TEAM_MEMORY_EXTRACTOR_BASE_URL" "$TEAM_MEMORY_EXTRACTOR_API_KEY"'

actual=$(EVAL_V2_BASE_ENV_FILE="$tmp/base.env" EVAL_V2_ENV_FILE="$tmp/eval.env" \
  sh -c "$read_extractor")
expect_eq "env files apply when no override is set" "$actual" \
  "from-base-env|https://base.example.com|base-key"

actual=$(EVAL_V2_BASE_ENV_FILE="$tmp/base.env" EVAL_V2_ENV_FILE="$tmp/eval.env" \
  TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE=swept-model \
  TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE=https://swept.example.com \
  TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE=swept-key \
  sh -c "$read_extractor")
expect_eq "overrides beat env files" "$actual" \
  "swept-model|https://swept.example.com|swept-key"

actual=$(EVAL_V2_BASE_ENV_FILE="$tmp/base.env" EVAL_V2_ENV_FILE="$tmp/eval.env" \
  TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE= \
  sh -c "$read_extractor")
expect_eq "empty override is inert" "$actual" \
  "from-base-env|https://base.example.com|base-key"

for slug in deepseek-v4-flash gemini-3.5-flash-lite gemini-3.6-flash; do
  fragment="evals/v3/sweep/extractor-${slug}.env"
  if [ ! -f "$fragment" ]; then
    fail "fragment missing: $fragment"
    continue
  fi
  actual=$(sh -c ". ./$fragment; printf '%s|%s|%s' \
    \"\$SWEEP_EXTRACTOR_MODEL\" \"\$SWEEP_EXTRACTOR_BASE_URL\" \"\$SWEEP_EXTRACTOR_KEY_ENV\"")
  case "$actual" in
    *"|"*"|"*) ;;
    *) fail "fragment $slug did not define all three fields: $actual" ;;
  esac
  expect_contains "fragment $slug names its model" "$actual" "$slug"
  if grep -qE '^(TEAM_MEMORY|OPENCODE|MEM0)_' "$fragment"; then
    fail "fragment $slug sets a real configuration variable"
  fi
  if grep -qiE '(api_key|apikey)[[:space:]]*=[[:space:]]*[A-Za-z0-9._-]{12,}' "$fragment"; then
    fail "fragment $slug appears to contain a credential"
  fi
done

expect_eq "deepseek fragment key env" \
  "$(sh -c '. ./evals/v3/sweep/extractor-deepseek-v4-flash.env; printf "%s" "$SWEEP_EXTRACTOR_KEY_ENV"')" \
  "DEEPSEEK_API_KEY"
expect_eq "gemini fragment key env" \
  "$(sh -c '. ./evals/v3/sweep/extractor-gemini-3.6-flash.env; printf "%s" "$SWEEP_EXTRACTOR_KEY_ENV"')" \
  "GEMINI_API_KEY"

template=evals/v3/config.sweep-template.yaml
if [ ! -f "$template" ]; then
  fail "template missing: $template"
else
  for token in __RUN_ID__ __OUTPUT_DIR__ __MANIFEST__; do
    expect_contains "template defines $token" "$(cat "$template")" "$token"
  done
  expect_contains "template records the extractor base URL" \
    "$(cat "$template")" "TEAM_MEMORY_EXTRACTOR_BASE_URL"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all extractor sweep checks passed"
