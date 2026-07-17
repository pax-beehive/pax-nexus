package teamnote

import (
	"fmt"
	"sort"
	"strings"
)

const recallRRFConstant = 60.0

// RecallCandidate carries adapter-supplied retrieval scores into deterministic
// Team Note recall policy.
type RecallCandidate struct {
	Note
	LexicalScore  float64
	SemanticScore *float64
	fusionScore   float64
	rerankScore   float64
	intentScore   float64
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
	Candidates    int               `json:"candidates"`
	FusionKept    int               `json:"fusion_kept"`
	PlannedNotes  int               `json:"planned_notes"`
	PlannedTokens int               `json:"planned_tokens"`
	Rejections    []RecallRejection `json:"rejections,omitempty"`
}

// PlanRecall applies shared precision, ranking, relation, and budget policy to
// candidates supplied by any NoteStore adapter.
func PlanRecall(candidates []RecallCandidate, request RecallRequest, policy RecallPolicy) ([]PlannedRecall, RecallTrace) {
	ranked, rejections := rankRecallCandidates(candidates, request, policy)
	trace := RecallTrace{Candidates: len(candidates), FusionKept: len(ranked), Rejections: rejections}
	allNotes := notesFromRecallCandidates(candidates)
	planned := make([]PlannedRecall, 0, len(ranked))
	usedTokens := 0
	selectedNotes := 0
	for index, candidate := range ranked {
		if request.MaxItems > 0 && selectedNotes >= request.MaxItems {
			for _, remaining := range ranked[index:] {
				trace.Rejections = append(trace.Rejections, RecallRejection{NoteID: remaining.ID, Reason: RejectMaxItems})
			}
			break
		}
		if policy.SuppressDuplicates && duplicatesSelected(candidate.Note, planned) {
			trace.Rejections = append(trace.Rejections, RecallRejection{NoteID: candidate.ID, Reason: RejectDuplicate})
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
			trace.Rejections = append(trace.Rejections, RecallRejection{NoteID: candidate.ID, Reason: RejectTokenBudget, Tokens: tokens})
			continue
		}
		planned = append(planned, PlannedRecall{
			Note: candidate.Note, SourceNoteIDs: recallSourceNoteIDs(candidate.Note, related), Text: text, Tokens: tokens,
			Relevance: hybridRelevance(candidate, request.Query),
		})
		usedTokens += tokens
		selectedNotes += 1 + len(related)
	}
	trace.PlannedNotes = len(planned)
	trace.PlannedTokens = usedTokens
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

func rankRecallCandidates(candidates []RecallCandidate, request RecallRequest, policy RecallPolicy) ([]RecallCandidate, []RecallRejection) {
	if strings.TrimSpace(request.Query) == "" {
		result := append([]RecallCandidate(nil), candidates...)
		sortRecallCandidates(result, request)
		return result, nil
	}
	limit := policy.CandidateLimit
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
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

	scores := make(map[string]float64, limit*2)
	for rank, candidate := range lexical {
		if rank >= limit || candidate.LexicalScore <= 0 {
			break
		}
		scores[candidate.ID] += reciprocalRank(rank)
	}
	for rank, candidate := range semantic {
		if rank >= limit || candidate.SemanticScore == nil || *candidate.SemanticScore < policy.SemanticThreshold {
			break
		}
		scores[candidate.ID] += reciprocalRank(rank)
	}

	result := make([]RecallCandidate, 0, len(scores))
	var rejections []RecallRejection
	for _, candidate := range candidates {
		score, ok := scores[candidate.ID]
		if !ok {
			rejections = append(rejections, RecallRejection{NoteID: candidate.ID, Reason: RejectFusionLimit})
			continue
		}
		candidate.fusionScore = score
		if hybridRelevant(candidate, request.Query, policy.SemanticThreshold) {
			candidate.intentScore = queryIntentScore(candidate.Note, request.Query)
			result = append(result, candidate)
		} else {
			rejections = append(rejections, RecallRejection{NoteID: candidate.ID, Reason: RejectRelevanceGate})
		}
	}
	sortRecallCandidates(result, request)
	for rank := range result {
		result[rank].rerankScore = result[rank].fusionScore + reciprocalRank(rank)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].intentScore != result[right].intentScore {
			return result[left].intentScore > result[right].intentScore
		}
		return result[left].rerankScore > result[right].rerankScore
	})
	return result, rejections
}

func sortRecallCandidates(candidates []RecallCandidate, request RecallRequest) {
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].intentScore != candidates[right].intentScore {
			return candidates[left].intentScore > candidates[right].intentScore
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

func reciprocalRank(zeroBasedRank int) float64 {
	return 1 / (recallRRFConstant + float64(zeroBasedRank+1))
}

func ownSourceRank(note Note, request RecallRequest) int {
	if QueryRequestsOwnContext(request.Query) && note.Origin.UserID == request.Actor.UserID {
		return 0
	}
	return 1
}

func hybridRelevant(candidate RecallCandidate, query string, semanticThreshold float64) bool {
	if QueryRelevant(candidate.Note, query) {
		return true
	}
	return candidate.SemanticScore != nil && *candidate.SemanticScore >= semanticThreshold &&
		QuerySemanticallyRelevant(candidate.Note, query)
}

func hybridRelevance(candidate RecallCandidate, query string) float64 {
	relevance := QueryRelevance(candidate.Note, query)
	if candidate.SemanticScore != nil && *candidate.SemanticScore > relevance {
		return *candidate.SemanticScore
	}
	return relevance
}

func notesFromRecallCandidates(candidates []RecallCandidate) []Note {
	notes := make([]Note, 0, len(candidates))
	for _, candidate := range candidates {
		notes = append(notes, candidate.Note)
	}
	return notes
}
