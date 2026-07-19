package recallreplay_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/stretchr/testify/suite"
)

type recallEvalSuite struct {
	suite.Suite
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

func (s *recallEvalSuite) TestFixtureV3PersistsAtomSupportAndCandidateSnapshotDigest() {
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
	s.Equal("pax-recall-replay-v3", loaded.SchemaVersion)
	s.Require().Len(loaded.Cases[0].AtomSupports, 1)
	s.Equal("deploy", loaded.Cases[0].AtomSupports[0].AtomID)
	s.Equal([]string{"note-hit"}, loaded.Cases[0].AtomSupports[0].ItemIDs)
	s.Len(loaded.Cases[0].CandidateSnapshotSHA256, 64)
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
