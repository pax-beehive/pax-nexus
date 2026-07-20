package recallreplay

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/platform/textembedding"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

// Exporter captures deterministic recall replay fixtures from a persisted
// Team Note store. Candidate retrieval goes through the same NoteStore code
// path as live recall, so exported scores match production behavior.
type Exporter struct {
	store          *postgres.Store
	embedder       textembedding.Embedder
	embeddingModel string
	policy         Policy
}

// NewExporter validates exporter dependencies.
func NewExporter(store *postgres.Store, embedder textembedding.Embedder, embeddingModel string, policy Policy) (*Exporter, error) {
	if store == nil {
		return nil, fmt.Errorf("create recall replay exporter: store is required")
	}
	if embedder != nil && embeddingModel == "" {
		return nil, fmt.Errorf("create recall replay exporter: embedding model is required when embedding is enabled")
	}
	return &Exporter{store: store, embedder: embedder, embeddingModel: embeddingModel, policy: policy}, nil
}

// Export captures one replay case per stage fixture. scopeFor maps a case ID
// to its persisted Team Note scope; a fresh recipient session is used so the
// exported candidates reflect everything eligible at observation time.
func (e *Exporter) Export(ctx context.Context, fixtures stageeval.FixtureSet, provenance Provenance, scopeFor func(caseID string) string) (FixtureSet, error) {
	set := FixtureSet{
		SchemaVersion: SchemaVersion, Dataset: fixtures.Dataset,
		ExportedFrom: provenance, Policy: e.policy,
	}
	for _, fixture := range fixtures.Cases {
		replayCase, err := e.exportCase(ctx, fixture, scopeFor(fixture.CaseID))
		if err != nil {
			return FixtureSet{}, err
		}
		set.Cases = append(set.Cases, replayCase)
	}
	return set, nil
}

func (e *Exporter) exportCase(ctx context.Context, fixture stageeval.Fixture, scopeID string) (Case, error) {
	actor := Actor{
		UserID:    fixture.RecallContext.ConsumerUserID,
		AgentID:   fixture.RecallContext.ConsumerAgentID,
		SessionID: "recall-replay",
	}
	if actor.AgentID == "" {
		actor.AgentID = "recall-replay"
	}
	observationTime, err := e.observationTime(ctx, scopeID, actor, fixture.RecallContext)
	if err != nil {
		return Case{}, err
	}
	noteStore, err := postgres.NewNoteStore(e.store, teamnote.DefaultTTLPolicy(), fixedClock{now: observationTime}, postgres.RetrievalConfig{
		Embedder: e.embedder, EmbeddingModel: e.embeddingModel,
		Policy: teamnote.RecallPolicy{SemanticThreshold: e.policy.SemanticThreshold, CandidateLimit: e.policy.CandidateLimit},
	})
	if err != nil {
		return Case{}, err
	}
	request := teamnote.RecallRequest{
		Actor: teamnote.Actor{UserID: actor.UserID, AgentID: actor.AgentID, SessionID: actor.SessionID},
		Query: fixture.RecallContext.Query, TokenBudget: fixture.RecallContext.TokenBudget,
	}
	candidates, err := noteStore.RecallCandidates(ctx, scopeID, request)
	if err != nil {
		return Case{}, fmt.Errorf("export recall candidates for %q: %w", fixture.CaseID, err)
	}
	extractionItems, err := e.extractionSnapshot(ctx, scopeID, observationTime)
	if err != nil {
		return Case{}, fmt.Errorf("export extraction snapshot for %q: %w", fixture.CaseID, err)
	}
	return Case{
		Fixture: fixture, ScopeID: scopeID, Actor: actor,
		ObservationTime: observationTime,
		ExtractionItems: extractionItems, Candidates: pinCandidates(candidates),
	}, nil
}

func (e *Exporter) observationTime(
	ctx context.Context,
	scopeID string,
	actor Actor,
	recallContext stageeval.RecallContext,
) (time.Time, error) {
	queryDigest := sha256.Sum256([]byte(recallContext.Query))
	var observedAt time.Time
	err := e.store.Pool().QueryRow(ctx, `
SELECT created_at
FROM team_note_recall_observations
WHERE scope_id = $1
  AND recipient_user_id = $2
  AND recipient_agent_id = $3
  AND query_digest = $4
  AND token_budget = $5
  AND max_items = $6
ORDER BY observation_id DESC
LIMIT 1`, scopeID, actor.UserID, actor.AgentID, queryDigest[:], recallContext.TokenBudget, recallContext.MaxItems).Scan(&observedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("query recall observation time for scope %q: %w", scopeID, err)
	}
	if observedAt.IsZero() {
		return time.Time{}, fmt.Errorf("query recall observation time for scope %q: zero timestamp", scopeID)
	}
	return observedAt.UTC(), nil
}

// extractionSnapshot mirrors the recall-time snapshot eligibility from the
// note store: active, valid, and unexpired notes with current evidence.
func (e *Exporter) extractionSnapshot(ctx context.Context, scopeID string, at time.Time) ([]stageeval.Item, error) {
	rows, err := e.store.Pool().Query(ctx, `
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
  AND created_at <= $2
  AND soft_expires_at > $2
  AND hard_expires_at > $2
ORDER BY note_id`, scopeID, at)
	if err != nil {
		return nil, fmt.Errorf("query extraction snapshot: %w", err)
	}
	items := make([]stageeval.Item, 0)
	for rows.Next() {
		var item stageeval.Item
		if err := rows.Scan(&item.ID, &item.Text, &item.EvidenceEventIDs); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan extraction snapshot: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read extraction snapshot: %w", err)
	}
	rows.Close()
	return items, nil
}

func pinCandidates(candidates []teamnote.RecallCandidate) []Candidate {
	pinned := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		pinned = append(pinned, Candidate{
			ID: candidate.ID, Kind: string(candidate.Kind), Subject: candidate.Subject, Body: candidate.Body,
			TaskRef: candidate.TaskRef, ThreadRef: candidate.ThreadRef,
			Origin: Actor{
				UserID: candidate.Origin.UserID, AgentID: candidate.Origin.AgentID, SessionID: candidate.Origin.SessionID,
			},
			RelatedSubjects: candidate.RelatedSubjects, EvidenceEventIDs: candidate.EvidenceEventIDs,
			Revision:  candidate.Revision,
			CreatedAt: candidate.CreatedAt, UpdatedAt: candidate.UpdatedAt,
			ValidAt: candidate.ValidAt, InvalidAt: candidate.InvalidAt,
			SourceOccurredAt: candidate.SourceOccurredAt,
			LexicalScore:     candidate.LexicalScore, SemanticScore: candidate.SemanticScore,
		})
	}
	return pinned
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}
