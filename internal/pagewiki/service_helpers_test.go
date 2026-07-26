package pagewiki

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGivenLinksWhenRevisionsAreComparedThenLinkIdentityIsSignificant(t *testing.T) {
	t.Parallel()
	left := PageRevision{
		Title:    "Page",
		Summary:  "Summary",
		Markdown: "# Page",
		Links: []PageLink{{
			SectionKey:   "links",
			StartByte:    0,
			EndByte:      6,
			ExactText:    "target",
			TargetPageID: "target-page",
		}},
	}
	right := left

	assert.True(t, revisionsEquivalent(left, right))

	right.Links = append([]PageLink(nil), left.Links...)
	right.Links[0].TargetPageID = "different-page"
	assert.False(t, revisionsEquivalent(left, right))
}

func TestGivenInvalidDraftShapesWhenValidatedThenEachIsRejected(t *testing.T) {
	t.Parallel()
	valid := PageDraft{
		Slug:    "page",
		Title:   "Page",
		Summary: "Summary",
		Sections: []SectionDraft{{
			Key:      "decision",
			Heading:  "Decision",
			Markdown: "Grounded text.",
		}},
		Citations: []CitationDraft{{SectionKey: "decision"}},
	}
	tests := []struct {
		name  string
		brief PageBrief
		draft PageDraft
	}{
		{
			name:  "missing identity",
			brief: PageBrief{Action: PageActionUpdate},
			draft: func() PageDraft {
				draft := valid
				draft.Title = ""
				return draft
			}(),
		},
		{
			name:  "create changes reserved slug",
			brief: PageBrief{Action: PageActionCreate, ProposedSlug: "reserved"},
			draft: valid,
		},
		{
			name:  "missing sections",
			brief: PageBrief{Action: PageActionUpdate},
			draft: func() PageDraft {
				draft := valid
				draft.Sections = nil
				return draft
			}(),
		},
		{
			name:  "missing citations",
			brief: PageBrief{Action: PageActionUpdate},
			draft: func() PageDraft {
				draft := valid
				draft.Citations = nil
				return draft
			}(),
		},
		{
			name:  "invalid section",
			brief: PageBrief{Action: PageActionUpdate},
			draft: func() PageDraft {
				draft := valid
				draft.Sections = []SectionDraft{{
					Key:      "not valid",
					Heading:  "Decision",
					Markdown: "Grounded text.",
				}}
				return draft
			}(),
		},
		{
			name:  "duplicate section",
			brief: PageBrief{Action: PageActionUpdate},
			draft: func() PageDraft {
				draft := valid
				draft.Sections = append(
					append([]SectionDraft(nil), valid.Sections...),
					valid.Sections[0],
				)
				return draft
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := validateDraft(tt.brief, tt.draft)

			require.ErrorIs(t, err, ErrInvalidDraft)
		})
	}
}

func TestGivenBlankExactTextWhenLocatedThenItIsRejected(t *testing.T) {
	t.Parallel()

	_, _, err := uniqueTextRange("content", "")

	require.EqualError(t, err, "exact text is required")
}
