package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type NoteStore struct {
	store  *Store
	policy teamnote.TTLPolicy
	clock  teamnote.Clock
}

func NewNoteStore(store *Store, policy teamnote.TTLPolicy, clock teamnote.Clock) (*NoteStore, error) {
	if store == nil || policy == nil || clock == nil {
		return nil, fmt.Errorf("create postgres note store: store, policy, and clock are required")
	}
	return &NoteStore{store: store, policy: policy, clock: clock}, nil
}

func (s *NoteStore) ApplyCandidate(ctx context.Context, scopeID, runID string, candidate teamnote.Candidate, evidence []teamnote.SessionEvent) (note teamnote.Note, returnedErr error) {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(runID) == "" {
		return teamnote.Note{}, fmt.Errorf("apply postgres candidate: scope and run are required")
	}
	candidate = teamnote.WithEvidenceTime(candidate, evidence)
	tx, err := s.store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return teamnote.Note{}, fmt.Errorf("begin apply candidate: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback apply candidate: %w", rollbackErr))
		}
	}()

	if err := ensureExtractionRun(ctx, tx, scopeID, runID, candidate); err != nil {
		return teamnote.Note{}, err
	}
	inserted, err := insertCandidate(ctx, tx, scopeID, runID, candidate)
	if err != nil {
		return teamnote.Note{}, err
	}
	if !inserted {
		existing, findErr := noteForCandidate(ctx, tx, scopeID, candidate.ID)
		if findErr != nil {
			return teamnote.Note{}, findErr
		}
		return existing, nil
	}
	noteKey := teamnote.CanonicalKey(candidate)
	if err := lockNoteKey(ctx, tx, scopeID, noteKey); err != nil {
		return teamnote.Note{}, err
	}
	current, err := noteForUpdate(ctx, tx, scopeID, noteKey)
	if err != nil {
		return teamnote.Note{}, err
	}
	note, err = teamnote.AdmitCandidate(s.policy, s.clock.Now(), candidate, evidence, current)
	if err != nil {
		return teamnote.Note{}, err
	}
	if err := saveNote(ctx, tx, scopeID, candidate, note); err != nil {
		return teamnote.Note{}, err
	}
	if err := saveEvidence(ctx, tx, scopeID, note); err != nil {
		return teamnote.Note{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE note_candidates SET admission_status = 'admitted' WHERE scope_id = $1 AND candidate_id = $2`, scopeID, candidate.ID); err != nil {
		return teamnote.Note{}, fmt.Errorf("mark candidate admitted: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE extraction_runs SET status = 'completed', completed_at = NOW() WHERE scope_id = $1 AND run_id = $2`, scopeID, runID); err != nil {
		return teamnote.Note{}, fmt.Errorf("complete extraction run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return teamnote.Note{}, fmt.Errorf("commit candidate: %w", err)
	}
	return note, nil
}

func (s *NoteStore) RecallNotes(ctx context.Context, scopeID string, request teamnote.RecallRequest) (envelope teamnote.NoteEnvelope, returnedErr error) {
	if strings.TrimSpace(scopeID) == "" {
		return teamnote.NoteEnvelope{}, fmt.Errorf("recall postgres notes: scope is required")
	}
	if err := teamnote.ValidateRecall(request); err != nil {
		return teamnote.NoteEnvelope{}, err
	}
	tx, err := s.store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return teamnote.NoteEnvelope{}, fmt.Errorf("begin recall notes: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback recall notes: %w", rollbackErr))
		}
	}()
	notes, err := recallableNotes(ctx, tx, scopeID, request, s.clock.Now())
	if err != nil {
		return teamnote.NoteEnvelope{}, err
	}
	for _, note := range notes {
		item := teamnote.FormatForRecallWithRelated(note, findRelatedNotes(note, notes))
		tokens := estimateNoteTokens(item)
		if envelope.Tokens+tokens > request.TokenBudget {
			continue
		}
		claimed, err := insertDelivery(ctx, tx, scopeID, note, request.Actor, tokens)
		if err != nil {
			return teamnote.NoteEnvelope{}, err
		}
		if !claimed {
			continue
		}
		envelope.Items = append(envelope.Items, item)
		envelope.Details = append(envelope.Details, teamnote.RecalledNote{
			NoteID: note.ID, Revision: note.Revision, Text: item, Origin: note.Origin,
		})
		envelope.Tokens += tokens
		envelope.Revision = fmt.Sprintf("%s:%d", note.ID, note.Revision)
	}
	if err := tx.Commit(ctx); err != nil {
		return teamnote.NoteEnvelope{}, fmt.Errorf("commit note delivery: %w", err)
	}
	return envelope, nil
}

func ensureExtractionRun(ctx context.Context, tx pgx.Tx, scopeID, runID string, candidate teamnote.Candidate) error {
	_, err := tx.Exec(ctx, `
INSERT INTO extraction_runs (
    scope_id, run_id, agent_id, session_id, from_sequence, to_sequence,
    input_checksum, status
) VALUES ($1, $2, $3, $4, 0, 0, $2, 'processing')
ON CONFLICT (scope_id, run_id) DO NOTHING`,
		scopeID, runID, candidate.Origin.AgentID, candidate.Origin.SessionID)
	if err != nil {
		return fmt.Errorf("ensure extraction run: %w", err)
	}
	return nil
}

func insertCandidate(ctx context.Context, tx pgx.Tx, scopeID, runID string, candidate teamnote.Candidate) (bool, error) {
	result, err := tx.Exec(ctx, `
INSERT INTO note_candidates (
    scope_id, candidate_id, run_id, action, kind, subject, body, task_ref,
    thread_ref, origin_user_id, origin_agent_id, origin_session_id,
    audience_agent_ids, related_subjects, evidence_event_ids, valid_at, invalid_at, source_occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
ON CONFLICT (scope_id, candidate_id) DO NOTHING`,
		scopeID, candidate.ID, runID, candidate.Action, candidate.Kind, candidate.Subject,
		candidate.Body, candidate.TaskRef, candidate.ThreadRef, candidate.Origin.UserID,
		candidate.Origin.AgentID, candidate.Origin.SessionID, nonNilStrings(candidate.AudienceAgentIDs),
		nonNilStrings(candidate.RelatedSubjects), candidate.EvidenceEventIDs, candidate.ValidAt,
		candidate.InvalidAt, candidate.SourceOccurredAt)
	if err != nil {
		return false, fmt.Errorf("insert note candidate: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func noteForUpdate(ctx context.Context, tx pgx.Tx, scopeID, key string) (*teamnote.Note, error) {
	row := tx.QueryRow(ctx, noteSelect+` WHERE scope_id = $1 AND note_key = $2 FOR UPDATE`, scopeID, key)
	note, err := scanNote(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load current note: %w", err)
	}
	return &note, nil
}

func lockNoteKey(ctx context.Context, tx pgx.Tx, scopeID, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, scopeID+"\x1f"+key); err != nil {
		return fmt.Errorf("lock note key: %w", err)
	}
	return nil
}

func noteForCandidate(ctx context.Context, tx pgx.Tx, scopeID, candidateID string) (teamnote.Note, error) {
	row := tx.QueryRow(ctx, noteSelect+`
 WHERE scope_id = $1
   AND note_id = (
       SELECT note_id FROM note_revisions
       WHERE scope_id = $1 AND candidate_id = $2
   )`, scopeID, candidateID)
	note, err := scanNote(row)
	if err != nil {
		return teamnote.Note{}, fmt.Errorf("load note for candidate: %w", err)
	}
	return note, nil
}

func saveNote(ctx context.Context, tx pgx.Tx, scopeID string, candidate teamnote.Candidate, note teamnote.Note) error {
	_, err := tx.Exec(ctx, `
INSERT INTO team_notes (
    scope_id, note_id, note_key, kind, subject, body, task_ref, thread_ref,
    origin_user_id, origin_agent_id, origin_session_id, audience_agent_ids, related_subjects,
    state, current_revision, soft_expires_at, hard_expires_at, created_at, updated_at,
    valid_at, invalid_at, source_occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
ON CONFLICT (scope_id, note_id) DO UPDATE SET
    body = EXCLUDED.body,
    origin_user_id = EXCLUDED.origin_user_id,
    origin_agent_id = EXCLUDED.origin_agent_id,
    origin_session_id = EXCLUDED.origin_session_id,
    audience_agent_ids = EXCLUDED.audience_agent_ids,
	related_subjects = EXCLUDED.related_subjects,
    state = EXCLUDED.state,
    current_revision = EXCLUDED.current_revision,
    soft_expires_at = EXCLUDED.soft_expires_at,
    valid_at = EXCLUDED.valid_at,
    invalid_at = EXCLUDED.invalid_at,
    source_occurred_at = EXCLUDED.source_occurred_at,
    updated_at = EXCLUDED.updated_at`,
		scopeID, note.ID, note.Key, note.Kind, note.Subject, note.Body, note.TaskRef,
		note.ThreadRef, note.Origin.UserID, note.Origin.AgentID, note.Origin.SessionID,
		nonNilStrings(note.AudienceAgentIDs), nonNilStrings(note.RelatedSubjects), note.State, note.Revision, note.SoftExpiresAt,
		note.HardExpiresAt, note.CreatedAt, note.UpdatedAt, note.ValidAt, note.InvalidAt, note.SourceOccurredAt)
	if err != nil {
		return fmt.Errorf("save team note: %w", err)
	}
	validityBoundary := note.SourceOccurredAt
	if candidate.Action == teamnote.ActionResolve && note.InvalidAt != nil {
		validityBoundary = *note.InvalidAt
	} else if note.ValidAt != nil {
		validityBoundary = *note.ValidAt
	}
	if _, err = tx.Exec(ctx, `
UPDATE note_revisions
SET expired_at = $3, invalid_at = COALESCE(invalid_at, $4)
WHERE scope_id = $1 AND note_id = $2 AND expired_at IS NULL`,
		scopeID, note.ID, note.UpdatedAt, validityBoundary); err != nil {
		return fmt.Errorf("expire previous note revision: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO note_revisions (
    scope_id, note_id, revision, candidate_id, operation, body,
    related_subjects, valid_at, invalid_at, created_at, expired_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL)`,
		scopeID, note.ID, note.Revision, candidate.ID, candidate.Action, note.Body,
		nonNilStrings(note.RelatedSubjects), note.ValidAt, note.InvalidAt, note.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save note revision: %w", err)
	}
	return nil
}

func saveEvidence(ctx context.Context, tx pgx.Tx, scopeID string, note teamnote.Note) error {
	for _, eventID := range note.EvidenceEventIDs {
		_, err := tx.Exec(ctx, `
INSERT INTO note_evidence (scope_id, note_id, revision, event_id)
VALUES ($1, $2, $3, $4)`, scopeID, note.ID, note.Revision, eventID)
		if err != nil {
			return fmt.Errorf("save note evidence %q: %w", eventID, err)
		}
	}
	return nil
}

func recallableNotes(ctx context.Context, tx pgx.Tx, scopeID string, request teamnote.RecallRequest, now time.Time) ([]teamnote.Note, error) {
	rows, err := tx.Query(ctx, noteSelect+`
 WHERE scope_id = $1
   AND state = 'active'
   AND invalid_at IS NULL
   AND soft_expires_at > $2
   AND hard_expires_at > $2
   AND (task_ref = '' OR task_ref = $3)
   AND (thread_ref = '' OR thread_ref = $4)
   AND (cardinality(audience_agent_ids) = 0 OR $5 = ANY(audience_agent_ids))
   AND NOT EXISTS (
       SELECT 1 FROM note_deliveries delivery
       WHERE delivery.scope_id = team_notes.scope_id
         AND delivery.note_id = team_notes.note_id
         AND delivery.revision = team_notes.current_revision
         AND delivery.recipient_session_id = $6
   )
 ORDER BY CASE WHEN $7 = '' THEN 0 ELSE ts_rank_cd(
         to_tsvector('simple', subject || ' ' || body),
         to_tsquery('simple', array_to_string(tsvector_to_array(to_tsvector('simple', $7)), ' | '))
     ) END DESC,
     CASE kind
     WHEN 'handoff' THEN 0 WHEN 'blocker' THEN 1 WHEN 'status' THEN 2 ELSE 3 END,
     source_occurred_at DESC NULLS LAST,
     updated_at DESC
 FOR UPDATE OF team_notes`, scopeID, now, request.TaskRef, request.ThreadRef,
		request.Actor.AgentID, request.Actor.SessionID, teamnote.SearchQuery(request.Query))
	if err != nil {
		return nil, fmt.Errorf("query recallable notes: %w", err)
	}
	notes, err := pgx.CollectRows(rows, scanCollectableNote)
	if err != nil {
		return nil, fmt.Errorf("scan recallable notes: %w", err)
	}
	return notes, nil
}

func insertDelivery(ctx context.Context, tx pgx.Tx, scopeID string, note teamnote.Note, actor teamnote.Actor, tokens int) (bool, error) {
	result, err := tx.Exec(ctx, `
INSERT INTO note_deliveries (
    scope_id, note_id, revision, recipient_user_id, recipient_agent_id,
    recipient_session_id, context_tokens
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING`, scopeID, note.ID, note.Revision, actor.UserID, actor.AgentID, actor.SessionID, tokens)
	if err != nil {
		return false, fmt.Errorf("save note delivery: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

const noteSelect = `
SELECT note_id, note_key, kind, subject, body, task_ref, thread_ref,
       origin_user_id, origin_agent_id, origin_session_id, audience_agent_ids, related_subjects,
       COALESCE((
           SELECT array_agg(event_id ORDER BY event_id)
           FROM note_evidence
           WHERE scope_id = team_notes.scope_id
             AND note_id = team_notes.note_id
             AND revision = team_notes.current_revision
       ), '{}'),
       state, current_revision, soft_expires_at, hard_expires_at, created_at, updated_at,
       valid_at, invalid_at, COALESCE(source_occurred_at, updated_at)
FROM team_notes`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNote(row rowScanner) (teamnote.Note, error) {
	var note teamnote.Note
	err := row.Scan(
		&note.ID, &note.Key, &note.Kind, &note.Subject, &note.Body, &note.TaskRef,
		&note.ThreadRef, &note.Origin.UserID, &note.Origin.AgentID, &note.Origin.SessionID,
		&note.AudienceAgentIDs, &note.RelatedSubjects, &note.EvidenceEventIDs, &note.State, &note.Revision, &note.SoftExpiresAt,
		&note.HardExpiresAt, &note.CreatedAt, &note.UpdatedAt, &note.ValidAt, &note.InvalidAt,
		&note.SourceOccurredAt,
	)
	if err != nil {
		return teamnote.Note{}, err
	}
	note.SoftExpiresAt = note.SoftExpiresAt.UTC()
	note.HardExpiresAt = note.HardExpiresAt.UTC()
	note.CreatedAt = note.CreatedAt.UTC()
	note.UpdatedAt = note.UpdatedAt.UTC()
	if note.ValidAt != nil {
		value := note.ValidAt.UTC()
		note.ValidAt = &value
	}
	if note.InvalidAt != nil {
		value := note.InvalidAt.UTC()
		note.InvalidAt = &value
	}
	if len(note.RelatedSubjects) == 0 {
		note.RelatedSubjects = nil
	}
	note.SourceOccurredAt = note.SourceOccurredAt.UTC()
	return note, nil
}

func scanCollectableNote(row pgx.CollectableRow) (teamnote.Note, error) {
	return scanNote(row)
}

func estimateNoteTokens(text string) int {
	return max(1, (len(text)+3)/4)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func findRelatedNotes(note teamnote.Note, notes []teamnote.Note) []teamnote.Note {
	if len(note.RelatedSubjects) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(note.RelatedSubjects))
	for _, subject := range note.RelatedSubjects {
		wanted[strings.ToLower(subject)] = struct{}{}
	}
	result := make([]teamnote.Note, 0, len(wanted))
	for _, candidate := range notes {
		if _, ok := wanted[strings.ToLower(candidate.Subject)]; ok {
			result = append(result, candidate)
		}
	}
	return result
}
