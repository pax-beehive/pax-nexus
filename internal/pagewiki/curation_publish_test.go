package pagewiki

import (
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
	assert.Equal(t, LinkDraft{SectionKey: "related-knowledge", ExactText: "Foo", TargetPageID: "page-foo"}, result.Links[0])
	assert.Equal(t, LinkDraft{SectionKey: "related-knowledge", ExactText: "Baz", TargetPageID: "page-baz"}, result.Links[1])
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
