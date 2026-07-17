// Package recallreplay replays persisted Team Note retrieval candidates
// through shared recall policy without a live store, so ranking changes can
// be validated on a fixed cohort before any paid end-to-end run.
package recallreplay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const SchemaVersion = "pax-recall-replay-v1"

// FixtureSet pins one exported recall cohort: persisted Team Notes with
// adapter retrieval scores, per-case recall requests, extraction observations,
// and gold atoms.
type FixtureSet struct {
	SchemaVersion string     `json:"schema_version"`
	Dataset       string     `json:"dataset,omitempty"`
	ExportedFrom  Provenance `json:"exported_from"`
	Policy        Policy     `json:"policy"`
	Cases         []Case     `json:"cases"`
}

// Provenance records where the replay cohort was captured.
type Provenance struct {
	RunID          string `json:"run_id"`
	Arm            string `json:"arm"`
	ScopePrefix    string `json:"scope_prefix"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
}

// Policy is the recall policy captured at export time.
type Policy struct {
	SemanticThreshold  float64 `json:"semantic_threshold"`
	CandidateLimit     int     `json:"candidate_limit"`
	SuppressDuplicates bool    `json:"suppress_duplicates,omitempty"`
	DegradeRelated     bool    `json:"degrade_related,omitempty"`
}

// Actor identifies one recall recipient.
type Actor struct {
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// Candidate is one persisted Team Note with its adapter-supplied retrieval
// scores at export time.
type Candidate struct {
	ID               string     `json:"id"`
	Kind             string     `json:"kind"`
	Subject          string     `json:"subject"`
	Body             string     `json:"body"`
	TaskRef          string     `json:"task_ref,omitempty"`
	ThreadRef        string     `json:"thread_ref,omitempty"`
	Origin           Actor      `json:"origin"`
	RelatedSubjects  []string   `json:"related_subjects,omitempty"`
	EvidenceEventIDs []string   `json:"evidence_event_ids,omitempty"`
	Revision         int        `json:"revision"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ValidAt          *time.Time `json:"valid_at,omitempty"`
	InvalidAt        *time.Time `json:"invalid_at,omitempty"`
	SourceOccurredAt time.Time  `json:"source_occurred_at"`
	LexicalScore     float64    `json:"lexical_score"`
	SemanticScore    *float64   `json:"semantic_score,omitempty"`
}

// Case pins one recall request against its persisted candidate set.
type Case struct {
	Fixture         stageeval.Fixture `json:"fixture"`
	ScopeID         string            `json:"scope_id"`
	Actor           Actor             `json:"actor"`
	ExtractionItems []stageeval.Item  `json:"extraction_items"`
	Candidates      []Candidate       `json:"candidates"`
}

// LoadFixtureSet reads a replay fixture set from disk.
func LoadFixtureSet(path string) (FixtureSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FixtureSet{}, fmt.Errorf("open recall replay fixture set: %w", err)
	}
	var set FixtureSet
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&set); err != nil {
		return FixtureSet{}, fmt.Errorf("decode recall replay fixture set: %w", err)
	}
	if err := set.Validate(); err != nil {
		return FixtureSet{}, err
	}
	return set, nil
}

// WriteFixtureSet persists a replay fixture set.
func WriteFixtureSet(path string, set FixtureSet) error {
	if err := set.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recall replay fixture set: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create recall replay fixture directory: %w", err)
		}
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write recall replay fixture set: %w", err)
	}
	return nil
}

// Validate checks the replay fixture set invariants.
func (set FixtureSet) Validate() error {
	if set.SchemaVersion != SchemaVersion {
		return fmt.Errorf("validate recall replay fixture set: schema_version must be %q", SchemaVersion)
	}
	if len(set.Cases) == 0 {
		return fmt.Errorf("validate recall replay fixture set: at least one case is required")
	}
	seen := make(map[string]struct{}, len(set.Cases))
	for _, replayCase := range set.Cases {
		if err := replayCase.Validate(); err != nil {
			return err
		}
		if _, ok := seen[replayCase.Fixture.CaseID]; ok {
			return fmt.Errorf("validate recall replay fixture set: duplicate case %q", replayCase.Fixture.CaseID)
		}
		seen[replayCase.Fixture.CaseID] = struct{}{}
	}
	return nil
}

// Validate checks one replay case.
func (replayCase Case) Validate() error {
	if replayCase.Fixture.CaseID == "" || replayCase.Fixture.SourceRevision == "" {
		return fmt.Errorf("validate recall replay case: case id and source revision are required")
	}
	if replayCase.ScopeID == "" || replayCase.Actor.UserID == "" || replayCase.Actor.AgentID == "" || replayCase.Actor.SessionID == "" {
		return fmt.Errorf("validate recall replay case %q: scope and actor are required", replayCase.Fixture.CaseID)
	}
	if replayCase.Fixture.RecallContext.TokenBudget <= 0 {
		return fmt.Errorf("validate recall replay case %q: positive token budget is required", replayCase.Fixture.CaseID)
	}
	return nil
}

// recallRequest rebuilds the recall request captured for the case.
func (replayCase Case) recallRequest() teamnote.RecallRequest {
	return teamnote.RecallRequest{
		Actor: teamnote.Actor{
			UserID:    replayCase.Actor.UserID,
			AgentID:   replayCase.Actor.AgentID,
			SessionID: replayCase.Actor.SessionID,
		},
		Query:       replayCase.Fixture.RecallContext.Query,
		TokenBudget: replayCase.Fixture.RecallContext.TokenBudget,
		MaxItems:    replayCase.Fixture.RecallContext.MaxItems,
	}
}

// recallCandidates rebuilds the adapter-scored candidates captured for the case.
func (replayCase Case) recallCandidates() []teamnote.RecallCandidate {
	candidates := make([]teamnote.RecallCandidate, 0, len(replayCase.Candidates))
	for _, pinned := range replayCase.Candidates {
		candidates = append(candidates, teamnote.RecallCandidate{
			Note: teamnote.Note{
				ID: pinned.ID, Kind: teamnote.NoteKind(pinned.Kind), Subject: pinned.Subject, Body: pinned.Body,
				TaskRef: pinned.TaskRef, ThreadRef: pinned.ThreadRef,
				Origin: teamnote.Actor{
					UserID: pinned.Origin.UserID, AgentID: pinned.Origin.AgentID, SessionID: pinned.Origin.SessionID,
				},
				RelatedSubjects:  pinned.RelatedSubjects,
				EvidenceEventIDs: pinned.EvidenceEventIDs,
				Revision:         pinned.Revision,
				CreatedAt:        pinned.CreatedAt, UpdatedAt: pinned.UpdatedAt,
				ValidAt: pinned.ValidAt, InvalidAt: pinned.InvalidAt,
				SourceOccurredAt: pinned.SourceOccurredAt,
			},
			LexicalScore: pinned.LexicalScore, SemanticScore: pinned.SemanticScore,
		})
	}
	return candidates
}
