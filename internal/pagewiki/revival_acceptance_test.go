package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type RevivalAcceptanceSuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
}

func TestRevivalAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(RevivalAcceptanceSuite))
}

func (s *RevivalAcceptanceSuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
}

// retireSQLite creates the "sqlite" Page via the multi-page fixture and then
// retires it, returning the Page as it stood right before retirement so
// tests can assert against its ID and revision lineage.
func (s *RevivalAcceptanceSuite) retireSQLite(runID string) pagewiki.Page {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()[:1]},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)
	request := multiPageSource()
	request.IdempotencyKey = "create-sqlite-for-revival"
	_, err := service.InjectSession(s.ctx, request)
	s.Require().NoError(err)

	page, err := s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)

	s.Require().NoError(s.repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 page.ID,
		ExpectedBaseRevisionID: page.CurrentRevisionID,
		RunID:                  runID,
	}))
	retired, err := s.repository.PageByID(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Require().True(retired.Retired())
	return page
}

func (s *RevivalAcceptanceSuite) TestGivenRetiredPageWhenCreateBriefReusesSlugThenPageIsRevived() {
	retiredPage := s.retireSQLite("retire-run")

	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
			{
				Key:              "sqlite-revival",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "sqlite",
				ProposedTitle:    "SQLite",
				EvidenceEventIDs: []string{"event-wal"},
			},
		}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"sqlite-revival": sqliteWALDraft(),
		}},
	)

	result, err := service.InjectSession(s.ctx, walSource("revive-sqlite"))

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, result.Run.Targets[0].Status)

	revived, err := s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)
	s.Require().Equal(retiredPage.ID, revived.ID)
	s.Require().False(revived.Retired())
	s.Require().Empty(revived.SuccessorPageID)
	s.Require().Empty(revived.RetiredByRunID)

	newRevision, err := s.repository.PageRevision(s.ctx, revived.CurrentRevisionID)
	s.Require().NoError(err)
	s.Require().Equal(retiredPage.CurrentRevisionID, newRevision.BaseRevisionID)
	s.Require().Contains(newRevision.Markdown, "WAL mode")

	// No second Page with a suffixed slug was minted.
	s.Require().Equal(1, s.repository.PageCount())
}

func (s *RevivalAcceptanceSuite) TestGivenRetiredPageWhenCreateBriefWithIdenticalContentThenPageIsStillActivated() {
	retiredPage := s.retireSQLite("retire-run-noop")

	// The editor's draft reproduces the exact content of the current
	// (pre-retirement) revision, which would ordinarily trip the
	// revisionsEquivalent no-op short-circuit in commitTarget. A revive must
	// still flip the Page back to active even though no new revision is
	// needed content-wise.
	identicalDraft := multiPageDrafts(false)["sqlite"]

	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
			{
				Key:              "sqlite-revival-noop",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "sqlite",
				ProposedTitle:    "SQLite",
				EvidenceEventIDs: []string{"event-storage"},
			},
		}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"sqlite-revival-noop": identicalDraft,
		}},
	)
	request := multiPageSource()
	request.IdempotencyKey = "revive-sqlite-noop"

	result, err := service.InjectSession(s.ctx, request)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, result.Run.Targets[0].Status)

	revived, err := s.repository.PageByID(s.ctx, retiredPage.ID)
	s.Require().NoError(err)
	s.Require().False(revived.Retired(), "revive must land even when new content matches the old revision")
	s.Require().Empty(revived.SuccessorPageID)
	s.Require().Empty(revived.RetiredByRunID)
}

func (s *RevivalAcceptanceSuite) TestGivenActivePageWhenCreateBriefReusesSlugThenTargetFailsAsBefore() {
	service := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()[:1]},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)
	request := multiPageSource()
	request.IdempotencyKey = "create-sqlite-active"
	_, err := service.InjectSession(s.ctx, request)
	s.Require().NoError(err)
	activePage, err := s.repository.PageBySlug(s.ctx, "sqlite")
	s.Require().NoError(err)
	s.Require().False(activePage.Retired())

	conflictingService := pagewiki.NewService(
		s.repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
			{
				Key:              "sqlite-conflict",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "sqlite",
				ProposedTitle:    "SQLite",
				EvidenceEventIDs: []string{"event-wal"},
			},
		}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"sqlite-conflict": sqliteWALDraft(),
		}},
	)

	result, err := conflictingService.InjectSession(s.ctx, walSource("conflict-sqlite"))

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusFailed, result.Run.Status)
	s.Require().Equal(pagewiki.TargetStatusFailed, result.Run.Targets[0].Status)
	s.Require().Equal(
		pagewiki.TargetFailurePublicationConflict,
		result.Run.Targets[0].FailureReason,
	)
	s.Require().Equal(1, s.repository.PageCount())
	unchanged, err := s.repository.PageByID(s.ctx, activePage.ID)
	s.Require().NoError(err)
	s.Require().Equal(activePage.CurrentRevisionID, unchanged.CurrentRevisionID)
}
