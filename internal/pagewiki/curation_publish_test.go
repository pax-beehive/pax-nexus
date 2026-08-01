package pagewiki

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGivenValidCurationInputsWhenDraftIsAssembledThenSectionsAndCitationsAreProduced(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Deploy Pipeline",
		Summary: "How the deploy pipeline works.",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Some background."},
		},
	}
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{
			{ID: "anchor-beta", SourceRevisionID: "rev-1", EventID: "evt-1", ExactQuote: "Quote Beta"},
		}},
		{SourceAnchors: []SourceAnchor{
			{ID: "anchor-alpha", SourceRevisionID: "rev-2", EventID: "evt-2", ExactQuote: "Quote Alpha"},
		}},
	}

	result, err := buildCurationDraft("deploy-pipeline", draft, carried, nil)
	require.NoError(t, err)

	assert.Equal(t, "deploy-pipeline", result.Slug)
	assert.Equal(t, "Deploy Pipeline", result.Title)
	assert.Equal(t, "How the deploy pipeline works.", result.Summary)

	require.Len(t, result.Sections, 2)
	assert.Equal(t, SectionDraft{Key: "background", Heading: "Background", Markdown: "Some background."}, result.Sections[0])
	// "Quote Alpha" and "Quote Beta" are equal length, so lexicographic order
	// breaks the tie: Alpha sorts before Beta.
	assert.Equal(t, SectionDraft{
		Key: "source-evidence", Heading: "Source evidence",
		Markdown: "Quote Alpha\n\nQuote Beta",
	}, result.Sections[1])

	require.Len(t, result.Citations, 2)
	assert.Equal(t, CitationDraft{
		SectionKey: "source-evidence", ExactText: "Quote Alpha",
		Anchors: []SourceAnchor{{ID: "anchor-alpha", SourceRevisionID: "rev-2", EventID: "evt-2", ExactQuote: "Quote Alpha"}},
	}, result.Citations[0])
	assert.Equal(t, CitationDraft{
		SectionKey: "source-evidence", ExactText: "Quote Beta",
		Anchors: []SourceAnchor{{ID: "anchor-beta", SourceRevisionID: "rev-1", EventID: "evt-1", ExactQuote: "Quote Beta"}},
	}, result.Citations[1])
	assert.Empty(t, result.Links)
}

func TestGivenDuplicateSectionKeysWhenDraftIsAssembledThenKeysAreUniquified(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "First."},
			{Key: "background", Heading: "Background", Markdown: "Second."},
		},
	}
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{{ID: "a1", ExactQuote: "Some quote"}}},
	}

	result, err := buildCurationDraft("slug", draft, carried, nil)
	require.NoError(t, err)

	require.Len(t, result.Sections, 3)
	assert.Equal(t, "background", result.Sections[0].Key)
	assert.Equal(t, "background-2", result.Sections[1].Key)
}

func TestGivenAnchorUnionAcrossTwoCitationsWhenDraftIsAssembledThenDuplicateAnchorIDIsDedupedByID(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}
	// Both source revisions carried the same anchor, plus each contributes a
	// distinct anchor over the same quote text.
	shared := SourceAnchor{ID: "anchor-shared", SourceRevisionID: "rev-1", EventID: "evt-1", ExactQuote: "Shared quote"}
	extra := SourceAnchor{ID: "anchor-extra", SourceRevisionID: "rev-2", EventID: "evt-2", ExactQuote: "Shared quote"}
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{shared}},
		{SourceAnchors: []SourceAnchor{shared, extra}},
	}

	result, err := buildCurationDraft("slug", draft, carried, nil)
	require.NoError(t, err)

	require.Len(t, result.Citations, 1)
	citation := result.Citations[0]
	assert.Equal(t, "Shared quote", citation.ExactText)
	// Anchor order within a quote is stable by anchor ID ascending.
	require.Len(t, citation.Anchors, 2)
	assert.Equal(t, "anchor-extra", citation.Anchors[0].ID)
	assert.Equal(t, "anchor-shared", citation.Anchors[1].ID)
}

func TestGivenAQuoteContainedInALongerQuoteWhenDraftIsAssembledThenTheShorterQuoteIsDropped(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}
	longQuote := "This is a longer quote containing short"
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{
			{ID: "anchor-short", ExactQuote: "short"},
			{ID: "anchor-long", ExactQuote: longQuote},
		}},
	}

	result, err := buildCurationDraft("slug", draft, carried, nil)
	require.NoError(t, err)

	require.Len(t, result.Citations, 1)
	assert.Equal(t, longQuote, result.Citations[0].ExactText)
	assert.Equal(t, SectionDraft{
		Key: "source-evidence", Heading: "Source evidence",
		Markdown: longQuote,
	}, result.Sections[1])
}

func TestGivenNoSurvivingQuotesWhenDraftIsAssembledThenErrorIsReturned(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}

	_, err := buildCurationDraft("slug", draft, nil, nil)
	require.Error(t, err)

	_, err = buildCurationDraft("slug", draft, []PageCitation{{SourceAnchors: nil}}, nil)
	require.Error(t, err)
}

func TestGivenRelatedPagesWhenDraftIsAssembledThenRelatedSectionAndLinksAreEmitted(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{{ID: "a1", ExactQuote: "Some quote"}}},
	}
	related := []RelatedPage{
		{ID: "page-foo", Title: "Foo"},
		{ID: "page-baz", Title: "Baz"},
	}

	result, err := buildCurationDraft("slug", draft, carried, related)
	require.NoError(t, err)

	require.Len(t, result.Sections, 3)
	assert.Equal(t, "related-knowledge", result.Sections[2].Key)
	assert.Equal(t, "See also: Foo; Baz.", result.Sections[2].Markdown)

	require.Len(t, result.Links, 2)
	assert.Equal(t, LinkDraft{
		SectionKey: "related-knowledge", ExactText: "Foo", TargetPageID: "page-foo",
		RelationType: RelationTypeRelatesTo,
	}, result.Links[0])
	assert.Equal(t, LinkDraft{
		SectionKey: "related-knowledge", ExactText: "Baz", TargetPageID: "page-baz",
		RelationType: RelationTypeRelatesTo,
	}, result.Links[1])
}

func TestGivenNoRelatedPagesWhenDraftIsAssembledThenNoRelatedSectionIsEmitted(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{{ID: "a1", ExactQuote: "Some quote"}}},
	}

	result, err := buildCurationDraft("slug", draft, carried, nil)
	require.NoError(t, err)

	require.Len(t, result.Sections, 2)
	assert.Empty(t, result.Links)
}

func TestGivenEmptyTitleWhenDraftIsAssembledThenErrorIsReturned(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "  ",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{{ID: "a1", ExactQuote: "Some quote"}}},
	}

	_, err := buildCurationDraft("slug", draft, carried, nil)
	require.Error(t, err)
}

func TestGivenEmptySummaryWhenDraftIsAssembledThenErrorIsReturned(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{{ID: "a1", ExactQuote: "Some quote"}}},
	}

	_, err := buildCurationDraft("slug", draft, carried, nil)
	require.Error(t, err)
}

func TestGivenNoSectionsWhenDraftIsAssembledThenErrorIsReturned(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{Title: "Title", Summary: "Summary"}
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{{ID: "a1", ExactQuote: "Some quote"}}},
	}

	_, err := buildCurationDraft("slug", draft, carried, nil)
	require.Error(t, err)
}

// Regression test for a Critical review finding on Task 6: the pairwise
// containment sweep in carriedEvidenceQuotes only checks whether one quote's
// exact text contains another's. It missed a quote whose exact text is
// spelled out by joining two OTHER quotes with the "\n\n" section separator,
// even though none of the three is a substring of any other in isolation.
//
// A = "XXXwor", B = "ldYYY", C = "wor\n\nld" are pairwise non-substrings.
// Sorted longest-first: C, A, B. Joining them all as
// "wor\n\nld" + "\n\n" + "XXXwor" + "\n\n" + "ldYYY" produces
// "wor\n\nld\n\nXXXwor\n\nldYYY", in which C's text "wor\n\nld" occurs both
// at position 0 (itself) and again spanning A's tail, the separator, and
// B's head — two occurrences, which fails buildCitations' downstream
// exactly-once check (service.go uniqueTextRange). C is the (only) quote
// that offends in that join, so it must be dropped even though it is the
// longest of the three; A and B, which do not offend, must both survive.
func TestGivenAQuoteSpelledOutAcrossTheJoinSeparatorWhenDraftIsAssembledThenThatQuoteIsDroppedAndOthersSurvive(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}
	quoteA := "XXXwor"
	quoteB := "ldYYY"
	quoteC := "wor\n\nld"
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{
			{ID: "anchor-a", ExactQuote: quoteA},
			{ID: "anchor-b", ExactQuote: quoteB},
			{ID: "anchor-c", ExactQuote: quoteC},
		}},
	}

	result, err := buildCurationDraft("slug", draft, carried, nil)
	require.NoError(t, err)

	require.Len(t, result.Sections, 2)
	evidenceSection := result.Sections[1]
	assert.Equal(t, "source-evidence", evidenceSection.Key)

	require.Len(t, result.Citations, 2)
	assert.Equal(t, quoteA, result.Citations[0].ExactText, "quote A survives")
	assert.Equal(t, quoteB, result.Citations[1].ExactText, "quote B survives")
	for _, citation := range result.Citations {
		assert.NotEqual(t, quoteC, citation.ExactText, "quote C must be dropped: its text is spelled out across the A/B join boundary")
	}

	// Every surviving citation's exact text must occur exactly once in the
	// joined section markdown, matching the invariant buildCitations enforces
	// downstream via uniqueTextRange.
	for _, citation := range result.Citations {
		assert.Equal(t, 1, strings.Count(evidenceSection.Markdown, citation.ExactText),
			"citation %q must occur exactly once in the source-evidence markdown", citation.ExactText)
	}
}

// Regression test companion: when dropping one offending quote resolves the
// collision, every other quote — including ones entirely unrelated to the
// collision — must survive untouched. This guards against an overly broad
// fix that drops more than the minimum necessary, or that disturbs quotes
// that never collided.
func TestGivenOneOffendingQuoteAmongOthersWhenDraftIsAssembledThenOnlyThatQuoteIsDroppedAndUnrelatedQuotesSurvive(t *testing.T) {
	t.Parallel()

	draft := CurationDraft{
		Title:   "Title",
		Summary: "Summary",
		Sections: []SectionDraft{
			{Key: "background", Heading: "Background", Markdown: "Body."},
		},
	}
	quoteA := "XXXwor"
	quoteB := "ldYYY"
	quoteC := "wor\n\nld"
	quoteD := "unique-quote-zzz" // unrelated to A, B, and C; must survive untouched
	carried := []PageCitation{
		{SourceAnchors: []SourceAnchor{
			{ID: "anchor-a", ExactQuote: quoteA},
			{ID: "anchor-b", ExactQuote: quoteB},
			{ID: "anchor-c", ExactQuote: quoteC},
			{ID: "anchor-d", ExactQuote: quoteD},
		}},
	}

	result, err := buildCurationDraft("slug", draft, carried, nil)
	require.NoError(t, err)

	require.Len(t, result.Citations, 3)
	texts := make([]string, 0, len(result.Citations))
	for _, citation := range result.Citations {
		texts = append(texts, citation.ExactText)
	}
	assert.ElementsMatch(t, []string{quoteD, quoteA, quoteB}, texts)

	evidenceSection := result.Sections[1]
	for _, citation := range result.Citations {
		assert.Equal(t, 1, strings.Count(evidenceSection.Markdown, citation.ExactText),
			"citation %q must occur exactly once in the source-evidence markdown", citation.ExactText)
	}
}
