package stageeval_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/stretchr/testify/suite"
)

type RunSuite struct {
	suite.Suite
}

func TestRunSuite(t *testing.T) {
	suite.Run(t, new(RunSuite))
}

func (s *RunSuite) TestRunPairsStageObservationsAndAggregatesSummary() {
	fixtures := stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Dataset: "fixture-set", Cases: []stageeval.Fixture{
		{CaseID: "one", Category: "handoff", SourceRevision: testSourceRevision, RecallContext: testRecallContext(), RequiredAtoms: []stageeval.Atom{{ID: "owner", Patterns: []string{"Ops"}}}},
		{CaseID: "two", Category: "update", SourceRevision: testSourceRevision, RecallContext: testRecallContext(), RequiredAtoms: []stageeval.Atom{{ID: "state", Patterns: []string{"current"}}}},
	}}
	observations := strings.NewReader(strings.Join([]string{
		`{"case_id":"one","stage":"extraction","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","items":[{"id":"n1","text":"Ops"}]}`,
		`{"case_id":"one","stage":"recall","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recall_context":{"consumer_user_id":"user","query":"question","token_budget":512},"items":[{"id":"n1","text":"Ops"}]}`,
		`{"case_id":"two","stage":"extraction","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","items":[]}`,
		`{"case_id":"two","stage":"recall","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recall_context":{"consumer_user_id":"user","query":"question","token_budget":512},"items":[]}`,
	}, "\n"))

	results, summary, err := stageeval.Run(fixtures, observations)

	s.Require().NoError(err)
	s.Len(results, 2)
	s.InDelta(0.5, summary.ExtractionFactRecall, 0.0001)
	s.InDelta(0.5, summary.RecallGoldRecall, 0.0001)
	s.InDelta(1.0, summary.RecallConditionalRecall, 0.0001)
	s.Equal(1, summary.UpstreamMissedAtoms)
	s.Equal(0, summary.RecallMissedAvailableAtoms)
	var output bytes.Buffer
	s.Require().NoError(stageeval.WriteResultsJSONL(&output, results))
	s.Equal(2, strings.Count(strings.TrimSpace(output.String()), "\n")+1)
}

func (s *RunSuite) TestRunRejectsIncompleteOrUnknownObservations() {
	tests := []struct {
		name         string
		observations string
	}{
		{name: "missing recall", observations: `{"case_id":"one","stage":"extraction","items":[]}`},
		{name: "unknown case", observations: "{\"case_id\":\"other\",\"stage\":\"extraction\",\"items\":[]}\n{\"case_id\":\"other\",\"stage\":\"recall\",\"items\":[]}"},
		{name: "duplicate stage", observations: "{\"case_id\":\"one\",\"stage\":\"extraction\",\"items\":[]}\n{\"case_id\":\"one\",\"stage\":\"extraction\",\"items\":[]}"},
	}
	fixtures := stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Cases: []stageeval.Fixture{
		{CaseID: "one", SourceRevision: testSourceRevision, RecallContext: testRecallContext(), RequiredAtoms: []stageeval.Atom{{ID: "atom", Patterns: []string{"fact"}}}},
	}}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, _, err := stageeval.Run(fixtures, strings.NewReader(test.observations))
			s.Error(err)
		})
	}
}

func (s *RunSuite) TestRunAggregatesSuppressedLeakage() {
	fixtures := stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Cases: []stageeval.Fixture{
		{CaseID: "one", SourceRevision: testSourceRevision, RecallContext: testRecallContext(),
			ForbiddenAtoms: []stageeval.Atom{{ID: "owner", Patterns: []string{"(?i)compliance.{0,100}owner"}}}},
	}}
	observations := strings.NewReader(strings.Join([]string{
		`{"case_id":"one","stage":"extraction","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","items":[{"id":"n1","text":"Compliance is not the owner."}]}`,
		`{"case_id":"one","stage":"recall","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recall_context":{"consumer_user_id":"user","query":"question","token_budget":512},"items":[{"id":"n1","text":"Compliance is not the owner."}]}`,
	}, "\n"))

	results, summary, err := stageeval.Run(fixtures, observations)

	s.Require().NoError(err)
	s.Require().Len(results, 1)
	s.Equal(1, summary.ExtractionLeakageItems)
	s.Equal(1, summary.ExtractionSuppressedLeakageItems)
	s.Equal(1, summary.RecallLeakageItems)
	s.Equal(1, summary.RecallSuppressedLeakageItems)
	s.Equal(1, results[0].Extraction.SuppressedLeakageItems)
	s.Require().Len(results[0].Extraction.LeakageDetails, 1)
	s.Equal(stageeval.LeakageDetail{
		ItemID: "n1", AtomID: "owner", Suppressed: true, SuppressionCue: "not",
	}, results[0].Extraction.LeakageDetails[0])
}

func (s *RunSuite) TestRunSeparatesObservationErrorsFromQualityScores() {
	fixtures := stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Cases: []stageeval.Fixture{
		{CaseID: "one", SourceRevision: testSourceRevision, RecallContext: testRecallContext(), RequiredAtoms: []stageeval.Atom{{ID: "atom", Patterns: []string{"fact"}}}},
	}}
	observations := strings.NewReader(strings.Join([]string{
		`{"case_id":"one","stage":"extraction","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","items":[],"error":"extractor timeout"}`,
		`{"case_id":"one","stage":"recall","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recall_context":{"consumer_user_id":"user","query":"question","token_budget":512},"items":[],"error":"provider timeout"}`,
	}, "\n"))

	results, summary, err := stageeval.Run(fixtures, observations)

	s.Require().NoError(err)
	s.False(results[0].Extraction.Scored)
	s.False(results[0].Recall.Scored)
	s.Equal(1, summary.ExtractionErrors)
	s.Equal(1, summary.RecallErrors)
	s.Zero(summary.ExtractionScoredAtoms)
	s.Zero(summary.RecallScoredAtoms)
}
