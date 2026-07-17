package extractionshadow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

// CaseReport pairs one case's stage evaluation with its shadow telemetry.
type CaseReport struct {
	CaseID  string           `json:"case_id"`
	Result  stageeval.Result `json:"result"`
	Streams int              `json:"streams"`
	Events  int              `json:"events"`
	Notes   int              `json:"notes"`
	Slices  int              `json:"slices"`
}

// Telemetry aggregates extraction usage and v2 products across the cohort.
type Telemetry struct {
	Calls                      int            `json:"calls"`
	CallErrors                 int            `json:"call_errors"`
	InputTokens                int            `json:"input_tokens"`
	OutputTokens               int            `json:"output_tokens"`
	CacheHitTokens             int            `json:"cache_hit_tokens"`
	CacheMissTokens            int            `json:"cache_miss_tokens"`
	CacheHitRate               float64        `json:"cache_hit_rate"`
	MeanDurationMS             float64        `json:"mean_duration_ms"`
	P95DurationMS              int64          `json:"p95_duration_ms"`
	Slices                     int            `json:"slices"`
	SliceRejections            int            `json:"slice_rejections"`
	Claims                     int            `json:"claims,omitempty"`
	StateDecisions             int            `json:"state_decisions,omitempty"`
	ClaimRejections            int            `json:"claim_rejections,omitempty"`
	DecisionRejections         int            `json:"decision_rejections,omitempty"`
	NoStateEvents              int            `json:"no_state_events,omitempty"`
	UnreviewedEvents           int            `json:"unreviewed_events,omitempty"`
	SlicesWithUnreviewedEvents int            `json:"slices_with_unreviewed_events,omitempty"`
	InvalidNoStateEvents       int            `json:"invalid_no_state_events,omitempty"`
	OrphanClaims               int            `json:"orphan_claims,omitempty"`
	DecisionDist               map[string]int `json:"decision_distribution,omitempty"`
	WouldVerifySlices          int            `json:"would_verify_slices,omitempty"`
	WouldVerifyDist            map[string]int `json:"would_verify_distribution,omitempty"`

	durations []int64
}

// Report is one shadow replay over the fixed cohort.
type Report struct {
	SchemaVersion string            `json:"schema_version"`
	RunID         string            `json:"run_id"`
	Arm           string            `json:"arm"`
	Extractor     string            `json:"extractor"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Stage         stageeval.Summary `json:"stage_summary"`
	Telemetry     Telemetry         `json:"telemetry"`
	Cases         []CaseReport      `json:"cases"`
}

const SchemaVersion = "pax-extraction-shadow-v1"

// BuildReport scores the replayed cases with the unchanged stage evaluator
// and aggregates shadow telemetry.
func BuildReport(runID, arm, extractorVersion string, fixtures stageeval.FixtureSet, runs []CaseRun) (Report, error) {
	if len(runs) == 0 {
		return Report{}, fmt.Errorf("build extraction shadow report: at least one case run is required")
	}
	var observations bytes.Buffer
	encoder := json.NewEncoder(&observations)
	report := Report{
		SchemaVersion: SchemaVersion, RunID: runID, Arm: arm, Extractor: extractorVersion,
		GeneratedAt: time.Now().UTC(), Telemetry: Telemetry{DecisionDist: map[string]int{}, WouldVerifyDist: map[string]int{}},
	}
	runsByID := make(map[string]CaseRun, len(runs))
	for _, run := range runs {
		runsByID[run.CaseID] = run
	}
	for _, fixture := range fixtures.Cases {
		run, ok := runsByID[fixture.CaseID]
		if !ok {
			return Report{}, fmt.Errorf("build extraction shadow report: missing replay for case %q", fixture.CaseID)
		}
		extraction := stageeval.Observation{
			CaseID: run.CaseID, Stage: stageeval.StageExtraction, SourceRevision: fixture.SourceRevision,
			Items: noteItems(run.Notes), DurationMS: totalDurationMS(run.Slices),
		}
		if err := encoder.Encode(extraction); err != nil {
			return Report{}, fmt.Errorf("encode shadow extraction observation: %w", err)
		}
		recallContext := fixture.RecallContext
		recall := stageeval.Observation{
			CaseID: run.CaseID, Stage: stageeval.StageRecall, SourceRevision: fixture.SourceRevision,
			RecallContext: &recallContext,
		}
		if err := encoder.Encode(recall); err != nil {
			return Report{}, fmt.Errorf("encode shadow recall observation: %w", err)
		}
		report.Cases = append(report.Cases, CaseReport{
			CaseID: run.CaseID, Streams: run.Streams, Events: run.Events, Notes: len(run.Notes), Slices: len(run.Slices),
		})
		report.Telemetry.add(run.Slices)
	}
	results, summary, err := stageeval.Run(fixtures, &observations)
	if err != nil {
		return Report{}, err
	}
	report.Stage = summary
	resultsByID := make(map[string]stageeval.Result, len(results))
	for _, result := range results {
		resultsByID[result.CaseID] = result
	}
	for index := range report.Cases {
		report.Cases[index].Result = resultsByID[report.Cases[index].CaseID]
	}
	report.Telemetry.finalize()
	return report, nil
}

func noteItems(notes []teamnote.Note) []stageeval.Item {
	items := make([]stageeval.Item, 0, len(notes))
	for _, note := range notes {
		items = append(items, stageeval.Item{ID: note.ID, Text: note.Body, EvidenceEventIDs: note.EvidenceEventIDs})
	}
	return items
}

func totalDurationMS(slices []SliceRecord) int64 {
	var total int64
	for _, slice := range slices {
		total += slice.DurationMS
	}
	return total
}

func (t *Telemetry) add(slices []SliceRecord) {
	for _, slice := range slices {
		t.Calls++
		t.Slices++
		if slice.Error != "" {
			t.CallErrors++
		}
		t.InputTokens += slice.Usage.InputTokens
		t.OutputTokens += slice.Usage.OutputTokens
		t.CacheHitTokens += slice.Usage.PromptCacheHitTokens
		t.CacheMissTokens += slice.Usage.PromptCacheMissTokens
		t.SliceRejections += slice.Rejections
		t.Claims += slice.Claims
		t.StateDecisions += slice.StateDecisions
		t.ClaimRejections += slice.ClaimRejections
		t.DecisionRejections += slice.DecisionRejections
		t.NoStateEvents += slice.NoStateEvents
		t.UnreviewedEvents += slice.UnreviewedEvents
		if slice.UnreviewedEvents > 0 {
			t.SlicesWithUnreviewedEvents++
		}
		t.InvalidNoStateEvents += slice.InvalidNoStateEvents
		t.OrphanClaims += slice.OrphanClaims
		if slice.Trace != nil {
			for _, decision := range slice.Trace.StateDecisions {
				t.DecisionDist[string(decision.Decision)]++
			}
		}
		if len(slice.WouldVerify) > 0 {
			t.WouldVerifySlices++
			for _, class := range slice.WouldVerify {
				t.WouldVerifyDist[class]++
			}
		}
	}
	t.durations = append(t.durations, durationsOf(slices)...)
}

func durationsOf(slices []SliceRecord) []int64 {
	durations := make([]int64, 0, len(slices))
	for _, slice := range slices {
		durations = append(durations, slice.DurationMS)
	}
	return durations
}

func (t *Telemetry) finalize() {
	if total := t.CacheHitTokens + t.CacheMissTokens; total > 0 {
		t.CacheHitRate = float64(t.CacheHitTokens) / float64(total)
	}
	sort.Slice(t.durations, func(left, right int) bool { return t.durations[left] < t.durations[right] })
	if len(t.durations) > 0 {
		var total int64
		for _, duration := range t.durations {
			total += duration
		}
		t.MeanDurationMS = float64(total) / float64(len(t.durations))
		t.P95DurationMS = percentile(t.durations, 0.95)
	}
	if len(t.durations) == 0 {
		t.P95DurationMS = 0
	}
}

func percentile(sorted []int64, quantile float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * quantile)
	return sorted[index]
}

// WriteReport persists the shadow report as indented JSON.
func WriteReport(writer interface{ Write([]byte) (int, error) }, report Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode extraction shadow report: %w", err)
	}
	return nil
}
