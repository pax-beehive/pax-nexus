package pagewiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	nonSlugCharacter = regexp.MustCompile(`[^a-z0-9]+`)
	eventPrefix      = regexp.MustCompile(`^\[event:[^\]]+\]\s*`)
)

type knowledgeUnit struct {
	key       string
	slug      string
	title     string
	topicPath []string
	eventIDs  []string
	quotes    []string
}

// SessionDocumentPlanner is the deterministic knowledge-oriented fallback used
// when an external Wiki maintainer is unavailable. Sessions remain provenance;
// navigation is built from durable reader topics, never Session identifiers.
type SessionDocumentPlanner struct{}

func (SessionDocumentPlanner) Plan(_ context.Context, input PlanInput) ([]PageBrief, error) {
	units := sessionKnowledgeUnits(input.SourceRevision)
	briefs := make([]PageBrief, 0, len(units))
	for _, unit := range units {
		evidence := make([]EvidenceQuoteDraft, 0, len(unit.quotes))
		for index, quote := range unit.quotes {
			evidence = append(evidence, EvidenceQuoteDraft{
				EventID: unit.eventIDs[index], ExactText: quote,
			})
		}
		brief := PageBrief{
			Key: unit.key, Action: PageActionCreate,
			ProposedSlug: unit.slug, ProposedTitle: unit.title,
			ReaderGoal:       "Understand the durable knowledge supported by Session Lake evidence.",
			TopicPath:        unit.topicPath,
			EvidenceEventIDs: uniqueStrings(unit.eventIDs),
			Evidence:         evidence,
		}
		for _, page := range input.PageCatalog {
			if page.Slug != unit.slug {
				continue
			}
			brief.Action = PageActionUpdate
			brief.TargetPageID = page.ID
			brief.ExpectedBaseRevisionID = page.CurrentRevisionID
			brief.ProposedSlug = ""
			brief.ProposedTitle = ""
			brief.TopicPath = nil
			break
		}
		briefs = append(briefs, brief)
	}
	addRelatedKnowledgeLinks(input.SourceRevision.ID, units, briefs)
	if len(briefs) == 0 {
		eventIDs := make([]string, 0, len(input.SourceRevision.Events))
		for _, event := range input.SourceRevision.Events {
			eventIDs = append(eventIDs, event.ID)
		}
		return []PageBrief{{
			Key: "source-only", Action: PageActionSourceOnly, EvidenceEventIDs: eventIDs,
		}}, nil
	}
	return briefs, nil
}

type SessionDocumentEditor struct{}

func (SessionDocumentEditor) Edit(_ context.Context, input EditInput) (PageDraft, error) {
	var selected *knowledgeUnit
	for _, unit := range sessionKnowledgeUnits(input.SourceRevision) {
		if unit.key == input.Brief.Key {
			copy := unit
			selected = &copy
			break
		}
	}
	if selected == nil {
		return PageDraft{}, fmt.Errorf("edit knowledge page: brief %q is unavailable", input.Brief.Key)
	}
	slug := selected.slug
	title := selected.title
	if input.CurrentPage != nil {
		slug = input.CurrentPage.Slug
		title = input.CurrentPage.Title
	}
	markdown := strings.Join(selected.quotes, "\n\n")
	sections := []SectionDraft{{
		Key: "current-knowledge", Heading: "Current knowledge", Markdown: markdown,
	}}
	links := make([]LinkDraft, 0, len(input.Brief.RelatedPages))
	if len(input.Brief.RelatedPages) > 0 {
		related := make([]string, 0, len(input.Brief.RelatedPages))
		for _, page := range input.Brief.RelatedPages {
			related = append(related, page.Title)
			links = append(links, LinkDraft{
				SectionKey:   "related-knowledge",
				ExactText:    page.Title,
				TargetPageID: page.ID,
			})
		}
		sections = append(sections, SectionDraft{
			Key: "related-knowledge", Heading: "Related knowledge",
			Markdown: "See also: " + strings.Join(related, "; ") + ".",
		})
	}
	citations := make([]CitationDraft, 0, len(selected.quotes))
	for index, quote := range selected.quotes {
		citations = append(citations, CitationDraft{
			SectionKey: "current-knowledge", ExactText: quote,
			Evidence: []EvidenceQuoteDraft{{
				EventID: selected.eventIDs[index], ExactText: quote,
			}},
		})
	}
	return PageDraft{
		Slug: slug, Title: title,
		Summary: fmt.Sprintf(
			"Maintained from %d grounded Session Lake evidence item(s).",
			len(selected.quotes),
		),
		Sections:  sections,
		Citations: citations,
		Links:     links,
	}, nil
}

func addRelatedKnowledgeLinks(
	sourceRevisionID string,
	units []knowledgeUnit,
	briefs []PageBrief,
) {
	for index := range briefs {
		for candidate := index - 1; candidate >= 0; candidate-- {
			if !sameTopicPath(units[index].topicPath, units[candidate].topicPath) {
				continue
			}
			targetID := briefs[candidate].TargetPageID
			targetTitle := units[candidate].title
			if briefs[candidate].Action == PageActionCreate {
				targetID = stableID("page", sourceRevisionID, briefs[candidate].Key)
			}
			if targetID != "" && targetTitle != "" {
				briefs[index].RelatedPages = []RelatedPage{{
					ID: targetID, Title: targetTitle,
				}}
			}
			break
		}
	}
}

func sameTopicPath(left, right []string) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sessionKnowledgeUnits(revision SourceRevision) []knowledgeUnit {
	bySlug := make(map[string]int)
	units := make([]knowledgeUnit, 0, 8)
	for _, event := range revision.Events {
		if len(units) >= 8 {
			break
		}
		content := strings.TrimSpace(string(revision.Raw[event.StartByte:event.EndByte]))
		content = strings.TrimSpace(eventPrefix.ReplaceAllString(content, ""))
		for _, candidate := range knowledgeCandidates(content) {
			if len(units) >= 8 {
				break
			}
			quote := strings.TrimSpace(candidate.text)
			if quote == "" {
				continue
			}
			title := knowledgeTitle(candidate.title, quote)
			slug := "knowledge-" + slugValue(title, quote)
			if position, found := bySlug[slug]; found {
				if containsString(units[position].quotes, quote) {
					continue
				}
				units[position].eventIDs = append(units[position].eventIDs, event.ID)
				units[position].quotes = append(units[position].quotes, quote)
				continue
			}
			bySlug[slug] = len(units)
			units = append(units, knowledgeUnit{
				key: slug, slug: slug, title: title, topicPath: knowledgeTopic(title + " " + quote),
				eventIDs: []string{event.ID}, quotes: []string{quote},
			})
		}
	}
	return units
}

type knowledgeCandidate struct {
	title string
	text  string
}

func knowledgeCandidates(content string) []knowledgeCandidate {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	result := make([]knowledgeCandidate, 0)
	var heading string
	var body []string
	flush := func() {
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if heading != "" && text != "" {
			result = append(result, knowledgeCandidate{title: heading, text: text})
		}
		body = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			heading = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if heading != "" {
			body = append(body, line)
		}
	}
	flush()
	if len(result) > 0 {
		return result
	}
	if label, value, ok := labeledKnowledge(content); ok {
		return []knowledgeCandidate{{title: label, text: value}}
	}
	return []knowledgeCandidate{{text: content}}
}

func labeledKnowledge(content string) (string, string, bool) {
	for _, separator := range []string{"：", ": "} {
		position := strings.Index(content, separator)
		if position <= 0 || position > 60 || strings.Contains(content[:position], "\n") {
			continue
		}
		label := strings.TrimSpace(content[:position])
		value := strings.TrimSpace(content[position+len(separator):])
		if label != "" && value != "" {
			return label, value, true
		}
	}
	return "", "", false
}

func knowledgeTitle(candidate, content string) string {
	value := strings.ToLower(candidate + " " + content)
	switch {
	case containsAny(value, "retrieval", "search", "检索", "召回", "一到三跳"):
		return "LLM Wiki Retrieval"
	case containsAny(value, "source anchor", "source 原文", "citation", "引用区间", "evidence"):
		return "Evidence Grounding"
	case containsAny(value, "wiki data", "wiki 数据", "数据建模", "page revision", "revisioned tree"):
		return "Wiki Data Model"
	case containsAny(value, "experiment", "实验", "coal", "briquette", "煤球"):
		return "White Briquette Experiment"
	}
	if title := strings.TrimSpace(candidate); title != "" {
		return truncateRunes(title, 72)
	}
	line := strings.TrimSpace(strings.Split(content, "\n")[0])
	for _, separator := range []string{"。", ".", "！", "!", "？", "?"} {
		if position := strings.Index(line, separator); position > 0 {
			line = line[:position]
			break
		}
	}
	if line == "" {
		return "Untitled knowledge"
	}
	return truncateRunes(line, 72)
}

func knowledgeTopic(content string) []string {
	value := strings.ToLower(content)
	switch {
	case containsAny(value, "retrieval", "search", "检索", "召回"):
		return []string{"Engineering", "Retrieval"}
	case containsAny(value, "wiki", "source anchor", "citation", "evidence", "引用"):
		return []string{"Engineering", "Wiki Architecture"}
	case containsAny(value, "experiment", "实验", "coal", "briquette", "煤球"):
		return []string{"Research", "Experiments"}
	case containsAny(value, "runtime", "deploy", "release", "运行", "发布"):
		return []string{"Engineering", "Runtime"}
	case containsAny(value, "decision", "决策", "approved", "决定"):
		return []string{"Product", "Decisions"}
	default:
		return []string{"Knowledge", "Concepts"}
	}
}

func slugValue(title, content string) string {
	base := strings.Trim(nonSlugCharacter.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if base != "" {
		return base
	}
	sum := sha256.Sum256([]byte(content))
	return "item-" + hex.EncodeToString(sum[:4])
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !containsString(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

var _ Planner = SessionDocumentPlanner{}
var _ Editor = SessionDocumentEditor{}
