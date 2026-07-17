package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
	queryInstruction         = "Retrieve Team Notes containing facts, decisions, blockers, ownership, deadlines, or status relevant to the current agent request."
)

type RetrievalConfig struct {
	Embedder          textembedding.Embedder
	EmbeddingModel    string
	SemanticThreshold float64
	CandidateLimit    int
}

type recallSnapshotItem struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	EvidenceEventIDs []string `json:"evidence_event_ids,omitempty"`
}

type recallExtractionProvenance struct {
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	DurationMS    int64  `json:"duration_ms"`
	Error         string `json:"error,omitempty"`
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
	fromSequence, toSequence := evidenceRange(evidence)
	run := teamnote.ExtractionRun{
		ID: runID, Actor: candidate.Origin, FromSequence: fromSequence, ToSequence: toSequence,
		InputChecksum: runID, Candidates: []teamnote.Candidate{candidate}, Evidence: evidence,
	}
	notes, err := s.ApplyExtractionRun(ctx, scopeID, run)
	if err != nil {
		return teamnote.Note{}, err
	}
	if len(notes) != 1 {
		return teamnote.Note{}, fmt.Errorf("apply postgres candidate: expected one admitted note")
	}
	return notes[0], nil
}

func (s *NoteStore) ApplyExtractionRun(ctx context.Context, scopeID string, run teamnote.ExtractionRun) (notes []teamnote.Note, returnedErr error) {
	var err error
	run, err = teamnote.NormalizeExtractionRun(run)
	if err != nil {
		return nil, err
	}
	if err := validateExtractionRun(scopeID, run); err != nil {
		return nil, err
	}
	tx, err := s.store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin apply extraction run: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback apply extraction run: %w", rollbackErr))
		}
	}()

	notes, err = s.applyExtractionRunTx(ctx, tx, scopeID, run)
	if err != nil {
		if !isDeterministicAdmissionError(err) {
			return nil, err
		}
		// Roll back before quarantining: the quarantine insert conflicts with
		// this transaction's own uncommitted run row and would otherwise wait
		// on itself forever.
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return nil, errors.Join(err, fmt.Errorf("rollback apply extraction run: %w", rollbackErr))
		}
		// A deterministic admission failure cannot succeed on retry, so the
		// run is recorded as quarantined and the caller may advance the stream.
		if quarantineErr := s.quarantineExtractionRun(ctx, scopeID, run, err); quarantineErr != nil {
			return nil, errors.Join(err, fmt.Errorf("quarantine extraction run: %w", quarantineErr))
		}
		return nil, fmt.Errorf("quarantine extraction run %q: %w", run.ID, errors.Join(teamnote.ErrExtractionRunQuarantined, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit extraction run: %w", err)
	}
	for _, note := range notes {
		if err := s.refreshEmbedding(ctx, scopeID, note); err != nil {
			return nil, err
		}
	}
	return notes, nil
}

// isDeterministicAdmissionError reports whether re-admitting the same run can
// never succeed, as opposed to a transient store failure.
func isDeterministicAdmissionError(err error) bool {
	return errors.Is(err, teamnote.ErrInvalidCandidate) ||
		errors.Is(err, teamnote.ErrMissingEvidence) ||
		errors.Is(err, teamnote.ErrNoteNotFound)
}

// quarantineExtractionRun durably records a run whose candidates failed
// deterministic admission, so later replays observe the same outcome and
// extraction evaluation can attribute the lost slice.
func (s *NoteStore) quarantineExtractionRun(ctx context.Context, scopeID string, run teamnote.ExtractionRun, cause error) (returnedErr error) {
	tx, err := s.store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin quarantine extraction run: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback quarantine extraction run: %w", rollbackErr))
		}
	}()
	result, err := tx.Exec(ctx, `
INSERT INTO extraction_runs (
    scope_id, run_id, user_id, agent_id, session_id, from_sequence, to_sequence,
    input_checksum, candidate_checksum, model, prompt_version, status, input_tokens, output_tokens,
    error, completed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'quarantined', $12, $13, $14, NOW())
ON CONFLICT (scope_id, run_id) DO NOTHING`,
		scopeID, run.ID, run.Actor.UserID, run.Actor.AgentID, run.Actor.SessionID, run.FromSequence, run.ToSequence,
		run.InputChecksum, run.CandidateChecksum, run.Model, run.PromptVersion, run.InputTokens, run.OutputTokens,
		cause.Error())
	if err != nil {
		return fmt.Errorf("insert quarantined extraction run: %w", err)
	}
	if result.RowsAffected() == 1 {
		for _, candidate := range run.Candidates {
			if err := insertRejectedCandidate(ctx, tx, scopeID, run.ID, candidate, cause.Error()); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit quarantined extraction run: %w", err)
	}
	return nil
}

func (s *NoteStore) applyExtractionRunTx(ctx context.Context, tx pgx.Tx, scopeID string, run teamnote.ExtractionRun) ([]teamnote.Note, error) {
	inserted, err := ensureExtractionRun(ctx, tx, scopeID, run)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return existingExtractionRunNotes(ctx, tx, scopeID, run)
	}
	notes := make([]teamnote.Note, 0, len(run.Candidates))
	for _, original := range run.Candidates {
		candidate := teamnote.WithEvidenceTime(original, run.Evidence)
		note, err := s.applyRunCandidate(ctx, tx, scopeID, run, candidate)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	for _, rejection := range run.Rejections {
		if err := insertRejectedCandidate(ctx, tx, scopeID, run.ID, rejection.Candidate, rejection.Reason); err != nil {
			return nil, err
		}
	}
	result, err := json.Marshal(notes)
	if err != nil {
		return nil, fmt.Errorf("encode extraction run result: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE extraction_runs SET status = 'completed', result = $3::jsonb, completed_at = NOW()
WHERE scope_id = $1 AND run_id = $2`, scopeID, run.ID, string(result)); err != nil {
		return nil, fmt.Errorf("complete extraction run: %w", err)
	}
	return notes, nil
}

func (s *NoteStore) applyRunCandidate(ctx context.Context, tx pgx.Tx, scopeID string, run teamnote.ExtractionRun, candidate teamnote.Candidate) (teamnote.Note, error) {
	inserted, err := insertCandidate(ctx, tx, scopeID, run.ID, candidate)
	if err != nil {
		return teamnote.Note{}, err
	}
	if !inserted {
		return noteForCandidate(ctx, tx, scopeID, candidate.ID)
	}
	noteKey := teamnote.CanonicalKey(candidate)
	if err := lockNoteKey(ctx, tx, scopeID, noteKey); err != nil {
		return teamnote.Note{}, err
	}
	current, err := noteForUpdate(ctx, tx, scopeID, noteKey)
	if err != nil {
		return teamnote.Note{}, err
	}
	note, err := teamnote.AdmitCandidate(s.policy, s.clock.Now(), candidate, run.Evidence, current)
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
	return note, nil
}

func (s *NoteStore) RecallNotes(ctx context.Context, scopeID string, request teamnote.RecallRequest) (envelope teamnote.NoteEnvelope, returnedErr error) {
	startedAt := time.Now()
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
	observationTime := s.clock.Now()
	eligible, err := recallableNotes(ctx, tx, scopeID, request, observationTime, queryVector, s.retrieval.EmbeddingModel)
	if err != nil {
		return teamnote.NoteEnvelope{}, err
	}
	planned, trace := teamnote.PlanRecall(eligible, request, teamnote.RecallPolicy{
		SemanticThreshold:  s.retrieval.SemanticThreshold,
		CandidateLimit:     s.retrieval.CandidateLimit,
		SuppressDuplicates: true,
		DegradeRelated:     true,
	})
	for _, delivery := range planned {
		claimed, err := insertDelivery(ctx, tx, scopeID, delivery.Note, request.Actor, delivery.Tokens)
		if err != nil {
			return teamnote.NoteEnvelope{}, err
		}
		if !claimed {
			trace.Rejections = append(trace.Rejections, teamnote.RecallRejection{
				NoteID: delivery.Note.ID, Reason: teamnote.RejectDeliveryClaim, Tokens: delivery.Tokens,
			})
			continue
		}
		teamnote.AppendPlannedRecall(&envelope, delivery)
	}
	if err := saveRecallObservation(ctx, tx, scopeID, request, envelope, observationTime, time.Since(startedAt), trace); err != nil {
		return teamnote.NoteEnvelope{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return teamnote.NoteEnvelope{}, fmt.Errorf("commit note delivery: %w", err)
	}
	return envelope, nil
}

// RecallCandidates loads the eligible retrieval candidates exactly as
// RecallNotes would, without planning, claiming, or observing deliveries.
// Evaluation tooling uses it to export deterministic recall replay fixtures.
func (s *NoteStore) RecallCandidates(ctx context.Context, scopeID string, request teamnote.RecallRequest) (candidates []teamnote.RecallCandidate, returnedErr error) {
	if strings.TrimSpace(scopeID) == "" {
		return nil, fmt.Errorf("load recall candidates: scope is required")
	}
	if err := teamnote.ValidateRecall(request); err != nil {
		return nil, err
	}
	queryVector, embeddingErr := s.embedQuery(ctx, request.Query)
	if embeddingErr != nil {
		// Semantic recall is best effort, mirroring RecallNotes.
		queryVector = nil
	}
	tx, err := s.store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin recall candidates: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback recall candidates: %w", rollbackErr))
		}
	}()
	candidates, err = recallableNotes(ctx, tx, scopeID, request, s.clock.Now(), queryVector, s.retrieval.EmbeddingModel)
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

func saveRecallObservation(
	ctx context.Context,
	tx pgx.Tx,
	scopeID string,
	request teamnote.RecallRequest,
	envelope teamnote.NoteEnvelope,
	observationTime time.Time,
	duration time.Duration,
	trace teamnote.RecallTrace,
) error {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode recall observation: %w", err)
	}
	encodedTrace, err := json.Marshal(trace)
	if err != nil {
		return fmt.Errorf("encode recall trace: %w", err)
	}
	snapshot, err := loadRecallSnapshot(ctx, tx, scopeID, observationTime)
	if err != nil {
		return err
	}
	encodedSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode recall extraction snapshot: %w", err)
	}
	provenance, err := loadRecallProvenance(ctx, tx, scopeID, observationTime)
	if err != nil {
		return err
	}
	encodedProvenance, err := json.Marshal(provenance)
	if err != nil {
		return fmt.Errorf("encode recall extraction provenance: %w", err)
	}
	queryDigest := sha256.Sum256([]byte(request.Query))
	if _, err := tx.Exec(ctx, `
INSERT INTO team_note_recall_observations (
    scope_id, recipient_user_id, recipient_agent_id, recipient_session_id,
    task_ref, thread_ref, query_digest, token_budget, max_items,
    extraction_snapshot, extraction_provenance, envelope, trace, duration_ms, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12::jsonb, $13::jsonb, $14, $15)`,
		scopeID, request.Actor.UserID, request.Actor.AgentID, request.Actor.SessionID,
		request.TaskRef, request.ThreadRef, queryDigest[:], request.TokenBudget, request.MaxItems,
		string(encodedSnapshot), string(encodedProvenance), string(encoded), string(encodedTrace), duration.Milliseconds(), observationTime,
	); err != nil {
		return fmt.Errorf("save recall observation: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM team_note_recall_observations WHERE expires_at <= NOW()`); err != nil {
		return fmt.Errorf("delete expired recall observations: %w", err)
	}
	return nil
}

func loadRecallProvenance(
	ctx context.Context,
	tx pgx.Tx,
	scopeID string,
	at time.Time,
) (recallExtractionProvenance, error) {
	var provenance recallExtractionProvenance
	err := tx.QueryRow(ctx, `
SELECT COALESCE(string_agg(DISTINCT NULLIF(model, ''), ','), ''),
       COALESCE(string_agg(DISTINCT NULLIF(prompt_version, ''), ','), ''),
       COALESCE(sum(input_tokens), 0), COALESCE(sum(output_tokens), 0),
       COALESCE(sum(EXTRACT(EPOCH FROM (completed_at - created_at)) * 1000), 0)::bigint,
       COALESCE(string_agg(NULLIF(error, ''), '; ' ORDER BY created_at), '')
FROM extraction_runs
WHERE scope_id = $1 AND completed_at <= $2`, scopeID, at).Scan(
		&provenance.Model, &provenance.PromptVersion, &provenance.InputTokens,
		&provenance.OutputTokens, &provenance.DurationMS, &provenance.Error,
	)
	if err != nil {
		return recallExtractionProvenance{}, fmt.Errorf("query recall extraction provenance: %w", err)
	}
	return provenance, nil
}

func loadRecallSnapshot(ctx context.Context, tx pgx.Tx, scopeID string, at time.Time) ([]recallSnapshotItem, error) {
	rows, err := tx.Query(ctx, `
SELECT note_id, body, COALESCE((
    SELECT array_agg(event_id ORDER BY event_id)
    FROM note_evidence
    WHERE scope_id = team_notes.scope_id
      AND note_id = team_notes.note_id
      AND revision = team_notes.current_revision
), '{}')
FROM team_notes
WHERE scope_id = $1
  AND state = 'active'
  AND invalid_at IS NULL
  AND soft_expires_at > $2
  AND hard_expires_at > $2
ORDER BY note_id`, scopeID, at)
	if err != nil {
		return nil, fmt.Errorf("query recall extraction snapshot: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (recallSnapshotItem, error) {
		var item recallSnapshotItem
		return item, row.Scan(&item.ID, &item.Text, &item.EvidenceEventIDs)
	})
	if err != nil {
		return nil, fmt.Errorf("scan recall extraction snapshot: %w", err)
	}
	return items, nil
}

func ensureExtractionRun(ctx context.Context, tx pgx.Tx, scopeID string, run teamnote.ExtractionRun) (bool, error) {
	result, err := tx.Exec(ctx, `
INSERT INTO extraction_runs (
    scope_id, run_id, user_id, agent_id, session_id, from_sequence, to_sequence,
    input_checksum, candidate_checksum, model, prompt_version, status, input_tokens, output_tokens
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'processing', $12, $13)
ON CONFLICT (scope_id, run_id) DO NOTHING`,
		scopeID, run.ID, run.Actor.UserID, run.Actor.AgentID, run.Actor.SessionID, run.FromSequence, run.ToSequence,
		run.InputChecksum, run.CandidateChecksum, run.Model, run.PromptVersion, run.InputTokens, run.OutputTokens)
	if err != nil {
		return false, fmt.Errorf("ensure extraction run: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func existingExtractionRunNotes(ctx context.Context, tx pgx.Tx, scopeID string, run teamnote.ExtractionRun) ([]teamnote.Note, error) {
	var stored teamnote.ExtractionRun
	var status string
	var storedError string
	var encodedResult []byte
	err := tx.QueryRow(ctx, `
SELECT user_id, agent_id, session_id, from_sequence, to_sequence, input_checksum,
       candidate_checksum, model, prompt_version, input_tokens, output_tokens, status, result, error
FROM extraction_runs WHERE scope_id = $1 AND run_id = $2`, scopeID, run.ID).Scan(
		&stored.Actor.UserID, &stored.Actor.AgentID, &stored.Actor.SessionID,
		&stored.FromSequence, &stored.ToSequence, &stored.InputChecksum,
		&stored.CandidateChecksum, &stored.Model, &stored.PromptVersion,
		&stored.InputTokens, &stored.OutputTokens, &status, &encodedResult, &storedError,
	)
	if err != nil {
		return nil, fmt.Errorf("load extraction run %q: %w", run.ID, err)
	}
	if !sameExtractionRunInputs(stored, run) {
		return nil, fmt.Errorf("replay extraction run %q: %w", run.ID, teamnote.ErrExtractionRunConflict)
	}
	if status == "quarantined" {
		return nil, fmt.Errorf("replay extraction run %q: %w", run.ID, errors.Join(teamnote.ErrExtractionRunQuarantined, errors.New(storedError)))
	}
	if status != "completed" {
		return nil, fmt.Errorf("replay extraction run %q with status %q: %w", run.ID, status, teamnote.ErrExtractionRunConflict)
	}
	// The durable result wins over any recomputation for the same input, so a
	// replay after a lost saved response cannot double-apply state.
	var notes []teamnote.Note
	if err := json.Unmarshal(encodedResult, &notes); err != nil {
		return nil, fmt.Errorf("decode extraction run result: %w", err)
	}
	return notes, nil
}

// sameExtractionRunInputs compares the deterministic inputs of one extraction
// run. Token usage is telemetry, and a recomputed candidate batch after a lost
// saved response must not conflict with the durable result, so neither is part
// of the replay identity.
func sameExtractionRunInputs(left, right teamnote.ExtractionRun) bool {
	return left.Actor == right.Actor && left.FromSequence == right.FromSequence &&
		left.ToSequence == right.ToSequence && left.InputChecksum == right.InputChecksum &&
		left.Model == right.Model && left.PromptVersion == right.PromptVersion
}

// insertRejectedCandidate durably records one candidate dropped before or
// during admission, so extraction evaluation can attribute lost facts.
func insertRejectedCandidate(ctx context.Context, tx pgx.Tx, scopeID, runID string, candidate teamnote.Candidate, reason string) error {
	inserted, err := insertCandidate(ctx, tx, scopeID, runID, candidate)
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE note_candidates SET admission_status = 'rejected', rejection_reason = $3
WHERE scope_id = $1 AND candidate_id = $2`, scopeID, candidate.ID, reason); err != nil {
		return fmt.Errorf("mark candidate rejected: %w", err)
	}
	return nil
}

func validateExtractionRun(scopeID string, run teamnote.ExtractionRun) error {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(run.ID) == "" ||
		strings.TrimSpace(run.Actor.UserID) == "" || strings.TrimSpace(run.Actor.AgentID) == "" ||
		strings.TrimSpace(run.Actor.SessionID) == "" || strings.TrimSpace(run.InputChecksum) == "" ||
		strings.TrimSpace(run.CandidateChecksum) == "" ||
		run.FromSequence <= 0 || run.ToSequence < run.FromSequence || run.InputTokens < 0 || run.OutputTokens < 0 {
		return fmt.Errorf("apply postgres extraction run: invalid scope or run")
	}
	seen := make(map[string]struct{}, len(run.Candidates))
	for _, candidate := range run.Candidates {
		if candidate.Origin != run.Actor {
			return fmt.Errorf("apply postgres extraction run candidate %q origin: %w", candidate.ID, teamnote.ErrInvalidCandidate)
		}
		if _, ok := seen[candidate.ID]; ok {
			return fmt.Errorf("apply postgres extraction run duplicate candidate %q: %w", candidate.ID, teamnote.ErrInvalidCandidate)
		}
		seen[candidate.ID] = struct{}{}
	}
	for _, event := range run.Evidence {
		if event.Actor != run.Actor {
			return fmt.Errorf("apply postgres extraction run evidence %q actor: %w", event.ID, teamnote.ErrMissingEvidence)
		}
	}
	return nil
}

func evidenceRange(evidence []teamnote.SessionEvent) (int64, int64) {
	if len(evidence) == 0 {
		return 0, 0
	}
	fromSequence, toSequence := evidence[0].Sequence, evidence[0].Sequence
	for _, event := range evidence[1:] {
		if event.Sequence < fromSequence {
			fromSequence = event.Sequence
		}
		if event.Sequence > toSequence {
			toSequence = event.Sequence
		}
	}
	return fromSequence, toSequence
}

func insertCandidate(ctx context.Context, tx pgx.Tx, scopeID, runID string, candidate teamnote.Candidate) (bool, error) {
	result, err := tx.Exec(ctx, `
INSERT INTO note_candidates (
    scope_id, candidate_id, run_id, action, kind, subject, identity_ref, body, task_ref,
    thread_ref, origin_user_id, origin_agent_id, origin_session_id,
    audience_agent_ids, related_subjects, evidence_event_ids, valid_at, invalid_at, source_occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
ON CONFLICT (scope_id, candidate_id) DO NOTHING`,
		scopeID, candidate.ID, runID, candidate.Action, candidate.Kind, candidate.Subject, candidate.IdentityRef,
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

func recallableNotes(ctx context.Context, tx pgx.Tx, scopeID string, request teamnote.RecallRequest, now time.Time, queryVector []float32, embeddingModel string) ([]teamnote.RecallCandidate, error) {
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
	     updated_at DESC,
	     note_id ASC
	FOR UPDATE OF team_notes`, scopeID, now, request.TaskRef, request.ThreadRef,
		request.Actor.AgentID, request.Actor.SessionID, teamnote.SearchQuery(request.Query),
		teamnote.QueryRequestsOwnContext(request.Query), request.Actor.UserID, vectorValue, embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("query recallable notes: %w", err)
	}
	notes, err := pgx.CollectRows(rows, scanRecallCandidate)
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
	targets, err := s.embeddingTargets(ctx, batchSize, s.clock.Now())
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

func (s *NoteStore) embeddingTargets(ctx context.Context, limit int, now time.Time) ([]embeddingTarget, error) {
	rows, err := s.store.pool.Query(ctx, `
SELECT scope_id, note_id, kind, subject, body, current_revision
FROM team_notes
WHERE state = 'active'
	AND invalid_at IS NULL
	AND soft_expires_at > $2
	AND hard_expires_at > $2
  AND (embedding IS NULL OR embedding_revision != current_revision OR embedding_model != $1)
ORDER BY scope_id, note_id
LIMIT $3`, s.retrieval.EmbeddingModel, now, limit)
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

func scanRecallCandidate(row pgx.CollectableRow) (teamnote.RecallCandidate, error) {
	var ranked teamnote.RecallCandidate
	err := row.Scan(
		&ranked.ID, &ranked.Key, &ranked.Kind, &ranked.Subject, &ranked.Body, &ranked.TaskRef,
		&ranked.ThreadRef, &ranked.Origin.UserID, &ranked.Origin.AgentID, &ranked.Origin.SessionID,
		&ranked.AudienceAgentIDs, &ranked.RelatedSubjects, &ranked.EvidenceEventIDs, &ranked.State,
		&ranked.Revision, &ranked.SoftExpiresAt, &ranked.HardExpiresAt, &ranked.CreatedAt, &ranked.UpdatedAt,
		&ranked.ValidAt, &ranked.InvalidAt, &ranked.SourceOccurredAt, &ranked.LexicalScore, &ranked.SemanticScore,
	)
	if err != nil {
		return teamnote.RecallCandidate{}, err
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

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
