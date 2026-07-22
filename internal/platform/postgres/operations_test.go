package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type operationsStoreSuite struct {
	suite.Suite
	store      *postgres.Store
	operations *postgres.OperationsStore
	now        time.Time
	adminPool  *pgxpool.Pool
	schema     string
}

func TestOperationsStoreSuite(t *testing.T) {
	suite.Run(t, new(operationsStoreSuite))
}

func (s *operationsStoreSuite) SetupSuite() {
	ctx := context.Background()
	dsn := testDSN(s.T())
	adminPool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	s.adminPool = adminPool
	s.schema = fmt.Sprintf("operations_%d", time.Now().UnixNano())
	_, err = adminPool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{s.schema}.Sanitize())
	s.Require().NoError(err)
	parsed, err := url.Parse(dsn)
	s.Require().NoError(err)
	query := parsed.Query()
	query.Set("search_path", s.schema+",public")
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, parsed.String())
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(ctx))
	s.store = store
	s.operations = store.Operations()
	s.now = time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
}

func (s *operationsStoreSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
	if s.adminPool != nil {
		_, err := s.adminPool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{s.schema}.Sanitize()+" CASCADE")
		s.NoError(err)
		s.adminPool.Close()
	}
}

func (s *operationsStoreSuite) TestRecordListSummaryAndCursor() {
	ctx := context.Background()
	agentID := uniqueCredentialValue("operations-agent")
	observation := s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("observe-attempt"), Kind: operations.KindObservationObserve,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		SessionID: "session", StartedAt: s.now, CompletedAt: s.now.Add(12 * time.Millisecond), DurationMS: 12,
		InputItems: 3, AcceptedItems: 2, DuplicateItems: 1,
	})
	search := s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("search-attempt"), Kind: operations.KindMemorySearch,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		SessionID: "session", StartedAt: s.now.Add(time.Minute), CompletedAt: s.now.Add(time.Minute + 25*time.Millisecond),
		DurationMS: 25, ResultItems: 2, DeliveredItems: 1, EvidenceItems: 1, HintItems: 1,
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("get-attempt"), Kind: operations.KindMemoryGet,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		SessionID: "session", StartedAt: s.now.Add(-30 * time.Second),
		CompletedAt: s.now.Add(-30*time.Second + 35*time.Millisecond), DurationMS: 35, ResultItems: 1,
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("failed-attempt"), Kind: operations.KindMemoryGet,
		Outcome: operations.OutcomeFailed, Actor: operations.Actor{Kind: "agent", AgentID: "other-agent"},
		StartedAt: s.now.Add(2 * time.Minute), CompletedAt: s.now.Add(2 * time.Minute), ErrorCode: "provider_unavailable",
	})

	summary, err := s.operations.Summary(ctx, operations.TimeFilter{
		From: s.now.Add(-time.Hour), To: s.now.Add(time.Hour), AgentID: agentID,
	}, s.now.Add(time.Hour))
	s.Require().NoError(err)
	s.Equal(int64(1), summary.Observations.Requests)
	s.Equal(int64(3), summary.Observations.InputEvents)
	s.Equal(int64(2), summary.Observations.EventsWritten)
	s.Equal(int64(2), summary.Recalls.Requests)
	s.Equal(int64(1), summary.Recalls.WithEvidence)
	s.Equal(int64(2), summary.Recalls.MemoryHits)
	s.Equal(int64(1), summary.Recalls.TeamNotesDelivered)
	s.Equal(int64(1), summary.Recalls.MemorySearchRequests)
	s.Equal(int64(1), summary.Recalls.MemoryGetRequests)
	s.Zero(summary.Recalls.TeamNoteRecallRequests)
	s.Equal(int64(1), summary.Recalls.EvidenceHits)
	s.Equal(int64(1), summary.Recalls.HintHits)
	s.Zero(summary.Recalls.ReferenceHits)
	s.Zero(summary.Errors)
	s.Equal(int64(2), summary.Latency.SampleCount)
	s.Require().NotNil(summary.Latency.P50MS)
	s.Equal(int64(25), *summary.Latency.P50MS)
	s.Nil(summary.Latency.P95MS)

	events, err := s.operations.ListEvents(ctx, operations.EventFilter{
		TimeFilter: operations.TimeFilter{From: s.now.Add(-time.Hour), To: s.now.Add(time.Hour), AgentID: agentID},
		Limit:      1,
	})
	s.Require().NoError(err)
	s.Require().Len(events, 2)
	s.Equal(search.OperationEventID, events[0].OperationEventID)
	cursor := operations.NextEventCursor(events, 1)
	next, err := s.operations.ListEvents(ctx, operations.EventFilter{
		TimeFilter: operations.TimeFilter{From: s.now.Add(-time.Hour), To: s.now.Add(time.Hour), AgentID: agentID},
		Limit:      1, Cursor: cursor,
	})
	s.Require().NoError(err)
	s.Require().Len(next, 2)
	s.Equal(observation.OperationEventID, next[0].OperationEventID)
	s.Equal(operations.KindMemoryGet, next[1].Kind)
}

func (s *operationsStoreSuite) TestRecallDiagnosticIsAnAllowlistedProjection() {
	ctx := context.Background()
	trace := teamnote.RecallTrace{
		LanesExecuted: []teamnote.RecallLane{teamnote.RecallLaneLexical}, Candidates: 2, FusionKept: 1,
		PlannedNotes: 1, PlannedTokens: 12, DeliveredItems: []string{"secret-note-id"},
		CandidateTraces: []teamnote.RecallCandidateTrace{{
			NoteID: "secret-note-id", Disposition: teamnote.RecallDispositionEvidence,
			FocusedQuery:    "sensitive focused query",
			HardGateResults: []teamnote.RecallHardGateResult{{Gate: "temporal", Passed: false}},
		}},
		Rejections:  []teamnote.RecallRejection{{NoteID: "other-secret", Reason: teamnote.RejectTemporalGate}},
		BudgetDrops: []teamnote.RecallRejection{{NoteID: "budget-secret", Reason: teamnote.RejectTokenBudget}},
	}
	envelope := teamnote.NoteEnvelope{
		Items: []string{"secret recalled text"}, Tokens: 12,
		Decision: teamnote.RecallDecisionSummary{
			EvidenceSufficient: true, ReasonCodes: []teamnote.RecallReasonCode{teamnote.RecallReasonBudgetDrop},
		},
	}
	encodedTrace, err := json.Marshal(trace)
	s.Require().NoError(err)
	encodedEnvelope, err := json.Marshal(envelope)
	s.Require().NoError(err)
	digest := sha256.Sum256([]byte("secret raw query"))
	var observationID int64
	err = s.store.Pool().QueryRow(ctx, `
INSERT INTO team_note_recall_observations (
    scope_id, recipient_user_id, recipient_agent_id, recipient_session_id,
    query_digest, token_budget, max_items, envelope, trace, duration_ms, created_at, expires_at
) VALUES ($1, 'user', 'agent', 'session', $2, 128, 3, $3::jsonb, $4::jsonb, 17, $5, $6)
RETURNING observation_id`, uniqueScope("operations-recall"), digest[:], string(encodedEnvelope),
		string(encodedTrace), s.now, s.now.Add(24*time.Hour)).Scan(&observationID)
	s.Require().NoError(err)

	diagnostic, err := s.operations.GetRecallDiagnostic(ctx, observationID)
	s.Require().NoError(err)
	s.True(diagnostic.EvidenceSufficient)
	s.Equal(int64(2), diagnostic.Candidates)
	s.Equal(int64(1), diagnostic.DeliveredItems)
	s.Equal(int64(1), diagnostic.DispositionCounts["evidence"])
	s.Equal(int64(1), diagnostic.RejectionCounts["temporal_gate"])
	s.Equal(int64(1), diagnostic.BudgetDropCounts["token_budget"])
	s.Equal(int64(1), diagnostic.HardGateFailureCounts["temporal"])
}

func (s *operationsStoreSuite) TestRecallDiagnosticDistinguishesExpiredFromMissing() {
	ctx := context.Background()
	digest := sha256.Sum256([]byte("expired query"))
	var observationID int64
	err := s.store.Pool().QueryRow(ctx, `
INSERT INTO team_note_recall_observations (
    scope_id, recipient_user_id, recipient_agent_id, recipient_session_id,
    query_digest, token_budget, max_items, envelope, trace, duration_ms, created_at, expires_at
) VALUES ($1, 'user', 'agent', 'session', $2, 64, 3, '{}'::jsonb, '{}'::jsonb, 1, $3, $4)
RETURNING observation_id`, uniqueScope("operations-expired-recall"), digest[:],
		time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-time.Hour),
	).Scan(&observationID)
	s.Require().NoError(err)
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("expired-recall-event"), Kind: operations.KindMemorySearch,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent"},
		StartedAt: s.now, CompletedAt: s.now, DetailKind: "recall_observation",
		DetailID: fmt.Sprintf("%d", observationID),
	})

	_, err = s.operations.GetRecallDiagnostic(ctx, observationID)
	s.Require().ErrorIs(err, operations.ErrRecallExpired)
	_, err = s.store.Pool().Exec(ctx, `DELETE FROM team_note_recall_observations WHERE observation_id = $1`, observationID)
	s.Require().NoError(err)
	_, err = s.operations.GetRecallDiagnostic(ctx, observationID)
	s.Require().ErrorIs(err, operations.ErrRecallExpired)
	_, err = s.operations.GetRecallDiagnostic(ctx, observationID+1_000_000)
	s.Require().ErrorIs(err, operations.ErrRecallNotFound)
}

func (s *operationsStoreSuite) TestRetentionDeletesExpiredRowsInBoundedBatches() {
	ctx := context.Background()
	oldAttempt := uniqueCredentialValue("old-operation")
	currentAttempt := uniqueCredentialValue("current-operation")
	s.recordEvent(operations.Event{
		AttemptID: oldAttempt, Kind: operations.KindMemoryGet, Outcome: operations.OutcomeSucceeded,
		Actor: operations.Actor{Kind: "agent"}, StartedAt: s.now.Add(-10 * 24 * time.Hour),
		CompletedAt: s.now.Add(-10 * 24 * time.Hour),
	})
	s.recordEvent(operations.Event{
		AttemptID: currentAttempt, Kind: operations.KindMemoryGet, Outcome: operations.OutcomeSucceeded,
		Actor: operations.Actor{Kind: "agent"}, StartedAt: s.now, CompletedAt: s.now,
	})
	var oldSnapshotID int64
	err := s.store.Pool().QueryRow(ctx, `
INSERT INTO onprem_storage_snapshots (
    schema_version, captured_at, status, warning_codes,
    database_physical_bytes, other_physical_bytes, components
) VALUES (1, $1, 'complete', '{}', 0, 0, '[]'::jsonb)
RETURNING snapshot_id`, s.now.Add(-100*24*time.Hour)).Scan(&oldSnapshotID)
	s.Require().NoError(err)

	deletedEvents, deletedSnapshots, err := s.operations.DeleteBefore(
		ctx, s.now.Add(-7*24*time.Hour), s.now.Add(-90*24*time.Hour),
	)
	s.Require().NoError(err)
	s.Positive(deletedEvents)
	s.Positive(deletedSnapshots)
	s.operationExists(oldAttempt, false)
	s.operationExists(currentAttempt, true)
	var snapshotExists bool
	err = s.store.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM onprem_storage_snapshots WHERE snapshot_id = $1)`, oldSnapshotID,
	).Scan(&snapshotExists)
	s.Require().NoError(err)
	s.False(snapshotExists)
}

func (s *operationsStoreSuite) TestStorageSnapshotIncludesSessionLakeAndTeamMemory() {
	ctx := context.Background()
	actor := teamnote.Actor{UserID: "owner", AgentID: uniqueCredentialValue("storage-agent"), SessionID: "session"}
	_, err := s.store.Sessions().AppendSession(ctx, uniqueScope("operations-storage"), teamnote.SessionBatch{
		Events: []teamnote.SessionEvent{{
			ID: uniqueCredentialValue("storage-event"), Actor: actor, Sequence: 1,
			Type: "message", Content: "storage accounting payload", OccurredAt: s.now,
		}},
	})
	s.Require().NoError(err)

	snapshot, err := s.operations.CaptureStorage(ctx, s.now.Add(3*time.Hour))
	s.Require().NoError(err)
	s.Positive(snapshot.SnapshotID)
	s.Positive(snapshot.DatabasePhysicalBytes)
	components := make(map[string]operations.StorageComponent)
	for _, component := range snapshot.Components {
		components[component.Component] = component
	}
	s.Contains(components, "session_lake")
	s.Contains(components, "team_memory")
	s.GreaterOrEqual(components["session_lake"].Counts["events"], int64(1))
	s.Positive(components["session_lake"].LogicalBytes)
	s.Equal(
		components["session_lake"].Counts["content_bytes"]+components["session_lake"].Counts["metadata_bytes"],
		components["session_lake"].LogicalBytes,
	)
	s.Positive(components["session_lake"].PhysicalBytes)

	latest, err := s.operations.LatestStorage(ctx)
	s.Require().NoError(err)
	s.Equal(snapshot.SnapshotID, latest.SnapshotID)
	history, err := s.operations.ListStorage(ctx, operations.StorageFilter{
		From: s.now, To: s.now.Add(4 * time.Hour), Limit: 10,
	})
	s.Require().NoError(err)
	s.NotEmpty(history)
}

func (s *operationsStoreSuite) TestStorageSnapshotPersistsPartialResultAfterComponentTimeout() {
	ctx := context.Background()
	lockTx, err := s.store.Pool().Begin(ctx)
	s.Require().NoError(err)
	defer func() {
		rollbackErr := lockTx.Rollback(ctx)
		if !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			s.NoError(rollbackErr)
		}
	}()
	_, err = lockTx.Exec(ctx, `LOCK TABLE session_events IN ACCESS EXCLUSIVE MODE`)
	s.Require().NoError(err)

	captureContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	snapshot, captureErr := s.operations.CaptureStorage(captureContext, s.now.Add(10*time.Hour))
	cancel()
	s.Require().NoError(captureErr)
	s.Equal("partial", snapshot.Status)
	s.Contains(snapshot.WarningCodes, "session_lake_logical_unavailable")
	s.Positive(snapshot.SnapshotID)

	s.Require().NoError(lockTx.Rollback(ctx))
	latest, err := s.operations.LatestStorage(ctx)
	s.Require().NoError(err)
	s.Equal(snapshot.SnapshotID, latest.SnapshotID)
}

func (s *operationsStoreSuite) recordEvent(event operations.Event) operations.Event {
	recorded, err := s.operations.Record(context.Background(), event)
	s.Require().NoError(err)
	return recorded
}

func (s *operationsStoreSuite) operationExists(attemptID string, expected bool) {
	var exists bool
	err := s.store.Pool().QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM onprem_operation_events WHERE attempt_id = $1)`, attemptID,
	).Scan(&exists)
	s.Require().NoError(err)
	s.Equal(expected, exists)
}
