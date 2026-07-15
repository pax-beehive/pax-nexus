# Eval v2 HTML report renderer implementation plan

Status: implemented
Date: 2026-07-15

## Goal

`make eval-v2` publishes a self-contained `report.html` alongside the raw
CSV/JSONL artifacts. The report is generic over configured arms and categories,
preserves the raw artifact contract, and exposes every evaluated case rather
than only a few hand-picked examples.

## Completed work

- [x] Accept `html` as an output format and enable it by default.
- [x] Exclude `html` from the resumable trial config hash because it is a
  presentation-only option.
- [x] Carry `Question` on new `TrialResult` values and CSV/JSONL output.
- [x] Hydrate missing question and case metadata from the manifest for legacy
  completed results, without rerunning paid trials.
- [x] Make `Runner.Run` return the run record and hydrated results while keeping
  trial execution and artifact publication as separate responsibilities.
- [x] Add an exporter-owned HTML renderer callback so the report is written
  atomically and appears in `artifacts.json` only after successful publication.
- [x] Use configured arm order for stable display and color assignment.
- [x] Generate overall, pairwise, category, cost, and latency summaries.
- [x] Keep up to three representative field notes for quick orientation.
- [x] Add a complete, category-grouped case breakdown containing every arm's
  question, expected answer, status, token F1, baseline delta, answer, and error.
- [x] Use `html/template` auto-escaping and class-based colors; regression tests
  reject raw script injection and `ZgotmplZ` sanitizer output.
- [x] State clearly that token F1 is a lexical diagnostic and not semantic
  correctness, grounding, or safe-abstention scoring.
- [x] Update the example and local eval configuration to request HTML output.
- [x] Bump the artifact contract to `pax-eval-v2.3`.

## Verification

The implementation is complete only when all of these pass:

1. `go test ./internal/eval/v2/... ./cmd/team-memory-eval-v2`
2. `make lint test`
3. Resume the existing `groupmembench-finance-v2-gpt5mini` run without
   re-executing completed trials.
4. Confirm `report.html` and `artifacts.json.files.report` exist.
5. Inspect the rendered report in light and dark mode and confirm all 30 cases,
   all three arms, escaped answers, category summaries, and lexical-metric
   disclaimers are present.

## Deferred scoring work

The renderer deliberately does not invent semantic labels from token overlap.
A future eval-contract revision should add an explicit semantic-equivalence,
grounding, source-coverage, and safe-abstention judge. Until that judge exists,
the report must not label token-F1 wins as "correct answers."
