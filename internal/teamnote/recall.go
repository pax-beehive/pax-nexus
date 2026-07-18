package teamnote

import (
	"fmt"
	"sort"
	"strings"
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
}

// RecallPolicy configures bounded candidate fusion without exposing adapter details.
type RecallPolicy struct {
	SemanticThreshold float64
	CandidateLimit    int
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
	RejectFusionLimit   RecallRejectReason = "fusion_limit"
	RejectRelevanceGate RecallRejectReason = "relevance_gate"
	RejectMaxItems      RecallRejectReason = "max_items"
	RejectTokenBudget   RecallRejectReason = "token_budget"
	RejectDeliveryClaim RecallRejectReason = "delivery_claim_lost"
	RejectDuplicate     RecallRejectReason = "duplicate"
	RejectTemporalGate  RecallRejectReason = "temporal_gate"
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
	PlanVersion     string                 `json:"plan_version"`
	ScoringVersion  string                 `json:"scoring_version"`
	Intent          RecallIntent           `json:"intent"`
	LanesExecuted   []RecallLane           `json:"lanes_executed"`
	CandidateTraces []RecallCandidateTrace `json:"candidate_traces"`
	SelectedSet     []string               `json:"selected_set,omitempty"`
	BudgetDrops     []RecallRejection      `json:"budget_drops,omitempty"`
	DeliveredItems  []string               `json:"delivered_items,omitempty"`
	Candidates      int                    `json:"candidates"`
	FusionKept      int                    `json:"fusion_kept"`
	PlannedNotes    int                    `json:"planned_notes"`
	PlannedTokens   int                    `json:"planned_tokens"`
	Rejections      []RecallRejection      `json:"rejections,omitempty"`
}

// PlanRecall applies shared precision, ranking, relation, and budget policy to
// candidates supplied by any NoteStore adapter.
func PlanRecall(candidates []RecallCandidate, request RecallRequest, policy RecallPolicy) ([]PlannedRecall, RecallTrace) {
	intent := compileRecallIntent(request)
	ranked, rejections := rankRecallCandidates(candidates, request, policy, intent)
	trace := initializeRecallTrace(candidates, ranked, request, intent, rejections)
	allNotes := notesFromRecallCandidates(candidates)
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
			related = excludeSelected(related, planned)
		}
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

func excludeSelected(related []Note, planned []PlannedRecall) []Note {
	if len(related) == 0 {
		return related
	}
	selected := make(map[string]struct{}, len(planned))
	for _, delivery := range planned {
		selected[delivery.Note.ID] = struct{}{}
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
) ([]RecallCandidate, []RecallRejection) {
	if strings.TrimSpace(request.Query) == "" {
		result := evaluatedRecallCandidates(candidates, request, intent)
		sortRecallCandidates(result, request)
		return result, nil
	}
	limit := policy.CandidateLimit
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	lexicalLane, semanticLane := recallLaneMembership(candidates, policy, limit)
	result := make([]RecallCandidate, 0, len(lexicalLane)+len(semanticLane))
	var rejections []RecallRejection
	for _, candidate := range candidates {
		admitted, rejection, keep := admitRecallCandidate(candidate, request, intent, lexicalLane, semanticLane)
		if keep {
			result = append(result, admitted)
			continue
		}
		rejections = append(rejections, rejection)
	}
	sortRecallCandidates(result, request)
	return result, rejections
}

func recallLaneMembership(
	candidates []RecallCandidate,
	policy RecallPolicy,
	limit int,
) (map[string]struct{}, map[string]struct{}) {
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

	lexicalLane := make(map[string]struct{}, limit)
	for rank, candidate := range lexical {
		if rank >= limit || candidate.LexicalScore <= 0 {
			break
		}
		lexicalLane[candidate.ID] = struct{}{}
	}
	semanticLane := make(map[string]struct{}, limit)
	for rank, candidate := range semantic {
		if rank >= limit || candidate.SemanticScore == nil || *candidate.SemanticScore < policy.SemanticThreshold {
			break
		}
		semanticLane[candidate.ID] = struct{}{}
	}
	return lexicalLane, semanticLane
}

func admitRecallCandidate(
	candidate RecallCandidate,
	request RecallRequest,
	intent RecallIntent,
	lexicalLane map[string]struct{},
	semanticLane map[string]struct{},
) (RecallCandidate, RecallRejection, bool) {
	candidate = evaluateRecallRanking(candidate, request, intent)
	if candidate.temporalFit == 0 {
		return candidate, RecallRejection{NoteID: candidate.ID, Reason: RejectTemporalGate}, false
	}
	_, lexicalRetrieved := lexicalLane[candidate.ID]
	_, semanticRetrieved := semanticLane[candidate.ID]
	if QueryRelevant(candidate.Note, request.Query) && (lexicalRetrieved || candidate.exactMatch > 0) {
		candidate.explicitLane = 1
		return candidate, RecallRejection{}, true
	}
	if semanticRetrieved && QuerySemanticallyRelevant(candidate.Note, request.Query) {
		return candidate, RecallRejection{}, true
	}
	if !lexicalRetrieved && !semanticRetrieved && candidate.exactMatch == 0 {
		return candidate, RecallRejection{NoteID: candidate.ID, Reason: RejectFusionLimit}, false
	}
	return candidate, RecallRejection{NoteID: candidate.ID, Reason: RejectRelevanceGate}, false
}

func evaluatedRecallCandidates(candidates []RecallCandidate, request RecallRequest, intent RecallIntent) []RecallCandidate {
	result := make([]RecallCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = evaluateRecallRanking(candidate, request, intent)
		if candidate.temporalFit > 0 {
			candidate.explicitLane = 1
			result = append(result, candidate)
		}
	}
	return result
}

func evaluateRecallRanking(candidate RecallCandidate, request RecallRequest, intent RecallIntent) RecallCandidate {
	temporalPassed, _ := temporalGate(candidate, intent)
	if temporalPassed {
		candidate.temporalFit = 1
	}
	candidate.intentScore = queryIntentScore(candidate.Note, request.Query)
	candidate.factCoverage = requiredFactCoverage(candidate.Note, intent)
	candidate.coordination = coordinationMatch(candidate.Note, intent)
	if exactScopeMatch(candidate.Note, request) {
		candidate.exactMatch = 1
	}
	candidate.lexicalFit = QueryRelevance(candidate.Note, request.Query)
	candidate.routingFit = recallRoutingAffinity(candidate.Note, request, intent)
	trace := evaluateRecallCandidate(candidate, request, intent)
	candidate.evidenceConfidence = trace.EvidenceConfidence
	return candidate
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

func notesFromRecallCandidates(candidates []RecallCandidate) []Note {
	notes := make([]Note, 0, len(candidates))
	for _, candidate := range candidates {
		notes = append(notes, candidate.Note)
	}
	return notes
}
