# Eval v2 HTML report renderer

Status: approved
Date: 2026-07-15

## Problem

Eval v2 runs (`make eval-v2`) already produce `trials.csv`, `trials.jsonl`,
`summary.csv`, `pairwise.csv`, and `artifacts.json`. Turning those into a
readable comparison currently means hand-building an HTML report after every
run. That doesn't scale past a one-off: it needs to happen automatically,
from whatever arms and cases a given run actually contains.

## Goals

- `make eval-v2` produces a self-contained `report.html` next to the other
  artifacts, with no separate step.
- The renderer is generic over the run: arms, the baseline arm, and
  categories are all read from the trial data, not hardcoded. Adding a 4th
  arm or renaming one requires no renderer changes.
- Untrusted content (model answers, error strings) is HTML-escaped by
  construction, not by convention.
- The visual design matches the hand-built report already approved this
  session: stat tiles per arm, a token-F1 bar chart, a win/loss/tie summary
  vs. baseline, a by-category table, cost/latency bars, a small set of
  concrete example answers ("field notes"), an expandable breakdown of every
  case and arm, and a methodology footer. Light and dark mode both styled.
- Existing completed runs hydrate question text from the case manifest, so
  adding the report does not require rerunning paid trials.
- `artifacts.json` declares `report.html` only after the report has been
  rendered and atomically published.

## Non-goals

- No standalone "regenerate report from an old run's artifacts" command.
  `report.html` is produced by the same process that writes the other
  artifacts; rerunning `make eval-v2` on a completed run already regenerates
  everything for free via the existing resume path.
- No new charting dependency. Bars are plain divs with computed widths, as
  in the hand-built version.
- No user-facing configuration of the report layout itself (which sections
  appear, thresholds, etc.) beyond the existing `output.formats` on/off
  switch.

## Architecture

New package `internal/eval/v2/render`, imported by
`cmd/team-memory-eval-v2/main.go` only — never by package `v2` itself, which
avoids an import cycle (`render` needs `v2`'s `RunRecord`/`TrialResult`
types).

```
internal/eval/v2/render/
  render.go       Report(run, baselineArm, results, w) error — entry point
  viewmodel.go    pure functions: results -> report view model
  template.html   //go:embed'd html/template source
  render_test.go  table-driven tests on the view model + a smoke render
```

`Report` writes to an `io.Writer` (not a path) so it's trivial to test
against a `bytes.Buffer` and so the caller decides the destination.

### Data flow

1. `cmd/team-memory-eval-v2/main.go` calls `runner.Run(...)`, which now
   returns `(RunRecord, []TrialResult, error)` instead of just `error`.
   Before returning, it hydrates legacy result fields from the authoritative
   case manifest. The runner owns trial execution and persistence; artifact
   publication remains at the command boundary.
2. Main passes `render.Report` to `v2.ExportArtifacts` as an `io.Writer`
   callback. The exporter writes `report.html` through a temporary file,
   atomically renames it, and then records it in `artifacts.json`.
3. `render` builds a view model by calling the *existing* `Summarize`,
   `Pairwise`, and `CostTotals` functions in `artifacts.go` — no
   reimplementation of aggregation. It adds only what those don't already
   provide:
   - **Arm list + order**: distinct `Arm` values from `results`, baseline
     first, remaining arms in configured order, followed by any unexpected
     arms found in results.
   - **Arm color**: assigned from the existing eval-v2 categorical palette
     (blue/aqua/yellow/... in that fixed order) by position in the arm
     list — never by name, so it's stable run to run as long as arm order
     in the config is stable, and never collides.
   - **Axis scaling**: a "nice number" round-up (1/2/5 x 10^n) of
     `max(values) * 1.15`, shared per chart.
   - **Field notes** (up to 3 cards): candidate arm = the non-baseline arm
     with the highest mean ΔF1 vs. baseline (ties broken by arm order).
     From cases where baseline and that candidate both completed:
     - biggest win (max ΔF1)
     - biggest loss (min ΔF1, only if negative — omitted if the candidate
       never lost)
     - one case from the category with the largest |mean ΔF1| that isn't
       already used above
     Each card shows every arm's answer for that case, not just baseline
     and the leading candidate.
   - **All case breakdown**: every case grouped by category, with question,
     expected answer, status, token F1, delta from baseline, verbatim answer,
     and error for every arm. Native `details` elements keep this usable
     without JavaScript.
4. `html/template` renders `template.html` against the view model.
   Free-text fields (question, expected, answer, error) go through the
   template's default auto-escaping — no manual escaping code.

### Config change

`internal/eval/v2/config.go`: `"html"` becomes a third valid value in
`output.formats` (`validateOutputFormats`), and `Config.OutputFormats()`'s
zero-value default becomes `[csv, jsonl, html]` so existing configs that
don't set `output.formats` at all start getting the report for free,
matching what was asked for. Configs that explicitly list
`output.formats: [csv, jsonl]` keep opting out.

Output formats are presentation-only and do not affect trial execution, so
`html` is removed before computing the resumable trial config hash. This keeps
`[csv, jsonl]` and `[csv, jsonl, html]` compatible for an existing run ID.

### Docs

`evals/v2/README.md`'s "Output contract" section currently says "Eval v2
deliberately does not generate an HTML dashboard." That line goes; it's
replaced with a short description of `report.html` alongside the existing
CSV/JSONL description, which stays accurate — the raw artifacts remain
available for anyone who wants to build a different view.

### Edge cases

`Report` never errors merely because data is sparse. Zero completed trials for an arm,
a baseline with no completed trials, or a candidate that never loses all
degrade to an empty or zero-value section (empty field notes list, 0-value
tiles) rather than a panic or error, since `Summarize`/`Pairwise`/`CostTotals`
already tolerate empty groups (guarded divisions, `Pairs == 0` rows). The
only real error path is a template-execution or artifact I/O failure. Trial
results remain durably completed in PostgreSQL, but the command returns an
error because a requested artifact was not successfully published.

## Testing

- `viewmodel.go`: table-driven tests over synthetic `[]TrialResult` fixtures
  — arm ordering and color assignment, axis scaling on a few known ranges,
  field-note selection (including the "candidate never loses" and
  "only one candidate arm" edge cases, and a fixture with zero completed
  trials).
- `render.go`: one smoke test that runs `Report` against a small fixture and
  asserts the output parses as HTML and contains a few expected strings
  (arm names, every case ID), escapes untrusted answers, and contains no
  `ZgotmplZ` template sanitizer output — not a pixel-level check.
- Runner tests cover question hydration for resumed legacy results. Artifact
  tests cover atomic HTML publication and the `files.report` manifest entry.
- Manual acceptance: regenerate `report.html` for the
  `groupmembench-finance-v2-gpt5mini` run already on disk and compare
  against the hand-built version from earlier this session.
- `runner_test.go` and `main.go`'s existing call sites need updating for the
  new `Runner.Run` signature.

## Open questions

None — scope was narrowed via the three clarifying questions already
answered (wired into `make eval-v2`; fully generic over arms; field notes
auto-selected).
