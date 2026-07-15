package render

import (
	"bytes"
	"testing"
	"time"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/stretchr/testify/suite"
)

type renderSuite struct{ suite.Suite }

func TestRenderSuite(t *testing.T) { suite.Run(t, new(renderSuite)) }

func (s *renderSuite) TestReportUsesConfigArmOrderAndIncludesEveryCase() {
	run := v2.RunRecord{ID: "run-1", Dataset: "suite", DatasetRevision: "rev", Config: v2.Config{Arms: []v2.ArmConfig{{Name: "control"}, {Name: "team_note"}, {Name: "missing"}, {Name: "mem0"}}}}
	results := []v2.TrialResult{
		trial("case-1", "temporal", "mem0", 0.1), trial("case-1", "temporal", "control", 0.2), trial("case-1", "temporal", "team_note", 0.3),
		trial("case-2", "abstention", "control", 0.4), trial("case-2", "abstention", "team_note", 0), trial("case-2", "abstention", "mem0", 0.2),
	}
	data := buildReportData(run, "control", results)
	s.Equal([]string{"control", "team_note", "mem0"}, []string{data.Arms[0].Name, data.Arms[1].Name, data.Arms[2].Name})
	s.Require().Len(data.CaseGroups, 2)
	s.Equal(2, data.CaseCount)

	var output bytes.Buffer
	s.Require().NoError(Report(run, "control", results, &output))
	html := output.String()
	s.Contains(html, "All case breakdown")
	s.Contains(html, "case-1")
	s.Contains(html, "case-2")
	s.Contains(html, "<progress")
	s.NotContains(html, "missing")
	s.NotContains(html, "ZgotmplZ")
}

func (s *renderSuite) TestNiceMax() {
	tests := []struct {
		name     string
		input    float64
		expected float64
	}{
		{name: "zero", input: 0, expected: 1},
		{name: "negative", input: -5, expected: 1},
		{name: "fraction", input: 0.108, expected: 0.2},
		{name: "small fraction", input: 0.00226, expected: 0.005},
		{name: "hundreds", input: 124, expected: 200},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.InDelta(test.expected, niceMax(test.input), 0.0000001)
		})
	}
}

func (s *renderSuite) TestRepresentativeCasesUseLargestMeanCategoryShift() {
	pairs := []caseDelta{
		{caseID: "largest-win", category: "mixed", delta: 0.9},
		{caseID: "largest-loss", category: "mixed", delta: -0.8},
		{caseID: "shift-category", category: "shift", delta: 0.7},
		{caseID: "small-category", category: "small", delta: 0.2},
	}
	selections := selectRepresentativeCases(pairs)
	s.Require().Len(selections, 3)
	s.Equal("largest-win", selections[0].caseID)
	s.Equal("largest-loss", selections[1].caseID)
	s.Equal("shift-category", selections[2].caseID)
	s.Equal("largest category shift", selections[2].tag)
}

func (s *renderSuite) TestFieldNotesDegradeForSparseComparisons() {
	tests := []struct {
		name        string
		results     []v2.TrialResult
		expectedLen int
	}{
		{
			name: "candidate never loses",
			results: []v2.TrialResult{
				trial("one", "a", "control", 0.1), trial("one", "a", "memory", 0.5),
				trial("two", "a", "control", 0.2), trial("two", "a", "memory", 0.3),
			},
			expectedLen: 2,
		},
		{
			name: "candidate has zero completed trials",
			results: []v2.TrialResult{
				trial("one", "a", "control", 0.1), failedTrial("one", "a", "memory"),
			},
			expectedLen: 0,
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			run := v2.RunRecord{Config: v2.Config{Arms: []v2.ArmConfig{{Name: "control"}, {Name: "memory"}}}}
			data := buildReportData(run, "control", test.results)
			s.Len(data.FieldNotes, test.expectedLen)
			s.Require().Len(data.Arms, 2)
			if test.name == "candidate never loses" {
				for _, note := range data.FieldNotes {
					s.NotContains(note.Tag, "loss")
				}
			}
			if test.name == "candidate has zero completed trials" {
				s.Equal("0.000", data.Arms[1].MeanF1Display)
			}
		})
	}
}

func (s *renderSuite) TestFieldNotesShowFailedThirdArm() {
	run := v2.RunRecord{Config: v2.Config{Arms: []v2.ArmConfig{{Name: "control"}, {Name: "memory"}, {Name: "third"}}}}
	results := []v2.TrialResult{
		trial("one", "a", "control", 0.1),
		trial("one", "a", "memory", 0.5),
		failedTrial("one", "a", "third"),
	}
	data := buildReportData(run, "control", results)
	s.Require().Len(data.FieldNotes, 1)
	s.Require().Len(data.FieldNotes[0].Answers, 3)
	s.Equal("third", data.FieldNotes[0].Answers[2].Arm)
	s.Equal("failed", data.FieldNotes[0].Answers[2].Status)
	s.Equal("failed", data.FieldNotes[0].Answers[2].Error)
}

func (s *renderSuite) TestSeriesClassesRemainUniqueBeyondBasePalette() {
	seen := make(map[string]bool)
	for index := range 20 {
		className := seriesClass(index)
		s.False(seen[className], "class %q was reused", className)
		seen[className] = true
	}
}

func (s *renderSuite) TestReportEscapesAnswers() {
	run := v2.RunRecord{ID: "run", Config: v2.Config{Arms: []v2.ArmConfig{{Name: "control"}, {Name: "memory"}}}}
	results := []v2.TrialResult{trial("case", "category", "control", 0), trial("case", "category", "memory", 1)}
	results[1].Answer = `<script>alert("unsafe")</script>`
	var output bytes.Buffer
	s.Require().NoError(Report(run, "control", results, &output))
	s.NotContains(output.String(), "<script>")
	s.Contains(output.String(), "&lt;script&gt;")
}

func trial(caseID, category, arm string, f1 float64) v2.TrialResult {
	return v2.TrialResult{
		RunID: "run", Dataset: "suite", CaseID: caseID, Category: category, Question: "question " + caseID,
		Arm: arm, Status: "completed", Expected: "expected", Answer: "answer " + arm, TokenF1: f1,
		Cost: 0.01, TotalDurationMS: 1000, StartedAt: time.Unix(1, 0).UTC(), CompletedAt: time.Unix(2, 0).UTC(),
	}
}

func failedTrial(caseID, category, arm string) v2.TrialResult {
	result := trial(caseID, category, arm, 0)
	result.Status = "failed"
	result.Error = "failed"
	return result
}
