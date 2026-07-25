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

  # Extract each field individually to validate non-empty
  model=$(sh -c ". ./$fragment; printf '%s' \"\$SWEEP_EXTRACTOR_MODEL\"")
  url=$(sh -c ". ./$fragment; printf '%s' \"\$SWEEP_EXTRACTOR_BASE_URL\"")
  key_env=$(sh -c ". ./$fragment; printf '%s' \"\$SWEEP_EXTRACTOR_KEY_ENV\"")

  # Validate each field is non-empty
  if [ -z "$model" ]; then
    fail "fragment $slug has empty SWEEP_EXTRACTOR_MODEL"
  fi
  if [ -z "$url" ]; then
    fail "fragment $slug has empty SWEEP_EXTRACTOR_BASE_URL"
  fi
  if [ -z "$key_env" ]; then
    fail "fragment $slug has empty SWEEP_EXTRACTOR_KEY_ENV"
  fi

  # Validate SWEEP_EXTRACTOR_KEY_ENV is a valid shell variable name (not a pasted credential)
  if ! printf '%s' "$key_env" | grep -qE '^[A-Z][A-Z0-9_]*$'; then
    fail "fragment $slug SWEEP_EXTRACTOR_KEY_ENV is not a valid variable name: $key_env"
  fi

  # Check for forbidden config variable names
  if grep -qE '^(TEAM_MEMORY|OPENCODE|MEM0)_' "$fragment"; then
    fail "fragment $slug sets a real configuration variable"
  fi

  # Validate file-wide: every non-blank, non-comment line must be an assignment
  # to one of the three allowed variables (allowlist approach catches any stray
  # assignments like SWEEP_EXTRACTOR_API_KEY=...)
  while IFS= read -r line; do
    # Skip blank lines
    if [ -z "$line" ]; then
      continue
    fi
    # Skip comment lines
    case "$line" in
      \#*) continue ;;
    esac
    # All other lines must match exactly: VAR=value where VAR is one of three allowed names
    # and value contains only alphanumerics, dots, colons, slashes, hyphens, underscores
    # This rejects semicolons, spaces, quotes, backticks, and other injection vectors
    if ! printf '%s' "$line" | grep -qE '^(SWEEP_EXTRACTOR_MODEL|SWEEP_EXTRACTOR_BASE_URL|SWEEP_EXTRACTOR_KEY_ENV)=[A-Za-z0-9._:/-]*$'; then
      fail "fragment $slug contains disallowed line: $line"
    fi
  done < "$fragment"
done

# Specific assertions for each model
expect_eq "deepseek model" \
  "$(sh -c '. ./evals/v3/sweep/extractor-deepseek-v4-flash.env; printf "%s" "$SWEEP_EXTRACTOR_MODEL"')" \
  "deepseek-v4-flash"
expect_eq "deepseek base URL" \
  "$(sh -c '. ./evals/v3/sweep/extractor-deepseek-v4-flash.env; printf "%s" "$SWEEP_EXTRACTOR_BASE_URL"')" \
  "https://api.deepseek.com"
expect_eq "deepseek key env" \
  "$(sh -c '. ./evals/v3/sweep/extractor-deepseek-v4-flash.env; printf "%s" "$SWEEP_EXTRACTOR_KEY_ENV"')" \
  "DEEPSEEK_API_KEY"

expect_eq "gemini 3.5 model" \
  "$(sh -c '. ./evals/v3/sweep/extractor-gemini-3.5-flash-lite.env; printf "%s" "$SWEEP_EXTRACTOR_MODEL"')" \
  "gemini-3.5-flash-lite"
expect_eq "gemini 3.5 base URL" \
  "$(sh -c '. ./evals/v3/sweep/extractor-gemini-3.5-flash-lite.env; printf "%s" "$SWEEP_EXTRACTOR_BASE_URL"')" \
  "https://generativelanguage.googleapis.com/v1beta/openai/"
expect_eq "gemini 3.5 key env" \
  "$(sh -c '. ./evals/v3/sweep/extractor-gemini-3.5-flash-lite.env; printf "%s" "$SWEEP_EXTRACTOR_KEY_ENV"')" \
  "GEMINI_API_KEY"

expect_eq "gemini 3.6 model" \
  "$(sh -c '. ./evals/v3/sweep/extractor-gemini-3.6-flash.env; printf "%s" "$SWEEP_EXTRACTOR_MODEL"')" \
  "gemini-3.6-flash"
expect_eq "gemini 3.6 base URL" \
  "$(sh -c '. ./evals/v3/sweep/extractor-gemini-3.6-flash.env; printf "%s" "$SWEEP_EXTRACTOR_BASE_URL"')" \
  "https://generativelanguage.googleapis.com/v1beta/openai/"
expect_eq "gemini 3.6 key env" \
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

# --- scripts/eval-postgres-dsn.sh direct coverage. A stub docker prints a
# fixed `docker compose port` mapping so port resolution can be exercised
# without Docker or the network.
dsn_script=./scripts/eval-postgres-dsn.sh

cat > "$tmp/docker-stub.sh" <<'STUB'
#!/bin/sh
set -eu
printf 'docker %s\n' "$*" >> "${DOCKER_STUB_LOG:-/dev/null}"
if [ -n "${DOCKER_STUB_FAIL:-}" ]; then
  echo "${DOCKER_STUB_STDERR:-stub docker failure}" >&2
  exit "${DOCKER_STUB_FAIL}"
fi
printf '%b' "${DOCKER_STUB_MAPPING:-}"
STUB
chmod +x "$tmp/docker-stub.sh"

actual=$(EVAL_DOCKER_CMD="$tmp/docker-stub.sh" DOCKER_STUB_MAPPING='0.0.0.0:55999' \
  "$dsn_script" someproject evals/v2/compose.yaml)
expect_eq "dsn: ipv4 mapping yields the expected DSN" "$actual" \
  "postgres://team_memory:team_memory@127.0.0.1:55999/team_memory?sslmode=disable"

actual=$(EVAL_DOCKER_CMD="$tmp/docker-stub.sh" DOCKER_STUB_MAPPING='[::]:56001' \
  "$dsn_script" someproject evals/v2/compose.yaml)
expect_eq "dsn: ipv6-style mapping yields the expected DSN (not a naive first-colon cut)" \
  "$actual" "postgres://team_memory:team_memory@127.0.0.1:56001/team_memory?sslmode=disable"

actual=$(EVAL_DOCKER_CMD="$tmp/docker-stub.sh" DOCKER_STUB_MAPPING='0.0.0.0:56002\n[::]:56099' \
  "$dsn_script" someproject evals/v2/compose.yaml)
expect_eq "dsn: multi-line mapping yields exactly one line" \
  "$(printf '%s\n' "$actual" | wc -l | tr -d ' ')" "1"
expect_eq "dsn: multi-line mapping uses the first line's port" "$actual" \
  "postgres://team_memory:team_memory@127.0.0.1:56002/team_memory?sslmode=disable"

out=$(EVAL_DOCKER_CMD="$tmp/docker-stub.sh" DOCKER_STUB_FAIL=3 \
  "$dsn_script" myproject evals/v2/compose.yaml 2>&1) && fail "dsn: docker failure unexpectedly succeeded"
expect_contains "dsn: docker failure names the compose project on stderr" "$out" "myproject"

out=$(EVAL_DOCKER_CMD="$tmp/docker-stub.sh" DOCKER_STUB_MAPPING='' \
  "$dsn_script" emptyproject evals/v2/compose.yaml 2>&1) && fail "dsn: empty mapping unexpectedly succeeded"
expect_contains "dsn: empty mapping is reported on stderr" "$out" "emptyproject"

# Mutation guard: a failed docker invocation whose captured stderr happens
# to END in ":<digits>" must still be rejected on exit status alone, not
# rescued (or condemned) by the downstream digits-only port validation. A
# mutant that swallows docker's exit status (e.g.
# `mapping=$(... 2>&1) || true`) would otherwise read this stderr text as a
# valid port mapping and mint a DSN pointing at whatever number happens to
# follow the last colon in the error message — exactly the "healthy-looking
# but wrong DSN" this script exists to prevent.
out=$(EVAL_DOCKER_CMD="$tmp/docker-stub.sh" DOCKER_STUB_FAIL=1 \
  DOCKER_STUB_STDERR='dockererror:55123' \
  "$dsn_script" digitfailproject evals/v2/compose.yaml 2>&1) && \
  fail "dsn: docker failure with digit-suffixed stderr unexpectedly succeeded"
expect_contains "dsn: docker failure with digit-suffixed stderr names the compose project on stderr" \
  "$out" "digitfailproject"

stdout_only=$(EVAL_DOCKER_CMD="$tmp/docker-stub.sh" DOCKER_STUB_FAIL=1 \
  DOCKER_STUB_STDERR='dockererror:55123' \
  "$dsn_script" digitfailproject evals/v2/compose.yaml 2>/dev/null) && \
  fail "dsn: docker failure with digit-suffixed stderr unexpectedly succeeded (stdout capture)"
expect_eq "dsn: docker failure with digit-suffixed stderr emits no DSN on stdout" \
  "$stdout_only" ""

sweep=./scripts/eval-v3-extractor-sweep.sh
sweep_env="EVAL_V2_BASE_ENV_FILE=$tmp/base.env EVAL_V2_ENV_FILE=$tmp/eval.env"
manifest=runs/groupmembench-v3-micro-canary-v1/manifest.five.json

if [ ! -f "$manifest" ]; then
  fail "fixture manifest missing: $manifest (run: make eval-v3-prepare)"
else
  plan=$(env $sweep_env EVAL_V3_SWEEP_MANIFEST="$manifest" \
    DEEPSEEK_API_KEY=fake-deepseek GEMINI_API_KEY=fake-gemini \
    "$sweep" --dry-run planprefix 2>&1) || fail "dry run exited non-zero: $plan"

  expect_contains "plan covers deepseek" "$plan" \
    "slug=deepseek-v4-flash model=deepseek-v4-flash"
  expect_contains "plan covers flash-lite" "$plan" \
    "slug=gemini-3.5-flash-lite model=gemini-3.5-flash-lite"
  expect_contains "plan covers 3.6-flash" "$plan" \
    "slug=gemini-3.6-flash model=gemini-3.6-flash"
  expect_contains "plan reports resolved keys" "$plan" "key=present"
  expect_contains "plan derives run ids" "$plan" \
    "run_id=planprefix-gemini-3.6-flash"
  expect_contains "plan derives output dirs" "$plan" \
    "output_dir=runs/eval-v3-sweep/planprefix/gemini-3.6-flash"

  expect_eq "plan lists exactly three rounds" \
    "$(printf '%s\n' "$plan" | grep -c '^round ')" "3"

  expect_eq "deepseek round is first" \
    "$(printf '%s\n' "$plan" | grep '^round ' | head -1 | sed -n 's/.*slug=\([^ ]*\).*/\1/p')" \
    "deepseek-v4-flash"

  # The plan reports only whether a key resolved, never the value, so a
  # future change that starts printing credentials fails this loudly.
  case "$plan" in
    *fake-deepseek*|*fake-gemini*)
      fail "dry-run plan leaked a fake credential value" ;;
  esac

  rendered=runs/eval-v3-sweep/planprefix/configs/gemini-3.6-flash.yaml
  if [ ! -f "$rendered" ]; then
    fail "dry run did not render $rendered"
  else
    expect_contains "rendered run id" "$(cat "$rendered")" \
      "id: planprefix-gemini-3.6-flash"
    expect_contains "rendered output dir" "$(cat "$rendered")" \
      "output_dir: runs/eval-v3-sweep/planprefix/gemini-3.6-flash"
    expect_contains "rendered manifest" "$(cat "$rendered")" "manifest: $manifest"
    expect_contains "rendered runtime_env keeps base URL" "$(cat "$rendered")" \
      "TEAM_MEMORY_EXTRACTOR_BASE_URL"
    if grep -q '__' "$rendered"; then
      fail "rendered config still contains a placeholder"
    fi
  fi
  rm -rf runs/eval-v3-sweep/planprefix

  out=$(env $sweep_env EVAL_V3_SWEEP_MANIFEST="$manifest" \
    DEEPSEEK_API_KEY=fake-deepseek GEMINI_API_KEY= \
    "$sweep" --dry-run keyprefix 2>&1) && fail "missing key unexpectedly succeeded"
  expect_contains "missing key names the variable" "$out" "GEMINI_API_KEY"
  rm -rf runs/eval-v3-sweep/keyprefix

  out=$(env $sweep_env EVAL_V3_SWEEP_MANIFEST="$manifest" \
    DEEPSEEK_API_KEY=fake-deepseek \
    "$sweep" --dry-run slugprefix no-such-model 2>&1) && fail "unknown slug unexpectedly succeeded"
  expect_contains "unknown slug is named" "$out" "no-such-model"
  rm -rf runs/eval-v3-sweep/slugprefix

  out=$(env $sweep_env EVAL_V3_SWEEP_MANIFEST=runs/does-not-exist/manifest.json \
    DEEPSEEK_API_KEY=fake-deepseek \
    "$sweep" --dry-run badmanifest 2>&1) && fail "missing manifest unexpectedly succeeded"
  expect_contains "missing manifest is named" "$out" "does-not-exist"

  # --- Stub-driven, non-dry-run coverage of reset/up/runner/down control flow.
  # These invocations point EVAL_V3_STACK_CMD / EVAL_V3_RUNNER_CMD at stub
  # scripts, so no round ever touches Docker, the network, or go, even though
  # the sweep script itself is run without --dry-run.

  cat > "$tmp/stack-stub.sh" <<'STUB'
#!/bin/sh
set -eu
printf 'stack %s\n' "$*" >> "$STACK_STUB_LOG"
case "${1:-}" in
  reset)
    [ -z "${STUB_RESET_FAIL:-}" ] || exit "$STUB_RESET_FAIL"
    ;;
  up)
    [ -z "${STUB_UP_FAIL:-}" ] || exit "$STUB_UP_FAIL"
    ;;
esac
exit 0
STUB
  chmod +x "$tmp/stack-stub.sh"

  cat > "$tmp/runner-stub.sh" <<'STUB'
#!/bin/sh
set -eu
{
  printf 'runner %s\n' "$*"
  printf 'runner-env model=%s base_url=%s key=%s dsn=%s\n' \
    "${TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE:-}" \
    "${TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE:-}" \
    "${TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE:-}" \
    "${EVAL_V2_POSTGRES_DSN:-}"
} >> "$RUNNER_STUB_LOG"
# Also record a call marker in the shared ordering log (when provided) so
# tests can assert the DSN command ran after `up` and before the runner.
printf 'stack runner\n' >> "${STACK_STUB_LOG:-/dev/null}"
case "$*" in
  *"${STUB_RUNNER_FAIL_MATCH:-__never_match__}"*) exit 7 ;;
esac
exit 0
STUB
  chmod +x "$tmp/runner-stub.sh"

  cat > "$tmp/dsn-stub.sh" <<'STUB'
#!/bin/sh
set -eu
# Also record a call marker in the shared ordering log (when provided) so
# tests can assert the DSN command ran after `up` and before the runner.
printf 'stack dsn\n' >> "${STACK_STUB_LOG:-/dev/null}"
printf 'dsn %s\n' "$*" >> "${DSN_STUB_LOG:-/dev/null}"
if [ -n "${STUB_DSN_FAIL:-}" ]; then
  echo "stub dsn failure for $*" >&2
  exit "${STUB_DSN_FAIL}"
fi
printf '%s\n' "${STUB_DSN_VALUE:-postgres://stub-user:stub-pass@127.0.0.1:1/stubdb?sslmode=disable}"
STUB
  chmod +x "$tmp/dsn-stub.sh"

  # Scenario 1: all three rounds run; the first slug's runner fails. Verifies
  # (a) reset precedes up for every round, (b) a failed round does not stop
  # later slugs and the overall exit is 1, (c) down is logged for the failed
  # round too, and (d) each round's stub observes that round's fragment
  # values via the *_OVERRIDE variables.
  stack_log="$tmp/stack.log"
  runner_log="$tmp/runner.log"
  dsn_log="$tmp/dsn.log"
  : > "$stack_log"
  : > "$runner_log"
  : > "$dsn_log"

  stub_dsn_value='postgres://stub-user:stub-pass@127.0.0.1:56321/stubdb?sslmode=disable'

  stub_status=0
  stub_out=$(env $sweep_env EVAL_V3_SWEEP_MANIFEST="$manifest" \
    DEEPSEEK_API_KEY=fake-deepseek GEMINI_API_KEY=fake-gemini \
    EVAL_V3_STACK_CMD="$tmp/stack-stub.sh" EVAL_V3_RUNNER_CMD="$tmp/runner-stub.sh" \
    EVAL_V3_DSN_CMD="$tmp/dsn-stub.sh" \
    STACK_STUB_LOG="$stack_log" RUNNER_STUB_LOG="$runner_log" DSN_STUB_LOG="$dsn_log" \
    STUB_RUNNER_FAIL_MATCH=deepseek-v4-flash STUB_DSN_VALUE="$stub_dsn_value" \
    "$sweep" stubprefix 2>&1) || stub_status=$?

  expect_eq "stub sweep: first-slug runner failure yields overall exit 1" \
    "$stub_status" "1"

  seq_actual=$(awk '{print $2}' "$stack_log")
  expected_seq='reset
up
dsn
runner
down
reset
up
dsn
runner
down
reset
up
dsn
runner
down'
  expect_eq "stub sweep: reset,up,dsn,runner,down logged in order for all three rounds" \
    "$seq_actual" "$expected_seq"

  expect_contains "stub sweep: runner ran for deepseek round with its fragment values" \
    "$(cat "$runner_log")" \
    "runner-env model=deepseek-v4-flash base_url=https://api.deepseek.com key=fake-deepseek"
  expect_contains "stub sweep: runner still ran for flash-lite round after deepseek failed" \
    "$(cat "$runner_log")" \
    "runner-env model=gemini-3.5-flash-lite base_url=https://generativelanguage.googleapis.com/v1beta/openai/ key=fake-gemini"
  expect_contains "stub sweep: runner still ran for 3.6-flash round after deepseek failed" \
    "$(cat "$runner_log")" \
    "runner-env model=gemini-3.6-flash base_url=https://generativelanguage.googleapis.com/v1beta/openai/ key=fake-gemini"

  expect_contains "stub sweep: summary reports the failed round" \
    "$stub_out" "deepseek-v4-flash	FAILED (exit 7)"

  # The DSN command must be invoked with the compose project/file (so it can
  # resolve the ephemeral postgres port for THIS stack) and its resolved
  # value must reach the runner via EVAL_V2_POSTGRES_DSN, for every round,
  # including the round whose runner subsequently fails.
  expect_contains "stub sweep: dsn command receives the compose project and file" \
    "$(cat "$dsn_log")" "pax-nexus-eval-v3 evals/v2/compose.yaml"
  expect_eq "stub sweep: dsn command invoked exactly once per round" \
    "$(wc -l < "$dsn_log" | tr -d ' ')" "3"
  expect_contains "stub sweep: deepseek round runner observes the resolved DSN" \
    "$(cat "$runner_log")" \
    "runner-env model=deepseek-v4-flash base_url=https://api.deepseek.com key=fake-deepseek dsn=${stub_dsn_value}"
  expect_contains "stub sweep: flash-lite round runner observes the resolved DSN" \
    "$(cat "$runner_log")" \
    "runner-env model=gemini-3.5-flash-lite base_url=https://generativelanguage.googleapis.com/v1beta/openai/ key=fake-gemini dsn=${stub_dsn_value}"

  rm -rf runs/eval-v3-sweep/stubprefix

  # Scenario 2: reset itself fails. Verifies up is never attempted for that
  # round and the failure is reported, not swallowed.
  resetfail_stack_log="$tmp/stack-resetfail.log"
  resetfail_runner_log="$tmp/runner-resetfail.log"
  : > "$resetfail_stack_log"
  : > "$resetfail_runner_log"

  resetfail_status=0
  resetfail_out=$(env $sweep_env EVAL_V3_SWEEP_MANIFEST="$manifest" \
    DEEPSEEK_API_KEY=fake-deepseek \
    EVAL_V3_STACK_CMD="$tmp/stack-stub.sh" EVAL_V3_RUNNER_CMD="$tmp/runner-stub.sh" \
    EVAL_V3_DSN_CMD="$tmp/dsn-stub.sh" \
    STACK_STUB_LOG="$resetfail_stack_log" RUNNER_STUB_LOG="$resetfail_runner_log" \
    STUB_RESET_FAIL=5 \
    "$sweep" resetfailprefix deepseek-v4-flash 2>&1) || resetfail_status=$?

  expect_eq "stub sweep: reset failure yields overall exit 1" "$resetfail_status" "1"
  expect_eq "stub sweep: reset failure logs only the reset call" \
    "$(cat "$resetfail_stack_log")" "stack reset"
  if [ -s "$resetfail_runner_log" ]; then
    fail "stub sweep: runner ran despite reset failure"
  fi
  expect_contains "stub sweep: reset failure is named on stderr" \
    "$resetfail_out" "stack reset failed"
  expect_contains "stub sweep: reset failure is reported in the summary" \
    "$resetfail_out" "deepseek-v4-flash	FAILED (stack reset exit 5)"

  rm -rf runs/eval-v3-sweep/resetfailprefix

  # Scenario 3: `up` succeeds but DSN resolution fails. Verifies the runner
  # is never invoked, the stack is still torn down (unlike a reset failure,
  # which skips the round entirely), and the summary names the DSN step
  # rather than reporting a bare exit code.
  dsnfail_stack_log="$tmp/stack-dsnfail.log"
  dsnfail_runner_log="$tmp/runner-dsnfail.log"
  dsnfail_dsn_log="$tmp/dsn-dsnfail.log"
  : > "$dsnfail_stack_log"
  : > "$dsnfail_runner_log"
  : > "$dsnfail_dsn_log"

  dsnfail_status=0
  dsnfail_out=$(env $sweep_env EVAL_V3_SWEEP_MANIFEST="$manifest" \
    DEEPSEEK_API_KEY=fake-deepseek \
    EVAL_V3_STACK_CMD="$tmp/stack-stub.sh" EVAL_V3_RUNNER_CMD="$tmp/runner-stub.sh" \
    EVAL_V3_DSN_CMD="$tmp/dsn-stub.sh" \
    STACK_STUB_LOG="$dsnfail_stack_log" RUNNER_STUB_LOG="$dsnfail_runner_log" \
    DSN_STUB_LOG="$dsnfail_dsn_log" \
    STUB_DSN_FAIL=9 \
    "$sweep" dsnfailprefix deepseek-v4-flash 2>&1) || dsnfail_status=$?

  expect_eq "stub sweep: DSN resolution failure yields overall exit 1" "$dsnfail_status" "1"
  expect_eq "stub sweep: DSN resolution failure still resets, ups, and tears down" \
    "$(awk '{print $2}' "$dsnfail_stack_log")" "$(printf 'reset\nup\ndsn\ndown')"
  if [ -s "$dsnfail_runner_log" ]; then
    fail "stub sweep: runner ran despite DSN resolution failure"
  fi
  expect_contains "stub sweep: DSN resolution failure is named on stderr" \
    "$dsnfail_out" "DSN resolution failed"
  expect_contains "stub sweep: DSN resolution failure is reported in the summary" \
    "$dsnfail_out" "deepseek-v4-flash	FAILED (DSN resolution failed)"

  rm -rf runs/eval-v3-sweep/dsnfailprefix
fi

out=$("$sweep" --dry-run 2>&1) && fail "missing prefix unexpectedly succeeded"
expect_contains "usage is printed without a prefix" "$out" "usage:"

expect_contains "sweep test is registered in test-scripts" \
  "$(sed -n '/^test-scripts:/,/^$/p' Makefile)" \
  "test-eval-v3-extractor-sweep.sh"

expect_contains "sweep target exists" \
  "$(cat Makefile)" "eval-v3-extractor-sweep:"

expect_contains "sweep target is phony" \
  "$(sed -n '/^\.PHONY:/p' Makefile)" "eval-v3-extractor-sweep"

expect_contains "example env documents the Gemini key" \
  "$(cat .env.eval-v2.example)" "GEMINI_API_KEY"

if grep -qE '^GEMINI_API_KEY=.{12,}' .env.eval-v2.example; then
  fail ".env.eval-v2.example appears to contain a real credential"
fi

expect_contains "README documents the sweep" \
  "$(cat evals/v3/README.md)" "eval-v3-extractor-sweep"

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all extractor sweep checks passed"
