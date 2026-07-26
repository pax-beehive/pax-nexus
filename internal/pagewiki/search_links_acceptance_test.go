package pagewiki_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type SearchLinksAcceptanceSuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
	sqlitePage pagewiki.Page
}

func TestSearchLinksAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(SearchLinksAcceptanceSuite))
}

func (s *SearchLinksAcceptanceSuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()[:1]},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)
	request := multiPageSource()
	request.IdempotencyKey = "search-prerequisite-sqlite"
	_, err := service.InjectSession(s.ctx, request)
	s.Require().NoError(err)
	s.sqlitePage, err = s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)
}

func (s *SearchLinksAcceptanceSuite) TestGivenLinkedKnowledgeWhenInjectedThenItIsSearchableWithEvidence() {
	injection := s.injectSearchPage()

	results, err := s.repository.Search(s.ctx, "lexical ranking")

	s.Require().NoError(err)
	s.Require().Len(results, 1)
	result := results[0]
	s.Require().Equal("wiki-search", result.Page.Slug)
	s.Require().Equal(result.Page.CurrentRevisionID, result.RevisionID)
	s.Require().Equal("retrieval", result.SectionKey)
	s.Require().Contains(result.Passage, "lexical ranking")
	s.Require().Positive(result.Score)
	s.Require().Len(result.Citations, 1)
	s.Require().Equal("lexical ranking", result.Citations[0].ExactText)
	s.Require().Equal(
		"Wiki search uses SQLite for lexical ranking.",
		result.Citations[0].SourceAnchors[0].ExactQuote,
	)
	s.Require().Len(result.Links, 1)
	s.Require().Equal(s.sqlitePage.ID, result.Links[0].TargetPageID)

	searchPage, err := s.repository.PageBySlug(s.ctx, "wiki-search")
	s.Require().NoError(err)
	searchLinks, err := s.repository.PageLinks(s.ctx, searchPage.ID)
	s.Require().NoError(err)
	s.Require().Len(searchLinks.Outgoing, 1)
	s.Require().Equal(s.sqlitePage.ID, searchLinks.Outgoing[0].TargetPage.ID)
	sqliteLinks, err := s.repository.PageLinks(s.ctx, s.sqlitePage.ID)
	s.Require().NoError(err)
	s.Require().Len(sqliteLinks.Incoming, 1)
	s.Require().Equal(searchPage.ID, sqliteLinks.Incoming[0].SourcePage.ID)

	backlinks, err := s.repository.SourceBacklinks(
		s.ctx,
		injection.SourceRevisionID,
	)
	s.Require().NoError(err)
	s.Require().Len(backlinks, 1)
	s.Require().Equal(searchPage.ID, backlinks[0].Page.ID)
	s.Require().Equal(searchPage.CurrentRevisionID, backlinks[0].Revision.ID)
	s.Require().Len(backlinks[0].Citations, 1)
}

func (s *SearchLinksAcceptanceSuite) TestGivenUpdatedPageWhenSearchedThenOldRevisionIsExcluded() {
	firstInjection := s.injectSearchPage()
	searchPage, err := s.repository.PageBySlug(s.ctx, "wiki-search")
	s.Require().NoError(err)
	oldRevisionID := searchPage.CurrentRevisionID
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
			{
				Key:                    "wiki-search-update",
				Action:                 pagewiki.PageActionUpdate,
				TargetPageID:           searchPage.ID,
				ExpectedBaseRevisionID: oldRevisionID,
				EvidenceEventIDs:       []string{"event-semantic"},
			},
		}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"wiki-search-update": semanticSearchDraft(s.sqlitePage.ID),
		}},
	)

	update, err := service.InjectSession(s.ctx, semanticSearchSource())

	s.Require().NoError(err)
	oldResults, err := s.repository.Search(s.ctx, "lexical ranking")
	s.Require().NoError(err)
	s.Require().Empty(oldResults)
	currentResults, err := s.repository.Search(s.ctx, "semantic reranking")
	s.Require().NoError(err)
	s.Require().Len(currentResults, 1)
	s.Require().Equal(update.Run.Targets[0].PageRevisionID, currentResults[0].RevisionID)
	oldBacklinks, err := s.repository.SourceBacklinks(
		s.ctx,
		firstInjection.SourceRevisionID,
	)
	s.Require().NoError(err)
	s.Require().Empty(oldBacklinks)
	currentBacklinks, err := s.repository.SourceBacklinks(
		s.ctx,
		update.SourceRevisionID,
	)
	s.Require().NoError(err)
	s.Require().Len(currentBacklinks, 1)
}

func (s *SearchLinksAcceptanceSuite) TestGivenDeletedSearchChunksWhenRebuiltThenResultsAreEquivalent() {
	s.injectSearchPage()
	before, err := s.repository.Search(s.ctx, "lexical ranking")
	s.Require().NoError(err)
	s.Require().NotEmpty(before)
	s.repository.ClearSearchChunks()
	s.Require().Zero(s.repository.SearchChunkCount())

	err = s.repository.RebuildSearchIndex(s.ctx)

	s.Require().NoError(err)
	after, err := s.repository.Search(s.ctx, "lexical ranking")
	s.Require().NoError(err)
	s.Require().Equal(before, after)
}

func (s *SearchLinksAcceptanceSuite) TestGivenInvalidLinkWhenInjectedThenPublicationFails() {
	tests := []struct {
		name         string
		section      string
		targetPageID string
	}{
		{
			name:         "unknown target Page",
			section:      "Wiki search uses SQLite for lexical ranking.",
			targetPageID: "page-forged",
		},
		{
			name:         "link exact text is repeated",
			section:      "Wiki search mentions SQLite and SQLite.",
			targetPageID: s.sqlitePage.ID,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			service := pagewiki.NewService(
				s.repository,
				pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
					searchPageBrief(),
				}},
				pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
					"wiki-search": {
						Slug:    "wiki-search",
						Title:   "Wiki Search",
						Summary: "Wiki search behavior.",
						Sections: []pagewiki.SectionDraft{
							{
								Key:      "retrieval",
								Heading:  "Retrieval",
								Markdown: tt.section,
							},
						},
						Citations: []pagewiki.CitationDraft{
							{
								SectionKey: "retrieval",
								ExactText:  "Wiki search",
								Evidence: []pagewiki.EvidenceQuoteDraft{
									{
										EventID: "event-search",
										ExactText: "Wiki search uses SQLite " +
											"for lexical ranking.",
									},
								},
							},
						},
						Links: []pagewiki.LinkDraft{
							{
								SectionKey:   "retrieval",
								ExactText:    "SQLite",
								TargetPageID: tt.targetPageID,
							},
						},
					},
				}},
			)
			request := searchPageSource()
			request.IdempotencyKey = "invalid-link-" + strings.ReplaceAll(tt.name, " ", "-")

			result, err := service.InjectSession(s.ctx, request)

			s.Require().NoError(err)
			s.Require().Equal(pagewiki.RunStatusFailed, result.Run.Status)
			s.Require().Equal(
				pagewiki.TargetFailureInvalidLink,
				result.Run.Targets[0].FailureReason,
			)
			s.Require().Equal(1, s.repository.PageCount())
		})
	}
}

func (s *SearchLinksAcceptanceSuite) injectSearchPage() pagewiki.InjectResult {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{searchPageBrief()}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"wiki-search": linkedSearchDraft(s.sqlitePage.ID),
		}},
	)

	result, err := service.InjectSession(s.ctx, searchPageSource())

	s.Require().NoError(err)
	return result
}

func searchPageBrief() pagewiki.PageBrief {
	return pagewiki.PageBrief{
		Key:              "wiki-search",
		Action:           pagewiki.PageActionCreate,
		ProposedSlug:     "wiki-search",
		ProposedTitle:    "Wiki Search",
		TopicPath:        []string{"Engineering", "Search"},
		EvidenceEventIDs: []string{"event-search"},
	}
}

func searchPageSource() pagewiki.InjectSessionRequest {
	raw := "event-search: Wiki search uses SQLite for lexical ranking."
	eventText := "Wiki search uses SQLite for lexical ranking."
	start := strings.Index(raw, eventText)
	return pagewiki.InjectSessionRequest{
		SourceID:       "session-search",
		IdempotencyKey: "create-search-page",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{
			{
				ID:        "event-search",
				StartByte: start,
				EndByte:   start + len(eventText),
			},
		},
	}
}

func linkedSearchDraft(targetPageID string) pagewiki.PageDraft {
	return pagewiki.PageDraft{
		Slug:    "wiki-search",
		Title:   "Wiki Search",
		Summary: "Wiki search starts with deterministic lexical ranking.",
		Sections: []pagewiki.SectionDraft{
			{
				Key:      "retrieval",
				Heading:  "Retrieval",
				Markdown: "Wiki search uses SQLite for lexical ranking.",
			},
		},
		Citations: []pagewiki.CitationDraft{
			{
				SectionKey: "retrieval",
				ExactText:  "lexical ranking",
				Evidence: []pagewiki.EvidenceQuoteDraft{
					{
						EventID:   "event-search",
						ExactText: "Wiki search uses SQLite for lexical ranking.",
					},
				},
			},
		},
		Links: []pagewiki.LinkDraft{
			{
				SectionKey:   "retrieval",
				ExactText:    "SQLite",
				TargetPageID: targetPageID,
			},
		},
	}
}

func semanticSearchSource() pagewiki.InjectSessionRequest {
	raw := "event-semantic: Wiki search now uses SQLite with semantic reranking."
	eventText := "Wiki search now uses SQLite with semantic reranking."
	start := strings.Index(raw, eventText)
	return pagewiki.InjectSessionRequest{
		SourceID:       "session-semantic-search",
		IdempotencyKey: "update-search-page",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{
			{
				ID:        "event-semantic",
				StartByte: start,
				EndByte:   start + len(eventText),
			},
		},
	}
}

func semanticSearchDraft(targetPageID string) pagewiki.PageDraft {
	return pagewiki.PageDraft{
		Slug:    "wiki-search",
		Title:   "Wiki Search",
		Summary: "Wiki search now includes semantic reranking.",
		Sections: []pagewiki.SectionDraft{
			{
				Key:      "retrieval",
				Heading:  "Retrieval",
				Markdown: "Wiki search now uses SQLite with semantic reranking.",
			},
		},
		Citations: []pagewiki.CitationDraft{
			{
				SectionKey: "retrieval",
				ExactText:  "semantic reranking",
				Evidence: []pagewiki.EvidenceQuoteDraft{
					{
						EventID: "event-semantic",
						ExactText: "Wiki search now uses SQLite " +
							"with semantic reranking.",
					},
				},
			},
		},
		Links: []pagewiki.LinkDraft{
			{
				SectionKey:   "retrieval",
				ExactText:    "SQLite",
				TargetPageID: targetPageID,
			},
		},
	}
}
