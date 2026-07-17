# Engineering rules

These rules apply to all handwritten Go code in this repository.

## Interfaces and generation

- Hertz is the HTTP framework.
- Thrift files under `idl/` are the source of truth for HTTP interfaces.
- Run `make generate-init` only when bootstrapping Hertz generation. After that,
  run `make generate` for every IDL change.
- Generated model and router code must not contain handwritten domain logic.
- Run `make mocks` after changing a mocked interface.

## Tests

- New and changed handwritten packages must keep aggregate unit-test coverage
  at or above 75 percent. `make coverage` enforces the threshold.
- Use `github.com/stretchr/testify/suite` for package/module test fixtures.
- Use table-driven subtests for input matrices, validation cases, lifecycle
  transitions, and error cases.
- Test observable behavior through module interfaces. Avoid tests coupled to
  private implementation details.
- PostgreSQL adapter tests run against PostgreSQL, not SQLite substitutes.

## Team Note evaluation and recall

- Treat ingest, extraction, recall, and answer judging as separate control
  loops. Do not attribute a missing answer to recall unless the required fact
  is present in the extraction observation.
- Optimize recall first against a fixed replay fixture containing persisted
  Team Notes, relations, queries, and gold atoms. Run the paid end-to-end cohort
  only after the deterministic replay improves.
- Recall evaluation must distinguish candidate retrieval, relation expansion,
  final selection, and token-budget packing. Record why an available candidate
  was rejected instead of reporting only the final delivered context.
- Report candidate recall at the configured lane limit, conditional recall over
  available atoms, context precision, budget drops, superseded-fact leakage,
  and end-to-end judge accuracy. Token F1 is diagnostic, not answer quality.
- Do not change a global semantic threshold or candidate limit from one case.
  Validate retrieval changes on the fixed cohort and compare stage metrics as
  well as judge accuracy. Keep extraction output fixed when calibrating recall.
- Prefer structural composition over progressively wider retrieval. One-hop
  Team Note relations are traversable in both directions, but related notes
  must still pass query relevance, authorization, and the shared token budget.
- Keep `RecallNotes` as the external module interface. Ranking, relation
  expansion, set selection, and budget decisions belong behind the shared
  `PlanRecall` seam so adapters and tests exercise the same policy.
- Compaction and continuity summaries are extraction concerns. Evaluate their
  retention and latency separately from recall ranking and do not use them to
  explain recall regressions without stage evidence.

## Errors and complexity

- Code comments must use English only and must not contain emoji.
- Handle every returned error. Do not discard errors with `_` unless an
  unavoidable best-effort cleanup is accompanied by a comment and test.
- Wrap errors with operation context using `%w`; callers use `errors.Is` or
  `errors.As` rather than matching strings.
- Cyclomatic complexity must not exceed 20.
- Cognitive complexity must not exceed 25.
- Split orchestration from decisions before adding linter suppressions.
- `//nolint` requires the exact linter name and a concrete justification.
- Run `make lint test` before handing off code.
