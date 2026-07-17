package recallreplay_test

import (
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
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
	s.Equal(2, report.Summary.Cases)
	s.Equal(2, report.Summary.RequiredAtoms)
	s.Equal(1, report.Summary.RecallMatchedAtoms)
	s.Equal(1, report.Summary.RecallMissedAvailableAtoms)
	s.Equal(4, report.StageTotals.Candidates)
	s.Equal(3, report.StageTotals.FusionKept)
	s.Equal(1, report.StageTotals.Rejections["fusion_limit"])
	s.Equal(1, report.StageTotals.Rejections["token_budget"])
	for _, caseReport := range report.Cases {
		s.NotEmpty(caseReport.Trace.Rejections, caseReport.CaseID)
	}
}

func (s *replaySuite) TestRunRejectsInvalidFixture() {
	_, err := recallreplay.Run(recallreplay.FixtureSet{}, recallreplay.Policy{})
	s.Require().Error(err)
	s.Contains(err.Error(), "schema_version")
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

func syntheticCase(caseID, query string, candidates []recallreplay.Candidate) recallreplay.Case {
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
