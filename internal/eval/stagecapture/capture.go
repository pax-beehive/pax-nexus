// Package stagecapture exports live Team Note state through the stable stage
// evaluation Observation contract.
package stagecapture

import (
	"context"
	"encoding/json"
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

type persistedRecall struct {
	Envelope             []byte
	ExtractionSnapshot   []byte
	ExtractionProvenance []byte
	Trace                []byte
	DurationMS           int64
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
	rows, err := o.pool.Query(ctx, `
SELECT envelope, extraction_snapshot, extraction_provenance, trace, duration_ms
FROM team_note_recall_observations
WHERE scope_id = $1
  AND recipient_user_id = $2
  AND recipient_agent_id = $3
  AND recipient_session_id = $4
  AND token_budget = $5
ORDER BY observation_id`, target.ScopeID, context.ConsumerUserID, target.RecipientAgentID,
		target.RecipientSessionID, context.TokenBudget,
	)
	if err != nil {
		return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("query recall observations: %w", err)
	}
	recalls, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (persistedRecall, error) {
		var recall persistedRecall
		return recall, row.Scan(
			&recall.Envelope, &recall.ExtractionSnapshot, &recall.ExtractionProvenance, &recall.Trace, &recall.DurationMS,
		)
	})
	if err != nil {
		return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("scan recall observations: %w", err)
	}
	if len(recalls) == 0 {
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
	var snapshot extractionSnapshot
	if err := json.Unmarshal(recalls[0].ExtractionSnapshot, &snapshot.Items); err != nil {
		return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("decode recall extraction snapshot: %w", err)
	}
	if err := json.Unmarshal(recalls[0].ExtractionProvenance, &snapshot); err != nil {
		return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("decode recall extraction provenance: %w", err)
	}
	items := make([]stageeval.Item, 0)
	seen := make(map[string]struct{})
	traces := make([]json.RawMessage, 0, len(recalls))
	var durationMS int64
	for _, persisted := range recalls {
		durationMS += persisted.DurationMS
		if len(persisted.Trace) > 0 && string(persisted.Trace) != "{}" {
			traces = append(traces, json.RawMessage(persisted.Trace))
		}
		var envelope teamnote.NoteEnvelope
		if err := json.Unmarshal(persisted.Envelope, &envelope); err != nil {
			return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("decode recall observation: %w", err)
		}
		for _, detail := range envelope.Details {
			key := fmt.Sprintf("%s:%d", detail.NoteID, detail.Revision)
			if _, exists := seen[key]; exists {
				continue
			}
			evidence, evidenceErr := o.recallEvidence(ctx, target.ScopeID, detail.NoteID, detail.Revision)
			if evidenceErr != nil {
				return stageeval.Observation{}, extractionSnapshot{}, evidenceErr
			}
			seen[key] = struct{}{}
			items = append(items, stageeval.Item{
				ID: detail.NoteID, SourceItemIDs: detail.SourceNoteIDs,
				Text: detail.Text, EvidenceEventIDs: evidence,
			})
		}
	}
	recallContext := target.Fixture.RecallContext
	provenance := map[string]string{
		"scope_id": target.ScopeID, "recipient_agent_id": target.RecipientAgentID,
		"recipient_session_id": target.RecipientSessionID,
		"recall_count":         strconv.Itoa(len(recalls)),
	}
	if len(traces) > 0 {
		encodedTraces, err := json.Marshal(traces)
		if err != nil {
			return stageeval.Observation{}, extractionSnapshot{}, fmt.Errorf("encode recall traces: %w", err)
		}
		provenance["recall_traces"] = string(encodedTraces)
	}
	return stageeval.Observation{
		CaseID: target.Fixture.CaseID, Stage: stageeval.StageRecall,
		SourceRevision: target.Fixture.SourceRevision, RecallContext: &recallContext,
		Items: items, DurationMS: durationMS,
		Provenance: provenance,
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
