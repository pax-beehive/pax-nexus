package recallreplay_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type replaySuite struct {
	suite.Suite
}

func TestReplaySuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(replaySuite))
}

func (s *replaySuite) TestFixtureRoundTripAndRun() {
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Dataset:       "synthetic",
		ExportedFrom:  recallreplay.Provenance{RunID: "run-1", Arm: "team_note", ScopePrefix: "run-1-groupmembench-"},
		Policy:        recallreplay.Policy{SemanticThreshold: 0.65, CandidateLimit: 16},
		Cases: []recallreplay.Case{
			syntheticCase("case_hit", "deploy window friday", []recallreplay.Candidate{
				syntheticCandidate("note-1", "deploy window", "Deploy window moves to Friday.", 0.9, nil),
				syntheticCandidate("note-2", "pancake meetup", "Pancake recipe swap meetup.", 0, nil),
			}),
			syntheticCase("case_budget", "audit trail deadline", []recallreplay.Candidate{
				syntheticCandidate("note-3", "audit trail", "Audit trail freezes on Friday.", 0.9, nil),
				syntheticCandidate("note-4", "audit owner", "Audit owner signs the deadline on Friday.", 0.8, nil),
			}),
		},
	}
	set.Cases[0].Fixture.RequiredAtoms = []stageeval.Atom{{ID: "deploys", Patterns: []string{"(?i)deploy window"}}}
	set.Cases[1].Fixture.RequiredAtoms = []stageeval.Atom{{ID: "audit", Patterns: []string{"(?i)audit owner"}}}
	set.Cases[1].Fixture.RecallContext.TokenBudget = 20

	path := s.T().TempDir() + "/fixture.json"
	s.Require().NoError(recallreplay.WriteFixtureSet(path, set))
	loaded, err := recallreplay.LoadFixtureSet(path)
	s.Require().NoError(err)
	s.Equal(set.Policy, loaded.Policy)
	s.Require().Len(loaded.Cases, 2)
	s.Equal(set.Cases[0].Candidates[0].Body, loaded.Cases[0].Candidates[0].Body)

	report, err := recallreplay.Run(loaded, loaded.Policy)
	s.Require().NoError(err)
	s.Require().Len(report.Cases, 2)
	s.NotEmpty(report.Cases[0].PlannedItems)
	s.Equal(2, report.Summary.Cases)
	s.Equal(2, report.Summary.RequiredAtoms)
	s.Equal(2, report.Summary.RecallMatchedAtoms)
	s.Equal(0, report.Summary.RecallMissedAvailableAtoms)
	s.Equal(4, report.StageTotals.Candidates)
	s.Equal(3, report.StageTotals.FusionKept)
	s.Equal(1, report.StageTotals.Rejections["fusion_limit"])
	s.Equal(1, report.StageTotals.Rejections["token_budget"])
	s.Equal(2, report.StageTotals.PlanVersions[teamnote.GeneralRecallV3FinalStatePlanVersion])
	s.Positive(report.StageTotals.LaneCandidateCounts[string(teamnote.RecallLaneLexical)])
	s.Positive(report.StageTotals.LaneCandidateCounts[string(teamnote.RecallLaneTemporal)])
	s.Positive(report.StageTotals.Dispositions[string(teamnote.RecallDispositionEvidence)])
	s.Equal(1, report.StageTotals.BudgetDrops["token_budget"])
	for _, caseReport := range report.Cases {
		s.NotEmpty(caseReport.Trace.Rejections, caseReport.CaseID)
	}
}

func (s *replaySuite) TestRunBM25RanksFixedTeamNotesWithoutAdapterScores() {
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Dataset:       "synthetic",
		ExportedFrom:  recallreplay.Provenance{RunID: "run-1", Arm: "team_note", ScopePrefix: "run-1-groupmembench-"},
		Policy:        recallreplay.Policy{CandidateLimit: 16},
		Cases: []recallreplay.Case{syntheticCase("case-bm25", "who owns rollback evidence", []recallreplay.Candidate{
			syntheticCandidate("distractor", "ownership", "Finance owns the evidence review.", 0.99, nil),
			syntheticCandidate("gold", "rollback", "Ops Lead owns the rollback evidence pack.", 0, nil),
		})},
	}
	set.Cases[0].Fixture.RequiredAtoms = []stageeval.Atom{{ID: "owner", Patterns: []string{"(?i)ops lead owns"}}}

	report, err := recallreplay.Run(set, recallreplay.Policy{
		RetrievalStrategy: recallreplay.RetrievalStrategyBM25, CandidateLimit: 16,
	})

	s.Require().NoError(err)
	s.Require().Len(report.Cases, 1)
	s.Contains(report.Cases[0].Trace.SelectedSet, "gold")
}

func (s *replaySuite) TestCuratedRelationUtilityFixtureSelectsUsefulAndRejectsNoiseEdges() {
	set, err := recallreplay.LoadFixtureSet(filepath.Join(
		"..", "..", "..", "evals", "stage", "replay", "relation-marginal-utility-v1.json",
	))
	s.Require().NoError(err)

	report, err := recallreplay.Run(set, recallreplay.Policy{
		SemanticThreshold: 0.5, CandidateLimit: 16, SuppressDuplicates: true, DegradeRelated: true,
	})
	s.Require().NoError(err)

	tests := []struct {
		caseID   string
		usefulID string
		noiseID  string
	}{
		{caseID: "status_payments_billing", usefulID: "status-useful", noiseID: "status-noise"},
		{caseID: "deadline_america_europe", usefulID: "deadline-useful", noiseID: "deadline-noise"},
		{caseID: "decision_backend_frontend", usefulID: "decision-useful", noiseID: "decision-noise"},
		{caseID: "blocker_data_reporting", usefulID: "blocker-useful", noiseID: "blocker-noise"},
	}
	for _, test := range tests {
		s.Run(test.caseID, func() {
			caseReport := replayReportCase(s, report.Cases, test.caseID)
			s.Contains(caseReport.Trace.SelectedSet, test.usefulID)
			s.NotContains(caseReport.Trace.SelectedSet, test.noiseID)
			s.Require().Len(caseReport.Trace.RelationRejections, 1)
			s.Equal(test.noiseID, caseReport.Trace.RelationRejections[0].RelatedNoteID)
			s.Equal(teamnote.RejectRelationMarginalUtility, caseReport.Trace.RelationRejections[0].Reason)
		})
	}
}

func replayReportCase(s *replaySuite, cases []recallreplay.CaseReport, caseID string) recallreplay.CaseReport {
	for _, caseReport := range cases {
		if caseReport.CaseID == caseID {
			return caseReport
		}
	}
	s.FailNow("missing replay case", caseID)
	return recallreplay.CaseReport{}
}

func (s *replaySuite) TestRunRejectsInvalidFixture() {
	_, err := recallreplay.Run(recallreplay.FixtureSet{}, recallreplay.Policy{})
	s.Require().Error(err)
}

func (s *replaySuite) TestRunPreservesCapturedMaxItems() {
	replayCase := syntheticCase("case_max_items", "deploy window friday", []recallreplay.Candidate{
		syntheticCandidate("note-1", "deploy window", "Deploy window moves to Friday.", 0.9, nil),
		syntheticCandidate("note-2", "deploy owner", "Deploy owner confirms Friday.", 0.8, nil),
	})
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{{ID: "deploy", Patterns: []string{"(?i)deploy"}}}
	replayCase.Fixture.RecallContext.MaxItems = 1
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	report, err := recallreplay.Run(set, set.Policy)
	s.Require().NoError(err)
	s.Equal(1, report.StageTotals.PlannedNotes)
	s.Equal(1, report.StageTotals.Rejections["max_items"])
}

func (s *replaySuite) TestRoundTripPreservesObservationTimeForFutureValidity() {
	replayCase := syntheticCase("case_future", "alpha release status", []recallreplay.Candidate{
		syntheticCandidate("note-future", "alpha release status", "Alpha release will be ready.", 0.9, nil),
	})
	validAt := replayCase.ObservationTime.Add(time.Hour)
	replayCase.Candidates[0].ValidAt = &validAt
	replayCase.Fixture.RequiredAtoms = []stageeval.Atom{{ID: "future", Patterns: []string{"(?i)alpha release"}}}
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{CandidateLimit: 16},
		Cases:         []recallreplay.Case{replayCase},
	}

	path := s.T().TempDir() + "/fixture.json"
	s.Require().NoError(recallreplay.WriteFixtureSet(path, set))
	loaded, err := recallreplay.LoadFixtureSet(path)
	s.Require().NoError(err)
	s.Equal(replayCase.ObservationTime, loaded.Cases[0].ObservationTime)

	report, err := recallreplay.Run(loaded, loaded.Policy)
	s.Require().NoError(err)
	s.Empty(report.Cases[0].PlannedItems)
	s.Equal(1, report.StageTotals.Rejections[string(teamnote.RejectTemporalGate)])
}

func (s *replaySuite) TestLoadRejectsUnknownFields() {
	path := s.T().TempDir() + "/fixture.json"
	s.Require().NoError(recallreplay.WriteFixtureSet(path, recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.65, CandidateLimit: 16},
		Cases:         []recallreplay.Case{syntheticCase("case_hit", "query", nil)},
	}))
	_, err := recallreplay.LoadFixtureSet(path)
	s.Require().NoError(err)
}

func (s *replaySuite) TestLoadMigratesLegacyObservationTime() {
	path := s.T().TempDir() + "/legacy.json"
	legacy := `{
  "schema_version": "pax-recall-replay-v1",
  "exported_from": {"run_id":"run-1","arm":"team_note","scope_prefix":"scope-"},
  "policy": {"semantic_threshold":0.65,"candidate_limit":16},
  "cases": [{
    "fixture": {"case_id":"legacy","source_revision":"rev","recall_context":{"query":"alpha","token_budget":128}},
    "scope_id":"scope-legacy",
    "actor":{"user_id":"user","agent_id":"agent","session_id":"session"},
    "extraction_items":[],
    "candidates":[{
      "id":"note-1","kind":"status","subject":"alpha","body":"alpha ready",
      "origin":{"user_id":"user","agent_id":"agent","session_id":"session"},
      "evidence_event_ids":["event-1"],"revision":1,
      "created_at":"2026-07-16T10:00:00Z","updated_at":"2026-07-16T11:00:00Z",
      "source_occurred_at":"2026-07-16T09:00:00Z","lexical_score":1
    }]
  }, {
    "fixture": {"case_id":"legacy-empty","source_revision":"rev","recall_context":{"query":"alpha","token_budget":128}},
    "scope_id":"scope-legacy-empty",
    "actor":{"user_id":"user","agent_id":"agent","session_id":"session"},
    "extraction_items":[],
    "candidates":[]
  }]
}`
	s.Require().NoError(os.WriteFile(path, []byte(legacy), 0o600))

	loaded, err := recallreplay.LoadFixtureSet(path)

	s.Require().NoError(err)
	s.Equal(recallreplay.SchemaVersion, loaded.SchemaVersion)
	s.Equal(time.Date(2026, time.July, 16, 11, 0, 0, 0, time.UTC), loaded.Cases[0].ObservationTime)
	s.Equal(time.Unix(0, 0).UTC(), loaded.Cases[1].ObservationTime)
}

func syntheticCase(caseID, query string, candidates []recallreplay.Candidate) recallreplay.Case {
	observationTime := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	return recallreplay.Case{
		Fixture: stageeval.Fixture{
			CaseID: caseID, SourceRevision: "6c79b2531a762ed4fa50dd359ed3e363f63c3c4c640a5151a4174577d9dbc596",
			RecallContext: stageeval.RecallContext{
				ConsumerUserID: "User_1", ConsumerAgentID: "consumer",
				Query: query, TokenBudget: 512,
			},
		},
		ScopeID:         "run-1-groupmembench-" + caseID,
		Actor:           recallreplay.Actor{UserID: "User_1", AgentID: "consumer", SessionID: "recall-replay"},
		ObservationTime: observationTime,
		QueryTimezone:   "UTC",
		ExtractionItems: extractionItems(candidates),
		Candidates:      candidates,
	}
}

func extractionItems(candidates []recallreplay.Candidate) []stageeval.Item {
	items := make([]stageeval.Item, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, stageeval.Item{ID: candidate.ID, Text: candidate.Body, EvidenceEventIDs: candidate.EvidenceEventIDs})
	}
	return items
}

func syntheticCandidate(id, subject, body string, lexical float64, semantic *float64) recallreplay.Candidate {
	occurred := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	return recallreplay.Candidate{
		ID: id, Kind: "status", Subject: subject, Body: body,
		Origin:           recallreplay.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"},
		EvidenceEventIDs: []string{"event-" + id},
		Revision:         1,
		CreatedAt:        occurred, UpdatedAt: occurred, SourceOccurredAt: occurred,
		LexicalScore: lexical, SemanticScore: semantic,
	}
}
