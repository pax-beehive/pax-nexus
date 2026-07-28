package recalladapter_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/recalladapter"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type activeSearchAcceptanceSuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
	router     *recall.Router
	teamCalls  int
}

func TestActiveSearchAcceptanceSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(activeSearchAcceptanceSuite))
}

func (s *activeSearchAcceptanceSuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
	s.publishCitedPage()
	adapter, err := recalladapter.New(s.repository)
	s.Require().NoError(err)
	s.router, err = recall.NewRouter(teamNotePathFunc(func(
		context.Context,
		teamnote.RecallRequest,
	) (teamnote.NoteEnvelope, error) {
		s.teamCalls++
		return teamnote.NoteEnvelope{}, nil
	}), adapter, recall.Config{})
	s.Require().NoError(err)
}

func (s *activeSearchAcceptanceSuite) TestGivenCurrentCitedPageWhenAgentActivelySearchesThenRevisionReferenceIsReturned() {
	result, err := s.router.Search(s.ctx, recall.SearchRequest{
		Intent:      recall.IntentActive,
		Source:      recall.SourcePageWiki,
		Actor:       teamnote.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "session-2"},
		Query:       "Radix Slot",
		TokenBudget: 128,
		MaxItems:    4,
	})

	s.Require().NoError(err)
	s.Require().Len(result.Hits, 1)
	hit := result.Hits[0]
	s.Equal("pagewiki:revision/revision-radix-1", hit.Ref)
	s.Contains(hit.Text, "Radix Slot")
	s.Equal(recall.DispositionReference, hit.Disposition)
	s.Equal("page-radix", hit.Metadata["page_id"])
	s.Equal("radix-slot", hit.Metadata["slug"])
	s.Equal("revision-radix-1", hit.Metadata["revision_id"])
	s.Equal("root-cause", hit.Metadata["section_key"])
	s.Zero(s.teamCalls)
	s.Equal(recall.PathSkipped, result.Trace.TeamNote.Status)
	s.Equal(recall.PathCompleted, result.Trace.WikiSearch.Status)
}

func (s *activeSearchAcceptanceSuite) publishCitedPage() {
	raw := []byte("Radix Slot fails when it receives two direct children.")
	source := pagewiki.SourceRevision{
		ID:       "source-revision-radix",
		SourceID: "session-radix",
		SHA256:   "sha-radix",
		Raw:      raw,
		Events: []pagewiki.SourceEvent{{
			ID: "event-radix", StartByte: 0, EndByte: len(raw),
		}},
	}
	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, source))
	page := pagewiki.Page{
		ID: "page-radix", Slug: "radix-slot", Title: "Radix Slot",
		CurrentRevisionID: "revision-radix-1",
	}
	revision := pagewiki.PageRevision{
		ID: "revision-radix-1", PageID: page.ID, Title: page.Title,
		Summary:  "Why slotted link buttons crashed.",
		Markdown: "# Radix Slot\n\n## Root cause\n\nRadix Slot requires one direct child.",
		Sections: []pagewiki.PageSection{{
			Key: "root-cause", Heading: "Root cause",
			Markdown: "Radix Slot requires one direct child.",
		}},
		Citations: []pagewiki.PageCitation{{
			ID: "citation-radix", PageRevisionID: "revision-radix-1",
			SectionKey: "root-cause", StartByte: 0, EndByte: len("Radix Slot"),
			ExactText: "Radix Slot",
			SourceAnchors: []pagewiki.SourceAnchor{{
				ID: "anchor-radix", SourceRevisionID: source.ID, EventID: "event-radix",
				StartByte: 0, EndByte: len(raw), ExactQuote: string(raw),
			}},
		}},
	}
	s.Require().NoError(s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page: page, Revision: revision,
		Topics: []pagewiki.Topic{{
			ID: "topic-ui-reliability", Slug: "ui-reliability", Title: "UI reliability",
		}},
		Placement: &pagewiki.PagePlacement{
			PageID: page.ID, TopicID: "topic-ui-reliability",
		},
	}))
}

type teamNotePathFunc func(
	context.Context,
	teamnote.RecallRequest,
) (teamnote.NoteEnvelope, error)

func (f teamNotePathFunc) RecallNotes(
	ctx context.Context,
	request teamnote.RecallRequest,
) (teamnote.NoteEnvelope, error) {
	return f(ctx, request)
}
