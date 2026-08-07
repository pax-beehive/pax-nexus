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

## Knowledge Eval platform runbook

- The API entrypoint is `cmd/knowledge-eval-api`; the dashboard is
  `web/llmwiki-benchmark-dashboard`; the prepared-dataset pipeline is under
  `scripts/prepare_llmwiki_session_datasets.py` and
  `internal/eval/knowledgeeval`.
- Treat build, artifact support, retrieval, reader output, and answer judging as
  separate stages. Diagnose a low accuracy score from the compact run metrics
  and case metadata before inspecting any artifact payload.
- Do not read or print large Wiki trees, source-session payloads, dataset files,
  or raw model traces unless the user explicitly asks. Prefer `jq` aggregation
  over `dataset-run.json`, task state, and bounded samples.
- Never commit `.build`, `.env*`, credentials, prepared/raw datasets, SQLite
  demo data, dashboard `public/acceptance`, or generated run artifacts. Keep
  those paths ignored and verify the staged file list before every commit.
- Paid maintainer tasks use `DEEPSEEK_API_KEY` from `.env.eval-v2`; the generic
  `.env` contains different LLM Wiki variables. Source the correct file without
  printing secret values. Bind the API to `0.0.0.0:58081` for LAN access.
- Before restarting the API, inspect persisted tasks and do not interrupt a
  `queued` or `running` task. Restart only when idle, then verify `/healthz` and
  an experiment preview with `llm_configured=true`.
- Maintainer QA runs use the semantic answer judge. Preserve per-case
  `judge_confidence`, `judge_disputed`, `judge_reason_code`, dataset category,
  and run-level confidence/dispute metrics. Deterministic judging is only the
  no-LLM/test fallback.
- Do not let QA evaluator outcomes gate or mutate artifact construction. Build
  and validation complete before downstream QA evaluation begins.
- When adding an LLM call per question or arm, update experiment and cohort
  `MaxLLMCalls` previews and their tests so paid-call confirmation remains
  accurate.
- Run focused coverage for changed Knowledge Eval packages, full `go test
  ./...`, dashboard `npm test`, and scoped golangci-lint. Report unrelated
  repository-wide lint debt separately rather than modifying it opportunistically.

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

## Web frontend

`web/` is a self-contained React + TypeScript + Vite app (the Human Portal);
it has its own `package.json`, `tsconfig`, and build, and no Go code depends
on it. The API contract is `docs/on-prem-identity-frontend-integration.md`.

- Develop: `cd web && npm install && npm run dev` (proxies `/v1` to
  `http://localhost:58080`, override with `VITE_API_ORIGIN`).
- Test: `cd web && npm test` (vitest).
- Build: `cd web && npm run build` (`tsc --noEmit` + `vite build`).
- Always render buttons with `web/src/components/Button.tsx`
  (`variant`/`size` props map to the `.btn` class system); do not hand-write
  `className="btn …"` on `<button>` elements. Parent-scoped styles
  (`.tabs button`, `.seg button`, `.wiki-*`) stay plain `<button>`.
- 状态徽标一律通过 `components/Badge.tsx` 或 `components/Tag.tsx` 渲染。设计系统是
  两色制：accent 表示需要注意/主行动/危险，neutral 表示常态；不要引入新的色相。
- `web/src/components/` 是共享组件库（`ls web/src/components/` 为准，新增组件更新此处）：
  设计系统原件 `Badge`、`Button`、`Card`、`Tag`、`Seg`、`TimeWindowPicker`、
  `Kicker`、`PageHeader`、`MetricTile`、`Crumbs`、
  `EmptyState`、`DataTable`、`CommandPalette`、`Field`；弹层/仪式类 `Modal`、
  `ConfirmDialog`、`SecretCeremony`、`DeviceEnrollmentCeremony`、
  `CreateDeviceEnrollmentModal`、`IssueAccessModal`、`RevokeDeviceModal`；其余
  `Countdown`、`ErrorBoundary`、`RegionError`、`Toasts`、`TeamSwitcher`、
  `PagedListCard`；`components/wiki/` 下另有 `TopicTree`、`RelationList`、
  `WikiMarkdown`（wiki 专属，不复用到壳内其他页面）。
- 全局样式在 `web/src/styles/`，按层组织：`tokens.css`（设计 token 单一真源）→
  `themes.css` → `base.css` → `components.css` → `layout.css` → `features/*.css`。
  `features/` 下按屏幕/功能一一对应新建文件（`ls web/src/styles/features/` 为准），
  目前包括 `access-tree.css`、`agent-detail.css`、
  `apps-settings.css` / `apps-todos.css` / `apps-wiki.css`、`entry.css`、
  `governance.css` / `governance-explorer.css` / `governance-pipeline.css`、
  `operations.css`、`overview-chart.css`、`session-audit.css`、
  `settings-pages.css`、`teams.css`、`wiki.css` 等。Use the layout utilities
  (`.toolbar`, `.stack`, `.section`, `.flush`, `.row`) instead of inline
  spacing styles; use `.seg` for single-choice preset toggles (not `.tabs`).
- Themes (beige default, dark, arcade) are pure design-token overrides in
  `web/src/styles/themes.css`, applied via `data-theme` on `<html>` and
  persisted by `web/src/lib/theme.ts`. Components must reference CSS
  variables, never hardcoded colors; new colors need a token in
  `styles/base.css` plus an override per theme.
- Wiki markdown renders through `react-markdown` + `remark-gfm` in
  `web/src/components/wiki/WikiMarkdown.tsx`; do not hand-roll markdown
  parsing. Xanadu inline links are re-applied on top via the section-scoped
  `linkedComponents` mapping, never by string-replacing rendered HTML.
- All mutations go through `web/src/api/actions.ts` (Idempotency-Key,
  `resource_version` + `If-Match`, CSRF header); one-time secrets never touch
  durable storage, server-visible URL components, or logs. Invitation tokens
  may transit an immediately erased URL fragment and tab-scoped
  `sessionStorage` solely to survive the OIDC round trip.
