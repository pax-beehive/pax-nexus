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
}

// RecallPolicy configures bounded candidate fusion without exposing adapter details.
type RecallPolicy struct {
	SemanticThreshold float64
	CandidateLimit    int
}

// PlannedRecall is one budgeted Delivery decision awaiting an adapter claim.
type PlannedRecall struct {
	Note      Note
	Text      string
	Tokens    int
	Relevance float64
}

// PlanRecall applies shared precision, ranking, relation, and budget policy to
// candidates supplied by any NoteStore adapter.
func PlanRecall(candidates []RecallCandidate, request RecallRequest, policy RecallPolicy) []PlannedRecall {
	ranked := rankRecallCandidates(candidates, request, policy)
	allNotes := notesFromRecallCandidates(candidates)
	planned := make([]PlannedRecall, 0, len(ranked))
	usedTokens := 0
	selectedNotes := 0
	for _, candidate := range ranked {
		if !hybridRelevant(candidate, request.Query, policy.SemanticThreshold) {
			continue
		}
		if request.MaxItems > 0 && selectedNotes >= request.MaxItems {
			break
		}
		remainingRelated := 0
		if request.MaxItems > 0 {
			remainingRelated = request.MaxItems - selectedNotes - 1
		}
		related := relevantRelatedNotes(
			relatedNotes(candidate.Note, allNotes), request.Query, remainingRelated, request.MaxItems > 0,
		)
		text := FormatForRecallWithRelated(candidate.Note, related)
		tokens := estimateTokens(text)
		if usedTokens+tokens > request.TokenBudget {
			continue
		}
		planned = append(planned, PlannedRecall{
			Note: candidate.Note, Text: text, Tokens: tokens,
			Relevance: hybridRelevance(candidate, request.Query),
		})
		usedTokens += tokens
		selectedNotes += 1 + len(related)
	}
	return planned
}

// AppendPlannedRecall adds a claimed Delivery to its external envelope.
func AppendPlannedRecall(envelope *NoteEnvelope, planned PlannedRecall) {
	note := planned.Note
	envelope.Items = append(envelope.Items, planned.Text)
	envelope.Details = append(envelope.Details, RecalledNote{
		NoteID: note.ID, Revision: note.Revision, Text: planned.Text, Origin: note.Origin,
		Relevance: planned.Relevance, Certainty: CertaintyForKind(note.Kind),
	})
	envelope.Tokens += planned.Tokens
	envelope.Revision = fmt.Sprintf("%s:%d", note.ID, note.Revision)
}

func rankRecallCandidates(candidates []RecallCandidate, request RecallRequest, policy RecallPolicy) []RecallCandidate {
	if strings.TrimSpace(request.Query) == "" {
		result := append([]RecallCandidate(nil), candidates...)
		sortRecallCandidates(result, request)
		return result
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
	for _, candidate := range candidates {
		if score, ok := scores[candidate.ID]; ok {
			candidate.fusionScore = score
			if hybridRelevant(candidate, request.Query, policy.SemanticThreshold) {
				result = append(result, candidate)
			}
		}
	}
	sortRecallCandidates(result, request)
	for rank := range result {
		result[rank].rerankScore = result[rank].fusionScore + reciprocalRank(rank)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].rerankScore > result[right].rerankScore
	})
	return result
}

func sortRecallCandidates(candidates []RecallCandidate, request RecallRequest) {
	sort.SliceStable(candidates, func(left, right int) bool {
		if ownSourceRank(candidates[left].Note, request) != ownSourceRank(candidates[right].Note, request) {
			return ownSourceRank(candidates[left].Note, request) < ownSourceRank(candidates[right].Note, request)
		}
		if notePriority(candidates[left].Kind) != notePriority(candidates[right].Kind) {
			return notePriority(candidates[left].Kind) < notePriority(candidates[right].Kind)
		}
		if !candidates[left].SourceOccurredAt.Equal(candidates[right].SourceOccurredAt) {
			return candidates[left].SourceOccurredAt.After(candidates[right].SourceOccurredAt)
		}
		return candidates[left].UpdatedAt.After(candidates[right].UpdatedAt)
	})
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
