package recallreplay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

// CaseReport pairs one case's stage evaluation with its recall stage trace.
type CaseReport struct {
	CaseID       string               `json:"case_id"`
	Result       stageeval.Result     `json:"result"`
	Trace        teamnote.RecallTrace `json:"trace"`
	PlannedItems []stageeval.Item     `json:"planned_items"`
}

// StageTotals aggregates recall stage counters across the cohort.
type StageTotals struct {
	Candidates          int            `json:"candidates"`
	FusionKept          int            `json:"fusion_kept"`
	PlannedNotes        int            `json:"planned_notes"`
	PlannedTokens       int            `json:"planned_tokens"`
	PlanVersions        map[string]int `json:"plan_versions"`
	LaneCandidateCounts map[string]int `json:"lane_candidate_counts"`
	Dispositions        map[string]int `json:"dispositions"`
	Rejections          map[string]int `json:"rejections"`
	BudgetDrops         map[string]int `json:"budget_drops"`
}

// Report is one replay run over a fixed cohort.
type Report struct {
	SchemaVersion string            `json:"schema_version"`
	Dataset       string            `json:"dataset,omitempty"`
	Policy        Policy            `json:"policy"`
	Summary       stageeval.Summary `json:"stage_summary"`
	StageTotals   StageTotals       `json:"stage_totals"`
	Cases         []CaseReport      `json:"-"`
}

// Run replays every case through PlanRecall with the given policy and scores
// the planned deliveries with the shared stage evaluator.
func Run(set FixtureSet, policy Policy) (Report, error) {
	if err := set.Validate(); err != nil {
		return Report{}, err
	}
	stageFixtures := stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Dataset: set.Dataset}
	traces := make(map[string]teamnote.RecallTrace, len(set.Cases))
	plannedByCase := make(map[string][]stageeval.Item, len(set.Cases))
	var observations bytes.Buffer
	encoder := json.NewEncoder(&observations)
	for _, replayCase := range set.Cases {
		stageFixtures.Cases = append(stageFixtures.Cases, replayCase.Fixture)
		extraction := stageeval.Observation{
			CaseID: replayCase.Fixture.CaseID, Stage: stageeval.StageExtraction,
			SourceRevision: replayCase.Fixture.SourceRevision, Items: replayCase.ExtractionItems,
		}
		if err := encoder.Encode(extraction); err != nil {
			return Report{}, fmt.Errorf("encode replay extraction observation: %w", err)
		}
		planned, trace := teamnote.PlanRecall(replayCase.recallCandidates(), replayCase.recallRequest(), teamnote.RecallPolicy{
			SemanticThreshold: policy.SemanticThreshold, CandidateLimit: policy.CandidateLimit,
			SuppressDuplicates: policy.SuppressDuplicates, DegradeRelated: policy.DegradeRelated,
			ObservationTime: replayCase.ObservationTime,
		})
		traces[replayCase.Fixture.CaseID] = trace
		plannedByCase[replayCase.Fixture.CaseID] = plannedItems(planned)
		recallContext := replayCase.Fixture.RecallContext
		recall := stageeval.Observation{
			CaseID: replayCase.Fixture.CaseID, Stage: stageeval.StageRecall,
			SourceRevision: replayCase.Fixture.SourceRevision, RecallContext: &recallContext,
			Items: plannedByCase[replayCase.Fixture.CaseID],
		}
		if err := encoder.Encode(recall); err != nil {
			return Report{}, fmt.Errorf("encode replay recall observation: %w", err)
		}
	}
	results, summary, err := stageeval.Run(stageFixtures, &observations)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		SchemaVersion: SchemaVersion, Dataset: set.Dataset, Policy: policy, Summary: summary,
		StageTotals: StageTotals{
			PlanVersions: map[string]int{}, LaneCandidateCounts: map[string]int{}, Dispositions: map[string]int{},
			Rejections: map[string]int{}, BudgetDrops: map[string]int{},
		},
	}
	for _, result := range results {
		trace := traces[result.CaseID]
		report.Cases = append(report.Cases, CaseReport{
			CaseID: result.CaseID, Result: result, Trace: trace,
			PlannedItems: plannedByCase[result.CaseID],
		})
		report.StageTotals.Candidates += trace.Candidates
		report.StageTotals.FusionKept += trace.FusionKept
		report.StageTotals.PlannedNotes += trace.PlannedNotes
		report.StageTotals.PlannedTokens += trace.PlannedTokens
		report.StageTotals.PlanVersions[trace.PlanVersion]++
		for _, candidate := range trace.CandidateTraces {
			report.StageTotals.Dispositions[string(candidate.Disposition)]++
			for _, lane := range candidate.RetrievalLanes {
				report.StageTotals.LaneCandidateCounts[string(lane)]++
			}
		}
		for _, rejection := range trace.Rejections {
			report.StageTotals.Rejections[string(rejection.Reason)]++
		}
		for _, rejection := range trace.BudgetDrops {
			report.StageTotals.BudgetDrops[string(rejection.Reason)]++
		}
	}
	return report, nil
}

func plannedItems(planned []teamnote.PlannedRecall) []stageeval.Item {
	items := make([]stageeval.Item, 0, len(planned))
	for _, delivery := range planned {
		items = append(items, stageeval.Item{
			ID: delivery.Note.ID, SourceItemIDs: delivery.SourceNoteIDs,
			Text: delivery.Text, EvidenceEventIDs: delivery.Note.EvidenceEventIDs,
		})
	}
	return items
}

// WriteResultsJSONL writes one case report per line.
func WriteResultsJSONL(writer io.Writer, report Report) error {
	encoder := json.NewEncoder(writer)
	for _, caseReport := range report.Cases {
		if err := encoder.Encode(caseReport); err != nil {
			return fmt.Errorf("encode recall replay result: %w", err)
		}
	}
	return nil
}

// WriteSummaryJSON writes the aggregate replay report without per-case detail.
func WriteSummaryJSON(writer io.Writer, report Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode recall replay summary: %w", err)
	}
	return nil
}
