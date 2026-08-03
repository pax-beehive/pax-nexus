package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type explorerStoreSuite struct {
	suite.Suite
	store     *postgres.Store
	explorer  *postgres.ExplorerStore
	adminPool *pgxpool.Pool
	schema    string
}

func TestExplorerStoreSuite(t *testing.T) {
	suite.Run(t, new(explorerStoreSuite))
}

func (s *explorerStoreSuite) SetupSuite() {
	ctx := context.Background()
	dsn := testDSN(s.T())
	adminPool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	s.adminPool = adminPool
	s.schema = fmt.Sprintf("explorer_%d", time.Now().UnixNano())
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
	s.explorer = store.Explorer(onprem.LocalScopeID)
}

func (s *explorerStoreSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
	if s.adminPool != nil {
		_, err := s.adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+pgx.Identifier{s.schema}.Sanitize()+" CASCADE",
		)
		s.NoError(err)
		s.adminPool.Close()
	}
}

func (s *explorerStoreSuite) SetupTest() {
	_, err := s.store.Pool().Exec(context.Background(), `
TRUNCATE team_note_recall_observations, note_deliveries, note_evidence,
         note_revisions, note_candidates, extraction_runs, session_events,
         team_notes, onprem_channel_envelopes CASCADE`)
	s.Require().NoError(err)
}

func (s *explorerStoreSuite) TestListTeamNotesFiltersScopeAndPaginates() {
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond)
	s.insertNote(onprem.LocalScopeID, "note-new", "blocker", "Release blocker", "Waiting for approval", "codex", base)
	s.insertNote(onprem.LocalScopeID, "note-old", "blocker", "Release owner", "Todd owns release", "claude", base.Add(-time.Minute))
	s.insertNote("other-scope", "note-hidden", "blocker", "Release secret", "must not leak", "codex", base.Add(time.Minute))

	first, err := s.explorer.ListTeamNotes(ctx, explorer.TeamNoteFilter{
		Query: "release", Kind: "blocker", Limit: 1,
	})

	s.Require().NoError(err)
	s.Require().Len(first, 2)
	s.Equal("note-new", first[0].NoteID)
	s.NotEmpty(explorer.NextTeamNoteCursor(first, 1))

	second, err := s.explorer.ListTeamNotes(ctx, explorer.TeamNoteFilter{
		Query: "release", Kind: "blocker", Limit: 1,
		Cursor: explorer.NextTeamNoteCursor(first, 1),
	})
	s.Require().NoError(err)
	s.Require().Len(second, 1)
	s.Equal("note-old", second[0].NoteID)
}

func (s *explorerStoreSuite) TestExplorerScopeComesFromAccessor() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.insertNote(onprem.LocalScopeID, "note-local", "blocker", "Release blocker", "Waiting for approval", "codex", now)
	s.insertNote("other-scope", "note-other", "blocker", "Release blocker", "Waiting for approval", "codex", now)

	other := s.store.Explorer("other-scope")
	notes, err := other.ListTeamNotes(ctx, explorer.TeamNoteFilter{Kind: "blocker", Limit: 10})
	s.Require().NoError(err)
	s.Require().Len(notes, 1)
	s.Equal("note-other", notes[0].NoteID)
}

func (s *explorerStoreSuite) TestGetTeamNoteReturnsSourceToRecallChain() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.insertNote(onprem.LocalScopeID, "note-detail", "blocker", "Release blocker", "Waiting for approval", "codex", now)
	s.insertNote(onprem.LocalScopeID, "note-related-out", "fact", "Release owner", "Todd", "claude", now)
	s.insertNote(onprem.LocalScopeID, "note-related-in", "status", "Deployment status", "Blocked", "claude", now)
	_, err := s.store.Pool().Exec(ctx, `
UPDATE team_notes
SET related_subjects = CASE note_id
    WHEN 'note-detail' THEN ARRAY['Release owner']
    WHEN 'note-related-in' THEN ARRAY['Release blocker']
    ELSE related_subjects
END
WHERE scope_id = $1 AND note_id IN ('note-detail', 'note-related-in')`,
		onprem.LocalScopeID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO session_events (
    scope_id, event_id, user_id, agent_id, session_id, sequence,
    event_type, content, task_ref, occurred_at, captured_at
) VALUES ($1, 'event-detail', 'user', 'codex', 'session-source', 1,
          'message', 'The release is waiting for approval', 'release', $2, $2)`,
		onprem.LocalScopeID, now)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO extraction_runs (
    scope_id, run_id, user_id, agent_id, session_id, from_sequence, to_sequence,
    input_checksum, model, prompt_version, status, input_tokens, output_tokens,
    completed_at
) VALUES ($1, 'run-detail', 'user', 'codex', 'session-source', 1, 1,
          'checksum-detail', 'extractor', 'prompt-v1', 'completed', 20, 5, $2)`,
		onprem.LocalScopeID, now)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO note_candidates (
    scope_id, candidate_id, run_id, action, kind, subject, body,
    origin_user_id, origin_agent_id, origin_session_id, evidence_event_ids,
    admission_status
) VALUES ($1, 'candidate-detail', 'run-detail', 'create', 'blocker',
          'Release blocker', 'Waiting for approval', 'user', 'codex',
          'session-source', ARRAY['event-detail'], 'admitted')`,
		onprem.LocalScopeID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO note_revisions (
    scope_id, note_id, revision, candidate_id, operation, body, created_at
) VALUES ($1, 'note-detail', 1, 'candidate-detail', 'create',
          'Waiting for approval', $2)`,
		onprem.LocalScopeID, now)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO note_evidence (scope_id, note_id, revision, event_id)
VALUES ($1, 'note-detail', 1, 'event-detail')`,
		onprem.LocalScopeID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO note_deliveries (
    scope_id, note_id, revision, recipient_user_id, recipient_agent_id,
    recipient_session_id, delivered_at, context_tokens
) VALUES ($1, 'note-detail', 1, 'consumer-user', 'claude',
          'session-consumer', $2, 12)`, onprem.LocalScopeID, now)
	s.Require().NoError(err)
	trace := teamnote.RecallTrace{
		CandidateTraces: []teamnote.RecallCandidateTrace{{
			NoteID: "note-detail", Disposition: teamnote.RecallDispositionEvidence,
			HardGateResults: []teamnote.RecallHardGateResult{{Gate: "temporal", Passed: true}},
		}},
		DeliveredItems: []string{"note-detail"},
	}
	encodedTrace, err := json.Marshal(trace)
	s.Require().NoError(err)
	digest := sha256.Sum256([]byte("raw query must not be returned"))
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO team_note_recall_observations (
    scope_id, recipient_user_id, recipient_agent_id, recipient_session_id,
    query_digest, token_budget, envelope, trace, created_at, expires_at
) VALUES ($1, 'consumer-user', 'claude', 'session-consumer', $2, 256,
          '{}'::jsonb, $3::jsonb, $4, $5)`,
		onprem.LocalScopeID, digest[:], string(encodedTrace), now, now.Add(time.Hour))
	s.Require().NoError(err)
	revisionOnlyTrace, err := json.Marshal(teamnote.RecallTrace{
		DeliveredItems: []string{"note-detail@1"},
	})
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO team_note_recall_observations (
    scope_id, recipient_user_id, recipient_agent_id, recipient_session_id,
    query_digest, token_budget, envelope, trace, created_at, expires_at
) VALUES ($1, 'consumer-user', 'pi', 'session-revision-consumer', $2, 256,
          '{}'::jsonb, $3::jsonb, $4, $5)`,
		onprem.LocalScopeID, digest[:], string(revisionOnlyTrace),
		now.Add(-time.Second), now.Add(time.Hour))
	s.Require().NoError(err)

	heldConnections := make([]*pgxpool.Conn, 0, s.store.Pool().Config().MaxConns-1)
	for range s.store.Pool().Config().MaxConns - 1 {
		connection, acquireErr := s.store.Pool().Acquire(ctx)
		s.Require().NoError(acquireErr)
		heldConnections = append(heldConnections, connection)
	}
	defer func() {
		for index := range heldConnections {
			heldConnections[index].Release()
		}
	}()

	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	detail, err := s.explorer.GetTeamNote(requestCtx, "note-detail")

	s.Require().NoError(err)
	s.Equal("Waiting for approval", detail.Note.Body)
	s.Require().Len(detail.RelatedNotes, 2)
	s.Require().Len(detail.Revisions, 1)
	s.Equal("run-detail", detail.Revisions[0].Extraction.RunID)
	s.Equal("candidate-detail", detail.Revisions[0].Candidate.CandidateID)
	s.Equal("Release blocker", detail.Revisions[0].Candidate.Subject)
	s.Require().Len(detail.Revisions[0].Evidence, 1)
	s.Equal("The release is waiting for approval", detail.Revisions[0].Evidence[0].Content)
	s.Require().Len(detail.Revisions[0].Deliveries, 1)
	s.Require().Len(detail.RecallObservations, 2)
	s.True(detail.RecallObservations[0].Delivered)
	s.Equal("evidence", detail.RecallObservations[0].Disposition)
}

func (s *explorerStoreSuite) TestGetExtractionDiagnosticDistinguishesCandidateOutcomes() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.insertNote(onprem.LocalScopeID, "note-extraction", "status", "Release status", "Ready", "codex", now)
	_, err := s.store.Pool().Exec(ctx, `
INSERT INTO session_events (
    scope_id, event_id, user_id, agent_id, session_id, sequence,
    event_type, content, occurred_at, captured_at
) VALUES ($1, 'event-extraction', 'user', 'codex', 'session-extraction', 1,
          'message', 'Release is ready', $2, $2)`,
		onprem.LocalScopeID, now)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO extraction_runs (
    scope_id, run_id, user_id, agent_id, session_id, from_sequence, to_sequence,
    input_checksum, model, prompt_version, status, input_tokens, output_tokens,
    completed_at
) VALUES ($1, 'run-extraction', 'user', 'codex', 'session-extraction', 1, 1,
          'checksum-extraction', 'extractor', 'prompt-v1', 'completed', 30, 7, $2)`,
		onprem.LocalScopeID, now)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO note_candidates (
    scope_id, candidate_id, run_id, action, kind, subject, body,
    origin_user_id, origin_agent_id, origin_session_id, evidence_event_ids,
    admission_status
) VALUES
    ($1, 'candidate-admitted', 'run-extraction', 'create', 'status',
     'Release status', 'Ready', 'user', 'codex', 'session-extraction',
     ARRAY['event-extraction'], 'admitted'),
    ($1, 'candidate-rejected', 'run-extraction', 'create', 'status',
     'Unrelated status', 'Rejected', 'user', 'codex', 'session-extraction',
     ARRAY['event-extraction'], 'rejected'),
    ($1, 'candidate-unsafe', 'run-extraction', 'create', 'status',
     'Unsafe status', 'Rejected', 'user', 'codex', 'session-extraction',
     ARRAY['event-extraction'], 'rejected')`,
		onprem.LocalScopeID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
UPDATE note_candidates
SET rejection_reason = CASE candidate_id
    WHEN 'candidate-rejected' THEN 'missing_evidence'
    ELSE 'credential_sk_live_abc123'
END
WHERE scope_id = $1 AND candidate_id IN ('candidate-rejected', 'candidate-unsafe')`,
		onprem.LocalScopeID)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
INSERT INTO note_revisions (
    scope_id, note_id, revision, candidate_id, operation, body, created_at
) VALUES ($1, 'note-extraction', 1, 'candidate-admitted', 'create', 'Ready', $2)`,
		onprem.LocalScopeID, now,
	)
	s.Require().NoError(err)

	diagnostic, err := s.explorer.GetExtractionDiagnostic(ctx, "run-extraction")

	s.Require().NoError(err)
	s.Equal("completed", diagnostic.Run.Status)
	s.Require().Len(diagnostic.SourceEvents, 1)
	s.Require().Len(diagnostic.Candidates, 3)
	s.Equal("admitted", diagnostic.Candidates[0].AdmissionStatus)
	s.Equal("note-extraction", diagnostic.Candidates[0].ResultingNoteID)
	s.Equal("rejected", diagnostic.Candidates[1].AdmissionStatus)
	s.Equal("missing_evidence", diagnostic.Candidates[1].RejectionReason)
	s.Equal("candidate_rejected", diagnostic.Candidates[2].RejectionReason)
	s.Require().Len(diagnostic.ResultingNotes, 1)
}

func (s *explorerStoreSuite) TestGetChannelDiagnosticProjectsSafeCapsuleFields() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	payload := `{
		"schema_version": "paxl.envelope_payload.knowledge_capsule.v2",
		"capsule": {
			"capsule_id": "capsule-1",
			"source_session_id": "session-source",
			"source_agent": "codex",
			"keyword": "release",
			"title": "Release status",
			"summary": "Ready to ship",
			"content": "The release is ready to ship.",
			"status": "ready",
			"truncated": false,
			"original_estimated_chars": 33
		},
		"route": {"match_type": "agent", "target_agent": "claude"}
	}`
	_, err := s.store.Pool().Exec(ctx, `
INSERT INTO onprem_channel_envelopes (
    envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
    payload_type, payload_json, message, idempotency_key, status, created_at
) VALUES (
    'envelope-safe', 'user-source', 'codex', 'user-target', 'claude',
    'knowledge_capsule', $1::jsonb, 'Please review', 'secret-key', 'pending', $2
)`, payload, now)
	s.Require().NoError(err)

	diagnostic, err := s.explorer.GetChannelDiagnostic(ctx, "envelope-safe")

	s.Require().NoError(err)
	s.Equal("decoded", diagnostic.PayloadStatus)
	s.Equal("codex", diagnostic.FromAgentID)
	s.Equal("claude", diagnostic.ToAgentID)
	s.Equal("Release status", diagnostic.Capsule.Title)
	s.Equal("The release is ready to ship.", diagnostic.Capsule.Content)
	s.Equal("agent", diagnostic.Capsule.RouteMatchType)
}

func (s *explorerStoreSuite) TestGetChannelDiagnosticDegradesUnknownPayloadWithoutRawJSON() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := s.store.Pool().Exec(ctx, `
INSERT INTO onprem_channel_envelopes (
    envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
    payload_type, payload_json, message, idempotency_key, status, created_at
) VALUES (
    'envelope-unknown', 'user-source', 'codex', 'user-target', 'claude',
    'knowledge_capsule', '{"schema_version":"future","secret":"must-not-leak"}'::jsonb,
    '', 'secret-key-2', 'pending', $1
)`, now)
	s.Require().NoError(err)

	diagnostic, err := s.explorer.GetChannelDiagnostic(ctx, "envelope-unknown")

	s.Require().NoError(err)
	s.Equal("unavailable", diagnostic.PayloadStatus)
	s.Empty(diagnostic.Capsule)
}

func (s *explorerStoreSuite) insertNote(
	scopeID string,
	noteID string,
	kind string,
	subject string,
	body string,
	agentID string,
	updatedAt time.Time,
) {
	_, err := s.store.Pool().Exec(context.Background(), `
INSERT INTO team_notes (
    scope_id, note_id, note_key, kind, subject, body, origin_user_id,
    origin_agent_id, origin_session_id, state, current_revision,
    soft_expires_at, hard_expires_at, created_at, updated_at
) VALUES ($1, $2, $2, $3, $4, $5, 'user', $6, 'session', 'active', 1,
          $7, $8, $9, $9)`,
		scopeID, noteID, kind, subject, body, agentID,
		updatedAt.Add(time.Hour), updatedAt.Add(24*time.Hour), updatedAt,
	)
	s.Require().NoError(err)
}
