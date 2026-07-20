package teamnote

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	HintRecallV1PlanVersion    = "general-recall-v3-hint-recall-v1"
	HintRecallV1ScoringVersion = "hint-utility-v1-selective-eval-only"
	defaultHintThreshold       = 0.65
	maximumHintSubjectTerms    = 8
)

func planRecallHint(
	candidates []RecallCandidate,
	request RecallRequest,
	policy RecallPolicy,
	trace *RecallTrace,
	planned []PlannedRecall,
	usedTokens, selectedNotes int,
) (PlannedRecall, bool) {
	if !policy.EnableHintRecall || strings.TrimSpace(request.Query) == "" || len(planned) > 0 {
		return PlannedRecall{}, false
	}
	if request.MaxItems > 0 && selectedNotes >= request.MaxItems {
		return PlannedRecall{}, false
	}
	lead, score, ok := bestHintLead(candidates, request, policy, trace, true)
	if !ok {
		return PlannedRecall{}, false
	}
	focusedQuery := focusedHintQuery(request, lead.Note, trace.Intent)
	text := fmt.Sprintf("[Recall hint - not evidence]\nPotentially useful Team Memory may be available. If it would affect the answer, call active_recall with: %q.", focusedQuery)
	tokens := estimateTokens(text)
	if usedTokens+tokens > request.TokenBudget {
		recordRecallRejection(trace, RecallRejection{NoteID: lead.ID, Reason: RejectTokenBudget, Tokens: tokens})
		return PlannedRecall{}, false
	}
	markRecallHint(trace, lead.ID, score, focusedQuery)
	trace.PlanVersion = HintRecallV1PlanVersion
	trace.ScoringVersion = HintRecallV1ScoringVersion
	return PlannedRecall{
		Note: lead.Note, SourceNoteIDs: []string{lead.ID}, Text: text, Tokens: tokens,
		Relevance: score, Disposition: RecallDispositionHint, ClaimNoteDelivery: false,
		HintFingerprint: hintFingerprint(request, lead.ID, focusedQuery),
	}, true
}

func bestHintLead(
	candidates []RecallCandidate,
	request RecallRequest,
	policy RecallPolicy,
	trace *RecallTrace,
	passiveGap bool,
) (RecallCandidate, float64, bool) {
	threshold := policy.HintThreshold
	if threshold <= 0 {
		threshold = defaultHintThreshold
	}
	var best RecallCandidate
	bestScore := 0.0
	for _, candidate := range candidates {
		candidateTrace := recallCandidateTraceByID(trace.CandidateTraces, candidate.ID)
		if candidateTrace == nil || !hintEligibleCandidate(candidate, *candidateTrace, request, policy) || recallIDSelected(trace.SelectedSet, candidate.ID) {
			continue
		}
		score := hintUtility(candidate, request, *candidateTrace, passiveGap)
		if score >= threshold && score >= policy.HintMinMarginalUtility && score > bestScore {
			best, bestScore = candidate, score
		}
	}
	return best, bestScore, bestScore > 0
}

func hintEligibleCandidate(candidate RecallCandidate, trace RecallCandidateTrace, request RecallRequest, policy RecallPolicy) bool {
	for _, gate := range trace.HardGateResults {
		if !gate.Passed {
			return false
		}
	}
	if policy.HintMinQueryRelevance > 0 && QueryRelevance(candidate.Note, request.Query) < policy.HintMinQueryRelevance {
		return false
	}
	switch trace.RejectionReason {
	case RejectEvidenceGate, RejectRelevanceGate:
		return true
	case RejectFusionLimit:
		threshold := policy.HintSemanticThreshold
		if threshold <= 0 {
			threshold = policy.SemanticThreshold
		}
		return candidate.SemanticScore != nil && *candidate.SemanticScore >= threshold
	default:
		return false
	}
}

func hintUtility(candidate RecallCandidate, request RecallRequest, trace RecallCandidateTrace, passiveGap bool) float64 {
	score := 0.0
	if passiveGap {
		score += 0.25
	}
	if candidate.SemanticScore != nil {
		score += 0.40 * clampUnit(*candidate.SemanticScore)
	}
	score += 0.15 * clampUnit(QueryRelevance(candidate.Note, request.Query))
	if len(candidate.RelatedSubjects) > 0 {
		score += 0.10
	}
	if len(candidate.EvidenceEventIDs) > 0 {
		score += 0.10
	}
	if trace.TemporalResolution.GatePassed {
		score += 0.10
	}
	return clampUnit(score)
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func focusedHintQuery(request RecallRequest, note Note, intent RecallIntent) string {
	parts := []string{strings.TrimSpace(request.Query)}
	if subject := safeHintTerms(note.Subject); subject != "" {
		parts = append(parts, "related topic: "+subject)
	}
	if len(note.RelatedSubjects) > 0 {
		if related := safeHintTerms(strings.Join(note.RelatedSubjects, " ")); related != "" {
			parts = append(parts, "related context: "+related)
		}
	}
	if intent.ValidAt != nil {
		parts = append(parts, "as of "+intent.ValidAt.UTC().Format("2006-01-02"))
	}
	if intent.ChangedSince != nil {
		parts = append(parts, "changes since "+intent.ChangedSince.UTC().Format("2006-01-02"))
	}
	return strings.Join(parts, "; ")
}

func safeHintTerms(value string) string {
	fields := strings.Fields(value)
	if len(fields) > maximumHintSubjectTerms {
		fields = fields[:maximumHintSubjectTerms]
	}
	for index := range fields {
		fields[index] = strings.Trim(fields[index], "\"'`.,:;!?()[]{}")
	}
	return strings.Join(fields, " ")
}

func hintFingerprint(request RecallRequest, noteID, focusedQuery string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		request.Actor.UserID, request.Actor.AgentID, request.Actor.SessionID, noteID, focusedQuery,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func recallCandidateTraceByID(traces []RecallCandidateTrace, noteID string) *RecallCandidateTrace {
	for index := range traces {
		if traces[index].NoteID == noteID {
			return &traces[index]
		}
	}
	return nil
}

func markRecallHint(trace *RecallTrace, noteID string, score float64, focusedQuery string) {
	for index := range trace.CandidateTraces {
		candidate := &trace.CandidateTraces[index]
		if candidate.NoteID != noteID {
			continue
		}
		candidate.Disposition = RecallDispositionHint
		candidate.RejectionReason = ""
		candidate.HintUtility = score
		candidate.FocusedQuery = focusedQuery
		break
	}
	kept := trace.Rejections[:0]
	for _, rejection := range trace.Rejections {
		if rejection.NoteID != noteID {
			kept = append(kept, rejection)
		}
	}
	trace.Rejections = kept
}
