package pagewiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

const editorEvidenceContextBytes = 8 << 10

var llmSectionKeyCharacters = regexp.MustCompile(`[^a-z0-9]+`)

type LLMEditorConfig struct {
	Client llm.ChatClient
	Model  string
}

// LLMSessionEditor writes reader-facing English prose while deterministic
// PageWiki code retains control of routing, evidence, links, and publication.
type LLMSessionEditor struct {
	client llm.ChatClient
	model  string
}

func NewLLMSessionEditor(config LLMEditorConfig) (*LLMSessionEditor, error) {
	if config.Client == nil {
		return nil, errors.New("create Page Wiki LLM editor: client is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("create Page Wiki LLM editor: model is required")
	}
	return &LLMSessionEditor{client: config.Client, model: strings.TrimSpace(config.Model)}, nil
}

func (e *LLMSessionEditor) Edit(ctx context.Context, input EditInput) (PageDraft, error) {
	topic := strings.TrimSpace(input.Brief.ProposedTitle)
	if input.CurrentPage != nil {
		topic = input.CurrentPage.Title
	}
	quotes := make([]string, 0, len(input.Brief.Evidence))
	for _, evidence := range input.Brief.Evidence {
		quotes = append(quotes, evidence.ExactText)
	}
	contentByEvent := make(map[string]string, len(input.SourceRevision.Events))
	for _, event := range input.SourceRevision.Events {
		contentByEvent[event.ID] = string(input.SourceRevision.Raw[event.StartByte:event.EndByte])
	}
	evidenceContext := make([]string, 0, len(input.Brief.EvidenceEventIDs))
	for _, eventID := range input.Brief.EvidenceEventIDs {
		content, found := contentByEvent[eventID]
		if !found {
			continue
		}
		if len(content) > editorEvidenceContextBytes {
			content = content[:editorEvidenceContextBytes]
		}
		evidenceContext = append(evidenceContext, content)
	}
	payload, err := json.Marshal(llmEditRequest{
		Topic:           topic,
		ReaderGoal:      input.Brief.ReaderGoal,
		CurrentTitle:    currentTitle(input),
		CurrentText:     currentText(input),
		Evidence:        quotes,
		EvidenceContext: evidenceContext,
	})
	if err != nil {
		return PageDraft{}, fmt.Errorf("encode Page Wiki LLM request: %w", err)
	}
	response, err := e.client.Complete(ctx, llm.ChatRequest{
		Model: e.model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: pageWikiEnglishEditorPrompt + generationDirectivesPrompt(input.Directives)},
			{Role: "user", Content: string(payload)},
		},
	})
	if err != nil {
		return PageDraft{}, fmt.Errorf("write Page Wiki with LLM: %w", err)
	}
	var generated llmEditResponse
	if err := json.Unmarshal([]byte(trimJSONFence(response.Message.Content)), &generated); err != nil {
		return PageDraft{}, fmt.Errorf("decode Page Wiki LLM response: %w", err)
	}
	return generatedDraft(input, generated)
}

type llmEditRequest struct {
	Topic           string   `json:"topic"`
	ReaderGoal      string   `json:"reader_goal"`
	CurrentTitle    string   `json:"current_title,omitempty"`
	CurrentText     string   `json:"current_text,omitempty"`
	Evidence        []string `json:"evidence"`
	EvidenceContext []string `json:"evidence_context,omitempty"`
}

type llmEditResponse struct {
	Title    string               `json:"title"`
	Summary  string               `json:"summary"`
	Sections []llmSectionResponse `json:"sections"`
}

type llmSectionResponse struct {
	Key      string `json:"key"`
	Heading  string `json:"heading"`
	Markdown string `json:"markdown"`
}

func generatedDraft(
	input EditInput,
	generated llmEditResponse,
) (PageDraft, error) {
	title := strings.TrimSpace(generated.Title)
	summary := strings.TrimSpace(generated.Summary)
	if title == "" || summary == "" || len(generated.Sections) == 0 {
		return PageDraft{}, errors.New("decode Page Wiki LLM response: title, summary, and sections are required")
	}
	slug := input.Brief.ProposedSlug
	if input.CurrentPage != nil {
		slug = input.CurrentPage.Slug
	}
	sections := make([]SectionDraft, 0, len(generated.Sections)+2)
	seenKeys := make(map[string]int)
	for _, section := range generated.Sections {
		heading := strings.TrimSpace(section.Heading)
		markdown := strings.TrimSpace(section.Markdown)
		if heading == "" || markdown == "" {
			return PageDraft{}, errors.New("decode Page Wiki LLM response: section heading and markdown are required")
		}
		key := uniqueLLMSectionKey(section.Key, heading, seenKeys)
		sections = append(sections, SectionDraft{Key: key, Heading: heading, Markdown: markdown})
	}
	evidenceMarkdown := make([]string, 0, len(input.Brief.Evidence))
	citations := make([]CitationDraft, 0, len(input.Brief.Evidence))
	for _, evidence := range input.Brief.Evidence {
		evidenceMarkdown = append(evidenceMarkdown, evidence.ExactText)
		citations = append(citations, CitationDraft{
			SectionKey: "source-evidence",
			ExactText:  evidence.ExactText,
			Evidence: []EvidenceQuoteDraft{{
				EventID: evidence.EventID, ExactText: evidence.ExactText,
			}},
		})
	}
	sections = append(sections, SectionDraft{
		Key: "source-evidence", Heading: "Source evidence",
		Markdown: strings.Join(evidenceMarkdown, "\n\n"),
	})
	links := make([]LinkDraft, 0, len(input.Brief.RelatedPages))
	if section, linkDrafts, ok := relatedKnowledgeSection(input.Brief.RelatedPages); ok {
		sections = append(sections, section)
		links = append(links, linkDrafts...)
	}
	return PageDraft{
		Slug: slug, Title: title, Summary: summary,
		Sections: sections, Citations: citations, Links: links,
	}, nil
}

// relatedKnowledgeSection renders the deterministic "Related knowledge"
// section and its Xanadu link drafts. Drafts are deduplicated by target page,
// and titles that would overlap inside the section text are dropped: the
// publisher requires every link's exact text to appear in its section exactly
// once, so a shorter title contained in a longer one must never survive.
func relatedKnowledgeSection(related []RelatedPage) (SectionDraft, []LinkDraft, bool) {
	unique := make([]RelatedPage, 0, len(related))
	for _, page := range related {
		if page.ID == "" || strings.TrimSpace(page.Title) == "" {
			continue
		}
		duplicate := false
		for _, kept := range unique {
			if kept.ID == page.ID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			unique = append(unique, page)
		}
	}
	if len(unique) == 0 {
		return SectionDraft{}, nil, false
	}
	// Longest title wins every overlap; equal titles keep the planner's order.
	longestFirst := make([]RelatedPage, len(unique))
	copy(longestFirst, unique)
	sort.SliceStable(longestFirst, func(i, j int) bool {
		return len(longestFirst[i].Title) > len(longestFirst[j].Title)
	})
	dropped := make(map[string]bool, len(unique))
	for index, page := range longestFirst {
		if dropped[page.ID] {
			continue
		}
		for _, shorter := range longestFirst[index+1:] {
			if strings.Contains(page.Title, shorter.Title) {
				dropped[shorter.ID] = true
			}
		}
	}
	kept := make([]RelatedPage, 0, len(unique))
	for _, page := range unique {
		if !dropped[page.ID] {
			kept = append(kept, page)
		}
	}
	if len(kept) == 0 {
		return SectionDraft{}, nil, false
	}
	titles := make([]string, 0, len(kept))
	links := make([]LinkDraft, 0, len(kept))
	for _, page := range kept {
		titles = append(titles, page.Title)
		links = append(links, LinkDraft{
			SectionKey: "related-knowledge", ExactText: page.Title, TargetPageID: page.ID,
		})
	}
	return SectionDraft{
		Key: "related-knowledge", Heading: "Related knowledge",
		Markdown: "See also: " + strings.Join(titles, "; ") + ".",
	}, links, true
}

func currentTitle(input EditInput) string {
	if input.CurrentPage == nil {
		return ""
	}
	return input.CurrentPage.Title
}

func currentText(input EditInput) string {
	if input.CurrentRevision == nil {
		return ""
	}
	return input.CurrentRevision.Markdown
}

func uniqueLLMSectionKey(candidate, heading string, seen map[string]int) string {
	key := strings.Trim(llmSectionKeyCharacters.ReplaceAllString(
		strings.ToLower(strings.TrimSpace(candidate)), "-",
	), "-")
	if key == "" {
		key = strings.Trim(llmSectionKeyCharacters.ReplaceAllString(
			strings.ToLower(heading), "-",
		), "-")
	}
	if key == "" {
		key = "section"
	}
	seen[key]++
	if seen[key] > 1 {
		return fmt.Sprintf("%s-%d", key, seen[key])
	}
	return key
}

func trimJSONFence(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) >= 3 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		return strings.Join(lines[1:len(lines)-1], "\n")
	}
	return trimmed
}

const pageWikiEnglishEditorPrompt = `You are the senior editor of a durable, evidence-backed Wiki.
Return exactly one JSON object with this shape and no Markdown fence:
{"title":"English title","summary":"English summary","sections":[{"key":"stable-kebab-case","heading":"English heading","markdown":"English prose"}]}

Write every generated title, summary, heading, and prose sentence in English,
regardless of the language of the evidence. Preserve proper nouns accurately.
The title is a concise noun phrase naming the page's concept — at most five words,
never a sentence, no trailing period; when current_text already carries such a
title, keep it rather than rewriting it. Each section heading is likewise a short
noun phrase, not a sentence.
evidence lists the exact quotes that will be cited; evidence_context carries
the full source material they come from. Ground every claim in that
material: preserve attribution, negation, scope, uncertainty, and
chronology. Never invent a fact, decision, owner, status, date, outcome, or
relationship. Write a complete, self-contained article, not a Session
transcript or a list of chat turns: open with a substantive lead, then two
to six sections organized by reader meaning. Expand every point the
material supports — background, rationale, consequences, current state —
and stop where the evidence stops instead of padding. If current_text is
present, retain still-supported durable knowledge while updating it from
the supplied evidence. Do not include source quotations or a related-pages
section; the runtime appends exact evidence and audited Xanadu links
deterministically. Return JSON only.`

var _ Editor = (*LLMSessionEditor)(nil)
