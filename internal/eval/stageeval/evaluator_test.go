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

func (s *EvaluatorSuite) TestEvaluateCreditsRelationComposedFromExtractedNote() {
	fixture := validFixture()
	extraction := testObservation("case", stageeval.StageExtraction, []stageeval.Item{
		{ID: "anchor", Text: "anchor fact"},
		{ID: "related", Text: "fact", EvidenceEventIDs: []string{"event-related"}},
	})
	recall := testObservation("case", stageeval.StageRecall, []stageeval.Item{
		{ID: "anchor", SourceItemIDs: []string{"anchor", "related"}, Text: "anchor [related: fact]"},
	})

	result, err := stageeval.Evaluate(fixture, extraction, recall)

	s.Require().NoError(err)
	s.InDelta(1.0, result.Recall.GoldRecall, 0.0001)
	s.InDelta(1.0, result.Recall.ConditionalRecall, 0.0001)
}

func (s *EvaluatorSuite) TestEvaluateSupportsForbiddenOnlyAbstentionCase() {
	fixture := stageeval.Fixture{
		CaseID: "abstention", SourceRevision: testSourceRevision, RecallContext: testRecallContext(),
		ForbiddenAtoms: []stageeval.Atom{{ID: "false_owner", Patterns: []string{"(?i)compliance.*designated.*owner"}}},
	}
	extraction := testObservation("abstention", stageeval.StageExtraction, []stageeval.Item{
		{ID: "false-owner", Text: "Compliance is designated as owner."},
	})
	recall := testObservation("abstention", stageeval.StageRecall, []stageeval.Item{
		{ID: "false-owner", Text: "Compliance is designated as owner."},
	})

	result, err := stageeval.Evaluate(fixture, extraction, recall)

	s.Require().NoError(err)
	s.Zero(result.Extraction.RequiredAtoms)
	s.Equal(1, result.Extraction.LeakageItems)
	s.Equal(1, result.Recall.LeakageItems)
}

func (s *EvaluatorSuite) TestEvaluateClassifiesNegationSuppressedLeakage() {
	ownerPattern := `(?i)compliance.{0,100}(designated|owner)|designated.{0,100}owner.{0,100}compliance`
	simplePattern := `(?i)compliance.{0,100}designated.{0,100}owner`
	tests := []struct {
		name              string
		patterns          []string
		text              string
		evidenceEventIDs  []string
		forbiddenEventIDs []string
		wantRaw           int
		wantSuppressed    int
		wantDetails       []stageeval.LeakageDetail
	}{
		{
			name:     "negated match inside the span",
			patterns: []string{ownerPattern},
			text: "Exceptions log ownership is shared between Legal, Reporting, and Compliance until the baseline " +
				"stabilizes; Compliance is not sole owner early.",
			wantRaw: 1, wantSuppressed: 1,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "not"},
			},
		},
		{
			name:     "cue in the same sentence suppresses",
			patterns: []string{simplePattern},
			text:     "Compliance is not the designated owner.",
			wantRaw:  1, wantSuppressed: 1,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "not"},
			},
		},
		{
			name:     "cue in a different sentence does not suppress",
			patterns: []string{simplePattern},
			text:     "This is not a legal matter. Compliance is the designated owner.",
			wantRaw:  1, wantSuppressed: 0,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner"},
			},
		},
		{
			name:     "genuine leak is counted and not suppressed",
			patterns: []string{`(?i)owner.{0,50}compliance`},
			text:     "Owner of exception log: Compliance.",
			wantRaw:  1, wantSuppressed: 0,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner"},
			},
		},
		{
			name:     "contraction n't suppresses",
			patterns: []string{simplePattern},
			text:     "Compliance isn't the designated owner.",
			wantRaw:  1, wantSuppressed: 1,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "n't"},
			},
		},
		{
			name:     "never suppresses",
			patterns: []string{simplePattern},
			text:     "Compliance never became the designated owner.",
			wantRaw:  1, wantSuppressed: 1,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "never"},
			},
		},
		{
			name:     "no longer suppresses",
			patterns: []string{simplePattern},
			text:     "Compliance is no longer the designated owner.",
			wantRaw:  1, wantSuppressed: 1,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "no longer"},
			},
		},
		{
			name:     "neither suppresses",
			patterns: []string{simplePattern},
			text:     "Compliance is neither the designated owner nor the reviewer.",
			wantRaw:  1, wantSuppressed: 1,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "neither"},
			},
		},
		{
			name:     "nor suppresses",
			patterns: []string{simplePattern},
			text:     "Compliance is the designated owner, nor is there a deputy.",
			wantRaw:  1, wantSuppressed: 1,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "nor"},
			},
		},
		{
			name:     "false friend noted does not suppress",
			patterns: []string{simplePattern},
			text:     "Compliance noted the designated owner rotation.",
			wantRaw:  1, wantSuppressed: 0,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner"},
			},
		},
		{
			name:     "unsuppressed occurrence wins over a suppressed one",
			patterns: []string{`(?i)compliance[^.]{0,40}owner`},
			text:     "Compliance is not the owner. Compliance is the owner.",
			wantRaw:  1, wantSuppressed: 0,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner"},
			},
		},
		{
			name:     "all occurrences suppressed reports the first cue",
			patterns: []string{`(?i)compliance[^.]{0,40}owner`},
			text:     "Compliance is not the owner. Compliance is never the owner.",
			wantRaw:  1, wantSuppressed: 1,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "not"},
			},
		},
		{
			name:              "forbidden event id is never suppressed",
			patterns:          []string{simplePattern},
			text:              "Compliance is not the designated owner.",
			evidenceEventIDs:  []string{"event-private"},
			forbiddenEventIDs: []string{"event-private"},
			wantRaw:           1, wantSuppressed: 0,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "not"},
				{ItemID: "note", EventID: "event-private"},
			},
		},
		{
			name:              "forbidden event id alone counts without suppression",
			patterns:          []string{simplePattern},
			text:              "Compliance is not discussed here.",
			evidenceEventIDs:  []string{"event-private"},
			forbiddenEventIDs: []string{"event-private"},
			wantRaw:           1, wantSuppressed: 0,
			wantDetails: []stageeval.LeakageDetail{
				{ItemID: "note", EventID: "event-private"},
			},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			fixture := stageeval.Fixture{
				CaseID: "case", SourceRevision: testSourceRevision, RecallContext: testRecallContext(),
				ForbiddenAtoms:    []stageeval.Atom{{ID: "compliance_owner", Patterns: test.patterns}},
				ForbiddenEventIDs: test.forbiddenEventIDs,
			}
			item := stageeval.Item{ID: "note", Text: test.text, EvidenceEventIDs: test.evidenceEventIDs}
			extraction := testObservation("case", stageeval.StageExtraction, []stageeval.Item{item})
			recall := testObservation("case", stageeval.StageRecall, []stageeval.Item{item})

			result, err := stageeval.Evaluate(fixture, extraction, recall)

			s.Require().NoError(err)
			s.Equal(test.wantRaw, result.Extraction.LeakageItems)
			s.Equal(test.wantSuppressed, result.Extraction.SuppressedLeakageItems)
			s.Equal(test.wantDetails, result.Extraction.LeakageDetails)
			s.Equal(test.wantRaw, result.Recall.LeakageItems)
			s.Equal(test.wantSuppressed, result.Recall.SuppressedLeakageItems)
		})
	}
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
