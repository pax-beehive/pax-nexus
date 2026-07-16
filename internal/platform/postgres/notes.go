package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pax-beehive/pax-nexus/internal/platform/textembedding"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const (
	EmbeddingDimensions      = 384
	defaultCandidateLimit    = 16
	defaultSemanticThreshold = 0.65
	rrfConstant              = 60.0
	queryInstruction         = "Retrieve Team Notes containing facts, decisions, blockers, ownership, deadlines, or status relevant to the current agent request."
)

type RetrievalConfig struct {
	Embedder          textembedding.Embedder
	EmbeddingModel    string
	SemanticThreshold float64
	CandidateLimit    int
}

type NoteStore struct {
	store     *Store
	policy    teamnote.TTLPolicy
	clock     teamnote.Clock
	retrieval RetrievalConfig
}

func NewNoteStore(store *Store, policy teamnote.TTLPolicy, clock teamnote.Clock, retrieval RetrievalConfig) (*NoteStore, error) {
	if store == nil || policy == nil || clock == nil {
		return nil, fmt.Errorf("create postgres note store: store, policy, and clock are required")
	}
	if retrieval.Embedder != nil && strings.TrimSpace(retrieval.EmbeddingModel) == "" {
		return nil, fmt.Errorf("create postgres note store: embedding model is required when embedding is enabled")
	}
	if retrieval.SemanticThreshold == 0 {
		retrieval.SemanticThreshold = defaultSemanticThreshold
	}
	if retrieval.SemanticThreshold < 0 || retrieval.SemanticThreshold > 1 {
		return nil, fmt.Errorf("create postgres note store: semantic threshold must be between zero and one")
	}
	if retrieval.CandidateLimit == 0 {
		retrieval.CandidateLimit = defaultCandidateLimit
	}
	if retrieval.CandidateLimit < 1 {
		return nil, fmt.Errorf("create postgres note store: candidate limit must be positive")
	}
	return &NoteStore{store: store, policy: policy, clock: clock, retrieval: retrieval}, nil
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
	if err := s.refreshEmbedding(ctx, scopeID, note); err != nil {
		return teamnote.Note{}, err
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
	queryVector, embeddingErr := s.embedQuery(ctx, request.Query)
	if embeddingErr != nil {
		// Semantic recall is best effort; lexical recall remains available when the local model is unavailable.
		queryVector = nil
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
	eligible, err := recallableNotes(ctx, tx, scopeID, request, s.clock.Now(), queryVector, s.retrieval.EmbeddingModel)
	if err != nil {
		return teamnote.NoteEnvelope{}, err
	}
	candidates := rankRecallCandidates(eligible, request, s.retrieval)
	allNotes := notesFromRanked(eligible)
	selectedNotes := 0
	for _, candidate := range candidates {
		note := candidate.Note
		relevance := hybridRelevance(candidate, request.Query)
		if !hybridRelevant(candidate, request.Query, s.retrieval.SemanticThreshold) {
			continue
		}
		if request.MaxItems > 0 && selectedNotes >= request.MaxItems {
			break
		}
		remainingRelated := 0
		if request.MaxItems > 0 {
			remainingRelated = request.MaxItems - selectedNotes - 1
		}
		related := filterRelatedNotes(findRelatedNotes(note, allNotes), request.Query, remainingRelated, request.MaxItems > 0)
		item := teamnote.FormatForRecallWithRelated(note, related)
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
			Relevance: relevance, Certainty: teamnote.CertaintyForKind(note.Kind),
		})
		envelope.Tokens += tokens
		selectedNotes += 1 + len(related)
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
	    updated_at = EXCLUDED.updated_at,
	    embedding = NULL,
	    embedding_model = '',
	    embedding_revision = NULL,
	    embedding_error = ''`,
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

type rankedNote struct {
	teamnote.Note
	LexicalScore  float64
	SemanticScore *float64
	FusionScore   float64
	RerankScore   float64
}

func recallableNotes(ctx context.Context, tx pgx.Tx, scopeID string, request teamnote.RecallRequest, now time.Time, queryVector []float32, embeddingModel string) ([]rankedNote, error) {
	var vectorValue any
	if len(queryVector) > 0 {
		vectorValue = formatVector(queryVector)
	}
	rows, err := tx.Query(ctx, `SELECT `+noteColumns+`,
       CASE WHEN $7 = '' THEN 0 ELSE ts_rank_cd(
           to_tsvector('simple', subject || ' ' || body),
           to_tsquery('simple', array_to_string(tsvector_to_array(to_tsvector('simple', $7)), ' | '))
       ) END,
	       CASE WHEN $10::vector IS NULL OR embedding IS NULL
	                 OR embedding_revision != current_revision OR embedding_model != $11 THEN NULL
	            ELSE 1 - (embedding <=> $10::vector) END
 FROM team_notes
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
     CASE WHEN $8 AND origin_user_id = $9 THEN 0 ELSE 1 END,
     CASE kind
     WHEN 'handoff' THEN 0 WHEN 'blocker' THEN 1 WHEN 'status' THEN 2 ELSE 3 END,
     source_occurred_at DESC NULLS LAST,
     updated_at DESC
	FOR UPDATE OF team_notes`, scopeID, now, request.TaskRef, request.ThreadRef,
		request.Actor.AgentID, request.Actor.SessionID, teamnote.SearchQuery(request.Query),
		teamnote.QueryRequestsOwnContext(request.Query), request.Actor.UserID, vectorValue, embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("query recallable notes: %w", err)
	}
	notes, err := pgx.CollectRows(rows, scanRankedNote)
	if err != nil {
		return nil, fmt.Errorf("scan recallable notes: %w", err)
	}
	return notes, nil
}

func (s *NoteStore) refreshEmbedding(ctx context.Context, scopeID string, note teamnote.Note) error {
	if s.retrieval.Embedder == nil {
		return nil
	}
	if note.State != teamnote.StateActive {
		_, err := s.store.pool.Exec(ctx, `UPDATE team_notes SET embedding = NULL, embedding_revision = NULL, embedding_error = '' WHERE scope_id = $1 AND note_id = $2`, scopeID, note.ID)
		if err != nil {
			return fmt.Errorf("clear resolved note embedding: %w", err)
		}
		return nil
	}
	vectors, err := s.retrieval.Embedder.Embed(ctx, []string{embeddingDocument(note)})
	if err != nil {
		return s.recordEmbeddingError(ctx, scopeID, note, err)
	}
	if len(vectors) != 1 || len(vectors[0]) != EmbeddingDimensions {
		return s.recordEmbeddingError(ctx, scopeID, note, fmt.Errorf("unexpected embedding dimensions"))
	}
	return s.saveEmbedding(ctx, scopeID, note, vectors[0])
}

func (s *NoteStore) saveEmbedding(ctx context.Context, scopeID string, note teamnote.Note, vector []float32) error {
	_, err := s.store.pool.Exec(ctx, `
UPDATE team_notes
SET embedding = $4::vector, embedding_model = $5, embedding_revision = $3, embedding_error = ''
WHERE scope_id = $1 AND note_id = $2 AND current_revision = $3`,
		scopeID, note.ID, note.Revision, formatVector(vector), s.retrieval.EmbeddingModel)
	if err != nil {
		return fmt.Errorf("save note embedding: %w", err)
	}
	return nil
}

// BackfillEmbeddings refreshes at most one bounded batch after an upgrade or
// embedding model change. Provider failures are recorded and leave lexical
// recall available.
func (s *NoteStore) BackfillEmbeddings(ctx context.Context, batchSize int) (int, error) {
	if s.retrieval.Embedder == nil {
		return 0, nil
	}
	if batchSize <= 0 {
		return 0, fmt.Errorf("backfill note embeddings: positive batch size is required")
	}
	targets, err := s.embeddingTargets(ctx, batchSize)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}
	return s.backfillEmbeddingTargets(ctx, targets)
}

func (s *NoteStore) backfillEmbeddingTargets(ctx context.Context, targets []embeddingTarget) (int, error) {
	texts := make([]string, 0, len(targets))
	for _, target := range targets {
		texts = append(texts, embeddingDocument(target.Note))
	}
	vectors, err := s.retrieval.Embedder.Embed(ctx, texts)
	if err != nil {
		return 0, s.recordTargetEmbeddingErrors(ctx, targets, err)
	}
	if len(vectors) != len(targets) {
		mismatch := fmt.Errorf("received %d vectors for %d notes", len(vectors), len(targets))
		return 0, s.recordTargetEmbeddingErrors(ctx, targets, mismatch)
	}
	return s.saveTargetEmbeddings(ctx, targets, vectors)
}

func (s *NoteStore) recordTargetEmbeddingErrors(ctx context.Context, targets []embeddingTarget, embeddingErr error) error {
	for _, target := range targets {
		if err := s.recordEmbeddingError(ctx, target.ScopeID, target.Note, embeddingErr); err != nil {
			return err
		}
	}
	return nil
}

func (s *NoteStore) saveTargetEmbeddings(ctx context.Context, targets []embeddingTarget, vectors [][]float32) (int, error) {
	completed := 0
	for index, target := range targets {
		if len(vectors[index]) != EmbeddingDimensions {
			if err := s.recordEmbeddingError(ctx, target.ScopeID, target.Note, fmt.Errorf("unexpected embedding dimensions")); err != nil {
				return completed, err
			}
			continue
		}
		if err := s.saveEmbedding(ctx, target.ScopeID, target.Note, vectors[index]); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}

type embeddingTarget struct {
	ScopeID string
	teamnote.Note
}

func (s *NoteStore) embeddingTargets(ctx context.Context, limit int) ([]embeddingTarget, error) {
	rows, err := s.store.pool.Query(ctx, `
SELECT scope_id, note_id, kind, subject, body, current_revision
FROM team_notes
WHERE state = 'active'
	AND invalid_at IS NULL
	AND soft_expires_at > NOW()
	AND hard_expires_at > NOW()
  AND (embedding IS NULL OR embedding_revision != current_revision OR embedding_model != $1)
ORDER BY scope_id, note_id
LIMIT $2`, s.retrieval.EmbeddingModel, limit)
	if err != nil {
		return nil, fmt.Errorf("query note embedding backfill: %w", err)
	}
	targets, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (embeddingTarget, error) {
		var target embeddingTarget
		scanErr := row.Scan(&target.ScopeID, &target.ID, &target.Kind, &target.Subject, &target.Body, &target.Revision)
		return target, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("scan note embedding backfill: %w", err)
	}
	return targets, nil
}

func (s *NoteStore) recordEmbeddingError(ctx context.Context, scopeID string, note teamnote.Note, embeddingErr error) error {
	message := embeddingErr.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	_, err := s.store.pool.Exec(ctx, `
UPDATE team_notes
SET embedding = NULL, embedding_model = $4, embedding_revision = NULL, embedding_error = $3
WHERE scope_id = $1 AND note_id = $2 AND current_revision = $5`,
		scopeID, note.ID, message, s.retrieval.EmbeddingModel, note.Revision)
	if err != nil {
		return fmt.Errorf("record note embedding failure: %w", err)
	}
	return nil
}

func (s *NoteStore) embedQuery(ctx context.Context, query string) ([]float32, error) {
	if s.retrieval.Embedder == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	input := "Instruct: " + queryInstruction + "\nQuery:" + strings.TrimSpace(query)
	vectors, err := s.retrieval.Embedder.Embed(ctx, []string{input})
	if err != nil {
		return nil, fmt.Errorf("embed recall query: %w", err)
	}
	if len(vectors) != 1 || len(vectors[0]) != EmbeddingDimensions {
		return nil, fmt.Errorf("embed recall query: expected one %d-dimensional vector", EmbeddingDimensions)
	}
	return vectors[0], nil
}

func embeddingDocument(note teamnote.Note) string {
	return fmt.Sprintf("Kind: %s\nSubject: %s\nBody: %s", note.Kind, note.Subject, note.Body)
}

func formatVector(vector []float32) string {
	var result strings.Builder
	result.Grow(len(vector) * 8)
	result.WriteByte('[')
	for index, value := range vector {
		if index > 0 {
			result.WriteByte(',')
		}
		result.WriteString(strconv.FormatFloat(float64(value), 'g', -1, 32))
	}
	result.WriteByte(']')
	return result.String()
}

func rankRecallCandidates(eligible []rankedNote, request teamnote.RecallRequest, config RetrievalConfig) []rankedNote {
	if strings.TrimSpace(request.Query) == "" {
		return append([]rankedNote(nil), eligible...)
	}
	lexical := append([]rankedNote(nil), eligible...)
	sort.SliceStable(lexical, func(left, right int) bool {
		return lexical[left].LexicalScore > lexical[right].LexicalScore
	})
	semantic := append([]rankedNote(nil), eligible...)
	sort.SliceStable(semantic, func(left, right int) bool {
		leftScore, rightScore := semantic[left].SemanticScore, semantic[right].SemanticScore
		if leftScore == nil {
			return false
		}
		if rightScore == nil {
			return true
		}
		return *leftScore > *rightScore
	})

	scores := make(map[string]float64, config.CandidateLimit*2)
	for rank, candidate := range lexical {
		if rank >= config.CandidateLimit || candidate.LexicalScore <= 0 {
			break
		}
		scores[candidate.ID] += reciprocalRank(rank)
	}
	for rank, candidate := range semantic {
		if rank >= config.CandidateLimit || candidate.SemanticScore == nil || *candidate.SemanticScore < config.SemanticThreshold {
			break
		}
		scores[candidate.ID] += reciprocalRank(rank)
	}

	result := make([]rankedNote, 0, len(scores))
	for _, candidate := range eligible {
		if score, ok := scores[candidate.ID]; ok {
			candidate.FusionScore = score
			if hybridRelevant(candidate, request.Query, config.SemanticThreshold) {
				result = append(result, candidate)
			}
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if ownSourceRank(result[left].Note, request) != ownSourceRank(result[right].Note, request) {
			return ownSourceRank(result[left].Note, request) < ownSourceRank(result[right].Note, request)
		}
		if noteKindRank(result[left].Kind) != noteKindRank(result[right].Kind) {
			return noteKindRank(result[left].Kind) < noteKindRank(result[right].Kind)
		}
		if !result[left].SourceOccurredAt.Equal(result[right].SourceOccurredAt) {
			return result[left].SourceOccurredAt.After(result[right].SourceOccurredAt)
		}
		return result[left].UpdatedAt.After(result[right].UpdatedAt)
	})
	for rank := range result {
		result[rank].RerankScore = result[rank].FusionScore + reciprocalRank(rank)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].RerankScore > result[right].RerankScore
	})
	return result
}

func reciprocalRank(zeroBasedRank int) float64 {
	return 1 / (rrfConstant + float64(zeroBasedRank+1))
}

func ownSourceRank(note teamnote.Note, request teamnote.RecallRequest) int {
	if teamnote.QueryRequestsOwnContext(request.Query) && note.Origin.UserID == request.Actor.UserID {
		return 0
	}
	return 1
}

func noteKindRank(kind teamnote.NoteKind) int {
	switch kind {
	case teamnote.KindHandoff:
		return 0
	case teamnote.KindBlocker:
		return 1
	case teamnote.KindStatus:
		return 2
	default:
		return 3
	}
}

func hybridRelevant(candidate rankedNote, query string, semanticThreshold float64) bool {
	if teamnote.QueryRelevant(candidate.Note, query) {
		return true
	}
	return candidate.SemanticScore != nil && *candidate.SemanticScore >= semanticThreshold &&
		teamnote.QuerySemanticallyRelevant(candidate.Note, query)
}

func hybridRelevance(candidate rankedNote, query string) float64 {
	relevance := teamnote.QueryRelevance(candidate.Note, query)
	if candidate.SemanticScore != nil && *candidate.SemanticScore > relevance {
		return *candidate.SemanticScore
	}
	return relevance
}

func notesFromRanked(ranked []rankedNote) []teamnote.Note {
	notes := make([]teamnote.Note, 0, len(ranked))
	for _, candidate := range ranked {
		notes = append(notes, candidate.Note)
	}
	return notes
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

const noteColumns = `note_id, note_key, kind, subject, body, task_ref, thread_ref,
       origin_user_id, origin_agent_id, origin_session_id, audience_agent_ids, related_subjects,
       COALESCE((
           SELECT array_agg(event_id ORDER BY event_id)
           FROM note_evidence
           WHERE scope_id = team_notes.scope_id
             AND note_id = team_notes.note_id
             AND revision = team_notes.current_revision
       ), '{}'),
       state, current_revision, soft_expires_at, hard_expires_at, created_at, updated_at,
       valid_at, invalid_at, COALESCE(source_occurred_at, updated_at)`

const noteSelect = `SELECT ` + noteColumns + ` FROM team_notes`

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
	normalizeNoteTimes(&note)
	return note, nil
}

func scanRankedNote(row pgx.CollectableRow) (rankedNote, error) {
	var ranked rankedNote
	err := row.Scan(
		&ranked.ID, &ranked.Key, &ranked.Kind, &ranked.Subject, &ranked.Body, &ranked.TaskRef,
		&ranked.ThreadRef, &ranked.Origin.UserID, &ranked.Origin.AgentID, &ranked.Origin.SessionID,
		&ranked.AudienceAgentIDs, &ranked.RelatedSubjects, &ranked.EvidenceEventIDs, &ranked.State,
		&ranked.Revision, &ranked.SoftExpiresAt, &ranked.HardExpiresAt, &ranked.CreatedAt, &ranked.UpdatedAt,
		&ranked.ValidAt, &ranked.InvalidAt, &ranked.SourceOccurredAt, &ranked.LexicalScore, &ranked.SemanticScore,
	)
	if err != nil {
		return rankedNote{}, err
	}
	normalizeNoteTimes(&ranked.Note)
	return ranked, nil
}

func normalizeNoteTimes(note *teamnote.Note) {
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

func filterRelatedNotes(notes []teamnote.Note, query string, limit int, limited bool) []teamnote.Note {
	result := make([]teamnote.Note, 0, len(notes))
	for _, note := range notes {
		if !teamnote.QueryRelated(note, query) {
			continue
		}
		if limited && len(result) >= max(0, limit) {
			break
		}
		result = append(result, note)
	}
	return result
}
