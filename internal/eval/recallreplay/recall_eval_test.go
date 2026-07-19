package recallreplay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type recallEvalSuite struct {
	suite.Suite
}

type contractStore struct {
	envelopes map[string]teamnote.NoteEnvelope
	errors    map[string]error
	requests  []teamnote.RecallRequest
}

func (s *contractStore) RecallNotes(_ context.Context, scopeID string, request teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
	s.requests = append(s.requests, request)
	if err := s.errors[scopeID]; err != nil {
		return teamnote.NoteEnvelope{}, err
	}
	return s.envelopes[scopeID], nil
}

func (s *recallEvalSuite) TestEvaluateUnauthorizedInfluencePairUsesRealRecallNotesContract() {
	consumer := teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "recipient"}
	request := teamnote.RecallRequest{Actor: consumer, Query: "release owner", TokenBudget: 100}
	visible := teamnote.RecalledNote{
		NoteID: "different-per-scope", SourceNoteIDs: []string{"visible"}, Revision: 1,
		Text: "Release owner is Alice.", Origin: teamnote.Actor{UserID: "owner", AgentID: "producer"},
		Relevance: 0.9, Certainty: teamnote.CertaintyConfirmed,
	}
	challengeVisible := visible
	challengeVisible.NoteID = "challenge-visible"
	store := &contractStore{
		envelopes: map[string]teamnote.NoteEnvelope{
			"baseline":  {Items: []string{visible.Text}, Tokens: 4, Details: []teamnote.RecalledNote{visible}},
			"challenge": {Items: []string{visible.Text}, Tokens: 4, Details: []teamnote.RecalledNote{challengeVisible}},
		},
	}

	result, err := recallreplay.EvaluateUnauthorizedInfluencePair(context.Background(), store, recallreplay.UnauthorizedInfluencePair{
		BaselineScopeID: "baseline", ChallengeScopeID: "challenge", UnauthorizedNoteID: "secret", Request: request,
	})

	s.Require().NoError(err)
	s.True(result.UnauthorizedExcluded)
	s.True(result.ZeroInfluence)
	s.Zero(result.OutputDifferences)
	s.Len(store.requests, 2)
	s.Equal(consumer, store.requests[0].Actor)
	s.Equal(consumer, store.requests[1].Actor)
}

func (s *recallEvalSuite) TestEvaluateUnauthorizedInfluencePairErrorMatrix() {
	baselineErr := errors.New("baseline unavailable")
	challengeErr := errors.New("challenge unavailable")
	validPair := recallreplay.UnauthorizedInfluencePair{
		BaselineScopeID: "baseline", ChallengeScopeID: "challenge", UnauthorizedNoteID: "secret",
		Request: teamnote.RecallRequest{
			Actor: teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "recipient"},
			Query: "release owner", TokenBudget: 100,
		},
	}
	tests := []struct {
		name    string
		store   recallreplay.RecallNotesContract
		pair    recallreplay.UnauthorizedInfluencePair
		wantErr error
	}{
		{name: "missing store", pair: validPair, wantErr: recallreplay.ErrInvalidUnauthorizedInfluencePair},
		{
			name: "invalid pair", store: &contractStore{},
			pair: recallreplay.UnauthorizedInfluencePair{}, wantErr: recallreplay.ErrInvalidUnauthorizedInfluencePair,
		},
		{
			name: "baseline failure", store: &contractStore{errors: map[string]error{"baseline": baselineErr}},
			pair: validPair, wantErr: baselineErr,
		},
		{
			name: "challenge failure", store: &contractStore{errors: map[string]error{"challenge": challengeErr}},
			pair: validPair, wantErr: challengeErr,
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := recallreplay.EvaluateUnauthorizedInfluencePair(context.Background(), test.store, test.pair)
			s.ErrorIs(err, test.wantErr)
		})
	}
}

func (s *recallEvalSuite) TestWriteLossLedgerJSONLWritesOneAtomPerLine() {
	report := recallreplay.Report{LossLedger: []recallreplay.AtomLoss{
		{CaseID: "case-1", AtomID: "atom-1", Available: true, LostAt: recallreplay.LossStageCandidateRetrieval},
		{CaseID: "case-1", AtomID: "atom-2", Available: false},
	}}
	var output bytes.Buffer

	s.Require().NoError(recallreplay.WriteLossLedgerJSONL(&output, report))
	decoder := json.NewDecoder(&output)
	var first, second recallreplay.AtomLoss
	s.Require().NoError(decoder.Decode(&first))
	s.Require().NoError(decoder.Decode(&second))
	s.Equal("atom-1", first.AtomID)
	s.Equal("atom-2", second.AtomID)
}

func (s *recallEvalSuite) TestFixtureV4PersistsEligibilityAtomSupportAndCandidateSnapshotDigest() {
	replayCase := syntheticCase("fixture-v3", "deploy window friday", []recallreplay.Candidate{
		syntheticCandidate("note-hit", "deploy window", "Deploy window moves to Friday.", 0.9, nil),
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deploy", Patterns: []string{"(?i)deploy window"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}
	path := filepath.Join(s.T().TempDir(), "fixture.json")

	s.Require().NoError(recallreplay.WriteFixtureSet(path, set))
	loaded, err := recallreplay.LoadFixtureSet(path)
	s.Require().NoError(err)
	s.Equal("pax-recall-replay-v4", loaded.SchemaVersion)
	s.Require().Len(loaded.Cases[0].AtomSupports, 1)
	s.Equal("deploy", loaded.Cases[0].AtomSupports[0].AtomID)
	s.Equal([]string{"note-hit"}, loaded.Cases[0].AtomSupports[0].ItemIDs)
	s.Len(loaded.Cases[0].CandidateSnapshotSHA256, 64)
	s.Equal([]recallreplay.EligibilityDecision{
		{ItemID: "note-hit", Eligible: true, Reason: recallreplay.EligibilityEligible},
	}, loaded.Cases[0].EligibilityDecisions)
}

func (s *recallEvalSuite) TestFixtureV4RejectsInvalidEligibilityAndHintMatrices() {
	tests := []struct {
		name   string
		mutate func(*recallreplay.Case)
	}{
		{
			name: "unknown eligibility reason",
			mutate: func(replayCase *recallreplay.Case) {
				replayCase.EligibilityDecisions = []recallreplay.EligibilityDecision{{ItemID: "note-hit", Reason: "typo"}}
			},
		},
		{
			name: "missing native timezone",
			mutate: func(replayCase *recallreplay.Case) {
				replayCase.QueryTimezone = ""
			},
		},
		{
			name: "hint score outside probability range",
			mutate: func(replayCase *recallreplay.Case) {
				observation := validHintObservation(*replayCase)
				score := 1.1
				observation.Score = &score
				replayCase.HintObservation = &observation
			},
		},
		{
			name: "negative call usage",
			mutate: func(replayCase *recallreplay.Case) {
				observation := validHintObservation(*replayCase)
				observation.Calls[0].Tokens = -1
				replayCase.HintObservation = &observation
			},
		},
		{
			name: "duplicate evidence score",
			mutate: func(replayCase *recallreplay.Case) {
				observation := validHintObservation(*replayCase)
				observation.EvidenceScores = []recallreplay.EvidenceScoreObservation{
					{ItemID: "note-hit", Score: 0.8}, {ItemID: "note-hit", Score: 0.7},
				}
				replayCase.HintObservation = &observation
			},
		},
		{
			name: "unknown eligibility item",
			mutate: func(replayCase *recallreplay.Case) {
				replayCase.EligibilityDecisions = []recallreplay.EligibilityDecision{{
					ItemID: "unknown", Eligible: true, Reason: recallreplay.EligibilityEligible,
				}}
			},
		},
		{
			name: "duplicate eligibility item",
			mutate: func(replayCase *recallreplay.Case) {
				decision := recallreplay.EligibilityDecision{ItemID: "note-hit", Eligible: true, Reason: recallreplay.EligibilityEligible}
				replayCase.EligibilityDecisions = []recallreplay.EligibilityDecision{decision, decision}
			},
		},
		{
			name: "incomplete eligibility coverage",
			mutate: func(replayCase *recallreplay.Case) {
				replayCase.ExtractionItems = append(replayCase.ExtractionItems, stageeval.Item{ID: "missing", Text: "unrelated"})
				replayCase.EligibilityDecisions = []recallreplay.EligibilityDecision{{
					ItemID: "note-hit", Eligible: true, Reason: recallreplay.EligibilityEligible,
				}}
			},
		},
		{
			name: "candidate missing eligibility metadata",
			mutate: func(replayCase *recallreplay.Case) {
				replayCase.ExtractionItems[0].ID = "other-item"
				replayCase.EligibilityDecisions = []recallreplay.EligibilityDecision{{
					ItemID: "other-item", Eligible: true, Reason: recallreplay.EligibilityEligible,
				}}
			},
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			replayCase := syntheticCase("invalid-v4", "deploy window friday", []recallreplay.Candidate{
				syntheticCandidate("note-hit", "deploy window", "Deploy window moves to Friday.", 0.9, nil),
			})
			replayCase.Fixture.RequiredAtoms = []stageeval.Atom{{ID: "deploy", Patterns: []string{"(?i)deploy window"}}}
			test.mutate(&replayCase)
			set := recallreplay.FixtureSet{
				SchemaVersion: recallreplay.SchemaVersion,
				Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
				Cases:         []recallreplay.Case{replayCase},
			}

			err := recallreplay.WriteFixtureSet(filepath.Join(s.T().TempDir(), "fixture.json"), set)

			s.ErrorIs(err, recallreplay.ErrInvalidFixture)
		})
	}
}

func validHintObservation(replayCase recallreplay.Case) recallreplay.HintObservation {
	score := 0.8
	queryTime := replayCase.ObservationTime
	return recallreplay.HintObservation{
		Score: &score, FocusedQuery: replayCase.Fixture.RecallContext.Query,
		Consumer: replayCase.Actor, ObservationTime: replayCase.ObservationTime,
		TemporalMode: "current", QueryTime: &queryTime, QueryTimezone: replayCase.QueryTimezone,
		LeadNoteIDs: []string{"note-hit"},
		Calls:       []recallreplay.HintRecallCall{{Items: []stageeval.Item{}}},
	}
}

func (s *recallEvalSuite) TestLoadRejectsCandidateSnapshotDigestMismatch() {
	replayCase := syntheticCase("fixture-digest", "deploy window friday", []recallreplay.Candidate{
		syntheticCandidate("note-hit", "deploy window", "Deploy window moves to Friday.", 0.9, nil),
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deploy", Patterns: []string{"(?i)deploy window"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}
	path := filepath.Join(s.T().TempDir(), "fixture.json")
	s.Require().NoError(recallreplay.WriteFixtureSet(path, set))
	encoded, err := os.ReadFile(path)
	s.Require().NoError(err)
	tampered := strings.ReplaceAll(string(encoded), "Deploy window moves to Friday.", "Deploy window moves to Monday.")
	s.NotEqual(string(encoded), tampered)
	s.Require().NoError(os.WriteFile(path, []byte(tampered), 0o600))

	_, err = recallreplay.LoadFixtureSet(path)
	s.Require().ErrorContains(err, "candidate snapshot SHA-256 does not match")
}

func (s *recallEvalSuite) TestLoadRejectsAtomSupportThatDoesNotMatchExtractionSnapshot() {
	replayCase := syntheticCase("fixture-support", "deploy window friday", []recallreplay.Candidate{
		syntheticCandidate("note-hit", "deploy window", "Deploy window moves to Friday.", 0.9, nil),
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deploy", Patterns: []string{"(?i)deploy window"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}
	path := filepath.Join(s.T().TempDir(), "fixture.json")
	s.Require().NoError(recallreplay.WriteFixtureSet(path, set))
	encoded, err := os.ReadFile(path)
	s.Require().NoError(err)
	tampered := strings.Replace(string(encoded), "Deploy window moves to Friday.", "Unrelated extraction text.", 1)
	s.NotEqual(string(encoded), tampered)
	s.Require().NoError(os.WriteFile(path, []byte(tampered), 0o600))

	_, err = recallreplay.LoadFixtureSet(path)
	s.Require().ErrorContains(err, "atom support metadata does not match extraction snapshot")
}

func TestRecallEvalSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(recallEvalSuite))
}

func (s *recallEvalSuite) TestRunAttributesAvailableAtomLostAtCandidateRetrieval() {
	replayCase := syntheticCase("candidate-loss", "deploy window friday", []recallreplay.Candidate{
		syntheticCandidate("note-hit", "deploy window", "Deploy window moves to Friday.", 0.9, nil),
		syntheticCandidate("note-miss", "pancake meetup", "Pancake recipe swap meetup.", 0, nil),
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deploy", Patterns: []string{"(?i)deploy window"}},
		{ID: "pancake", Patterns: []string{"(?i)pancake recipe"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 1},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(2, report.RecallEval.AvailableAtoms)
	s.Equal(1, report.RecallEval.CandidateMatchedAtoms)
	s.InDelta(0.5, report.RecallEval.CandidateRecallAtLimit, 0.0001)
	s.Equal(1, report.RecallEval.DeliveredMatchedAtoms)
	s.InDelta(0.5, report.RecallEval.DeliveredConditionalRecall, 0.0001)
	s.Require().Len(report.LossLedger, 2)
	s.Equal(recallreplay.LossStageCandidateRetrieval, report.LossLedger[1].LostAt)
	s.Equal("fusion_limit", report.LossLedger[1].Reason)
}

func (s *recallEvalSuite) TestRunUsesEligibleAtomsForConsumerScopedRecall() {
	replayCase := syntheticCase("identity-eligibility", "deployment status", []recallreplay.Candidate{
		syntheticCandidate("note-visible", "deployment status", "Deployment status is ready.", 0.9, nil),
	})
	replayCase.ExtractionItems = append(replayCase.ExtractionItems, stageeval.Item{
		ID: "note-hidden", Text: "Deployment secret is blocked.", EvidenceEventIDs: []string{"event-hidden"},
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "visible", Patterns: []string{"(?i)deployment status is ready"}},
		{ID: "hidden", Patterns: []string{"(?i)deployment secret is blocked"}},
	}
	replayCase.EligibilityDecisions = []recallreplay.EligibilityDecision{
		{ItemID: "note-visible", Eligible: true, Reason: recallreplay.EligibilityEligible},
		{ItemID: "note-hidden", Eligible: false, Reason: recallreplay.EligibilityAuthorization},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(2, report.RecallEval.AvailableAtoms)
	s.Equal(1, report.RecallEval.EligibleAtoms)
	s.Equal(1, report.RecallEval.IneligibleAtoms)
	s.InDelta(0.5, report.RecallEval.DeliveredConditionalRecall, 0.0001)
	s.InDelta(1, report.RecallEval.DeliveredEligibleRecall, 0.0001)
	s.Require().Len(report.LossLedger, 2)
	s.True(report.LossLedger[0].Eligible)
	s.False(report.LossLedger[1].Eligible)
	s.Equal(recallreplay.EligibilityAuthorization, report.LossLedger[1].EligibilityReason)
	s.Empty(report.LossLedger[1].LostAt)
}

func (s *recallEvalSuite) TestRunFiltersAdapterIneligibleCandidatesBeforePlanner() {
	tests := []struct {
		name   string
		reason recallreplay.EligibilityReason
	}{
		{name: "authorization", reason: recallreplay.EligibilityAuthorization},
		{name: "task thread", reason: recallreplay.EligibilityTaskThread},
		{name: "delivery", reason: recallreplay.EligibilityDelivery},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			candidate := syntheticCandidate("restricted", "release", "Release owner is Mallory.", 0.9, nil)
			replayCase := syntheticCase("adapter-filter-"+test.name, "Who owns the release?", []recallreplay.Candidate{candidate})
			replayCase.Fixture.RequiredAtoms = []stageeval.Atom{{ID: "owner", Patterns: []string{"(?i)release owner is mallory"}}}
			replayCase.EligibilityDecisions = []recallreplay.EligibilityDecision{{ItemID: candidate.ID, Reason: test.reason}}
			set := recallreplay.FixtureSet{
				SchemaVersion: recallreplay.SchemaVersion,
				Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
				Cases:         []recallreplay.Case{replayCase},
			}

			report, err := recallreplay.Run(set, set.Policy)
			s.Require().NoError(err)
			s.Empty(report.Cases[0].Trace.CandidateTraces)
			s.Empty(report.Cases[0].PlannedItems)
			s.False(report.LossLedger[0].Eligible)
			s.Equal(test.reason, report.LossLedger[0].EligibilityReason)
		})
	}
}

func (s *recallEvalSuite) TestRunSlicesEligibleRecallByKnowledgeOriginRelationship() {
	sameAgent := syntheticCandidate("note-self", "alpha status", "Alpha status is ready.", 0.9, nil)
	sameAgent.Origin = recallreplay.Actor{UserID: "User_1", AgentID: "consumer", SessionID: "source-self"}
	crossAgent := syntheticCandidate("note-peer", "beta status", "Beta status is blocked.", 0.8, nil)
	replayCase := syntheticCase("identity-slices", "alpha beta status", []recallreplay.Candidate{sameAgent, crossAgent})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "self", Patterns: []string{"(?i)alpha status is ready"}},
		{ID: "peer", Patterns: []string{"(?i)beta status is blocked"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(1, report.RecallEval.IdentitySlices[recallreplay.IdentitySameAgent].EligibleAtoms)
	s.Equal(1, report.RecallEval.IdentitySlices[recallreplay.IdentitySameAgent].DeliveredMatchedAtoms)
	s.Equal(1, report.RecallEval.IdentitySlices[recallreplay.IdentityCrossUserAgent].EligibleAtoms)
	s.InDelta(1, report.RecallEval.IdentitySlices[recallreplay.IdentityCrossUserAgent].DeliveredConditionalRecall, 0.0001)
	s.Equal(recallreplay.IdentitySameAgent, report.LossLedger[0].IdentityRelationship)
	s.Equal(recallreplay.IdentityCrossUserAgent, report.LossLedger[1].IdentityRelationship)
	s.False(report.LossLedger[0].StrictCrossAgent)
	s.True(report.LossLedger[1].StrictCrossAgent)
	s.Equal(1, report.RecallEval.StrictCrossAgent.EligibleAtoms)
	s.Equal(replayCase.Actor, report.LossLedger[0].Consumer)
	s.Equal(sameAgent.Origin, report.LossLedger[0].KnowledgeOrigins[0])
}

func (s *recallEvalSuite) TestRunSlicesRecallByTemporalModeAndPinsThreeTimes() {
	candidate := syntheticCandidate("note-history", "release status", "Release status was queued on July 15.", 0.9, nil)
	validAt := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	invalidAt := time.Date(2026, time.July, 16, 0, 0, 0, 0, time.UTC)
	candidate.ValidAt = &validAt
	candidate.InvalidAt = &invalidAt
	candidate.SourceOccurredAt = time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	replayCase := syntheticCase("as-of-slice", "What was release status as of 2026-07-15?", []recallreplay.Candidate{candidate})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "historical-status", Patterns: []string{"(?i)release status was queued"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	asOf := report.RecallEval.TemporalSlices["as_of"]
	s.Equal(1, asOf.EligibleAtoms, "%+v", report.LossLedger)
	s.InDelta(1, asOf.DeliveredConditionalRecall, 0.0001)
	loss := report.LossLedger[0]
	s.Equal("as_of", loss.TemporalMode)
	s.Equal(replayCase.ObservationTime, loss.ObservationTime)
	s.Require().NotNil(loss.QueryTime)
	s.Equal(time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC), *loss.QueryTime)
	s.Equal([]time.Time{candidate.SourceOccurredAt}, loss.SourceTimes)
}

func (s *recallEvalSuite) TestRunScoresCapturedHintOpportunityWithoutAgentActivation() {
	passive := syntheticCandidate("note-passive", "release", "Release owner is Alice.", 0.9, nil)
	active := syntheticCandidate("note-active", "rollout", "The rollout deadline is Friday.", 0, nil)
	replayCase := syntheticCase("hint-opportunity", "Who owns the release and what is the rollout deadline?", []recallreplay.Candidate{passive, active})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "owner", Patterns: []string{"(?i)release owner is alice"}},
		{ID: "deadline", Patterns: []string{"(?i)rollout deadline is friday"}},
	}
	score := 0.9
	queryTime := replayCase.ObservationTime
	replayCase.HintObservation = &recallreplay.HintObservation{
		Exposed: true, Score: &score, FocusedQuery: "What is the rollout deadline?",
		Consumer: replayCase.Actor, ObservationTime: replayCase.ObservationTime,
		TemporalMode: "current", QueryTime: &queryTime, QueryTimezone: replayCase.QueryTimezone,
		LeadNoteIDs: []string{active.ID},
		EvidenceScores: []recallreplay.EvidenceScoreObservation{
			{ItemID: passive.ID, Score: 0.9, Admitted: true},
			{ItemID: active.ID, Score: 0.4},
		},
		Calls: []recallreplay.HintRecallCall{{
			Items: []stageeval.Item{{
				ID: "active-result", SourceItemIDs: []string{active.ID}, Text: active.Body,
				EvidenceEventIDs: active.EvidenceEventIDs,
			}},
			OriginAttributions: []recallreplay.HintOriginAttribution{{
				ItemID: "active-result", Origins: []recallreplay.Actor{active.Origin},
			}},
		}},
	}
	replayCase.HintObservation.LeadFingerprint = "rollout-deadline"
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 1},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(1, report.HintEval.ScoredOpportunities)
	s.True(report.HintEval.HintScored, "%+v", report.HintEval)
	s.True(report.HintEval.EvidenceScored)
	s.Equal(2, report.HintEval.EvidenceScoredItems)
	s.Equal(2, report.HintEval.EligibleEvidenceCandidates)
	s.InDelta(1, report.HintEval.EvidenceScoreCoverage, 0.0001)
	s.Equal(2, report.HintEval.PositiveEvidenceItems)
	s.InDelta(1, report.HintEval.EvidencePrecision, 0.0001)
	s.InDelta(0.5, report.HintEval.EvidenceRecall, 0.0001)
	s.InDelta(0.185, report.HintEval.EvidenceBrierScore, 0.0001)
	s.Equal(1, report.HintEval.PositiveOpportunities)
	s.Equal(1, report.HintEval.ExposedHints)
	s.Equal(1, report.HintEval.SuccessAtOneCall)
	s.Equal(1, report.HintEval.NewEligibleAtoms)
	s.InDelta(1, report.HintEval.HintPrecision, 0.0001)
	s.InDelta(1, report.HintEval.HintRecall, 0.0001)
	s.InDelta(0.01, report.HintEval.HintBrierScore, 0.0001)
	s.Equal(1, report.HintEval.IdentitySlices[recallreplay.IdentityCrossUserAgent].PositiveOpportunities)
	s.Equal(1, report.HintEval.TemporalSlices["current"].SuccessAtOneCall)
	s.Equal(1, report.HintEval.StrictCrossAgent.PositiveOpportunities)
	s.Zero(report.HintEval.UnauthorizedLeakageItems)
	s.Zero(report.HintEval.TemporalPreservationErrors)

	duplicateCase := replayCase
	duplicateCase.Fixture.CaseID = "hint-opportunity-repeat"
	duplicateCase.ScopeID = "run-1-groupmembench-hint-opportunity-repeat"
	duplicateObservation := *replayCase.HintObservation
	duplicateObservation.Exposed = false
	duplicateObservation.DuplicateDropped = true
	duplicateCase.HintObservation = &duplicateObservation
	set.Cases = []recallreplay.Case{replayCase, duplicateCase}
	dedupReport, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.True(dedupReport.HintEval.DedupScored)
	s.Equal(1, dedupReport.HintEval.DuplicateOpportunities)
	s.Equal(1, dedupReport.HintEval.DuplicateDrops)
	s.Zero(dedupReport.HintEval.DedupErrors)
}

func (s *recallEvalSuite) TestRunRejectsUnauthorizedAndWrongTimeHintIntervention() {
	lead := syntheticCandidate("note-lead", "release", "Release context is available.", 0.9, nil)
	replayCase := syntheticCase("unsafe-hint", "What was the release deadline as of 2026-07-15?", []recallreplay.Candidate{lead})
	hidden := stageeval.Item{ID: "note-hidden", Text: "The release deadline was Friday.", EvidenceEventIDs: []string{"event-hidden"}}
	replayCase.ExtractionItems = append(replayCase.ExtractionItems, hidden)
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{{ID: "deadline", Patterns: []string{"(?i)deadline was friday"}}}
	replayCase.EligibilityDecisions = []recallreplay.EligibilityDecision{
		{ItemID: lead.ID, Eligible: true, Reason: recallreplay.EligibilityEligible},
		{ItemID: hidden.ID, Reason: recallreplay.EligibilityAuthorization},
	}
	score := 0.8
	queryTime := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	replayCase.HintObservation = &recallreplay.HintObservation{
		Exposed: true, Score: &score, FocusedQuery: "What is the release deadline?",
		Consumer: replayCase.Actor, ObservationTime: replayCase.ObservationTime,
		TemporalMode: "as_of", QueryTime: &queryTime, QueryTimezone: replayCase.QueryTimezone,
		LeadNoteIDs: []string{lead.ID},
		Calls: []recallreplay.HintRecallCall{
			{Items: []stageeval.Item{{
				ID: "unauthorized-result", SourceItemIDs: []string{hidden.ID}, Text: hidden.Text,
				EvidenceEventIDs: hidden.EvidenceEventIDs,
			}}},
			{Items: []stageeval.Item{}},
		},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 1},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Zero(report.HintEval.PositiveOpportunities)
	s.Zero(report.HintEval.TruePositiveHints)
	s.Equal(1, report.HintEval.UnauthorizedLeakageItems)
	s.Equal(1, report.HintEval.TemporalPreservationErrors)
	s.InDelta(0.64, report.HintEval.HintBrierScore, 0.0001)
}

func (s *recallEvalSuite) TestRunScoresFocusedCallLifecycleMatrix() {
	tests := []struct {
		name      string
		callOrder []bool
		wantErr   error
		wantAtOne int
		wantAtTwo int
	}{
		{name: "one-call success", callOrder: []bool{true}, wantAtOne: 1, wantAtTwo: 1},
		{name: "two-call recovery", callOrder: []bool{false, true}, wantAtTwo: 1},
		{name: "one-call miss is incomplete", callOrder: []bool{false}, wantErr: recallreplay.ErrIncompleteHintIntervention},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			passive := syntheticCandidate("note-passive", "release", "Release context is available.", 0.9, nil)
			active := syntheticCandidate("note-active", "owner", "Release owner is Alice.", 0, nil)
			replayCase := syntheticCase("hint-lifecycle", "What is the release owner?", []recallreplay.Candidate{passive, active})
			replayCase.Fixture.RequiredAtoms = []stageeval.Atom{{ID: "owner", Patterns: []string{"(?i)release owner is alice"}}}
			calls := make([]recallreplay.HintRecallCall, 0, len(test.callOrder))
			for index, hit := range test.callOrder {
				call := recallreplay.HintRecallCall{Items: []stageeval.Item{}}
				if hit {
					itemID := fmt.Sprintf("active-result-%d", index)
					call.Items = []stageeval.Item{{
						ID: itemID, SourceItemIDs: []string{active.ID}, Text: active.Body,
						EvidenceEventIDs: active.EvidenceEventIDs,
					}}
					call.OriginAttributions = []recallreplay.HintOriginAttribution{{
						ItemID: itemID, Origins: []recallreplay.Actor{active.Origin},
					}}
				}
				calls = append(calls, call)
			}
			score := 0.8
			queryTime := replayCase.ObservationTime
			replayCase.HintObservation = &recallreplay.HintObservation{
				Score: &score, FocusedQuery: replayCase.Fixture.RecallContext.Query,
				Consumer: replayCase.Actor, ObservationTime: replayCase.ObservationTime,
				TemporalMode: "current", QueryTime: &queryTime, QueryTimezone: replayCase.QueryTimezone,
				LeadNoteIDs: []string{active.ID}, Calls: calls,
			}
			set := recallreplay.FixtureSet{
				SchemaVersion: recallreplay.SchemaVersion,
				Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 1},
				Cases:         []recallreplay.Case{replayCase},
			}

			report, err := recallreplay.Run(set, set.Policy)
			if test.wantErr != nil {
				s.ErrorIs(err, test.wantErr)
				return
			}
			s.Require().NoError(err)
			s.Equal(test.wantAtOne, report.HintEval.SuccessAtOneCall)
			s.Equal(test.wantAtTwo, report.HintEval.SuccessAtTwoCalls)
		})
	}
}

func (s *recallEvalSuite) TestRunDerivesFutureHintLeakageFromPinnedCandidateTime() {
	passive := syntheticCandidate("note-passive", "release", "Release context is available.", 0.9, nil)
	future := syntheticCandidate("note-future", "owner", "Release owner will be Alice.", 0, nil)
	validAt := time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC)
	future.ValidAt = &validAt
	replayCase := syntheticCase("hint-future", "What is the release owner?", []recallreplay.Candidate{passive, future})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{{ID: "owner", Patterns: []string{"(?i)release owner will be alice"}}}
	score := 0.8
	queryTime := replayCase.ObservationTime
	replayCase.HintObservation = &recallreplay.HintObservation{
		Exposed: true, Score: &score, FocusedQuery: replayCase.Fixture.RecallContext.Query,
		Consumer: replayCase.Actor, ObservationTime: replayCase.ObservationTime,
		TemporalMode: "current", QueryTime: &queryTime, QueryTimezone: replayCase.QueryTimezone,
		LeadNoteIDs: []string{future.ID},
		Calls: []recallreplay.HintRecallCall{
			{
				Items: []stageeval.Item{{
					ID: "future-result", SourceItemIDs: []string{future.ID}, Text: future.Body,
					EvidenceEventIDs: future.EvidenceEventIDs,
				}},
				OriginAttributions: []recallreplay.HintOriginAttribution{{
					ItemID: "future-result", Origins: []recallreplay.Actor{future.Origin},
				}},
			},
			{Items: []stageeval.Item{}},
		},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 1},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)

	s.Require().NoError(err)
	s.Equal(1, report.HintEval.FutureLeakageItems)
	s.Zero(report.HintEval.PositiveOpportunities)
}

func (s *recallEvalSuite) TestRunCreditsAtomReachedThroughOneHopRelation() {
	primary := syntheticCandidate("note-primary", "deployment", "Deployment is ready.", 0.9, nil)
	primary.RelatedSubjects = []string{"schedule"}
	related := syntheticCandidate("note-related", "schedule", "Friday deployment detail is confirmed.", 0, nil)
	replayCase := syntheticCase("relation-hit", "deployment ready friday", []recallreplay.Candidate{primary, related})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deadline", Patterns: []string{"(?i)friday deployment detail"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 1},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(0, report.RecallEval.CandidateMatchedAtoms)
	s.Equal(1, report.RecallEval.RelationMatchedAtoms)
	s.Equal(1, report.RecallEval.SelectedMatchedAtoms)
	s.Equal(1, report.RecallEval.DeliveredMatchedAtoms)
	s.Empty(report.LossLedger[0].LostAt)
	s.True(report.LossLedger[0].RelationExpanded)
}

func (s *recallEvalSuite) TestRunAttributesQueryIrrelevantRelationToExpansion() {
	primary := syntheticCandidate("note-primary", "deployment", "Deployment is ready.", 0.9, nil)
	primary.RelatedSubjects = []string{"schedule"}
	related := syntheticCandidate("note-related", "schedule", "Friday is the final deadline.", 0, nil)
	replayCase := syntheticCase("relation-loss", "deployment ready", []recallreplay.Candidate{primary, related})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deadline", Patterns: []string{"(?i)friday is the final deadline"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 1},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.False(report.LossLedger[0].CandidateKept)
	s.True(report.LossLedger[0].RelationReachable)
	s.False(report.LossLedger[0].RelationExpanded)
	s.Equal(recallreplay.LossStageRelationExpansion, report.LossLedger[0].LostAt)
	s.Equal("relation_relevance_gate", report.LossLedger[0].Reason)
}

func (s *recallEvalSuite) TestRunAttributesUnvisitedPrimaryRelationToItemBudget() {
	first := syntheticCandidate("note-first", "deployment plan", "Deployment review happens Friday.", 0.9, nil)
	second := syntheticCandidate("note-second", "deployment schedule", "Deployment schedule review happens Friday.", 0.8, nil)
	second.RelatedSubjects = []string{"schedule detail"}
	related := syntheticCandidate("note-related", "schedule detail", "Deployment deadline is Friday.", 0, nil)
	replayCase := syntheticCase(
		"unvisited-relation", "deployment friday",
		[]recallreplay.Candidate{first, second, related},
	)
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deadline", Patterns: []string{"(?i)deployment deadline is friday"}},
	}
	replayCase.Fixture.RecallContext.MaxItems = 1
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 2},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.True(report.LossLedger[0].RelationReachable)
	s.True(report.LossLedger[0].RelationExpanded)
	s.True(report.LossLedger[0].Selected)
	s.False(report.LossLedger[0].Delivered)
	s.Equal(recallreplay.LossStageBudgetPacking, report.LossLedger[0].LostAt)
	s.Equal("max_items", report.LossLedger[0].Reason)
}

func (s *recallEvalSuite) TestRunAttributesSelectedCandidateLostToTokenBudget() {
	replayCase := syntheticCase("budget-loss", "audit trail deadline", []recallreplay.Candidate{
		syntheticCandidate("note-budget", "audit trail", "Audit trail deadline is Friday after final approval.", 0.9, nil),
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deadline", Patterns: []string{"(?i)audit trail deadline"}},
	}
	replayCase.Fixture.RecallContext.TokenBudget = 1
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(1, report.RecallEval.CandidateMatchedAtoms)
	s.Equal(1, report.RecallEval.RelationMatchedAtoms)
	s.Equal(1, report.RecallEval.SelectedMatchedAtoms)
	s.Zero(report.RecallEval.DeliveredMatchedAtoms)
	s.Equal(1, report.RecallEval.BudgetDroppedAtoms)
	s.Equal(recallreplay.LossStageBudgetPacking, report.LossLedger[0].LostAt)
	s.Equal("token_budget", report.LossLedger[0].Reason)
}

func (s *recallEvalSuite) TestRunAttributesRelationRemovedByBudgetDegradation() {
	primary := syntheticCandidate("note-primary", "deployment", "Deployment is ready.", 0.9, nil)
	primary.RelatedSubjects = []string{"schedule"}
	related := syntheticCandidate(
		"note-related", "schedule",
		"Friday deployment detail includes the final validation deadline and evidence package.", 0, nil,
	)
	replayCase := syntheticCase("relation-budget", "deployment ready friday", []recallreplay.Candidate{primary, related})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deadline", Patterns: []string{"(?i)friday deployment detail"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy: recallreplay.Policy{
			SemanticThreshold: 0.5, CandidateLimit: 1, DegradeRelated: true,
		},
		Cases: []recallreplay.Case{replayCase},
	}
	full, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Require().Positive(full.StageTotals.PlannedTokens)
	set.Cases[0].Fixture.RecallContext.TokenBudget = full.StageTotals.PlannedTokens - 1

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.True(report.LossLedger[0].RelationReachable)
	s.True(report.LossLedger[0].RelationExpanded)
	s.True(report.LossLedger[0].Selected)
	s.False(report.LossLedger[0].Delivered)
	s.Equal(recallreplay.LossStageBudgetPacking, report.LossLedger[0].LostAt)
	s.Equal("uncovered_relation_cost", report.LossLedger[0].Reason)
}

func (s *recallEvalSuite) TestCandidateRecallIncludesSemanticLaneBeforeHardGate() {
	semantic := 0.9
	candidate := syntheticCandidate("note-semantic", "release", "Release is ready.", 0, &semantic)
	invalidAt := candidate.SourceOccurredAt.Add(-time.Minute)
	candidate.InvalidAt = &invalidAt
	replayCase := syntheticCase("semantic-hard-gate", "What is the project status?", []recallreplay.Candidate{candidate})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "ready", Patterns: []string{"(?i)release is ready"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(1, report.RecallEval.CandidateMatchedAtoms)
	s.Zero(report.RecallEval.DeliveredMatchedAtoms)
}

func (s *recallEvalSuite) TestRunUsesFarthestRejectionReason() {
	tests := []struct {
		name   string
		set    recallreplay.FixtureSet
		reason string
	}{
		{name: "multiple supporting notes", set: multiSupportBudgetSet(), reason: "token_budget"},
		{name: "later rejection does not overwrite relation budget", set: overwrittenRelationBudgetSet(), reason: "uncovered_relation_cost"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			report, err := recallreplay.Run(test.set, test.set.Policy)
			s.Require().NoError(err)
			s.Equal(recallreplay.LossStageBudgetPacking, report.LossLedger[0].LostAt)
			s.Equal(test.reason, report.LossLedger[0].Reason)
		})
	}
}

func (s *recallEvalSuite) TestRunReportsPlannerLatency() {
	replayCase := syntheticCase("planner-latency", "release status", []recallreplay.Candidate{
		syntheticCandidate("note-status", "release status", "Release status is ready.", 0.9, nil),
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "status", Patterns: []string{"(?i)release status is ready"}},
	}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(1, report.RecallEval.PlannerCalls)
	s.Positive(report.RecallEval.PlannerMeanDurationNS)
	s.Positive(report.RecallEval.PlannerP95DurationNS)
	s.Require().Len(report.Cases, 1)
	s.Positive(report.Cases[0].PlannerDurationNS)
}

func multiSupportBudgetSet() recallreplay.FixtureSet {
	replayCase := syntheticCase("multi-support", "audit trail deadline", []recallreplay.Candidate{
		syntheticCandidate("note-fusion", "other", "Audit trail deadline is Friday.", 0, nil),
		syntheticCandidate("note-budget", "audit trail", "Audit trail deadline is Friday.", 0.9, nil),
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "deadline", Patterns: []string{"(?i)audit trail deadline"}},
	}
	replayCase.Fixture.RecallContext.TokenBudget = 1
	return recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 1},
		Cases:         []recallreplay.Case{replayCase},
	}
}

func overwrittenRelationBudgetSet() recallreplay.FixtureSet {
	shared := "shared alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau"
	primary := syntheticCandidate(
		"note-primary", "audit deadline",
		shared+" primary marker", 0.9, nil,
	)
	primary.RelatedSubjects = []string{"audit evidence"}
	related := syntheticCandidate(
		"note-related", "audit evidence",
		shared+" target marker", 0.8, nil,
	)
	replayCase := syntheticCase("overwritten-rejection", "shared alpha beta", []recallreplay.Candidate{primary, related})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "target", Patterns: []string{"(?i)target marker"}},
	}
	fullSet := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy: recallreplay.Policy{
			SemanticThreshold: 0.5, CandidateLimit: 2, SuppressDuplicates: true, DegradeRelated: true,
		},
		Cases: []recallreplay.Case{replayCase},
	}
	full, err := recallreplay.Run(fullSet, fullSet.Policy)
	if err != nil || full.StageTotals.PlannedTokens < 2 {
		panic("build overwritten relation budget fixture")
	}
	fullSet.Cases[0].Fixture.RecallContext.TokenBudget = full.StageTotals.PlannedTokens - 1
	return fullSet
}

func (s *recallEvalSuite) TestRunCountsDeliveredSupersededEventLeakage() {
	candidate := syntheticCandidate("note-stale", "release status", "Release status is ready.", 0.9, nil)
	candidate.EvidenceEventIDs = []string{"event-superseded"}
	replayCase := syntheticCase("superseded", "release status ready", []recallreplay.Candidate{candidate})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{
		{ID: "status", Patterns: []string{"(?i)release status is ready"}},
	}
	replayCase.Fixture.SupersededEventIDs = []string{"event-superseded"}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(1, report.Summary.RecallLeakageItems)
	s.Equal(1, report.RecallEval.SupersededLeakageItems)
}
