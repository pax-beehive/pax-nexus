# Wiki Standalone Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the wiki out of the portal shell onto a full-screen `/wiki/browse` route, and turn the in-shell `/wiki` entry into an observability page (ingestion controls + extraction progress) backed by a new progress field on the ingestion status API.

**Architecture:** Same SPA, new full-screen route registered before the PortalShell catch-all. Backend extends `sessionconsumer.Status` with an optional `Progress` (pending sessions + last processed time) surfaced through the existing `GET /v1/wiki/ingestion` endpoint via new optional IDL fields. Spec: `docs/superpowers/specs/2026-07-29-wiki-standalone-page-design.md`.

**Tech Stack:** Go (hertz, thrift-IDL codegen via `make generate`, pgx, testify suites), React 18 + react-router 6 + vitest DOM tests (`web/tests/*.dom.test.tsx`, no jest-dom — use truthy/null assertions).

## Global Constraints

- Branch: `feat/wiki-standalone-page` (already created; spec committed).
- `internal/teamnote/transport/httpapi/model/**` and `router/**` are GENERATED (hz/thrift). Never hand-edit; change `idl/team_memory.thrift` and run `make generate`.
- New API fields must be `optional` in the IDL — mutation responses (`PUT /v1/wiki/ingestion`, `POST /v1/wiki/rebuild`) keep returning only `auto_inject`.
- No visual redesign: reuse existing CSS classes/tokens (`var(--muted)` etc.); only add the minimal `.wiki-browse` layout rules.
- Pre-existing gate failures on main (3 lint findings + 2 DB tests) are NOT yours to fix; ensure no NEW failures.
- Frontend tests: follow `web/tests/helpers.tsx` patterns (`setupDomTest()`, `renderApp`, `jsonResponse`); there is no jest-dom, so assert with `toBeTruthy()` / `toBeNull()`.
- Commit after every task with a conventional message ending in the Claude co-author trailer.

---

### Task 1: sessionconsumer progress model

**Files:**
- Modify: `internal/pagewiki/sessionconsumer/consumer.go`
- Test: `internal/pagewiki/sessionconsumer/consumer_test.go`

**Interfaces:**
- Consumes: existing `Store` interface, `Controller.Status(ctx, scopeID)`.
- Produces: `type Progress struct { PendingSessions int; LastProcessedAt *time.Time }`; `Status` gains field `Progress *Progress`; `Store` interface gains `Progress(context.Context, string) (Progress, error)`. Task 2 implements the store method; Task 3 reads `Status.Progress` in the handler.

- [ ] **Step 1: Write the failing tests**

Add to `consumer_test.go` (package `sessionconsumer_test`; the suite already has `s.store *consumerStore` and `s.consumer *sessionconsumer.Controller` built in `SetupTest`):

```go
func (s *consumerSuite) TestStatusIncludesProgress() {
	s.store.enabled["local-team"] = true
	processed := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	s.store.progress = sessionconsumer.Progress{PendingSessions: 3, LastProcessedAt: &processed}

	status, err := s.consumer.Status(context.Background(), "local-team")

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Require().NotNil(status.Progress)
	s.Equal(3, status.Progress.PendingSessions)
	s.Equal(processed, *status.Progress.LastProcessedAt)
}

func (s *consumerSuite) TestStatusDegradesWhenProgressQueryFails() {
	s.store.enabled["local-team"] = true
	s.store.progressErr = errors.New("progress query failed")

	status, err := s.consumer.Status(context.Background(), "local-team")

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Nil(status.Progress)
}
```

Extend the `consumerStore` fake in the same file — add fields `progress sessionconsumer.Progress` and `progressErr error` to the struct, plus:

```go
func (s *consumerStore) Progress(context.Context, string) (sessionconsumer.Progress, error) {
	if s.progressErr != nil {
		return sessionconsumer.Progress{}, s.progressErr
	}
	return s.progress, nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pagewiki/sessionconsumer/ -run TestConsumerSuite -count=1`
Expected: compile error — `undefined: sessionconsumer.Progress` / `Status` has no field `Progress`.

- [ ] **Step 3: Implement in `consumer.go`**

Extend the types (near the existing `Status` declaration):

```go
type Progress struct {
	PendingSessions int
	LastProcessedAt *time.Time
}

type Status struct {
	AutoInject bool
	// Progress is nil when the progress query failed; ingestion status
	// stays available so the toggle keeps working (spec section 4).
	Progress *Progress
}
```

Add to the `Store` interface (after `SetAutoInjectEnabled`):

```go
	Progress(context.Context, string) (Progress, error)
```

Replace `Controller.Status`:

```go
func (c *Controller) Status(ctx context.Context, scopeID string) (Status, error) {
	enabled, err := c.store.AutoInjectEnabled(ctx, scopeID)
	if err != nil {
		return Status{}, fmt.Errorf("read Page Wiki ingestion status: %w", err)
	}
	status := Status{AutoInject: enabled}
	progress, err := c.store.Progress(ctx, scopeID)
	if err != nil {
		c.logger.Warn("read Page Wiki ingestion progress", "error", err)
		return status, nil
	}
	status.Progress = &progress
	return status, nil
}
```

`SetAutoInject` and `Rebuild` keep returning `Status{AutoInject: ...}` with nil `Progress` — do not touch them.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pagewiki/sessionconsumer/ -run TestConsumerSuite -count=1`
Expected: PASS (the whole suite, not just the new tests).
Also run: `go build ./...` — Expected: FAIL with `*PageWikiConsumerStore does not implement sessionconsumer.Store (missing method Progress)`. That is Task 2's job; to keep this task green, add the postgres stub now as part of this task instead: implement the real method in Task 2, but here add nothing — instead run only the package test above and `go vet ./internal/pagewiki/sessionconsumer/`. Do NOT commit a broken build: fold Step 5 of this task together with Task 2's commit if `go build ./...` fails. Preferred: proceed straight to Task 2 and commit both together.

- [ ] **Step 5: Commit (only if `go build ./...` passes; otherwise commit at end of Task 2)**

```bash
git add internal/pagewiki/sessionconsumer/consumer.go internal/pagewiki/sessionconsumer/consumer_test.go
git commit -m "feat: add ingestion progress to session consumer status"
```

---

### Task 2: postgres progress query

**Files:**
- Modify: `internal/platform/postgres/pagewiki_consumer.go`
- Test: `internal/pagewiki/sessionconsumer/integration_test.go`

**Interfaces:**
- Consumes: `sessionconsumer.Progress` from Task 1; existing `session_streams` / `session_processor_cursors` tables (no migration — `updated_at` already exists).
- Produces: `func (s *PageWikiConsumerStore) Progress(ctx context.Context, scopeID string) (sessionconsumer.Progress, error)`.

- [ ] **Step 1: Write the failing test**

Add to `integration_test.go` (suite `postgresConsumerSuite`; `s.seedSession()` inserts one stream `runtime-agent`/`runtime-session` with `last_sequence 1` and one event; `TearDownTest` already cleans both tables):

```go
func (s *postgresConsumerSuite) TestProgressCountsBacklogAndLastProcessed() {
	s.seedSession()
	consumerStore, err := platformpostgres.NewPageWikiConsumerStore(s.store.Pool(), s.scopeID)
	s.Require().NoError(err)

	// Backlog is visible even though auto inject was never enabled.
	progress, err := consumerStore.Progress(s.ctx, s.scopeID)
	s.Require().NoError(err)
	s.Equal(1, progress.PendingSessions)
	s.Nil(progress.LastProcessedAt)

	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	controller, err := sessionconsumer.New(
		consumerStore,
		pagewiki.NewService(
			repository,
			pagewiki.SessionDocumentPlanner{},
			pagewiki.SessionDocumentEditor{},
		),
		repository,
		slog.New(slog.DiscardHandler),
		time.Hour,
	)
	s.Require().NoError(err)
	_, err = controller.InjectSession(s.ctx, s.scopeID, "runtime-session")
	s.Require().NoError(err)

	progress, err = consumerStore.Progress(s.ctx, s.scopeID)
	s.Require().NoError(err)
	s.Zero(progress.PendingSessions)
	s.Require().NotNil(progress.LastProcessedAt)
	s.WithinDuration(time.Now(), *progress.LastProcessedAt, time.Minute)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/sessionconsumer/ -run TestPostgresConsumerSuite -count=1`
Expected: compile error (`Progress` undefined on `*PageWikiConsumerStore`). If `TEAM_MEMORY_TEST_POSTGRES_DSN` is unset the suite skips at runtime, but the compile failure fires regardless.

- [ ] **Step 3: Implement `Progress` in `pagewiki_consumer.go`**

Place after `PendingStreams`. Same stream filters as `PendingStreams`, but deliberately WITHOUT the `pagewiki_ingestion_settings` join — the backlog must stay visible while auto inject is off:

```go
// Progress reports the ingestion backlog for the status page. Unlike
// PendingStreams it does not gate on auto_inject: the backlog is shown
// even while automatic injection is off.
func (s *PageWikiConsumerStore) Progress(
	ctx context.Context,
	scopeID string,
) (sessionconsumer.Progress, error) {
	var progress sessionconsumer.Progress
	err := s.pool.QueryRow(ctx, `
SELECT
  (SELECT COUNT(*)
   FROM session_streams AS stream
   LEFT JOIN session_processor_cursors AS cursor
     ON cursor.processor_name = $1
    AND cursor.processor_version = $2
    AND cursor.scope_id = stream.scope_id
    AND cursor.agent_id = stream.agent_id
    AND cursor.session_id = stream.session_id
   WHERE stream.last_sequence > COALESCE(cursor.committed_sequence, 0)
     AND stream.scope_id = $3
     AND stream.source = 'agent-session'
     AND stream.agent_id <> ''),
  (SELECT MAX(updated_at)
   FROM session_processor_cursors
   WHERE processor_name = $1 AND processor_version = $2 AND scope_id = $3)`,
		sessionconsumer.ProcessorName, sessionconsumer.ProcessorVersion, scopeID,
	).Scan(&progress.PendingSessions, &progress.LastProcessedAt)
	if err != nil {
		return sessionconsumer.Progress{}, fmt.Errorf("read Page Wiki ingestion progress: %w", err)
	}
	return progress, nil
}
```

(pgx scans `MAX(updated_at)`'s NULL into the `*time.Time` field as nil.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go build ./... && go test ./internal/pagewiki/sessionconsumer/ -count=1`
Expected: build OK; unit suite PASS. The postgres suite PASSES if `TEAM_MEMORY_TEST_POSTGRES_DSN` is exported, otherwise SKIPs — if it skipped, say so in your report; do not claim the DB test ran.

- [ ] **Step 5: Commit (include Task 1 files if they were held back)**

```bash
git add internal/pagewiki/sessionconsumer/ internal/platform/postgres/pagewiki_consumer.go
git commit -m "feat: expose Page Wiki ingestion backlog progress from the consumer store"
```

---

### Task 3: ingestion status API carries progress

**Files:**
- Modify: `idl/team_memory.thrift` (struct `WikiIngestionStatusResponse`, ~line 333)
- Regenerate: `make generate` (touches `internal/teamnote/transport/httpapi/model/...` and `router/...` — never hand-edit)
- Modify: `internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints.go`
- Test: `internal/teamnote/transport/httpapi/handler/wiki_ingestion_endpoints_test.go`

**Interfaces:**
- Consumes: `sessionconsumer.Status{AutoInject, Progress}` from Task 1.
- Produces: `GET /v1/wiki/ingestion` → `{"auto_inject":bool, "pending_sessions"?:int, "last_processed_at"?:RFC3339}`; generated model fields `PendingSessions *int32`, `LastProcessedAt *string`. Task 5's frontend consumes this JSON shape.

- [ ] **Step 1: Write the failing tests**

In `wiki_ingestion_endpoints_test.go`, give the fake a configurable status — extend `wikiControlService` with a `status sessionconsumer.Status` field and change its `Status` method to:

```go
func (s *wikiControlService) Status(context.Context, string) (sessionconsumer.Status, error) {
	return s.status, nil
}
```

Add tests (the suite's `s.perform(method, path, csrf)` helper drives the real generated router):

```go
func (s *wikiIngestionHandlerSuite) TestStatusIncludesProgressWhenAvailable() {
	processed := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	s.wikiControl.status = sessionconsumer.Status{
		AutoInject: true,
		Progress:   &sessionconsumer.Progress{PendingSessions: 3, LastProcessedAt: &processed},
	}

	response := s.perform(http.MethodGet, "/v1/wiki/ingestion", false)

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(
		`{"auto_inject":true,"pending_sessions":3,"last_processed_at":"2026-07-29T08:00:00Z"}`,
		response.Body.String(),
	)
}

func (s *wikiIngestionHandlerSuite) TestStatusOmitsProgressWhenUnavailable() {
	s.wikiControl.status = sessionconsumer.Status{AutoInject: true}

	response := s.perform(http.MethodGet, "/v1/wiki/ingestion", false)

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"auto_inject":true}`, response.Body.String())
}
```

Add `"time"` to the test imports. If an existing status test asserts the old exact JSON body, update it to set `s.wikiControl.status` explicitly rather than deleting the assertion.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/teamnote/transport/httpapi/handler/ -run TestWikiIngestionHandlerSuite -count=1`
Expected: FAIL — the new JSON fields are absent (`pending_sessions` missing from response body).

- [ ] **Step 3: Extend the IDL and regenerate**

In `idl/team_memory.thrift`:

```thrift
struct WikiIngestionStatusResponse {
  1: required bool auto_inject
  2: optional i32 pending_sessions
  3: optional string last_processed_at
}
```

Run: `make generate`
Expected: `WikiIngestionStatusResponse` in the generated model gains `PendingSessions *int32` and `LastProcessedAt *string` (JSON `omitempty`). Inspect `git diff --stat` — only generated model/router files plus the IDL should change. Verify the optional fields serialize with `omitempty` (grep the generated struct tags); if hz emitted them as required-style tags, stop and re-check the IDL annotation rather than editing generated code.

- [ ] **Step 4: Map progress in the handler**

In `wiki_ingestion_endpoints.go`, replace the `GetWikiIngestionStatus` response write:

```go
	response := &api.WikiIngestionStatusResponse{AutoInject: status.AutoInject}
	if status.Progress != nil {
		pending := int32(status.Progress.PendingSessions)
		response.PendingSessions = &pending
		if status.Progress.LastProcessedAt != nil {
			formatted := status.Progress.LastProcessedAt.UTC().Format(time.RFC3339)
			response.LastProcessedAt = &formatted
		}
	}
	c.JSON(consts.StatusOK, response)
```

Add `"time"` to the handler imports. `UpdateWikiIngestion` and `RebuildWiki` stay untouched (their `Status.Progress` is always nil).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go build ./... && go test ./internal/teamnote/transport/httpapi/handler/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add idl/team_memory.thrift internal/teamnote/transport/httpapi/
git commit -m "feat: report ingestion progress from GET /v1/wiki/ingestion"
```

---

### Task 4: full-screen /wiki/browse route

**Files:**
- Create: `web/src/pages/WikiBrowsePage.tsx` (derived from `WikiPage.tsx` — leave `WikiPage.tsx` in place until Task 5 so the `/wiki` route keeps compiling)
- Modify: `web/src/App.tsx`, `web/src/styles.css`
- Test: `web/tests/wiki-browse.dom.test.tsx`

**Interfaces:**
- Consumes: everything `WikiPage.tsx` already imports except `HumanMe`, `beginAction`/`injectWikiSession`/`rebuildWiki`/`setWikiAutoInject`, and `ConfirmDialog`.
- Produces: `export function WikiBrowsePage(): JSX.Element` (no props) at route `/wiki/browse`; URL state stays `?page=<slug>&revision=<id>`. Task 5 links here.

- [ ] **Step 1: Write the failing DOM test**

Create `web/tests/wiki-browse.dom.test.tsx`:

```tsx
// The /wiki/browse route renders the wiki full-screen, outside the portal
// shell (spec 2026-07-29-wiki-standalone-page section 1-2).

import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

const REVISION = {
  id: "rev-1",
  page_id: "p1",
  title: "Alpha",
  summary: "Alpha summary",
  sections: [{ key: "s1", heading: "Overview", markdown: "Alpha body." }],
  markdown: "Alpha body.",
  citations: [],
  links: [],
};

export function wikiFetch(path: string): Response {
  if (path === "/v1/wiki/navigation") {
    return jsonResponse({ roots: [], pages: [{ id: "p1", slug: "alpha", title: "Alpha", rank: 1 }] });
  }
  if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
  if (path === "/v1/wiki/pages/alpha") {
    return jsonResponse({
      id: "p1", slug: "alpha", title: "Alpha", current_revision_id: "rev-1", revision: REVISION,
    });
  }
  if (path === "/v1/wiki/pages/alpha/revisions") return jsonResponse({ revisions: [REVISION] });
  if (path === "/v1/wiki/pages/alpha/backlinks") return jsonResponse({ outgoing: [], incoming: [] });
  throw new Error(`unexpected path: ${path}`);
}

describe("wiki browse route", () => {
  it("renders the wiki full-screen without the portal shell", async () => {
    await renderApp({ route: "/wiki/browse", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(screen.getByText("Alpha summary")).toBeTruthy());
    // No portal navigation: the shell's nav links must not render.
    expect(screen.queryByText("My Agents")).toBeNull();
    // Back link to the portal status page is present.
    expect(screen.getByRole("link", { name: /back to portal/i })).toBeTruthy();
    // Selecting the first page rewrote the URL under /wiki/browse.
    expect(window.location.pathname).toBe("/wiki/browse");
    expect(window.location.search).toBe("?page=alpha");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run tests/wiki-browse.dom.test.tsx`
Expected: FAIL — `/wiki/browse` falls through to the PortalShell catch-all (no "Alpha summary"; "My Agents" renders).

- [ ] **Step 3: Create `WikiBrowsePage.tsx`**

Copy `web/src/pages/WikiPage.tsx` to `web/src/pages/WikiBrowsePage.tsx`, then apply exactly these changes:

1. Rename the component and drop the prop: `export function WikiBrowsePage() {` (delete `{ me }: { me: HumanMe }` and the `import type { HumanMe }` line).
2. Imports: add `Link` — `import { Link, useNavigate } from "react-router-dom";`; delete the `beginAction, injectWikiSession, rebuildWiki, setWikiAutoInject` import line and the `ConfirmDialog` import.
3. Delete ingestion-control state and handlers, keeping the auto-inject read that gates the navigation refresh poll: keep `autoInject`, the `getWikiIngestionStatus` effect, and the `usePolling` block; delete the state hooks `ingestionLoading`, `ingestionBusy`, `sessionID`, `ingestionMessage`, `rebuildOpen` and the functions `toggleAutoInject`, `injectFixedSession`, `confirmRebuild`. In the ingestion-status effect, drop the `.finally(...)` clause that referenced `setIngestionLoading`.
4. `updateLocation`: change the pathname to the new route —
   ```tsx
   navigate({ pathname: "/wiki/browse", search: `?${parameters.toString()}` }, { replace: true });
   ```
5. Root element: `<div className="wiki wiki-browse">`.
6. Replace the `<header className="wiki-header">`'s left `<div>` content with:
   ```tsx
   <div>
     <Link className="wiki-back" to="/wiki">← Back to portal</Link>
     <h1>Wiki</h1>
     <p className="muted">Durable pages, revision history, and evidence in one place.</p>
   </div>
   ```
   (The `wiki-eyebrow` span is replaced by the back link; the search `<form>` stays as is.)
7. Delete the whole `<section className="card wiki-ingestion" ...>...</section>` block and the trailing `{rebuildOpen && <ConfirmDialog .../>}` block.

In `web/src/App.tsx`, import the page and register the route before the shell catch-all:

```tsx
import { WikiBrowsePage } from "./pages/WikiBrowsePage";
```

```tsx
      {state.kind === "active" && (
        <>
          <Route
            path="/wiki/browse"
            element={
              <ErrorBoundary region="route" fullPage>
                <WikiBrowsePage />
              </ErrorBoundary>
            }
          />
          <Route path="*" element={<PortalShell me={state.me} />} />
        </>
      )}
```

In `web/src/styles.css`, append after the existing wiki rules:

```css
/* Full-viewport wiki browse route (outside the portal shell). */
.wiki-browse { min-height: 100vh; padding: 20px clamp(16px, 3vw, 44px) 32px; }
.wiki-browse .wiki-layout { grid-template-columns: clamp(240px, 20vw, 380px) minmax(0, 1fr); }
.wiki-back { display: inline-block; margin-bottom: 6px; color: var(--muted); font-size: 12.5px; text-decoration: none; }
.wiki-back:hover { color: var(--text); text-decoration: underline; }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run tests/wiki-browse.dom.test.tsx && npm run build`
Expected: test PASS; `tsc --noEmit` clean (unused-import errors here mean step 3's deletions were incomplete).

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/WikiBrowsePage.tsx web/src/App.tsx web/src/styles.css web/tests/wiki-browse.dom.test.tsx
git commit -m "feat: full-screen /wiki/browse route outside the portal shell"
```

---

### Task 5: /wiki status page replaces the in-shell wiki

**Files:**
- Create: `web/src/pages/WikiStatusPage.tsx`
- Delete: `web/src/pages/WikiPage.tsx`
- Modify: `web/src/api/wiki.ts` (type only), `web/src/pages/PortalShell.tsx`
- Test: `web/tests/wiki-status.dom.test.tsx`

**Interfaces:**
- Consumes: Task 3's JSON (`pending_sessions?`, `last_processed_at?`); Task 4's `/wiki/browse` route; existing `beginAction`/`injectWikiSession`/`rebuildWiki`/`setWikiAutoInject`, `usePolling`, `formatTime` (from `web/src/lib/format.ts`), `ConfirmDialog`.
- Produces: `export function WikiStatusPage({ me }: { me: HumanMe })` at `/wiki`; legacy `/wiki?page=<slug>` redirects to `/wiki/browse` preserving the whole query string.

- [ ] **Step 1: Extend the frontend status type**

In `web/src/api/wiki.ts`:

```ts
export interface WikiIngestionStatus {
  auto_inject: boolean;
  pending_sessions?: number;
  last_processed_at?: string;
}
```

- [ ] **Step 2: Write the failing DOM tests**

Create `web/tests/wiki-status.dom.test.tsx`:

```tsx
// The in-shell /wiki route is an observability page: ingestion controls,
// extraction progress, an Open Wiki entry, and a legacy deep-link redirect
// (spec 2026-07-29-wiki-standalone-page sections 1-3).

import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
import { wikiFetch } from "./wiki-browse.dom.test";

setupDomTest();

describe("wiki status page", () => {
  it("shows ingestion controls, progress, and opens the full-screen wiki", async () => {
    const { user } = await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") {
          return jsonResponse({
            auto_inject: true,
            pending_sessions: 3,
            last_processed_at: "2026-07-29T08:00:00Z",
          });
        }
        return wikiFetch(path);
      },
    });

    await waitFor(() => expect(screen.getByRole("switch")).toBeTruthy());
    expect(screen.getByText("3")).toBeTruthy();
    // Portal shell stays visible around the status page.
    expect(screen.getByText("My Agents")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Open Wiki" }));
    await waitFor(() => expect(window.location.pathname).toBe("/wiki/browse"));
  });

  it("degrades to a progress-unavailable notice without blocking controls", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        return wikiFetch(path);
      },
    });

    await waitFor(() => expect(screen.getByText("Progress is unavailable.")).toBeTruthy());
    expect(screen.getByRole("switch")).toBeTruthy();
  });

  it("redirects legacy /wiki?page= deep links to the browse route", async () => {
    await renderApp({ route: "/wiki?page=alpha", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(window.location.pathname).toBe("/wiki/browse"));
    expect(window.location.search).toBe("?page=alpha");
    await waitFor(() => expect(screen.getByText("Alpha summary")).toBeTruthy());
  });
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd web && npx vitest run tests/wiki-status.dom.test.tsx`
Expected: FAIL — `/wiki` still renders the old full wiki (no "Open Wiki" button, no redirect).

- [ ] **Step 4: Create `WikiStatusPage.tsx`**

```tsx
import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { getWikiIngestionStatus, type WikiIngestionStatus } from "../api/wiki";
import { beginAction, injectWikiSession, rebuildWiki, setWikiAutoInject } from "../api/actions";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { formatTime } from "../lib/format";
import { isAbortError, usePolling } from "../lib/usePolling";
import { useErrorHandler } from "../lib/useErrorHandler";

export function WikiStatusPage({ me }: { me: HumanMe }) {
  const navigate = useNavigate();
  const location = useLocation();
  const handleError = useErrorHandler();
  const [status, setStatus] = useState<WikiIngestionStatus>();
  const [statusError, setStatusError] = useState(false);
  const [autoInject, setAutoInject] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [sessionID, setSessionID] = useState(
    () => new URLSearchParams(window.location.search).get("session") ?? "",
  );
  const [message, setMessage] = useState("");
  const [rebuildOpen, setRebuildOpen] = useState(false);

  // Legacy deep links: /wiki?page=<slug> used to render the wiki inline
  // here. Forward the whole query string so revision links keep working.
  const legacyPage = new URLSearchParams(location.search).get("page");
  useEffect(() => {
    if (legacyPage) {
      navigate({ pathname: "/wiki/browse", search: location.search }, { replace: true });
    }
  }, [legacyPage, location.search, navigate]);

  usePolling(
    async (signal) => {
      try {
        const loaded = await getWikiIngestionStatus(signal);
        setStatus(loaded);
        setAutoInject(loaded.auto_inject);
        setStatusError(false);
      } catch (error) {
        if (isAbortError(error)) return;
        setStatusError(true);
      } finally {
        setLoading(false);
      }
    },
    5000,
    [],
  );

  const toggleAutoInject = async () => {
    setBusy(true);
    setMessage("");
    try {
      const updated = await setWikiAutoInject(!autoInject);
      setAutoInject(updated.auto_inject);
      setMessage(
        updated.auto_inject
          ? "Auto inject is on. New Session Lake evidence will be organized into the wiki."
          : "Auto inject is off.",
      );
    } catch (error) {
      handleError(error);
    } finally {
      setBusy(false);
    }
  };

  const injectFixedSession = async () => {
    const fixedSessionID = sessionID.trim();
    if (!fixedSessionID) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await injectWikiSession(fixedSessionID, beginAction());
      setMessage(
        `Injected ${result.processed_streams} stream${result.processed_streams === 1 ? "" : "s"} from ${fixedSessionID}.`,
      );
    } catch (error) {
      handleError(error);
    } finally {
      setBusy(false);
    }
  };

  const confirmRebuild = async () => {
    setBusy(true);
    setMessage("");
    try {
      const updated = await rebuildWiki(beginAction());
      setAutoInject(updated.auto_inject);
      setRebuildOpen(false);
      setMessage("Wiki cleared. Rebuilding from Session Lake…");
    } catch (error) {
      handleError(error);
    } finally {
      setBusy(false);
    }
  };

  const progressAvailable = !statusError && status?.pending_sessions !== undefined;

  return (
    <div className="wiki">
      <header className="wiki-header">
        <div>
          <span className="wiki-eyebrow">Grounded team knowledge</span>
          <h1>Wiki</h1>
          <p className="muted">Ingestion status and extraction progress for the team wiki.</p>
        </div>
        <button className="btn primary" type="button" onClick={() => navigate("/wiki/browse")}>
          Open Wiki
        </button>
      </header>

      <section className="card wiki-progress" aria-label="Extraction progress">
        <div className="wiki-ingestion-copy">
          <span className="wiki-eyebrow">Extraction</span>
          <strong>Progress</strong>
        </div>
        {progressAvailable ? (
          <div className="wiki-progress-stats">
            <div>
              <span className="muted small">Pending sessions</span>
              <strong className="wiki-progress-figure">{status?.pending_sessions}</strong>
            </div>
            <div>
              <span className="muted small">Last processed</span>
              <strong className="wiki-progress-figure">
                {status?.last_processed_at ? formatTime(status.last_processed_at) : "Never"}
              </strong>
            </div>
          </div>
        ) : (
          <p className="muted small">Progress is unavailable.</p>
        )}
      </section>

      <section className="card wiki-ingestion" aria-label="Wiki ingestion controls">
        <div className="wiki-ingestion-copy">
          <span className="wiki-eyebrow">Session Lake</span>
          <strong>Automatic Wiki injection</strong>
          <span className="muted small">
            Uses an independent PageWiki cursor; Team Note extraction is unaffected.
          </span>
        </div>
        <button
          className={autoInject ? "wiki-switch active" : "wiki-switch"}
          type="button"
          role="switch"
          aria-checked={autoInject}
          disabled={loading || busy}
          onClick={() => void toggleAutoInject()}
        >
          <span aria-hidden="true" />
          {autoInject ? "On" : "Off"}
        </button>
        <div className="wiki-fixed-session">
          <label htmlFor="wiki-session-id">Fixed session ID</label>
          <input
            id="wiki-session-id"
            value={sessionID}
            placeholder="e.g. 019fa46f-…"
            onChange={(event) => setSessionID(event.target.value)}
          />
          <button
            className="btn primary"
            type="button"
            disabled={busy || sessionID.trim() === ""}
            onClick={() => void injectFixedSession()}
          >
            {busy ? "Injecting…" : "Inject session"}
          </button>
        </div>
        {me.role === "owner" && (
          <div className="wiki-reset">
            <div>
              <strong>Start over with current Session Lake evidence</strong>
              <span className="muted small">
                Clears PageWiki-derived data and rebuilds it with the currently configured organizer.
              </span>
            </div>
            <button
              className="btn danger"
              type="button"
              disabled={busy}
              onClick={() => setRebuildOpen(true)}
            >
              Reset & rebuild
            </button>
          </div>
        )}
        {message && <p className="wiki-ingestion-message">{message}</p>}
      </section>

      {rebuildOpen && (
        <ConfirmDialog
          title="Reset and rebuild Wiki"
          consequences={[
            "All PageWiki pages, revisions, links, citations, and maintenance runs will be deleted.",
            "PageWiki ingestion cursors will reset and every Session Lake stream will be processed again.",
            "Session Lake events and Team Notes are preserved.",
            "An LLM-backed rebuild may make paid provider calls.",
          ]}
          confirmLabel="Confirm reset & rebuild"
          busy={busy}
          onConfirm={() => void confirmRebuild()}
          onClose={() => setRebuildOpen(false)}
        />
      )}
    </div>
  );
}
```

Append to `web/src/styles.css`:

```css
.wiki-progress { display: grid; grid-template-columns: minmax(240px, 1fr) auto; align-items: center; gap: 18px; padding: 14px 16px; margin-bottom: 18px; }
.wiki-progress-stats { display: flex; gap: 32px; }
.wiki-progress-stats > div { display: flex; flex-direction: column; gap: 2px; }
.wiki-progress-figure { font-size: 18px; }
```

In `web/src/pages/PortalShell.tsx`:
1. Replace the `WikiPage` import with `import { WikiStatusPage } from "./WikiStatusPage";`
2. Route: `<Route path="/wiki" element={<WikiStatusPage me={me} />} />`
3. The `<main>` element: replace `className={location.pathname === "/wiki" ? "main main-wide" : "main"}` with `className="main"`. If `location` becomes unused after this, remove only what the compiler flags — it is also used elsewhere (ErrorBoundary key), so likely nothing else changes.

Delete `web/src/pages/WikiPage.tsx`. If `.main.main-wide` is now unreferenced in the codebase (`grep -rn "main-wide" web/src`), delete that CSS rule too.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd web && npx vitest run tests/wiki-status.dom.test.tsx tests/wiki-browse.dom.test.tsx && npm run build`
Expected: all PASS; build clean (a leftover `WikiPage` reference fails `tsc`).

- [ ] **Step 6: Run the full frontend suite**

Run: `cd web && npm test`
Expected: PASS — existing suites (login-flow, a11y, etc.) must not regress; a11y tests that enumerate interactive controls may need the new switch/button roles accounted for only if they fail — investigate before touching them.

- [ ] **Step 7: Commit**

```bash
git add web/src web/tests
git rm web/src/pages/WikiPage.tsx 2>/dev/null || true
git commit -m "feat: replace in-shell wiki with observability status page"
```

---

### Task 6: repo-wide verification

**Files:** none (verification only; fix regressions you introduced).

- [ ] **Step 1: Backend gates**

Run: `go build ./... && make test-unit`
Expected: build OK. `make test-unit` may show the 2 pre-existing DB test failures noted in Global Constraints — compare failures against `git stash`-free main baseline knowledge: only failures ALREADY failing on main are acceptable; anything new is yours.

- [ ] **Step 2: Lint**

Run: `make lint` (golangci-lint via tools dir)
Expected: only the 3 pre-existing findings from main; no new findings in changed files.

- [ ] **Step 3: Frontend gates**

Run: `cd web && npm run build && npm test`
Expected: PASS.

- [ ] **Step 4: Commit any straggler fixes**

```bash
git status --short
```
Expected: clean tree (or commit fixes with `fix:` messages before finishing). Report the final `git log --oneline main..HEAD` summary.
