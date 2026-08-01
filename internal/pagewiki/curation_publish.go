package pagewiki

import (
	"errors"
	"sort"
	"strings"
)

// buildCurationDraft assembles the deterministic PageDraft for a curation
// rewrite or merge: normalized LLM sections + a rebuilt "Source evidence"
// section carrying forward the union of anchors + a rebuilt "Related
// knowledge" section from outgoing link targets.
//
// Unlike the fresh-page LLM session editor, a curation draft's evidence is
// not resolved against a single source revision: it is the union of every
// citation carried by the pages being rewritten or merged, so each
// CitationDraft carries its SourceAnchor rows forward verbatim via Anchors
// rather than re-resolving Evidence.
func buildCurationDraft(
	slug string,
	draft CurationDraft,
	carried []PageCitation,
	related []RelatedPage,
) (PageDraft, error) {
	title := strings.TrimSpace(draft.Title)
	summary := strings.TrimSpace(draft.Summary)
	if title == "" || summary == "" || len(draft.Sections) == 0 {
		return PageDraft{}, errors.New("build curation draft: title, summary, and at least one section are required")
	}

	sections := make([]SectionDraft, 0, len(draft.Sections)+2)
	seenKeys := make(map[string]int, len(draft.Sections))
	for _, section := range draft.Sections {
		key := uniqueLLMSectionKey(section.Key, section.Heading, seenKeys)
		sections = append(sections, SectionDraft{
			Key: key, Heading: section.Heading, Markdown: section.Markdown,
		})
	}

	quotes, err := carriedEvidenceQuotes(carried)
	if err != nil {
		return PageDraft{}, err
	}

	evidenceMarkdown := make([]string, 0, len(quotes))
	citations := make([]CitationDraft, 0, len(quotes))
	for _, quote := range quotes {
		evidenceMarkdown = append(evidenceMarkdown, quote.exactText)
		citations = append(citations, CitationDraft{
			SectionKey: "source-evidence",
			ExactText:  quote.exactText,
			Anchors:    quote.anchors,
		})
	}
	sections = append(sections, SectionDraft{
		Key: "source-evidence", Heading: "Source evidence",
		Markdown: strings.Join(evidenceMarkdown, "\n\n"),
	})

	links := make([]LinkDraft, 0, len(related))
	if section, linkDrafts, ok := relatedKnowledgeSection(related); ok {
		sections = append(sections, section)
		links = append(links, linkDrafts...)
	}

	return PageDraft{
		Slug: slug, Title: title, Summary: summary,
		Sections: sections, Citations: citations, Links: links,
	}, nil
}

// carriedQuote is a surviving evidence quote and the union of anchors that
// support it, sorted for deterministic output.
type carriedQuote struct {
	exactText string
	anchors   []SourceAnchor
}

// carriedEvidenceQuotes flattens every PageCitation.SourceAnchors from
// carried, dedupes anchors by ID, groups the survivors by ExactQuote, and
// drops any quote that is a substring of another kept quote — the same
// longest-first overlap rule relatedKnowledgeSection applies to titles.
// Quotes are sorted descending by length then lexicographically, both to
// make the overlap sweep deterministic and to fix the output order; equal
// lengths keep the lexicographically-first quote's position. It returns an
// error when no quote survives: a page with no citations cannot be curated,
// and the caller degrades to keep in that case.
func carriedEvidenceQuotes(carried []PageCitation) ([]carriedQuote, error) {
	anchorsByID := make(map[string]SourceAnchor)
	for _, citation := range carried {
		for _, anchor := range citation.SourceAnchors {
			anchorsByID[anchor.ID] = anchor
		}
	}

	anchorsByQuote := make(map[string][]SourceAnchor, len(anchorsByID))
	for _, anchor := range anchorsByID {
		anchorsByQuote[anchor.ExactQuote] = append(anchorsByQuote[anchor.ExactQuote], anchor)
	}

	quotes := make([]string, 0, len(anchorsByQuote))
	for quote := range anchorsByQuote {
		quotes = append(quotes, quote)
	}
	sort.Slice(quotes, func(i, j int) bool {
		if len(quotes[i]) != len(quotes[j]) {
			return len(quotes[i]) > len(quotes[j])
		}
		return quotes[i] < quotes[j]
	})

	dropped := make(map[string]bool, len(quotes))
	for i, quote := range quotes {
		if dropped[quote] {
			continue
		}
		for _, shorter := range quotes[i+1:] {
			if strings.Contains(quote, shorter) {
				dropped[shorter] = true
			}
		}
	}

	result := make([]carriedQuote, 0, len(quotes))
	for _, quote := range quotes {
		if dropped[quote] {
			continue
		}
		anchors := anchorsByQuote[quote]
		sort.Slice(anchors, func(i, j int) bool { return anchors[i].ID < anchors[j].ID })
		result = append(result, carriedQuote{exactText: quote, anchors: anchors})
	}
	if len(result) == 0 {
		return nil, errors.New("build curation draft: no citation anchors survive dedup")
	}
	return result, nil
}
