package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type InjectAcceptanceSuite struct {
	suite.Suite
	repository *memory.Repository
}

func TestInjectAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(InjectAcceptanceSuite))
}

func (s *InjectAcceptanceSuite) SetupTest() {
	s.repository = memory.NewRepository()
}

func (s *InjectAcceptanceSuite) TestGivenOneSessionWhenInjectedThenOneCitedPageIsPublished() {
	raw := []byte("event-1: The team selected SQLite for the local store.")
	eventText := "The team selected SQLite for the local store."
	eventStart := len("event-1: ")
	sourceBytesBefore := append([]byte(nil), raw...)

	planner := pagewiki.ScriptedPlanner{
		Briefs: []pagewiki.PageBrief{
			{
				Key:              "sqlite",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "sqlite",
				ProposedTitle:    "SQLite",
				ReaderGoal:       "Explain why SQLite is the local store.",
				EvidenceEventIDs: []string{"event-1"},
			},
		},
	}
	editor := pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			"sqlite": {
				Slug:    "sqlite",
				Title:   "SQLite",
				Summary: "SQLite is the selected local persistence layer.",
				Sections: []pagewiki.SectionDraft{
					{
						Key:      "decision",
						Heading:  "Decision",
						Markdown: "The team selected SQLite for the local store.",
					},
				},
				Citations: []pagewiki.CitationDraft{
					{
						SectionKey: "decision",
						ExactText:  "selected SQLite",
						Evidence: []pagewiki.EvidenceQuoteDraft{
							{
								EventID:   "event-1",
								ExactText: eventText,
							},
						},
					},
				},
			},
		},
	}
	service := pagewiki.NewService(s.repository, planner, editor)

	result, err := service.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-1",
		IdempotencyKey: "session-1-injection",
		Raw:            raw,
		Events: []pagewiki.SourceEventInput{
			{
				ID:        "event-1",
				StartByte: eventStart,
				EndByte:   eventStart + len(eventText),
			},
		},
	})

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Len(result.Run.Targets, 1)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, result.Run.Targets[0].Status)

	page, err := s.repository.PageBySlug(context.Background(), "sqlite")
	s.Require().NoError(err)
	revision, err := s.repository.PageRevision(
		context.Background(),
		page.CurrentRevisionID,
	)
	s.Require().NoError(err)
	s.Require().Equal(page.ID, revision.PageID)
	s.Require().Contains(revision.Markdown, "# SQLite")
	s.Require().Len(revision.Citations, 1)

	citation := revision.Citations[0]
	s.Require().Equal("selected SQLite", citation.ExactText)
	s.Require().Len(citation.SourceAnchors, 1)
	anchor := citation.SourceAnchors[0]
	s.Require().Equal("event-1", anchor.EventID)
	s.Require().Equal(eventText, anchor.ExactQuote)
	s.Require().Equal(eventText, string(raw[anchor.StartByte:anchor.EndByte]))

	storedSource, err := s.repository.SourceRevision(
		context.Background(),
		result.SourceRevisionID,
	)
	s.Require().NoError(err)
	s.Require().Equal(sourceBytesBefore, storedSource.Raw)
	s.Require().Equal(sourceBytesBefore, raw)
	s.Require().Zero(s.repository.TopicCount())
	s.Require().Zero(s.repository.PlacementCount())
}

func (s *InjectAcceptanceSuite) TestGivenInvalidCitationWhenInjectedThenNothingIsPublished() {
	raw := []byte("event-1: The team selected SQLite.")
	eventText := "The team selected SQLite."
	eventStart := len("event-1: ")
	tests := []struct {
		name       string
		section    string
		citation   pagewiki.CitationDraft
		wantReason pagewiki.TargetFailureReason
	}{
		{
			name:    "forged event id",
			section: "The team selected SQLite.",
			citation: pagewiki.CitationDraft{
				SectionKey: "decision",
				ExactText:  "selected SQLite",
				Evidence: []pagewiki.EvidenceQuoteDraft{
					{EventID: "event-forged", ExactText: eventText},
				},
			},
			wantReason: pagewiki.TargetFailureInvalidCitation,
		},
		{
			name:    "source quote is absent",
			section: "The team selected SQLite.",
			citation: pagewiki.CitationDraft{
				SectionKey: "decision",
				ExactText:  "selected SQLite",
				Evidence: []pagewiki.EvidenceQuoteDraft{
					{EventID: "event-1", ExactText: "PostgreSQL was selected."},
				},
			},
			wantReason: pagewiki.TargetFailureInvalidCitation,
		},
		{
			name:    "page exact text is absent",
			section: "The team selected SQLite.",
			citation: pagewiki.CitationDraft{
				SectionKey: "decision",
				ExactText:  "selected PostgreSQL",
				Evidence: []pagewiki.EvidenceQuoteDraft{
					{EventID: "event-1", ExactText: eventText},
				},
			},
			wantReason: pagewiki.TargetFailureInvalidCitation,
		},
		{
			name:    "page exact text is repeated",
			section: "SQLite was selected. SQLite was selected.",
			citation: pagewiki.CitationDraft{
				SectionKey: "decision",
				ExactText:  "SQLite was selected",
				Evidence: []pagewiki.EvidenceQuoteDraft{
					{EventID: "event-1", ExactText: eventText},
				},
			},
			wantReason: pagewiki.TargetFailureInvalidCitation,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.repository = memory.NewRepository()
			planner := pagewiki.ScriptedPlanner{
				Briefs: []pagewiki.PageBrief{
					{
						Key:              "sqlite",
						Action:           pagewiki.PageActionCreate,
						ProposedSlug:     "sqlite",
						ProposedTitle:    "SQLite",
						EvidenceEventIDs: []string{"event-1"},
					},
				},
			}
			editor := pagewiki.ScriptedEditor{
				Drafts: map[string]pagewiki.PageDraft{
					"sqlite": {
						Slug:    "sqlite",
						Title:   "SQLite",
						Summary: "SQLite is used locally.",
						Sections: []pagewiki.SectionDraft{
							{
								Key:      "decision",
								Heading:  "Decision",
								Markdown: tt.section,
							},
						},
						Citations: []pagewiki.CitationDraft{tt.citation},
					},
				},
			}
			service := pagewiki.NewService(s.repository, planner, editor)

			result, err := service.InjectSession(
				context.Background(),
				pagewiki.InjectSessionRequest{
					SourceID:       "session-invalid",
					IdempotencyKey: "session-invalid-injection",
					Raw:            raw,
					Events: []pagewiki.SourceEventInput{
						{
							ID:        "event-1",
							StartByte: eventStart,
							EndByte:   eventStart + len(eventText),
						},
					},
				},
			)

			s.Require().NoError(err)
			s.Require().Equal(pagewiki.RunStatusFailed, result.Run.Status)
			s.Require().Len(result.Run.Targets, 1)
			s.Require().Equal(pagewiki.TargetStatusFailed, result.Run.Targets[0].Status)
			s.Require().Equal(tt.wantReason, result.Run.Targets[0].FailureReason)
			s.Require().Zero(s.repository.PageCount())
			s.Require().Zero(s.repository.PageRevisionCount())
		})
	}
}

func (s *InjectAcceptanceSuite) TestGivenTypedBriefsWhenInjectedThenTypesPersist() {
	ctx := context.Background()

	// First session: an untyped brief, exercising the EntityTypeConcept
	// fallback for both the planner's zero value and the update-downgrade
	// guard's baseline.
	untyped := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()[:1]},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)
	firstRequest := multiPageSource()
	firstRequest.IdempotencyKey = "typed-first"
	firstResult, err := untyped.InjectSession(ctx, firstRequest)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, firstResult.Run.Status)

	sqlitePage, err := s.repository.PageBySlug(ctx, "sqlite")
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.EntityTypeConcept, sqlitePage.EntityType)

	// Second session: a typed brief that relates to the first page via a
	// typed relation.
	planner := pagewiki.ScriptedPlanner{
		Briefs: []pagewiki.PageBrief{
			{
				Key:              "wiki-search",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "wiki-search",
				ProposedTitle:    "Wiki Search",
				EvidenceEventIDs: []string{"event-search"},
				EntityType:       pagewiki.EntityTypeSystem,
				RelatedPages: []pagewiki.RelatedPage{
					{
						ID:       sqlitePage.ID,
						Title:    "SQLite",
						Relation: pagewiki.RelationTypeDependsOn,
					},
				},
			},
		},
	}
	editor := pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			"wiki-search": {
				Slug:    "wiki-search",
				Title:   "Wiki Search",
				Summary: "Wiki search depends on SQLite for storage.",
				Sections: []pagewiki.SectionDraft{
					{
						Key:      "retrieval",
						Heading:  "Retrieval",
						Markdown: "The first search version uses lexical ranking.",
					},
					{
						Key:      "related-knowledge",
						Heading:  "Related knowledge",
						Markdown: "See also: SQLite.",
					},
				},
				Citations: []pagewiki.CitationDraft{
					{
						SectionKey: "retrieval",
						ExactText:  "lexical ranking",
						Evidence: []pagewiki.EvidenceQuoteDraft{
							{
								EventID:   "event-search",
								ExactText: "The first search version uses lexical ranking.",
							},
						},
					},
				},
				Links: []pagewiki.LinkDraft{
					{
						SectionKey:   "related-knowledge",
						ExactText:    "SQLite",
						TargetPageID: sqlitePage.ID,
						RelationType: pagewiki.RelationTypeDependsOn,
					},
				},
			},
		},
	}
	service := pagewiki.NewService(s.repository, planner, editor)
	secondRequest := multiPageSource()
	secondRequest.IdempotencyKey = "typed-second"

	secondResult, err := service.InjectSession(ctx, secondRequest)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, secondResult.Run.Status)

	wikiSearchPage, err := s.repository.PageBySlug(ctx, "wiki-search")
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.EntityTypeSystem, wikiSearchPage.EntityType)

	revision, err := s.repository.PageRevision(ctx, wikiSearchPage.CurrentRevisionID)
	s.Require().NoError(err)
	s.Require().Len(revision.Links, 1)
	s.Require().Equal(pagewiki.RelationTypeDependsOn, revision.Links[0].RelationType)

	catalog, err := s.repository.PageCatalog(ctx)
	s.Require().NoError(err)
	entries := make(map[string]pagewiki.EntityType, len(catalog))
	for _, entry := range catalog {
		entries[entry.Slug] = entry.EntityType
	}
	s.Require().Equal(pagewiki.EntityTypeSystem, entries["wiki-search"])
	s.Require().Equal(pagewiki.EntityTypeConcept, entries["sqlite"])
}
