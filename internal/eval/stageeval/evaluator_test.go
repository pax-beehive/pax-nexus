package stageeval_test

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/stretchr/testify/suite"
)

type EvaluatorSuite struct {
	suite.Suite
}

func TestEvaluatorSuite(t *testing.T) {
	suite.Run(t, new(EvaluatorSuite))
}

func (s *EvaluatorSuite) TestEvaluateSeparatesExtractionAndRecallFailures() {
	fixture := stageeval.Fixture{CaseID: "case-1", SourceRevision: testSourceRevision, RecallContext: testRecallContext(), RequiredAtoms: []stageeval.Atom{
		{ID: "owner", Patterns: []string{"(?i)ops lead.*rollback evidence"}, SupportingEventIDs: []string{"event-owner"}},
		{ID: "cutoff", Patterns: []string{"(?i)july 26"}, SupportingEventIDs: []string{"event-cutoff"}},
	}}
	extraction := testObservation("case-1", stageeval.StageExtraction, []stageeval.Item{
		{ID: "note-owner", Text: "Ops Lead owns the rollback evidence pack.", EvidenceEventIDs: []string{"event-owner"}},
	})
	recall := testObservation("case-1", stageeval.StageRecall, nil)

	result, err := stageeval.Evaluate(fixture, extraction, recall)

	s.Require().NoError(err)
	s.InDelta(0.5, result.Extraction.FactRecall, 0.0001)
	s.InDelta(0.5, result.Extraction.EvidenceRecall, 0.0001)
	s.InDelta(0.0, result.Recall.GoldRecall, 0.0001)
	s.InDelta(0.0, result.Recall.ConditionalRecall, 0.0001)
	s.Equal([]string{"cutoff"}, result.Extraction.MissingAtomIDs)
	s.Equal([]string{"owner"}, result.Recall.MissedAvailableAtomIDs)
}

func (s *EvaluatorSuite) TestEvaluateReportsEvidenceAndLeakage() {
	fixture := stageeval.Fixture{
		CaseID: "case-2", SourceRevision: testSourceRevision, RecallContext: testRecallContext(),
		RequiredAtoms: []stageeval.Atom{{
			ID: "current", Patterns: []string{"current approach"}, SupportingEventIDs: []string{"event-current"},
		}},
		ForbiddenAtoms:     []stageeval.Atom{{ID: "old", Patterns: []string{"old approach"}}},
		ForbiddenEventIDs:  []string{"event-private"},
		SupersededEventIDs: []string{"event-old"},
	}
	extraction := testObservation("case-2", stageeval.StageExtraction, []stageeval.Item{
		{ID: "current", Text: "current approach", EvidenceEventIDs: []string{"event-current", "event-private"}},
		{ID: "old", Text: "old approach", EvidenceEventIDs: []string{"event-old"}},
	})
	recall := testObservation("case-2", stageeval.StageRecall, []stageeval.Item{
		{ID: "old", Text: "old approach", EvidenceEventIDs: []string{"event-old"}},
	})

	result, err := stageeval.Evaluate(fixture, extraction, recall)

	s.Require().NoError(err)
	s.InDelta(1.0/3.0, result.Extraction.EvidencePrecision, 0.0001)
	s.InDelta(1.0, result.Extraction.EvidenceRecall, 0.0001)
	s.Equal(2, result.Extraction.LeakageItems)
	s.Equal(1, result.Recall.LeakageItems)
	s.InDelta(0.0, result.Recall.ContextPrecision, 0.0001)
}

func (s *EvaluatorSuite) TestEvaluateCreditsAnyPairedNoteMatchingAnAtom() {
	fixture := validFixture()
	fixture.RequiredAtoms[0].SupportingEventIDs = []string{"event-second"}
	extraction := testObservation("case", stageeval.StageExtraction, []stageeval.Item{
		{ID: "first", Text: "fact", EvidenceEventIDs: []string{"event-other"}},
		{ID: "second", Text: "fact", EvidenceEventIDs: []string{"event-second"}},
	})
	recall := testObservation("case", stageeval.StageRecall, []stageeval.Item{
		{ID: "second", Text: "fact", EvidenceEventIDs: []string{"event-second"}},
	})

	result, err := stageeval.Evaluate(fixture, extraction, recall)

	s.Require().NoError(err)
	s.InDelta(1.0, result.Recall.ConditionalRecall, 0.0001)
	s.InDelta(1.0, result.Extraction.EvidenceRecall, 0.0001)
}

func (s *EvaluatorSuite) TestValidateRejectsInvalidFixturesAndObservations() {
	tests := []struct {
		name       string
		fixture    stageeval.Fixture
		extraction stageeval.Observation
		recall     stageeval.Observation
	}{
		{name: "missing case id", fixture: stageeval.Fixture{}},
		{name: "missing required atoms", fixture: stageeval.Fixture{CaseID: "case"}},
		{name: "duplicate atom", fixture: stageeval.Fixture{CaseID: "case", RequiredAtoms: []stageeval.Atom{{ID: "same", Patterns: []string{"a"}}, {ID: "same", Patterns: []string{"b"}}}}},
		{name: "invalid regex", fixture: stageeval.Fixture{
			CaseID: "case", RequiredAtoms: []stageeval.Atom{{ID: "atom", Patterns: []string{"["}}},
		}},
		{name: "wrong extraction stage", fixture: validFixture(), extraction: stageeval.Observation{CaseID: "case", Stage: stageeval.StageRecall}},
		{name: "mismatched recall case", fixture: validFixture(), recall: stageeval.Observation{CaseID: "other", Stage: stageeval.StageRecall}},
		{name: "recall item absent from extraction", fixture: validFixture(), extraction: testObservation("case", stageeval.StageExtraction, nil), recall: testObservation("case", stageeval.StageRecall, []stageeval.Item{{ID: "other", Text: "fact"}})},
		{name: "duplicate evidence", fixture: validFixture(), extraction: testObservation("case", stageeval.StageExtraction, []stageeval.Item{{ID: "note", Text: "fact", EvidenceEventIDs: []string{"event", "event"}}}), recall: testObservation("case", stageeval.StageRecall, nil)},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := stageeval.Evaluate(test.fixture, test.extraction, test.recall)
			s.Error(err)
		})
	}
}

func validFixture() stageeval.Fixture {
	return stageeval.Fixture{
		CaseID: "case", SourceRevision: testSourceRevision, RecallContext: testRecallContext(),
		RequiredAtoms: []stageeval.Atom{{ID: "atom", Patterns: []string{"fact"}}},
	}
}

func testRecallContext() stageeval.RecallContext {
	return stageeval.RecallContext{ConsumerUserID: "user", Query: "question", TokenBudget: 512}
}

func testObservation(caseID string, stage stageeval.Stage, items []stageeval.Item) stageeval.Observation {
	observation := stageeval.Observation{CaseID: caseID, Stage: stage, SourceRevision: testSourceRevision, Items: items}
	if stage == stageeval.StageRecall {
		context := testRecallContext()
		observation.RecallContext = &context
	}
	return observation
}

const testSourceRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
