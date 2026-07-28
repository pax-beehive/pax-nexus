package recalladapter_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/recalladapter"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/stretchr/testify/suite"
)

type immutableGetAcceptanceSuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
	adapter    *recalladapter.Adapter
	source     pagewiki.SourceRevision
	page       pagewiki.Page
}

func TestImmutableGetAcceptanceSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(immutableGetAcceptanceSuite))
}

func (s *immutableGetAcceptanceSuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
	raw := []byte("Radix Slot fails when it receives two direct children.")
	s.source = pagewiki.SourceRevision{
		ID: "source-revision-radix", SourceID: "session-radix", SHA256: "sha-radix", Raw: raw,
		Events: []pagewiki.SourceEvent{{
			ID: "event-radix", StartByte: 0, EndByte: len(raw),
		}},
	}
	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, s.source))
	s.page = pagewiki.Page{
		ID: "page-radix", Slug: "radix-slot", Title: "Radix Slot",
		CurrentRevisionID: "revision-radix-1",
	}
	s.Require().NoError(s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page:      s.page,
		Revision:  s.revision("revision-radix-1", "", "Radix Slot requires one direct child."),
		Topics:    []pagewiki.Topic{{ID: "topic-ui", Slug: "ui", Title: "UI"}},
		Placement: &pagewiki.PagePlacement{PageID: s.page.ID, TopicID: "topic-ui"},
	}))
	adapter, err := recalladapter.New(s.repository)
	s.Require().NoError(err)
	s.adapter = adapter
}

func (s *immutableGetAcceptanceSuite) TestGivenPageAdvancedWhenOriginalRefIsReadThenExactRevisionAndEvidenceAreReturned() {
	updated := s.page
	updated.CurrentRevisionID = "revision-radix-2"
	s.Require().NoError(s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page: updated,
		Revision: s.revision(
			"revision-radix-2",
			"revision-radix-1",
			"Radix Slottable now preserves adjacent icons.",
		),
	}))

	document, err := s.adapter.Get(s.ctx, recall.GetRequest{
		Ref: "pagewiki:revision/revision-radix-1",
	})

	s.Require().NoError(err)
	s.Equal("pagewiki:revision/revision-radix-1", document.Ref)
	s.Contains(document.Text, "requires one direct child")
	s.NotContains(document.Text, "preserves adjacent icons")
	s.Require().NotNil(document.PageWiki)
	s.Equal("page-radix", document.PageWiki.PageID)
	s.Equal("radix-slot", document.PageWiki.Slug)
	s.Equal("revision-radix-1", document.PageWiki.RevisionID)
	s.Require().Len(document.PageWiki.Citations, 1)
	citation := document.PageWiki.Citations[0]
	s.Equal("Radix Slot", citation.ExactText)
	s.Require().Len(citation.SourceAnchors, 1)
	s.Equal(s.source.ID, citation.SourceAnchors[0].SourceRevisionID)
	s.Equal("event-radix", citation.SourceAnchors[0].EventID)
	s.Equal(string(s.source.Raw), citation.SourceAnchors[0].ExactQuote)
	s.Require().Len(document.PageWiki.Links, 1)
	s.Equal("outgoing", document.PageWiki.Links[0].Direction)
	s.Equal(s.page.ID, document.PageWiki.Links[0].TargetPageID)
}

func (s *immutableGetAcceptanceSuite) TestGivenMalformedOrUnknownRefWhenReadThenItFailsClosed() {
	tests := []struct {
		name string
		ref  string
	}{
		{name: "empty revision", ref: "pagewiki:revision/"},
		{name: "wrong namespace", ref: "wiki:revision/revision-radix-1"},
		{name: "nested revision", ref: "pagewiki:revision/revision/radix"},
		{name: "surrounding whitespace", ref: " pagewiki:revision/revision-radix-1 "},
		{name: "unknown revision", ref: "pagewiki:revision/revision-missing"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.adapter.Get(s.ctx, recall.GetRequest{Ref: test.ref})

			s.Require().Error(err)
		})
	}
}

func (s *immutableGetAcceptanceSuite) revision(
	revisionID string,
	baseRevisionID string,
	sectionMarkdown string,
) pagewiki.PageRevision {
	return pagewiki.PageRevision{
		ID: revisionID, PageID: s.page.ID, BaseRevisionID: baseRevisionID,
		Title: s.page.Title, Summary: "Root cause and fix.",
		Markdown: "# Radix Slot\n\n## Root cause\n\n" + sectionMarkdown,
		Sections: []pagewiki.PageSection{{
			Key: "root-cause", Heading: "Root cause", Markdown: sectionMarkdown,
		}},
		Citations: []pagewiki.PageCitation{{
			ID: "citation-" + revisionID, PageRevisionID: revisionID,
			SectionKey: "root-cause", StartByte: 0, EndByte: len("Radix Slot"),
			ExactText: "Radix Slot",
			SourceAnchors: []pagewiki.SourceAnchor{{
				ID: "anchor-" + revisionID, SourceRevisionID: s.source.ID,
				EventID: "event-radix", StartByte: 0, EndByte: len(s.source.Raw),
				ExactQuote: string(s.source.Raw),
			}},
		}},
		Links: []pagewiki.PageLink{{
			ID: "link-" + revisionID, PageRevisionID: revisionID,
			SectionKey: "root-cause", StartByte: 0, EndByte: len("Radix Slot"),
			ExactText: "Radix Slot", TargetPageID: s.page.ID,
		}},
	}
}
