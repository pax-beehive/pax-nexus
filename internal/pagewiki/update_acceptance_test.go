package pagewiki_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type UpdateAcceptanceSuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
}

func TestUpdateAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(UpdateAcceptanceSuite))
}

func (s *UpdateAcceptanceSuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
}

func (s *UpdateAcceptanceSuite) TestGivenLaterSessionWhenInjectedThenPageIsUpdatedImmutably() {
	firstResult := s.injectCreatedSQLite("create-sqlite")
	pageAtRevisionOne, err := s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)
	revisionOne, err := s.repository.PageRevision(
		s.ctx,
		pageAtRevisionOne.CurrentRevisionID,
	)
	s.Require().NoError(err)

	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
			{
				Key:                    "sqlite-update",
				Action:                 pagewiki.PageActionUpdate,
				TargetPageID:           pageAtRevisionOne.ID,
				ExpectedBaseRevisionID: revisionOne.ID,
				EvidenceEventIDs:       []string{"event-wal"},
			},
		}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"sqlite-update": sqliteWALDraft(),
		}},
	)

	secondResult, err := service.InjectSession(s.ctx, walSource("update-sqlite"))

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, secondResult.Run.Status)
	s.Require().NotEqual(firstResult.Run.ID, secondResult.Run.ID)
	currentPage, err := s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)
	s.Require().Equal(pageAtRevisionOne.ID, currentPage.ID)
	s.Require().NotEqual(revisionOne.ID, currentPage.CurrentRevisionID)
	revisionTwo, err := s.repository.PageRevision(s.ctx, currentPage.CurrentRevisionID)
	s.Require().NoError(err)
	s.Require().Equal(revisionOne.ID, revisionTwo.BaseRevisionID)
	s.Require().Contains(revisionTwo.Markdown, "WAL mode")
	unchangedRevisionOne, err := s.repository.PageRevision(s.ctx, revisionOne.ID)
	s.Require().NoError(err)
	s.Require().Equal(revisionOne, unchangedRevisionOne)

	history, err := s.repository.PageRevisionHistory(s.ctx, currentPage.ID)
	s.Require().NoError(err)
	s.Require().Equal(
		[]string{revisionOne.ID, revisionTwo.ID},
		[]string{history[0].ID, history[1].ID},
	)
	navigation, err := s.repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(navigation.Roots)
	s.Require().Len(navigation.Pages, 1)
	s.Equal("sqlite", navigation.Pages[0].Slug)
	s.Require().Zero(s.repository.TopicCount())
	s.Require().Zero(s.repository.PlacementCount())
}

func (s *UpdateAcceptanceSuite) TestGivenTwoWritersWhenSecondUsesStaleBaseThenCurrentWins() {
	s.injectCreatedSQLite("create-sqlite")
	page, err := s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)
	revisionOneID := page.CurrentRevisionID

	firstUpdate := pagewiki.PagePublication{
		Page: pagewiki.Page{
			ID:                page.ID,
			Slug:              page.Slug,
			Title:             page.Title,
			CurrentRevisionID: "revision-two",
		},
		Revision: pagewiki.PageRevision{
			ID:             "revision-two",
			PageID:         page.ID,
			BaseRevisionID: revisionOneID,
			Title:          page.Title,
			Markdown:       "# SQLite\n\nWriter one.",
		},
	}
	s.Require().NoError(s.repository.PublishPage(s.ctx, firstUpdate))
	staleUpdate := pagewiki.PagePublication{
		Page: pagewiki.Page{
			ID:                page.ID,
			Slug:              page.Slug,
			Title:             page.Title,
			CurrentRevisionID: "revision-stale",
		},
		Revision: pagewiki.PageRevision{
			ID:             "revision-stale",
			PageID:         page.ID,
			BaseRevisionID: revisionOneID,
			Title:          page.Title,
			Markdown:       "# SQLite\n\nWriter two.",
		},
	}

	err = s.repository.PublishPage(s.ctx, staleUpdate)

	s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict)
	current, err := s.repository.PageByID(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Require().Equal("revision-two", current.CurrentRevisionID)
	_, err = s.repository.PageRevision(s.ctx, "revision-stale")
	s.Require().ErrorIs(err, pagewiki.ErrNotFound)
}

func (s *UpdateAcceptanceSuite) TestGivenCompletedInjectionWhenRetriedThenItIsIdempotent() {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)
	request := multiPageSource()
	request.IdempotencyKey = "multi-page-injection"

	first, err := service.InjectSession(s.ctx, request)
	s.Require().NoError(err)
	second, err := service.InjectSession(s.ctx, request)

	s.Require().NoError(err)
	s.Require().Equal(first, second)
	s.Require().Equal(1, s.repository.SourceRevisionCount())
	s.Require().Equal(2, s.repository.PageCount())
	s.Require().Equal(2, s.repository.PageRevisionCount())
	s.Require().Equal(1, s.repository.MaintenanceRunCount())
}

func (s *UpdateAcceptanceSuite) TestGivenEquivalentUpdateWhenInjectedThenNoRevisionIsAdded() {
	s.injectCreatedSQLite("create-sqlite")
	page, err := s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)
	revisionOneID := page.CurrentRevisionID
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
			{
				Key:                    "sqlite-no-op",
				Action:                 pagewiki.PageActionUpdate,
				TargetPageID:           page.ID,
				ExpectedBaseRevisionID: revisionOneID,
				EvidenceEventIDs:       []string{"event-storage"},
			},
		}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"sqlite-no-op": multiPageDrafts(false)["sqlite"],
		}},
	)
	request := multiPageSource()
	request.IdempotencyKey = "equivalent-update"

	result, err := service.InjectSession(s.ctx, request)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Equal(revisionOneID, result.Run.Targets[0].PageRevisionID)
	s.Require().Equal(1, s.repository.PageRevisionCount())
	current, err := s.repository.PageByID(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Require().Equal(revisionOneID, current.CurrentRevisionID)
}

func (s *UpdateAcceptanceSuite) injectCreatedSQLite(
	idempotencyKey string,
) pagewiki.InjectResult {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()[:1]},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)
	request := multiPageSource()
	request.IdempotencyKey = idempotencyKey

	result, err := service.InjectSession(s.ctx, request)

	s.Require().NoError(err)
	return result
}

func walSource(idempotencyKey string) pagewiki.InjectSessionRequest {
	raw := "event-wal: SQLite now runs in WAL mode."
	eventText := "SQLite now runs in WAL mode."
	start := strings.Index(raw, eventText)
	return pagewiki.InjectSessionRequest{
		SourceID:       "session-wal",
		IdempotencyKey: idempotencyKey,
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{
			{
				ID:        "event-wal",
				StartByte: start,
				EndByte:   start + len(eventText),
			},
		},
	}
}

func sqliteWALDraft() pagewiki.PageDraft {
	return pagewiki.PageDraft{
		Slug:    "sqlite",
		Title:   "SQLite",
		Summary: "SQLite is the local Wiki store and now uses WAL mode.",
		Sections: []pagewiki.SectionDraft{
			{
				Key:      "decision",
				Heading:  "Decision",
				Markdown: "SQLite now runs in WAL mode.",
			},
		},
		Citations: []pagewiki.CitationDraft{
			{
				SectionKey: "decision",
				ExactText:  "WAL mode",
				Evidence: []pagewiki.EvidenceQuoteDraft{
					{
						EventID:   "event-wal",
						ExactText: "SQLite now runs in WAL mode.",
					},
				},
			},
		},
	}
}
