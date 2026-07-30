# Todo App MVP (pax-nexus application platform) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the first preset application — an LLM todo list with a suggestion pipeline fed by team memory (read) and semantic user-behavior events written back to the Evidence Lake (report).

**Architecture:** New bounded context `internal/todoapp` owning its own tables (`todoapp_*`), consuming the nexus through two consumer-defined ports: a `NoteDirectory` (read.list over `team_notes` blocker/handoff notes) and an `EvidenceSink` (report via `evidencelake.Lake.ObserveStream` with new source `app:todo`). HTTP via a new thrift service + hz codegen mirroring the pagewiki transport pattern; frontend page in the Human Portal.

**Tech Stack:** Go 1.x (existing module `github.com/pax-beehive/pax-nexus`), Hertz + thrift/hz codegen, pgx/Postgres, `platform/llm.ChatClient`, React 18 + Vite + Vitest.

**Spec:** `docs/superpowers/specs/2026-07-29-pax-nexus-app-platform-design.md`

## Global Constraints

- Base branch: `main` (`git checkout -b feat/todoapp-mvp origin/main`). Known baseline: main's gate is already red (3 lint findings + 2 DB test failures pre-exist) — record `make test` output in Task 1 and only compare deltas afterwards.
- AGENTS.md rules apply: testify `suite` for fixtures; table-driven subtests; ≥75% coverage on new packages (`make coverage`); wrap errors with `%w`; comments in English, no emoji; cyclomatic ≤20; run `make lint test` before handoff.
- Thrift under `idl/` is the source of truth for HTTP; run `make generate` after IDL changes; no handwritten logic in generated files.
- Frontend mutations go through `web/src/api/actions.ts`.
- Postgres adapter tests run against real Postgres, gated on `TEAM_MEMORY_TEST_POSTGRES_DSN` (skip when unset). Local DSN: `postgres://team_memory:team_memory@localhost:55432/team_memory?sslmode=disable`.
- Migrations replay on every boot inside one transaction — every statement must be idempotent (`IF NOT EXISTS`).
- Scope is always the constant `onprem.LocalScopeID` (`"local-team"`), fixed at construction time like pagewiki.
- Spec deviation (accepted): spec says "schema `app_todo`"; the repo convention is prefix-named tables in `public` (no `CREATE SCHEMA` anywhere). Use `todoapp_*` tables — ownership, not physical schema, is the boundary.
- Report events carry provenance: they are *human actions observed by the app* (accept/complete/dismiss). The app's generated content (suggestion copy) is never reported.
- Deferred beyond MVP (spec §5 marks these secondary): `read.search` context enrichment for suggestion copy; auto-close prompt when a blocker note flips to resolved; per-user assignment.

## File Structure

```
idl/todo_app.thrift                                   new thrift service (7 routes)
internal/session/evidence.go                          + SourceAppTodo registration
internal/todoapp/contracts.go                         domain types
internal/todoapp/ports.go                             Repository/NoteDirectory/Rewriter/Reporter ports
internal/todoapp/service.go                           Service (CRUD + suggestion pipeline + reporting)
internal/todoapp/report.go                            LakeReporter (EvidenceSink adapter)
internal/todoapp/llm_rewriter.go                      LLM copywriter with verbatim degrade
internal/todoapp/scheduler.go                         StartSuggestionRefresh ticker
internal/todoapp/memory/repository.go                 in-memory Repository twin
internal/todoapp/postgres/repository.go               Postgres Repository adapter
internal/todoapp/transport/httpapi/{dependencies,endpoints,mapping}.go + generated bridges
internal/todoapp/CONTEXT.md                           context doc
internal/platform/postgres/todoapp_notes.go           TodoNoteDirectory (read.list adapter)
internal/platform/postgres/migrations/023_todoapp.sql tables
internal/platform/postgres/store.go                   embed + Migrate slice (2 edits)
internal/architecture/dependencies_test.go            + todoapp rules, platform rule edit
CONTEXT-MAP.md                                        + todoapp entries
Makefile                                              + TODOAPP_IDL + third hz update block
main.go                                               wiring + scheduler + env
web/src/api/todo.ts                                   typed reads
web/src/api/actions.ts                                todo mutations
web/src/pages/TodoPage.tsx                            page
web/src/pages/PortalShell.tsx                         route + nav
web/tests/todo.dom.test.tsx                           dom test
```

---

### Task 1: Branch + baseline

**Files:** none (process task)

- [ ] **Step 1: Create branch from main**

```bash
cd /Users/toddzheng/Workspace/golang/team-memory
git fetch origin main && git checkout -b feat/todoapp-mvp origin/main
```

- [ ] **Step 2: Record the red baseline**

```bash
make test 2>&1 | tail -40 > /tmp/todoapp-baseline.txt; make lint 2>&1 | tail -20 >> /tmp/todoapp-baseline.txt
```

Expected: some pre-existing failures (3 lint + 2 DB tests per prior sessions). Save the exact list; later tasks must not add to it.

---

### Task 2: Register the `app:todo` evidence source

**Files:**
- Modify: `internal/session/evidence.go` (consts ~line 18-45)
- Test: `internal/session/evidence_test.go` (or the existing validate test file — find with `grep -rn "ValidateStreamBatch" internal/session/*_test.go`)

**Interfaces:**
- Produces: `session.SourceAppTodo = "app:todo"` — used by Task 7 (reporter) and Task 12 (wiring).

- [ ] **Step 1: Write the failing test** (append to the existing stream-batch validation test file, following its existing table style)

```go
func TestValidateStreamBatchAcceptsAppTodoSource(t *testing.T) {
	batch := session.StreamBatch{Events: []session.StreamEvent{{
		ID:         "app-todo-evt-1",
		Stream:     session.Stream{Source: session.SourceAppTodo, StreamID: "app-todo"},
		Author:     session.Author{Kind: "user", NativeID: "user-1", UserID: "user-1"},
		Kind:       session.KindText,
		Type:       "message",
		Content:    "User completed todo fix-provider-credential.",
		Visibility: session.VisibilityTeam,
		OccurredAt: time.Now().UTC(),
	}}}
	if err := session.ValidateStreamBatch(batch); err != nil {
		t.Fatalf("expected app:todo batch to validate, got %v", err)
	}
}
```

Before writing, confirm exact `StreamEvent`/`Author` field names at `internal/session/evidence.go:47-95` and copy the construction style from an existing passing test in that package.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/session/ -run TestValidateStreamBatchAcceptsAppTodoSource -v`
Expected: FAIL — `undefined: session.SourceAppTodo`

- [ ] **Step 3: Implement**

In `internal/session/evidence.go` add the const next to `SourceAgentSession`/`SourceIMChannel`:

```go
SourceAppTodo = "app:todo"
```

and add `SourceAppTodo: {}` to the `registeredSources` map.

- [ ] **Step 4: Run package tests**

Run: `go test ./internal/session/ -v`
Expected: all PASS (including the new test).

- [ ] **Step 5: Commit** — `git commit -m "feat(session): register app:todo evidence source"`

---

### Task 3: todoapp domain contracts + in-memory repository

**Files:**
- Create: `internal/todoapp/contracts.go`, `internal/todoapp/ports.go`, `internal/todoapp/memory/repository.go`
- Test: `internal/todoapp/memory/repository_test.go`

**Interfaces (Produces — later tasks depend on these exact names):**

```go
// contracts.go
package todoapp

type TodoStatus string
const (
	TodoOpen TodoStatus = "open"
	TodoDone TodoStatus = "done"
)

type TodoSource string
const (
	TodoSourceManual     TodoSource = "manual"
	TodoSourceSuggestion TodoSource = "suggestion"
)

type Todo struct {
	ID           string
	Title        string
	Body         string
	Status       TodoStatus
	Source       TodoSource
	SuggestionID string
	NoteID       string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SuggestionStatus string
const (
	SuggestionPending   SuggestionStatus = "pending"
	SuggestionAccepted  SuggestionStatus = "accepted"
	SuggestionDismissed SuggestionStatus = "dismissed"
)

type Suggestion struct {
	ID          string
	Fingerprint string // MVP: the source note_id; a dismissed note stays dismissed
	NoteID      string
	Kind        string // "blocker" | "handoff"
	Title       string
	Body        string
	Status      SuggestionStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ActionItem struct {
	NoteID    string
	Kind      string
	Subject   string
	Body      string
	UpdatedAt time.Time
}

type ReportEventType string
const (
	EventSuggestionAccepted  ReportEventType = "suggestion_accepted"
	EventSuggestionDismissed ReportEventType = "suggestion_dismissed"
	EventTodoCompleted       ReportEventType = "todo_completed"
)

type ReportEvent struct {
	Type         ReportEventType
	UserID       string
	TodoID       string
	SuggestionID string
	NoteID       string
	Summary      string // human-readable sentence, English
	OccurredAt   time.Time
}
```

```go
// ports.go
package todoapp

var ErrNotFound = errors.New("todoapp: not found")

type Repository interface {
	SaveTodo(ctx context.Context, todo Todo) error
	TodoByID(ctx context.Context, todoID string) (Todo, error)
	ListTodos(ctx context.Context, status TodoStatus) ([]Todo, error) // status "" = all, newest UpdatedAt first
	SaveSuggestion(ctx context.Context, suggestion Suggestion) error
	SuggestionByID(ctx context.Context, suggestionID string) (Suggestion, error)
	ListSuggestions(ctx context.Context, status SuggestionStatus) ([]Suggestion, error) // status "" = all, newest first
	SuggestionFingerprints(ctx context.Context) (map[string]struct{}, error)            // every fingerprint ever stored, any status
}

type NoteDirectory interface {
	ListOpenActionItems(ctx context.Context, limit int) ([]ActionItem, error)
}

type Rewriter interface {
	Rewrite(ctx context.Context, item ActionItem) (title string, body string, err error)
}

type Reporter interface {
	Report(ctx context.Context, event ReportEvent) error
}
```

- [ ] **Step 1: Write failing repository tests** (`internal/todoapp/memory/repository_test.go`, package `memory_test`, testify suite per AGENTS.md)

Cover: save+get todo roundtrip; `TodoByID` unknown id returns `todoapp.ErrNotFound` (check with `errors.Is`); `ListTodos("")` returns all, `ListTodos(todoapp.TodoOpen)` filters; save overwrites by ID (upsert); suggestion roundtrip + `ListSuggestions(todoapp.SuggestionPending)` filter; `SuggestionFingerprints` includes dismissed ones. Use table-driven subtests for the filter matrix.

```go
package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/todoapp"
	"github.com/pax-beehive/pax-nexus/internal/todoapp/memory"
	"github.com/stretchr/testify/suite"
)

type RepositorySuite struct {
	suite.Suite
	repo *memory.Repository
	ctx  context.Context
}

func TestRepositorySuite(t *testing.T) { suite.Run(t, new(RepositorySuite)) }

func (s *RepositorySuite) SetupTest() {
	s.repo = memory.NewRepository()
	s.ctx = context.Background()
}

func (s *RepositorySuite) TestTodoRoundtripAndNotFound() {
	todo := todoapp.Todo{ID: "t1", Title: "Fix provider credential", Status: todoapp.TodoOpen,
		Source: todoapp.TodoSourceManual, CreatedBy: "user-1",
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC()}
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todo))
	loaded, err := s.repo.TodoByID(s.ctx, "t1")
	s.Require().NoError(err)
	s.Require().Equal(todo, loaded)
	_, err = s.repo.TodoByID(s.ctx, "missing")
	s.Require().True(errors.Is(err, todoapp.ErrNotFound))
}

func (s *RepositorySuite) TestListTodosFiltersByStatus() {
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{ID: "t1", Title: "a", Status: todoapp.TodoOpen, UpdatedAt: time.Unix(200, 0)}))
	s.Require().NoError(s.repo.SaveTodo(s.ctx, todoapp.Todo{ID: "t2", Title: "b", Status: todoapp.TodoDone, UpdatedAt: time.Unix(300, 0)}))
	cases := []struct {
		name   string
		status todoapp.TodoStatus
		want   []string
	}{
		{name: "all newest first", status: "", want: []string{"t2", "t1"}},
		{name: "open only", status: todoapp.TodoOpen, want: []string{"t1"}},
		{name: "done only", status: todoapp.TodoDone, want: []string{"t2"}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			listed, err := s.repo.ListTodos(s.ctx, tc.status)
			s.Require().NoError(err)
			ids := make([]string, 0, len(listed))
			for _, item := range listed {
				ids = append(ids, item.ID)
			}
			s.Require().Equal(tc.want, ids)
		})
	}
}

func (s *RepositorySuite) TestSuggestionFingerprintsIncludeAllStatuses() {
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, todoapp.Suggestion{ID: "s1", Fingerprint: "n1", Status: todoapp.SuggestionPending}))
	s.Require().NoError(s.repo.SaveSuggestion(s.ctx, todoapp.Suggestion{ID: "s2", Fingerprint: "n2", Status: todoapp.SuggestionDismissed}))
	prints, err := s.repo.SuggestionFingerprints(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(prints, 2)
	_, ok := prints["n2"]
	s.Require().True(ok)
}
```

(Add the analogous suggestion roundtrip/filter tests in the same style.)

- [ ] **Step 2: Run to verify failure** — `go test ./internal/todoapp/... -v` → FAIL (packages don't exist).

- [ ] **Step 3: Implement** `contracts.go` + `ports.go` exactly as in the Interfaces block, then `memory/repository.go`:

```go
package memory

// Repository is the in-memory twin of the Postgres adapter, for domain tests.
type Repository struct {
	mu          sync.Mutex
	todos       map[string]todoapp.Todo
	suggestions map[string]todoapp.Suggestion
}

func NewRepository() *Repository {
	return &Repository{todos: map[string]todoapp.Todo{}, suggestions: map[string]todoapp.Suggestion{}}
}
```

All list methods sort by `UpdatedAt` descending (tie-break by ID descending for determinism). Return copies, not map references. Interface assertion at file end: `var _ todoapp.Repository = (*Repository)(nil)`.

- [ ] **Step 4: Run** — `go test ./internal/todoapp/... -v` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(todoapp): domain contracts, ports, in-memory repository"`

---

### Task 4: Service — todo CRUD, complete, reporting

**Files:**
- Create: `internal/todoapp/service.go`
- Test: `internal/todoapp/service_test.go` (package `todoapp_test`)

**Interfaces (Produces):**

```go
type ServiceConfig struct {
	Repository Repository
	Notes      NoteDirectory
	Rewriter   Rewriter        // optional; nil means verbatim copy
	Reporter   Reporter        // required
	Logger     *slog.Logger    // optional; defaults to observability.DiscardLogger()
	Clock      func() time.Time // optional; defaults to func() { return time.Now().UTC() }
	NewID      func() string    // optional; defaults to crypto/rand 16-byte hex
}

func NewService(config ServiceConfig) (*Service, error) // errors if Repository, Notes, or Reporter nil

func (s *Service) CreateTodo(ctx context.Context, userID, title, body string) (Todo, error)
func (s *Service) CompleteTodo(ctx context.Context, userID, todoID string) (Todo, error)
func (s *Service) ListTodos(ctx context.Context, status TodoStatus) ([]Todo, error)
```

Semantics:
- `CreateTodo`: reject blank `title` or `userID` with a validation error (`fmt.Errorf("create todo: %w", ErrInvalidInput)`, add `ErrInvalidInput = errors.New("todoapp: invalid input")` to ports.go). Status `TodoOpen`, source `TodoSourceManual`. No report event (creation of manual todos is not one of the three MVP events).
- `CompleteTodo`: unknown id → `ErrNotFound`; already-done → return unchanged, no event (idempotent); open → set `TodoDone`, save, then `Reporter.Report` with `EventTodoCompleted`, `Summary` = `fmt.Sprintf("User completed todo %q.", todo.Title)`, `NoteID` carried from the todo. **Report failure must not fail the call**: log `Warn "todo report failed"` and return success.

- [ ] **Step 1: Write failing tests** with a `fakeReporter` recording events and an errorable variant:

```go
type fakeReporter struct {
	events []todoapp.ReportEvent
	err    error
}

func (f *fakeReporter) Report(_ context.Context, event todoapp.ReportEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

type fakeNotes struct{ items []todoapp.ActionItem }

func (f *fakeNotes) ListOpenActionItems(context.Context, int) ([]todoapp.ActionItem, error) {
	return f.items, nil
}
```

Tests (suite, deterministic `Clock: func() time.Time { return time.Unix(1000, 0).UTC() }`, `NewID` counter):
1. `TestCreateTodoValidatesInput` — table: blank title / blank user → error; valid → persisted with open/manual.
2. `TestCompleteTodoEmitsReportEvent` — create, complete, assert repo state done AND one event `{Type: EventTodoCompleted, UserID: "user-1", TodoID: <id>}` with non-empty Summary.
3. `TestCompleteTodoIsIdempotent` — complete twice, one event only.
4. `TestCompleteTodoSurvivesReportFailure` — reporter err set, complete succeeds, todo done.
5. `TestCompleteTodoUnknownIDReturnsNotFound` — `errors.Is(err, todoapp.ErrNotFound)`.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/todoapp/ -v` → FAIL (`NewService` undefined).

- [ ] **Step 3: Implement `service.go`** per semantics above. Default `NewID`:

```go
func defaultNewID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
```

- [ ] **Step 4: Run** — `go test ./internal/todoapp/ -v` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(todoapp): service todo lifecycle with completion reporting"`

---

### Task 5: Service — suggestion pipeline (refresh / accept / dismiss)

**Files:**
- Modify: `internal/todoapp/service.go`
- Test: `internal/todoapp/service_test.go`

**Interfaces (Produces):**

```go
func (s *Service) RefreshSuggestions(ctx context.Context) (int, error)           // returns count of newly created pending suggestions
func (s *Service) PendingSuggestions(ctx context.Context) ([]Suggestion, error)
func (s *Service) AcceptSuggestion(ctx context.Context, userID, suggestionID string) (Todo, error)
func (s *Service) DismissSuggestion(ctx context.Context, userID, suggestionID string) error
```

Semantics:
- `RefreshSuggestions` (serialize with a `sync.Mutex` on Service): `Notes.ListOpenActionItems(ctx, 50)` → load `SuggestionFingerprints` → for each item whose `NoteID` is NOT already a fingerprint: title/body from `Rewriter.Rewrite(ctx, item)` when Rewriter non-nil, else verbatim (`title = item.Subject`, `body = item.Body`); save `Suggestion{ID: NewID(), Fingerprint: item.NoteID, NoteID: item.NoteID, Kind: item.Kind, Status: SuggestionPending, ...}`. A Rewriter error skips that item with a Warn log (does not abort the batch). Returns created count.
- `AcceptSuggestion`: must be `SuggestionPending` (otherwise `fmt.Errorf("accept suggestion %s: %w", id, ErrInvalidTransition)`, add `ErrInvalidTransition = errors.New("todoapp: invalid transition")`). Creates `Todo{Source: TodoSourceSuggestion, SuggestionID: suggestion.ID, NoteID: suggestion.NoteID, Title/Body from suggestion, CreatedBy: userID}`, marks suggestion accepted, reports `EventSuggestionAccepted` (`Summary: fmt.Sprintf("User accepted suggested todo %q from team memory.", title)`). Report failure: warn + succeed.
- `DismissSuggestion`: pending → dismissed, reports `EventSuggestionDismissed` (`Summary: fmt.Sprintf("User dismissed suggested todo %q as not useful.", title)`). Same transition/report rules.

- [ ] **Step 1: Write failing tests**

```go
type scriptedRewriter struct{ prefix string }

func (r scriptedRewriter) Rewrite(_ context.Context, item todoapp.ActionItem) (string, string, error) {
	return r.prefix + item.Subject, "rewritten: " + item.Body, nil
}
```

1. `TestRefreshCreatesPendingSuggestionsWithCitation` — two action items → 2 created, each pending with `NoteID`/`Kind` carried and rewritten title.
2. `TestRefreshDeduplicatesByFingerprint` — refresh twice with same items → second returns 0.
3. `TestRefreshSkipsDismissedForever` — refresh, dismiss one, refresh again → 0 created (fingerprint includes dismissed).
4. `TestRefreshWithoutRewriterCopiesVerbatim` — Rewriter nil → title == Subject.
5. `TestAcceptSuggestionCreatesTodoAndReports` — accept → todo open/`TodoSourceSuggestion` with `SuggestionID`+`NoteID`, suggestion accepted, event `{Type: EventSuggestionAccepted, SuggestionID, NoteID}` recorded.
6. `TestAcceptRejectsNonPending` — accept twice → second `errors.Is(err, todoapp.ErrInvalidTransition)`.
7. `TestDismissReports` — dismiss → status dismissed + `EventSuggestionDismissed`.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/todoapp/ -run TestRefresh -v` → FAIL.
- [ ] **Step 3: Implement** in `service.go`.
- [ ] **Step 4: Run full package** — `go test ./internal/todoapp/... -v` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(todoapp): suggestion pipeline with fingerprint dedup and reporting"`

---

### Task 6: LLM rewriter with verbatim degrade

**Files:**
- Create: `internal/todoapp/llm_rewriter.go`
- Test: `internal/todoapp/llm_rewriter_test.go`

**Interfaces:**
- Consumes: `llm.ChatClient` from `internal/platform/llm/chat.go` (`Complete(context.Context, ChatRequest) (ChatResponse, error)`; messages `[]ChatMessage{Role, Content}`).
- Produces: `NewLLMRewriter(LLMRewriterConfig) (*LLMRewriter, error)` with `LLMRewriterConfig{Client llm.ChatClient; Model string; Logger *slog.Logger}` — the exact shape of `pagewiki.LLMPlannerConfig` (`internal/pagewiki/llm_session_planner.go:23-51`).

Behavior (copy the **planner** failure policy — graceful degrade, never error):
- 2 attempts (`const rewriterAttempts = 2`). Request: system prompt (package-level `const todoRewriterPrompt`, backtick string) + user message = JSON `{"kind":..., "subject":..., "body":...}`.
- System prompt requirements: "You rewrite one team-memory action item into a short actionable todo. Respond with a single JSON object {\"title\": string, \"body\": string} and nothing else — no Markdown fence. Title imperative, max 80 chars. Body: 1-3 sentences, keep concrete identifiers (commands, file names) verbatim."
- Parse with a private DTO `struct{ Title string `json:"title"`; Body string `json:"body"` }` after `trimJSONFence` (copy the 11-line helper from `internal/pagewiki/llm_session_editor.go:206-216` into this file — it is unexported there).
- Reject blank title as a failed attempt. After attempts exhausted: `logger.Warn("todo rewrite degraded", "note_id", item.NoteID, "error", lastErr)` and return `item.Subject, item.Body, nil`.
- Interface assertion: `var _ Rewriter = (*LLMRewriter)(nil)`.

- [ ] **Step 1: Write failing tests** with a scripted fake client:

```go
type fakeChatClient struct {
	responses []string
	errs      []error
	calls     int
}

func (f *fakeChatClient) Complete(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	index := f.calls
	f.calls++
	if index < len(f.errs) && f.errs[index] != nil {
		return llm.ChatResponse{}, f.errs[index]
	}
	return llm.ChatResponse{Message: llm.ChatMessage{Role: "assistant", Content: f.responses[index]}}, nil
}
```

Table-driven: valid JSON → parsed title/body; fenced ```json block → parsed; first attempt garbage, second valid → parsed with 2 calls; both attempts garbage → degraded verbatim, no error; client error twice → degraded verbatim. Plus `NewLLMRewriter` validation: nil client / blank model → error.

- [ ] **Step 2: Run to verify failure** — FAIL (`NewLLMRewriter` undefined).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(todoapp): LLM suggestion rewriter with verbatim degrade"`

---

### Task 7: LakeReporter — the report adapter

**Files:**
- Create: `internal/todoapp/report.go`
- Test: `internal/todoapp/report_test.go`

**Interfaces:**
- Consumes: `session.StreamBatch`/`StreamEvent`/`WithScope` from `internal/session` (field names verified in Task 2), `session.SourceAppTodo`.
- Produces:

```go
type EvidenceSink interface {
	ObserveStream(ctx context.Context, batch session.StreamBatch) (session.IngestReceipt, error)
}

func NewLakeReporter(sink EvidenceSink, scopeID string) (*LakeReporter, error) // nil sink / blank scope → error
```

`*evidencelake.Lake` satisfies `EvidenceSink` structurally — todoapp does NOT import evidencelake (keeps the dependency rule minimal); main.go passes the Lake in.

Report mapping (one event per call):

```go
func (r *LakeReporter) Report(ctx context.Context, event ReportEvent) error {
	scoped := session.WithScope(ctx, r.scopeID)
	streamEvent := session.StreamEvent{
		ID:         "app-todo-" + r.newID(),
		Stream:     session.Stream{Source: session.SourceAppTodo, StreamID: "app-todo"},
		Author:     session.Author{Kind: "user", NativeID: event.UserID, UserID: event.UserID},
		Kind:       session.KindText,
		Type:       "message",
		Content:    event.Summary,
		Visibility: session.VisibilityTeam,
		OccurredAt: event.OccurredAt,
		Metadata: map[string]string{
			"event_type":    string(event.Type),
			"todo_id":       event.TodoID,
			"suggestion_id": event.SuggestionID,
			"note_id":       event.NoteID,
		},
	}
	if _, err := r.sink.ObserveStream(scoped, session.StreamBatch{Events: []session.StreamEvent{streamEvent}}); err != nil {
		return fmt.Errorf("report todo event %s: %w", event.Type, err)
	}
	return nil
}
```

(`newID` = same crypto/rand hex default as the service, injectable for tests. Adjust `Metadata` field name/type to the actual `session.StreamEvent` definition; drop empty-string metadata keys before sending.)

- [ ] **Step 1: Write failing test** with a fake sink capturing the batch; assert: scope retrievable via `session.ScopeFromContext(ctx)` inside the fake equals `"local-team"`; source is `session.SourceAppTodo`; content = Summary; metadata has event_type; `Sequence` is zero (ingest assigns it). Error path: sink error is wrapped (`errors.Is` on a sentinel).
- [ ] **Step 2: Verify failure.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — `go test ./internal/todoapp/... -v` → PASS. Note: this adds `session` to todoapp's imports; the architecture test will fail until Task 10 registers the rule — run only `./internal/todoapp/...` here.
- [ ] **Step 5: Commit** — `git commit -m "feat(todoapp): evidence lake reporter for semantic user events"`

---

### Task 8: Migration 023 + Postgres repository adapter

**Files:**
- Create: `internal/platform/postgres/migrations/023_todoapp.sql`, `internal/todoapp/postgres/repository.go`
- Modify: `internal/platform/postgres/store.go` (embed directive line ~14 AND ordered slice inside `Migrate`, after `"migrations/022_pagewiki_topic_trees.sql"`)
- Test: `internal/todoapp/postgres/repository_test.go`

**Migration (idempotent, replayed every boot):**

```sql
CREATE TABLE IF NOT EXISTS todoapp_todos (
    scope_id TEXT NOT NULL,
    todo_id TEXT NOT NULL,
    status TEXT NOT NULL,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, todo_id)
);

CREATE INDEX IF NOT EXISTS todoapp_todos_status_idx
    ON todoapp_todos (scope_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS todoapp_suggestions (
    scope_id TEXT NOT NULL,
    suggestion_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    status TEXT NOT NULL,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, suggestion_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS todoapp_suggestions_fingerprint_idx
    ON todoapp_suggestions (scope_id, fingerprint);
```

**Adapter:** `func NewRepository(ctx context.Context, pool *pgxpool.Pool, scopeID string) (*Repository, error)` — mirror `internal/pagewiki/postgres/repository.go` construction/validation style. Whole domain struct marshalled into `payload`; `status`/`fingerprint` mirrored into columns for filtering. Upserts:

```sql
INSERT INTO todoapp_todos (scope_id, todo_id, status, payload, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (scope_id, todo_id)
DO UPDATE SET status = EXCLUDED.status, payload = EXCLUDED.payload, updated_at = EXCLUDED.updated_at
```

`ListTodos`: `WHERE scope_id = $1 AND ($2 = '' OR status = $2) ORDER BY updated_at DESC, todo_id DESC` (same predicate idiom as `internal/platform/postgres/explorer.go:33-53`). `SuggestionFingerprints`: `SELECT fingerprint FROM todoapp_suggestions WHERE scope_id = $1`. Unknown-id reads map `pgx.ErrNoRows` → `todoapp.ErrNotFound`. Interface assertion `var _ todoapp.Repository = (*Repository)(nil)`.

- [ ] **Step 1: Write failing DB tests** — copy the suite skeleton from `internal/pagewiki/postgres/repository_test.go:1-47` verbatim (env skip on `TEAM_MEMORY_TEST_POSTGRES_DSN`, `store.Migrate` in `SetupSuite`, unique scope per test: `fmt.Sprintf("todoapp-repository-%d", time.Now().UnixNano())`). Mirror the Task 3 memory-repo test cases so both adapters prove the same contract.
- [ ] **Step 2: Run** with the local DSN:

```bash
TEAM_MEMORY_TEST_POSTGRES_DSN="postgres://team_memory:team_memory@localhost:55432/team_memory?sslmode=disable" go test ./internal/todoapp/postgres/ -v
```

Expected: FAIL (migration table missing / package absent).

- [ ] **Step 3: Implement** migration + both `store.go` edits + adapter.
- [ ] **Step 4: Run** the same command → PASS. Also run `TEAM_MEMORY_TEST_POSTGRES_DSN=... go test ./internal/platform/postgres/ -run TestMigration -v` (replay idempotency) → PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(todoapp): postgres repository and migration 023"`

---

### Task 9: read.list adapter — TodoNoteDirectory

**Files:**
- Create: `internal/platform/postgres/todoapp_notes.go`
- Test: `internal/platform/postgres/todoapp_notes_test.go` (package `postgres_test`, reuse `testDSN(t)` helper from `store_test.go:38-46`)

**Interfaces:**
- Produces: `func NewTodoNoteDirectory(pool *pgxpool.Pool, scopeID string) (*TodoNoteDirectory, error)` implementing `todoapp.NoteDirectory`.

This is the spec's `read.list` primitive: enumerate open action items from team memory. Platform adapter implements the todoapp-owned port (CONTEXT-MAP rule: "Platform adapters implement ports defined by product contexts").

```go
func (d *TodoNoteDirectory) ListOpenActionItems(ctx context.Context, limit int) ([]todoapp.ActionItem, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.pool.Query(ctx, `
SELECT note_id, kind, subject, body, updated_at
FROM team_notes
WHERE scope_id = $1 AND kind IN ('blocker', 'handoff') AND state = 'active'
ORDER BY updated_at DESC, note_id DESC
LIMIT $2`, d.scopeID, limit)
	...
}
```

- [ ] **Step 1: Write failing test** — insert rows directly into `team_notes` with a unique scope (copy an existing raw INSERT from a platform postgres test, or fill every NOT NULL column from the schema in `migrations/001_init.sql:70-100`): one active blocker, one active handoff, one **resolved** blocker, one active **status** note. Assert exactly the two action items return, newest first, and resolved/status rows are excluded.
- [ ] **Step 2: Run to verify failure** (DSN env as Task 8) → FAIL.
- [ ] **Step 3: Implement.** Note: `internal/platform` importing `internal/todoapp` will fail the architecture registration test until Task 10 — run only `./internal/platform/postgres/` tests here.
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(platform): todoapp note directory implementing read.list"`

---

### Task 10: Architecture rules + context docs

**Files:**
- Modify: `internal/architecture/dependencies_test.go` (rules slice, lines ~48-66), `CONTEXT-MAP.md`
- Create: `internal/todoapp/CONTEXT.md`

- [ ] **Step 1: Run the architecture test to see the current failures**

Run: `go test ./internal/architecture/ -v`
Expected: FAIL — `internal/todoapp` unregistered; `platform` imports `todoapp` without grant.

- [ ] **Step 2: Add rules** (exact entries; insert after the `platform` entry):

```go
	{directory: "todoapp", excluded: []string{"transport"},
		allowed: []string{"platform/llm", "platform/observability", "session"}},
	{directory: "todoapp/transport",
		allowed: []string{"deployment/onprem", "todoapp", "teamnote/transport/httpapi/router/todoapp/api"}},
```

and add `"todoapp"` to the existing `platform` rule's `allowed` list. (The `todoapp/transport` entry is consumed by Task 11; the router path mirrors pagewiki's `teamnote/transport/httpapi/router/pagewiki/api` grant.)

- [ ] **Step 3: Run** — `go test ./internal/architecture/ -v` → PASS (todoapp/transport does not exist yet; absent directories are fine — verify, and if the test requires the directory, move this rule addition into Task 11).

- [ ] **Step 4: Write `internal/todoapp/CONTEXT.md`** (mirror the tone/length of `internal/pagewiki/CONTEXT.md`): purpose (first preset application: todo list over team memory), owns `todoapp_*` tables, consumes nexus via NoteDirectory (read.list) + EvidenceSink (report, source `app:todo`), never writes knowledge directly. Add to `CONTEXT-MAP.md` Contexts + Relationships:

```markdown
- [Todo App](./internal/todoapp/CONTEXT.md) — first preset application: todo list with suggestions derived from team memory; owns its own state and interacts with the nexus only through read (note directory) and report (app:todo evidence).
```

and under Relationships: `- **Todo App → Session**: reports semantic user actions as app:todo evidence streams; **Todo App → Team Note**: reads open blocker/handoff notes through a platform adapter.`

- [ ] **Step 5: Full test + commit**

```bash
go test ./internal/... 2>&1 | tail -20   # only pre-existing baseline failures allowed
git commit -m "feat(todoapp): register context in architecture rules and context map"
```

---

### Task 11: HTTP transport — IDL, codegen, handlers with human auth

**Files:**
- Create: `idl/todo_app.thrift`, `internal/todoapp/transport/httpapi/dependencies.go`, `internal/todoapp/transport/httpapi/endpoints.go`, `internal/todoapp/transport/httpapi/mapping.go`
- Modify: `Makefile` (IDL var + third `hz update` block in `generate`)
- Test: `internal/todoapp/transport/httpapi/endpoints_test.go`
- Generated (do not hand-edit): model + per-route bridge files + router under `internal/teamnote/transport/httpapi/router/todoapp/api/`

- [ ] **Step 1: Write the IDL** — `idl/todo_app.thrift`:

```thrift
namespace go todoapp.api

struct TodoItem {
  1: required string todo_id
  2: required string title
  3: required string body
  4: required string status
  5: required string source
  6: optional string suggestion_id
  7: optional string note_id
  8: required string created_by
  9: required string created_at
  10: required string updated_at
}

struct TodoSuggestionItem {
  1: required string suggestion_id
  2: required string note_id
  3: required string kind
  4: required string title
  5: required string body
  6: required string status
  7: required string created_at
}

struct ListTodosRequest { 1: optional string status (api.query="status") }
struct ListTodosResponse { 1: required list<TodoItem> todos }
struct CreateTodoRequest {
  1: required string title (api.body="title")
  2: optional string body (api.body="body")
}
struct TodoByIDRequest { 1: required string todo_id (api.path="todo_id") }
struct ListTodoSuggestionsRequest {}
struct ListTodoSuggestionsResponse { 1: required list<TodoSuggestionItem> suggestions }
struct RefreshTodoSuggestionsRequest {}
struct RefreshTodoSuggestionsResponse { 1: required i32 created }
struct TodoSuggestionByIDRequest { 1: required string suggestion_id (api.path="suggestion_id") }
struct DismissTodoSuggestionResponse {}

service TodoAppService {
  ListTodosResponse ListTodos(1: ListTodosRequest request) (api.get="/v1/todo/todos")
  TodoItem CreateTodo(1: CreateTodoRequest request) (api.post="/v1/todo/todos")
  TodoItem CompleteTodo(1: TodoByIDRequest request) (api.post="/v1/todo/todos/:todo_id/complete")
  ListTodoSuggestionsResponse ListTodoSuggestions(1: ListTodoSuggestionsRequest request) (api.get="/v1/todo/suggestions")
  RefreshTodoSuggestionsResponse RefreshTodoSuggestions(1: RefreshTodoSuggestionsRequest request) (api.post="/v1/todo/suggestions/refresh")
  TodoItem AcceptTodoSuggestion(1: TodoSuggestionByIDRequest request) (api.post="/v1/todo/suggestions/:suggestion_id/accept")
  DismissTodoSuggestionResponse DismissTodoSuggestion(1: TodoSuggestionByIDRequest request) (api.post="/v1/todo/suggestions/:suggestion_id/dismiss")
}
```

- [ ] **Step 2: Makefile + generate** — add near `PAGEWIKI_IDL`:

```makefile
TODOAPP_IDL := idl/todo_app.thrift
```

and a third block inside `generate` (copy the PAGEWIKI block, substituting dirs):

```makefile
	PATH=$(TOOLS_DIR):$$PATH $(HZ) update --module $(MODULE) --idl $(TODOAPP_IDL) --out_dir . \
		--handler_dir internal/todoapp/transport/httpapi \
		--model_dir internal/todoapp/transport/httpapi/model \
		--sort_router --handler_by_method
```

Run: `make generate`. Expected: stub handler files per route in `internal/todoapp/transport/httpapi/`, models under `.../model/todoapp/api/`, router under `internal/teamnote/transport/httpapi/router/todoapp/api/`, and root `router_gen.go`/register updated to include the new service. Verify with `git status` and `go build ./...` (build will fail until Step 4 fills the stubs — that is expected; check only that generation landed in the right dirs).

- [ ] **Step 3: Write failing handler tests** (`endpoints_test.go`). Copy the test harness style from `internal/pagewiki/transport/httpapi/contract_acceptance_test.go` (Hertz `ut.PerformRequest`). Fakes: a `fakeService` implementing the `Service` interface below, and a `fakeAuthenticator`:

```go
type fakeAuthenticator struct {
	principal onprem.HumanPrincipal
	err       error
}

func (f fakeAuthenticator) AuthenticateSession(context.Context, string) (onprem.HumanPrincipal, error) {
	return f.principal, f.err
}
```

Cases (table-driven where routes share behavior):
1. `GET /v1/todo/todos` without `tm_human_session` cookie → 401.
2. Mutation without `X-CSRF-Token` header matching the `tm_csrf` cookie → 403 `csrf_invalid`.
3. Authenticated `GET /v1/todo/todos?status=open` → 200, JSON `{"todos": [...]}` mapped from the fake.
4. `POST /v1/todo/todos` with cookie + CSRF → 201/200 with created item; asserts fake received `principal.UserID` as creator.
5. `POST /v1/todo/suggestions/:id/accept` unknown id (fake returns `todoapp.ErrNotFound`) → 404; `ErrInvalidTransition` → 409.

- [ ] **Step 4: Implement** `dependencies.go` (mirror `internal/pagewiki/transport/httpapi/dependencies.go` shape):

```go
package httpapi

const handlerContextKey = "todo-app.http-handler"

// Service is the domain surface the transport consumes.
type Service interface {
	CreateTodo(ctx context.Context, userID, title, body string) (todoapp.Todo, error)
	CompleteTodo(ctx context.Context, userID, todoID string) (todoapp.Todo, error)
	ListTodos(ctx context.Context, status todoapp.TodoStatus) ([]todoapp.Todo, error)
	PendingSuggestions(ctx context.Context) ([]todoapp.Suggestion, error)
	RefreshSuggestions(ctx context.Context) (int, error)
	AcceptSuggestion(ctx context.Context, userID, suggestionID string) (todoapp.Todo, error)
	DismissSuggestion(ctx context.Context, userID, suggestionID string) error
}

// HumanAuthenticator validates a Human Portal session token.
// *onprem.IdentityService satisfies it.
type HumanAuthenticator interface {
	AuthenticateSession(ctx context.Context, token string) (onprem.HumanPrincipal, error)
}

type Handler struct {
	service  Service
	identity HumanAuthenticator // nil => endpoints answer 501 not_configured
}

func New(service Service, identity HumanAuthenticator) (*Handler, error)
func InstanceMiddleware(handler *Handler) app.HandlerFunc
```

Auth helper in `endpoints.go` — duplicate the double-submit logic (source: `internal/teamnote/transport/httpapi/handler/identity_registry_endpoints.go:752-860`; it is unexported there, note the duplication in a comment):

```go
const (
	humanSessionCookieName = "tm_human_session"
	csrfCookieName         = "tm_csrf"
	csrfHeaderName         = "X-CSRF-Token"
)

func (h *Handler) authorize(ctx context.Context, c *app.RequestContext, mutation bool) (onprem.HumanPrincipal, bool) {
	if h.identity == nil {
		writeError(c, consts.StatusNotImplemented, "not_configured", "human identity is not configured")
		return onprem.HumanPrincipal{}, false
	}
	token := string(c.Cookie(humanSessionCookieName))
	if strings.TrimSpace(token) == "" {
		writeError(c, consts.StatusUnauthorized, "unauthenticated", "sign in to the portal first")
		return onprem.HumanPrincipal{}, false
	}
	if mutation {
		header := string(c.GetHeader(csrfHeaderName))
		cookie := string(c.Cookie(csrfCookieName))
		if header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(cookie)) != 1 {
			writeError(c, consts.StatusForbidden, "csrf_invalid", "the CSRF token is invalid")
			return onprem.HumanPrincipal{}, false
		}
	}
	principal, err := h.identity.AuthenticateSession(ctx, token)
	if err != nil {
		writeError(c, consts.StatusUnauthorized, "unauthenticated", "the session is not valid")
		return onprem.HumanPrincipal{}, false
	}
	return principal, true
}
```

**Before finalizing the CSRF check, read `validateCSRF` + `csrfToken` at `identity_registry_endpoints.go:845-860`**: the header is compared against a *derived* token (`base64url(sha256("csrf\x00" + sessionToken))`), and the frontend sends the `tm_csrf` cookie value. Copy the exact comparison the teamnote handler uses so the portal's existing CSRF cookie works unchanged.

Endpoint methods fill the generated stubs; error mapping in `mapping.go`: `ErrNotFound` → 404 `not_found`, `ErrInvalidTransition` → 409 `invalid_transition`, `ErrInvalidInput` → 400 `invalid_input`, other → 500. Domain→wire mapping formats times as RFC3339.

- [ ] **Step 5: Run** — `go test ./internal/todoapp/transport/... -v` → PASS; `go build ./...` → OK; `go test ./internal/architecture/ -v` → PASS.
- [ ] **Step 6: Commit** — include generated files: `git add -A && git commit -m "feat(todoapp): HTTP transport with human-session auth"`

---

### Task 12: main.go wiring + scheduler

**Files:**
- Create: `internal/todoapp/scheduler.go` (+ `scheduler_test.go`)
- Modify: `main.go`

**Scheduler** (copy the `startOperationsMaintenance` shape, `main.go:697-724` — run once immediately, then tick; returns stop func):

```go
// scheduler.go
package todoapp

func StartSuggestionRefresh(ctx context.Context, service *Service, interval time.Duration, logger *slog.Logger) func() {
	if interval <= 0 {
		interval = time.Hour
	}
	refreshContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		refresh(refreshContext, service, logger)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshContext.Done():
				return
			case <-ticker.C:
				refresh(refreshContext, service, logger)
			}
		}
	}()
	return func() { cancel(); <-done }
}

func refresh(ctx context.Context, service *Service, logger *slog.Logger) {
	created, err := service.RefreshSuggestions(ctx)
	if err != nil {
		logger.Warn("todo suggestion refresh failed", "error", err)
		return
	}
	if created > 0 {
		logger.Info("todo suggestions refreshed", "created", created)
	}
}
```

Test: short interval (10ms) + fake NoteDirectory counting calls; assert ≥2 refreshes then stop() returns and count freezes.

**main.go wiring** (inside `run`, after the pagewiki block; follow its style):

1. Reuse the Lake: at `main.go:103` change `teamruntime.New(evidencelake.New(sessions), ...)` to bind `lake := evidencelake.New(sessions)` first and pass `lake` to both the runtime and the reporter.
2. Build todoapp (new `buildTodoApp` helper in main.go to keep `run` complexity down):

```go
todoRepository, err := todoapppostgres.NewRepository(ctx, store.Pool(), onprem.LocalScopeID)
noteDirectory, err := postgres.NewTodoNoteDirectory(store.Pool(), onprem.LocalScopeID)
reporter, err := todoapp.NewLakeReporter(lake, onprem.LocalScopeID)
var rewriter todoapp.Rewriter
if config.llmWikiBaseURL != "" && config.llmWikiAPIKey != "" && config.llmWikiModel != "" {
	client := platformllm.NewDeepSeekClient(platformllm.DeepSeekConfig{BaseURL: config.llmWikiBaseURL, APIKey: config.llmWikiAPIKey})
	rewriter, err = todoapp.NewLLMRewriter(todoapp.LLMRewriterConfig{Client: client, Model: config.llmWikiModel, Logger: logger})
}
todoService, err := todoapp.NewService(todoapp.ServiceConfig{Repository: todoRepository, Notes: noteDirectory, Rewriter: rewriter, Reporter: reporter, Logger: logger})
```

Reuse the existing `LLMWIKI_LLM_*` env values — find how `buildPageWikiMaintainers` reads them (`main.go:202-247`) and hoist the three values into `applicationConfig` if they are currently read inline. New env: `TODOAPP_REFRESH_INTERVAL` via the existing `durationEnvironment` helper, default `1h`.

3. Identity sharing: the todo handler needs the same `*onprem.IdentityService` the teamnote handler gets. Locate its construction (`main.go:665-668` area inside the onprem build path); hoist it so both handler constructions receive it. When identity is not configured (legacy API-key mode), pass `nil` — todo endpoints then answer 501, which is correct (todo is a Human Portal feature).
4. Register: `h.Use(todoapphttp.InstanceMiddleware(todoHandler))` next to the pagewiki `InstanceMiddleware` call (`main.go:141-143` region).
5. Lifecycle: `stopTodoRefresh := todoapp.StartSuggestionRefresh(ctx, todoService, config.todoRefreshInterval, logger)`; call `stopTodoRefresh()` next to `stopOperations()` after `h.Spin()`.

- [ ] **Step 1: Write the scheduler test**, verify FAIL, implement scheduler, verify PASS.
- [ ] **Step 2: Wire main.go**; `go build ./...` → OK.
- [ ] **Step 3: End-to-end smoke against local Postgres:**

```bash
TEAM_MEMORY_DATABASE_URL="postgres://team_memory:team_memory@localhost:55432/team_memory?sslmode=disable" go run . &
sleep 3
curl -s http://localhost:58080/v1/todo/todos            # expect 401/501 JSON (auth wired), NOT 404
curl -s -X POST http://localhost:58080/v1/todo/suggestions/refresh   # expect 401/403/501, NOT 404
kill %1
```

Expected: routes exist and refuse unauthenticated access; startup logs show no todoapp errors; migration 023 applied (`psql ... -c '\d todoapp_todos'`).

- [ ] **Step 4: Commit** — `git commit -m "feat(todoapp): wire service, scheduler, and transport in main"`

---

### Task 13: Frontend — API modules, TodoPage, nav, dom test

**Files:**
- Create: `web/src/api/todo.ts`, `web/src/pages/TodoPage.tsx`
- Modify: `web/src/api/actions.ts` (mutations), `web/src/pages/PortalShell.tsx` (route + nav)
- Test: `web/tests/todo.dom.test.tsx`

- [ ] **Step 1: `web/src/api/todo.ts`** (reads only; wiki.ts conventions: AbortSignal param, `encodeURIComponent`, envelope `?? []`):

```ts
import { humanFetch } from "./client";

export interface TodoItem {
  todo_id: string;
  title: string;
  body: string;
  status: "open" | "done";
  source: "manual" | "suggestion";
  suggestion_id?: string;
  note_id?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface TodoSuggestion {
  suggestion_id: string;
  note_id: string;
  kind: string;
  title: string;
  body: string;
  status: string;
  created_at: string;
}

export async function listTodos(status: "" | "open" | "done", signal?: AbortSignal): Promise<TodoItem[]> {
  const query = status ? `?status=${encodeURIComponent(status)}` : "";
  const response = await humanFetch<{ todos: TodoItem[] }>(`/v1/todo/todos${query}`, { signal });
  return response.todos ?? [];
}

export async function listTodoSuggestions(signal?: AbortSignal): Promise<TodoSuggestion[]> {
  const response = await humanFetch<{ suggestions: TodoSuggestion[] }>("/v1/todo/suggestions", { signal });
  return response.suggestions ?? [];
}
```

- [ ] **Step 2: Mutations in `web/src/api/actions.ts`** (per AGENTS.md every mutation lives here; todo transitions are idempotent state machines, so no Idempotency-Key/If-Match — add a comment saying so):

```ts
export function createTodo(title: string, body: string): Promise<TodoItem> {
  return humanFetch<TodoItem>("/v1/todo/todos", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title, body }),
  });
}

export function completeTodo(todoId: string): Promise<TodoItem> {
  return humanFetch<TodoItem>(`/v1/todo/todos/${encodeURIComponent(todoId)}/complete`, { method: "POST" });
}

export function refreshTodoSuggestions(): Promise<{ created: number }> {
  return humanFetch<{ created: number }>("/v1/todo/suggestions/refresh", { method: "POST" });
}

export function acceptTodoSuggestion(suggestionId: string): Promise<TodoItem> {
  return humanFetch<TodoItem>(`/v1/todo/suggestions/${encodeURIComponent(suggestionId)}/accept`, { method: "POST" });
}

export function dismissTodoSuggestion(suggestionId: string): Promise<void> {
  return humanFetch<void>(`/v1/todo/suggestions/${encodeURIComponent(suggestionId)}/dismiss`, { method: "POST" });
}
```

(import `TodoItem` type from `./todo`.)

- [ ] **Step 3: `TodoPage.tsx`** — keep to ~200 lines, portal styling conventions (look at `WikiPage.tsx` for section/list class names). Structure:
  - "Suggestions" section: pending suggestions; each card shows kind badge (`blocker`/`handoff`), title, body, and two buttons Accept / Dismiss; a "Check team memory" button calling `refreshTodoSuggestions()` then refetching; empty state text "No suggestions right now."
  - "Todos" section: open todos each with a Complete button; a collapsed "Done" list underneath; a minimal add form (title input + Add button).
  - All mutations: optimistic-free — await, then refetch both lists; errors surface via the portal's existing error text pattern (see how `WikiPage` renders `ApiError` messages).

- [ ] **Step 4: PortalShell** — add under the `Knowledge` nav label (`PortalShell.tsx:124-133`):

```tsx
<NavLink to="/todo" className={navClass} end>
  Todos
</NavLink>
```

and route (in the `<Routes>` block, `:183-197`):

```tsx
<Route path="/todo" element={<TodoPage />} />
```

Import `TodoPage` with the other page imports.

- [ ] **Step 5: Write `web/tests/todo.dom.test.tsx`** — copy the harness from `web/tests/wiki.dom.test.tsx` (`setupDomTest()`, `stubFetch`, `renderApp`, `makeMe`; unmatched paths throw). Cases:
  1. Navigating to Todos renders suggestions + todos from stubbed `GET /v1/todo/suggestions` and `GET /v1/todo/todos`.
  2. Clicking Accept fires `POST /v1/todo/suggestions/s1/accept` (assert with `callsTo`) and refetches lists.
  3. Clicking Complete fires `POST /v1/todo/todos/t1/complete`.

- [ ] **Step 6: Run** — `cd web && npm test` → PASS; `npm run build` → OK.
- [ ] **Step 7: Commit** — `git commit -m "feat(web): todo app page with suggestion inbox"`

---

### Task 14: Full verification + PR

- [ ] **Step 1: Full gates**

```bash
make lint test 2>&1 | tail -30
TEAM_MEMORY_TEST_POSTGRES_DSN="postgres://team_memory:team_memory@localhost:55432/team_memory?sslmode=disable" go test ./... 2>&1 | tail -10
make coverage 2>&1 | tail -5
cd web && npm test && npm run build
```

Expected: no NEW failures vs the Task 1 baseline; coverage ≥75% on new packages.

- [ ] **Step 2: Manual loop check** (optional but recommended, needs LLM env unset → verbatim mode): run the server against local Postgres, seed one active blocker note via SQL, hit refresh, accept the suggestion in the UI, then verify the report landed:

```sql
SELECT stream_source, content FROM session_events WHERE stream_source = 'app:todo' ORDER BY id DESC LIMIT 5;
```

(Column names may differ — check `migrations/021_evidence_streams.sql`; acceptance = a row with the accept summary exists.)

- [ ] **Step 3: Update spec status** (`docs/superpowers/specs/2026-07-29-pax-nexus-app-platform-design.md` header: MVP implemented on branch feat/todoapp-mvp) and commit.

- [ ] **Step 4: PR** — `gh pr create` against main, title `feat(todoapp): todo list MVP on the app platform read/report contract`, body summarizing: new context, read.list adapter, app:todo evidence source, migration 023, portal page. End body with the standard generation footer.
