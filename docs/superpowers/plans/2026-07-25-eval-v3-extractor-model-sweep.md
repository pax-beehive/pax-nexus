# Eval v3 Extractor Model Sweep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the Eval v3 three-arm protocol once per candidate Team Note extraction model, holding every other model fixed, so accuracy differences are attributable to the extracted memory.

**Architecture:** A sweep is N sequential runs of the unchanged v3 runner. A driver script renders one config per model, resets the Docker stack between rounds, and injects the extractor model through the `_OVERRIDE` convention that already exists in `scripts/load-eval-v2-env.sh`. No Go code changes.

**Tech Stack:** POSIX `sh`, Docker Compose, Make, existing Go runner `cmd/team-memory-eval-v3`.

## Global Constraints

- Only the Team Note extractor varies. `OPENCODE_MODEL`, `MEM0_DEFAULT_LLM_MODEL`, and `EVAL_V2_JUDGE_MODEL` stay at their current DeepSeek values in every round.
- Scripts are POSIX `sh` with `set -eu`, starting with `repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)` then `cd "$repo_root"` — matching `scripts/test-recall-candidate-builds.sh:1-5`.
- No credential may appear in a committed file. Fragments name the variable holding a key; the value lives only in gitignored `.env` / `.env.eval-v2`.
- `runtime_env` entries must not contain `KEY`, `TOKEN`, `SECRET`, or `PASSWORD` — `internal/eval/v2/model.go:408` rejects them.
- Every round resets the stack with `down -v` before ingest. A round that reuses a previous round's memory is invalid and silently so.
- Candidate slugs, in sweep order: `deepseek-v4-flash`, `gemini-3.5-flash-lite`, `gemini-3.6-flash`.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `scripts/load-eval-v2-env.sh` (modify) | Add three `_OVERRIDE` lines so a caller can beat the env files. |
| `evals/v3/sweep/extractor-<slug>.env` (create x3) | Declare one candidate: model, base URL, name of the key variable. No secrets. |
| `evals/v3/config.sweep-template.yaml` (create) | v3 config with `__RUN_ID__`/`__OUTPUT_DIR__`/`__MANIFEST__` placeholders and the base URL in `runtime_env`. |
| `scripts/eval-v3-extractor-sweep.sh` (create) | Driver: validate, render, reset, up, run, down, summarize. |
| `scripts/test-eval-v3-extractor-sweep.sh` (create) | Docker-free, network-free tests driven by `--dry-run`. |
| `Makefile` (modify) | `eval-v3-extractor-sweep` target; register the test in `test-scripts`. |
| `.env.eval-v2.example` (modify) | Document `GEMINI_API_KEY` with a placeholder. |
| `evals/v3/README.md` (modify) | Document how to run and interpret a sweep. |

---

### Task 1: Extractor overrides in the env loader

`scripts/eval-v3-stack.sh:7` sources the env loader, which reads `.env` and `.env.eval-v2` under `set -a` (`scripts/load-eval-v2-env.sh:3-19`) and overwrites anything the caller exported. The `_OVERRIDE` lines at `load-eval-v2-env.sh:21-26` run after that and are still inside the `set -a` region, so they win and stay exported. This task extends that list.

**Files:**
- Modify: `scripts/load-eval-v2-env.sh:26`
- Test: `scripts/test-eval-v3-extractor-sweep.sh` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: three environment variables honored by every eval path — `TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE`, `TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE`, `TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE`. Each is inert when unset or empty.

- [ ] **Step 1: Write the failing test**

Create `scripts/test-eval-v3-extractor-sweep.sh`:

```sh
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

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all extractor sweep checks passed"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `chmod +x scripts/test-eval-v3-extractor-sweep.sh && ./scripts/test-eval-v3-extractor-sweep.sh`
Expected: FAIL — `overrides beat env files: expected [swept-model|...], got [from-base-env|...]`. The first and third checks already pass.

- [ ] **Step 3: Write minimal implementation**

In `scripts/load-eval-v2-env.sh`, immediately after line 26 (the `PAXM_PASSIVE_PROVIDER_TIMEOUT_OVERRIDE` line) and before the `if "${eval_v2_restore_allexport}"` block:

```sh
[ -z "${TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_MODEL="${TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE}"
[ -z "${TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_BASE_URL="${TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE}"
[ -z "${TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_API_KEY="${TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE}"
```

Placement matters: inside the `set -a` region so the values export, and after the env files are sourced so they are not overwritten.

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/test-eval-v3-extractor-sweep.sh`
Expected: PASS — `all extractor sweep checks passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/load-eval-v2-env.sh scripts/test-eval-v3-extractor-sweep.sh
git commit -m "feat(eval): allow extractor model, base URL, and key to be overridden

The env loader sources .env and .env.eval-v2 under set -a, which
overwrites anything an caller exported. Extend the existing _OVERRIDE
convention so a sweep can select an extraction provider per round."
```

---

### Task 2: Candidate fragments and config template

**Files:**
- Create: `evals/v3/sweep/extractor-deepseek-v4-flash.env`
- Create: `evals/v3/sweep/extractor-gemini-3.5-flash-lite.env`
- Create: `evals/v3/sweep/extractor-gemini-3.6-flash.env`
- Create: `evals/v3/config.sweep-template.yaml`
- Test: `scripts/test-eval-v3-extractor-sweep.sh` (extend)

**Interfaces:**
- Consumes: nothing.
- Produces: each fragment defines exactly `SWEEP_EXTRACTOR_MODEL`, `SWEEP_EXTRACTOR_BASE_URL`, `SWEEP_EXTRACTOR_KEY_ENV` (all strings). The template defines the placeholders `__RUN_ID__`, `__OUTPUT_DIR__`, `__MANIFEST__`, each substituted verbatim by Task 3.

- [ ] **Step 1: Write the failing test**

Append to `scripts/test-eval-v3-extractor-sweep.sh`, immediately before the final `if [ "$failures" -ne 0 ]` block:

```sh
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/test-eval-v3-extractor-sweep.sh`
Expected: FAIL — `fragment missing: evals/v3/sweep/extractor-deepseek-v4-flash.env` and `template missing: evals/v3/config.sweep-template.yaml`

- [ ] **Step 3: Write minimal implementation**

`evals/v3/sweep/extractor-deepseek-v4-flash.env`:

```sh
# Eval v3 extractor sweep candidate. No credentials here: the driver resolves
# SWEEP_EXTRACTOR_KEY_ENV from the gitignored .env / .env.eval-v2.
SWEEP_EXTRACTOR_MODEL=deepseek-v4-flash
SWEEP_EXTRACTOR_BASE_URL=https://api.deepseek.com
SWEEP_EXTRACTOR_KEY_ENV=DEEPSEEK_API_KEY
```

`evals/v3/sweep/extractor-gemini-3.5-flash-lite.env`:

```sh
# Eval v3 extractor sweep candidate. No credentials here: the driver resolves
# SWEEP_EXTRACTOR_KEY_ENV from the gitignored .env / .env.eval-v2.
SWEEP_EXTRACTOR_MODEL=gemini-3.5-flash-lite
SWEEP_EXTRACTOR_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai/
SWEEP_EXTRACTOR_KEY_ENV=GEMINI_API_KEY
```

`evals/v3/sweep/extractor-gemini-3.6-flash.env`:

```sh
# Eval v3 extractor sweep candidate. No credentials here: the driver resolves
# SWEEP_EXTRACTOR_KEY_ENV from the gitignored .env / .env.eval-v2.
SWEEP_EXTRACTOR_MODEL=gemini-3.6-flash
SWEEP_EXTRACTOR_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai/
SWEEP_EXTRACTOR_KEY_ENV=GEMINI_API_KEY
```

`evals/v3/config.sweep-template.yaml` — copied from `evals/v3/config.example.yaml`, with the three placeholders and `TEAM_MEMORY_EXTRACTOR_BASE_URL` added to `runtime_env`:

```yaml
version: v3
run:
  id: __RUN_ID__
  dataset: GroupMemBench
  manifest: __MANIFEST__
  output_dir: __OUTPUT_DIR__
  parallelism: 3
store:
  dsn_env: EVAL_V2_POSTGRES_DSN
baseline_arm: no_memory_team
answerer_seed: pax-eval-v3-answerer-1
mem0_reproduction_level: comparable_baseline
retry_failed: false
trial_timeout: 8m
runtime_env:
  - OPENCODE_MODEL
  - OPENCODE_VERSION
  - EVAL_V2_JUDGE_MODEL
  - EVAL_V2_JUDGE_THINKING
  - TEAM_MEMORY_EXTRACTOR_MODEL
  - TEAM_MEMORY_EXTRACTOR_BASE_URL
  - TEAM_MEMORY_PROMPT_VERSION
  - TEAM_MEMORY_EXTRACTION_CONTEXT_MODE
  - TEAM_MEMORY_EXTRACTION_VERSION
  - TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY
  - TEAM_MEMORY_WORKER_JOB_TIMEOUT
  - TEAM_MEMORY_WORKER_MAX_ATTEMPTS
  - MEM0_IMAGE
  - MEM0_DEFAULT_LLM_MODEL
  - MEM0_DEFAULT_EMBEDDER_MODEL
  - MEM0_EVAL_USER_ID
  - MEM0_EVAL_AGENT_ID
  - MEM0_SCORE_SEMANTICS
  - MEM0_SEARCH_SCOPE_PAYLOAD
  - PAXM_EXPECTED_VERSION
output:
  formats: [csv, jsonl, html]
before_run:
  program: scripts/eval-v3-opencode.sh
  args: [ingest-domain, all]
preflight:
  program: scripts/eval-v3-opencode.sh
  args: [preflight, all]
judge:
  program: scripts/eval-v2-judge.sh
arms:
  - name: no_memory_team
    consumer:
      program: scripts/eval-v3-opencode.sh
      args: [consumer, no_memory_team]
  - name: groupmembench_mem0
    consumer:
      program: scripts/eval-v3-opencode.sh
      args: [consumer, groupmembench_mem0]
  - name: private_sqlite_plus_team_note
    consumer:
      program: scripts/eval-v3-opencode.sh
      args: [consumer, private_sqlite_plus_team_note]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/test-eval-v3-extractor-sweep.sh`
Expected: PASS — `all extractor sweep checks passed`

- [ ] **Step 5: Commit**

```bash
git add evals/v3/sweep evals/v3/config.sweep-template.yaml scripts/test-eval-v3-extractor-sweep.sh
git commit -m "feat(eval): add extractor sweep candidates and config template

Fragments name the variable holding each provider's key rather than the
key itself, so swapping credentials never touches a committed file. The
template adds TEAM_MEMORY_EXTRACTOR_BASE_URL to runtime_env: two providers
can expose the same model name, and ResolveRuntime hard-fails on an empty
value, turning a forgotten base URL into a startup error."
```

---

### Task 3: Sweep driver

**Files:**
- Create: `scripts/eval-v3-extractor-sweep.sh`
- Test: `scripts/test-eval-v3-extractor-sweep.sh` (extend)

**Interfaces:**
- Consumes: `TEAM_MEMORY_EXTRACTOR_{MODEL,BASE_URL,API_KEY}_OVERRIDE` from Task 1; the fragments and template from Task 2.
- Produces: CLI `scripts/eval-v3-extractor-sweep.sh [--dry-run] <run-id-prefix> [slug...]`. Reads `EVAL_V3_SWEEP_MANIFEST` (default `runs/groupmembench-v3-micro-canary-v1/manifest.five.json`). Writes rendered configs to `runs/eval-v3-sweep/<prefix>/configs/<slug>.yaml` and run output to `runs/eval-v3-sweep/<prefix>/<slug>`. Exit 2 on usage or validation error, 1 if any round failed, 0 otherwise. One `round ` line per candidate on stdout in dry-run.

- [ ] **Step 1: Write the failing test**

Append to `scripts/test-eval-v3-extractor-sweep.sh`, before the final `if [ "$failures" -ne 0 ]` block:

```sh
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
fi

out=$("$sweep" --dry-run 2>&1) && fail "missing prefix unexpectedly succeeded"
expect_contains "usage is printed without a prefix" "$out" "usage:"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/test-eval-v3-extractor-sweep.sh`
Expected: FAIL — `dry run exited non-zero: sh: ./scripts/eval-v3-extractor-sweep.sh: No such file or directory`

- [ ] **Step 3: Write minimal implementation**

Create `scripts/eval-v3-extractor-sweep.sh`:

```sh
#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

usage() {
  echo "usage: $0 [--dry-run] <run-id-prefix> [slug...]" >&2
  exit 2
}

dry_run=false
if [ "${1:-}" = "--dry-run" ]; then
  dry_run=true
  shift
fi

prefix="${1:-}"
[ -n "$prefix" ] || usage
shift

if [ "$#" -gt 0 ]; then
  slugs="$*"
else
  slugs="deepseek-v4-flash gemini-3.5-flash-lite gemini-3.6-flash"
fi

manifest="${EVAL_V3_SWEEP_MANIFEST:-runs/groupmembench-v3-micro-canary-v1/manifest.five.json}"
template=evals/v3/config.sweep-template.yaml
sweep_root="runs/eval-v3-sweep/${prefix}"

if [ ! -f "$manifest" ]; then
  echo "manifest not found: ${manifest}" >&2
  exit 2
fi
if [ ! -f "$template" ]; then
  echo "config template not found: ${template}" >&2
  exit 2
fi

# Reject every unknown slug before doing any work, so a typo costs a second
# rather than a round of full-domain ingest.
for slug in $slugs; do
  if [ ! -f "evals/v3/sweep/extractor-${slug}.env" ]; then
    echo "unknown extractor slug: ${slug}" >&2
    exit 2
  fi
done

# Load .env / .env.eval-v2 so provider credentials are resolvable here. Child
# scripts source them again; that is harmless because the _OVERRIDE variables
# exported below are applied after each of those loads.
. ./scripts/load-eval-v3-env.sh

mkdir -p "${sweep_root}/configs"

summary=""
overall=0

for slug in $slugs; do
  SWEEP_EXTRACTOR_MODEL=""
  SWEEP_EXTRACTOR_BASE_URL=""
  SWEEP_EXTRACTOR_KEY_ENV=""
  . "./evals/v3/sweep/extractor-${slug}.env"

  if [ -z "$SWEEP_EXTRACTOR_MODEL" ] || [ -z "$SWEEP_EXTRACTOR_BASE_URL" ] || [ -z "$SWEEP_EXTRACTOR_KEY_ENV" ]; then
    echo "fragment for ${slug} is incomplete" >&2
    exit 2
  fi

  key_value=$(eval "printf '%s' \"\${${SWEEP_EXTRACTOR_KEY_ENV}:-}\"")
  if [ -n "$key_value" ]; then
    key_state=present
  else
    key_state=missing
  fi

  run_id="${prefix}-${slug}"
  output_dir="${sweep_root}/${slug}"
  config="${sweep_root}/configs/${slug}.yaml"

  sed -e "s|__RUN_ID__|${run_id}|g" \
      -e "s|__OUTPUT_DIR__|${output_dir}|g" \
      -e "s|__MANIFEST__|${manifest}|g" \
      "$template" > "$config"

  echo "round slug=${slug} model=${SWEEP_EXTRACTOR_MODEL} base_url=${SWEEP_EXTRACTOR_BASE_URL} key_env=${SWEEP_EXTRACTOR_KEY_ENV} key=${key_state} run_id=${run_id} output_dir=${output_dir} config=${config}"

  if [ "$key_state" = missing ]; then
    echo "  ${SWEEP_EXTRACTOR_KEY_ENV} is empty; set it in .env.eval-v2" >&2
    summary="${summary}${slug}\tSKIPPED (no ${SWEEP_EXTRACTOR_KEY_ENV})\n"
    overall=1
    continue
  fi

  if [ "$dry_run" = true ]; then
    summary="${summary}${slug}\tPLANNED\n"
    continue
  fi

  TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE="$SWEEP_EXTRACTOR_MODEL"
  TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE="$SWEEP_EXTRACTOR_BASE_URL"
  TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE="$key_value"
  export TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE
  export TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE
  export TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE

  # down -v before every round. Surviving Team Note rows from the previous
  # extractor would corrupt all three arms with nothing in the artifacts to
  # reveal it.
  ./scripts/eval-v3-stack.sh reset || true

  status=0
  if ./scripts/eval-v3-stack.sh up "$manifest" "$run_id"; then
    GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" \
      go run ./cmd/team-memory-eval-v3 -config "$config" || status=$?
  else
    status=1
  fi

  ./scripts/eval-v3-stack.sh down || true

  if [ "$status" -eq 0 ]; then
    summary="${summary}${slug}\tOK\n"
  else
    summary="${summary}${slug}\tFAILED (exit ${status})\n"
    overall=1
  fi
done

echo ""
echo "sweep summary (${prefix}):"
printf "%b" "$summary"
exit "$overall"
```

Make it executable: `chmod +x scripts/eval-v3-extractor-sweep.sh`

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/test-eval-v3-extractor-sweep.sh`
Expected: PASS — `all extractor sweep checks passed`

- [ ] **Step 5: Commit**

```bash
git add scripts/eval-v3-extractor-sweep.sh scripts/test-eval-v3-extractor-sweep.sh
git commit -m "feat(eval): add Eval v3 extractor model sweep driver

Runs the unchanged v3 runner once per candidate extractor, resetting the
stack between rounds so no round inherits the previous extractor's memory.
A failed round does not abort the sweep, so one bad provider does not cost
the rounds that would have succeeded. --dry-run validates the whole plan
without Docker."
```

---

### Task 4: Make targets and documentation

**Files:**
- Modify: `Makefile:31` (`.PHONY` list), `Makefile:100` (`test-scripts` recipe), `Makefile:205` (after the `eval-v3-reset` target)
- Modify: `.env.eval-v2.example`
- Modify: `evals/v3/README.md`

**Interfaces:**
- Consumes: `scripts/eval-v3-extractor-sweep.sh` and `scripts/test-eval-v3-extractor-sweep.sh` from Task 3.
- Produces: `make eval-v3-extractor-sweep [PREFIX=...] [SLUGS=...] [MANIFEST=...]`, and the sweep test running as part of `make test-scripts`.

- [ ] **Step 1: Write the failing test**

Append to `scripts/test-eval-v3-extractor-sweep.sh`, before the final `if [ "$failures" -ne 0 ]` block:

```sh
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/test-eval-v3-extractor-sweep.sh`
Expected: FAIL — four failures: `sweep test is registered in test-scripts`, `sweep target exists`, `sweep target is phony`, `example env documents the Gemini key`, `README documents the sweep`

- [ ] **Step 3: Write minimal implementation**

In `Makefile`, append `eval-v3-extractor-sweep` to the `.PHONY:` list on line 31 (after `eval-v3-reset`).

In the `test-scripts:` recipe, add a line after `./scripts/test-zep-native-acceptance.sh`:

```make
	./scripts/test-eval-v3-extractor-sweep.sh
```

After the `eval-v3-reset:` target, add:

```make
eval-v3-extractor-sweep:
	@prefix="$(PREFIX)"; prefix="$${prefix:-$${EVAL_V3_SWEEP_PREFIX:-extractor-sweep}}"; \
		manifest="$(MANIFEST)"; \
		EVAL_V3_SWEEP_MANIFEST="$${manifest:-$${EVAL_V3_SWEEP_MANIFEST:-runs/groupmembench-v3-micro-canary-v1/manifest.five.json}}" \
		./scripts/eval-v3-extractor-sweep.sh $(DRY_RUN) "$$prefix" $(SLUGS)
```

In `.env.eval-v2.example`, append:

```sh
# Eval v3 extractor sweep. Team Note extraction only; the consumer, Mem0, and
# the judge stay on DeepSeek. Referenced indirectly by
# evals/v3/sweep/extractor-gemini-*.env via SWEEP_EXTRACTOR_KEY_ENV.
GEMINI_API_KEY=
```

In `evals/v3/README.md`, append:

```markdown
## Extractor model sweep

Run the three-arm protocol once per candidate Team Note extraction model,
holding the consumer, Mem0 extraction LLM, and judge fixed, so any accuracy
delta is attributable to the extracted memory rather than the model reading it.

Check the plan without starting Docker:

```bash
make eval-v3-extractor-sweep DRY_RUN=--dry-run PREFIX=my-sweep
```

Run it:

```bash
make eval-v3-extractor-sweep PREFIX=my-sweep
```

Candidates live in `evals/v3/sweep/extractor-<slug>.env`. Each names the
variable holding its provider key rather than the key itself; set the value in
the gitignored `.env.eval-v2`. Restrict a run to specific candidates with
`SLUGS="deepseek-v4-flash gemini-3.6-flash"`, and change the question set with
`MANIFEST=...`.

Artifacts land in `runs/eval-v3-sweep/<prefix>/<slug>/`, with the rendered
config for each round under `runs/eval-v3-sweep/<prefix>/configs/`.

The driver resets the stack (`down -v`) before every round. This is required,
not hygiene: Team Note rows surviving from the previous extractor would corrupt
all three arms, and nothing in the artifacts would reveal it.

Read `validity.json` before comparing any numbers across rounds. A round whose
extractor produced no memory is reported as invalid rather than as a genuine
zero accuracy.
```

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/test-eval-v3-extractor-sweep.sh`
Expected: PASS — `all extractor sweep checks passed`

Then verify the target and the wider suite:

Run: `make eval-v3-extractor-sweep DRY_RUN=--dry-run PREFIX=maketest && rm -rf runs/eval-v3-sweep/maketest`
Expected: three `round ` lines, then `sweep summary (maketest):` with three `PLANNED` entries, exit 0.

Run: `make test-scripts`
Expected: PASS, including `all extractor sweep checks passed`.

- [ ] **Step 5: Commit**

```bash
git add Makefile .env.eval-v2.example evals/v3/README.md scripts/test-eval-v3-extractor-sweep.sh
git commit -m "feat(eval): wire extractor sweep into make and document it

Registers the sweep test in test-scripts so the harness cannot silently
rot, and documents why every round resets the stack and why validity.json
must be read before comparing rounds."
```

---

### Task 5: Execute the micro-5 sweep

The harness is now testable but unproven against a real provider. This task runs it and confirms the artifacts support the comparison the sweep exists to make.

**Files:**
- Create: `runs/eval-v3-sweep/micro5-<date>/` (gitignored output)
- Create: `docs/superpowers/notes/2026-07-25-extractor-sweep-micro5-results.md`

**Interfaces:**
- Consumes: everything from Tasks 1-4.
- Produces: a results note recording per-round validity, per-arm accuracy, and measured round duration — the input to deciding the second-stage manifest size.

- [ ] **Step 1: Confirm the plan and credentials before spending hours**

Run: `make eval-v3-extractor-sweep DRY_RUN=--dry-run PREFIX=micro5-20260725`
Expected: three `round ` lines, every one showing `key=present`. If any shows `key=missing`, set that variable in `.env.eval-v2` and re-run before continuing.

- [ ] **Step 2: Confirm the fixture manifest exists**

Run: `ls runs/groupmembench-v3-micro-canary-v1/manifest.five.json`
Expected: the path prints. If it does not exist, run `make eval-v3-prepare` first.

- [ ] **Step 3: Run the sweep**

Run: `make eval-v3-extractor-sweep PREFIX=micro5-20260725 2>&1 | tee /tmp/micro5-sweep.log`
Expected: three rounds execute in order; the final `sweep summary (micro5-20260725):` block lists each slug. Budget 15-30 minutes per round.

This is the step that first exercises a non-DeepSeek extractor end to end. If a Gemini round fails, capture the worker logs before the stack is torn down again — `docker compose -p pax-nexus-eval-v3 -f evals/v2/compose.yaml logs team-memory` — and check whether the failure is extraction-side or transport-side.

- [ ] **Step 4: Verify each round is valid before reading any number**

Run:

```bash
for d in runs/eval-v3-sweep/micro5-20260725/*/; do
  [ -d "$d" ] || continue
  printf '%s: ' "$(basename "$d")"
  python3 -c "import json,sys;o=json.load(open('$d/validity.json'));print('valid' if o.get('valid') else 'INVALID', o.get('failures') or '')" 2>/dev/null \
    || echo "no validity.json"
done
```

Expected: every round prints `valid`. A round printing `INVALID` must not contribute to the comparison; its failure list names the check that failed.

Then compare the arms:

```bash
for d in runs/eval-v3-sweep/micro5-20260725/*/; do
  [ -d "$d" ] || continue
  echo "== $(basename "$d")"
  awk -F, 'NR==1 || $1=="overall"' "$d/summary.csv" | cut -d, -f1-9
done
```

Expected: each round prints the three arms with their accuracy.

- [ ] **Step 5: Record the results**

Create `docs/superpowers/notes/2026-07-25-extractor-sweep-micro5-results.md` containing, for each of the three rounds: the validity verdict, the per-arm accuracy from `summary.csv`, the wall-clock duration, and any provider-specific failure observed. Close with a recommendation on the second-stage manifest — the existing 30-case `runs/groupmembench-v3-selection/manifest.json`, or a new 50-case selection from `GROUPMEMBENCH_TOTAL_CASES=50 make eval-v3-prepare` — justified by the measured per-round cost.

State plainly that five cases cannot separate extractor quality; the micro-5 sweep proves the pipeline, and the full-selection sweep produces the finding.

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/notes/2026-07-25-extractor-sweep-micro5-results.md
git commit -m "docs: record micro-5 extractor sweep results

Reports per-round validity, per-arm accuracy, and measured duration, and
recommends the manifest for the full sweep based on observed cost."
```

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
| --- | --- |
| Extractor overrides | 1 |
| Per-model fragments | 2 |
| Driver, dry-run, non-aborting rounds | 3 |
| Run identity and rendered configs | 2 (template), 3 (rendering) |
| Reset before every round | 3 |
| Base URL in `runtime_env` | 2 |
| Per-round validity | 4 (documented), 5 (enforced in verification) |
| Provider verification | already done before planning; re-confirmed in 5 Step 1 |
| Testing | 1-4, each extending the same test script |
| Cost / manifest choice | 5 Step 5 |

No spec requirement is unassigned. The spec's contingency section was dropped deliberately: `response_format: json_object` was verified working on both Gemini models, so building a toggle would be speculative.

**Placeholder scan:** No TBD/TODO. Every code step contains literal file content. The one judgement call, the second-stage manifest, is deferred to Task 5 Step 5 with the two concrete options and the criterion named.

**Type consistency:** The three fragment fields (`SWEEP_EXTRACTOR_MODEL`, `SWEEP_EXTRACTOR_BASE_URL`, `SWEEP_EXTRACTOR_KEY_ENV`) are spelled identically in Tasks 2 and 3. The three override variables are spelled identically in Tasks 1 and 3. The `round ` line format asserted in Task 3's test matches the `echo` in Task 3's implementation, field for field. Paths `runs/eval-v3-sweep/<prefix>/{configs,<slug>}` are consistent across Tasks 3, 4, and 5.
