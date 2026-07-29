# Evidence Lake Core (Plan 1 of 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generalize the session lake into a source-agnostic evidence lake: stream/author contract types with closed registries, a schema migration that re-keys streams by `(source, stream_id)`, a generic `/v1/stream-batches` ingest endpoint with ingest-assigned sequences, and the `sessionlake` → `evidencelake` rename — while the paxm `/v1/session-batches` endpoint stays byte-compatible.

**Architecture:** Additive generalization of `internal/session` contracts and the `session_events`/`session_streams` tables; the existing Actor-keyed path keeps working and now also populates the generalized columns. A new stream-keyed append path assigns sequences server-side. Downstream extraction stays Actor-keyed in this plan (stream-keyed extraction is Plan 2).

**Tech Stack:** Go, CloudWeGo Hertz + thrift IDL codegen (`make generate`), pgx/PostgreSQL (embedded SQL migrations), testify suite, mockgen (`make mocks`).

**Spec:** `docs/superpowers/specs/2026-07-28-evidence-lake-design.md`

## Plan series

The spec decomposes into four sequential plans; each ships working software:

1. **This plan** — contract, storage generalization, generic ingest endpoint, rename, docs.
2. Stream-keyed extraction: extraction cursors/jobs keyed by `(source, stream_id)` so non-session sources are extracted; eval acceptance per spec.
3. Identity mapping: On-prem-Identity-owned `source + native_id → user_id` mapping, ingest-time resolution, backfill.
4. Media: `BlobStore` port, filesystem adapter, blob upload endpoint, `MediaRef` enforcement.

## Global Constraints

- Registered sources this phase: `agent-session`, `im-channel`. Registered kinds: `text`, `audio`, `image`, `video`, `file`. Registered event types: `message`, `reply`, `reaction`, `system`, `checkpoint`, `attachment`. Ingest rejects unregistered values on the generic endpoint.
- Only `visibility == "team"` is accepted on the generic endpoint; everything else is rejected.
- Non-`text` kinds are rejected with an explicit "media ingestion not enabled" error until Plan 4 lands. The `media` JSONB column is created now so Plan 4 needs no second migration.
- The generic endpoint assigns `sequence` server-side per stream; source ordering is never trusted. Dedup key stays `(scope_id, event_id)`.
- `author.native_id` is required and stored verbatim; `author.user_id` is optional and stays empty until Plan 3.
- The paxm endpoint `/v1/session-batches` keeps its exact wire contract, including client-supplied sequences. Its registry rules are NOT tightened (existing paxm `type` values keep flowing).
- Legacy `agent-session` stream identity is `stream_id = agent_id + ":" + session_id`.
- `metadata` is never queried by SQL; all filters use the indexed columns.
- Go module `github.com/pax-beehive/pax-nexus`. Postgres-backed tests require `TEAM_MEMORY_TEST_POSTGRES_DSN` and skip without it. Run `make generate` after IDL edits and `make mocks` after interface edits; generated code is committed.
- macOS sed is BSD sed: use `sed -i ''`.

---

### Task 1: Evidence contract types, registries, and validation

**Files:**
- Create: `internal/session/evidence.go`
- Create: `internal/session/evidence_test.go`

**Interfaces:**
- Consumes: `session.Actor` (`internal/session/contracts.go:7`).
- Produces: `session.Stream{Source, StreamID}`, `session.Author{Kind, NativeID, UserID}`, `session.MediaRef{BlobID, MimeType, Size, Checksum}`, `session.StreamEvent`, `session.StreamBatch{Events, Complete}`, `session.ValidateStreamBatch(StreamBatch) error`, `session.StreamFromActor(Actor) Stream`, `session.AuthorFromActor(Actor) Author`, constants `SourceAgentSession`, `SourceIMChannel`, `KindText`, `VisibilityTeam`, errors `ErrInvalidStreamBatch`, `ErrUnregisteredValue`, `ErrVisibilityRejected`, `ErrMediaNotEnabled`. Tasks 2–5 rely on these exact names.

- [ ] **Step 1: Write the failing test**

```go
package session_test

import (
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

func validEvent() session.StreamEvent {
	return session.StreamEvent{
		ID:         "evt-1",
		Stream:     session.Stream{Source: session.SourceIMChannel, StreamID: "channel-9"},
		Author:     session.Author{Kind: "user", NativeID: "U0AB12"},
		Kind:       session.KindText,
		Type:       "message",
		Content:    "ship the rollback pack by Friday",
		Visibility: session.VisibilityTeam,
		OccurredAt: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
	}
}

func TestValidateStreamBatchAcceptsRegisteredTeamEvent(t *testing.T) {
	batch := session.StreamBatch{Events: []session.StreamEvent{validEvent()}}
	if err := session.ValidateStreamBatch(batch); err != nil {
		t.Fatalf("expected valid batch, got %v", err)
	}
}

func TestValidateStreamBatchRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*session.StreamEvent)
		target error
	}{
		{"unregistered source", func(e *session.StreamEvent) { e.Stream.Source = "carrier-pigeon" }, session.ErrUnregisteredValue},
		{"unregistered kind", func(e *session.StreamEvent) { e.Kind = "hologram" }, session.ErrUnregisteredValue},
		{"unregistered type", func(e *session.StreamEvent) { e.Type = "poke" }, session.ErrUnregisteredValue},
		{"unregistered author kind", func(e *session.StreamEvent) { e.Author.Kind = "robot" }, session.ErrUnregisteredValue},
		{"non-team visibility", func(e *session.StreamEvent) { e.Visibility = "private" }, session.ErrVisibilityRejected},
		{"media kind without plan 4", func(e *session.StreamEvent) { e.Kind = "audio" }, session.ErrMediaNotEnabled},
		{"missing native id", func(e *session.StreamEvent) { e.Author.NativeID = " " }, session.ErrInvalidStreamBatch},
		{"missing id", func(e *session.StreamEvent) { e.ID = "" }, session.ErrInvalidStreamBatch},
		{"zero occurred_at", func(e *session.StreamEvent) { e.OccurredAt = time.Time{} }, session.ErrInvalidStreamBatch},
		{"caller-set sequence", func(e *session.StreamEvent) { e.Sequence = 7 }, session.ErrInvalidStreamBatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validEvent()
			test.mutate(&event)
			err := session.ValidateStreamBatch(session.StreamBatch{Events: []session.StreamEvent{event}})
			if !errors.Is(err, test.target) {
				t.Fatalf("expected %v, got %v", test.target, err)
			}
		})
	}
}

func TestValidateStreamBatchRejectsEmptyAndMixedStreams(t *testing.T) {
	if err := session.ValidateStreamBatch(session.StreamBatch{}); !errors.Is(err, session.ErrInvalidStreamBatch) {
		t.Fatalf("expected empty batch rejection, got %v", err)
	}
	first, second := validEvent(), validEvent()
	second.ID = "evt-2"
	second.Stream.StreamID = "channel-other"
	err := session.ValidateStreamBatch(session.StreamBatch{Events: []session.StreamEvent{first, second}})
	if !errors.Is(err, session.ErrInvalidStreamBatch) {
		t.Fatalf("expected mixed-stream rejection, got %v", err)
	}
}

func TestStreamFromActorDerivesLegacyIdentity(t *testing.T) {
	actor := session.Actor{UserID: "todd", AgentID: "agent-7", SessionID: "sess-42"}
	stream := session.StreamFromActor(actor)
	if stream.Source != session.SourceAgentSession || stream.StreamID != "agent-7:sess-42" {
		t.Fatalf("unexpected stream %+v", stream)
	}
	author := session.AuthorFromActor(actor)
	if author.Kind != "agent" || author.NativeID != "agent-7" || author.UserID != "todd" {
		t.Fatalf("unexpected author %+v", author)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run 'TestValidateStreamBatch|TestStreamFromActor' -v`
Expected: FAIL (compile error: undefined `session.StreamEvent` etc.)

- [ ] **Step 3: Write the implementation**

```go
package session

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Evidence Lake generalized contracts. Any external connector pushes streams of
// flat events through these types; source-specific structure lives only in
// registered metadata keys.

const (
	SourceAgentSession = "agent-session"
	SourceIMChannel    = "im-channel"

	KindText  = "text"
	KindAudio = "audio"
	KindImage = "image"
	KindVideo = "video"
	KindFile  = "file"

	VisibilityTeam = "team"
)

var (
	ErrInvalidStreamBatch = errors.New("invalid stream batch")
	ErrUnregisteredValue  = errors.New("unregistered contract value")
	ErrVisibilityRejected = errors.New("visibility not accepted")
	ErrMediaNotEnabled    = errors.New("media ingestion not enabled")
)

var registeredSources = map[string]struct{}{SourceAgentSession: {}, SourceIMChannel: {}}

var registeredKinds = map[string]struct{}{
	KindText: {}, KindAudio: {}, KindImage: {}, KindVideo: {}, KindFile: {},
}

var registeredEventTypes = map[string]struct{}{
	"message": {}, "reply": {}, "reaction": {}, "system": {}, "checkpoint": {}, "attachment": {},
}

var registeredAuthorKinds = map[string]struct{}{"user": {}, "agent": {}, "system": {}}

type Stream struct {
	Source   string `json:"source"`
	StreamID string `json:"stream_id"`
}

type Author struct {
	Kind     string `json:"kind"`
	NativeID string `json:"native_id"`
	UserID   string `json:"user_id,omitempty"`
}

type MediaRef struct {
	BlobID   string `json:"blob_id"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Checksum string `json:"checksum"`
}

// StreamEvent is immutable source evidence pushed by an external connector.
// Sequence is assigned by ingest and must be zero on input.
type StreamEvent struct {
	ID         string            `json:"id"`
	Stream     Stream            `json:"stream"`
	Author     Author            `json:"author"`
	Sequence   int64             `json:"sequence"`
	Kind       string            `json:"kind"`
	Type       string            `json:"type"`
	Content    string            `json:"content"`
	Media      *MediaRef         `json:"media,omitempty"`
	ThreadRef  string            `json:"thread_ref,omitempty"`
	Visibility string            `json:"visibility"`
	OccurredAt time.Time         `json:"occurred_at"`
	CapturedAt time.Time         `json:"captured_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type StreamBatch struct {
	Events   []StreamEvent `json:"events"`
	Complete bool          `json:"complete"`
}

func ValidateStreamBatch(batch StreamBatch) error {
	if len(batch.Events) == 0 {
		return fmt.Errorf("empty batch: %w", ErrInvalidStreamBatch)
	}
	stream := batch.Events[0].Stream
	for _, event := range batch.Events {
		if strings.TrimSpace(event.ID) == "" || event.OccurredAt.IsZero() {
			return fmt.Errorf("event %q identity: %w", event.ID, ErrInvalidStreamBatch)
		}
		if event.Sequence != 0 {
			return fmt.Errorf("event %q carries a caller sequence: %w", event.ID, ErrInvalidStreamBatch)
		}
		if event.Stream != stream || strings.TrimSpace(stream.StreamID) == "" {
			return fmt.Errorf("event %q mixed or empty stream: %w", event.ID, ErrInvalidStreamBatch)
		}
		if _, ok := registeredSources[event.Stream.Source]; !ok {
			return fmt.Errorf("source %q: %w", event.Stream.Source, ErrUnregisteredValue)
		}
		if _, ok := registeredKinds[event.Kind]; !ok {
			return fmt.Errorf("kind %q: %w", event.Kind, ErrUnregisteredValue)
		}
		if _, ok := registeredEventTypes[event.Type]; !ok {
			return fmt.Errorf("type %q: %w", event.Type, ErrUnregisteredValue)
		}
		if _, ok := registeredAuthorKinds[event.Author.Kind]; !ok {
			return fmt.Errorf("author kind %q: %w", event.Author.Kind, ErrUnregisteredValue)
		}
		if strings.TrimSpace(event.Author.NativeID) == "" {
			return fmt.Errorf("event %q author native id: %w", event.ID, ErrInvalidStreamBatch)
		}
		if event.Visibility != VisibilityTeam {
			return fmt.Errorf("visibility %q: %w", event.Visibility, ErrVisibilityRejected)
		}
		if event.Kind != KindText {
			// Blob storage arrives in Plan 4; until then media kinds are rejected
			// deterministically instead of silently dropping the original bytes.
			return fmt.Errorf("kind %q: %w", event.Kind, ErrMediaNotEnabled)
		}
	}
	return nil
}

// StreamFromActor maps a legacy agent-session actor onto the generalized
// stream identity. The concatenation preserves the old per-(agent, session)
// stream uniqueness.
func StreamFromActor(actor Actor) Stream {
	return Stream{Source: SourceAgentSession, StreamID: actor.AgentID + ":" + actor.SessionID}
}

func AuthorFromActor(actor Actor) Author {
	return Author{Kind: "agent", NativeID: actor.AgentID, UserID: actor.UserID}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -v`
Expected: PASS (including the pre-existing scope tests)

- [ ] **Step 5: Commit**

```bash
git add internal/session/evidence.go internal/session/evidence_test.go
git commit -m "feat(session): add generalized evidence stream contracts and registries"
```

---

### Task 2: Schema migration 021 — generalized stream identity

**Files:**
- Create: `internal/platform/postgres/migrations/021_evidence_streams.sql`
- Modify: `internal/platform/postgres/migration_test.go`

**Interfaces:**
- Consumes: existing tables from `migrations/001_init.sql` (`session_events`, `session_streams`).
- Produces: columns `source`, `stream_id`, `kind`, `author_kind`, `author_native_id`, `author_user_id`, `media` on `session_events`; columns `source`, `stream_id`, `visibility` on `session_streams`; `session_streams` PK `(scope_id, source, stream_id)`; `session_events` unique `(scope_id, source, stream_id, sequence)`. Tasks 3–4 write these columns.

- [ ] **Step 1: Write the migration**

```sql
-- Generalize session storage into evidence streams keyed by (source, stream_id).
-- Legacy agent-session rows derive stream_id = agent_id || ':' || session_id.

ALTER TABLE session_events
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent-session',
    ADD COLUMN IF NOT EXISTS stream_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'text',
    ADD COLUMN IF NOT EXISTS author_kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS author_native_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS author_user_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS media JSONB;

UPDATE session_events
SET stream_id = agent_id || ':' || session_id,
    author_native_id = agent_id,
    author_user_id = user_id
WHERE stream_id = '';

ALTER TABLE session_events
    DROP CONSTRAINT IF EXISTS session_events_scope_id_agent_id_session_id_sequence_key;

CREATE UNIQUE INDEX IF NOT EXISTS session_events_stream_sequence_key
    ON session_events (scope_id, source, stream_id, sequence);

CREATE INDEX IF NOT EXISTS session_events_source_kind_type_idx
    ON session_events (scope_id, source, kind, event_type);

CREATE INDEX IF NOT EXISTS session_events_author_user_idx
    ON session_events (scope_id, author_user_id);

CREATE INDEX IF NOT EXISTS session_events_occurred_at_idx
    ON session_events (scope_id, occurred_at);

CREATE INDEX IF NOT EXISTS session_events_thread_ref_idx
    ON session_events (scope_id, thread_ref);

ALTER TABLE session_streams
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'agent-session',
    ADD COLUMN IF NOT EXISTS stream_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'team';

UPDATE session_streams
SET stream_id = agent_id || ':' || session_id
WHERE stream_id = '';

ALTER TABLE session_streams
    DROP CONSTRAINT IF EXISTS session_streams_pkey;

ALTER TABLE session_streams
    ADD PRIMARY KEY (scope_id, source, stream_id);

CREATE INDEX IF NOT EXISTS session_streams_actor_idx
    ON session_streams (scope_id, agent_id, session_id);
```

- [ ] **Step 2: Write the failing schema test**

Append to `migrationSuite` in `internal/platform/postgres/migration_test.go`:

```go
func (s *migrationSuite) TestEvidenceStreamSchema() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := fmt.Sprintf("evidence_schema_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	_, err := s.pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema)
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_, cleanupErr := s.pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
		s.NoError(cleanupErr)
	})

	config, err := pgxpool.ParseConfig(s.dsn)
	s.Require().NoError(err)
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	s.Require().NoError(err)
	s.T().Cleanup(pool.Close)
	s.Require().NoError(newStore(pool).Migrate(ctx))

	for _, column := range []string{"source", "stream_id", "kind", "author_kind", "author_native_id", "author_user_id", "media"} {
		var found bool
		err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = $1 AND table_name = 'session_events' AND column_name = $2)`,
			schema, column).Scan(&found)
		s.Require().NoError(err)
		s.True(found, "session_events.%s missing", column)
	}

	var streamPK string
	err = pool.QueryRow(ctx, `
SELECT string_agg(a.attname, ',' ORDER BY array_position(i.indkey, a.attnum))
FROM pg_index i
JOIN pg_class c ON c.oid = i.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY (i.indkey)
WHERE n.nspname = $1 AND c.relname = 'session_streams' AND i.indisprimary`, schema).Scan(&streamPK)
	s.Require().NoError(err)
	s.Equal("scope_id,source,stream_id", streamPK)

	var uniqueExists bool
	err = pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = $1 AND tablename = 'session_events'
      AND indexname = 'session_events_stream_sequence_key')`, schema).Scan(&uniqueExists)
	s.Require().NoError(err)
	s.True(uniqueExists)
}
```

Add `"github.com/jackc/pgx/v5/pgxpool"` to the file's imports if not already present (it is — verify).

- [ ] **Step 3: Run the test — verify it fails before the migration file is picked up, passes after**

Run: `TEAM_MEMORY_TEST_POSTGRES_DSN=<your dsn> go test ./internal/platform/postgres/ -run 'TestMigrationSuite' -v`
Expected: PASS with the migration file in place; temporarily `git stash` the SQL file to see it FAIL if you want the red step, then unstash. Without a DSN the suite skips — a Postgres instance is required for this task (use `make eval-v3-up`'s Postgres or a local one).

- [ ] **Step 4: Run the full postgres package tests**

Run: `TEAM_MEMORY_TEST_POSTGRES_DSN=<your dsn> go test ./internal/platform/postgres/ -v`
Expected: PASS — in particular the legacy store tests still pass; the migration must not break the Actor-keyed path. If `AdvanceExtractionCursor` or stream upserts fail here, the migration's PK change broke the legacy upsert — fix Task 3's SQL together with this before committing.

Note: `upsertStream` in `sessions.go` conflicts on `(scope_id, agent_id, session_id)`, which is no longer a unique constraint after this migration. Step 4 WILL fail until the Task 3 change to `sessions.go` lands. Implement Task 2 and Task 3 on one branch and commit them together if the intermediate state cannot pass; the commit checkpoint below applies after both are green — this is the one intentional exception to task-by-task commits in this plan.

- [ ] **Step 5: Commit (may be combined with Task 3)**

```bash
git add internal/platform/postgres/migrations/021_evidence_streams.sql internal/platform/postgres/migration_test.go
git commit -m "feat(postgres): migrate session storage to evidence stream identity"
```

---

### Task 3: Postgres stream append path and legacy write generalization

**Files:**
- Create: `internal/platform/postgres/streams.go`
- Create: `internal/platform/postgres/streams_test.go`
- Modify: `internal/platform/postgres/sessions.go` (`appendEvents` insert at `sessions.go:205`, `upsertStream` at `sessions.go:229`)

**Interfaces:**
- Consumes: `session.StreamBatch`, `session.StreamEvent`, `session.StreamFromActor`, `session.AuthorFromActor` (Task 1); migration 021 columns (Task 2).
- Produces: `(*SessionRepository) AppendStream(ctx context.Context, scopeID string, batch session.StreamBatch) (session.IngestReceipt, error)` and `(*SessionRepository) StreamEvents(ctx context.Context, scopeID string, stream session.Stream, after int64, limit int) ([]session.StreamEvent, error)`. Task 4 consumes both through the lake.

- [ ] **Step 1: Write the failing test**

`streams_test.go` (follow the harness used by `store_test.go` — same suite setup with `TEAM_MEMORY_TEST_POSTGRES_DSN`, isolated schema per test; reuse its helper if one is exported, otherwise mirror its `SetupSuite`):

```go
func (s *storeSuite) TestAppendStreamAssignsSequencesAndDedupes() {
	ctx := context.Background()
	scopeID := "scope-evidence"
	event := func(id, content string) session.StreamEvent {
		return session.StreamEvent{
			ID:         id,
			Stream:     session.Stream{Source: session.SourceIMChannel, StreamID: "channel-9"},
			Author:     session.Author{Kind: "user", NativeID: "U0AB12"},
			Kind:       session.KindText,
			Type:       "message",
			Content:    content,
			Visibility: session.VisibilityTeam,
			OccurredAt: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
		}
	}

	receipt, err := s.sessions.AppendStream(ctx, scopeID, session.StreamBatch{
		Events: []session.StreamEvent{event("evt-1", "first"), event("evt-2", "second")},
	})
	s.Require().NoError(err)
	s.Equal(2, receipt.Accepted)
	s.Equal(int64(2), receipt.Cursor)

	// Replay of evt-2 plus one new event: duplicate is not re-sequenced.
	receipt, err = s.sessions.AppendStream(ctx, scopeID, session.StreamBatch{
		Events: []session.StreamEvent{event("evt-2", "second"), event("evt-3", "third")},
	})
	s.Require().NoError(err)
	s.Equal(1, receipt.Accepted)
	s.Equal(1, receipt.Duplicate)
	s.Equal(int64(3), receipt.Cursor)

	events, err := s.sessions.StreamEvents(ctx, scopeID,
		session.Stream{Source: session.SourceIMChannel, StreamID: "channel-9"}, 0, 10)
	s.Require().NoError(err)
	s.Require().Len(events, 3)
	for index, event := range events {
		s.Equal(int64(index+1), event.Sequence)
		s.Equal("U0AB12", event.Author.NativeID)
		s.Equal(session.SourceIMChannel, event.Stream.Source)
	}
}

func (s *storeSuite) TestAppendSessionPopulatesEvidenceColumns() {
	ctx := context.Background()
	scopeID := "scope-legacy"
	actor := session.Actor{UserID: "todd", AgentID: "agent-7", SessionID: "sess-42"}
	_, err := s.sessions.AppendSession(ctx, scopeID, session.SessionBatch{Events: []session.SessionEvent{{
		ID: "legacy-1", Actor: actor, Sequence: 1, Type: "message",
		Content: "hello", OccurredAt: time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
	}}})
	s.Require().NoError(err)

	events, err := s.sessions.StreamEvents(ctx, scopeID, session.StreamFromActor(actor), 0, 10)
	s.Require().NoError(err)
	s.Require().Len(events, 1)
	s.Equal("agent-7:sess-42", events[0].Stream.StreamID)
	s.Equal("agent-7", events[0].Author.NativeID)
	s.Equal("todd", events[0].Author.UserID)
}
```

Adjust the suite name/field (`s.sessions`) to match `store_test.go`'s actual suite; add the test methods to that suite rather than creating a parallel harness.

- [ ] **Step 2: Run test to verify it fails**

Run: `TEAM_MEMORY_TEST_POSTGRES_DSN=<dsn> go test ./internal/platform/postgres/ -run 'TestStore' -v`
Expected: FAIL (undefined `AppendStream` / `StreamEvents`)

- [ ] **Step 3: Implement `streams.go`**

```go
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

// AppendStream persists one connector batch for a single generalized stream.
// Sequences are assigned here, in ingest order, under a stream row lock;
// duplicates (by event id) do not consume sequence numbers.
func (r *SessionRepository) AppendStream(ctx context.Context, scopeID string, batch session.StreamBatch) (receipt session.IngestReceipt, returnedErr error) {
	if err := session.ValidateStreamBatch(batch); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("append stream: %w", err)
	}
	stream := batch.Events[0].Stream
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return session.IngestReceipt{}, fmt.Errorf("begin append stream: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback append stream: %w", rollbackErr))
		}
	}()

	if _, err := tx.Exec(ctx, `
INSERT INTO session_streams (scope_id, source, stream_id, user_id, agent_id, session_id, visibility)
VALUES ($1, $2, $3, '', '', '', $4)
ON CONFLICT (scope_id, source, stream_id) DO NOTHING`,
		scopeID, stream.Source, stream.StreamID, session.VisibilityTeam); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("ensure stream row: %w", err)
	}
	var lastSequence int64
	if err := tx.QueryRow(ctx, `
SELECT last_sequence FROM session_streams
WHERE scope_id = $1 AND source = $2 AND stream_id = $3
FOR UPDATE`, scopeID, stream.Source, stream.StreamID).Scan(&lastSequence); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("lock stream row: %w", err)
	}

	for _, event := range batch.Events {
		metadata, err := json.Marshal(event.Metadata)
		if err != nil {
			return session.IngestReceipt{}, fmt.Errorf("marshal event %q metadata: %w", event.ID, err)
		}
		result, err := tx.Exec(ctx, `
INSERT INTO session_events (
    scope_id, event_id, source, stream_id, user_id, agent_id, session_id,
    author_kind, author_native_id, author_user_id, sequence, kind, event_type,
    content, task_ref, thread_ref, visibility, occurred_at, metadata
) VALUES ($1, $2, $3, $4, '', '', '', $5, $6, $7, $8, $9, $10, $11, '', $12, $13, $14, $15)
ON CONFLICT (scope_id, event_id) DO NOTHING`,
			scopeID, event.ID, stream.Source, stream.StreamID,
			event.Author.Kind, event.Author.NativeID, event.Author.UserID,
			lastSequence+1, event.Kind, event.Type, event.Content,
			event.ThreadRef, event.Visibility, event.OccurredAt, metadata)
		if err != nil {
			return session.IngestReceipt{}, fmt.Errorf("insert stream event %q: %w", event.ID, err)
		}
		if result.RowsAffected() == 0 {
			receipt.Duplicate++
			continue
		}
		lastSequence++
		receipt.Accepted++
	}
	receipt.Cursor = lastSequence

	if _, err := tx.Exec(ctx, `
UPDATE session_streams
SET last_sequence = $4, complete = complete OR $5, updated_at = NOW()
WHERE scope_id = $1 AND source = $2 AND stream_id = $3`,
		scopeID, stream.Source, stream.StreamID, lastSequence, batch.Complete); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("advance stream head: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("commit append stream: %w", err)
	}
	return receipt, nil
}

func (r *SessionRepository) StreamEvents(ctx context.Context, scopeID string, stream session.Stream, after int64, limit int) ([]session.StreamEvent, error) {
	if scopeID == "" || stream.Source == "" || stream.StreamID == "" || limit <= 0 {
		return nil, fmt.Errorf("list stream events: %w", ErrInvalidSessionBatch)
	}
	rows, err := r.pool.Query(ctx, `
SELECT event_id, source, stream_id, author_kind, author_native_id, author_user_id,
       sequence, kind, event_type, content, thread_ref, visibility,
       occurred_at, captured_at, metadata
FROM session_events
WHERE scope_id = $1 AND source = $2 AND stream_id = $3 AND sequence > $4
ORDER BY sequence
LIMIT $5`, scopeID, stream.Source, stream.StreamID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("query stream events: %w", err)
	}
	defer rows.Close()
	events, err := pgx.CollectRows(rows, scanStreamEvent)
	if err != nil {
		return nil, fmt.Errorf("scan stream events: %w", err)
	}
	return events, nil
}

func scanStreamEvent(row pgx.CollectableRow) (session.StreamEvent, error) {
	var event session.StreamEvent
	var metadata []byte
	err := row.Scan(
		&event.ID, &event.Stream.Source, &event.Stream.StreamID,
		&event.Author.Kind, &event.Author.NativeID, &event.Author.UserID,
		&event.Sequence, &event.Kind, &event.Type, &event.Content,
		&event.ThreadRef, &event.Visibility, &event.OccurredAt, &event.CapturedAt, &metadata,
	)
	if err != nil {
		return session.StreamEvent{}, fmt.Errorf("scan stream event columns: %w", err)
	}
	if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
		return session.StreamEvent{}, fmt.Errorf("decode stream event %q metadata: %w", event.ID, err)
	}
	return event, nil
}
```

- [ ] **Step 4: Generalize the legacy write path in `sessions.go`**

In `appendEvents` (`sessions.go:205`), extend the INSERT to populate the evidence columns from the actor mapping:

```go
		stream := session.StreamFromActor(event.Actor)
		author := session.AuthorFromActor(event.Actor)
		result, err := tx.Exec(ctx, `
INSERT INTO session_events (
    scope_id, event_id, source, stream_id, author_kind, author_native_id, author_user_id,
    user_id, agent_id, session_id, sequence, kind, event_type,
    content, task_ref, thread_ref, visibility, occurred_at, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'text', $12, $13, $14, $15, $16, $17, $18)
ON CONFLICT (scope_id, event_id) DO NOTHING`,
			scopeID, event.ID, stream.Source, stream.StreamID,
			author.Kind, author.NativeID, author.UserID,
			event.Actor.UserID, event.Actor.AgentID, event.Actor.SessionID,
			event.Sequence, event.Type, event.Content, event.TaskRef, event.ThreadRef,
			event.Visibility, event.OccurredAt, metadata)
```

In `upsertStream` (`sessions.go:229`), switch the conflict target to the new primary key and populate the stream identity:

```go
	actor := batch.Events[0].Actor
	stream := session.StreamFromActor(actor)
	_, err := tx.Exec(ctx, `
INSERT INTO session_streams (
    scope_id, source, stream_id, user_id, agent_id, session_id, last_sequence, complete
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (scope_id, source, stream_id) DO UPDATE SET
    last_sequence = GREATEST(session_streams.last_sequence, EXCLUDED.last_sequence),
    complete = session_streams.complete OR EXCLUDED.complete,
    updated_at = NOW()`, scopeID, stream.Source, stream.StreamID,
		actor.UserID, actor.AgentID, actor.SessionID, cursor, batch.Complete)
```

- [ ] **Step 5: Run the postgres package tests**

Run: `TEAM_MEMORY_TEST_POSTGRES_DSN=<dsn> go test ./internal/platform/postgres/ -v`
Expected: PASS, including all legacy store, explorer, operations, and pagewiki-consumer tests. If explorer or operations tests reference the dropped unique constraint by name, update them to the new index name from Task 2.

- [ ] **Step 6: Commit (together with Task 2 if Step 4 of Task 2 was red)**

```bash
git add internal/platform/postgres/
git commit -m "feat(postgres): add stream-keyed append path with ingest-assigned sequences"
```

---

### Task 4: Lake and runtime stream observation

**Files:**
- Create: none
- Modify: `internal/sessionlake/lake.go` (Repository interface at `lake.go:14`, add `ObserveStream`)
- Modify: `internal/sessionlake/lake_test.go`
- Modify: `internal/teamnote/contracts.go` (aliases at `contracts.go:14-16`, Runtime interface method list at `contracts.go:86`)
- Modify: `internal/teamnote/runtime/app.go` (next to `ObserveSession` at `app.go:92`)
- Run: `make mocks` (regenerates `internal/teamnote/mocks` for the Runtime interface)

**Interfaces:**
- Consumes: `session.StreamBatch`, `session.ValidateStreamBatch` (Task 1); `AppendStream` (Task 3).
- Produces: `(*Lake) ObserveStream(ctx context.Context, batch session.StreamBatch) (session.IngestReceipt, error)`; `(*App) ObserveStream(ctx context.Context, batch teamnote.StreamBatch) (teamnote.IngestReceipt, error)`; aliases `teamnote.Stream`, `teamnote.StreamEvent`, `teamnote.StreamBatch`; Runtime interface gains `ObserveStream(context.Context, StreamBatch) (IngestReceipt, error)`. Task 5's handler calls the runtime method.

- [ ] **Step 1: Write the failing lake test**

Add to `internal/sessionlake/lake_test.go`, following its existing fake-repository pattern (the file already fakes `Repository`; extend the fake with `AppendStream` recording its inputs):

```go
func TestObserveStreamRequiresScopeAndDelegates(t *testing.T) {
	repository := &fakeRepository{}
	lake := sessionlake.New(repository)
	batch := session.StreamBatch{Events: []session.StreamEvent{{
		ID:         "evt-1",
		Stream:     session.Stream{Source: session.SourceIMChannel, StreamID: "channel-9"},
		Author:     session.Author{Kind: "user", NativeID: "U0AB12"},
		Kind:       session.KindText,
		Type:       "message",
		Content:    "hello",
		Visibility: session.VisibilityTeam,
		OccurredAt: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
	}}}

	if _, err := lake.ObserveStream(context.Background(), batch); !errors.Is(err, session.ErrMissingScope) {
		t.Fatalf("expected missing scope, got %v", err)
	}

	ctx := session.WithScope(context.Background(), "scope-1")
	if _, err := lake.ObserveStream(ctx, batch); err != nil {
		t.Fatalf("expected delegation, got %v", err)
	}
	if repository.appendedStreamScope != "scope-1" {
		t.Fatalf("expected scope forwarded, got %q", repository.appendedStreamScope)
	}
}
```

(Extend the existing fake with `appendedStreamScope string` and an `AppendStream` method that records the scope and returns an empty receipt. Match the fake's actual name in the file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessionlake/ -v`
Expected: FAIL (undefined `ObserveStream`, fake does not satisfy Repository)

- [ ] **Step 3: Implement**

In `internal/sessionlake/lake.go`, extend the Repository interface (`lake.go:14`) with:

```go
	AppendStream(context.Context, string, session.StreamBatch) (session.IngestReceipt, error)
	StreamEvents(context.Context, string, session.Stream, int64, int) ([]session.StreamEvent, error)
```

and add:

```go
// ObserveStream ingests one generalized connector batch. Contract validation
// happens here so every transport rejects identically.
func (l *Lake) ObserveStream(ctx context.Context, batch session.StreamBatch) (session.IngestReceipt, error) {
	scopeID, err := session.ScopeFromContext(ctx)
	if err != nil {
		return session.IngestReceipt{}, fmt.Errorf("observe stream: %w", err)
	}
	if err := session.ValidateStreamBatch(batch); err != nil {
		return session.IngestReceipt{}, fmt.Errorf("observe stream: %w", err)
	}
	receipt, err := l.repository.AppendStream(ctx, scopeID, batch)
	if err != nil {
		return session.IngestReceipt{}, fmt.Errorf("observe stream: %w", err)
	}
	return receipt, nil
}
```

In `internal/teamnote/contracts.go`, add aliases next to the existing ones (`contracts.go:14-16`):

```go
type Stream = session.Stream
type StreamEvent = session.StreamEvent
type StreamBatch = session.StreamBatch
```

and add to the Runtime interface (next to `ObserveSession` at `contracts.go:86`):

```go
	ObserveStream(context.Context, StreamBatch) (IngestReceipt, error)
```

In `internal/teamnote/runtime/app.go`, next to `ObserveSession` (`app.go:92`):

```go
func (a *App) ObserveStream(ctx context.Context, batch teamnote.StreamBatch) (teamnote.IngestReceipt, error) {
	receipt, err := a.lake.ObserveStream(ctx, batch)
	if err != nil {
		return teamnote.IngestReceipt{}, err
	}
	return receipt, nil
}
```

If `App.lake` is an interface type declared in the runtime package, add `ObserveStream` to it with the same signature as the Lake method.

- [ ] **Step 4: Regenerate mocks and run tests**

Run: `make mocks && go test ./internal/sessionlake/ ./internal/teamnote/... -count=1`
Expected: PASS. Compilation of `internal/teamnote/transport/httpapi/...` will fail only if a non-mock fake implements Runtime — extend any such fake (e.g. `recordingRuntime` in `router/register_test.go:72`) with a stub `ObserveStream`.

- [ ] **Step 5: Commit**

```bash
git add internal/sessionlake/ internal/teamnote/
git commit -m "feat(sessionlake): observe generalized evidence streams"
```

---

### Task 5: Generic ingest endpoint `/v1/stream-batches`

**Files:**
- Modify: `idl/team_memory.thrift` (structs near `SessionBatch` at line 22; service method next to `ObserveSession` at line 1076)
- Run: `make generate` (regenerates model + router; creates a stub handler)
- Create/Modify: `internal/teamnote/transport/httpapi/handler/observe_stream.go` (hz generates the stub; fill it)
- Modify: `internal/teamnote/transport/httpapi/handler/mapping.go` (add `streamBatchToDomain`)
- Test: `internal/teamnote/transport/httpapi/handler/observe_stream_test.go`

**Interfaces:**
- Consumes: Runtime `ObserveStream` (Task 4), `session` registry errors (Task 1).
- Produces: `POST /v1/stream-batches` returning the existing `IngestReceipt` wire shape; handler method `(*Handler) ObserveStream(ctx context.Context, c *app.RequestContext)`.

- [ ] **Step 1: Extend the IDL**

Add after the `SessionBatch` struct block in `idl/team_memory.thrift`:

```thrift
struct StreamAuthor {
  1: required string kind (api.body="kind")
  2: required string native_id (api.body="native_id")
  3: optional string user_id (api.body="user_id")
}

struct StreamEvent {
  1: required string id (api.body="id")
  2: required string source (api.body="source")
  3: required string stream_id (api.body="stream_id")
  4: required StreamAuthor author (api.body="author")
  5: required string kind (api.body="kind")
  6: required string type (api.body="type")
  7: required string content (api.body="content")
  8: optional string thread_ref (api.body="thread_ref")
  9: required string visibility (api.body="visibility")
  10: required string occurred_at (api.body="occurred_at")
  11: optional map<string, string> metadata (api.body="metadata")
}

struct StreamBatch {
  1: required list<StreamEvent> events (api.body="events")
  2: required bool complete (api.body="complete")
}
```

Add to the service block next to `ObserveSession` (line 1076):

```thrift
  IngestReceipt ObserveStream(1: StreamBatch request) (api.post="/v1/stream-batches")
```

- [ ] **Step 2: Regenerate and verify the build breaks only at the stub**

Run: `make generate && go build ./...`
Expected: hz writes the model types, registers `POST /v1/stream-batches`, and generates a stub `ObserveStream` handler. The build must pass (stub returns nothing useful yet). Commit nothing yet.

- [ ] **Step 3: Write the failing handler test**

`observe_stream_test.go`, using the same `perform` helper and `onPremHandlerSuite` conventions as `handler_test.go`:

```go
func (s *onPremHandlerSuite) TestObserveStreamAcceptsRegisteredBatch() {
	s.runtime.EXPECT().
		ObserveStream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, batch teamnote.StreamBatch) (teamnote.IngestReceipt, error) {
			s.Require().Len(batch.Events, 1)
			s.Equal("im-channel", batch.Events[0].Stream.Source)
			s.Equal("U0AB12", batch.Events[0].Author.NativeID)
			return teamnote.IngestReceipt{Accepted: 1, Cursor: 1}, nil
		})

	response := perform(s.handler.ObserveStream, http.MethodPost, `{
		"events": [{
			"id": "evt-1",
			"source": "im-channel",
			"stream_id": "channel-9",
			"author": {"kind": "user", "native_id": "U0AB12"},
			"kind": "text",
			"type": "message",
			"content": "ship it Friday",
			"visibility": "team",
			"occurred_at": "2026-07-28T10:00:00Z"
		}],
		"complete": false
	}`, "agent")
	s.Equal(consts.StatusOK, response.Code)
	s.Contains(response.Body.String(), `"accepted":1`)
}

func (s *onPremHandlerSuite) TestObserveStreamRejectsContractViolations() {
	s.runtime.EXPECT().
		ObserveStream(gomock.Any(), gomock.Any()).
		Return(teamnote.IngestReceipt{}, fmt.Errorf("observe stream: %w", session.ErrVisibilityRejected))

	response := perform(s.handler.ObserveStream, http.MethodPost, `{
		"events": [{
			"id": "evt-1",
			"source": "im-channel",
			"stream_id": "channel-9",
			"author": {"kind": "user", "native_id": "U0AB12"},
			"kind": "text",
			"type": "message",
			"content": "x",
			"visibility": "private",
			"occurred_at": "2026-07-28T10:00:00Z"
		}],
		"complete": false
	}`, "agent")
	s.Equal(consts.StatusBadRequest, response.Code)
}
```

Match the `perform` helper's actual signature and the auth fixture value used by other observe tests in the suite (the `"agent"` credential subject).

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/teamnote/transport/httpapi/handler/ -run 'TestOnPremHandlerSuite' -v`
Expected: FAIL (stub handler returns 501/empty; mapping missing)

- [ ] **Step 5: Implement the mapping and handler**

Add to `mapping.go`:

```go
func streamBatchToDomain(request *api.StreamBatch) (teamnote.StreamBatch, error) {
	batch := teamnote.StreamBatch{Complete: request.Complete}
	for _, wire := range request.Events {
		occurredAt, err := time.Parse(time.RFC3339, wire.OccurredAt)
		if err != nil {
			return teamnote.StreamBatch{}, fmt.Errorf("event %q occurred_at: %w", wire.ID, err)
		}
		event := teamnote.StreamEvent{
			ID:         wire.ID,
			Stream:     teamnote.Stream{Source: wire.Source, StreamID: wire.StreamID},
			Author:     session.Author{Kind: wire.Author.Kind, NativeID: wire.Author.NativeID},
			Kind:       wire.Kind,
			Type:       wire.Type,
			Content:    wire.Content,
			Visibility: wire.Visibility,
			OccurredAt: occurredAt.UTC(),
			Metadata:   wire.Metadata,
		}
		if wire.ThreadRef != nil {
			event.ThreadRef = *wire.ThreadRef
		}
		if wire.Author.UserID != nil {
			event.Author.UserID = *wire.Author.UserID
		}
		batch.Events = append(batch.Events, event)
	}
	return batch, nil
}
```

(Mirror how `sessionBatchToDomain` in the same file handles optional pointers and time parsing; keep both consistent. Import `internal/session` if `teamnote` does not re-export `Author`.)

Fill the generated `ObserveStream` handler, mirroring `ObserveSession` (`endpoints.go:17`) exactly — authorization via `h.authorizeAgent(ctx, c, onprem.PermissionObserve)`, operations recording with `operations.KindObservationObserve`, scope resolution via `principal.ScopeID` (generic streams carry no actor to overwrite, so the principal-scope branch reduces to using `principal.ScopeID` directly; the resolver fallback stays the same as `resolveObserveScope`), then:

```go
	receipt, err := h.runtime.ObserveStream(teamnote.WithScope(ctx, scopeID), batch)
	if err != nil {
		if errors.Is(err, session.ErrInvalidStreamBatch) || errors.Is(err, session.ErrUnregisteredValue) ||
			errors.Is(err, session.ErrVisibilityRejected) || errors.Is(err, session.ErrMediaNotEnabled) {
			c.String(consts.StatusBadRequest, "invalid stream batch")
			return
		}
		c.String(consts.StatusUnprocessableEntity, "observe stream")
		return
	}
	c.JSON(consts.StatusOK, ingestReceiptToAPI(receipt))
```

- [ ] **Step 6: Run the handler and full test suite**

Run: `go test ./internal/teamnote/... -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add idl/team_memory.thrift internal/teamnote/transport/httpapi/
git commit -m "feat(httpapi): add the generic /v1/stream-batches ingest endpoint"
```

---

### Task 6: Rename `sessionlake` → `evidencelake`, update docs and architecture boundary

**Files:**
- Rename: `internal/sessionlake/` → `internal/evidencelake/` (`git mv`)
- Modify: every importer of `internal/sessionlake` (find with `grep -rl "internal/sessionlake" --include="*.go" .`; expected: `main.go`, `internal/teamnote/runtime/app.go`, tests)
- Modify: `internal/architecture/dependencies_test.go` (replace `sessionlake` entries with `evidencelake`)
- Rename: `docs/session-lake-processors.md` → `docs/evidence-lake-processors.md`
- Modify: `CONTEXT-MAP.md` (lines 5, 18-19, 30), `internal/session/CONTEXT.md`, `README.md` (session-lake references)

**Interfaces:**
- Consumes: everything above.
- Produces: package `evidencelake` with identical exported API (`New`, `Lake`, `Repository`, `Slice`, `SlicePolicy`, `Observe`, `ObserveStream`, `NextSlice`, `CommitSlice`, `IsCurrent`).

- [ ] **Step 1: Mechanical rename**

```bash
git mv internal/sessionlake internal/evidencelake
grep -rl "sessionlake" --include="*.go" . | xargs sed -i '' 's/sessionlake/evidencelake/g'
git mv docs/session-lake-processors.md docs/evidence-lake-processors.md
grep -rl "session-lake-processors" --include="*.md" . | xargs sed -i '' 's/session-lake-processors/evidence-lake-processors/g'
```

Update the package doc comment in `internal/evidencelake/doc.go` to:

```go
// Package evidencelake implements immutable, ordered evidence stream
// ingestion for any registered source, replay detection, extraction cursors,
// and bounded slice construction.
package evidencelake
```

- [ ] **Step 2: Update the context map and docs**

In `CONTEXT-MAP.md`: line 5 becomes `- [Session](./internal/session/CONTEXT.md) — shared identity contracts and the immutable Evidence Lake (source-agnostic evidence streams).`; lines 18-19 change "Session Lake" to "Evidence Lake". In `internal/session/CONTEXT.md` and `docs/evidence-lake-processors.md`, replace "Session Lake" prose with "Evidence Lake" and note the generalized stream identity `(source, stream_id)` and the connector boundary (connectors are external programs; this repository owns only the ingest contract). In `README.md`, update any "session lake" mentions the same way.

- [ ] **Step 3: Update the architecture test and run everything**

In `internal/architecture/dependencies_test.go`, the sed in Step 1 already rewrote `sessionlake` → `evidencelake`; review the diff to confirm the whitelist edges still describe reality (`evidencelake` imported by `teamnote/runtime`, `main`, importing only `internal/session`).

Run: `go build ./... && go test ./... -count=1 && make fmt`
Expected: build PASS; unit tests PASS; Postgres-backed suites skip without DSN (run them with the DSN if available). NOTE (from project memory): `main` currently has 3 pre-existing lint findings and 2 DB test failures unrelated to this work — compare failures against a clean checkout of the base branch before attributing them to the rename.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor(evidencelake): rename sessionlake and document the evidence boundary"
```

---

## Verification checklist (end of plan)

- [ ] `go build ./... && go test ./... -count=1` green (modulo the documented pre-existing main failures).
- [ ] With a Postgres DSN: `TEAM_MEMORY_TEST_POSTGRES_DSN=<dsn> go test ./internal/platform/postgres/ -v` green.
- [ ] Manual paxm compatibility check: run the service, push one session batch through paxm (or replay a recorded `/v1/session-batches` request) and confirm an identical receipt plus populated `source`/`stream_id` columns.
- [ ] `curl -X POST .../v1/stream-batches` with a registered `im-channel` text event returns `{"accepted":1,...}`; with `"visibility":"private"` returns 400; with `"kind":"audio"` returns 400.
- [ ] `docs/evidence-lake-processors.md`, `CONTEXT-MAP.md`, `internal/session/CONTEXT.md` describe the Evidence Lake boundary.

## Deferred to later plans (do not implement here)

- Stream-keyed extraction cursors/jobs and Team Note consumption of `im-channel` streams (Plan 2 — includes the spec's eval acceptance gates).
- `source + native_id → user_id` mapping table, admin maintenance, ingest-time resolution (Plan 3).
- `BlobStore` port, filesystem adapter, blob upload endpoint, `MediaRef` enforcement replacing `ErrMediaNotEnabled` (Plan 4).
