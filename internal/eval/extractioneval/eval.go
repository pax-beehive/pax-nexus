// Package extractioneval evaluates extraction protocols against fixed,
// persisted event domains without running recall or answer generation.
package extractioneval

import (
	"fmt"
	"sort"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/extractionshadow"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
)

const SchemaVersion = "pax-extraction-eval-v1"

// DomainPlan groups all cases that share one persisted event scope. The
// extractor must run once per plan, not once per question.
type DomainPlan struct {
	ScopeID string   `json:"scope_id"`
	CaseIDs []string `json:"case_ids"`
}

// PlanDomains resolves manifest scope suffixes and groups shared domains while
// retaining manifest order.
func PlanDomains(sourceRunID, scopeSuffix string, cases []extractionshadow.ManifestCase) ([]DomainPlan, error) {
	if sourceRunID == "" {
		return nil, fmt.Errorf("plan extraction eval domains: source run id is required")
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("plan extraction eval domains: at least one case is required")
	}
	plans := make([]DomainPlan, 0, len(cases))
	indexByScope := make(map[string]int, len(cases))
	seenCases := make(map[string]struct{}, len(cases))
	for _, manifestCase := range cases {
		if manifestCase.ID == "" || manifestCase.ScopeID == "" {
			return nil, fmt.Errorf("plan extraction eval domains: case id and scope id are required")
		}
		if _, exists := seenCases[manifestCase.ID]; exists {
			return nil, fmt.Errorf("plan extraction eval domains: duplicate case id %q", manifestCase.ID)
		}
		seenCases[manifestCase.ID] = struct{}{}
		scopeID := sourceRunID + "-" + manifestCase.ScopeID + scopeSuffix
		index, exists := indexByScope[scopeID]
		if !exists {
			index = len(plans)
			indexByScope[scopeID] = index
			plans = append(plans, DomainPlan{ScopeID: scopeID})
		}
		plans[index].CaseIDs = append(plans[index].CaseIDs, manifestCase.ID)
	}
	return plans, nil
}

// ExtractionSummary contains only extraction-stage cohort metrics. Recall
// metrics are intentionally absent from this protocol.
type ExtractionSummary struct {
	Cases                  int     `json:"cases"`
	RequiredAtoms          int     `json:"required_atoms"`
	ScoredAtoms            int     `json:"scored_atoms"`
	MatchedAtoms           int     `json:"matched_atoms"`
	FactRecall             float64 `json:"fact_recall"`
	Errors                 int     `json:"errors"`
	LeakageItems           int     `json:"leakage_items"`
	SuppressedLeakageItems int     `json:"suppressed_leakage_items"`
}

// CaseReport is one question-specific atom score over a shared domain output.
type CaseReport struct {
	CaseID     string                                `json:"case_id"`
	Category   string                                `json:"category,omitempty"`
	ScopeID    string                                `json:"scope_id"`
	Extraction stageeval.ExtractionResult            `json:"extraction"`
	AtomLosses []extractionshadow.ExtractionAtomLoss `json:"atom_losses,omitempty"`
}

// DomainReport records one physical extraction replay and the cases scored
// from its output.
type DomainReport struct {
	ScopeID        string   `json:"scope_id"`
	CaseIDs        []string `json:"case_ids"`
	Streams        int      `json:"streams"`
	Events         int      `json:"events"`
	SourceEvents   int      `json:"source_events,omitempty"`
	SelectedEvents int      `json:"selected_events,omitempty"`
	ExpectedSlices int      `json:"expected_slices,omitempty"`
	Notes          int      `json:"notes"`
	Slices         int      `json:"slices"`
}

// Report is the independent extraction-eval-v1 artifact.
type Report struct {
	SchemaVersion string                                `json:"schema_version"`
	RunID         string                                `json:"run_id"`
	SourceRunID   string                                `json:"source_run_id"`
	Dataset       string                                `json:"dataset"`
	Extractor     string                                `json:"extractor"`
	V2Variant     string                                `json:"v2_variant,omitempty"`
	Profile       string                                `json:"profile,omitempty"`
	GeneratedAt   time.Time                             `json:"generated_at"`
	Summary       ExtractionSummary                     `json:"extraction_summary"`
	Telemetry     extractionshadow.Telemetry            `json:"telemetry"`
	Domains       []DomainReport                        `json:"domains"`
	Cases         []CaseReport                          `json:"cases"`
	LossLedger    []extractionshadow.ExtractionAtomLoss `json:"-"`
}

// BuildReport expands each physical domain replay into case-level scoring. A
// shared scope contributes telemetry once even when it serves many cases.
func BuildReport(
	runID, sourceRunID, extractorVersion string,
	fixtures stageeval.FixtureSet, plans []DomainPlan, domainRuns []extractionshadow.CaseRun,
) (Report, error) {
	if runID == "" {
		return Report{}, fmt.Errorf("build extraction eval report: run id is required")
	}
	runsByScope := make(map[string]extractionshadow.CaseRun, len(domainRuns))
	for _, run := range domainRuns {
		if _, exists := runsByScope[run.ScopeID]; exists {
			return Report{}, fmt.Errorf("build extraction eval report: duplicate domain run for %q", run.ScopeID)
		}
		runsByScope[run.ScopeID] = run
	}
	caseScope := make(map[string]string)
	caseRuns := make([]extractionshadow.CaseRun, 0, len(fixtures.Cases))
	domains := make([]DomainReport, 0, len(plans))
	for _, plan := range plans {
		run, exists := runsByScope[plan.ScopeID]
		if !exists {
			return Report{}, fmt.Errorf("build extraction eval report: missing domain run for %q", plan.ScopeID)
		}
		domains = append(domains, DomainReport{
			ScopeID: plan.ScopeID, CaseIDs: append([]string(nil), plan.CaseIDs...), Streams: run.Streams,
			Events: run.Events, Notes: len(run.Notes), Slices: len(run.Slices),
		})
		for _, caseID := range plan.CaseIDs {
			if _, exists := caseScope[caseID]; exists {
				return Report{}, fmt.Errorf("build extraction eval report: case %q belongs to multiple domains", caseID)
			}
			caseScope[caseID] = plan.ScopeID
			caseRun := run
			caseRun.CaseID = caseID
			caseRuns = append(caseRuns, caseRun)
		}
	}
	shadowReport, err := extractionshadow.BuildReport(sourceRunID, "source", extractorVersion, fixtures, caseRuns)
	if err != nil {
		return Report{}, fmt.Errorf("build extraction eval report: %w", err)
	}
	report := Report{
		SchemaVersion: SchemaVersion, RunID: runID, SourceRunID: sourceRunID, Dataset: fixtures.Dataset,
		Extractor: extractorVersion, GeneratedAt: time.Now().UTC(), Telemetry: shadowReport.Telemetry, Domains: domains,
		LossLedger: append([]extractionshadow.ExtractionAtomLoss(nil), shadowReport.LossLedger...),
		Summary: ExtractionSummary{
			Cases: shadowReport.Stage.Cases, RequiredAtoms: shadowReport.Stage.RequiredAtoms,
			ScoredAtoms: shadowReport.Stage.ExtractionScoredAtoms, MatchedAtoms: shadowReport.Stage.ExtractionMatchedAtoms,
			FactRecall: shadowReport.Stage.ExtractionFactRecall, Errors: shadowReport.Stage.ExtractionErrors,
			LeakageItems:           shadowReport.Stage.ExtractionLeakageItems,
			SuppressedLeakageItems: shadowReport.Stage.ExtractionSuppressedLeakageItems,
		},
	}
	for _, scored := range shadowReport.Cases {
		report.Cases = append(report.Cases, CaseReport{
			CaseID: scored.CaseID, Category: scored.Result.Category, ScopeID: caseScope[scored.CaseID],
			Extraction: scored.Result.Extraction, AtomLosses: scored.AtomLosses,
		})
	}
	sort.Slice(report.Cases, func(left, right int) bool { return report.Cases[left].CaseID < report.Cases[right].CaseID })
	return report, nil
}
