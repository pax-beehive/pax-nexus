package teamnote

import (
	"fmt"
	"regexp"
	"strings"
)

var protectedQualifierPattern = regexp.MustCompile(`(?i)\b(?:if|unless|until|only after|only when|except|pending|subject to|must|may|cannot|can't|not|never|no longer)\b[^.;\n]*`)

var protectedValuePattern = regexp.MustCompile(`(?i)\b(?:\d+(?:[./:-]\d+)*|january|february|march|april|may|june|july|august|september|october|november|december|monday|tuesday|wednesday|thursday|friday|saturday|sunday|today|tomorrow|eod)\b`)

var protectedRolePattern = regexp.MustCompile(`(?i)\b(?:[a-z0-9_-]+\s+owns?|owner\s+(?:is|:)\s*[a-z0-9_-]+|[a-z0-9_-]+\s+(?:is\s+)?(?:responsible|assigned|designated))\b`)

var explicitTransitionPattern = regexp.MustCompile(`(?i)\b(?:changed?|updated?|moved?|replaced?|instead|no longer|from|now|removed?|cancelled?|superseded?)\b`)

func validateRevisionQualifiers(
	previous, next string,
	candidate Candidate,
	evidence []SessionEvent,
	authority TransitionAuthority,
) error {
	if err := validateTransitionAuthority(candidate, evidence, authority); err != nil {
		return err
	}
	for _, clause := range protectedClauses(previous) {
		if containsNormalized(next, clause) || authorityReplacesClause(authority, clause) {
			continue
		}
		return fmt.Errorf("dropped qualifier %q: %w", clause, ErrDestructiveRevision)
	}
	return nil
}

func validateTransitionAuthority(candidate Candidate, evidence []SessionEvent, authority TransitionAuthority) error {
	events := make(map[string]SessionEvent, len(evidence))
	for _, event := range evidence {
		events[event.ID] = event
	}
	cited := make(map[string]struct{}, len(candidate.EvidenceEventIDs))
	for _, eventID := range candidate.EvidenceEventIDs {
		cited[eventID] = struct{}{}
	}
	for _, clause := range authority.EvidenceClauses {
		if strings.TrimSpace(clause.EventID) == "" || strings.TrimSpace(clause.Quote) == "" {
			return fmt.Errorf("transition authority clause: %w", ErrInvalidCandidate)
		}
		event, exists := events[clause.EventID]
		_, isCited := cited[clause.EventID]
		if !exists || !isCited || !strings.Contains(event.Content, clause.Quote) {
			return fmt.Errorf("transition authority clause %q is not exact cited evidence: %w", clause.EventID, ErrInvalidCandidate)
		}
	}
	return nil
}

func protectedClauses(body string) []string {
	protected := make([]string, 0)
	for _, pattern := range []*regexp.Regexp{protectedQualifierPattern, protectedValuePattern, protectedRolePattern} {
		for _, match := range pattern.FindAllString(body, -1) {
			normalized := normalizeRevisionText(match)
			if normalized != "" {
				protected = append(protected, normalized)
			}
		}
	}
	return mergeStrings(nil, protected)
}

func authorityReplacesClause(authority TransitionAuthority, protected string) bool {
	protectedIsValue := protectedValuePattern.MatchString(protected)
	protectedTerms := revisionAuthorityAnchors(protected)
	if len(protectedTerms) == 0 {
		return false
	}
	for _, clause := range authority.EvidenceClauses {
		normalized := normalizeRevisionText(clause.Quote)
		if !explicitTransitionPattern.MatchString(normalized) {
			continue
		}
		if protectedIsValue && strings.Contains(normalized, protected) {
			return true
		}
		matches := true
		for _, term := range protectedTerms {
			if !strings.Contains(" "+normalized+" ", " "+term+" ") {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func containsNormalized(body, fragment string) bool {
	return strings.Contains(normalizeRevisionText(body), fragment)
}

func normalizeRevisionText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func revisionAuthorityAnchors(value string) []string {
	terms := strings.FieldsFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	result := make([]string, 0, len(terms))
	for _, term := range terms {
		switch term {
		case "if", "unless", "until", "only", "when", "after", "before", "except", "pending", "subject", "to", "must", "may", "not", "never", "no", "longer", "is", "the", "a", "an":
			continue
		}
		result = append(result, term)
	}
	return result
}
