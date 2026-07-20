package extractor

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

func mapExtractionSourceClauseV1(result *Result, slice sessionlake.Slice) {
	mapExtractionV2With(result, slice, mapSourceClauseDecision)
}

func mapSourceClauseDecision(
	decision StateDecision,
	claims map[string]Claim,
	allEvents map[string]struct{},
	newEvents map[string]struct{},
	events []teamnote.SessionEvent,
	slice sessionlake.Slice,
) (*teamnote.Candidate, string) {
	if reason := sourceClauseRejectionReason(decision, events, newEvents); reason != "" {
		return nil, reason
	}
	return mapDecision(decision, claims, allEvents, newEvents, events, extractionObservationTime(slice))
}

func sourceClauseRejectionReason(
	decision StateDecision,
	events []teamnote.SessionEvent,
	newEvents map[string]struct{},
) string {
	if !decisionChangesState(decision.Decision) {
		return ""
	}
	if len(decision.EvidenceClauses) == 0 {
		return "state-changing decision is missing a source clause citation"
	}
	eventsByID := make(map[string]teamnote.SessionEvent, len(events))
	for _, event := range events {
		eventsByID[event.ID] = event
	}
	decisionEvidence := stringSet(decision.EvidenceEventIDs)
	groundedInNewEvent := false
	for _, clause := range decision.EvidenceClauses {
		eventID := strings.TrimSpace(clause.EventID)
		quote := strings.TrimSpace(clause.Quote)
		if eventID == "" || quote == "" {
			return "source clause citation is missing event_id or quote"
		}
		if _, cited := decisionEvidence[eventID]; !cited {
			return fmt.Sprintf("source clause event %q is not cited by the decision", eventID)
		}
		event, exists := eventsByID[eventID]
		if !exists {
			return fmt.Sprintf("source clause cites unknown event %q", eventID)
		}
		if !strings.Contains(event.Content, quote) {
			return fmt.Sprintf("source clause quote is not exact text from event %q", eventID)
		}
		if !isAtomicSourceClause(event.Content, quote) {
			return fmt.Sprintf("source clause quote is not one atomic clause from event %q", eventID)
		}
		if sourceClauseIsNonCommittal(quote) {
			return fmt.Sprintf("source clause from event %q contains only non-committal language", eventID)
		}
		if _, isNew := newEvents[eventID]; isNew {
			groundedInNewEvent = true
		}
	}
	if !groundedInNewEvent {
		return "source clause citations are not grounded in a new event"
	}
	return ""
}

func sourceClauseIsNonCommittal(quote string) bool {
	normalized := " " + strings.ToLower(strings.Join(strings.Fields(quote), " ")) + " "
	if strings.Contains(normalized, "?") {
		return true
	}
	if containsAny(normalized, []string{
		" proposal was approved", " proposal is approved", " proposal was accepted",
		" proposal is accepted", " proposal has been approved", " proposal has been accepted",
		" request was approved", " request is approved", " request was accepted",
		" request is accepted", " request has been approved", " request has been accepted",
		" ask was approved", " ask is approved", " ask was accepted", " ask is accepted",
	}) {
		return false
	}
	nonCommittal := containsAny(normalized, []string{
		" i propose ", " we propose ", " proposal ", " suggest ", " recommend ",
		" should ", " please ", " can you ", " could ", " would ", " might ",
		" ask ", " asks ", " request ", " prefer ", " hope ",
	})
	return nonCommittal
}

func isAtomicSourceClause(content, quote string) bool {
	start := 0
	for index, character := range content {
		if !isSourceClauseBoundary(content, index, character) {
			continue
		}
		end := index + len(string(character))
		if strings.TrimSpace(content[start:end]) == quote {
			return true
		}
		start = end
	}
	return strings.TrimSpace(content[start:]) == quote
}

func isSourceClauseBoundary(content string, index int, character rune) bool {
	if character == ';' || character == '!' || character == '?' || character == '\n' || character == '。' || character == '！' || character == '？' {
		return true
	}
	if character != '.' {
		return false
	}
	nextIndex := index + utf8.RuneLen(character)
	if nextIndex < len(content) {
		next, _ := utf8.DecodeRuneInString(content[nextIndex:])
		if !unicode.IsSpace(next) {
			return false
		}
	}
	return !isSourceAbbreviation(content[:index])
}

func isSourceAbbreviation(prefix string) bool {
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	token := strings.ToLower(strings.Trim(fields[len(fields)-1], "()[]{}\"'"))
	switch token {
	case "mr", "mrs", "ms", "dr", "prof", "sr", "jr", "st", "vs", "e.g", "i.e":
		return true
	default:
		return false
	}
}

func containsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func extractionObservationTime(slice sessionlake.Slice) time.Time {
	newEvents := stringSet(slice.NewEventIDs)
	var observationTime time.Time
	for _, event := range slice.Events {
		if _, ok := newEvents[event.ID]; !ok || event.OccurredAt.IsZero() {
			continue
		}
		occurredAt := event.OccurredAt.UTC()
		if observationTime.IsZero() || occurredAt.After(observationTime) {
			observationTime = occurredAt
		}
	}
	return observationTime
}
