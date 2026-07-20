// Package recallreplay replays persisted Team Note retrieval candidates
// through shared recall policy without a live store, so ranking changes can
// be validated on a fixed cohort before any paid end-to-end run.
package recallreplay

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

// ErrInvalidFixture identifies invalid deterministic recall replay input.
var ErrInvalidFixture = errors.New("invalid recall replay fixture")

const (
	SchemaVersion         = "pax-recall-replay-v4"
	legacySchemaVersionV3 = "pax-recall-replay-v3"
	legacySchemaVersionV2 = "pax-recall-replay-v2"
	legacySchemaVersionV1 = "pax-recall-replay-v1"
)

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
	SemanticThreshold              float64 `json:"semantic_threshold"`
	HintThreshold                  float64 `json:"hint_threshold,omitempty"`
	CandidateLimit                 int     `json:"candidate_limit"`
	EnableHintRecall               bool    `json:"enable_hint_recall,omitempty"`
	SuppressDuplicates             bool    `json:"suppress_duplicates,omitempty"`
	DegradeRelated                 bool    `json:"degrade_related,omitempty"`
	DisableRelationMarginalUtility bool    `json:"disable_relation_marginal_utility,omitempty"`
}

// Actor identifies one recall recipient.
type Actor struct {
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// EligibilityReason identifies why one extracted item is or is not available
// to the captured Recall Consumer.
type EligibilityReason string

const (
	EligibilityEligible        EligibilityReason = "eligible"
	EligibilityAuthorization   EligibilityReason = "authorization"
	EligibilityTemporal        EligibilityReason = "temporal"
	EligibilityFuture          EligibilityReason = "future"
	EligibilityTaskThread      EligibilityReason = "task_thread"
	EligibilityDelivery        EligibilityReason = "delivery"
	EligibilityAdapterExcluded EligibilityReason = "adapter_excluded"
	EligibilityMixed           EligibilityReason = "mixed"
)

// EligibilityDecision pins the adapter decision for one extraction item.
type EligibilityDecision struct {
	ItemID   string            `json:"item_id"`
	Eligible bool              `json:"eligible"`
	Reason   EligibilityReason `json:"reason"`
}

// HintRecallCall captures one focused active-recall result. It is an eval
// observation, not evidence that an agent chose to make the call.
type HintRecallCall struct {
	Items              []stageeval.Item        `json:"items"`
	OriginAttributions []HintOriginAttribution `json:"origin_attributions,omitempty"`
	DurationNS         int64                   `json:"duration_ns,omitempty"`
	Tokens             int                     `json:"tokens,omitempty"`
	CostUSD            float64                 `json:"cost_usd,omitempty"`
}

// HintOriginAttribution pins the Knowledge Origins claimed for one focused
// recall result.
type HintOriginAttribution struct {
	ItemID  string  `json:"item_id"`
	Origins []Actor `json:"origins"`
}

// EvidenceScoreObservation captures one candidate's evidence calibration
// output and whether policy admitted it as passive evidence.
type EvidenceScoreObservation struct {
	ItemID   string  `json:"item_id"`
	Score    float64 `json:"score"`
	Admitted bool    `json:"admitted"`
}

// HintObservation captures a production hint decision and its deterministic
// one- or two-call intervention. Consumer activation is evaluated separately.
type HintObservation struct {
	Exposed          bool                       `json:"exposed"`
	Score            *float64                   `json:"score"`
	FocusedQuery     string                     `json:"focused_query"`
	Consumer         Actor                      `json:"consumer"`
	ObservationTime  time.Time                  `json:"observation_time"`
	TemporalMode     string                     `json:"temporal_mode"`
	QueryTimezone    string                     `json:"query_timezone"`
	QueryTime        *time.Time                 `json:"query_time,omitempty"`
	LeadNoteIDs      []string                   `json:"lead_note_ids"`
	ClaimedNoteIDs   []string                   `json:"claimed_note_ids,omitempty"`
	LeadFingerprint  string                     `json:"lead_fingerprint,omitempty"`
	DuplicateDropped bool                       `json:"duplicate_dropped,omitempty"`
	EvidenceScores   []EvidenceScoreObservation `json:"evidence_scores,omitempty"`
	Calls            []HintRecallCall           `json:"calls"`
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
	Fixture                 stageeval.Fixture       `json:"fixture"`
	ScopeID                 string                  `json:"scope_id"`
	Actor                   Actor                   `json:"actor"`
	ObservationTime         time.Time               `json:"observation_time"`
	QueryTimezone           string                  `json:"query_timezone"`
	ExtractionItems         []stageeval.Item        `json:"extraction_items"`
	Candidates              []Candidate             `json:"candidates"`
	AtomSupports            []stageeval.AtomSupport `json:"atom_supports,omitempty"`
	EligibilityDecisions    []EligibilityDecision   `json:"eligibility_decisions"`
	CandidateSnapshotSHA256 string                  `json:"candidate_snapshot_sha256"`
	HintObservation         *HintObservation        `json:"hint_observation,omitempty"`
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
	switch set.SchemaVersion {
	case legacySchemaVersionV1:
		set.migrateLegacyObservationTimes()
		set.defaultLegacyQueryTimezones()
		set.SchemaVersion = SchemaVersion
	case legacySchemaVersionV2, legacySchemaVersionV3:
		set.defaultLegacyQueryTimezones()
		set.SchemaVersion = SchemaVersion
	case SchemaVersion:
	default:
		return FixtureSet{}, fmt.Errorf("validate recall replay fixture set: schema_version must be %q", SchemaVersion)
	}
	if err := normalizeFixtureSet(&set); err != nil {
		return FixtureSet{}, err
	}
	if err := set.Validate(); err != nil {
		return FixtureSet{}, err
	}
	return set, nil
}

// WriteFixtureSet persists a replay fixture set.
func WriteFixtureSet(path string, set FixtureSet) error {
	if err := normalizeFixtureSet(&set); err != nil {
		return err
	}
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
	if replayCase.ObservationTime.IsZero() {
		return fmt.Errorf("validate recall replay case %q: observation time is required", replayCase.Fixture.CaseID)
	}
	if replayCase.QueryTimezone == "" {
		return fmt.Errorf("%w: case %q query timezone is required", ErrInvalidFixture, replayCase.Fixture.CaseID)
	}
	if _, err := time.LoadLocation(replayCase.QueryTimezone); err != nil {
		return fmt.Errorf("%w: case %q query timezone: %w", ErrInvalidFixture, replayCase.Fixture.CaseID, err)
	}
	if len(replayCase.CandidateSnapshotSHA256) != sha256.Size*2 {
		return fmt.Errorf("validate recall replay case %q: candidate snapshot SHA-256 is required", replayCase.Fixture.CaseID)
	}
	expectedDigest, err := candidateSnapshotSHA256(replayCase.Candidates)
	if err != nil {
		return fmt.Errorf("validate recall replay case %q candidate snapshot: %w", replayCase.Fixture.CaseID, err)
	}
	if replayCase.CandidateSnapshotSHA256 != expectedDigest {
		return fmt.Errorf("validate recall replay case %q: candidate snapshot SHA-256 does not match", replayCase.Fixture.CaseID)
	}
	if len(replayCase.AtomSupports) != len(replayCase.Fixture.RequiredAtoms) {
		return fmt.Errorf("validate recall replay case %q: every required atom needs support metadata", replayCase.Fixture.CaseID)
	}
	if len(replayCase.Fixture.RequiredAtoms) > 0 {
		expectedSupports, err := stageeval.RequiredAtomSupports(replayCase.Fixture, replayCase.ExtractionItems)
		if err != nil {
			return fmt.Errorf("validate recall replay case %q atom support metadata: %w", replayCase.Fixture.CaseID, err)
		}
		if !reflect.DeepEqual(replayCase.AtomSupports, expectedSupports) {
			return fmt.Errorf("validate recall replay case %q: atom support metadata does not match extraction snapshot", replayCase.Fixture.CaseID)
		}
	}
	if err := validateEligibilityDecisions(replayCase); err != nil {
		return err
	}
	if err := validateHintObservation(replayCase); err != nil {
		return err
	}
	return nil
}

func validateHintObservation(replayCase Case) error {
	observation := replayCase.HintObservation
	if observation == nil {
		return nil
	}
	if err := validateHintCore(replayCase.Fixture.CaseID, *observation); err != nil {
		return err
	}
	if err := validateHintCalls(replayCase.Fixture.CaseID, observation.Calls); err != nil {
		return err
	}
	return validateEvidenceScoreObservations(replayCase, observation.EvidenceScores)
}

func validateHintCore(caseID string, observation HintObservation) error {
	if observation.Score == nil || *observation.Score < 0 || *observation.Score > 1 {
		return fmt.Errorf("%w: case %q hint score must be between zero and one", ErrInvalidFixture, caseID)
	}
	if observation.FocusedQuery == "" || observation.ObservationTime.IsZero() ||
		observation.TemporalMode == "" || observation.QueryTimezone == "" {
		return fmt.Errorf("%w: case %q focused query and hint temporal context are required", ErrInvalidFixture, caseID)
	}
	if _, err := time.LoadLocation(observation.QueryTimezone); err != nil {
		return fmt.Errorf("%w: case %q query timezone: %w", ErrInvalidFixture, caseID, err)
	}
	if len(observation.LeadNoteIDs) == 0 || len(observation.Calls) == 0 || len(observation.Calls) > 2 {
		return fmt.Errorf("%w: case %q hint lead and one or two intervention calls are required", ErrInvalidFixture, caseID)
	}
	return nil
}

func validateHintCalls(caseID string, calls []HintRecallCall) error {
	for _, call := range calls {
		if call.DurationNS < 0 || call.Tokens < 0 || call.CostUSD < 0 {
			return fmt.Errorf("%w: case %q hint call usage cannot be negative", ErrInvalidFixture, caseID)
		}
	}
	return nil
}

func validateEvidenceScoreObservations(replayCase Case, scores []EvidenceScoreObservation) error {
	seenScores := make(map[string]struct{}, len(scores))
	knownItems := make(map[string]struct{}, len(replayCase.ExtractionItems))
	for _, item := range replayCase.ExtractionItems {
		knownItems[item.ID] = struct{}{}
	}
	for _, score := range scores {
		if score.ItemID == "" || score.Score < 0 || score.Score > 1 {
			return fmt.Errorf("%w: case %q evidence score item and probability are required", ErrInvalidFixture, replayCase.Fixture.CaseID)
		}
		if _, duplicate := seenScores[score.ItemID]; duplicate {
			return fmt.Errorf("%w: case %q duplicate evidence score for %q", ErrInvalidFixture, replayCase.Fixture.CaseID, score.ItemID)
		}
		if _, known := knownItems[score.ItemID]; !known {
			return fmt.Errorf("%w: case %q evidence score item %q is not in extraction snapshot", ErrInvalidFixture, replayCase.Fixture.CaseID, score.ItemID)
		}
		seenScores[score.ItemID] = struct{}{}
	}
	return nil
}

func (set *FixtureSet) migrateLegacyObservationTimes() {
	for index := range set.Cases {
		replayCase := &set.Cases[index]
		if set.SchemaVersion != SchemaVersion && replayCase.QueryTimezone == "" {
			replayCase.QueryTimezone = "UTC"
		}
		replayCase.ObservationTime = inferredObservationTime(replayCase.Candidates)
		if len(replayCase.ExtractionItems) == 0 {
			for _, candidate := range replayCase.Candidates {
				replayCase.ExtractionItems = append(replayCase.ExtractionItems, stageeval.Item{
					ID: candidate.ID, Text: candidate.Body, EvidenceEventIDs: append([]string(nil), candidate.EvidenceEventIDs...),
				})
			}
		}
	}
}

func (set *FixtureSet) defaultLegacyQueryTimezones() {
	for index := range set.Cases {
		if set.Cases[index].QueryTimezone == "" {
			set.Cases[index].QueryTimezone = "UTC"
		}
	}
}

func normalizeFixtureSet(set *FixtureSet) error {
	for index := range set.Cases {
		replayCase := &set.Cases[index]
		if set.SchemaVersion != SchemaVersion && replayCase.QueryTimezone == "" {
			replayCase.QueryTimezone = "UTC"
		}
		if replayCase.CandidateSnapshotSHA256 == "" {
			digest, err := candidateSnapshotSHA256(replayCase.Candidates)
			if err != nil {
				return fmt.Errorf("digest recall replay candidate snapshot for %q: %w", replayCase.Fixture.CaseID, err)
			}
			replayCase.CandidateSnapshotSHA256 = digest
		}
		if len(replayCase.AtomSupports) == 0 && len(replayCase.Fixture.RequiredAtoms) > 0 {
			supports, err := stageeval.RequiredAtomSupports(replayCase.Fixture, replayCase.ExtractionItems)
			if err != nil {
				return fmt.Errorf("derive recall replay atom support for %q: %w", replayCase.Fixture.CaseID, err)
			}
			replayCase.AtomSupports = supports
		}
		if len(replayCase.EligibilityDecisions) == 0 {
			replayCase.EligibilityDecisions = defaultEligibilityDecisions(*replayCase)
		}
	}
	return nil
}

func defaultEligibilityDecisions(replayCase Case) []EligibilityDecision {
	ineligible := make(map[string]EligibilityReason)
	_, trace := teamnote.PlanRecall(replayCase.recallCandidates(), replayCase.recallRequest(), teamnote.RecallPolicy{
		ObservationTime: replayCase.ObservationTime,
	})
	queryTime := temporalQueryTime(trace.Intent, replayCase.ObservationTime)
	candidates := make(map[string]Candidate, len(replayCase.Candidates))
	for _, candidate := range replayCase.Candidates {
		candidates[candidate.ID] = candidate
	}
	for _, candidateTrace := range trace.CandidateTraces {
		if candidateTrace.TemporalResolution.GatePassed {
			continue
		}
		reason := EligibilityTemporal
		candidate := candidates[candidateTrace.NoteID]
		if queryTime != nil && candidate.ValidAt != nil && candidate.ValidAt.After(*queryTime) {
			reason = EligibilityFuture
		}
		ineligible[candidateTrace.NoteID] = reason
	}
	decisions := make([]EligibilityDecision, 0, len(replayCase.ExtractionItems))
	for _, item := range replayCase.ExtractionItems {
		reason, excluded := ineligible[item.ID]
		if excluded {
			decisions = append(decisions, EligibilityDecision{ItemID: item.ID, Reason: reason})
			continue
		}
		decisions = append(decisions, EligibilityDecision{ItemID: item.ID, Eligible: true, Reason: EligibilityEligible})
	}
	return decisions
}

func validateEligibilityDecisions(replayCase Case) error {
	items := make(map[string]struct{}, len(replayCase.ExtractionItems))
	for _, item := range replayCase.ExtractionItems {
		items[item.ID] = struct{}{}
	}
	decisions := make(map[string]EligibilityDecision, len(replayCase.EligibilityDecisions))
	for _, decision := range replayCase.EligibilityDecisions {
		if _, ok := items[decision.ItemID]; !ok {
			return fmt.Errorf("%w: case %q eligibility item %q is not in extraction snapshot", ErrInvalidFixture, replayCase.Fixture.CaseID, decision.ItemID)
		}
		if _, duplicate := decisions[decision.ItemID]; duplicate {
			return fmt.Errorf("%w: case %q duplicate eligibility item %q", ErrInvalidFixture, replayCase.Fixture.CaseID, decision.ItemID)
		}
		if !validEligibilityReason(decision.Reason) || decision.Eligible != (decision.Reason == EligibilityEligible) {
			return fmt.Errorf("%w: case %q invalid eligibility decision for %q", ErrInvalidFixture, replayCase.Fixture.CaseID, decision.ItemID)
		}
		decisions[decision.ItemID] = decision
	}
	if len(decisions) != len(items) {
		return fmt.Errorf("%w: case %q every extraction item needs eligibility metadata", ErrInvalidFixture, replayCase.Fixture.CaseID)
	}
	for _, candidate := range replayCase.Candidates {
		if _, ok := decisions[candidate.ID]; !ok {
			return fmt.Errorf("%w: case %q candidate %q needs eligibility metadata", ErrInvalidFixture, replayCase.Fixture.CaseID, candidate.ID)
		}
	}
	return nil
}

func validEligibilityReason(reason EligibilityReason) bool {
	switch reason {
	case EligibilityEligible, EligibilityAuthorization, EligibilityTemporal, EligibilityFuture,
		EligibilityTaskThread, EligibilityDelivery, EligibilityAdapterExcluded, EligibilityMixed:
		return true
	default:
		return false
	}
}

func candidateSnapshotSHA256(candidates []Candidate) (string, error) {
	encoded, err := json.Marshal(candidates)
	if err != nil {
		return "", fmt.Errorf("encode candidates: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:]), nil
}

func inferredObservationTime(candidates []Candidate) time.Time {
	var result time.Time
	for _, candidate := range candidates {
		for _, value := range []time.Time{candidate.SourceOccurredAt, candidate.UpdatedAt, candidate.CreatedAt} {
			if value.After(result) {
				result = value.UTC()
			}
		}
	}
	if result.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return result
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

func (replayCase Case) plannerRecallCandidates() []teamnote.RecallCandidate {
	decisions := eligibilityByItem(replayCase.EligibilityDecisions)
	all := replayCase.recallCandidates()
	result := make([]teamnote.RecallCandidate, 0, len(all))
	for _, candidate := range all {
		if adapterExcludesCandidate(decisions[candidate.ID]) {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func adapterExcludesCandidate(decision EligibilityDecision) bool {
	if decision.Eligible {
		return false
	}
	switch decision.Reason {
	case EligibilityAuthorization, EligibilityTaskThread, EligibilityDelivery, EligibilityAdapterExcluded, EligibilityMixed:
		return true
	default:
		return false
	}
}
