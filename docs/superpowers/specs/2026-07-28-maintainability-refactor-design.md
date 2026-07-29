# Maintainability Refactor: Strategy Retirement, Dependency Whitelist, LLM Client Relocation

Date: 2026-07-28
Status: approved

Three coupled maintainability fixes, implemented in this order:

1. Move the LLM chat client out of `internal/llmwiki/workspace` into `internal/platform/llm`.
2. Rewrite the architecture dependency test from a blacklist to a default-deny whitelist.
3. Retire five extraction candidate strategies and document the retirement policy.

Ordering rationale: relocating the client first removes the `pagewiki → llmwiki`
dependency, so the whitelist never has to allow it; strategy deletion is
independent of the other two and lands last.

## Part 1 — LLM client relocation and documentation correction

### Problem

`internal/llmwiki` documents itself as a reserved future module, yet the
shipping PageWiki product (`internal/pagewiki/llm_session_planner.go`,
`llm_session_editor.go`) and `main.go` depend on its `workspace` package for a
generic LLM chat client. `CONTEXT-MAP.md` documents llmwiki as the wiki product
and never mentions pagewiki, recall, explorer, or platform.

### Change

New package `internal/platform/llm` (peer of `platform/observability`:
technical infrastructure that domain packages may import). Move, verbatim
except for package name:

- From `workspace/agent.go`: `ChatMessage`, `ChatRequest`, `ChatResponse`,
  `ChatClient`.
- From `workspace/deepseek.go`: `DeepSeekConfig`, `DeepSeekClient`,
  `NewDeepSeekClient` (file moves whole, with `deepseek_test.go`).

Update all references — `internal/llmwiki/workspace` internals,
`internal/pagewiki/llm_session_planner.go`, `llm_session_editor.go`,
`main.go`, `cmd/llmwiki-spike` — directly. No type aliases left behind.

### Documentation

- `CONTEXT-MAP.md`: rewrite Contexts and Relationships to list the live
  contexts — Session, Team Note, PageWiki (shipping wiki product), Recall,
  Evaluation, On-prem Identity, Operations, Explorer, Platform (infrastructure
  adapters) — and reposition LLM Wiki as an experimental spike
  (`effecteval`, `sessiondataset`, `cmd/llmwiki-spike`) plus a reserved name.
- `internal/llmwiki/doc.go` and `internal/llmwiki/CONTEXT.md`: same correction.
- `internal/pagewiki/CONTEXT.md`: add a short context description if missing.

## Part 2 — Default-deny architecture test

### Problem

`internal/architecture/dependencies_test.go` blacklists six directories.
`pagewiki`, `platform`, `deployment`, `explorer`, `operations`, and `eval` are
entirely unconstrained, and new packages are unconstrained by default. The
`pagewiki → llmwiki` dependency slipped in this way.

### Change

Rewrite the test to whitelist mode:

- Discover every top-level package under `internal/` with `os.ReadDir` at test
  time. A discovered package with no whitelist entry fails the test with a
  message telling the author to register an explicit dependency list.
- Two sub-package splits keep their own entries, mirroring today's special
  case: `teamnote/transport` and `pagewiki/transport` (transport is granted
  more than its domain).
- Each entry lists the complete set of allowed `internal/` import prefixes.
  Rules are exact-or-prefix matches, same as today's `hasModulePrefix`.

Whitelist (entries marked *as-built* are tightened to the actual current
imports during implementation — minimum set, no headroom):

| Package | Allowed internal imports |
|---|---|
| `session` | none |
| `sessionlake` | `session` |
| `teamnote` (excl. transport) | `session`, `sessionlake`, `platform/observability` |
| `teamnote/transport` | domain packages, `deployment`, `platform` (*as-built*) |
| `pagewiki` (excl. transport) | `session`, `sessionlake`, `platform/observability`, `platform/llm` |
| `pagewiki/transport` | `pagewiki`, `teamnote/transport` router (*as-built*) |
| `recall` | *as-built* (today: `teamnote` types, `session`, `sessionlake`) |
| `llmwiki` | `platform/llm` |
| `explorer` | *as-built* |
| `operations` | *as-built* |
| `platform` | `teamnote`, `pagewiki`, `deployment/onprem`, `explorer`, `session`, `sessionlake` (adapter direction) |
| `deployment` | domain packages + `platform` (*as-built*) |
| `eval` | unrestricted |
| `architecture` | none |

Kept as an explicit reverse assertion: no production package outside `eval`
imports `internal/eval`.

`pagewiki → llmwiki` is not granted; Part 1 removes the dependency before this
lands.

## Part 3 — Extraction strategy retirement

### Problem

`internal/teamnote/extractor/candidate_strategy.go` keeps ten live strategies;
one ships (selected via `-ldflags`), the rest carry permanent test and lint
cost with no retirement mechanism.

### Decision

Keep five: `source-clause-v1` (production default), `interaction-slim`,
`source-span-v1`, `source-span-v2` (active evals), `claim-card-v2` (newest
experiment direction).

Delete five, with all strategy-specific assets:

| Strategy | Strategy-specific deletions |
|---|---|
| `current` | `rollingSystemPromptV2` prompt body, `extractionProtocolV2RevisionCurrent` |
| `evidence-fidelity-v1` | its prompt and revision constant |
| `source-clause-implicit-state-v1` | its prompt and revision constant |
| `typed-2` | `v2_typed.go`, `v2_typed_prompt.go`, both typed decoders |
| `claim-card-v1` | `rollingSystemPromptClaimCardV1`, revision constant, its `case` in `rolling.go` |

Shared assets stay: `decodeExtractionResponseV2` / `decodeExtractionContentV2`
(used by kept strategies), `mapExtractionClaimCardV1` (used by `claim-card-v2`;
rename to `mapExtractionClaimCard`). Also shrink: constants and `V2Variant*`
aliases in `extractor.go`, both Makefile validation lists,
`scripts/test-extraction-candidate-builds.sh`, and all tests referencing
deleted strategies.

### Retirement policy (the mechanism)

A header comment on the strategy table in `candidate_strategy.go` plus a new
ADR in `docs/decisions/` (next free number):

- A strategy enters the table with a stated experiment goal and an eval exit
  condition.
- When the experiment concludes, the strategy is either promoted (becomes the
  build default) or deleted in the same change that records the conclusion.
  Git history is the archive; no dormant entries.

### Risk check

Before deleting the typed decoders, verify no production data stores
extraction content under the `typed-2` protocol revision
(`v2-typed-3-temporal-deterministic`); the other four retirees decode through
the retained V2 decoder, so saved content is unaffected.

## Acceptance

- `make lint` and `make coverage` pass; the 80% gate does not drop.
- The rewritten architecture test passes against the post-refactor tree and
  fails with a clear message when given an unregistered package.
- `make build` succeeds for each of the five kept strategies
  (`test-extraction-candidate-builds.sh`, shrunk to five).
- `grep -r "llmwiki" internal/pagewiki --include='*.go'` returns only
  `legacy_hydration.go` SQL identifiers (historical table names, untouched).

## Out of scope

- Splitting the 85-file `teamnote/transport/httpapi/handler` layer.
- `main.go` configuration refactor (52 env vars).
- The nil `WikiPath` in `recall.NewRouter` wiring.
- Frontend items (list-page abstraction, `wiki.ts` convergence).
