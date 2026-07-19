package recallreplay

import (
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

// LossStage identifies the first recall stage that lost an Available Atom.
type LossStage string

const (
	LossStageCandidateRetrieval LossStage = "candidate_retrieval"
	LossStageRelationExpansion  LossStage = "relation_expansion"
	LossStageSetSelection       LossStage = "set_selection"
	LossStageBudgetPacking      LossStage = "budget_packing"
)

// RecallEvalSummary is the fixed-extraction recall stage waterfall.
type RecallEvalSummary struct {
	AvailableAtoms             int     `json:"available_atoms"`
	CandidateMatchedAtoms      int     `json:"candidate_matched_atoms"`
	CandidateRecallAtLimit     float64 `json:"candidate_recall_at_limit"`
	RelationMatchedAtoms       int     `json:"relation_matched_atoms"`
	RelationExpandedRecall     float64 `json:"relation_expanded_recall"`
	SelectedMatchedAtoms       int     `json:"selected_matched_atoms"`
	SelectedSetRecall          float64 `json:"selected_set_recall"`
	DeliveredMatchedAtoms      int     `json:"delivered_matched_atoms"`
	DeliveredConditionalRecall float64 `json:"delivered_conditional_recall"`
	MeanContextPrecision       float64 `json:"mean_context_precision"`
	BudgetDroppedAtoms         int     `json:"budget_dropped_atoms"`
	SupersededLeakageItems     int     `json:"superseded_leakage_items"`
}

// AtomLoss records the recall stage outcome for one required atom.
type AtomLoss struct {
	CaseID            string    `json:"case_id"`
	AtomID            string    `json:"atom_id"`
	SupportingNoteIDs []string  `json:"supporting_note_ids,omitempty"`
	Available         bool      `json:"available"`
	CandidateKept     bool      `json:"candidate_kept"`
	RelationReachable bool      `json:"relation_reachable"`
	RelationExpanded  bool      `json:"relation_expanded"`
	Selected          bool      `json:"selected"`
	Delivered         bool      `json:"delivered"`
	LostAt            LossStage `json:"lost_at,omitempty"`
	Reason            string    `json:"reason,omitempty"`
}

type caseRecallEval struct {
	summary RecallEvalSummary
	losses  []AtomLoss
}

func evaluateRecallStages(
	replayCase Case,
	result stageeval.Result,
	trace teamnote.RecallTrace,
	plannedItems []stageeval.Item,
) (caseRecallEval, error) {
	supports := replayCase.AtomSupports
	direct := recallLaneIDs(trace, false)
	reachable := stringSet(trace.RelationEligibleSet)
	expanded := recallLaneIDs(trace, true)
	selected := stringSet(trace.PreBudgetSelectedSet)
	delivered := plannedSourceIDs(plannedItems)
	resultEval := caseRecallEval{
		summary: RecallEvalSummary{MeanContextPrecision: result.Recall.ContextPrecision},
		losses:  make([]AtomLoss, 0, len(supports)),
	}
	for _, support := range supports {
		loss := AtomLoss{
			CaseID: replayCase.Fixture.CaseID, AtomID: support.AtomID,
			SupportingNoteIDs: append([]string(nil), support.ItemIDs...), Available: len(support.ItemIDs) > 0,
		}
		if loss.Available {
			resultEval.summary.AvailableAtoms++
			loss.CandidateKept = intersects(support.ItemIDs, direct)
			loss.RelationReachable = loss.CandidateKept || intersects(support.ItemIDs, reachable)
			loss.RelationExpanded = intersects(support.ItemIDs, expanded)
			loss.Selected = intersects(support.ItemIDs, selected)
			loss.Delivered = intersects(support.ItemIDs, delivered)
			accumulateAtomStages(&resultEval.summary, loss)
			loss.LostAt, loss.Reason = classifyAtomLoss(loss, trace)
			if loss.LostAt == LossStageBudgetPacking {
				resultEval.summary.BudgetDroppedAtoms++
			}
		}
		resultEval.losses = append(resultEval.losses, loss)
	}
	resultEval.summary.SupersededLeakageItems = supersededLeakageItems(replayCase.Fixture, result.Recall.LeakageDetails)
	finalizeRecallEval(&resultEval.summary, 1)
	return resultEval, nil
}

func accumulateAtomStages(summary *RecallEvalSummary, loss AtomLoss) {
	if loss.CandidateKept {
		summary.CandidateMatchedAtoms++
	}
	if loss.RelationExpanded {
		summary.RelationMatchedAtoms++
	}
	if loss.Selected {
		summary.SelectedMatchedAtoms++
	}
	if loss.Delivered {
		summary.DeliveredMatchedAtoms++
	}
}

func finalizeRecallEval(summary *RecallEvalSummary, cases int) {
	summary.CandidateRecallAtLimit = ratio(summary.CandidateMatchedAtoms, summary.AvailableAtoms)
	summary.RelationExpandedRecall = ratio(summary.RelationMatchedAtoms, summary.AvailableAtoms)
	summary.SelectedSetRecall = ratio(summary.SelectedMatchedAtoms, summary.AvailableAtoms)
	summary.DeliveredConditionalRecall = ratio(summary.DeliveredMatchedAtoms, summary.AvailableAtoms)
	if cases > 0 {
		summary.MeanContextPrecision /= float64(cases)
	}
}

func recallLaneIDs(trace teamnote.RecallTrace, includeRelation bool) map[string]struct{} {
	result := make(map[string]struct{})
	for _, candidate := range trace.CandidateTraces {
		for _, lane := range candidate.RetrievalLanes {
			if lane == teamnote.RecallLaneExactScope || lane == teamnote.RecallLaneLexical ||
				lane == teamnote.RecallLaneSemanticFallback || (includeRelation && lane == teamnote.RecallLaneRelation) {
				result[candidate.NoteID] = struct{}{}
			}
		}
	}
	return result
}

func plannedSourceIDs(items []stageeval.Item) map[string]struct{} {
	result := make(map[string]struct{})
	for _, item := range items {
		for _, noteID := range item.SourceItemIDs {
			result[noteID] = struct{}{}
		}
	}
	return result
}

func classifyAtomLoss(loss AtomLoss, trace teamnote.RecallTrace) (LossStage, string) {
	if loss.Delivered {
		return "", ""
	}
	reason := farthestSupportingRejection(loss.SupportingNoteIDs, trace)
	if loss.Selected {
		return LossStageBudgetPacking, fallbackReason(reason, "not_delivered")
	}
	if loss.RelationExpanded {
		if isBudgetReason(reason) {
			return LossStageBudgetPacking, reason
		}
		return LossStageSetSelection, reason
	}
	if loss.RelationReachable {
		return LossStageRelationExpansion, "relation_not_expanded"
	}
	if loss.CandidateKept {
		return LossStageSetSelection, reason
	}
	if !loss.CandidateKept {
		return LossStageCandidateRetrieval, reason
	}
	return LossStageCandidateRetrieval, reason
}

func farthestSupportingRejection(noteIDs []string, trace teamnote.RecallTrace) string {
	bestReason := ""
	bestPriority := -1
	for _, noteID := range noteIDs {
		for _, candidate := range trace.CandidateTraces {
			if candidate.NoteID == noteID && candidate.RejectionReason != "" {
				reason := string(candidate.RejectionReason)
				priority := rejectionStagePriority(candidate.RejectionReason)
				if priority > bestPriority {
					bestReason = reason
					bestPriority = priority
				}
			}
		}
	}
	return bestReason
}

func rejectionStagePriority(reason teamnote.RecallRejectReason) int {
	switch reason {
	case teamnote.RejectDeliveryClaim:
		return 4
	case teamnote.RejectMaxItems, teamnote.RejectTokenBudget, teamnote.RejectUncoveredRelationCost:
		return 3
	case teamnote.RejectDuplicate:
		return 2
	case teamnote.RejectRelevanceGate, teamnote.RejectEvidenceGate, teamnote.RejectTemporalGate,
		teamnote.RejectProvenanceGate, teamnote.RejectUnsafeContent:
		return 1
	default:
		return 0
	}
}

func isBudgetReason(reason string) bool {
	switch teamnote.RecallRejectReason(reason) {
	case teamnote.RejectMaxItems, teamnote.RejectTokenBudget, teamnote.RejectUncoveredRelationCost:
		return true
	default:
		return false
	}
}

func supersededLeakageItems(fixture stageeval.Fixture, details []stageeval.LeakageDetail) int {
	superseded := stringSet(fixture.SupersededEventIDs)
	leakedItems := make(map[string]struct{})
	for _, detail := range details {
		if detail.Suppressed {
			continue
		}
		if _, ok := superseded[detail.EventID]; ok {
			leakedItems[detail.ItemID] = struct{}{}
		}
	}
	return len(leakedItems)
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func intersects(values []string, set map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := set[value]; ok {
			return true
		}
	}
	return false
}

func fallbackReason(reason, fallback string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	return fallback
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
