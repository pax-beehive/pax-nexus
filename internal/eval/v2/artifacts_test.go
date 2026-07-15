package v2

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type artifactSuite struct{ suite.Suite }

func TestArtifactSuite(t *testing.T) { suite.Run(t, new(artifactSuite)) }

func (s *artifactSuite) TestSummaryPairwiseAndExport() {
	results := []TrialResult{
		trial("case-1", "temporal", "control", "completed", 0.2, false, 100),
		trial("case-1", "temporal", "memory", "completed", 0.8, true, 120),
		trial("case-2", "multi_hop", "control", "completed", 0.4, false, 90),
		trial("case-2", "multi_hop", "memory", "completed", 0.1, false, 110),
		trial("case-3", "temporal", "control", "failed", 0, false, 10),
		trial("case-3", "temporal", "memory", "completed", 0.5, false, 100),
	}
	summaries := Summarize(results)
	s.NotEmpty(summaries)
	var memoryOverall SummaryRow
	for _, row := range summaries {
		if row.DimensionType == "overall" && row.Arm == "memory" {
			memoryOverall = row
		}
	}
	s.Equal(3, memoryOverall.Completed)
	s.InDelta(0.466666, memoryOverall.MeanTokenF1, 0.00001)
	s.InDelta(0.03, memoryOverall.TotalCost, 0.000001)

	pairs := Pairwise(results, "control")
	s.Require().NotEmpty(pairs)
	s.Equal("all", pairs[0].Category)
	s.Equal(2, pairs[0].Pairs)
	s.Equal(1, pairs[0].Wins)
	s.Equal(1, pairs[0].Losses)
	s.InDelta(0.01, pairs[0].BaselineMeanCost, 0.000001)
	s.InDelta(0.01, pairs[0].CandidateMeanCost, 0.000001)
	s.Zero(pairs[0].MeanDeltaCost)

	costs := CostTotals(results)
	s.Equal("opencode_reported", costs.Scope)
	s.InDelta(0.06, costs.TotalCost, 0.000001)
	s.InDelta(0.03, costs.ByArm["memory"].TotalCost, 0.000001)

	directory := s.T().TempDir()
	run := RunRecord{ID: "run", Dataset: "suite", DatasetRevision: "rev", ConfigHash: "hash"}
	s.Require().NoError(ExportArtifacts(directory, run, "control", []string{"csv", "jsonl", "html"}, results, func(writer io.Writer) error {
		_, err := io.Copy(writer, bytes.NewBufferString("<!doctype html><title>report</title>"))
		return err
	}))
	for _, name := range []string{"trials.jsonl", "trials.csv", "summary.csv", "pairwise.csv", "report.html", "artifacts.json"} {
		info, err := os.Stat(filepath.Join(directory, name))
		s.Require().NoError(err)
		s.Positive(info.Size())
	}
	manifestInput, err := os.ReadFile(filepath.Join(directory, "artifacts.json"))
	s.Require().NoError(err)
	var manifest struct {
		SchemaVersion string            `json:"schema_version"`
		CostSummary   CostSummary       `json:"cost_summary"`
		Files         map[string]string `json:"files"`
	}
	s.Require().NoError(json.Unmarshal(manifestInput, &manifest))
	s.Equal(ArtifactSchemaVersion, manifest.SchemaVersion)
	s.Equal("report.html", manifest.Files["report"])
	s.InDelta(0.06, manifest.CostSummary.TotalCost, 0.000001)
	summaryCSV, err := os.ReadFile(filepath.Join(directory, "summary.csv"))
	s.Require().NoError(err)
	s.Contains(string(summaryCSV), "cost_scope,total_cost,mean_completed_cost")
	pairwiseCSV, err := os.ReadFile(filepath.Join(directory, "pairwise.csv"))
	s.Require().NoError(err)
	s.Contains(string(pairwiseCSV), "paired_completed_incremental_cost")
}

func (s *artifactSuite) TestExportRejectsEmptyResults() {
	s.Require().Error(ExportArtifacts(s.T().TempDir(), RunRecord{}, "control", []string{"csv"}, nil, nil))
	s.Require().Error(ExportArtifacts(s.T().TempDir(), RunRecord{}, "control", []string{"unknown"}, []TrialResult{trial("case", "category", "control", "completed", 1, true, 1)}, nil))
	s.Require().Error(ExportArtifacts(s.T().TempDir(), RunRecord{}, "control", []string{"html"}, []TrialResult{trial("case", "category", "control", "completed", 1, true, 1)}, nil))
}

func trial(caseID, category, arm, status string, f1 float64, exact bool, duration int64) TrialResult {
	return TrialResult{
		RunID: "run", Dataset: "suite", DatasetRevision: "rev", CaseID: caseID, Category: category,
		Arm: arm, Status: status, Expected: "expected", Answer: "answer", TokenF1: f1,
		Exact: exact, SafeSuccess: exact, Cost: 0.01, CostScope: "opencode_reported", InputTokens: 10, OutputTokens: 5, TotalDurationMS: duration,
		StartedAt: time.Unix(1, 0).UTC(), CompletedAt: time.Unix(2, 0).UTC(),
	}
}
