# Eval v3 Extractor Model Sweep

## Goal

Measure how the Team Note extraction model affects end-to-end answer quality,
by running the Eval v3 three-arm protocol once per candidate extractor model and
comparing the results.

Only the Team Note extractor varies. The consumer (`OPENCODE_MODEL`), the Mem0
extraction LLM (`MEM0_DEFAULT_LLM_MODEL`), and the judge (`EVAL_V2_JUDGE_MODEL`)
stay pinned to their current DeepSeek values across every round. Holding the
consumer fixed is what makes the rounds comparable: any accuracy delta is
attributable to the memory the extractor produced, not to the model reading it.

Candidate extractors:

| slug | model | base URL | key env |
| --- | --- | --- | --- |
| `deepseek-v4-flash` | `deepseek-v4-flash` | `https://api.deepseek.com` | `DEEPSEEK_API_KEY` |
| `gemini-3.5-flash-lite` | `gemini-3.5-flash-lite` | `https://generativelanguage.googleapis.com/v1beta/openai/` | `GEMINI_API_KEY` |
| `gemini-3.6-flash` | `gemini-3.6-flash` | `https://generativelanguage.googleapis.com/v1beta/openai/` | `GEMINI_API_KEY` |

The `deepseek-v4-flash` round is the baseline and must be re-run by the sweep
itself. Historical run artifacts are not a valid baseline: they were produced by
different code, different manifests, and in some cases predate the validity
gate.

Scale: run the full sweep against the micro-5 manifest
(`runs/groupmembench-v3-micro-canary-v1/manifest.five.json`, 5 cases) first, then
against a larger one.

Note that the existing full v3 selection,
`runs/groupmembench-v3-selection/manifest.json`, holds 30 cases, not 50.
`runs/groupmembench-v2-selection-50` is a v2 selection and is not a valid v3
manifest. A 50-case v3 selection has to be generated first with
`GROUPMEMBENCH_TOTAL_CASES=50 make eval-v3-prepare`
(`scripts/eval-v3-prepare-groupmembench.sh:8`). The sweep driver is agnostic to
which manifest is used; choosing between the existing 30-case selection and a
newly prepared 50-case one is a separate decision, made after the micro-5 sweep
has measured real per-round cost.

## Non-goals

- Varying the consumer, judge, or Mem0 extraction model.
- A write/read cross matrix (extractor A x consumer B).
- Adding matrix support to `cmd/team-memory-eval-v3`. A sweep is N sequential
  runs of an unchanged runner; encoding that in the runner would force changes
  to the provenance and validity schemas for no benefit.

## Key constraint: env files win over exported variables

`scripts/eval-v3-stack.sh:7` sources `scripts/load-eval-v3-env.sh`, which sources
`scripts/load-eval-v2-env.sh`. That script reads `.env` and `.env.eval-v2` under
`set -a` (`load-eval-v2-env.sh:3-19`), unconditionally overwriting anything the
caller exported beforehand.

Exporting `TEAM_MEMORY_EXTRACTOR_MODEL` before invoking the stack therefore does
not work. The codebase already solves this with an `_OVERRIDE` convention
(`load-eval-v2-env.sh:21-26`), applied after the env files are sourced and still
inside the `set -a` region so the values are exported. `TEAM_MEMORY_EXTRACTOR_MODE_OVERRIDE`
exists; the sweep needs the same for model, base URL, and API key.

Rewriting `.env.eval-v2` per round is rejected: it is a gitignored user file, and
a sweep that crashes mid-round would leave the developer's everyday environment
permanently pointed at a sweep provider.

## Design

### 1. Extractor overrides

Add three lines to `scripts/load-eval-v2-env.sh` after line 25, following the
existing pattern:

```sh
[ -z "${TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_MODEL="${TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE}"
[ -z "${TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_BASE_URL="${TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE}"
[ -z "${TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_API_KEY="${TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE}"
```

These are inert when unset, so every existing eval path is unaffected.

### 2. Per-model fragments

One committed, secret-free fragment per candidate at
`evals/v3/sweep/extractor-<slug>.env`, with three fields:

```sh
SWEEP_EXTRACTOR_MODEL=gemini-3.5-flash-lite
SWEEP_EXTRACTOR_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai/
SWEEP_EXTRACTOR_KEY_ENV=GEMINI_API_KEY
```

The `SWEEP_` prefix keeps a fragment from mutating real configuration if it is
ever sourced by accident. Credentials stay out of the fragments: the fragment
names the variable holding the key, and the driver resolves it indirectly from
the environment loaded out of the gitignored `.env` / `.env.eval-v2`.

### 3. Driver

`scripts/eval-v3-extractor-sweep.sh [--dry-run] <run-id-prefix> [slug...]`,
defaulting to all three slugs in table order.

The manifest is resolved as `EVAL_V3_SWEEP_MANIFEST`, falling back to
`runs/groupmembench-v3-micro-canary-v1/manifest.five.json`. The driver verifies
the file exists before starting round one, so a typo costs a second rather than
a round's worth of ingest. Every round in a sweep uses the same manifest;
comparing rounds run against different manifests is not meaningful.

Per round:

1. Read the fragment; resolve the key named by `SWEEP_EXTRACTOR_KEY_ENV`. If it
   is empty, fail that round immediately, before touching Docker.
2. Render the config from the template.
3. `eval-v3-stack.sh reset` (`down -v`), then `up <manifest> <run-id>`.
4. Run `cmd/team-memory-eval-v3` against the rendered config, with the three
   `_OVERRIDE` variables exported.
5. `eval-v3-stack.sh down`, and record the round's exit status.

A failed round does not abort the sweep. The driver prints a per-round summary
table at the end and exits non-zero if any round failed, so one bad provider
does not cost the results of the rounds that would have succeeded.

`--dry-run` prints the planned rounds — model, base URL, whether the key
resolves, run ID, output directory — and exits without invoking Docker. This is
the only way to check a multi-hour sweep's wiring in seconds, and it is what the
tests exercise.

### 4. Run identity

Template `evals/v3/config.sweep-template.yaml`, identical to
`evals/v3/config.example.yaml` except for the placeholders and one added
`runtime_env` entry. Rendered configs are written to
`runs/eval-v3-sweep/<prefix>/configs/<slug>.yaml`; `runs/` is already gitignored
(`.gitignore:48`).

- `run.id` = `<prefix>-<slug>`
- `run.output_dir` = `runs/eval-v3-sweep/<prefix>/<slug>`

`eval-v3-stack.sh:29` derives `TEAM_MEMORY_API_KEYS` from the run ID, so each
round automatically gets non-colliding scope keys.

### 5. Comparability guardrails

**Reset before every round.** This is the load-bearing guarantee. If the
extractor changes but the previous round's Team Note rows survive in Postgres,
all three arms are scored against contaminated memory, and nothing in the
artifacts reveals it. `down -v` also clears the Mem0 volumes, so the Mem0 arm
rebuilds its memory each round with an unchanged model — making it a per-round
control for run-to-run noise.

**Record the base URL.** Add `TEAM_MEMORY_EXTRACTOR_BASE_URL` to the template's
`runtime_env`. It passes the secret-name filter (`internal/eval/v2/model.go:408`
rejects only `KEY`/`TOKEN`/`SECRET`/`PASSWORD`), and `ResolveRuntime`
(`model.go:466-476`) hard-fails on an empty value. Two providers can expose the
same model name, so without the base URL in provenance the rounds are not
distinguishable after the fact; with it, forgetting to set it becomes a startup
failure instead of a silently wrong run.

**Per-round validity.** `internal/eval/v3/validity.go:218` fails a run whose
Team Note ingest reports `memory_items <= 0`. An extractor that returns
unparseable output and writes nothing is therefore reported as an invalid run,
not as a genuine 0% accuracy.

## Known blocker: Gemini project access

The supplied `GEMINI_API_KEY` authenticates and can enumerate models — both
`gemini-3.5-flash-lite` and `gemini-3.6-flash` are present in the account's
model list — but every generation call returns:

```
403 PERMISSION_DENIED: Your project has been denied access. Please contact support.
```

Verified against the OpenAI-compatible `chat/completions` endpoint and the native
`generateContent` endpoint, with both `Authorization: Bearer` and
`x-goog-api-key`, and with `gemini-2.5-flash` as well as the target models. The
denial is project-level and not reachable from this repository.

Consequences: the `deepseek-v4-flash` baseline round runs today, and the Gemini
rounds are blocked until the account is granted generation access or a different
key is supplied. Because the credential is referenced indirectly through
`SWEEP_EXTRACTOR_KEY_ENV`, unblocking requires only replacing the value in
`.env.eval-v2`; no code or fragment changes.

## Contingency: JSON response format

`internal/teamnote/extractor/openai.go:247-248` sends
`response_format: {"type": "json_object"}` unconditionally. Whether Gemini's
OpenAI-compatibility layer accepts it is untested, because project access is
denied. If it rejects the field, add `TEAM_MEMORY_EXTRACTOR_RESPONSE_FORMAT`
(default `json_object`, alternative `none`) to the extractor configuration.

This is deliberately not built up front. The micro-5 round exists to surface it
cheaply, and building the toggle before knowing it is needed would be speculative.

## Testing

`scripts/test-eval-v3-extractor-sweep.sh`, following the existing
`scripts/test-*.sh` convention and wired into `make test-scripts`. All assertions
run against `--dry-run`, so no Docker and no network:

- The default sweep plans all three slugs in order, each with the model and base
  URL from its fragment.
- A round whose key variable is unset fails, and the failure names the missing
  variable.
- The rendered config carries the expected `run.id`, `output_dir`, and a
  `runtime_env` containing `TEAM_MEMORY_EXTRACTOR_BASE_URL`.
- An unknown slug is rejected rather than silently skipped.

## Cost

The v3 protocol builds sources full-domain, independent of the question count
(`evals/v3/README.md`), which the receipts confirm: a 6-case run still ingested
1605 events across 15 sessions and 8 actors. Ingest cost is therefore identical
at every manifest size, and only the trial phase scales with case count. That is
what makes the micro-5 sweep worth running first — it validates the entire
ingest path at the trial cost of five cases.

Observed trial-phase timings from historical runs: 9 trials in 1.2 min, 15 in
2.2 min, 18 in 15.4 min. The last is an outlier that the artifacts do not
explain; it is assumed to be retries.

| phase | micro-5 (5 cases) | full selection (30-50 cases) |
| --- | --- | --- |
| reset + up | 3-5 min | 3-5 min |
| full-domain ingest | 8-20 min | 8-20 min |
| preflight | ~1 min | ~1 min |
| trials + judge | 2-4 min | 15-90 min |
| down | ~1 min | ~1 min |
| **per round** | **15-30 min** | **30-120 min** |

Three rounds: roughly 1-1.5 h at micro-5, 1.5-6 h at full selection. The dominant
unknown is per-provider throughput during ingest, which runs roughly 64
sequential extraction jobs at the current `TEAM_MEMORY_SLICE_EVENT_LIMIT=25` and
`TEAM_MEMORY_MAX_SLICES_PER_JOB=1`. The first non-DeepSeek round will measure it
directly and make the 50-case extrapolation real.
