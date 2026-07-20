package recallreplay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const ReportSchemaVersion = "pax-recall-eval-v1"

// CaseReport pairs one case's stage evaluation with its recall stage trace.
type CaseReport struct {
	CaseID            string               `json:"case_id"`
	Result            stageeval.Result     `json:"result"`
	Trace             teamnote.RecallTrace `json:"trace"`
	PlannedItems      []stageeval.Item     `json:"planned_items"`
	AtomLosses        []AtomLoss           `json:"atom_losses"`
	PlannerDurationNS int64                `json:"planner_duration_ns"`
	HintEval          *HintCaseEval        `json:"hint_eval,omitempty"`
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
	RecallEval    RecallEvalSummary `json:"recall_eval"`
	HintEval      HintEvalSummary   `json:"hint_eval"`
	Cases         []CaseReport      `json:"-"`
	LossLedger    []AtomLoss        `json:"-"`
}

// Run replays every case through PlanRecall with the given policy and scores
// the planned deliveries with the shared stage evaluator.
func Run(set FixtureSet, policy Policy) (Report, error) {
	if err := normalizeFixtureSet(&set); err != nil {
		return Report{}, err
	}
	if err := set.Validate(); err != nil {
		return Report{}, err
	}
	stageFixtures := stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Dataset: set.Dataset}
	traces := make(map[string]teamnote.RecallTrace, len(set.Cases))
	plannedByCase := make(map[string][]stageeval.Item, len(set.Cases))
	plannerDurationByCase := make(map[string]int64, len(set.Cases))
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
		candidates := replayCase.plannerRecallCandidates()
		request := replayCase.recallRequest()
		recallPolicy := teamnote.RecallPolicy{
			SemanticThreshold: policy.SemanticThreshold, CandidateLimit: policy.CandidateLimit,
			HintThreshold: policy.HintThreshold, EnableHintRecall: policy.EnableHintRecall,
			SuppressDuplicates: policy.SuppressDuplicates, DegradeRelated: policy.DegradeRelated,
			DisableRelationMarginalUtility: policy.DisableRelationMarginalUtility,
			ObservationTime:                replayCase.ObservationTime,
		}
		plannerStarted := time.Now()
		planned, trace := teamnote.PlanRecall(candidates, request, recallPolicy)
		plannerDurationByCase[replayCase.Fixture.CaseID] = time.Since(plannerStarted).Nanoseconds()
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
		SchemaVersion: ReportSchemaVersion, Dataset: set.Dataset, Policy: policy, Summary: summary,
		StageTotals: StageTotals{
			PlanVersions: map[string]int{}, LaneCandidateCounts: map[string]int{}, Dispositions: map[string]int{},
			Rejections: map[string]int{}, BudgetDrops: map[string]int{},
		},
	}
	for _, result := range results {
		trace := traces[result.CaseID]
		replayCase := replayCaseByID(set.Cases, result.CaseID)
		caseEval, err := evaluateRecallStages(replayCase, result, trace, plannedByCase[result.CaseID])
		if err != nil {
			return Report{}, fmt.Errorf("evaluate recall stages for %q: %w", result.CaseID, err)
		}
		hintCaseEval, hintSummary, err := evaluateHintObservation(replayCase, trace, plannedByCase[result.CaseID])
		if err != nil {
			return Report{}, fmt.Errorf("evaluate hint opportunity for %q: %w", result.CaseID, err)
		}
		caseReport := CaseReport{
			CaseID: result.CaseID, Result: result, Trace: trace,
			PlannedItems: plannedByCase[result.CaseID], AtomLosses: caseEval.losses,
			PlannerDurationNS: plannerDurationByCase[result.CaseID],
		}
		if replayCase.HintObservation != nil {
			caseReport.HintEval = &hintCaseEval
		}
		report.Cases = append(report.Cases, caseReport)
		report.LossLedger = append(report.LossLedger, caseEval.losses...)
		mergeRecallEval(&report.RecallEval, caseEval.summary)
		mergeHintEval(&report.HintEval, hintSummary)
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
	evaluateHintDedup(set.Cases, &report.HintEval)
	finalizeRecallEval(&report.RecallEval, len(results))
	finalizeHintEval(&report.HintEval)
	setPlannerLatency(&report.RecallEval, plannerDurationByCase)
	return report, nil
}

func setPlannerLatency(summary *RecallEvalSummary, durationsByCase map[string]int64) {
	durations := make([]int64, 0, len(durationsByCase))
	var total int64
	for _, duration := range durationsByCase {
		durations = append(durations, duration)
		total += duration
	}
	if len(durations) == 0 {
		return
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	summary.PlannerCalls = len(durations)
	summary.PlannerMeanDurationNS = float64(total) / float64(len(durations))
	nearestRank := (len(durations)*95 + 99) / 100
	summary.PlannerP95DurationNS = durations[nearestRank-1]
}

func replayCaseByID(cases []Case, caseID string) Case {
	for _, replayCase := range cases {
		if replayCase.Fixture.CaseID == caseID {
			return replayCase
		}
	}
	return Case{}
}

func mergeRecallEval(target *RecallEvalSummary, current RecallEvalSummary) {
	target.AvailableAtoms += current.AvailableAtoms
	target.EligibleAtoms += current.EligibleAtoms
	target.IneligibleAtoms += current.IneligibleAtoms
	target.CandidateMatchedAtoms += current.CandidateMatchedAtoms
	target.RelationMatchedAtoms += current.RelationMatchedAtoms
	target.SelectedMatchedAtoms += current.SelectedMatchedAtoms
	target.DeliveredMatchedAtoms += current.DeliveredMatchedAtoms
	target.MeanContextPrecision += current.MeanContextPrecision
	target.BudgetDroppedAtoms += current.BudgetDroppedAtoms
	target.SupersededLeakageItems += current.SupersededLeakageItems
	if target.IdentitySlices == nil {
		target.IdentitySlices = map[IdentityRelationship]RecallSliceSummary{}
	}
	for key, currentSlice := range current.IdentitySlices {
		targetSlice := target.IdentitySlices[key]
		mergeRecallSlice(&targetSlice, currentSlice)
		target.IdentitySlices[key] = targetSlice
	}
	if target.TemporalSlices == nil {
		target.TemporalSlices = map[string]RecallSliceSummary{}
	}
	for key, currentSlice := range current.TemporalSlices {
		targetSlice := target.TemporalSlices[key]
		mergeRecallSlice(&targetSlice, currentSlice)
		target.TemporalSlices[key] = targetSlice
	}
	mergeRecallSlice(&target.StrictCrossAgent, current.StrictCrossAgent)
}

func mergeRecallSlice(target *RecallSliceSummary, current RecallSliceSummary) {
	target.EligibleAtoms += current.EligibleAtoms
	target.CandidateMatchedAtoms += current.CandidateMatchedAtoms
	target.RelationMatchedAtoms += current.RelationMatchedAtoms
	target.SelectedMatchedAtoms += current.SelectedMatchedAtoms
	target.DeliveredMatchedAtoms += current.DeliveredMatchedAtoms
	target.BudgetDroppedAtoms += current.BudgetDroppedAtoms
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

// WriteLossLedgerJSONL writes one required atom's recall stage outcome per line.
func WriteLossLedgerJSONL(writer io.Writer, report Report) error {
	encoder := json.NewEncoder(writer)
	for _, loss := range report.LossLedger {
		if err := encoder.Encode(loss); err != nil {
			return fmt.Errorf("encode recall loss ledger: %w", err)
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
