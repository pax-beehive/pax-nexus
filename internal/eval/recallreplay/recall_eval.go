package recallreplay

import (
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

// IdentityRelationship classifies the closest eligible Knowledge Origin for
// one atom relative to its Recall Consumer.
type IdentityRelationship string

const (
	IdentitySameAgent          IdentityRelationship = "same_agent"
	IdentitySameUserCrossAgent IdentityRelationship = "same_user_cross_agent"
	IdentityCrossUserAgent     IdentityRelationship = "cross_user_agent"
	IdentityUnknown            IdentityRelationship = "unknown"
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
	AvailableAtoms             int                                         `json:"available_atoms"`
	EligibleAtoms              int                                         `json:"eligible_atoms"`
	IneligibleAtoms            int                                         `json:"ineligible_atoms"`
	CandidateMatchedAtoms      int                                         `json:"candidate_matched_atoms"`
	CandidateRecallAtLimit     float64                                     `json:"candidate_recall_at_limit"`
	RelationMatchedAtoms       int                                         `json:"relation_matched_atoms"`
	RelationExpandedRecall     float64                                     `json:"relation_expanded_recall"`
	SelectedMatchedAtoms       int                                         `json:"selected_matched_atoms"`
	SelectedSetRecall          float64                                     `json:"selected_set_recall"`
	DeliveredMatchedAtoms      int                                         `json:"delivered_matched_atoms"`
	DeliveredConditionalRecall float64                                     `json:"delivered_conditional_recall"`
	DeliveredEligibleRecall    float64                                     `json:"delivered_eligible_recall"`
	MeanContextPrecision       float64                                     `json:"mean_context_precision"`
	BudgetDroppedAtoms         int                                         `json:"budget_dropped_atoms"`
	SupersededLeakageItems     int                                         `json:"superseded_leakage_items"`
	PlannerCalls               int                                         `json:"planner_calls"`
	PlannerMeanDurationNS      float64                                     `json:"planner_mean_duration_ns"`
	PlannerP95DurationNS       int64                                       `json:"planner_p95_duration_ns"`
	IdentitySlices             map[IdentityRelationship]RecallSliceSummary `json:"identity_slices,omitempty"`
	TemporalSlices             map[string]RecallSliceSummary               `json:"temporal_slices,omitempty"`
	StrictCrossAgent           RecallSliceSummary                          `json:"strict_cross_agent"`
}

// RecallSliceSummary is one recall waterfall projected onto an evaluation
// identity or temporal slice.
type RecallSliceSummary struct {
	EligibleAtoms              int     `json:"eligible_atoms"`
	CandidateMatchedAtoms      int     `json:"candidate_matched_atoms"`
	CandidateRecallAtLimit     float64 `json:"candidate_recall_at_limit"`
	RelationMatchedAtoms       int     `json:"relation_matched_atoms"`
	RelationExpandedRecall     float64 `json:"relation_expanded_recall"`
	SelectedMatchedAtoms       int     `json:"selected_matched_atoms"`
	SelectedSetRecall          float64 `json:"selected_set_recall"`
	DeliveredMatchedAtoms      int     `json:"delivered_matched_atoms"`
	DeliveredConditionalRecall float64 `json:"delivered_conditional_recall"`
	BudgetDroppedAtoms         int     `json:"budget_dropped_atoms"`
}

// AtomLoss records the recall stage outcome for one required atom.
type AtomLoss struct {
	CaseID               string               `json:"case_id"`
	AtomID               string               `json:"atom_id"`
	SupportingNoteIDs    []string             `json:"supporting_note_ids,omitempty"`
	Consumer             Actor                `json:"consumer"`
	KnowledgeOrigins     []Actor              `json:"knowledge_origins,omitempty"`
	IdentityRelationship IdentityRelationship `json:"identity_relationship,omitempty"`
	StrictCrossAgent     bool                 `json:"strict_cross_agent"`
	ObservationTime      time.Time            `json:"observation_time"`
	QueryTime            *time.Time           `json:"query_time,omitempty"`
	SourceTimes          []time.Time          `json:"source_times,omitempty"`
	TemporalMode         string               `json:"temporal_mode"`
	QueryTimezone        string               `json:"query_timezone"`
	Available            bool                 `json:"available"`
	Eligible             bool                 `json:"eligible"`
	EligibilityReason    EligibilityReason    `json:"eligibility_reason,omitempty"`
	CandidateKept        bool                 `json:"candidate_kept"`
	RelationReachable    bool                 `json:"relation_reachable"`
	RelationExpanded     bool                 `json:"relation_expanded"`
	Selected             bool                 `json:"selected"`
	Delivered            bool                 `json:"delivered"`
	LostAt               LossStage            `json:"lost_at,omitempty"`
	Reason               string               `json:"reason,omitempty"`
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
	reachable := stringSet(trace.RelationReachableSet)
	expanded := recallLaneIDs(trace, true)
	selected := stringSet(trace.PreBudgetSelectedSet)
	delivered := plannedSourceIDs(plannedItems)
	eligibility := eligibilityByItem(replayCase.EligibilityDecisions)
	resultEval := caseRecallEval{
		summary: RecallEvalSummary{
			MeanContextPrecision: result.Recall.ContextPrecision,
			IdentitySlices:       map[IdentityRelationship]RecallSliceSummary{},
			TemporalSlices:       map[string]RecallSliceSummary{},
		},
		losses: make([]AtomLoss, 0, len(supports)),
	}
	for _, support := range supports {
		loss := AtomLoss{
			CaseID: replayCase.Fixture.CaseID, AtomID: support.AtomID,
			SupportingNoteIDs: append([]string(nil), support.ItemIDs...), Available: len(support.ItemIDs) > 0,
			Consumer: replayCase.Actor, ObservationTime: replayCase.ObservationTime,
			QueryTime: temporalQueryTime(trace.Intent, replayCase.ObservationTime), TemporalMode: string(trace.Intent.Mode),
			QueryTimezone: replayCase.QueryTimezone,
		}
		if loss.Available {
			resultEval.summary.AvailableAtoms++
			loss.CandidateKept = intersects(support.ItemIDs, direct)
			if loss.CandidateKept {
				resultEval.summary.CandidateMatchedAtoms++
			}
			loss.Eligible, loss.EligibilityReason = atomEligibility(support.ItemIDs, eligibility)
			if !loss.Eligible {
				resultEval.summary.IneligibleAtoms++
				resultEval.losses = append(resultEval.losses, loss)
				continue
			}
			resultEval.summary.EligibleAtoms++
			loss.KnowledgeOrigins, loss.IdentityRelationship = supportingKnowledgeOrigins(
				support.ItemIDs, replayCase.Candidates, replayCase.Actor,
			)
			loss.StrictCrossAgent = isStrictCrossAgent(loss.KnowledgeOrigins, replayCase.Actor)
			loss.SourceTimes = supportingSourceTimes(support.ItemIDs, replayCase.Candidates)
			loss.RelationReachable = loss.CandidateKept || intersects(support.ItemIDs, reachable)
			loss.RelationExpanded = intersects(support.ItemIDs, expanded)
			loss.Selected = intersects(support.ItemIDs, selected)
			loss.Delivered = intersects(support.ItemIDs, delivered)
			accumulateAtomStages(&resultEval.summary, loss)
			loss.LostAt, loss.Reason = classifyAtomLoss(loss, trace)
			if loss.LostAt == LossStageBudgetPacking {
				resultEval.summary.BudgetDroppedAtoms++
			}
			accumulateRecallSlice(resultEval.summary.IdentitySlices, loss.IdentityRelationship, loss)
			accumulateTemporalSlice(resultEval.summary.TemporalSlices, loss.TemporalMode, loss)
			if loss.StrictCrossAgent {
				accumulateSlice(&resultEval.summary.StrictCrossAgent, loss)
			}
		}
		resultEval.losses = append(resultEval.losses, loss)
	}
	resultEval.summary.SupersededLeakageItems = supersededLeakageItems(replayCase.Fixture, result.Recall.LeakageDetails)
	finalizeRecallEval(&resultEval.summary, 1)
	return resultEval, nil
}

func accumulateAtomStages(summary *RecallEvalSummary, loss AtomLoss) {
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
	summary.RelationExpandedRecall = ratio(summary.RelationMatchedAtoms, summary.EligibleAtoms)
	summary.SelectedSetRecall = ratio(summary.SelectedMatchedAtoms, summary.EligibleAtoms)
	summary.DeliveredConditionalRecall = ratio(summary.DeliveredMatchedAtoms, summary.AvailableAtoms)
	summary.DeliveredEligibleRecall = ratio(summary.DeliveredMatchedAtoms, summary.EligibleAtoms)
	if cases > 0 {
		summary.MeanContextPrecision /= float64(cases)
	}
	for key, slice := range summary.IdentitySlices {
		finalizeRecallSlice(&slice)
		summary.IdentitySlices[key] = slice
	}
	for key, slice := range summary.TemporalSlices {
		finalizeRecallSlice(&slice)
		summary.TemporalSlices[key] = slice
	}
	finalizeRecallSlice(&summary.StrictCrossAgent)
}

func temporalQueryTime(intent teamnote.RecallIntent, observationTime time.Time) *time.Time {
	var result *time.Time
	switch intent.Mode {
	case teamnote.RecallModeCurrent:
		value := observationTime.UTC()
		result = &value
	case teamnote.RecallModeAsOf:
		result = intent.ValidAt
	case teamnote.RecallModeChangesSince:
		result = intent.ChangedSince
	}
	return result
}

func supportingKnowledgeOrigins(itemIDs []string, candidates []Candidate, consumer Actor) ([]Actor, IdentityRelationship) {
	supporting := stringSet(itemIDs)
	origins := make([]Actor, 0, len(itemIDs))
	seen := make(map[Actor]struct{})
	for _, candidate := range candidates {
		if _, ok := supporting[candidate.ID]; !ok {
			continue
		}
		if _, duplicate := seen[candidate.Origin]; duplicate {
			continue
		}
		seen[candidate.Origin] = struct{}{}
		origins = append(origins, candidate.Origin)
	}
	return origins, closestIdentityRelationship(origins, consumer)
}

func supportingSourceTimes(itemIDs []string, candidates []Candidate) []time.Time {
	supporting := stringSet(itemIDs)
	times := make([]time.Time, 0, len(itemIDs))
	seen := make(map[time.Time]struct{})
	for _, candidate := range candidates {
		if _, ok := supporting[candidate.ID]; !ok || candidate.SourceOccurredAt.IsZero() {
			continue
		}
		value := candidate.SourceOccurredAt.UTC()
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		times = append(times, value)
	}
	return times
}

func closestIdentityRelationship(origins []Actor, consumer Actor) IdentityRelationship {
	result := IdentityUnknown
	for _, origin := range origins {
		if origin.UserID == consumer.UserID && origin.AgentID == consumer.AgentID {
			return IdentitySameAgent
		}
		if origin.UserID == consumer.UserID {
			result = IdentitySameUserCrossAgent
		} else if result == IdentityUnknown {
			result = IdentityCrossUserAgent
		}
	}
	return result
}

func isStrictCrossAgent(origins []Actor, consumer Actor) bool {
	if len(origins) == 0 {
		return false
	}
	for _, origin := range origins {
		if origin.AgentID == consumer.AgentID {
			return false
		}
	}
	return true
}

func accumulateRecallSlice(slices map[IdentityRelationship]RecallSliceSummary, key IdentityRelationship, loss AtomLoss) {
	slice := slices[key]
	slice.EligibleAtoms++
	if loss.CandidateKept {
		slice.CandidateMatchedAtoms++
	}
	if loss.RelationExpanded {
		slice.RelationMatchedAtoms++
	}
	if loss.Selected {
		slice.SelectedMatchedAtoms++
	}
	if loss.Delivered {
		slice.DeliveredMatchedAtoms++
	}
	if loss.LostAt == LossStageBudgetPacking {
		slice.BudgetDroppedAtoms++
	}
	slices[key] = slice
}

func accumulateTemporalSlice(slices map[string]RecallSliceSummary, key string, loss AtomLoss) {
	slice := slices[key]
	accumulateSlice(&slice, loss)
	slices[key] = slice
}

func accumulateSlice(slice *RecallSliceSummary, loss AtomLoss) {
	slice.EligibleAtoms++
	if loss.CandidateKept {
		slice.CandidateMatchedAtoms++
	}
	if loss.RelationExpanded {
		slice.RelationMatchedAtoms++
	}
	if loss.Selected {
		slice.SelectedMatchedAtoms++
	}
	if loss.Delivered {
		slice.DeliveredMatchedAtoms++
	}
	if loss.LostAt == LossStageBudgetPacking {
		slice.BudgetDroppedAtoms++
	}
}

func finalizeRecallSlice(slice *RecallSliceSummary) {
	slice.CandidateRecallAtLimit = ratio(slice.CandidateMatchedAtoms, slice.EligibleAtoms)
	slice.RelationExpandedRecall = ratio(slice.RelationMatchedAtoms, slice.EligibleAtoms)
	slice.SelectedSetRecall = ratio(slice.SelectedMatchedAtoms, slice.EligibleAtoms)
	slice.DeliveredConditionalRecall = ratio(slice.DeliveredMatchedAtoms, slice.EligibleAtoms)
}

func eligibilityByItem(decisions []EligibilityDecision) map[string]EligibilityDecision {
	result := make(map[string]EligibilityDecision, len(decisions))
	for _, decision := range decisions {
		result[decision.ItemID] = decision
	}
	return result
}

func atomEligibility(itemIDs []string, decisions map[string]EligibilityDecision) (bool, EligibilityReason) {
	reason := EligibilityReason("")
	for _, itemID := range itemIDs {
		decision := decisions[itemID]
		if decision.Eligible {
			return true, EligibilityEligible
		}
		if reason == "" {
			reason = decision.Reason
			continue
		}
		if reason != decision.Reason {
			reason = EligibilityMixed
		}
	}
	return false, reason
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
		return LossStageRelationExpansion, relationLossReason(loss.SupportingNoteIDs, trace)
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
	supporting := stringSet(noteIDs)
	for _, rejection := range trace.Rejections {
		if _, ok := supporting[rejection.NoteID]; !ok {
			continue
		}
		priority := rejectionStagePriority(rejection.Reason)
		if priority > bestPriority {
			bestReason = string(rejection.Reason)
			bestPriority = priority
		}
	}
	return bestReason
}

func relationLossReason(noteIDs []string, trace teamnote.RecallTrace) string {
	supporting := stringSet(noteIDs)
	for _, rejection := range trace.Rejections {
		if _, ok := supporting[rejection.NoteID]; ok && rejection.Reason == teamnote.RejectRelationRelevanceGate {
			return string(rejection.Reason)
		}
	}
	return "relation_not_expanded"
}

func rejectionStagePriority(reason teamnote.RecallRejectReason) int {
	switch reason {
	case teamnote.RejectDeliveryClaim:
		return 4
	case teamnote.RejectMaxItems, teamnote.RejectTokenBudget, teamnote.RejectUncoveredRelationCost:
		return 3
	case teamnote.RejectDuplicate:
		return 2
	case teamnote.RejectRelationRelevanceGate:
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
