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
	scope      string
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
	s.scope = "operations-suite-scope"
	s.operations = store.Operations(s.scope)
	s.now = time.Now().UTC().Truncate(time.Microsecond)
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

	s.insertUnextractedEvent(s.scope, uniqueCredentialValue("summary-event"), agentID)
	s.insertUnextractedEvent(uniqueScope("operations-foreign"), uniqueCredentialValue("foreign-event"), agentID)

	summary, err := s.operations.Summary(ctx, operations.TimeFilter{
		From: s.now.Add(-time.Hour), To: s.now.Add(time.Hour), AgentID: agentID,
	}, s.now.Add(time.Hour))
	s.Require().NoError(err)
	s.Equal(int64(1), summary.Extraction.UnextractedEvents,
		"extraction summary must only count session events in the bound scope")
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
RETURNING observation_id`, s.scope, digest[:], string(encodedEnvelope),
		string(encodedTrace), s.now, s.now.Add(24*time.Hour)).Scan(&observationID)
	s.Require().NoError(err)
	var foreignObservationID int64
	err = s.store.Pool().QueryRow(ctx, `
INSERT INTO team_note_recall_observations (
    scope_id, recipient_user_id, recipient_agent_id, recipient_session_id,
    query_digest, token_budget, max_items, envelope, trace, duration_ms, created_at, expires_at
) VALUES ($1, 'user', 'agent', 'session', $2, 128, 3, $3::jsonb, $4::jsonb, 17, $5, $6)
RETURNING observation_id`, uniqueScope("operations-foreign-recall"), digest[:], string(encodedEnvelope),
		string(encodedTrace), s.now, s.now.Add(24*time.Hour)).Scan(&foreignObservationID)
	s.Require().NoError(err)

	_, err = s.operations.GetRecallDiagnostic(ctx, foreignObservationID)
	s.Require().ErrorIs(err, operations.ErrRecallNotFound,
		"recall diagnostics must not resolve observations from other scopes")

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
RETURNING observation_id`, s.scope, digest[:],
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

func (s *operationsStoreSuite) TestAgentStatsAggregatesEventsNotesAndIdentity() {
	ctx := context.Background()
	agentID := uniqueCredentialValue("stats-agent")
	userID := uniqueCredentialValue("stats-user")
	membershipID := uniqueCredentialValue("stats-membership")
	_, err := s.store.Pool().Exec(ctx, `
INSERT INTO onprem_users (user_id, display_name, identity_status, created_at, updated_at)
VALUES ($1, 'Stats Owner', 'active', $2, $2)`, userID, s.now)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO onprem_memberships (membership_id, user_id, role, status, joined_at, updated_at)
VALUES ($1, $2, 'owner', 'active', $3, $3)`, membershipID, userID, s.now)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO onprem_agents (agent_id, owner_membership_id, display_name, agent_type, status, created_at, updated_at)
VALUES ($1, $2, 'Codex Agent', 'codex', 'active', $3, $3)`, agentID, membershipID, s.now)
	s.Require().NoError(err)

	inputTokens, outputTokens := int64(120), int64(45)
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-observe"), Kind: operations.KindObservationObserve,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now, CompletedAt: s.now, InputItems: 2, AcceptedItems: 2,
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-search"), Kind: operations.KindMemorySearch,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now.Add(time.Minute), CompletedAt: s.now.Add(time.Minute),
		ResultItems: 2, DeliveredItems: 1,
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-empty-recall"), Kind: operations.KindTeamNoteRecall,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now.Add(2 * time.Minute), CompletedAt: s.now.Add(2 * time.Minute),
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-extraction"), Kind: operations.KindExtractionRun,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now.Add(3 * time.Minute), CompletedAt: s.now.Add(3 * time.Minute),
		InputTokens: &inputTokens, OutputTokens: &outputTokens,
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-send"), Kind: operations.KindChannelSend,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now.Add(4 * time.Minute), CompletedAt: s.now.Add(4 * time.Minute),
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-accept"), Kind: operations.KindChannelAccept,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now.Add(5 * time.Minute), CompletedAt: s.now.Add(5 * time.Minute),
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-rejected-send"), Kind: operations.KindChannelSend,
		Outcome: operations.OutcomeRejected, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now.Add(6 * time.Minute), CompletedAt: s.now.Add(6 * time.Minute),
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-other"), Kind: operations.KindObservationObserve,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: "stats-outside-agent"},
		StartedAt: s.now.Add(time.Minute), CompletedAt: s.now.Add(time.Minute), InputItems: 9, AcceptedItems: 9,
	})
	s.recordEvent(operations.Event{
		AttemptID: uniqueCredentialValue("stats-stale"), Kind: operations.KindObservationObserve,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now.Add(-2 * time.Hour), CompletedAt: s.now.Add(-2 * time.Hour),
		InputItems: 7, AcceptedItems: 7,
	})

	for index := 0; index < 7; index++ {
		s.insertStatsNote(s.scope, fmt.Sprintf("%s-note-%d", agentID, index), agentID,
			fmt.Sprintf("stats subject %d", index), s.now.Add(time.Duration(index)*time.Minute))
	}
	s.insertStatsNote(s.scope, agentID+"-old-note", agentID, "old subject", s.now.Add(-2*time.Hour))
	s.insertStatsNote(uniqueScope("operations-foreign-stats"), agentID+"-foreign-note", agentID,
		"foreign subject", s.now.Add(10*time.Minute))

	stats, err := s.operations.AgentStats(ctx, operations.TimeFilter{
		From: s.now.Add(-time.Hour), To: s.now.Add(time.Hour),
	})
	s.Require().NoError(err)
	byAgent := make(map[string]operations.AgentStats)
	for _, entry := range stats {
		byAgent[entry.AgentID] = entry
	}
	s.Contains(byAgent, "stats-outside-agent")
	agent, ok := byAgent[agentID]
	s.Require().True(ok)
	s.Equal("Codex Agent", agent.DisplayName)
	s.Equal(int64(1), agent.ObservationRequests)
	s.Equal(int64(2), agent.EventsWritten)
	s.Equal(int64(1), agent.ExtractionRuns)
	s.Equal(int64(120), agent.ExtractionInputTokens)
	s.Equal(int64(45), agent.ExtractionOutputTokens)
	s.Equal(int64(2), agent.RecallRequests)
	s.Equal(int64(1), agent.RecallDeliveredItems)
	s.Equal(int64(1), agent.RecallEmpty)
	s.Equal(int64(1), agent.ChannelSent)
	s.Equal(int64(1), agent.ChannelReceivedAccepted)
	s.Equal(int64(7), agent.NotesAuthored)
	s.Require().NotNil(agent.LastActiveAt)
	s.Equal(s.now.Add(6*time.Minute), agent.LastActiveAt.UTC())
	s.Require().Len(agent.RecentNotes, 5)
	for _, note := range agent.RecentNotes {
		s.NotEqual(agentID+"-foreign-note", note.NoteID,
			"recent notes must not leak team notes from other scopes")
	}
	s.Equal(agentID+"-note-6", agent.RecentNotes[0].NoteID)
	s.Equal("fact", agent.RecentNotes[0].Kind)
	s.Equal("stats subject 6", agent.RecentNotes[0].Subject)
	s.Equal(s.now.Add(6*time.Minute), agent.RecentNotes[0].CreatedAt.UTC())
	s.NotEqual(agentID+"-old-note", agent.RecentNotes[4].NoteID)

	outside := byAgent["stats-outside-agent"]
	s.Empty(outside.DisplayName)
	s.Equal(int64(9), outside.EventsWritten)
	s.Empty(outside.RecentNotes)

	stale, err := s.operations.AgentStats(ctx, operations.TimeFilter{
		From: s.now.Add(-3 * time.Hour), To: s.now.Add(-90 * time.Minute),
	})
	s.Require().NoError(err)
	staleByAgent := make(map[string]operations.AgentStats)
	for _, entry := range stale {
		staleByAgent[entry.AgentID] = entry
	}
	staleAgent, ok := staleByAgent[agentID]
	s.Require().True(ok)
	s.Equal(int64(7), staleAgent.EventsWritten)
	s.Equal(int64(1), staleAgent.NotesAuthored)
	s.Equal(s.now.Add(-2*time.Hour), staleAgent.LastActiveAt.UTC())
}

func (s *operationsStoreSuite) insertStatsNote(
	scope, noteID, agentID, subject string,
	createdAt time.Time,
) {
	_, err := s.store.Pool().Exec(context.Background(), `
INSERT INTO team_notes (
    scope_id, note_id, note_key, kind, subject, body,
    origin_user_id, origin_agent_id, origin_session_id,
    state, current_revision, soft_expires_at, hard_expires_at, created_at, updated_at
) VALUES ($1, $2, $3, 'fact', $4, 'stats body', 'stats-user', $5, 'session',
          'active', 1, $6, $7, $8, $8)`,
		scope, noteID, "key-"+noteID, subject, agentID,
		createdAt.Add(24*time.Hour), createdAt.Add(30*24*time.Hour), createdAt)
	s.Require().NoError(err)
}

func (s *operationsStoreSuite) insertUnextractedEvent(scope, eventID, agentID string) {
	_, err := s.store.Pool().Exec(context.Background(), `
INSERT INTO session_events (
    scope_id, event_id, user_id, agent_id, session_id, sequence,
    event_type, content, occurred_at, captured_at
) VALUES ($1, $2, 'user', $3, 'summary-session', 1, 'message', 'pending extraction', $4, $4)`,
		scope, eventID, agentID, s.now)
	s.Require().NoError(err)
}

// recordScopedObservation writes an observation.observe event under an
// explicit scope, distinct from recordEvent which always defaults to the
// suite's own scope. It's the vehicle for TestReadsAreScopeIsolated: one
// call under the suite's own scope, one under a foreign one.
func (s *operationsStoreSuite) recordScopedObservation(
	ctx context.Context, scopeID, agentID string,
) operations.Event {
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	recorded, err := s.operations.Record(ctx, operations.Event{
		ScopeID: scopeID, AttemptID: attempt, Kind: operations.KindObservationObserve,
		Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
		StartedAt: s.now, CompletedAt: s.now, InputItems: 1, AcceptedItems: 1,
	})
	s.Require().NoError(err)
	return recorded
}

func (s *operationsStoreSuite) recordEvent(event operations.Event) operations.Event {
	if event.ScopeID == "" {
		event.ScopeID = s.scope
	}
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

// The leak this migration closes: two teams' events live in one table, and
// before the scope predicate every team's Operations view counted every other
// team's traffic. Summary and ListEvents must each see only their own scope.
//
// This suite shares one schema across every test with no per-test cleanup,
// and several other tests record observation.observe events near s.now under
// s.scope too — so both events here use the SAME agent id, "own-event", which
// no other test in this file uses. That serves two purposes at once: the
// AgentID filter below keeps the query clean of the suite's other fixtures,
// and because own- and foreign-scope events are otherwise indistinguishable
// (same agent id, same instant), only a working scope filter can tell them
// apart — the AgentID filter can't accidentally do that job for it.
func (s *operationsStoreSuite) TestReadsAreScopeIsolated() {
	ctx := context.Background()
	window := operations.TimeFilter{
		From: s.now.Add(-time.Hour), To: s.now.Add(time.Hour), AgentID: "own-event",
	}

	own := s.recordScopedObservation(ctx, s.scope, "own-event")
	s.recordScopedObservation(ctx, "a-different-team", "own-event")

	summary, err := s.operations.Summary(ctx, window, s.now)
	s.Require().NoError(err)
	s.Equal(int64(1), summary.Observations.Requests)

	events, err := s.operations.ListEvents(ctx, operations.EventFilter{TimeFilter: window, Limit: 50})
	s.Require().NoError(err)
	s.Require().Len(events, 1)
	s.Equal(own.OperationEventID, events[0].OperationEventID)
}

// An event must carry the scope it belongs to. Recording without one is a
// programming error, not a runtime condition — catching it at the boundary
// stops unattributed rows from entering the table at all.
func (s *operationsStoreSuite) TestRecordRejectsAnEventWithoutAScope() {
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	_, err = s.operations.Record(context.Background(), operations.Event{
		AttemptID:   attempt,
		Kind:        operations.KindObservationObserve,
		Outcome:     operations.OutcomeSucceeded,
		Actor:       operations.Actor{Kind: "agent", AgentID: "scope-test-agent"},
		StartedAt:   s.now,
		CompletedAt: s.now,
	})
	s.Require().Error(err)
}

// The recorded row must carry the scope from the EVENT, not the one the store
// happens to be constructed with — the writer is a process-level singleton
// serving every team, so binding to the store's scope would attribute every
// team's traffic to whichever scope the process was wired with.
func (s *operationsStoreSuite) TestRecordPersistsTheEventsOwnScope() {
	ctx := context.Background()
	attempt, err := operations.NewAttemptID()
	s.Require().NoError(err)
	recorded, err := s.operations.Record(ctx, operations.Event{
		ScopeID:     "some-other-team",
		AttemptID:   attempt,
		Kind:        operations.KindObservationObserve,
		Outcome:     operations.OutcomeSucceeded,
		Actor:       operations.Actor{Kind: "agent", AgentID: "scope-test-agent"},
		StartedAt:   s.now,
		CompletedAt: s.now,
	})
	s.Require().NoError(err)

	var stored string
	err = s.store.Pool().QueryRow(ctx,
		`SELECT scope_id FROM onprem_operation_events WHERE operation_event_id = $1`,
		recorded.OperationEventID,
	).Scan(&stored)
	s.Require().NoError(err)
	s.Equal("some-other-team", stored)
}
