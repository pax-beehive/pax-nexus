package teamnote

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RecallCandidate carries adapter-supplied retrieval scores into deterministic
// Team Note recall policy.
type RecallCandidate struct {
	Note
	LexicalScore       float64
	SemanticScore      *float64
	intentScore        float64
	temporalFit        int
	factCoverage       int
	coordination       int
	exactMatch         int
	lexicalFit         float64
	routingFit         int
	explicitLane       int
	evidenceConfidence float64
	hardGateFailure    RecallRejectReason
}

// RecallPolicy configures bounded retrieval and evidence selection without exposing adapter details.
type RecallPolicy struct {
	SemanticThreshold float64
	EvidenceThreshold float64
	CandidateLimit    int
	ObservationTime   time.Time
	// SuppressDuplicates skips candidates that restate an already selected
	// note and keeps selected notes out of later related blocks.
	SuppressDuplicates bool
	// DegradeRelated falls back to the note without its related block when
	// the composed text would exceed the remaining token budget.
	DegradeRelated bool
}

// duplicateBodySimilarity is the term-set Jaccard overlap above which two
// note bodies are treated as the same fact.
const duplicateBodySimilarity = 0.8

// PlannedRecall is one budgeted Delivery decision awaiting an adapter claim.
type PlannedRecall struct {
	Note          Note
	SourceNoteIDs []string
	Text          string
	Tokens        int
	Relevance     float64
}

// RecallRejectReason identifies the recall stage that rejected one available
// candidate.
type RecallRejectReason string

const (
	RejectFusionLimit    RecallRejectReason = "fusion_limit"
	RejectRelevanceGate  RecallRejectReason = "relevance_gate"
	RejectMaxItems       RecallRejectReason = "max_items"
	RejectTokenBudget    RecallRejectReason = "token_budget"
	RejectDeliveryClaim  RecallRejectReason = "delivery_claim_lost"
	RejectDuplicate      RecallRejectReason = "duplicate"
	RejectTemporalGate   RecallRejectReason = "temporal_gate"
	RejectProvenanceGate RecallRejectReason = "provenance_gate"
	RejectUnsafeContent  RecallRejectReason = "unsafe_content"
	RejectEvidenceGate   RecallRejectReason = "evidence_gate"
)

// RecallRejection records why one available candidate was not planned for
// Delivery.
type RecallRejection struct {
	NoteID string             `json:"note_id"`
	Reason RecallRejectReason `json:"reason"`
	Tokens int                `json:"tokens,omitempty"`
}

// RecallTrace summarizes each recall stage for one PlanRecall invocation so
// evaluations can attribute losses without re-running retrieval.
type RecallTrace struct {
	PlanVersion       string                 `json:"plan_version"`
	ScoringVersion    string                 `json:"scoring_version"`
	EvidenceThreshold float64                `json:"evidence_threshold"`
	Intent            RecallIntent           `json:"intent"`
	LanesExecuted     []RecallLane           `json:"lanes_executed"`
	CandidateTraces   []RecallCandidateTrace `json:"candidate_traces"`
	SelectedSet       []string               `json:"selected_set,omitempty"`
	BudgetDrops       []RecallRejection      `json:"budget_drops,omitempty"`
	DeliveredItems    []string               `json:"delivered_items,omitempty"`
	Candidates        int                    `json:"candidates"`
	FusionKept        int                    `json:"fusion_kept"`
	PlannedNotes      int                    `json:"planned_notes"`
	PlannedTokens     int                    `json:"planned_tokens"`
	Rejections        []RecallRejection      `json:"rejections,omitempty"`
}

// PlanRecall applies shared precision, ranking, relation, and budget policy to
// candidates supplied by any NoteStore adapter.
func PlanRecall(candidates []RecallCandidate, request RecallRequest, policy RecallPolicy) ([]PlannedRecall, RecallTrace) {
	intent := compileRecallIntent(request)
	observationTime := recallObservationTime(candidates, policy.ObservationTime)
	ranked, rejections, lanes := rankRecallCandidates(candidates, request, policy, intent, observationTime)
	trace := initializeRecallTrace(
		candidates, ranked, request, intent, observationTime, evidenceThreshold(policy), lanes, rejections,
	)
	allNotes := recallRelationEligibleNotes(candidates, intent, observationTime)
	planned := make([]PlannedRecall, 0, len(ranked))
	usedTokens := 0
	selectedNotes := 0
	for index, candidate := range ranked {
		if recallIDSelected(trace.SelectedSet, candidate.ID) {
			continue
		}
		if request.MaxItems > 0 && selectedNotes >= request.MaxItems {
			for _, remaining := range ranked[index:] {
				recordRecallRejection(&trace, RecallRejection{NoteID: remaining.ID, Reason: RejectMaxItems})
			}
			break
		}
		if policy.SuppressDuplicates && duplicatesSelected(candidate.Note, planned) {
			recordRecallRejection(&trace, RecallRejection{NoteID: candidate.ID, Reason: RejectDuplicate})
			continue
		}
		remainingRelated := 0
		if request.MaxItems > 0 {
			remainingRelated = request.MaxItems - selectedNotes - 1
		}
		related := relevantRelatedNotes(
			relatedNotes(candidate.Note, allNotes), request.Query, remainingRelated, request.MaxItems > 0,
		)
		if policy.SuppressDuplicates {
			related = excludeRecallIDs(related, trace.SelectedSet)
		}
		recordRecallRelations(&trace, candidate.Note, related)
		text := FormatForRecallWithRelated(candidate.Note, related)
		tokens := estimateTokens(text)
		if policy.DegradeRelated && len(related) > 0 && usedTokens+tokens > request.TokenBudget {
			related = nil
			text = FormatForRecall(candidate.Note)
			tokens = estimateTokens(text)
		}
		if usedTokens+tokens > request.TokenBudget {
			recordRecallRejection(&trace, RecallRejection{NoteID: candidate.ID, Reason: RejectTokenBudget, Tokens: tokens})
			continue
		}
		planned = append(planned, PlannedRecall{
			Note: candidate.Note, SourceNoteIDs: recallSourceNoteIDs(candidate.Note, related), Text: text, Tokens: tokens,
			Relevance: candidate.evidenceConfidence,
		})
		markRecallEvidence(&trace, candidate.Note, related)
		usedTokens += tokens
		selectedNotes += 1 + len(related)
	}
	trace.PlannedNotes = len(planned)
	trace.PlannedTokens = usedTokens
	finalizeRecallTrace(&trace)
	return planned, trace
}

func recallSourceNoteIDs(primary Note, related []Note) []string {
	result := make([]string, 0, len(related)+1)
	result = append(result, primary.ID)
	for _, note := range related {
		result = append(result, note.ID)
	}
	return result
}

// duplicatesSelected reports whether a candidate restates an already selected
// note by near-identical body terms.
func duplicatesSelected(note Note, planned []PlannedRecall) bool {
	for _, delivery := range planned {
		if bodyOverlap(note.Body, delivery.Note.Body) >= duplicateBodySimilarity {
			return true
		}
	}
	return false
}

func excludeRecallIDs(related []Note, selectedIDs []string) []Note {
	if len(related) == 0 {
		return related
	}
	selected := make(map[string]struct{}, len(selectedIDs))
	for _, noteID := range selectedIDs {
		selected[noteID] = struct{}{}
	}
	kept := make([]Note, 0, len(related))
	for _, note := range related {
		if _, ok := selected[note.ID]; !ok {
			kept = append(kept, note)
		}
	}
	return kept
}

// bodyOverlap reports the Jaccard similarity of two bodies' searchable terms.
func bodyOverlap(left, right string) float64 {
	leftTerms := searchableTerms(left)
	rightTerms := searchableTerms(right)
	if len(leftTerms) == 0 || len(rightTerms) == 0 {
		return 0
	}
	shared := 0
	for term := range leftTerms {
		if _, ok := rightTerms[term]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(leftTerms)+len(rightTerms)-shared)
}

// AppendPlannedRecall adds a claimed Delivery to its external envelope.
func AppendPlannedRecall(envelope *NoteEnvelope, planned PlannedRecall) {
	note := planned.Note
	envelope.Items = append(envelope.Items, planned.Text)
	envelope.Details = append(envelope.Details, RecalledNote{
		NoteID: note.ID, Revision: note.Revision, Text: planned.Text, Origin: note.Origin,
		SourceNoteIDs: planned.SourceNoteIDs,
		Relevance:     planned.Relevance, Certainty: CertaintyForKind(note.Kind),
	})
	envelope.Tokens += planned.Tokens
	envelope.Revision = fmt.Sprintf("%s:%d", note.ID, note.Revision)
}

func rankRecallCandidates(
	candidates []RecallCandidate,
	request RecallRequest,
	policy RecallPolicy,
	intent RecallIntent,
	observationTime time.Time,
) ([]RecallCandidate, []RecallRejection, recallLaneSet) {
	if strings.TrimSpace(request.Query) == "" {
		result, rejections := evaluatedRecallCandidates(candidates, request, intent, observationTime)
		sortRecallCandidates(result, request)
		return result, rejections, recallLaneSet{}
	}
	limit := policy.CandidateLimit
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	lanes := recallLaneMembership(candidates, request, policy, limit)
	result := make([]RecallCandidate, 0, lanes.capacity())
	var rejections []RecallRejection
	for _, candidate := range candidates {
		admitted, rejection, keep, semanticFallback := admitRecallCandidate(
			candidate, request, intent, observationTime, evidenceThreshold(policy), lanes,
		)
		if semanticFallback {
			lanes.semanticFallback[candidate.ID] = struct{}{}
		}
		if keep {
			result = append(result, admitted)
			continue
		}
		rejections = append(rejections, rejection)
	}
	sortRecallCandidates(result, request)
	return result, rejections, lanes
}

type recallLaneSet struct {
	exact            map[string]struct{}
	lexical          map[string]struct{}
	semantic         map[string]struct{}
	semanticFallback map[string]struct{}
}

func (lanes recallLaneSet) capacity() int {
	return len(lanes.exact) + len(lanes.lexical) + len(lanes.semantic)
}

func recallLaneMembership(
	candidates []RecallCandidate,
	request RecallRequest,
	policy RecallPolicy,
	limit int,
) recallLaneSet {
	lexical := append([]RecallCandidate(nil), candidates...)
	sort.SliceStable(lexical, func(left, right int) bool {
		return lexical[left].LexicalScore > lexical[right].LexicalScore
	})
	semantic := append([]RecallCandidate(nil), candidates...)
	sort.SliceStable(semantic, func(left, right int) bool {
		leftScore, rightScore := semantic[left].SemanticScore, semantic[right].SemanticScore
		if leftScore == nil {
			return false
		}
		if rightScore == nil {
			return true
		}
		return *leftScore > *rightScore
	})

	lanes := recallLaneSet{
		exact: make(map[string]struct{}, limit), lexical: make(map[string]struct{}, limit),
		semantic: make(map[string]struct{}, limit), semanticFallback: make(map[string]struct{}, limit),
	}
	for rank, candidate := range lexical {
		if rank >= limit || candidate.LexicalScore <= 0 {
			break
		}
		lanes.lexical[candidate.ID] = struct{}{}
	}
	for _, candidate := range lexical {
		if len(lanes.exact) >= limit {
			break
		}
		if exactScopeMatch(candidate.Note, request) {
			lanes.exact[candidate.ID] = struct{}{}
		}
	}
	for rank, candidate := range semantic {
		if rank >= limit || candidate.SemanticScore == nil || *candidate.SemanticScore < policy.SemanticThreshold {
			break
		}
		lanes.semantic[candidate.ID] = struct{}{}
	}
	return lanes
}

func admitRecallCandidate(
	candidate RecallCandidate,
	request RecallRequest,
	intent RecallIntent,
	observationTime time.Time,
	evidenceThreshold float64,
	lanes recallLaneSet,
) (RecallCandidate, RecallRejection, bool, bool) {
	candidate = evaluateRecallRanking(candidate, request, intent, observationTime)
	if candidate.hardGateFailure != "" {
		return candidate, RecallRejection{NoteID: candidate.ID, Reason: candidate.hardGateFailure}, false, false
	}
	_, exactRetrieved := lanes.exact[candidate.ID]
	_, lexicalRetrieved := lanes.lexical[candidate.ID]
	_, semanticRetrieved := lanes.semantic[candidate.ID]
	if QueryRelevant(candidate.Note, request.Query) && (lexicalRetrieved || exactRetrieved) {
		candidate.explicitLane = 1
		return candidate, RecallRejection{}, true, false
	}
	if semanticRetrieved && QuerySemanticallyRelevant(candidate.Note, request.Query) {
		if semanticEvidenceSupported(candidate, evidenceThreshold) {
			return candidate, RecallRejection{}, true, true
		}
		return candidate, RecallRejection{NoteID: candidate.ID, Reason: RejectEvidenceGate}, false, true
	}
	if !exactRetrieved && !lexicalRetrieved && !semanticRetrieved {
		return candidate, RecallRejection{NoteID: candidate.ID, Reason: RejectFusionLimit}, false, false
	}
	return candidate, RecallRejection{NoteID: candidate.ID, Reason: RejectRelevanceGate}, false, semanticRetrieved
}

func evaluatedRecallCandidates(
	candidates []RecallCandidate,
	request RecallRequest,
	intent RecallIntent,
	observationTime time.Time,
) ([]RecallCandidate, []RecallRejection) {
	result := make([]RecallCandidate, 0, len(candidates))
	var rejections []RecallRejection
	for _, candidate := range candidates {
		candidate = evaluateRecallRanking(candidate, request, intent, observationTime)
		if candidate.hardGateFailure == "" {
			candidate.explicitLane = 1
			result = append(result, candidate)
			continue
		}
		rejections = append(rejections, RecallRejection{NoteID: candidate.ID, Reason: candidate.hardGateFailure})
	}
	return result, rejections
}

func evaluateRecallRanking(candidate RecallCandidate, request RecallRequest, intent RecallIntent, observationTime time.Time) RecallCandidate {
	temporalPassed, _ := temporalGate(candidate, intent, observationTime)
	if temporalPassed {
		candidate.temporalFit = 1
	}
	candidate.hardGateFailure = recallHardGateFailure(candidate.Note, intent, observationTime)
	candidate.intentScore = queryIntentScore(candidate.Note, request.Query)
	candidate.factCoverage = requiredFactCoverage(candidate.Note, intent)
	candidate.coordination = coordinationMatch(candidate.Note, intent)
	if exactScopeMatch(candidate.Note, request) {
		candidate.exactMatch = 1
	}
	candidate.lexicalFit = QueryRelevance(candidate.Note, request.Query)
	candidate.routingFit = recallRoutingAffinity(candidate.Note, request, intent)
	trace := evaluateRecallCandidate(candidate, request, intent, observationTime)
	candidate.evidenceConfidence = trace.EvidenceConfidence
	return candidate
}

func evidenceThreshold(policy RecallPolicy) float64 {
	if policy.EvidenceThreshold <= 0 {
		return 0.4
	}
	return policy.EvidenceThreshold
}

func semanticEvidenceSupported(candidate RecallCandidate, threshold float64) bool {
	if candidate.evidenceConfidence < threshold {
		return false
	}
	return candidate.coordination > 0 || candidate.routingFit > 0 || candidate.intentScore > 0
}

func sortRecallCandidates(candidates []RecallCandidate, request RecallRequest) {
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].temporalFit != candidates[right].temporalFit {
			return candidates[left].temporalFit > candidates[right].temporalFit
		}
		if candidates[left].intentScore != candidates[right].intentScore {
			return candidates[left].intentScore > candidates[right].intentScore
		}
		if candidates[left].factCoverage != candidates[right].factCoverage {
			return candidates[left].factCoverage > candidates[right].factCoverage
		}
		if candidates[left].coordination != candidates[right].coordination {
			return candidates[left].coordination > candidates[right].coordination
		}
		if candidates[left].exactMatch != candidates[right].exactMatch {
			return candidates[left].exactMatch > candidates[right].exactMatch
		}
		if candidates[left].lexicalFit != candidates[right].lexicalFit {
			return candidates[left].lexicalFit > candidates[right].lexicalFit
		}
		if candidates[left].explicitLane != candidates[right].explicitLane {
			return candidates[left].explicitLane > candidates[right].explicitLane
		}
		if candidates[left].routingFit != candidates[right].routingFit {
			return candidates[left].routingFit > candidates[right].routingFit
		}
		if ownSourceRank(candidates[left].Note, request) != ownSourceRank(candidates[right].Note, request) {
			return ownSourceRank(candidates[left].Note, request) < ownSourceRank(candidates[right].Note, request)
		}
		if recallKindPriority(candidates[left].Kind, request.Query) != recallKindPriority(candidates[right].Kind, request.Query) {
			return recallKindPriority(candidates[left].Kind, request.Query) < recallKindPriority(candidates[right].Kind, request.Query)
		}
		if !candidates[left].SourceOccurredAt.Equal(candidates[right].SourceOccurredAt) {
			return candidates[left].SourceOccurredAt.After(candidates[right].SourceOccurredAt)
		}
		if !candidates[left].UpdatedAt.Equal(candidates[right].UpdatedAt) {
			return candidates[left].UpdatedAt.After(candidates[right].UpdatedAt)
		}
		return candidates[left].ID < candidates[right].ID
	})
}

func queryIntentScore(note Note, query string) float64 {
	if scalarSlotRequested(query) {
		subjectTerms := searchableTerms(note.Subject)
		if len(subjectTerms) == 0 || !slotCompatible(note.Subject+" "+note.Body, query) {
			return 0
		}
		queryTerms := searchableTerms(query)
		matched := 0
		for term := range subjectTerms {
			if _, ok := queryTerms[term]; ok {
				matched++
			}
		}
		return float64(matched) / float64(len(subjectTerms))
	}
	if queryRequestsCurrentState(query) {
		return QueryRelevance(note, query)
	}
	return 0
}

func recallKindPriority(kind NoteKind, query string) int {
	if !scalarSlotRequested(query) && !queryRequestsCurrentState(query) {
		return notePriority(kind)
	}
	switch kind {
	case KindStatus:
		return 0
	case KindArtifactReference:
		return 1
	case KindBlocker:
		return 2
	case KindHandoff:
		return 3
	default:
		return 4
	}
}

func ownSourceRank(note Note, request RecallRequest) int {
	if QueryRequestsOwnContext(request.Query) && note.Origin.UserID == request.Actor.UserID {
		return 0
	}
	return 1
}

func recallRelationEligibleNotes(candidates []RecallCandidate, intent RecallIntent, observationTime time.Time) []Note {
	notes := make([]Note, 0, len(candidates))
	for _, candidate := range candidates {
		if recallHardGateFailure(candidate.Note, intent, observationTime) == "" {
			notes = append(notes, candidate.Note)
		}
	}
	return notes
}
