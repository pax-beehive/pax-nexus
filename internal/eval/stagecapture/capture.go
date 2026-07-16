// Package stagecapture exports live Team Note state through the stable stage
// evaluation Observation contract.
package stagecapture

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type Target struct {
	Fixture            stageeval.Fixture
	ScopeID            string
	RecipientAgentID   string
	RecipientSessionID string
}

type Observer struct {
	pool *pgxpool.Pool
}

type extractionSnapshot struct {
	Items         []stageeval.Item
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	DurationMS    int64  `json:"duration_ms"`
	Error         string `json:"error,omitempty"`
}

func Open(ctx context.Context, dsn string) (*Observer, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("open stage capture: DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open stage capture pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping stage capture store: %w", err)
	}
	return &Observer{pool: pool}, nil
}

func (o *Observer) Close() {
	o.pool.Close()
}

func (o *Observer) Capture(ctx context.Context, target Target) (stageeval.Observation, stageeval.Observation, error) {
	if err := validateTarget(target); err != nil {
		return stageeval.Observation{}, stageeval.Observation{}, err
	}
	recall, snapshot, err := o.captureRecall(ctx, target)
	if err != nil {
		return stageeval.Observation{}, stageeval.Observation{}, err
	}
	extraction := o.captureExtraction(target, snapshot, recall.Error == "")
	return extraction, recall, nil
}

func validateTarget(target Target) error {
	if strings.TrimSpace(target.Fixture.CaseID) == "" || strings.TrimSpace(target.ScopeID) == "" ||
		strings.TrimSpace(target.RecipientAgentID) == "" || strings.TrimSpace(target.RecipientSessionID) == "" {
		return fmt.Errorf("capture stage observations: case, scope, recipient agent, and recipient session are required")
	}
	return nil
}

func (o *Observer) captureExtraction(
	target Target,
	snapshot extractionSnapshot,
	hasSnapshot bool,
) stageeval.Observation {
	observation := stageeval.Observation{
		CaseID: target.Fixture.CaseID, Stage: stageeval.StageExtraction,
		SourceRevision: target.Fixture.SourceRevision, Items: snapshot.Items, DurationMS: snapshot.DurationMS,
		Error: snapshot.Error,
		Provenance: map[string]string{
			"scope_id": target.ScopeID, "model": snapshot.Model, "prompt_version": snapshot.PromptVersion,
			"input_tokens": strconv.FormatInt(snapshot.InputTokens, 10), "output_tokens": strconv.FormatInt(snapshot.OutputTokens, 10),
		},
	}
	if !hasSnapshot {
		observation.Error = "no matching successful Team Note recall snapshot"
	}
	return observation
}

func (o *Observer) captureRecall(ctx context.Context, target Target) (stageeval.Observation, extractionSnapshot, error) {
	context := target.Fixture.RecallContext
	queryDigest := sha256.Sum256([]byte(context.Query))
	var encoded, encodedSnapshot, encodedProvenance []byte
	var durationMS int64
	err := o.pool.QueryRow(ctx, `
SELECT envelope, extraction_snapshot, extraction_provenance, duration_ms
FROM team_note_recall_observations
WHERE scope_id = $1
  AND recipient_user_id = $2
  AND recipient_agent_id = $3
  AND recipient_session_id = $4
  AND query_digest = $5
  AND token_budget = $6
ORDER BY observation_id DESC
LIMIT 1`, target.ScopeID, context.ConsumerUserID, target.RecipientAgentID,
		target.RecipientSessionID, queryDigest[:], context.TokenBudget,
	).Scan(&encoded, &encodedSnapshot, &encodedProvenance, &durationMS)
	if errors.Is(err, pgx.ErrNoRows) {
		recallContext := target.Fixture.RecallContext
		return stageeval.Observation{
			CaseID: target.Fixture.CaseID, Stage: stageeval.StageRecall,
			SourceRevision: target.Fixture.SourceRevision, RecallContext: &recallContext,
			Error: "no matching successful Team Note recall",
			Provenance: map[string]string{
				"scope_id": target.ScopeID, "recipient_agent_id": target.RecipientAgentID,
				"recipient_session_id": target.RecipientSessionID,
			},
		}, extractionSnapshot{}, nil
	}
	if err != nil {
		return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("query recall observation: %w", err)
	}
	var envelope teamnote.NoteEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("decode recall observation: %w", err)
	}
	var snapshot extractionSnapshot
	if err := json.Unmarshal(encodedSnapshot, &snapshot.Items); err != nil {
		return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("decode recall extraction snapshot: %w", err)
	}
	if err := json.Unmarshal(encodedProvenance, &snapshot); err != nil {
		return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("decode recall extraction provenance: %w", err)
	}
	items := make([]stageeval.Item, 0, len(envelope.Details))
	for _, detail := range envelope.Details {
		evidence, evidenceErr := o.recallEvidence(ctx, target.ScopeID, detail.NoteID, detail.Revision)
		if evidenceErr != nil {
			return stageeval.Observation{}, extractionSnapshot{}, evidenceErr
		}
		items = append(items, stageeval.Item{ID: detail.NoteID, Text: detail.Text, EvidenceEventIDs: evidence})
	}
	recallContext := target.Fixture.RecallContext
	return stageeval.Observation{
		CaseID: target.Fixture.CaseID, Stage: stageeval.StageRecall,
		SourceRevision: target.Fixture.SourceRevision, RecallContext: &recallContext,
		Items: items, DurationMS: durationMS,
		Provenance: map[string]string{
			"scope_id": target.ScopeID, "recipient_agent_id": target.RecipientAgentID,
			"recipient_session_id": target.RecipientSessionID,
		},
	}, snapshot, nil
}

func (o *Observer) recallEvidence(ctx context.Context, scopeID, noteID string, revision int) ([]string, error) {
	rows, err := o.pool.Query(ctx, `
SELECT event_id
FROM note_evidence
WHERE scope_id = $1 AND note_id = $2 AND revision = $3
ORDER BY event_id`, scopeID, noteID, revision)
	if err != nil {
		return nil, fmt.Errorf("query recalled note evidence: %w", err)
	}
	evidence, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
		var eventID string
		return eventID, row.Scan(&eventID)
	})
	if err != nil {
		return nil, fmt.Errorf("scan recalled note evidence: %w", err)
	}
	return evidence, nil
}
