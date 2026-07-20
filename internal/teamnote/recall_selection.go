package teamnote

import (
	"strings"
	"time"
)

// selectFinalStateFamilies removes planner-visible stale members of a durable
// factual family before token packing. Historical modes intentionally retain
// their revision chains.
func selectFinalStateFamilies(
	candidates []RecallCandidate,
	intent RecallIntent,
) ([]RecallCandidate, []RecallRejection) {
	if intent.Mode != RecallModeCurrent {
		return candidates, nil
	}
	winners := make(map[string]RecallCandidate, len(candidates))
	for _, candidate := range candidates {
		family := recallCandidateFamily(candidate)
		if family == "" {
			continue
		}
		winner, exists := winners[family]
		if !exists || finalStateAfter(candidate, winner) {
			winners[family] = candidate
		}
	}
	selected := make([]RecallCandidate, 0, len(candidates))
	rejections := make([]RecallRejection, 0)
	for _, candidate := range candidates {
		family := recallCandidateFamily(candidate)
		if family == "" || winners[family].ID == candidate.ID {
			selected = append(selected, candidate)
			continue
		}
		rejections = append(rejections, RecallRejection{NoteID: candidate.ID, Reason: RejectSupersededState})
	}
	return selected, rejections
}

func selectRankedDistinctFacts(
	candidates []RecallCandidate,
	policy RecallPolicy,
	intent RecallIntent,
) ([]RecallCandidate, []RecallRejection) {
	if !policy.SuppressDuplicates || intent.Mode == RecallModeHistory || intent.Mode == RecallModeChangesSince {
		return candidates, nil
	}
	kept := make([]RecallCandidate, 0, len(candidates))
	rejections := make([]RecallRejection, 0)
	for _, candidate := range candidates {
		duplicate := false
		for _, prior := range kept {
			if bodyOverlap(candidate.Body, prior.Body) >= duplicateBodySimilarity {
				duplicate = true
				break
			}
		}
		if duplicate {
			rejections = append(rejections, RecallRejection{NoteID: candidate.ID, Reason: RejectDuplicate})
			continue
		}
		kept = append(kept, candidate)
	}
	return kept, rejections
}

func recallCandidateFamily(candidate RecallCandidate) string {
	if candidate.CanonicalNoteID != "" {
		return "note:" + candidate.CanonicalNoteID
	}
	if candidate.Key != "" {
		return "key:" + candidate.Key
	}
	return ""
}

func finalStateAfter(left, right RecallCandidate) bool {
	if left.Revision != right.Revision {
		return left.Revision > right.Revision
	}
	leftEffective, rightEffective := recallEffectiveTime(left.Note), recallEffectiveTime(right.Note)
	if !leftEffective.Equal(rightEffective) {
		return leftEffective.After(rightEffective)
	}
	if !left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.UpdatedAt.After(right.UpdatedAt)
	}
	return finalStateRecordedAt(left).After(finalStateRecordedAt(right))
}

func finalStateRecordedAt(candidate RecallCandidate) time.Time {
	if !candidate.CreatedAt.IsZero() {
		return candidate.CreatedAt
	}
	return candidate.SourceOccurredAt
}

type recallSelectionOption struct {
	candidate RecallCandidate
	related   []Note
	gain      int
}

// orderRecallCandidatesByCoverage chooses primary evidence bundles by
// uncovered requested facts before token cost. It leaves final packing and
// delivery claims to PlanRecall.
func orderRecallCandidatesByCoverage(
	candidates []RecallCandidate,
	relationPlans map[string][]Note,
	request RecallRequest,
	intent RecallIntent,
) ([]RecallCandidate, []RecallRejection) {
	if strings.TrimSpace(request.Query) == "" || intent.Mode == RecallModeHistory || intent.Mode == RecallModeChangesSince {
		return candidates, nil
	}
	entities := relationQueryEntities(searchableTerms(request.Query))
	covered := make(map[string]struct{})
	remaining := append([]RecallCandidate(nil), candidates...)
	ordered := make([]RecallCandidate, 0, len(candidates))
	selectedRelated := make(map[string]struct{})
	for len(remaining) > 0 {
		options := make([]recallSelectionOption, 0, len(remaining))
		for _, candidate := range remaining {
			if _, related := selectedRelated[candidate.ID]; related {
				continue
			}
			optionRelated := relationPlans[candidate.ID]
			if request.MaxItems > 0 && len(optionRelated) >= request.MaxItems {
				optionRelated = optionRelated[:request.MaxItems-1]
			}
			options = append(options, recallSelectionOption{
				candidate: candidate, related: optionRelated,
				gain: recallBundleFactGain(candidate.Note, optionRelated, entities, intent.RequestedFacts, covered),
			})
		}
		if len(options) == 0 {
			break
		}
		best := options[0]
		for _, option := range options[1:] {
			if option.gain > best.gain {
				best = option
			}
		}
		if best.gain == 0 {
			ordered = append(ordered, remaining...)
			break
		}
		ordered = append(ordered, best.candidate)
		addRecallBundleFactSlots(covered, best.candidate.Note, best.related, entities, intent.RequestedFacts)
		for _, related := range best.related {
			selectedRelated[related.ID] = struct{}{}
		}
		remaining = removeRecallCandidate(remaining, best.candidate.ID)
	}
	return ordered, nil
}

func recallBundleFactGain(
	primary Note,
	related []Note,
	entities, facts []string,
	covered map[string]struct{},
) int {
	prospective := make(map[string]struct{}, len(covered))
	for slot := range covered {
		prospective[slot] = struct{}{}
	}
	addRecallBundleFactSlots(prospective, primary, related, entities, facts)
	return len(prospective) - len(covered)
}

func addRecallBundleFactSlots(target map[string]struct{}, primary Note, related []Note, entities, facts []string) {
	addRecallSelectionFactSlots(target, primary, entities, facts)
	for _, note := range related {
		addRecallSelectionFactSlots(target, note, entities, facts)
	}
}

func addRecallSelectionFactSlots(target map[string]struct{}, note Note, entities, facts []string) {
	text := strings.ToLower(note.Subject + " " + note.Body)
	matchedEntity := false
	for _, entity := range entities {
		if !strings.Contains(strings.ToLower(note.Subject), entity) {
			continue
		}
		matchedEntity = true
		for _, fact := range facts {
			if recallFactMatched(note, text, fact) {
				target[entity+"\x00"+fact] = struct{}{}
			}
		}
	}
	if matchedEntity {
		return
	}
	for _, fact := range facts {
		if recallFactMatched(note, text, fact) {
			target["*\x00"+fact] = struct{}{}
		}
	}
}

func removeRecallCandidate(candidates []RecallCandidate, noteID string) []RecallCandidate {
	for index, candidate := range candidates {
		if candidate.ID == noteID {
			return append(candidates[:index], candidates[index+1:]...)
		}
	}
	return candidates
}
