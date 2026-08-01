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
// lengths keep the lexicographically-first quote's position.
//
// Pairwise containment is not the whole story: buildCitations resolves each
// CitationDraft.ExactText against the rendered "Source evidence" section
// markdown, which joins every surviving quote with a "\n\n" separator, and
// requires that text to occur exactly once (uniqueTextRange). Two quotes
// that are not substrings of one another in isolation can still spell out a
// third kept quote's full text once joined — the third quote's tail, the
// separator, and the fourth quote's head can read back as the third quote's
// own text. dropUntilExactlyOnce catches and resolves that after the
// pairwise sweep. It returns an error when no quote survives: a page with no
// citations cannot be curated, and the caller degrades to keep in that case.
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

	kept := make([]string, 0, len(quotes))
	for _, quote := range quotes {
		if !dropped[quote] {
			kept = append(kept, quote)
		}
	}

	kept, err := dropUntilExactlyOnce(kept)
	if err != nil {
		return nil, err
	}

	result := make([]carriedQuote, 0, len(kept))
	for _, quote := range kept {
		anchors := anchorsByQuote[quote]
		sort.Slice(anchors, func(i, j int) bool { return anchors[i].ID < anchors[j].ID })
		result = append(result, carriedQuote{exactText: quote, anchors: anchors})
	}
	return result, nil
}

// dropUntilExactlyOnce enforces the exactly-once invariant buildCitations
// requires: every quote's exact text must occur exactly once in the
// "\n\n"-joined section markdown. Joining two unrelated quotes can spell out
// a third quote's text across the separator even when no single quote
// contains another as a substring, so each iteration re-joins the current
// kept set, finds every quote whose count in the join is not exactly 1, and
// drops the shortest offender (least evidence lost; ties broken
// lexicographically ascending). Dropping one quote changes the join and can
// resolve or create other collisions, so this repeats to a fixed point; the
// kept set only shrinks, so it always terminates. quotes must already be in
// the sorted, pairwise-deduped order carriedEvidenceQuotes establishes; that
// order is preserved throughout. Returns an error if the set empties out.
func dropUntilExactlyOnce(quotes []string) ([]string, error) {
	kept := append([]string(nil), quotes...)
	for {
		if len(kept) == 0 {
			return nil, errors.New("build curation draft: no citation anchors survive dedup")
		}
		joined := strings.Join(kept, "\n\n")
		var offenders []string
		for _, quote := range kept {
			if strings.Count(joined, quote) != 1 {
				offenders = append(offenders, quote)
			}
		}
		if len(offenders) == 0 {
			return kept, nil
		}
		sort.Slice(offenders, func(i, j int) bool {
			if len(offenders[i]) != len(offenders[j]) {
				return len(offenders[i]) < len(offenders[j])
			}
			return offenders[i] < offenders[j]
		})
		kept = removeFirst(kept, offenders[0])
	}
}

// removeFirst returns quotes with the first occurrence of target removed.
func removeFirst(quotes []string, target string) []string {
	result := make([]string, 0, len(quotes)-1)
	removed := false
	for _, quote := range quotes {
		if !removed && quote == target {
			removed = true
			continue
		}
		result = append(result, quote)
	}
	return result
}
