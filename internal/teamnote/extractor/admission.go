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
	normalizeSourceClauseCitations(result.Trace, slice.Events)
	mapExtractionV2With(result, slice, mapSourceClauseDecision)
}

func normalizeSourceClauseCitations(trace *TraceV2, events []teamnote.SessionEvent) {
	if trace == nil {
		return
	}
	eventsByID := make(map[string]teamnote.SessionEvent, len(events))
	for _, event := range events {
		eventsByID[event.ID] = event
	}
	for decisionIndex := range trace.StateDecisions {
		for clauseIndex := range trace.StateDecisions[decisionIndex].EvidenceClauses {
			clause := &trace.StateDecisions[decisionIndex].EvidenceClauses[clauseIndex]
			event, exists := eventsByID[clause.EventID]
			if !exists || strings.Contains(event.Content, clause.Quote) {
				continue
			}
			if exact, ok := exactSourceSpanIgnoringMarkdown(event.Content, clause.Quote); ok {
				clause.Quote = exact
			}
		}
	}
}

func exactSourceSpanIgnoringMarkdown(content, quote string) (string, bool) {
	formatting := markdownFormattingPositions(content)
	normalizedContent, sourceOffsets := sourceTextWithoutMarkdownFormatting(content)
	normalizedQuote, _ := sourceTextWithoutMarkdownFormatting(quote)
	if normalizedQuote == "" || len(sourceOffsets) == 0 {
		return "", false
	}
	start := strings.Index(normalizedContent, normalizedQuote)
	if start < 0 || strings.Contains(normalizedContent[start+1:], normalizedQuote) {
		return "", false
	}
	sourceStart := sourceOffsets[start]
	sourceEnd := sourceOffsets[start+len(normalizedQuote)-1] + 1
	for sourceStart > 0 && formatting[sourceStart-1] {
		sourceStart--
	}
	for sourceEnd < len(content) && formatting[sourceEnd] {
		sourceEnd++
	}
	return content[sourceStart:sourceEnd], true
}

func sourceTextWithoutMarkdownFormatting(content string) (string, []int) {
	formatting := markdownFormattingPositions(content)
	text := make([]byte, 0, len(content))
	offsets := make([]int, 0, len(content))
	for index := 0; index < len(content); index++ {
		if formatting[index] {
			continue
		}
		text = append(text, content[index])
		offsets = append(offsets, index)
	}
	return string(text), offsets
}

func markdownFormattingPositions(content string) []bool {
	positions := make([]bool, len(content))
	for _, delimiter := range []string{"**", "__", "`", "*", "_"} {
		markMarkdownDelimiterPairs(content, delimiter, positions)
	}
	return positions
}

func markMarkdownDelimiterPairs(content, delimiter string, positions []bool) {
	searchFrom := 0
	for searchFrom < len(content) {
		opening := nextMarkdownDelimiter(content, delimiter, positions, searchFrom, true)
		if opening < 0 {
			return
		}
		closing := nextMarkdownDelimiter(content, delimiter, positions, opening+len(delimiter), false)
		if closing < 0 {
			return
		}
		for index := 0; index < len(delimiter); index++ {
			positions[opening+index] = true
			positions[closing+index] = true
		}
		searchFrom = closing + len(delimiter)
	}
}

func nextMarkdownDelimiter(
	content, delimiter string,
	positions []bool,
	searchFrom int,
	opening bool,
) int {
	for searchFrom <= len(content)-len(delimiter) {
		relative := strings.Index(content[searchFrom:], delimiter)
		if relative < 0 {
			return -1
		}
		index := searchFrom + relative
		if !markdownDelimiterAlreadyMarked(positions, index, len(delimiter)) &&
			markdownDelimiterHasBoundary(content, index, delimiter, opening) {
			return index
		}
		searchFrom = index + 1
	}
	return -1
}

func markdownDelimiterAlreadyMarked(positions []bool, start, length int) bool {
	for index := start; index < start+length; index++ {
		if positions[index] {
			return true
		}
	}
	return false
}

func markdownDelimiterHasBoundary(content string, index int, delimiter string, opening bool) bool {
	if delimiter == "`" {
		return true
	}
	if opening {
		after := index + len(delimiter)
		if after >= len(content) {
			return false
		}
		next, _ := utf8.DecodeRuneInString(content[after:])
		return !unicode.IsSpace(next) && markdownBoundaryBefore(content, index)
	}
	if index == 0 {
		return false
	}
	previous, _ := utf8.DecodeLastRuneInString(content[:index])
	return !unicode.IsSpace(previous) && markdownBoundaryAfter(content, index+len(delimiter))
}

func markdownBoundaryBefore(content string, index int) bool {
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(content[:index])
	return unicode.IsSpace(previous) || unicode.IsPunct(previous)
}

func markdownBoundaryAfter(content string, index int) bool {
	if index == len(content) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(content[index:])
	return unicode.IsSpace(next) || unicode.IsPunct(next)
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
	return mapDecision(decision, claims, allEvents, newEvents, events, extractionObservationTime(slice), true)
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
		eventID := clause.EventID
		quote := clause.Quote
		if eventID == "" || quote == "" {
			return "source clause citation is missing event_id or quote"
		}
		if eventID != strings.TrimSpace(eventID) || quote != strings.TrimSpace(quote) {
			return "source clause citation contains surrounding whitespace"
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
		" should ", " please ", " can you ", " can we ", " could ", " would ", " might ",
		" i'd want ", " i’d want ", " i'd like ", " i’d like ",
		" we'd want ", " we’d want ", " we'd like ", " we’d like ",
		" ask ", " asks ", " request ", " prefer ", " hope ",
	})
	return nonCommittal
}

func isAtomicSourceClause(content, quote string) bool {
	if hasInternalSourceClauseBoundary(quote) {
		return false
	}
	searchFrom := 0
	for searchFrom <= len(content)-len(quote) {
		relative := strings.Index(content[searchFrom:], quote)
		if relative < 0 {
			return false
		}
		start := searchFrom + relative
		end := start + len(quote)
		if sourceClauseBoundaryBefore(content, start) && sourceClauseBoundaryAfter(content, end) {
			return true
		}
		searchFrom = start + 1
	}
	return false
}

func hasInternalSourceClauseBoundary(quote string) bool {
	trimmed := strings.TrimSpace(quote)
	for index, character := range trimmed {
		if !isSourceClauseBoundary(trimmed, index, character) {
			continue
		}
		if index+utf8.RuneLen(character) < len(trimmed) {
			return true
		}
	}
	return false
}

func sourceClauseBoundaryBefore(content string, index int) bool {
	formatting := markdownFormattingPositions(content)
	prefixEnd := index
	for prefixEnd > 0 {
		last, size := utf8.DecodeLastRuneInString(content[:prefixEnd])
		if !unicode.IsSpace(last) && (size != 1 || !formatting[prefixEnd-1]) {
			break
		}
		prefixEnd -= size
	}
	if prefixEnd == 0 {
		return true
	}
	last, size := utf8.DecodeLastRuneInString(content[:prefixEnd])
	if isSourceClauseBoundary(content, prefixEnd-size, last) {
		return true
	}
	if isInlineSourceClauseDelimiter(last) {
		return true
	}
	prefix := content[:prefixEnd]
	return endsWithSourceConjunction(strings.ToLower(prefix))
}

func sourceClauseBoundaryAfter(content string, index int) bool {
	formatting := markdownFormattingPositions(content)
	if index > 0 {
		last, size := utf8.DecodeLastRuneInString(content[:index])
		if isSourceClauseBoundary(content, index-size, last) {
			return true
		}
	}
	suffixStart := index
	for suffixStart < len(content) {
		first, size := utf8.DecodeRuneInString(content[suffixStart:])
		if !unicode.IsSpace(first) && (size != 1 || !formatting[suffixStart]) {
			break
		}
		suffixStart += size
	}
	if suffixStart == len(content) {
		return true
	}
	first, _ := utf8.DecodeRuneInString(content[suffixStart:])
	if isSourceClauseBoundary(content, suffixStart, first) {
		return true
	}
	if isInlineSourceClauseDelimiter(first) {
		return true
	}
	suffix := content[suffixStart:]
	return startsWithSourceConjunction(strings.ToLower(suffix))
}

func isInlineSourceClauseDelimiter(character rune) bool {
	return character == ',' || character == ':' || character == '，' || character == '：'
}

func endsWithSourceConjunction(value string) bool {
	for _, conjunction := range []string{" and", " but", " while", " however"} {
		if strings.HasSuffix(value, conjunction) {
			return true
		}
	}
	return false
}

func startsWithSourceConjunction(value string) bool {
	for _, conjunction := range []string{"and ", "but ", "while ", "however "} {
		if strings.HasPrefix(value, conjunction) {
			return true
		}
	}
	return false
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
