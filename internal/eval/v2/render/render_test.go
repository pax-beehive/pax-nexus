package render

import (
	"bytes"
	"strings"
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

func (s *renderSuite) TestEvalV3ReportIncludesPrivateTeamNoteAgainstMem0Comparison() {
	run := v2.RunRecord{Config: v2.Config{Version: "v3", Arms: []v2.ArmConfig{
		{Name: "no_memory_team"}, {Name: "groupmembench_mem0"}, {Name: "private_sqlite_plus_team_note"},
	}}}
	results := []v2.TrialResult{
		trial("case", "temporal", "no_memory_team", 0.1),
		trial("case", "temporal", "groupmembench_mem0", 0.5),
		trial("case", "temporal", "private_sqlite_plus_team_note", 0.8),
	}
	for index := range results {
		results[index].Judged = true
	}
	results[2].Correct = true

	data := buildReportData(run, "no_memory_team", results)

	found := false
	for _, row := range data.Pairwise {
		if row.BaselineArm == "groupmembench_mem0" && row.CandidateArm == "private_sqlite_plus_team_note" && row.RecordDisplay == "1 W / 0 L / 0 T" {
			found = true
		}
	}
	s.True(found)
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
				trial("three", "b", "control", 0.2), trial("three", "b", "memory", 0.25),
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
				s.Len(data.AcceptanceCases, 3)
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

func (s *renderSuite) TestAcceptanceCasesPreserveArmColumnsWhenResultMissing() {
	run := v2.RunRecord{Config: v2.Config{Arms: []v2.ArmConfig{{Name: "control"}, {Name: "memory"}, {Name: "third"}}}}
	results := []v2.TrialResult{
		trial("one", "a", "control", 0.1), trial("one", "a", "memory", 0.9),
		trial("two", "b", "control", 0.8), trial("two", "b", "memory", 0.1), trial("two", "b", "third", 0.2),
	}
	data := buildReportData(run, "control", results)
	s.Require().NotEmpty(data.AcceptanceCases)
	answers := data.AcceptanceCases[0].Answers
	s.Require().Len(answers, 3)
	s.Equal([]string{"control", "memory", "third"}, []string{answers[0].Arm, answers[1].Arm, answers[2].Arm})
	s.Equal("missing", answers[2].Status)
	s.Equal("—", answers[2].F1Display)
	s.Equal("No trial result for this arm.", answers[2].Error)
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

func (s *renderSuite) TestReportIncludesThreeCaseAcceptanceBreakdownTable() {
	run := v2.RunRecord{ID: "run", Config: v2.Config{Arms: []v2.ArmConfig{{Name: "control"}, {Name: "team_note"}, {Name: "mem0"}}}}
	results := []v2.TrialResult{
		trial("win", "temporal", "control", 0.1), trial("win", "temporal", "team_note", 0.9), trial("win", "temporal", "mem0", 0.2),
		trial("loss", "abstention", "control", 0.5), trial("loss", "abstention", "team_note", 0.1), trial("loss", "abstention", "mem0", 0.3),
		trial("shift", "temporal", "control", 0.1), trial("shift", "temporal", "team_note", 0.6), trial("shift", "temporal", "mem0", 0.2),
	}
	var output bytes.Buffer
	s.Require().NoError(Report(run, "control", results, &output))
	html := output.String()
	s.Contains(html, "Three-case acceptance breakdown")
	s.Contains(html, "Expected answer")
	s.Equal(3, strings.Count(html, `class="acceptance-row`))
	for _, arm := range []string{"control", "team_note", "mem0"} {
		s.Contains(html, `<th>`+arm+`</th>`)
	}
}

func (s *renderSuite) TestReportShowsMemoryIngestNoOpBreakdown() {
	run := v2.RunRecord{ID: "run", Config: v2.Config{Arms: []v2.ArmConfig{{Name: "control"}, {Name: "mem0"}, {Name: "team_note"}}}}
	results := []v2.TrialResult{
		trial("one", "temporal", "control", 0.1),
		trial("one", "temporal", "mem0", 0.1),
		trial("one", "temporal", "team_note", 0.1),
		trial("two", "multi_hop", "control", 0.1),
		trial("two", "multi_hop", "mem0", 0.1),
	}
	results[1].MemoryIngestProvider = "mem0"
	results[1].MemoryIngestCreated = 3
	results[1].MemoryIngestUpdated = 1
	results[1].MemoryIngestDeleted = 2
	results[1].MemoryIngestNoOpKnown = true
	results[1].MemorySourceEvents = 32
	results[1].MemorySourceActors = 7
	results[1].MemorySourceSessions = 9
	results[2].MemoryIngestProvider = "team_note"
	results[2].MemoryIngestAccepted = 1
	results[4].MemoryIngestProvider = "mem0"
	results[4].MemoryIngestNoOpKnown = true
	results[4].MemoryIngestNoOp = true

	var output bytes.Buffer
	s.Require().NoError(Report(run, "control", results, &output))
	html := output.String()
	start := strings.Index(html, "Memory ingest receipts")
	s.Require().GreaterOrEqual(start, 0)
	end := strings.Index(html[start:], "Metric profiles")
	s.Require().Positive(end)
	ingestHTML := html[start : start+end]
	s.Contains(ingestHTML, "Case")
	s.Contains(ingestHTML, "Created")
	s.Contains(ingestHTML, "Updated")
	s.Contains(ingestHTML, "Deleted")
	s.Contains(ingestHTML, "Source events")
	s.Contains(ingestHTML, "Source actors")
	s.Contains(ingestHTML, "Source sessions")
	s.Contains(ingestHTML, `<td class="num">32</td><td class="num">7</td><td class="num">9</td>`)
	s.Contains(ingestHTML, `<td class="mono">one</td><td>temporal</td><td>mem0</td>`)
	s.Contains(ingestHTML, `<td class="num">3</td><td class="num">1</td><td class="num">2</td><td>no</td>`)
	s.Contains(ingestHTML, `<td class="mono">two</td><td>multi_hop</td><td>mem0</td>`)
	s.Contains(ingestHTML, `<td>yes</td>`)
	s.Contains(ingestHTML, `<td>unknown</td>`)
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
