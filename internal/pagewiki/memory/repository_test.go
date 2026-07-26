package memory_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type RepositorySuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
}

func TestRepositorySuite(t *testing.T) {
	suite.Run(t, new(RepositorySuite))
}

func (s *RepositorySuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
}

func (s *RepositorySuite) TestGivenSourceRevisionWhenSavedThenStoredBytesAreImmutable() {
	revision := sourceRevisionFixture()

	err := s.repository.SaveSourceRevision(s.ctx, revision)
	s.Require().NoError(err)
	revision.Raw[0] = 'X'
	revision.Events[0].ID = "changed"

	stored, err := s.repository.SourceRevision(s.ctx, "source-revision-1")
	s.Require().NoError(err)
	s.Require().Equal([]byte("event text"), stored.Raw)
	s.Require().Equal("event-1", stored.Events[0].ID)

	stored.Raw[0] = 'Y'
	again, err := s.repository.SourceRevision(s.ctx, "source-revision-1")
	s.Require().NoError(err)
	s.Require().Equal([]byte("event text"), again.Raw)
}

func (s *RepositorySuite) TestGivenExistingSourceRevisionWhenSameIdentityChangesThenSaveFails() {
	revision := sourceRevisionFixture()
	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, revision))
	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, revision))
	revision.Raw = []byte("different")

	err := s.repository.SaveSourceRevision(s.ctx, revision)

	s.Require().ErrorIs(err, pagewiki.ErrImmutableConflict)
}

func (s *RepositorySuite) TestGivenPagePublicationWhenReadThenNestedRevisionValuesAreCopied() {
	page, revision := pageFixture()

	err := s.repository.PublishPage(s.ctx, page, revision)
	s.Require().NoError(err)
	revision.Sections[0].Markdown = "changed"
	revision.Citations[0].SourceAnchors[0].ExactQuote = "changed"
	revision.Links[0].ExactText = "changed"

	stored, err := s.repository.PageRevision(s.ctx, "revision-1")
	s.Require().NoError(err)
	s.Require().Equal("SQLite is local.", stored.Sections[0].Markdown)
	s.Require().Equal("SQLite", stored.Citations[0].SourceAnchors[0].ExactQuote)
	s.Require().Equal("architecture", stored.Links[0].ExactText)

	catalog, err := s.repository.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.PageCatalog{
		{
			ID:                "page-1",
			Slug:              "sqlite",
			Title:             "SQLite",
			CurrentRevisionID: "revision-1",
		},
	}, catalog)
	byID, err := s.repository.PageByID(s.ctx, "page-1")
	s.Require().NoError(err)
	s.Require().Equal(page, byID)
}

func (s *RepositorySuite) TestGivenInvalidPublicationWhenPublishedThenRepositoryRejectsIt() {
	page, revision := pageFixture()
	tests := []struct {
		name     string
		prepare  func()
		page     pagewiki.Page
		revision pagewiki.PageRevision
		wantErr  error
	}{
		{
			name:     "page points at another revision",
			page:     pagewiki.Page{ID: "page-1", Slug: "sqlite", CurrentRevisionID: "other"},
			revision: revision,
			wantErr:  pagewiki.ErrRevisionConflict,
		},
		{
			name: "slug belongs to another page",
			prepare: func() {
				s.Require().NoError(s.repository.PublishPage(s.ctx, page, revision))
			},
			page: pagewiki.Page{
				ID:                "page-2",
				Slug:              "sqlite",
				Title:             "Other",
				CurrentRevisionID: "revision-2",
			},
			revision: pagewiki.PageRevision{
				ID:     "revision-2",
				PageID: "page-2",
			},
			wantErr: pagewiki.ErrRevisionConflict,
		},
		{
			name: "update uses stale base",
			prepare: func() {
				s.Require().NoError(s.repository.PublishPage(s.ctx, page, revision))
			},
			page: pagewiki.Page{
				ID:                "page-1",
				Slug:              "sqlite",
				Title:             "SQLite",
				CurrentRevisionID: "revision-2",
			},
			revision: pagewiki.PageRevision{
				ID:             "revision-2",
				PageID:         "page-1",
				BaseRevisionID: "stale",
			},
			wantErr: pagewiki.ErrRevisionConflict,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.repository = memory.NewRepository()
			if tt.prepare != nil {
				tt.prepare()
			}
			err := s.repository.PublishPage(s.ctx, tt.page, tt.revision)
			s.Require().ErrorIs(err, tt.wantErr)
		})
	}
}

func (s *RepositorySuite) TestGivenImmutableRunWhenChangedThenSaveFails() {
	run := pagewiki.MaintenanceRun{
		ID:               "run-1",
		SourceRevisionID: "source-revision-1",
		Status:           pagewiki.RunStatusSucceeded,
		Targets: []pagewiki.MaintenanceTarget{
			{ID: "target-1", Status: pagewiki.TargetStatusSucceeded},
		},
	}
	s.Require().NoError(s.repository.SaveMaintenanceRun(s.ctx, run))
	s.Require().NoError(s.repository.SaveMaintenanceRun(s.ctx, run))
	run.Status = pagewiki.RunStatusFailed

	err := s.repository.SaveMaintenanceRun(s.ctx, run)

	s.Require().ErrorIs(err, pagewiki.ErrImmutableConflict)
}

func (s *RepositorySuite) TestGivenMissingValuesWhenReadThenNotFoundIsReturned() {
	_, sourceErr := s.repository.SourceRevision(s.ctx, "missing")
	_, pageErr := s.repository.PageByID(s.ctx, "missing")
	_, slugErr := s.repository.PageBySlug(s.ctx, "missing")
	_, revisionErr := s.repository.PageRevision(s.ctx, "missing")

	s.Require().ErrorIs(sourceErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(pageErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(slugErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(revisionErr, pagewiki.ErrNotFound)
}

func sourceRevisionFixture() pagewiki.SourceRevision {
	return pagewiki.SourceRevision{
		ID:       "source-revision-1",
		SourceID: "session-1",
		SHA256:   "sha",
		Raw:      []byte("event text"),
		Events: []pagewiki.SourceEvent{
			{ID: "event-1", StartByte: 0, EndByte: 10},
		},
	}
}

func pageFixture() (pagewiki.Page, pagewiki.PageRevision) {
	page := pagewiki.Page{
		ID:                "page-1",
		Slug:              "sqlite",
		Title:             "SQLite",
		CurrentRevisionID: "revision-1",
	}
	revision := pagewiki.PageRevision{
		ID:       "revision-1",
		PageID:   "page-1",
		Title:    "SQLite",
		Markdown: "# SQLite",
		Sections: []pagewiki.PageSection{
			{Key: "decision", Heading: "Decision", Markdown: "SQLite is local."},
		},
		Citations: []pagewiki.PageCitation{
			{
				ID: "citation-1",
				SourceAnchors: []pagewiki.SourceAnchor{
					{ID: "anchor-1", ExactQuote: "SQLite"},
				},
			},
		},
		Links: []pagewiki.PageLink{
			{ID: "link-1", ExactText: "architecture"},
		},
	}
	return page, revision
}
