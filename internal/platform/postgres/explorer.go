package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type ExplorerStore struct {
	pool    *pgxpool.Pool
	scopeID string
}

func (s *ExplorerStore) ListTeamNotes(
	ctx context.Context,
	filter explorer.TeamNoteFilter,
) ([]explorer.TeamNoteSummary, error) {
	cursorTime, cursorNoteID, err := explorer.DecodeTeamNoteCursor(filter.Cursor)
	if err != nil {
		return nil, err
	}
	var cursorValue any
	if !cursorTime.IsZero() {
		cursorValue = cursorTime
	}
	rows, err := s.pool.Query(ctx, `
SELECT note_id, kind, subject, state, task_ref, thread_ref, origin_agent_id,
       audience_agent_ids, current_revision, created_at, updated_at,
       soft_expires_at, hard_expires_at
FROM team_notes
WHERE scope_id = $1
  AND ($2 = '' OR kind = $2)
  AND ($3 = '' OR state = $3)
  AND ($4 = '' OR origin_agent_id = $4)
  AND ($5 = '' OR task_ref = $5)
  AND ($6 = '' OR thread_ref = $6)
  AND (
      $7 = ''
      OR subject ILIKE '%' || $7 || '%'
      OR body ILIKE '%' || $7 || '%'
  )
  AND (
      $8::timestamptz IS NULL
      OR (updated_at, note_id) < ($8::timestamptz, $9)
  )
ORDER BY updated_at DESC, note_id DESC
LIMIT $10`,
		s.scopeID, filter.Kind, filter.State, filter.AgentID, filter.TaskRef,
		filter.ThreadRef, escapeLike(filter.Query), cursorValue, cursorNoteID, filter.Limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("list postgres explorer team notes: %w", err)
	}
	defer rows.Close()
	notes := make([]explorer.TeamNoteSummary, 0)
	for rows.Next() {
		var note explorer.TeamNoteSummary
		if err := rows.Scan(
			&note.NoteID, &note.Kind, &note.Subject, &note.State,
			&note.TaskRef, &note.ThreadRef, &note.OriginAgentID,
			&note.AudienceAgentIDs, &note.Revision, &note.CreatedAt,
			&note.UpdatedAt, &note.SoftExpiresAt, &note.HardExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan postgres explorer team note: %w", err)
		}
		note.CreatedAt = note.CreatedAt.UTC()
		note.UpdatedAt = note.UpdatedAt.UTC()
		note.SoftExpiresAt = note.SoftExpiresAt.UTC()
		note.HardExpiresAt = note.HardExpiresAt.UTC()
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres explorer team notes: %w", err)
	}
	return notes, nil
}

func (s *ExplorerStore) GetTeamNote(
	ctx context.Context,
	noteID string,
) (explorer.TeamNoteDetail, error) {
	note, err := s.getTeamNote(ctx, noteID)
	if err != nil {
		return explorer.TeamNoteDetail{}, err
	}
	related, err := s.relatedTeamNotes(ctx, note.NoteID, note.Subject, note.RelatedSubjects)
	if err != nil {
		return explorer.TeamNoteDetail{}, err
	}
	revisions, err := s.teamNoteRevisions(ctx, noteID)
	if err != nil {
		return explorer.TeamNoteDetail{}, err
	}
	recalls, err := s.teamNoteRecallUses(ctx, noteID)
	if err != nil {
		return explorer.TeamNoteDetail{}, err
	}
	return explorer.TeamNoteDetail{
		Note: note, RelatedNotes: related, Revisions: revisions, RecallObservations: recalls,
	}, nil
}

func (s *ExplorerStore) getTeamNote(ctx context.Context, noteID string) (explorer.TeamNote, error) {
	var note explorer.TeamNote
	err := s.pool.QueryRow(ctx, `
SELECT note_id, kind, subject, state, task_ref, thread_ref, origin_agent_id,
       audience_agent_ids, current_revision, created_at, updated_at,
       soft_expires_at, hard_expires_at, body, origin_user_id,
       origin_session_id, related_subjects, valid_at, invalid_at,
       source_occurred_at
FROM team_notes
WHERE scope_id = $1 AND note_id = $2`, s.scopeID, noteID).Scan(
		&note.NoteID, &note.Kind, &note.Subject, &note.State,
		&note.TaskRef, &note.ThreadRef, &note.OriginAgentID,
		&note.AudienceAgentIDs, &note.Revision, &note.CreatedAt,
		&note.UpdatedAt, &note.SoftExpiresAt, &note.HardExpiresAt,
		&note.Body, &note.OriginUserID, &note.OriginSessionID,
		&note.RelatedSubjects, &note.ValidAt, &note.InvalidAt,
		&note.SourceOccurredAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return explorer.TeamNote{}, explorer.ErrNotFound
	}
	if err != nil {
		return explorer.TeamNote{}, fmt.Errorf("get postgres explorer team note: %w", err)
	}
	return note, nil
}

func (s *ExplorerStore) relatedTeamNotes(
	ctx context.Context,
	noteID string,
	subject string,
	subjects []string,
) ([]explorer.TeamNoteSummary, error) {
	rows, err := s.pool.Query(ctx, `
SELECT note_id, kind, subject, state, task_ref, thread_ref, origin_agent_id,
       audience_agent_ids, current_revision, created_at, updated_at,
       soft_expires_at, hard_expires_at
FROM team_notes
WHERE scope_id = $1 AND note_id <> $2
  AND (subject = ANY($3) OR $4 = ANY(related_subjects))
ORDER BY updated_at DESC, note_id DESC`, s.scopeID, noteID, subjects, subject)
	if err != nil {
		return nil, fmt.Errorf("list related postgres explorer team notes: %w", err)
	}
	defer rows.Close()
	result := make([]explorer.TeamNoteSummary, 0)
	for rows.Next() {
		note, err := scanTeamNoteSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan related postgres explorer team note: %w", err)
		}
		result = append(result, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate related postgres explorer team notes: %w", err)
	}
	return result, nil
}

func (s *ExplorerStore) teamNoteRevisions(
	ctx context.Context,
	noteID string,
) ([]explorer.Revision, error) {
	rows, err := s.pool.Query(ctx, `
SELECT revision.revision, revision.candidate_id, revision.operation,
       revision.body, revision.related_subjects, revision.valid_at,
       revision.invalid_at, revision.created_at, revision.expired_at,
       candidate.action, candidate.kind, candidate.subject, candidate.body,
       candidate.task_ref, candidate.thread_ref, candidate.origin_agent_id,
       candidate.evidence_event_ids, candidate.admission_status,
       candidate.rejection_reason, candidate.created_at,
       run.run_id, run.user_id, run.agent_id, run.session_id,
       run.from_sequence, run.to_sequence, run.model, run.prompt_version,
       run.status, run.input_tokens, run.output_tokens, run.error,
       run.created_at, run.completed_at
FROM note_revisions revision
JOIN note_candidates candidate
  ON candidate.scope_id = revision.scope_id
 AND candidate.candidate_id = revision.candidate_id
JOIN extraction_runs run
  ON run.scope_id = candidate.scope_id AND run.run_id = candidate.run_id
WHERE revision.scope_id = $1 AND revision.note_id = $2
ORDER BY revision.revision DESC`, s.scopeID, noteID)
	if err != nil {
		return nil, fmt.Errorf("list postgres explorer note revisions: %w", err)
	}
	revisions := make([]explorer.Revision, 0)
	for rows.Next() {
		var revision explorer.Revision
		var storedError string
		var rejectionReason string
		if err := rows.Scan(
			&revision.Revision, &revision.CandidateID, &revision.Operation,
			&revision.Body, &revision.RelatedSubjects, &revision.ValidAt,
			&revision.InvalidAt, &revision.CreatedAt, &revision.ExpiredAt,
			&revision.Candidate.Action, &revision.Candidate.Kind,
			&revision.Candidate.Subject, &revision.Candidate.Body,
			&revision.Candidate.TaskRef, &revision.Candidate.ThreadRef,
			&revision.Candidate.OriginAgentID, &revision.Candidate.EvidenceEventIDs,
			&revision.Candidate.AdmissionStatus, &rejectionReason,
			&revision.Candidate.CreatedAt,
			&revision.Extraction.RunID, &revision.Extraction.UserID,
			&revision.Extraction.AgentID, &revision.Extraction.SessionID,
			&revision.Extraction.FromSequence, &revision.Extraction.ToSequence,
			&revision.Extraction.Model, &revision.Extraction.PromptVersion,
			&revision.Extraction.Status, &revision.Extraction.InputTokens,
			&revision.Extraction.OutputTokens, &storedError,
			&revision.Extraction.CreatedAt, &revision.Extraction.CompletedAt,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan postgres explorer note revision: %w", err)
		}
		revision.Candidate.CandidateID = revision.CandidateID
		revision.Candidate.RejectionReason = safeCandidateRejectionReason(rejectionReason)
		revision.Candidate.ResultingNoteID = noteID
		revision.Extraction.ErrorCode = extractionErrorCode(revision.Extraction.Status, storedError)
		revisions = append(revisions, revision)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres explorer note revisions: %w", err)
	}
	if len(revisions) == 0 {
		return revisions, nil
	}
	revisionNumbers := make([]int32, len(revisions))
	for index := range revisions {
		revisionNumbers[index] = int32(revisions[index].Revision)
	}
	evidenceByRevision, err := s.revisionEvidence(ctx, noteID, revisionNumbers)
	if err != nil {
		return nil, err
	}
	deliveriesByRevision, err := s.revisionDeliveries(ctx, noteID, revisionNumbers)
	if err != nil {
		return nil, err
	}
	for index := range revisions {
		revision := &revisions[index]
		revision.Evidence = evidenceByRevision[revision.Revision]
		if revision.Evidence == nil {
			revision.Evidence = []explorer.SourceEvent{}
		}
		revision.Deliveries = deliveriesByRevision[revision.Revision]
		if revision.Deliveries == nil {
			revision.Deliveries = []explorer.Delivery{}
		}
	}
	return revisions, nil
}

// revisionEvidence loads the evidence events for every requested revision in
// one query and groups them per revision, preserving the per-revision
// occurred_at, sequence, event_id ordering the per-revision queries used.
func (s *ExplorerStore) revisionEvidence(
	ctx context.Context,
	noteID string,
	revisions []int32,
) (map[int][]explorer.SourceEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT evidence.revision,
       event.event_id, event.user_id, event.agent_id, event.session_id,
       event.sequence, event.event_type, event.content, event.task_ref,
       event.thread_ref, event.visibility, event.occurred_at,
       event.captured_at, event.extracted_at
FROM note_evidence evidence
JOIN session_events event
  ON event.scope_id = evidence.scope_id AND event.event_id = evidence.event_id
WHERE evidence.scope_id = $1 AND evidence.note_id = $2
  AND evidence.revision = ANY($3)
ORDER BY evidence.revision, event.occurred_at, event.sequence, event.event_id`,
		s.scopeID, noteID, revisions,
	)
	if err != nil {
		return nil, fmt.Errorf("list postgres explorer revision evidence: %w", err)
	}
	defer rows.Close()
	events := make(map[int][]explorer.SourceEvent)
	for rows.Next() {
		var revision int
		var event explorer.SourceEvent
		if err := rows.Scan(
			&revision,
			&event.EventID, &event.UserID, &event.AgentID, &event.SessionID,
			&event.Sequence, &event.Type, &event.Content, &event.TaskRef,
			&event.ThreadRef, &event.Visibility, &event.OccurredAt,
			&event.CapturedAt, &event.ExtractedAt,
		); err != nil {
			return nil, fmt.Errorf("scan postgres explorer revision evidence: %w", err)
		}
		events[revision] = append(events[revision], event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres explorer revision evidence: %w", err)
	}
	return events, nil
}

// revisionDeliveries loads the deliveries for every requested revision in one
// query and groups them per revision, preserving the per-revision
// delivered_at DESC, recipient_session_id ordering.
func (s *ExplorerStore) revisionDeliveries(
	ctx context.Context,
	noteID string,
	revisions []int32,
) (map[int][]explorer.Delivery, error) {
	rows, err := s.pool.Query(ctx, `
SELECT revision, recipient_user_id, recipient_agent_id, recipient_session_id,
       delivered_at, context_tokens
FROM note_deliveries
WHERE scope_id = $1 AND note_id = $2 AND revision = ANY($3)
ORDER BY revision, delivered_at DESC, recipient_session_id`,
		s.scopeID, noteID, revisions,
	)
	if err != nil {
		return nil, fmt.Errorf("list postgres explorer revision deliveries: %w", err)
	}
	defer rows.Close()
	deliveries := make(map[int][]explorer.Delivery)
	for rows.Next() {
		var revision int
		var delivery explorer.Delivery
		if err := rows.Scan(
			&revision,
			&delivery.RecipientUserID, &delivery.RecipientAgentID,
			&delivery.RecipientSessionID, &delivery.DeliveredAt,
			&delivery.ContextTokens,
		); err != nil {
			return nil, fmt.Errorf("scan postgres explorer revision delivery: %w", err)
		}
		deliveries[revision] = append(deliveries[revision], delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres explorer revision deliveries: %w", err)
	}
	return deliveries, nil
}

func (s *ExplorerStore) teamNoteRecallUses(
	ctx context.Context,
	noteID string,
) ([]explorer.RecallUse, error) {
	rows, err := s.pool.Query(ctx, `
SELECT observation_id, recipient_agent_id, recipient_session_id,
       created_at, trace
FROM team_note_recall_observations
WHERE scope_id = $1 AND expires_at > NOW()
  AND (
      EXISTS (
          SELECT 1
          FROM jsonb_array_elements_text(
              CASE WHEN jsonb_typeof(trace->'delivered_items') = 'array'
                   THEN trace->'delivered_items' ELSE '[]'::jsonb END
          ) item
          WHERE item = $2 OR item LIKE $2 || '@%'
      )
      OR EXISTS (
          SELECT 1 FROM jsonb_array_elements(
              CASE WHEN jsonb_typeof(trace->'candidate_traces') = 'array'
                   THEN trace->'candidate_traces' ELSE '[]'::jsonb END
          ) item
          WHERE item->>'note_id' = $2
      )
      OR EXISTS (
          SELECT 1 FROM jsonb_array_elements(
              CASE WHEN jsonb_typeof(trace->'rejections') = 'array'
                   THEN trace->'rejections' ELSE '[]'::jsonb END
          ) item
          WHERE item->>'note_id' = $2
      )
      OR EXISTS (
          SELECT 1 FROM jsonb_array_elements(
              CASE WHEN jsonb_typeof(trace->'budget_drops') = 'array'
                   THEN trace->'budget_drops' ELSE '[]'::jsonb END
          ) item
          WHERE item->>'note_id' = $2
      )
  )
ORDER BY created_at DESC, observation_id DESC`, s.scopeID, noteID)
	if err != nil {
		return nil, fmt.Errorf("list postgres explorer note recall uses: %w", err)
	}
	defer rows.Close()
	uses := make([]explorer.RecallUse, 0)
	for rows.Next() {
		var use explorer.RecallUse
		var encodedTrace []byte
		if err := rows.Scan(
			&use.ObservationID, &use.RecipientAgentID,
			&use.RecipientSessionID, &use.OccurredAt, &encodedTrace,
		); err != nil {
			return nil, fmt.Errorf("scan postgres explorer note recall use: %w", err)
		}
		var trace teamnote.RecallTrace
		if err := json.Unmarshal(encodedTrace, &trace); err != nil {
			return nil, fmt.Errorf("decode postgres explorer note recall trace: %w", err)
		}
		populateRecallUse(&use, noteID, trace)
		uses = append(uses, use)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres explorer note recall uses: %w", err)
	}
	return uses, nil
}

func populateRecallUse(use *explorer.RecallUse, noteID string, trace teamnote.RecallTrace) {
	for _, candidate := range trace.CandidateTraces {
		if candidate.NoteID != noteID {
			continue
		}
		use.Disposition = string(candidate.Disposition)
		if candidate.RejectionReason != "" {
			use.RejectionReasons = append(use.RejectionReasons, string(candidate.RejectionReason))
		}
		for _, gate := range candidate.HardGateResults {
			if !gate.Passed {
				use.HardGateFailures = append(use.HardGateFailures, gate.Gate)
			}
		}
	}
	for _, deliveredID := range trace.DeliveredItems {
		if deliveredID == noteID || strings.HasPrefix(deliveredID, noteID+"@") {
			use.Delivered = true
			break
		}
	}
	for _, rejection := range trace.Rejections {
		if rejection.NoteID == noteID {
			use.RejectionReasons = append(use.RejectionReasons, string(rejection.Reason))
		}
	}
	for _, drop := range trace.BudgetDrops {
		if drop.NoteID == noteID {
			use.BudgetDropReasons = append(use.BudgetDropReasons, string(drop.Reason))
		}
	}
}

type explorerRowScanner interface {
	Scan(...any) error
}

func scanTeamNoteSummary(scanner explorerRowScanner) (explorer.TeamNoteSummary, error) {
	var note explorer.TeamNoteSummary
	err := scanner.Scan(
		&note.NoteID, &note.Kind, &note.Subject, &note.State,
		&note.TaskRef, &note.ThreadRef, &note.OriginAgentID,
		&note.AudienceAgentIDs, &note.Revision, &note.CreatedAt,
		&note.UpdatedAt, &note.SoftExpiresAt, &note.HardExpiresAt,
	)
	return note, err
}

func scanSourceEvent(scanner explorerRowScanner) (explorer.SourceEvent, error) {
	var event explorer.SourceEvent
	err := scanner.Scan(
		&event.EventID, &event.UserID, &event.AgentID, &event.SessionID,
		&event.Sequence, &event.Type, &event.Content, &event.TaskRef,
		&event.ThreadRef, &event.Visibility, &event.OccurredAt,
		&event.CapturedAt, &event.ExtractedAt,
	)
	return event, err
}

func extractionErrorCode(status string, storedError string) string {
	if strings.TrimSpace(storedError) == "" {
		return ""
	}
	if status == "quarantined" {
		return "extraction_quarantined"
	}
	return "operation_failed"
}

func (s *ExplorerStore) GetExtractionDiagnostic(
	ctx context.Context,
	runID string,
) (explorer.ExtractionDiagnostic, error) {
	run, err := s.getExtractionRun(ctx, runID)
	if err != nil {
		return explorer.ExtractionDiagnostic{}, err
	}
	candidates, err := s.extractionCandidates(ctx, runID)
	if err != nil {
		return explorer.ExtractionDiagnostic{}, err
	}
	events, err := s.extractionSourceEvents(ctx, run)
	if err != nil {
		return explorer.ExtractionDiagnostic{}, err
	}
	notes, err := s.extractionResultingNotes(ctx, runID)
	if err != nil {
		return explorer.ExtractionDiagnostic{}, err
	}
	return explorer.ExtractionDiagnostic{
		Run: run, Candidates: candidates, SourceEvents: events, ResultingNotes: notes,
	}, nil
}

func (s *ExplorerStore) getExtractionRun(
	ctx context.Context,
	runID string,
) (explorer.ExtractionRun, error) {
	var run explorer.ExtractionRun
	var storedError string
	err := s.pool.QueryRow(ctx, `
SELECT run_id, user_id, agent_id, session_id, from_sequence, to_sequence,
       model, prompt_version, status, input_tokens, output_tokens, error,
       created_at, completed_at
FROM extraction_runs
WHERE scope_id = $1 AND run_id = $2`, s.scopeID, runID).Scan(
		&run.RunID, &run.UserID, &run.AgentID, &run.SessionID,
		&run.FromSequence, &run.ToSequence, &run.Model, &run.PromptVersion,
		&run.Status, &run.InputTokens, &run.OutputTokens, &storedError,
		&run.CreatedAt, &run.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return explorer.ExtractionRun{}, explorer.ErrNotFound
	}
	if err != nil {
		return explorer.ExtractionRun{}, fmt.Errorf("get postgres extraction diagnostic run: %w", err)
	}
	run.ErrorCode = extractionErrorCode(run.Status, storedError)
	return run, nil
}

func (s *ExplorerStore) extractionCandidates(
	ctx context.Context,
	runID string,
) ([]explorer.Candidate, error) {
	rows, err := s.pool.Query(ctx, `
SELECT candidate.candidate_id, candidate.action, candidate.kind,
       candidate.subject, candidate.body, candidate.task_ref,
       candidate.thread_ref, candidate.origin_agent_id,
       candidate.evidence_event_ids, candidate.admission_status,
       candidate.rejection_reason, candidate.created_at,
       COALESCE(revision.note_id, '')
FROM note_candidates candidate
LEFT JOIN note_revisions revision
  ON revision.scope_id = candidate.scope_id
 AND revision.candidate_id = candidate.candidate_id
WHERE candidate.scope_id = $1 AND candidate.run_id = $2
ORDER BY candidate.candidate_id`, s.scopeID, runID)
	if err != nil {
		return nil, fmt.Errorf("list postgres extraction diagnostic candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]explorer.Candidate, 0)
	for rows.Next() {
		var candidate explorer.Candidate
		if err := rows.Scan(
			&candidate.CandidateID, &candidate.Action, &candidate.Kind,
			&candidate.Subject, &candidate.Body, &candidate.TaskRef,
			&candidate.ThreadRef, &candidate.OriginAgentID,
			&candidate.EvidenceEventIDs, &candidate.AdmissionStatus,
			&candidate.RejectionReason, &candidate.CreatedAt,
			&candidate.ResultingNoteID,
		); err != nil {
			return nil, fmt.Errorf("scan postgres extraction diagnostic candidate: %w", err)
		}
		candidate.RejectionReason = safeCandidateRejectionReason(candidate.RejectionReason)
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres extraction diagnostic candidates: %w", err)
	}
	return candidates, nil
}

func safeCandidateRejectionReason(value string) string {
	switch strings.TrimSpace(value) {
	case "":
		return ""
	case "missing_evidence":
		return "missing_evidence"
	default:
		return "candidate_rejected"
	}
}

func (s *ExplorerStore) extractionSourceEvents(
	ctx context.Context,
	run explorer.ExtractionRun,
) ([]explorer.SourceEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT event_id, user_id, agent_id, session_id, sequence, event_type,
       content, task_ref, thread_ref, visibility, occurred_at, captured_at,
       extracted_at
FROM session_events
WHERE scope_id = $1 AND user_id = $2 AND agent_id = $3 AND session_id = $4
  AND sequence BETWEEN $5 AND $6
ORDER BY sequence, event_id`,
		s.scopeID, run.UserID, run.AgentID, run.SessionID, run.FromSequence, run.ToSequence,
	)
	if err != nil {
		return nil, fmt.Errorf("list postgres extraction diagnostic events: %w", err)
	}
	defer rows.Close()
	events := make([]explorer.SourceEvent, 0)
	for rows.Next() {
		event, err := scanSourceEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres extraction diagnostic event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres extraction diagnostic events: %w", err)
	}
	return events, nil
}

func (s *ExplorerStore) extractionResultingNotes(
	ctx context.Context,
	runID string,
) ([]explorer.TeamNoteSummary, error) {
	rows, err := s.pool.Query(ctx, `
SELECT note.note_id, note.kind, note.subject, note.state, note.task_ref,
       note.thread_ref, note.origin_agent_id, note.audience_agent_ids,
       note.current_revision, note.created_at, note.updated_at,
       note.soft_expires_at, note.hard_expires_at
FROM team_notes note
JOIN note_revisions revision
  ON revision.scope_id = note.scope_id AND revision.note_id = note.note_id
JOIN note_candidates candidate
  ON candidate.scope_id = revision.scope_id
 AND candidate.candidate_id = revision.candidate_id
WHERE note.scope_id = $1 AND candidate.run_id = $2
ORDER BY note.updated_at DESC, note.note_id DESC`, s.scopeID, runID)
	if err != nil {
		return nil, fmt.Errorf("list postgres extraction diagnostic notes: %w", err)
	}
	defer rows.Close()
	notes := make([]explorer.TeamNoteSummary, 0)
	for rows.Next() {
		note, err := scanTeamNoteSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan postgres extraction diagnostic note: %w", err)
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres extraction diagnostic notes: %w", err)
	}
	return notes, nil
}

// GetChannelDiagnostic reads an envelope by ID alone: onprem_channel_envelopes
// has no scope_id column, so the unguessable envelope ID is the only guard
// today. Scoping this table is deferred to SaaS Phase 3 per the on-prem/SaaS
// split ADR (docs/decisions/2026-07-31-onprem-saas-split.md).
func (s *ExplorerStore) GetChannelDiagnostic(
	ctx context.Context,
	envelopeID string,
) (explorer.ChannelDiagnostic, error) {
	var diagnostic explorer.ChannelDiagnostic
	var payloadJSON []byte
	err := s.pool.QueryRow(ctx, `
SELECT envelope_id, from_agent_id, to_agent_id, status, message,
       created_at, accepted_at, archived_at, payload_json
FROM onprem_channel_envelopes
WHERE envelope_id = $1`, envelopeID).Scan(
		&diagnostic.EnvelopeID, &diagnostic.FromAgentID,
		&diagnostic.ToAgentID, &diagnostic.Status, &diagnostic.Message,
		&diagnostic.CreatedAt, &diagnostic.AcceptedAt,
		&diagnostic.ArchivedAt, &payloadJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return explorer.ChannelDiagnostic{}, explorer.ErrNotFound
	}
	if err != nil {
		return explorer.ChannelDiagnostic{}, fmt.Errorf("get postgres channel diagnostic: %w", err)
	}
	diagnostic.PayloadStatus = "unavailable"
	payload, decoded := decodeDiagnosticKnowledgeCapsule(payloadJSON)
	if !decoded {
		return diagnostic, nil
	}
	if payload.SchemaVersion != "paxl.envelope_payload.knowledge_capsule.v2" || payload.Capsule == nil {
		return diagnostic, nil
	}
	diagnostic.PayloadStatus = "decoded"
	diagnostic.Capsule = explorer.KnowledgeCapsule{
		CapsuleID:       payload.Capsule.CapsuleID,
		SourceSessionID: payload.Capsule.SourceSessionID,
		SourceAgent:     payload.Capsule.SourceAgent,
		Keyword:         payload.Capsule.Keyword,
		Title:           payload.Capsule.Title,
		Summary:         payload.Capsule.Summary,
		Content:         payload.Capsule.Content,
		Status:          payload.Capsule.Status,
		Truncated:       payload.Capsule.Truncated,
	}
	if payload.Route != nil {
		diagnostic.Capsule.RouteMatchType = payload.Route.MatchType
	}
	return diagnostic, nil
}

func decodeDiagnosticKnowledgeCapsule(raw []byte) (diagnosticKnowledgeCapsulePayload, bool) {
	var payload diagnosticKnowledgeCapsulePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return diagnosticKnowledgeCapsulePayload{}, false
	}
	return payload, true
}

type diagnosticKnowledgeCapsulePayload struct {
	SchemaVersion string                      `json:"schema_version"`
	Capsule       *diagnosticKnowledgeCapsule `json:"capsule"`
	Route         *diagnosticCapsuleRoute     `json:"route"`
}

type diagnosticKnowledgeCapsule struct {
	CapsuleID       string `json:"capsule_id"`
	SourceSessionID string `json:"source_session_id"`
	SourceAgent     string `json:"source_agent"`
	Keyword         string `json:"keyword"`
	Title           string `json:"title"`
	Summary         string `json:"summary"`
	Content         string `json:"content"`
	Status          string `json:"status"`
	Truncated       bool   `json:"truncated"`
}

type diagnosticCapsuleRoute struct {
	MatchType string `json:"match_type"`
}

var _ explorer.Repository = (*ExplorerStore)(nil)
