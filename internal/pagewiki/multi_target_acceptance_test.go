package pagewiki_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type MultiTargetAcceptanceSuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
}

func TestMultiTargetAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(MultiTargetAcceptanceSuite))
}

func (s *MultiTargetAcceptanceSuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
}

func (s *MultiTargetAcceptanceSuite) TestGivenTwoValidBriefsWhenInjectedThenBothPagesAreNavigable() {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)

	result, err := service.InjectSession(s.ctx, multiPageSource())

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Len(result.Run.Targets, 2)
	s.Require().Equal(2, s.repository.PageCount())

	navigation, err := s.repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().True(navigationContainsPage(navigation, "sqlite"))
	s.Require().True(navigationContainsPage(navigation, "wiki-search"))
	s.Require().False(navigationContainsTopic(navigation, "Inbox"))
	s.Require().LessOrEqual(navigationDepthForPage(navigation, "sqlite"), 2)
	s.Require().LessOrEqual(navigationDepthForPage(navigation, "wiki-search"), 2)
}

func (s *MultiTargetAcceptanceSuite) TestGivenOneInvalidTargetWhenInjectedThenSiblingRemainsPublished() {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(true)},
	)

	result, err := service.InjectSession(s.ctx, multiPageSource())

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusPartialSuccess, result.Run.Status)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, result.Run.Targets[0].Status)
	s.Require().Equal(pagewiki.TargetStatusFailed, result.Run.Targets[1].Status)
	s.Require().Equal(
		pagewiki.TargetFailureInvalidCitation,
		result.Run.Targets[1].FailureReason,
	)
	s.Require().Equal(1, s.repository.PageCount())

	_, err = s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)
	_, err = s.repository.PageBySlug(s.ctx, "wiki-search")
	s.Require().ErrorIs(err, pagewiki.ErrNotFound)

	navigation, err := s.repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().True(navigationContainsPage(navigation, "sqlite"))
	s.Require().False(navigationContainsPage(navigation, "wiki-search"))
	s.Require().False(navigationContainsTopic(navigation, "Search"))
}

func (s *MultiTargetAcceptanceSuite) TestGivenAmbiguousBriefWhenInjectedThenItStaysPendingWithoutInbox() {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{
			Briefs: []pagewiki.PageBrief{
				{
					Key:              "unclear",
					Action:           pagewiki.PageActionAmbiguous,
					EvidenceEventIDs: []string{"event-storage"},
				},
			},
		},
		pagewiki.ScriptedEditor{},
	)

	result, err := service.InjectSession(s.ctx, multiPageSource())

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusPending, result.Run.Status)
	s.Require().Equal(pagewiki.TargetStatusPending, result.Run.Targets[0].Status)
	s.Require().Zero(s.repository.PageCount())
	navigation, err := s.repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(navigation.Roots)
	s.Require().False(navigationContainsTopic(navigation, "Inbox"))
}

func (s *MultiTargetAcceptanceSuite) TestGivenSourceOnlyBriefWhenInjectedThenRunCompletesWithoutPage() {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{
			Briefs: []pagewiki.PageBrief{
				{
					Key:              "source-only",
					Action:           pagewiki.PageActionSourceOnly,
					EvidenceEventIDs: []string{"event-storage"},
				},
			},
		},
		pagewiki.ScriptedEditor{},
	)

	result, err := service.InjectSession(s.ctx, multiPageSource())

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, result.Run.Targets[0].Status)
	s.Require().Zero(s.repository.PageCount())
}

func multiPageBriefs() []pagewiki.PageBrief {
	return []pagewiki.PageBrief{
		{
			Key:              "sqlite",
			Action:           pagewiki.PageActionCreate,
			ProposedSlug:     "sqlite",
			ProposedTitle:    "SQLite",
			TopicPath:        []string{"Engineering", "Storage"},
			EvidenceEventIDs: []string{"event-storage"},
		},
		{
			Key:              "wiki-search",
			Action:           pagewiki.PageActionCreate,
			ProposedSlug:     "wiki-search",
			ProposedTitle:    "Wiki Search",
			TopicPath:        []string{"Engineering", "Search"},
			EvidenceEventIDs: []string{"event-search"},
		},
	}
}

func multiPageDrafts(invalidSecond bool) map[string]pagewiki.PageDraft {
	searchEventID := "event-search"
	if invalidSecond {
		searchEventID = "event-forged"
	}
	return map[string]pagewiki.PageDraft{
		"sqlite": {
			Slug:    "sqlite",
			Title:   "SQLite",
			Summary: "SQLite stores the local Wiki.",
			Sections: []pagewiki.SectionDraft{
				{
					Key:      "decision",
					Heading:  "Decision",
					Markdown: "The local Wiki uses SQLite.",
				},
			},
			Citations: []pagewiki.CitationDraft{
				{
					SectionKey: "decision",
					ExactText:  "uses SQLite",
					Evidence: []pagewiki.EvidenceQuoteDraft{
						{
							EventID:   "event-storage",
							ExactText: "The local Wiki uses SQLite.",
						},
					},
				},
			},
		},
		"wiki-search": {
			Slug:    "wiki-search",
			Title:   "Wiki Search",
			Summary: "Wiki search is lexical in the first version.",
			Sections: []pagewiki.SectionDraft{
				{
					Key:      "retrieval",
					Heading:  "Retrieval",
					Markdown: "The first search version uses lexical ranking.",
				},
			},
			Citations: []pagewiki.CitationDraft{
				{
					SectionKey: "retrieval",
					ExactText:  "lexical ranking",
					Evidence: []pagewiki.EvidenceQuoteDraft{
						{
							EventID:   searchEventID,
							ExactText: "The first search version uses lexical ranking.",
						},
					},
				},
			},
		},
	}
}

func multiPageSource() pagewiki.InjectSessionRequest {
	raw := "event-storage: The local Wiki uses SQLite.\n" +
		"event-search: The first search version uses lexical ranking."
	storageText := "The local Wiki uses SQLite."
	searchText := "The first search version uses lexical ranking."
	storageStart := strings.Index(raw, storageText)
	searchStart := strings.Index(raw, searchText)
	return pagewiki.InjectSessionRequest{
		SourceID: "session-multi",
		Raw:      []byte(raw),
		Events: []pagewiki.SourceEventInput{
			{
				ID:        "event-storage",
				StartByte: storageStart,
				EndByte:   storageStart + len(storageText),
			},
			{
				ID:        "event-search",
				StartByte: searchStart,
				EndByte:   searchStart + len(searchText),
			},
		},
	}
}

func navigationContainsPage(navigation pagewiki.Navigation, slug string) bool {
	return navigationDepthForPage(navigation, slug) > 0
}

func navigationDepthForPage(navigation pagewiki.Navigation, slug string) int {
	for _, root := range navigation.Roots {
		for _, page := range root.Pages {
			if page.Slug == slug {
				return 1
			}
		}
		for _, child := range root.Children {
			for _, page := range child.Pages {
				if page.Slug == slug {
					return 2
				}
			}
		}
	}
	return 0
}

func navigationContainsTopic(navigation pagewiki.Navigation, title string) bool {
	for _, root := range navigation.Roots {
		if root.Title == title {
			return true
		}
		for _, child := range root.Children {
			if child.Title == title {
				return true
			}
		}
	}
	return false
}
