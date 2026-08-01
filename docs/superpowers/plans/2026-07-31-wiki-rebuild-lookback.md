# Wiki Rebuild Lookback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the wiki Reset & rebuild dialog take an optional calendar date so the rebuild replays only sessions with activity on or after that date.

**Architecture:** The date flows browser → `POST /v1/wiki/rebuild` (`since`, RFC3339) → `sessionconsumer.Controller.Rebuild` → `postgres.Repository.RebuildPageWiki`, which — inside the existing rebuild transaction — pre-seeds `session_processor_cursors` to `last_sequence` for every stream that has no `session_events` row with `occurred_at >= since`. Streams with recent activity get no cursor row, so the existing `PendingStreams` scan replays them in full from sequence 0. Zero/absent `since` reproduces today's full rebuild exactly.

**Tech Stack:** Go (hertz HTTP, pgx), thrift-generated API models (`make generate`), React + TypeScript frontend, vitest DOM tests, testify suites.

## Global Constraints

- Session-level granularity: a stream with any event `occurred_at >= since` replays **in full from sequence 0**; a stream with none is skipped entirely (spec: "Semantics").
- Empty/absent `since` = full rebuild, exactly today's behavior.
- Skipping is durable: skipped streams get `committed_sequence = last_sequence`.
- Cutoff comparison is `occurred_at >= since`; `since` arrives as RFC3339 and is treated as an opaque instant. Malformed `since` → HTTP 400.
- No new tables, no migration. The generated hertz models are refreshed only via `idl/team_memory.thrift` + `make generate` (never hand-edit generated files).
- Calendar input is the native `<input type="date">` — no new frontend dependency.
- Lint gate is strict (`make lint` runs golangci-lint with errcheck `check-blank: true` — never `_ =` an error), coverage gate is `make coverage` ≥ 80%.
- Run gates from the repo root: `make lint`, `go test ./internal/...`, and for DB-backed tests `TEAM_MEMORY_TEST_POSTGRES_DSN` must be set (skipped otherwise; `make integration-test` provisions it in CI).

---

### Task 1: Thread `since` through repository, consumer, and handler (zero-value passthrough)

Everything compiles and behaves exactly as before (handler passes zero time); the new cursor pre-seed is implemented and integration-tested at the repository layer.

**Files:**
- Modify: `internal/pagewiki/postgres/repository.go` (`RebuildPageWiki`, ~line 199)
- Modify: `internal/pagewiki/sessionconsumer/consumer.go` (`Rebuilder` interface ~line 73, `Rebuild` ~line 155)
- Modify: `internal/teamnote/transport/httpapi/handler/dependencies.go` (`WikiControl` interface, ~line 131)
- Modify: `internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints.go` (`RebuildWiki`, ~line 80)
- Test (modify): `internal/pagewiki/postgres/repository_test.go`
- Test (modify, mechanical signature ripples): `internal/pagewiki/sessionconsumer/consumer_test.go`, `internal/pagewiki/sessionconsumer/backoff_test.go`, `internal/pagewiki/sessionconsumer/integration_test.go`, `internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints_test.go`

**Interfaces:**
- Consumes: existing rebuild transaction in `RebuildPageWiki`; `session_streams(scope_id, agent_id, session_id, last_sequence)`; `session_events(scope_id, agent_id, session_id, sequence, occurred_at, stream_id, …)`.
- Produces (Task 2 relies on these):
  - `Rebuilder.RebuildPageWiki(ctx context.Context, scopeID, processorName, processorVersion string, since time.Time) error`
  - `Controller.Rebuild(ctx context.Context, scopeID string, since time.Time) (Status, error)`
  - `WikiControl.Rebuild(context.Context, string, time.Time) (sessionconsumer.Status, error)`

- [ ] **Step 1: Write the failing repository integration test**

In `internal/pagewiki/postgres/repository_test.go`, extend `TearDownTest`'s query list with the session-side tables the new test seeds:

```go
	for _, query := range []string{
		"DELETE FROM pagewiki_maintenance_runs WHERE scope_id = $1",
		"DELETE FROM pagewiki_publications WHERE scope_id = $1",
		"DELETE FROM pagewiki_source_revisions WHERE scope_id = $1",
		"DELETE FROM pagewiki_topic_trees WHERE scope_id = $1",
		"DELETE FROM session_processor_cursors WHERE scope_id = $1",
		"DELETE FROM session_events WHERE scope_id = $1",
		"DELETE FROM session_streams WHERE scope_id = $1",
	} {
```

Add the tests (note: `session_events.stream_id` must be set per session — the unique index is `(scope_id, source, stream_id, sequence)` and the column defaults to `''`, so two sessions sharing a sequence would collide):

```go
func (s *repositorySuite) seedStream(agentID, sessionID string, lastSequence int64, occurredAt []time.Time) {
	s.T().Helper()
	_, err := s.store.Pool().Exec(s.ctx, `
INSERT INTO session_streams (scope_id, user_id, agent_id, session_id, last_sequence)
VALUES ($1, 'user-1', $2, $3, $4)`, s.scopeID, agentID, sessionID, lastSequence)
	s.Require().NoError(err)
	for index, at := range occurredAt {
		_, err := s.store.Pool().Exec(s.ctx, `
INSERT INTO session_events
    (scope_id, event_id, user_id, agent_id, session_id, sequence, event_type, content, occurred_at, stream_id)
VALUES ($1, $2, 'user-1', $3, $4, $5, 'message', 'event content', $6, $4)`,
			s.scopeID, fmt.Sprintf("%s-event-%d", sessionID, index+1),
			agentID, sessionID, int64(index+1), at)
		s.Require().NoError(err)
	}
}

func (s *repositorySuite) processorCursors() map[string]int64 {
	s.T().Helper()
	rows, err := s.store.Pool().Query(s.ctx, `
SELECT session_id, committed_sequence FROM session_processor_cursors
WHERE scope_id = $1 AND processor_name = 'page_wiki' AND processor_version = 'v1'`, s.scopeID)
	s.Require().NoError(err)
	defer rows.Close()
	cursors := map[string]int64{}
	for rows.Next() {
		var sessionID string
		var committed int64
		s.Require().NoError(rows.Scan(&sessionID, &committed))
		cursors[sessionID] = committed
	}
	s.Require().NoError(rows.Err())
	return cursors
}

func (s *repositorySuite) TestRebuildWithLookbackSkipsStaleStreamsAndReplaysActiveOnes() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Entirely before the cutoff: must be skipped (cursor = last_sequence).
	s.seedStream("agent-1", "stale-session", 2,
		[]time.Time{cutoff.Add(-48 * time.Hour), cutoff.Add(-24 * time.Hour)})
	// Straddles the cutoff: must replay in full (no cursor row).
	s.seedStream("agent-1", "straddling-session", 2,
		[]time.Time{cutoff.Add(-24 * time.Hour), cutoff.Add(24 * time.Hour)})
	// Entirely after the cutoff: must replay in full (no cursor row).
	s.seedStream("agent-1", "fresh-session", 1, []time.Time{cutoff.Add(48 * time.Hour)})

	s.Require().NoError(repository.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1", cutoff))

	s.Equal(map[string]int64{"stale-session": 2}, s.processorCursors())
}

func (s *repositorySuite) TestRebuildWithZeroSinceSeedsNoCursors() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	s.seedStream("agent-1", "old-session", 1,
		[]time.Time{time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})

	s.Require().NoError(repository.RebuildPageWiki(s.ctx, s.scopeID, "page_wiki", "v1", time.Time{}))

	s.Empty(s.processorCursors())
}
```

(`fmt` and `time` are already imported by this test file.)

- [ ] **Step 2: Update every `RebuildPageWiki` / `Rebuild` signature so the package compiles, then run the new tests to verify they fail**

These are the mechanical ripples; make them all in one pass. Behavior must not change — every existing caller passes the zero value `time.Time{}`.

`internal/pagewiki/postgres/repository.go` — add `"time"` to imports, extend the signature (implementation of the seed comes in Step 3):

```go
func (r *Repository) RebuildPageWiki(
	ctx context.Context,
	scopeID string,
	processorName string,
	processorVersion string,
	since time.Time,
) (returnedErr error) {
```

Existing calls in `repository_test.go` (5 sites, e.g. lines 159, 231, 248, 286): append `, time.Time{}` — except the two new tests from Step 1 which already pass `cutoff` / `time.Time{}`.

`internal/pagewiki/sessionconsumer/consumer.go`:

```go
type Rebuilder interface {
	RebuildPageWiki(context.Context, string, string, string, time.Time) error
}
```

```go
func (c *Controller) Rebuild(ctx context.Context, scopeID string, since time.Time) (Status, error) {
	if strings.TrimSpace(scopeID) == "" {
		return Status{}, fmt.Errorf("rebuild Page Wiki: scope is required")
	}
	c.mu.Lock()
	err := c.rebuilder.RebuildPageWiki(ctx, scopeID, ProcessorName, ProcessorVersion, since)
	...
```

(only the two signature lines change; body otherwise untouched)

`internal/teamnote/transport/httpapi/handler/dependencies.go` (`"time"` import needed):

```go
	Rebuild(context.Context, string, time.Time) (sessionconsumer.Status, error)
```

`internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints.go` line ~80 (temporary until Task 2):

```go
	status, err := h.wikiControl.Rebuild(ctx, onprem.LocalScopeID, time.Time{})
```

Test fakes and call sites:
- `sessionconsumer/backoff_test.go:63`: `func (noopRebuilder) RebuildPageWiki(context.Context, string, string, string, time.Time) error { return nil }`; line 162 `controller.Rebuild(ctx, "local-team", time.Time{})`.
- `sessionconsumer/consumer_test.go`: `recordingRebuilder` (~line 363) gains a `since time.Time` field recorded in `RebuildPageWiki`; call sites 84, 130, 247 append a `since` argument. In `TestRebuildResetsDerivedWikiStateAndSchedulesFreshConsumption`, pass a non-zero cutoff and assert passthrough:

```go
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	status, err := s.consumer.Rebuild(context.Background(), "local-team", cutoff)
	...
	s.Equal(cutoff, s.rebuilder.since)
```

  (other call sites pass `time.Time{}`)
- `sessionconsumer/integration_test.go:131`: `controller.Rebuild(s.ctx, s.scopeID, time.Time{})`.
- `handler/wiki_ingestion_endpoints_test.go` (~line 168): `func (s *wikiControlService) Rebuild(_ context.Context, _ string, since time.Time) (sessionconsumer.Status, error)` — add a `since time.Time` field on `wikiControlService`, record it, keep returning `Status{AutoInject: true}, s.rebuildErr`. Add `"time"` to the file's imports if not present (it is present — `time.RFC3339` isn't used here, check; if absent, add).

Run: `go build ./... && go test ./internal/pagewiki/... ./internal/teamnote/transport/httpapi/handler/`
Expected: build passes; with `TEAM_MEMORY_TEST_POSTGRES_DSN` set, `TestRebuildWithLookbackSkipsStaleStreamsAndReplaysActiveOnes` FAILS (no cursor rows seeded yet — got empty map); everything else passes. Without a DSN the DB suites skip — the implementer MUST run the repository suite against a DSN (`make db-up` provides a local Postgres; see Makefile) for this task's test cycle.

- [ ] **Step 3: Implement the cursor pre-seed**

In `repository.go` `RebuildPageWiki`, between the cursor `DELETE` (~line 242) and the `pagewiki_ingestion_settings` upsert:

```go
	if !since.IsZero() {
		if _, err := tx.Exec(ctx, `
INSERT INTO session_processor_cursors
    (processor_name, processor_version, scope_id, agent_id, session_id, committed_sequence)
SELECT $1, $2, stream.scope_id, stream.agent_id, stream.session_id, stream.last_sequence
FROM session_streams AS stream
WHERE stream.scope_id = $3
  AND stream.agent_id <> ''
  AND NOT EXISTS (
    SELECT 1 FROM session_events AS event
    WHERE event.scope_id = stream.scope_id
      AND event.agent_id = stream.agent_id
      AND event.session_id = stream.session_id
      AND event.occurred_at >= $4
  )`, processorName, processorVersion, scopeID, since); err != nil {
			return fmt.Errorf("rebuild Page Wiki: seed lookback cursors: %w", err)
		}
	}
```

- [ ] **Step 4: Run the gates**

Run: `go test ./internal/pagewiki/... ./internal/teamnote/... && make lint` (repository suite with DSN set)
Expected: PASS, lint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki internal/teamnote
git commit -m "feat(pagewiki): rebuild accepts a lookback cutoff, pre-seeding cursors for stale streams"
```

---

### Task 2: API `since` field — IDL, regeneration, handler parsing

**Files:**
- Modify: `idl/team_memory.thrift:347` (`struct RebuildWikiRequest {}`)
- Generated (via `make generate`, never by hand): `internal/teamnote/transport/httpapi/model/teammemory/api/…`
- Modify: `internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints.go` (`RebuildWiki`)
- Test: `internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints_test.go`

**Interfaces:**
- Consumes: `WikiControl.Rebuild(context.Context, string, time.Time)` from Task 1; generated `api.RebuildWikiRequest` with `Since *string`.
- Produces: `POST /v1/wiki/rebuild` accepting optional JSON body field `since` (RFC3339 string). Task 3's frontend posts exactly this.

- [ ] **Step 1: Write the failing handler tests**

The existing `perform` helper hard-codes body `{}`. Refactor it to take a body (keep existing call sites via a wrapper):

```go
func (s *wikiIngestionHandlerSuite) perform(method, path string, csrf bool) *ut.ResponseRecorder {
	return s.performWithBody(method, path, csrf, `{}`)
}

func (s *wikiIngestionHandlerSuite) performWithBody(
	method, path string,
	csrf bool,
	body string,
) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	headers := []ut.Header{
		{Key: "Content-Type", Value: "application/json"},
		{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
	}
	if csrf {
		headers = append(headers, ut.Header{Key: "X-CSRF-Token", Value: "csrf"})
	}
	payload := &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	return ut.PerformRequest(hertz.Engine, method, path, payload, headers...)
}
```

New tests:

```go
func (s *wikiIngestionHandlerSuite) TestRebuildForwardsParsedSinceCutoff() {
	response := s.performWithBody(http.MethodPost, "/v1/wiki/rebuild", true,
		`{"since":"2026-07-01T00:00:00Z"}`)

	s.Equal(consts.StatusOK, response.Code)
	s.Equal(1, s.wikiControl.rebuilds)
	s.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), s.wikiControl.since)
}

func (s *wikiIngestionHandlerSuite) TestRebuildRejectsMalformedSince() {
	response := s.performWithBody(http.MethodPost, "/v1/wiki/rebuild", true,
		`{"since":"yesterday"}`)

	s.Equal(consts.StatusBadRequest, response.Code)
	s.Equal(0, s.wikiControl.rebuilds)
}

func (s *wikiIngestionHandlerSuite) TestRebuildWithoutSincePassesZeroTime() {
	response := s.perform(http.MethodPost, "/v1/wiki/rebuild", true)

	s.Equal(consts.StatusOK, response.Code)
	s.Equal(1, s.wikiControl.rebuilds)
	s.True(s.wikiControl.since.IsZero())
}
```

(`wikiControlService.since` was added in Task 1. The existing `TestOwnerConfirmsRebuildThroughGeneratedRoute` keeps passing unchanged.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/teamnote/transport/httpapi/handler/ -run TestWikiIngestionHandlerSuite -v`
Expected: `TestRebuildForwardsParsedSinceCutoff` FAILS (since is zero — handler ignores the body); `TestRebuildRejectsMalformedSince` FAILS (got 200).

- [ ] **Step 3: Extend the IDL and regenerate**

`idl/team_memory.thrift` line 347:

```thrift
struct RebuildWikiRequest {
  1: optional string since (api.body="since")
}
```

Run: `make generate`
Expected: only generated files under `internal/teamnote/transport/httpapi/` change (plus any formatting the generator applies). `git diff --stat` to confirm the blast radius is generated code only.

- [ ] **Step 4: Parse `since` in the handler**

Replace the body of `RebuildWiki` between the role check and the response write:

```go
	var request api.RebuildWikiRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	var since time.Time
	if request.Since != nil && strings.TrimSpace(*request.Since) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*request.Since))
		if err != nil {
			writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request",
				"since must be an RFC3339 timestamp")
			return
		}
		since = parsed
	}
	status, err := h.wikiControl.Rebuild(ctx, onprem.LocalScopeID, since)
```

(If the generated field is a value `string` instead of `*string`, adapt the nil check to `strings.TrimSpace(request.Since) != ""` — check the generated model after Step 3.)

- [ ] **Step 5: Run the gates**

Run: `go test ./internal/teamnote/... && make lint`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add idl internal/teamnote
git commit -m "feat(api): optional RFC3339 since cutoff on POST /v1/wiki/rebuild"
```

---

### Task 3: Frontend — calendar date in the rebuild dialog

**Files:**
- Modify: `web/src/components/ConfirmDialog.tsx`
- Modify: `web/src/api/actions.ts` (`rebuildWiki`, ~line 370)
- Modify: `web/src/pages/WikiStatusPage.tsx`
- Test: `web/tests/wiki-status.dom.test.tsx`

**Interfaces:**
- Consumes: `POST /v1/wiki/rebuild` with optional body `{"since": "<RFC3339>"}` (Task 2).
- Produces: `rebuildWiki(idempotencyKey: string, since?: string)`; `ConfirmDialog` optional `children` rendered between consequences and buttons.

- [ ] **Step 1: Write the failing DOM tests**

Read `web/tests/wiki-status.dom.test.tsx` first — reuse its existing fetch-mock, render, and `callsTo` helpers exactly as the rebuild test at line ~130 does. Add to the same describe block (capture request bodies from the fetch mock's recorded calls):

```tsx
  it("sends the lookback cutoff when a rebuild date is picked", async () => {
    const fetchMock = installFetchMock((path, method) => {
      if (path === "/v1/wiki/rebuild" && method === "POST") {
        return { auto_inject: true };
      }
      return defaultRoutes(path, method);
    });
    const user = renderWikiStatusPage(); // follow the file's existing setup helper

    await user.click(screen.getByRole("button", { name: "Reset & rebuild" }));
    const dialog = screen.getByRole("dialog", { name: "Reset and rebuild Wiki" });
    fireEvent.change(within(dialog).getByLabelText("Replay sessions since (optional)"), {
      target: { value: "2026-07-01" },
    });
    expect(
      within(dialog).getByText(/Only sessions with activity on or after 2026-07-01/),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));

    await screen.findByText("Wiki cleared. Rebuilding from Session Lake…");
    const calls = callsTo(fetchMock, "/v1/wiki/rebuild", "POST");
    expect(calls).toHaveLength(1);
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      since: new Date("2026-07-01T00:00:00").toISOString(),
    });
  });

  it("omits since when the rebuild date is left empty", async () => {
    // same setup as the full-rebuild test at line ~130
    ...
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));
    await screen.findByText("Wiki cleared. Rebuilding from Session Lake…");
    const calls = callsTo(fetchMock, "/v1/wiki/rebuild", "POST");
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({});
  });
```

Adapt helper names (`installFetchMock`, `renderWikiStatusPage`, `defaultRoutes`, `callsTo` signature/return shape) to what the file actually defines — the structure above is the requirement, the helpers are the file's. If `callsTo` returns only counts, assert the body via the mock's raw `mock.calls` instead. The expected `since` is computed with the same expression the page uses (`new Date("2026-07-01T00:00:00").toISOString()`) so the test is timezone-independent.

Run: `cd web && npx vitest run tests/wiki-status.dom.test.tsx`
Expected: both new tests FAIL (no date input exists; body lacks `since`).

- [ ] **Step 2: Add the `children` slot to `ConfirmDialog`**

```tsx
import type { ReactNode } from "react";
import { Modal } from "./Modal";

/**
 * Destructive-action confirmation. Cascade consequences are spelled out in
 * the dialog body; terminal actions get the danger-styled confirm button.
 * Optional children render between the consequences and the action row.
 */
export function ConfirmDialog({
  title,
  consequences,
  confirmLabel,
  busy,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  consequences: string[];
  confirmLabel: string;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
  children?: ReactNode;
}) {
  return (
    <Modal title={title} onClose={onClose}>
      <div className="note bad">
        <ul style={{ margin: "2px 0 2px 18px", padding: 0 }}>
          {consequences.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ul>
      </div>
      {children}
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button className="btn danger" onClick={onConfirm} disabled={busy}>
          {busy ? "Processing…" : confirmLabel}
        </button>
      </div>
    </Modal>
  );
}
```

- [ ] **Step 3: Send `since` from the API layer**

`web/src/api/actions.ts` (the file already defines `JSON_HEADERS`):

```ts
export function rebuildWiki(
  idempotencyKey: string,
  since?: string,
): Promise<WikiIngestionStatus> {
  return humanFetch<WikiIngestionStatus>("/v1/wiki/rebuild", {
    method: "POST",
    headers: { ...JSON_HEADERS, "Idempotency-Key": idempotencyKey },
    body: JSON.stringify(since ? { since } : {}),
  });
}
```

- [ ] **Step 4: Wire the date input into `WikiStatusPage`**

State (next to `rebuildOpen`):

```tsx
  const [rebuildOpen, setRebuildOpen] = useState(false);
  // Calendar cutoff for Reset & rebuild (YYYY-MM-DD); empty = full history.
  const [rebuildSince, setRebuildSince] = useState("");
```

Close helper + confirm:

```tsx
  const closeRebuild = () => {
    setRebuildOpen(false);
    setRebuildSince("");
  };

  const confirmRebuild = async () => {
    setBusy(true);
    setMessage("");
    try {
      const since = rebuildSince
        ? new Date(`${rebuildSince}T00:00:00`).toISOString()
        : undefined;
      const updated = await rebuildWiki(beginAction(), since);
      setAutoInject(updated.auto_inject);
      closeRebuild();
      setMessage("Wiki cleared. Rebuilding from Session Lake…");
    } catch (error) {
      handleError(error);
    } finally {
      setBusy(false);
    }
  };
```

Dialog (replace the existing `{rebuildOpen && (…)}` block):

```tsx
      {rebuildOpen && (
        <ConfirmDialog
          title="Reset and rebuild Wiki"
          consequences={[
            "All PageWiki pages, revisions, links, citations, and maintenance runs will be deleted.",
            rebuildSince
              ? `Only sessions with activity on or after ${rebuildSince} will be replayed; older sessions will be skipped until a wider rebuild.`
              : "PageWiki ingestion cursors will reset and every Session Lake stream will be processed again.",
            "Session Lake events and Team Notes are preserved.",
            "An LLM-backed rebuild may make paid provider calls.",
          ]}
          confirmLabel="Confirm reset & rebuild"
          busy={busy}
          onConfirm={() => void confirmRebuild()}
          onClose={closeRebuild}
        >
          <label className="wiki-rebuild-since">
            <span>Replay sessions since (optional)</span>
            <input
              type="date"
              value={rebuildSince}
              onChange={(event) => setRebuildSince(event.target.value)}
              disabled={busy}
            />
            <span className="muted small">
              Leave empty to replay the full Session Lake history.
            </span>
          </label>
        </ConfirmDialog>
      )}
```

If the page's stylesheet has no suitable stacked-label pattern, add a minimal rule where the page's other wiki styles live (follow the existing CSS file for the wiki page — e.g. a `display: flex; flex-direction: column; gap: 4px; margin: 8px 0;` block for `.wiki-rebuild-since`). Match however `WikiStatusPage` styles are organized; do not add inline style objects if a stylesheet exists.

- [ ] **Step 5: Run the tests**

Run: `cd web && npx vitest run tests/wiki-status.dom.test.tsx && npx tsc --noEmit`
(use the project's typecheck script if one exists in `web/package.json` — check `scripts`)
Expected: all tests PASS, no type errors.

- [ ] **Step 6: Commit**

```bash
git add web
git commit -m "feat(web): calendar lookback picker in the wiki rebuild dialog"
```

---

## Final verification

- `make lint && make coverage` green at repo root (coverage floor 80%).
- Repository suite ran against a real Postgres (`TEAM_MEMORY_TEST_POSTGRES_DSN` set) at least once during Task 1.
- `cd web && npx vitest run` green.
