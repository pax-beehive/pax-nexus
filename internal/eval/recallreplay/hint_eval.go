package recallreplay

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const hintCalibrationBuckets = 10

// ErrIncompleteHintIntervention means the one-call miss lacks the second call
// needed to score the two-call budget.
var ErrIncompleteHintIntervention = errors.New("incomplete hint intervention")

// HintEvalSummary scores captured hint opportunities. It deliberately does
// not infer agent exposure or activation beyond the captured hint decision.
type HintEvalSummary struct {
	EvidenceScored             bool                                      `json:"evidence_scored"`
	EligibleEvidenceCandidates int                                       `json:"eligible_evidence_candidates"`
	EvidenceScoredItems        int                                       `json:"evidence_scored_items"`
	EvidenceScoreCoverage      float64                                   `json:"evidence_score_coverage"`
	PositiveEvidenceItems      int                                       `json:"positive_evidence_items"`
	AdmittedEvidenceItems      int                                       `json:"admitted_evidence_items"`
	TruePositiveEvidenceItems  int                                       `json:"true_positive_evidence_items"`
	EvidencePrecision          float64                                   `json:"evidence_precision"`
	EvidenceRecall             float64                                   `json:"evidence_recall"`
	EvidenceBrierScore         float64                                   `json:"evidence_brier_score"`
	EvidenceCalibrationError   float64                                   `json:"evidence_calibration_error"`
	UnauthorizedEvidenceScores int                                       `json:"unauthorized_evidence_scores"`
	HintScored                 bool                                      `json:"hint_scored"`
	ScoredOpportunities        int                                       `json:"scored_opportunities"`
	PositiveOpportunities      int                                       `json:"positive_opportunities"`
	ExposedHints               int                                       `json:"exposed_hints"`
	TruePositiveHints          int                                       `json:"true_positive_hints"`
	HintPrecision              float64                                   `json:"hint_precision"`
	HintRecall                 float64                                   `json:"hint_recall"`
	HintBrierScore             float64                                   `json:"hint_brier_score"`
	HintCalibrationError       float64                                   `json:"hint_calibration_error"`
	SuccessAtOneCall           int                                       `json:"success_at_one_call"`
	SuccessAtOneCallRate       float64                                   `json:"success_at_one_call_rate"`
	SuccessAtTwoCalls          int                                       `json:"success_at_two_calls"`
	SuccessAtTwoCallsRate      float64                                   `json:"success_at_two_calls_rate"`
	NewEligibleAtoms           int                                       `json:"new_eligible_atoms"`
	UnauthorizedLeakageItems   int                                       `json:"unauthorized_leakage_items"`
	UnauthorizedLeadInfluence  int                                       `json:"unauthorized_lead_influence"`
	WrongTimeLeakageItems      int                                       `json:"wrong_time_leakage_items"`
	WrongTimeLeadInfluence     int                                       `json:"wrong_time_lead_influence"`
	FutureLeakageItems         int                                       `json:"future_leakage_items"`
	FutureLeadInfluence        int                                       `json:"future_lead_influence"`
	ForbiddenLeakageItems      int                                       `json:"forbidden_leakage_items"`
	SupersededLeakageItems     int                                       `json:"superseded_leakage_items"`
	ProvenanceErrors           int                                       `json:"provenance_errors"`
	IdentityErrors             int                                       `json:"identity_errors"`
	TemporalPreservationErrors int                                       `json:"temporal_preservation_errors"`
	DeliveryClaimViolations    int                                       `json:"delivery_claim_violations"`
	DedupScored                bool                                      `json:"dedup_scored"`
	DuplicateOpportunities     int                                       `json:"duplicate_opportunities"`
	DuplicateDrops             int                                       `json:"duplicate_drops"`
	DedupErrors                int                                       `json:"dedup_errors"`
	FocusedRecallCalls         int                                       `json:"focused_recall_calls"`
	FocusedRecallTokens        int                                       `json:"focused_recall_tokens"`
	FocusedRecallCostUSD       float64                                   `json:"focused_recall_cost_usd"`
	FocusedRecallDurationNS    int64                                     `json:"focused_recall_duration_ns"`
	IdentitySlices             map[IdentityRelationship]HintSliceSummary `json:"identity_slices,omitempty"`
	TemporalSlices             map[string]HintSliceSummary               `json:"temporal_slices,omitempty"`
	StrictCrossAgent           HintSliceSummary                          `json:"strict_cross_agent"`

	brierSum               float64
	bucketCount            [hintCalibrationBuckets]int
	bucketScoreSum         [hintCalibrationBuckets]float64
	bucketPositive         [hintCalibrationBuckets]int
	evidenceBrierSum       float64
	evidenceBucketCount    [hintCalibrationBuckets]int
	evidenceBucketScoreSum [hintCalibrationBuckets]float64
	evidenceBucketPositive [hintCalibrationBuckets]int
}

// HintSliceSummary projects opportunity safety and utility onto one identity
// relationship or temporal mode.
type HintSliceSummary struct {
	ScoredOpportunities   int     `json:"scored_opportunities"`
	PositiveOpportunities int     `json:"positive_opportunities"`
	ExposedHints          int     `json:"exposed_hints"`
	TruePositiveHints     int     `json:"true_positive_hints"`
	HintPrecision         float64 `json:"hint_precision"`
	HintRecall            float64 `json:"hint_recall"`
	SuccessAtOneCall      int     `json:"success_at_one_call"`
	SuccessAtTwoCalls     int     `json:"success_at_two_calls"`
	NewEligibleAtoms      int     `json:"new_eligible_atoms"`
	SafetyViolations      int     `json:"safety_violations"`
}

// HintCaseEval is the auditable result for one captured intervention.
type HintCaseEval struct {
	Positive                   bool `json:"positive"`
	SuccessAtOneCall           bool `json:"success_at_one_call"`
	SuccessAtTwoCalls          bool `json:"success_at_two_calls"`
	NewEligibleAtoms           int  `json:"new_eligible_atoms"`
	UnauthorizedLeakageItems   int  `json:"unauthorized_leakage_items"`
	UnauthorizedLeadInfluence  int  `json:"unauthorized_lead_influence"`
	WrongTimeLeakageItems      int  `json:"wrong_time_leakage_items"`
	WrongTimeLeadInfluence     int  `json:"wrong_time_lead_influence"`
	FutureLeakageItems         int  `json:"future_leakage_items"`
	FutureLeadInfluence        int  `json:"future_lead_influence"`
	ForbiddenLeakageItems      int  `json:"forbidden_leakage_items"`
	SupersededLeakageItems     int  `json:"superseded_leakage_items"`
	ProvenanceErrors           int  `json:"provenance_errors"`
	IdentityErrors             int  `json:"identity_errors"`
	TemporalPreservationErrors int  `json:"temporal_preservation_errors"`
	DeliveryClaimViolations    int  `json:"delivery_claim_violations"`
}

func evaluateHintObservation(
	replayCase Case,
	passiveTrace teamnote.RecallTrace,
	passiveItems []stageeval.Item,
) (HintCaseEval, HintEvalSummary, error) {
	observation := replayCase.HintObservation
	if observation == nil {
		return HintCaseEval{}, HintEvalSummary{}, nil
	}
	if observation.DuplicateDropped {
		return HintCaseEval{}, HintEvalSummary{}, nil
	}
	baseline, err := evaluateEligibleHintItems(replayCase, passiveItems)
	if err != nil {
		return HintCaseEval{}, HintEvalSummary{}, fmt.Errorf("score passive eligible evidence: %w", err)
	}
	caseEval := evaluateHintContracts(replayCase, passiveTrace, *observation)
	evidenceSummary, evidenceErr := evaluateEvidenceScores(replayCase, observation.EvidenceScores)
	if evidenceErr != nil {
		return HintCaseEval{}, HintEvalSummary{}, evidenceErr
	}
	if err := evaluateHintCalls(replayCase, passiveTrace, passiveItems, baseline.Recall.MatchedAvailableAtoms, *observation, &caseEval); err != nil {
		return HintCaseEval{}, HintEvalSummary{}, err
	}
	caseEval.SuccessAtTwoCalls = caseEval.NewEligibleAtoms > 0 && hintCaseSafe(caseEval)
	caseEval.Positive = caseEval.SuccessAtTwoCalls
	summary := summarizeHintObservation(replayCase, passiveTrace, *observation, caseEval, evidenceSummary)
	return caseEval, summary, nil
}

func evaluateHintCalls(
	replayCase Case,
	passiveTrace teamnote.RecallTrace,
	passiveItems []stageeval.Item,
	baselineMatched int,
	observation HintObservation,
	caseEval *HintCaseEval,
) error {
	combined := append([]stageeval.Item(nil), passiveItems...)
	for callIndex, call := range observation.Calls {
		caseEval.ProvenanceErrors += activeItemProvenanceErrors(replayCase, call)
		caseEval.UnauthorizedLeakageItems += activeEligibilityLeakage(replayCase, call.Items, EligibilityAuthorization)
		capturedWrongTime := activeEligibilityLeakage(replayCase, call.Items, EligibilityTemporal)
		capturedFuture := activeEligibilityLeakage(replayCase, call.Items, EligibilityFuture)
		wrongTime, future := activeCandidateTemporalLeakage(replayCase, passiveTrace, call.Items)
		caseEval.WrongTimeLeakageItems += max(capturedWrongTime, wrongTime)
		caseEval.FutureLeakageItems += max(capturedFuture, future)
		forbidden, superseded, leakageErr := hintLeakage(replayCase, call.Items)
		if leakageErr != nil {
			return leakageErr
		}
		caseEval.ForbiddenLeakageItems += forbidden
		caseEval.SupersededLeakageItems += superseded
		combined = append(combined, eligibleRecallItems(replayCase, call.Items)...)
		scored, scoreErr := evaluateEligibleHintItems(replayCase, combined)
		if scoreErr != nil {
			return fmt.Errorf("score focused recall call %d: %w", callIndex+1, scoreErr)
		}
		gain := scored.Recall.MatchedAvailableAtoms - baselineMatched
		if callIndex == 0 {
			caseEval.SuccessAtOneCall = gain > 0 && hintCaseSafe(*caseEval)
		}
		if gain > caseEval.NewEligibleAtoms {
			caseEval.NewEligibleAtoms = gain
		}
	}
	if !caseEval.SuccessAtOneCall && len(observation.Calls) < 2 {
		return fmt.Errorf("%w: second focused recall call required after first-call miss", ErrIncompleteHintIntervention)
	}
	return nil
}

func summarizeHintObservation(
	replayCase Case,
	passiveTrace teamnote.RecallTrace,
	observation HintObservation,
	caseEval HintCaseEval,
	evidenceSummary HintEvalSummary,
) HintEvalSummary {
	summary := summarizeHintCase(observation, caseEval)
	origins, identity := supportingKnowledgeOrigins(observation.LeadNoteIDs, replayCase.Candidates, replayCase.Actor)
	summary.IdentitySlices = map[IdentityRelationship]HintSliceSummary{
		identity: hintSliceFromCase(observation, caseEval),
	}
	summary.TemporalSlices = map[string]HintSliceSummary{
		string(passiveTrace.Intent.Mode): hintSliceFromCase(observation, caseEval),
	}
	if isStrictCrossAgent(origins, replayCase.Actor) {
		summary.StrictCrossAgent = hintSliceFromCase(observation, caseEval)
	}
	mergeEvidenceScoreSummary(&summary, evidenceSummary)
	finalizeHintEval(&summary)
	return summary
}

func evaluateHintContracts(replayCase Case, passiveTrace teamnote.RecallTrace, observation HintObservation) HintCaseEval {
	result := HintCaseEval{}
	if observation.Consumer != replayCase.Actor {
		result.IdentityErrors++
	}
	if !observation.ObservationTime.Equal(replayCase.ObservationTime) ||
		observation.TemporalMode != string(passiveTrace.Intent.Mode) ||
		observation.QueryTimezone != replayCase.QueryTimezone ||
		!equalOptionalTime(observation.QueryTime, temporalQueryTime(passiveTrace.Intent, replayCase.ObservationTime)) {
		result.TemporalPreservationErrors++
	}
	focusedRequest := replayCase.recallRequest()
	focusedRequest.Query = observation.FocusedQuery
	_, focusedTrace := teamnote.PlanRecall(nil, focusedRequest, teamnote.RecallPolicy{ObservationTime: replayCase.ObservationTime})
	if focusedTrace.Intent.Mode != passiveTrace.Intent.Mode ||
		!equalOptionalTime(temporalQueryTime(focusedTrace.Intent, replayCase.ObservationTime), temporalQueryTime(passiveTrace.Intent, replayCase.ObservationTime)) ||
		!requestedFactsPreserved(focusedTrace.Intent.RequestedFacts, passiveTrace.Intent.RequestedFacts) {
		result.TemporalPreservationErrors++
	}
	leadContracts := evaluateHintLeadContracts(replayCase, passiveTrace, observation.LeadNoteIDs)
	result.ProvenanceErrors += leadContracts.ProvenanceErrors
	result.UnauthorizedLeadInfluence += leadContracts.UnauthorizedLeadInfluence
	result.WrongTimeLeadInfluence += leadContracts.WrongTimeLeadInfluence
	result.FutureLeadInfluence += leadContracts.FutureLeadInfluence
	claimed := stringSet(observation.ClaimedNoteIDs)
	for _, noteID := range observation.LeadNoteIDs {
		if _, ok := claimed[noteID]; ok {
			result.DeliveryClaimViolations++
		}
	}
	return result
}

func evaluateHintLeadContracts(replayCase Case, trace teamnote.RecallTrace, leadIDs []string) HintCaseEval {
	result := HintCaseEval{}
	candidates := make(map[string]Candidate, len(replayCase.Candidates))
	for _, candidate := range replayCase.Candidates {
		candidates[candidate.ID] = candidate
	}
	decisions := eligibilityByItem(replayCase.EligibilityDecisions)
	failedTemporal := make(map[string]struct{})
	for _, candidateTrace := range trace.CandidateTraces {
		if !candidateTrace.TemporalResolution.GatePassed {
			failedTemporal[candidateTrace.NoteID] = struct{}{}
		}
	}
	queryTime := temporalQueryTime(trace.Intent, replayCase.ObservationTime)
	for _, noteID := range leadIDs {
		candidate, ok := candidates[noteID]
		if !ok {
			result.ProvenanceErrors++
			continue
		}
		if adapterExcludesCandidate(decisions[noteID]) {
			result.UnauthorizedLeadInfluence++
		}
		if _, rejected := failedTemporal[noteID]; !rejected {
			continue
		}
		if queryTime != nil && candidate.ValidAt != nil && candidate.ValidAt.After(*queryTime) {
			result.FutureLeadInfluence++
		} else {
			result.WrongTimeLeadInfluence++
		}
	}
	return result
}

func evaluateEvidenceScores(replayCase Case, scores []EvidenceScoreObservation) (HintEvalSummary, error) {
	result := HintEvalSummary{}
	decisions := eligibilityByItem(replayCase.EligibilityDecisions)
	for _, candidate := range replayCase.Candidates {
		if decisions[candidate.ID].Eligible {
			result.EligibleEvidenceCandidates++
		}
	}
	supportedCandidates := positiveEvidenceCandidateIDs(replayCase.AtomSupports, decisions)
	extractionByID := make(map[string]stageeval.Item, len(replayCase.ExtractionItems))
	for _, item := range replayCase.ExtractionItems {
		extractionByID[item.ID] = item
	}
	positiveCandidates, err := evidenceCandidateLabels(replayCase, extractionByID, supportedCandidates)
	if err != nil {
		return HintEvalSummary{}, err
	}
	for _, positive := range positiveCandidates {
		if positive {
			result.PositiveEvidenceItems++
		}
	}
	for _, scored := range scores {
		decision := decisions[scored.ItemID]
		if !decision.Eligible {
			countUnauthorizedEvidenceScore(&result, decision)
			continue
		}
		positive := positiveCandidates[scored.ItemID]
		recordEvidenceScore(&result, scored, positive)
	}
	return result, nil
}

func evidenceCandidateLabels(
	replayCase Case,
	extractionByID map[string]stageeval.Item,
	supportedCandidates map[string]struct{},
) (map[string]bool, error) {
	result := make(map[string]bool, len(replayCase.Candidates))
	for _, candidate := range replayCase.Candidates {
		positive, err := evidenceScoreLabel(replayCase, extractionByID[candidate.ID], supportedCandidates)
		if err != nil {
			return nil, fmt.Errorf("label evidence candidate %q: %w", candidate.ID, err)
		}
		result[candidate.ID] = positive
	}
	return result, nil
}

func positiveEvidenceCandidateIDs(
	supports []stageeval.AtomSupport,
	decisions map[string]EligibilityDecision,
) map[string]struct{} {
	result := make(map[string]struct{})
	for _, support := range supports {
		for _, itemID := range support.ItemIDs {
			if decisions[itemID].Eligible {
				result[itemID] = struct{}{}
			}
		}
	}
	return result
}

func countUnauthorizedEvidenceScore(summary *HintEvalSummary, decision EligibilityDecision) {
	if decision.Reason == EligibilityAuthorization {
		summary.UnauthorizedEvidenceScores++
	}
}

func evidenceScoreLabel(
	replayCase Case,
	item stageeval.Item,
	positiveCandidates map[string]struct{},
) (bool, error) {
	recallItem := stageeval.Item{
		ID: "evidence-score-" + item.ID, SourceItemIDs: []string{item.ID},
		Text: item.Text, EvidenceEventIDs: item.EvidenceEventIDs,
	}
	forbidden, superseded, err := hintLeakage(replayCase, []stageeval.Item{recallItem})
	if err != nil {
		return false, err
	}
	_, supported := positiveCandidates[item.ID]
	return supported && forbidden == 0 && superseded == 0, nil
}

func recordEvidenceScore(summary *HintEvalSummary, scored EvidenceScoreObservation, positive bool) {
	summary.EvidenceScoredItems++
	if scored.Admitted {
		summary.AdmittedEvidenceItems++
		if positive {
			summary.TruePositiveEvidenceItems++
		}
	}
	label := 0.0
	if positive {
		label = 1
	}
	delta := scored.Score - label
	summary.evidenceBrierSum += delta * delta
	bucket := calibrationBucket(scored.Score)
	summary.evidenceBucketCount[bucket]++
	summary.evidenceBucketScoreSum[bucket] += scored.Score
	summary.evidenceBucketPositive[bucket] += int(label)
}

func calibrationBucket(score float64) int {
	bucket := int(score * hintCalibrationBuckets)
	if bucket == hintCalibrationBuckets {
		return bucket - 1
	}
	return bucket
}

func summarizeHintCase(observation HintObservation, result HintCaseEval) HintEvalSummary {
	summary := HintEvalSummary{ScoredOpportunities: 1, NewEligibleAtoms: result.NewEligibleAtoms}
	if result.Positive {
		summary.PositiveOpportunities = 1
	}
	if observation.Exposed {
		summary.ExposedHints = 1
		if result.Positive {
			summary.TruePositiveHints = 1
		}
	}
	if result.SuccessAtOneCall {
		summary.SuccessAtOneCall = 1
	}
	if result.SuccessAtTwoCalls {
		summary.SuccessAtTwoCalls = 1
	}
	summary.UnauthorizedLeakageItems = result.UnauthorizedLeakageItems
	summary.UnauthorizedLeadInfluence = result.UnauthorizedLeadInfluence
	summary.WrongTimeLeakageItems = result.WrongTimeLeakageItems
	summary.WrongTimeLeadInfluence = result.WrongTimeLeadInfluence
	summary.FutureLeakageItems = result.FutureLeakageItems
	summary.FutureLeadInfluence = result.FutureLeadInfluence
	summary.ForbiddenLeakageItems = result.ForbiddenLeakageItems
	summary.SupersededLeakageItems = result.SupersededLeakageItems
	summary.ProvenanceErrors = result.ProvenanceErrors
	summary.IdentityErrors = result.IdentityErrors
	summary.TemporalPreservationErrors = result.TemporalPreservationErrors
	summary.DeliveryClaimViolations = result.DeliveryClaimViolations
	for _, call := range observation.Calls {
		summary.FocusedRecallCalls++
		summary.FocusedRecallTokens += call.Tokens
		summary.FocusedRecallCostUSD += call.CostUSD
		summary.FocusedRecallDurationNS += call.DurationNS
	}
	label := 0.0
	if result.Positive {
		label = 1
	}
	score := *observation.Score
	delta := score - label
	summary.brierSum = delta * delta
	bucket := calibrationBucket(score)
	summary.bucketCount[bucket] = 1
	summary.bucketScoreSum[bucket] = score
	summary.bucketPositive[bucket] = int(label)
	finalizeHintEval(&summary)
	return summary
}

func mergeHintEval(target *HintEvalSummary, current HintEvalSummary) {
	mergeEvidenceScoreSummary(target, current)
	target.ScoredOpportunities += current.ScoredOpportunities
	target.PositiveOpportunities += current.PositiveOpportunities
	target.ExposedHints += current.ExposedHints
	target.TruePositiveHints += current.TruePositiveHints
	target.SuccessAtOneCall += current.SuccessAtOneCall
	target.SuccessAtTwoCalls += current.SuccessAtTwoCalls
	target.NewEligibleAtoms += current.NewEligibleAtoms
	target.UnauthorizedLeakageItems += current.UnauthorizedLeakageItems
	target.UnauthorizedLeadInfluence += current.UnauthorizedLeadInfluence
	target.WrongTimeLeakageItems += current.WrongTimeLeakageItems
	target.WrongTimeLeadInfluence += current.WrongTimeLeadInfluence
	target.FutureLeakageItems += current.FutureLeakageItems
	target.FutureLeadInfluence += current.FutureLeadInfluence
	target.ForbiddenLeakageItems += current.ForbiddenLeakageItems
	target.SupersededLeakageItems += current.SupersededLeakageItems
	target.ProvenanceErrors += current.ProvenanceErrors
	target.IdentityErrors += current.IdentityErrors
	target.TemporalPreservationErrors += current.TemporalPreservationErrors
	target.DeliveryClaimViolations += current.DeliveryClaimViolations
	target.FocusedRecallCalls += current.FocusedRecallCalls
	target.FocusedRecallTokens += current.FocusedRecallTokens
	target.FocusedRecallCostUSD += current.FocusedRecallCostUSD
	target.FocusedRecallDurationNS += current.FocusedRecallDurationNS
	target.brierSum += current.brierSum
	for index := 0; index < hintCalibrationBuckets; index++ {
		target.bucketCount[index] += current.bucketCount[index]
		target.bucketScoreSum[index] += current.bucketScoreSum[index]
		target.bucketPositive[index] += current.bucketPositive[index]
	}
	if target.IdentitySlices == nil {
		target.IdentitySlices = map[IdentityRelationship]HintSliceSummary{}
	}
	for key, currentSlice := range current.IdentitySlices {
		targetSlice := target.IdentitySlices[key]
		mergeHintSlice(&targetSlice, currentSlice)
		target.IdentitySlices[key] = targetSlice
	}
	if target.TemporalSlices == nil {
		target.TemporalSlices = map[string]HintSliceSummary{}
	}
	for key, currentSlice := range current.TemporalSlices {
		targetSlice := target.TemporalSlices[key]
		mergeHintSlice(&targetSlice, currentSlice)
		target.TemporalSlices[key] = targetSlice
	}
	mergeHintSlice(&target.StrictCrossAgent, current.StrictCrossAgent)
}

func mergeEvidenceScoreSummary(target *HintEvalSummary, current HintEvalSummary) {
	target.EligibleEvidenceCandidates += current.EligibleEvidenceCandidates
	target.EvidenceScoredItems += current.EvidenceScoredItems
	target.PositiveEvidenceItems += current.PositiveEvidenceItems
	target.AdmittedEvidenceItems += current.AdmittedEvidenceItems
	target.TruePositiveEvidenceItems += current.TruePositiveEvidenceItems
	target.UnauthorizedEvidenceScores += current.UnauthorizedEvidenceScores
	target.evidenceBrierSum += current.evidenceBrierSum
	for index := 0; index < hintCalibrationBuckets; index++ {
		target.evidenceBucketCount[index] += current.evidenceBucketCount[index]
		target.evidenceBucketScoreSum[index] += current.evidenceBucketScoreSum[index]
		target.evidenceBucketPositive[index] += current.evidenceBucketPositive[index]
	}
}

func finalizeHintEval(summary *HintEvalSummary) {
	summary.EvidenceScored = summary.EvidenceScoredItems > 0
	summary.HintScored = summary.ScoredOpportunities > 0
	summary.EvidenceScoreCoverage = ratio(summary.EvidenceScoredItems, summary.EligibleEvidenceCandidates)
	summary.EvidencePrecision = ratio(summary.TruePositiveEvidenceItems, summary.AdmittedEvidenceItems)
	summary.EvidenceRecall = ratio(summary.TruePositiveEvidenceItems, summary.PositiveEvidenceItems)
	if summary.EvidenceScoredItems > 0 {
		summary.EvidenceBrierScore = summary.evidenceBrierSum / float64(summary.EvidenceScoredItems)
	}
	summary.EvidenceCalibrationError = calibrationError(
		summary.EvidenceScoredItems, summary.evidenceBucketCount,
		summary.evidenceBucketScoreSum, summary.evidenceBucketPositive,
	)
	summary.HintPrecision = ratio(summary.TruePositiveHints, summary.ExposedHints)
	summary.HintRecall = ratio(summary.TruePositiveHints, summary.PositiveOpportunities)
	summary.SuccessAtOneCallRate = ratio(summary.SuccessAtOneCall, summary.ScoredOpportunities)
	summary.SuccessAtTwoCallsRate = ratio(summary.SuccessAtTwoCalls, summary.ScoredOpportunities)
	if summary.ScoredOpportunities > 0 {
		summary.HintBrierScore = summary.brierSum / float64(summary.ScoredOpportunities)
	}
	summary.HintCalibrationError = calibrationError(
		summary.ScoredOpportunities, summary.bucketCount, summary.bucketScoreSum, summary.bucketPositive,
	)
	for key, slice := range summary.IdentitySlices {
		finalizeHintSlice(&slice)
		summary.IdentitySlices[key] = slice
	}
	for key, slice := range summary.TemporalSlices {
		finalizeHintSlice(&slice)
		summary.TemporalSlices[key] = slice
	}
	finalizeHintSlice(&summary.StrictCrossAgent)
}

func evaluateHintDedup(cases []Case, summary *HintEvalSummary) {
	type dedupKey struct {
		userID      string
		agentID     string
		sessionID   string
		fingerprint string
	}
	seen := make(map[dedupKey]struct{})
	for _, replayCase := range cases {
		observation := replayCase.HintObservation
		if observation == nil || observation.LeadFingerprint == "" {
			continue
		}
		key := dedupKey{userID: observation.Consumer.UserID, agentID: observation.Consumer.AgentID,
			sessionID: observation.Consumer.SessionID, fingerprint: observation.LeadFingerprint}
		if _, duplicate := seen[key]; !duplicate {
			seen[key] = struct{}{}
			if observation.DuplicateDropped {
				summary.DedupErrors++
			}
			continue
		}
		summary.DedupScored = true
		summary.DuplicateOpportunities++
		if observation.DuplicateDropped && !observation.Exposed {
			summary.DuplicateDrops++
		} else {
			summary.DedupErrors++
		}
	}
}

func hintSliceFromCase(observation HintObservation, result HintCaseEval) HintSliceSummary {
	slice := HintSliceSummary{ScoredOpportunities: 1, NewEligibleAtoms: result.NewEligibleAtoms}
	if result.Positive {
		slice.PositiveOpportunities++
	}
	if observation.Exposed {
		slice.ExposedHints++
		if result.Positive {
			slice.TruePositiveHints++
		}
	}
	if result.SuccessAtOneCall {
		slice.SuccessAtOneCall++
	}
	if result.SuccessAtTwoCalls {
		slice.SuccessAtTwoCalls++
	}
	if !hintCaseSafe(result) {
		slice.SafetyViolations++
	}
	return slice
}

func mergeHintSlice(target *HintSliceSummary, current HintSliceSummary) {
	target.ScoredOpportunities += current.ScoredOpportunities
	target.PositiveOpportunities += current.PositiveOpportunities
	target.ExposedHints += current.ExposedHints
	target.TruePositiveHints += current.TruePositiveHints
	target.SuccessAtOneCall += current.SuccessAtOneCall
	target.SuccessAtTwoCalls += current.SuccessAtTwoCalls
	target.NewEligibleAtoms += current.NewEligibleAtoms
	target.SafetyViolations += current.SafetyViolations
}

func finalizeHintSlice(slice *HintSliceSummary) {
	slice.HintPrecision = ratio(slice.TruePositiveHints, slice.ExposedHints)
	slice.HintRecall = ratio(slice.TruePositiveHints, slice.PositiveOpportunities)
}

func calibrationError(
	total int,
	counts [hintCalibrationBuckets]int,
	scoreSums [hintCalibrationBuckets]float64,
	positives [hintCalibrationBuckets]int,
) float64 {
	if total == 0 {
		return 0
	}
	var result float64
	for index, count := range counts {
		if count == 0 {
			continue
		}
		meanScore := scoreSums[index] / float64(count)
		positiveRate := float64(positives[index]) / float64(count)
		result += math.Abs(meanScore-positiveRate) * float64(count) / float64(total)
	}
	return result
}

func evaluateEligibleHintItems(replayCase Case, items []stageeval.Item) (stageeval.Result, error) {
	eligibleExtraction := eligibleExtractionItems(replayCase)
	extraction := stageeval.Observation{
		CaseID: replayCase.Fixture.CaseID, Stage: stageeval.StageExtraction,
		SourceRevision: replayCase.Fixture.SourceRevision, Items: eligibleExtraction,
	}
	context := replayCase.Fixture.RecallContext
	recall := stageeval.Observation{
		CaseID: replayCase.Fixture.CaseID, Stage: stageeval.StageRecall,
		SourceRevision: replayCase.Fixture.SourceRevision, RecallContext: &context,
		Items: eligibleRecallItems(replayCase, items),
	}
	return stageeval.Evaluate(replayCase.Fixture, extraction, recall)
}

func eligibleExtractionItems(replayCase Case) []stageeval.Item {
	decisions := eligibilityByItem(replayCase.EligibilityDecisions)
	items := make([]stageeval.Item, 0, len(replayCase.ExtractionItems))
	for _, item := range replayCase.ExtractionItems {
		if decisions[item.ID].Eligible {
			items = append(items, item)
		}
	}
	return items
}

func eligibleRecallItems(replayCase Case, items []stageeval.Item) []stageeval.Item {
	decisions := eligibilityByItem(replayCase.EligibilityDecisions)
	result := make([]stageeval.Item, 0, len(items))
	for _, item := range items {
		if len(item.SourceItemIDs) == 0 {
			continue
		}
		eligible := true
		for _, sourceID := range item.SourceItemIDs {
			decision, ok := decisions[sourceID]
			if !ok || !decision.Eligible {
				eligible = false
				break
			}
		}
		if eligible {
			result = append(result, item)
		}
	}
	return result
}

func hintLeakage(replayCase Case, items []stageeval.Item) (int, int, error) {
	if len(items) == 0 {
		return 0, 0, nil
	}
	extraction := stageeval.Observation{
		CaseID: replayCase.Fixture.CaseID, Stage: stageeval.StageExtraction,
		SourceRevision: replayCase.Fixture.SourceRevision, Items: replayCase.ExtractionItems,
	}
	context := replayCase.Fixture.RecallContext
	recall := stageeval.Observation{
		CaseID: replayCase.Fixture.CaseID, Stage: stageeval.StageRecall,
		SourceRevision: replayCase.Fixture.SourceRevision, RecallContext: &context,
		Items: knownRecallItems(replayCase, items),
	}
	result, err := stageeval.Evaluate(replayCase.Fixture, extraction, recall)
	if err != nil {
		return 0, 0, fmt.Errorf("score hint leakage: %w", err)
	}
	supersededEvents := stringSet(replayCase.Fixture.SupersededEventIDs)
	forbiddenItems := make(map[string]struct{})
	supersededItems := make(map[string]struct{})
	for _, detail := range result.Recall.LeakageDetails {
		if detail.Suppressed {
			continue
		}
		if _, ok := supersededEvents[detail.EventID]; ok {
			supersededItems[detail.ItemID] = struct{}{}
		} else {
			forbiddenItems[detail.ItemID] = struct{}{}
		}
	}
	return len(forbiddenItems), len(supersededItems), nil
}

func knownRecallItems(replayCase Case, items []stageeval.Item) []stageeval.Item {
	known := make(map[string]struct{}, len(replayCase.ExtractionItems))
	for _, item := range replayCase.ExtractionItems {
		known[item.ID] = struct{}{}
	}
	result := make([]stageeval.Item, 0, len(items))
	for _, item := range items {
		if len(item.SourceItemIDs) == 0 {
			continue
		}
		valid := true
		for _, sourceID := range item.SourceItemIDs {
			if _, ok := known[sourceID]; !ok {
				valid = false
				break
			}
		}
		if valid {
			result = append(result, item)
		}
	}
	return result
}

func activeItemProvenanceErrors(replayCase Case, call HintRecallCall) int {
	known := make(map[string]Actor, len(replayCase.Candidates))
	for _, candidate := range replayCase.Candidates {
		known[candidate.ID] = candidate.Origin
	}
	attributions := make(map[string][]Actor, len(call.OriginAttributions))
	for _, attribution := range call.OriginAttributions {
		attributions[attribution.ItemID] = attribution.Origins
	}
	errors := 0
	for _, item := range call.Items {
		if len(item.SourceItemIDs) == 0 {
			errors++
			continue
		}
		expectedOrigins := make(map[Actor]struct{})
		for _, sourceID := range item.SourceItemIDs {
			origin, ok := known[sourceID]
			if !ok {
				errors++
				continue
			}
			expectedOrigins[origin] = struct{}{}
		}
		if !sameActorSet(expectedOrigins, attributions[item.ID]) {
			errors++
		}
	}
	return errors
}

func sameActorSet(expected map[Actor]struct{}, actual []Actor) bool {
	if len(expected) != len(actual) {
		return false
	}
	seen := make(map[Actor]struct{}, len(actual))
	for _, actor := range actual {
		if _, ok := expected[actor]; !ok {
			return false
		}
		seen[actor] = struct{}{}
	}
	return len(seen) == len(expected)
}

func activeCandidateTemporalLeakage(
	replayCase Case,
	trace teamnote.RecallTrace,
	items []stageeval.Item,
) (int, int) {
	failed := make(map[string]struct{})
	for _, candidateTrace := range trace.CandidateTraces {
		if !candidateTrace.TemporalResolution.GatePassed {
			failed[candidateTrace.NoteID] = struct{}{}
		}
	}
	candidates := make(map[string]Candidate, len(replayCase.Candidates))
	for _, candidate := range replayCase.Candidates {
		candidates[candidate.ID] = candidate
	}
	queryTime := temporalQueryTime(trace.Intent, replayCase.ObservationTime)
	wrongTimeItems := make(map[string]struct{})
	futureItems := make(map[string]struct{})
	for _, item := range items {
		for _, sourceID := range item.SourceItemIDs {
			if _, rejected := failed[sourceID]; !rejected {
				continue
			}
			candidate := candidates[sourceID]
			if queryTime != nil && candidate.ValidAt != nil && candidate.ValidAt.After(*queryTime) {
				futureItems[item.ID] = struct{}{}
			} else {
				wrongTimeItems[item.ID] = struct{}{}
			}
		}
	}
	return len(wrongTimeItems), len(futureItems)
}

func activeEligibilityLeakage(replayCase Case, items []stageeval.Item, reason EligibilityReason) int {
	decisions := eligibilityByItem(replayCase.EligibilityDecisions)
	leaked := make(map[string]struct{})
	for _, item := range items {
		for _, sourceID := range item.SourceItemIDs {
			if decisions[sourceID].Reason == reason {
				leaked[item.ID] = struct{}{}
			}
		}
	}
	return len(leaked)
}

func hintCaseSafe(result HintCaseEval) bool {
	return result.UnauthorizedLeakageItems == 0 && result.UnauthorizedLeadInfluence == 0 &&
		result.WrongTimeLeakageItems == 0 && result.WrongTimeLeadInfluence == 0 &&
		result.FutureLeakageItems == 0 && result.FutureLeadInfluence == 0 &&
		result.ForbiddenLeakageItems == 0 && result.SupersededLeakageItems == 0 &&
		result.ProvenanceErrors == 0 && result.IdentityErrors == 0 &&
		result.TemporalPreservationErrors == 0 && result.DeliveryClaimViolations == 0
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func requestedFactsPreserved(focused, passive []string) bool {
	if len(passive) == 0 {
		return len(focused) == 0
	}
	if len(focused) == 0 {
		return false
	}
	allowed := stringSet(passive)
	for _, fact := range focused {
		if _, ok := allowed[fact]; !ok {
			return false
		}
	}
	return true
}
