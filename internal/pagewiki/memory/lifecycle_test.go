package memory_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type LifecycleSuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
}

func TestLifecycleSuite(t *testing.T) {
	suite.Run(t, new(LifecycleSuite))
}

func (s *LifecycleSuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
}

func (s *LifecycleSuite) TestGivenPublishedPageWhenRetiredThenCatalogNavigationSearchExcludeIt() {
	page, revision := lifecyclePageFixture()
	s.Require().NoError(s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page:     page,
		Revision: revision,
	}))

	err := s.repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 "page-1",
		ExpectedBaseRevisionID: "rev-1",
		SuccessorPageID:        "page-2",
		RunID:                  "run-1",
	})
	s.Require().NoError(err)

	catalog, err := s.repository.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(catalog)

	navigation, err := s.repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(navigation.Roots)
	s.Require().Empty(navigation.Pages)

	results, err := s.repository.Search(s.ctx, "body")
	s.Require().NoError(err)
	s.Require().Empty(results)

	stored, err := s.repository.PageByID(s.ctx, "page-1")
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.PageStatusRetired, stored.Status)
	s.Require().True(stored.Retired())
	s.Require().Equal("page-2", stored.SuccessorPageID)
	s.Require().Equal("run-1", stored.RetiredByRunID)

	history, err := s.repository.PageRevisionHistory(s.ctx, "page-1")
	s.Require().NoError(err)
	s.Require().Len(history, 1)
	s.Require().Equal("rev-1", history[0].ID)
}

func (s *LifecycleSuite) TestGivenStaleRevisionWhenRetiredThenRevisionConflict() {
	page, revision := lifecyclePageFixture()
	s.Require().NoError(s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page:     page,
		Revision: revision,
	}))

	err := s.repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 "page-1",
		ExpectedBaseRevisionID: "rev-stale",
		RunID:                  "run-1",
	})

	s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict)
	stored, err := s.repository.PageByID(s.ctx, "page-1")
	s.Require().NoError(err)
	s.Require().False(stored.Retired())
}

func (s *LifecycleSuite) TestGivenUnknownPageWhenRetiredThenNotFound() {
	err := s.repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 "missing",
		ExpectedBaseRevisionID: "rev-1",
		RunID:                  "run-1",
	})

	s.Require().ErrorIs(err, pagewiki.ErrNotFound)
}

func (s *LifecycleSuite) TestGivenAlreadyRetiredPageWhenRetiredAgainThenConflictAndSuccessorUnchanged() {
	page, revision := lifecyclePageFixture()
	s.Require().NoError(s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page:     page,
		Revision: revision,
	}))
	s.Require().NoError(s.repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 "page-1",
		ExpectedBaseRevisionID: "rev-1",
		SuccessorPageID:        "page-2",
		RunID:                  "run-1",
	}))

	// A second retire attempt against the same page — e.g. a different
	// candidate lane re-discovering an already-merged page in the same
	// curation round — must not silently succeed and wipe the successor: CAS
	// on ExpectedBaseRevisionID alone would pass here since retiring never
	// changes CurrentRevisionID, so an explicit already-retired guard is
	// required.
	err := s.repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 "page-1",
		ExpectedBaseRevisionID: "rev-1",
		SuccessorPageID:        "",
		RunID:                  "run-2",
	})
	s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict)

	stored, err := s.repository.PageByID(s.ctx, "page-1")
	s.Require().NoError(err)
	s.Require().True(stored.Retired())
	s.Require().Equal("page-2", stored.SuccessorPageID, "second retire must not overwrite the successor")
	s.Require().Equal("run-1", stored.RetiredByRunID, "second retire must not overwrite the retiring run")
}

func (s *LifecycleSuite) TestGivenRetiredPageWhenUpdatePublishedThenConflict() {
	page, revision := lifecyclePageFixture()
	s.Require().NoError(s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page:     page,
		Revision: revision,
	}))
	s.Require().NoError(s.repository.RetirePage(s.ctx, pagewiki.RetireRequest{
		PageID:                 "page-1",
		ExpectedBaseRevisionID: "rev-1",
		SuccessorPageID:        "page-2",
		RunID:                  "run-1",
	}))
	retired, err := s.repository.PageByID(s.ctx, "page-1")
	s.Require().NoError(err)
	s.Require().True(retired.Retired())

	updateRevision := pagewiki.PageRevision{
		ID:             "rev-2",
		PageID:         "page-1",
		BaseRevisionID: "rev-1",
		Title:          "T2",
		Summary:        "S2",
		Sections: []pagewiki.PageSection{
			{Key: "k", Heading: "H", Markdown: "body two"},
		},
		Markdown: "# T2",
	}
	updatedPage := retired
	updatedPage.CurrentRevisionID = "rev-2"

	err = s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page:     updatedPage,
		Revision: updateRevision,
	})
	s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict)
	stillRetired, err := s.repository.PageByID(s.ctx, "page-1")
	s.Require().NoError(err)
	s.Require().True(stillRetired.Retired())

	err = s.repository.PublishPage(s.ctx, pagewiki.PagePublication{
		Page:     updatedPage,
		Revision: updateRevision,
		Revive:   true,
	})
	s.Require().NoError(err)

	revived, err := s.repository.PageByID(s.ctx, "page-1")
	s.Require().NoError(err)
	s.Require().False(revived.Retired())
	s.Require().Empty(revived.SuccessorPageID)
	s.Require().Empty(revived.RetiredByRunID)
	s.Require().Equal("rev-2", revived.CurrentRevisionID)
}

func lifecyclePageFixture() (pagewiki.Page, pagewiki.PageRevision) {
	page := pagewiki.Page{
		ID:                "page-1",
		Slug:              "t",
		Title:             "T",
		CurrentRevisionID: "rev-1",
	}
	revision := pagewiki.PageRevision{
		ID:      "rev-1",
		PageID:  "page-1",
		Title:   "T",
		Summary: "S",
		Sections: []pagewiki.PageSection{
			{Key: "k", Heading: "H", Markdown: "body"},
		},
		Markdown: "# T",
	}
	return page, revision
}
