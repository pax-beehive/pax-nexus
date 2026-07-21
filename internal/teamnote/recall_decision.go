package teamnote

// SummarizeRecallDecision reduces the detailed PlanRecall trace to the stable
// decision consumed by an outer recall router.
func SummarizeRecallDecision(trace RecallTrace) RecallDecisionSummary {
	reasons := make([]RecallReasonCode, 0, 4)
	selected := recallIDSet(trace.SelectedSet)
	if len(trace.DeliveredItems) == 0 {
		reasons = append(reasons, RecallReasonNoEvidence)
	}
	covered := make(map[string]struct{}, len(trace.Intent.RequestedFacts))
	confidencePassed := len(selected) > 0
	hardGatesPassed := len(selected) > 0
	for _, candidate := range trace.CandidateTraces {
		if _, ok := selected[candidate.NoteID]; !ok {
			continue
		}
		for _, fact := range candidate.CoveredFacts {
			covered[fact] = struct{}{}
		}
		if candidate.EvidenceConfidence < trace.EvidenceThreshold {
			confidencePassed = false
		}
		for _, gate := range candidate.HardGateResults {
			if !gate.Passed {
				hardGatesPassed = false
			}
		}
	}
	if !coversRequiredFacts(covered, trace.Intent.RequestedFacts) {
		reasons = append(reasons, RecallReasonFactCoverage)
	}
	if !confidencePassed {
		reasons = append(reasons, RecallReasonConfidence)
	}
	if hasAnswerBearingBudgetDrop(trace) {
		reasons = append(reasons, RecallReasonBudgetDrop)
	}
	if !hardGatesPassed {
		reasons = append(reasons, RecallReasonHardGate)
	}
	return RecallDecisionSummary{EvidenceSufficient: len(reasons) == 0, ReasonCodes: reasons}
}

func recallIDSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func coversRequiredFacts(covered map[string]struct{}, required []string) bool {
	for _, fact := range required {
		if _, ok := covered[fact]; !ok {
			return false
		}
	}
	return true
}

func hasAnswerBearingBudgetDrop(trace RecallTrace) bool {
	dropped := recallIDSet(nil)
	for _, rejection := range trace.BudgetDrops {
		dropped[rejection.NoteID] = struct{}{}
	}
	for _, candidate := range trace.CandidateTraces {
		if _, ok := dropped[candidate.NoteID]; ok && len(candidate.CoveredFacts) > 0 {
			return true
		}
	}
	return false
}
