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
	run := v2.RunRecord{ID: "run-1", Dataset: "suite", DatasetRevision: "rev", Config: v2.Config{Arms: []v2.ArmConfig{{Name: "control"}, {Name: "team_note"}, {Name: "mem0"}}}}
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
	s.NotContains(html, "ZgotmplZ")
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
