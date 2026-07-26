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
	publication := publicationFixture(page, revision)

	err := s.repository.PublishPage(s.ctx, publication)
	s.Require().NoError(err)
	publication.Topics[0].Title = "Changed"
	publication.Placement.TopicID = "changed"
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
	history, err := s.repository.PageRevisionHistory(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Require().Len(history, 1)
	history[0].Sections[0].Markdown = "changed again"
	againHistory, err := s.repository.PageRevisionHistory(s.ctx, page.ID)
	s.Require().NoError(err)
	s.Require().Equal("SQLite is local.", againHistory[0].Sections[0].Markdown)
}

func (s *RepositorySuite) TestGivenInvalidPublicationWhenPublishedThenRepositoryRejectsIt() {
	page, revision := pageFixture()
	tests := []struct {
		name        string
		prepare     func()
		publication pagewiki.PagePublication
		wantErr     error
	}{
		{
			name: "page points at another revision",
			publication: publicationFixture(
				pagewiki.Page{ID: "page-1", Slug: "sqlite", CurrentRevisionID: "other"},
				revision,
			),
			wantErr: pagewiki.ErrRevisionConflict,
		},
		{
			name: "slug belongs to another page",
			prepare: func() {
				s.Require().NoError(
					s.repository.PublishPage(s.ctx, publicationFixture(page, revision)),
				)
			},
			publication: publicationFixture(
				pagewiki.Page{
					ID:                "page-2",
					Slug:              "sqlite",
					Title:             "Other",
					CurrentRevisionID: "revision-2",
				},
				pagewiki.PageRevision{
					ID:     "revision-2",
					PageID: "page-2",
				},
			),
			wantErr: pagewiki.ErrRevisionConflict,
		},
		{
			name: "update uses stale base",
			prepare: func() {
				s.Require().NoError(
					s.repository.PublishPage(s.ctx, publicationFixture(page, revision)),
				)
			},
			publication: pagewiki.PagePublication{
				Page: pagewiki.Page{
					ID:                "page-1",
					Slug:              "sqlite",
					Title:             "SQLite",
					CurrentRevisionID: "revision-2",
				},
				Revision: pagewiki.PageRevision{
					ID:             "revision-2",
					PageID:         "page-1",
					BaseRevisionID: "stale",
				},
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
			err := s.repository.PublishPage(s.ctx, tt.publication)
			s.Require().ErrorIs(err, tt.wantErr)
		})
	}
}

func (s *RepositorySuite) TestGivenInvalidPlacementWhenPublishedThenNothingIsStored() {
	page, revision := pageFixture()
	tests := []struct {
		name        string
		publication pagewiki.PagePublication
	}{
		{
			name: "placement names another page",
			publication: func() pagewiki.PagePublication {
				value := publicationFixture(page, revision)
				value.Placement.PageID = "page-other"
				return value
			}(),
		},
		{
			name: "placement topic is missing",
			publication: func() pagewiki.PagePublication {
				value := publicationFixture(page, revision)
				value.Placement.TopicID = "topic-missing"
				return value
			}(),
		},
		{
			name: "placement rank is negative",
			publication: func() pagewiki.PagePublication {
				value := publicationFixture(page, revision)
				value.Placement.Rank = -1
				return value
			}(),
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.repository = memory.NewRepository()

			err := s.repository.PublishPage(s.ctx, tt.publication)

			s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict)
			s.Require().Zero(s.repository.PageCount())
			s.Require().Zero(s.repository.PageRevisionCount())
			s.Require().Zero(s.repository.TopicCount())
			s.Require().Zero(s.repository.PlacementCount())
		})
	}
}

func (s *RepositorySuite) TestGivenMissingParentTopicWhenPublishedThenNothingIsStored() {
	page, revision := pageFixture()
	publication := publicationFixture(page, revision)
	publication.Topics[1].ParentID = "topic-missing"

	err := s.repository.PublishPage(s.ctx, publication)

	s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict)
	s.Require().Zero(s.repository.PageCount())
	s.Require().Zero(s.repository.TopicCount())
}

func (s *RepositorySuite) TestGivenExistingTopicWhenChangedThenPublicationFails() {
	page, revision := pageFixture()
	s.Require().NoError(
		s.repository.PublishPage(s.ctx, publicationFixture(page, revision)),
	)
	nextPage := pagewiki.Page{
		ID:                "page-2",
		Slug:              "search",
		Title:             "Search",
		CurrentRevisionID: "revision-2",
	}
	nextRevision := pagewiki.PageRevision{ID: "revision-2", PageID: "page-2"}
	publication := publicationFixture(nextPage, nextRevision)
	publication.Topics[0].Title = "Changed Engineering"

	err := s.repository.PublishPage(s.ctx, publication)

	s.Require().ErrorIs(err, pagewiki.ErrImmutableConflict)
	s.Require().Equal(1, s.repository.PageCount())
	s.Require().Equal(2, s.repository.TopicCount())
}

func (s *RepositorySuite) TestGivenPublicationsWhenNavigatedThenCopiesAreSorted() {
	page, revision := pageFixture()
	first := publicationFixture(page, revision)
	first.Placement.Rank = 2
	s.Require().NoError(s.repository.PublishPage(s.ctx, first))
	secondPage := pagewiki.Page{
		ID:                "page-2",
		Slug:              "architecture",
		Title:             "Architecture",
		CurrentRevisionID: "revision-2",
	}
	secondRevision := pagewiki.PageRevision{ID: "revision-2", PageID: "page-2"}
	second := publicationFixture(secondPage, secondRevision)
	second.Placement.Rank = 1
	s.Require().NoError(s.repository.PublishPage(s.ctx, second))

	navigation, err := s.repository.Navigation(s.ctx)

	s.Require().NoError(err)
	s.Require().Len(navigation.Roots, 1)
	s.Require().Equal("engineering", navigation.Roots[0].Slug)
	s.Require().Len(navigation.Roots[0].Children, 1)
	s.Require().Equal(
		[]string{"architecture", "sqlite"},
		[]string{
			navigation.Roots[0].Children[0].Pages[0].Slug,
			navigation.Roots[0].Children[0].Pages[1].Slug,
		},
	)
	navigation.Roots[0].Title = "Changed"
	navigation.Roots[0].Children[0].Pages[0].Title = "Changed"

	again, err := s.repository.Navigation(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal("Engineering", again.Roots[0].Title)
	s.Require().Equal("Architecture", again.Roots[0].Children[0].Pages[0].Title)
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
	stored, err := s.repository.MaintenanceRun(s.ctx, run.ID)
	s.Require().NoError(err)
	stored.Targets[0].Status = pagewiki.TargetStatusFailed
	again, err := s.repository.MaintenanceRun(s.ctx, run.ID)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, again.Targets[0].Status)
	run.Status = pagewiki.RunStatusFailed

	err = s.repository.SaveMaintenanceRun(s.ctx, run)

	s.Require().ErrorIs(err, pagewiki.ErrImmutableConflict)
}

func (s *RepositorySuite) TestGivenMissingValuesWhenReadThenNotFoundIsReturned() {
	_, sourceErr := s.repository.SourceRevision(s.ctx, "missing")
	_, pageErr := s.repository.PageByID(s.ctx, "missing")
	_, slugErr := s.repository.PageBySlug(s.ctx, "missing")
	_, revisionErr := s.repository.PageRevision(s.ctx, "missing")
	_, historyErr := s.repository.PageRevisionHistory(s.ctx, "missing")
	_, runErr := s.repository.MaintenanceRun(s.ctx, "missing")

	s.Require().ErrorIs(sourceErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(pageErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(slugErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(revisionErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(historyErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(runErr, pagewiki.ErrNotFound)
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

func publicationFixture(
	page pagewiki.Page,
	revision pagewiki.PageRevision,
) pagewiki.PagePublication {
	return pagewiki.PagePublication{
		Page:     page,
		Revision: revision,
		Topics: []pagewiki.Topic{
			{
				ID:    "topic-engineering",
				Slug:  "engineering",
				Title: "Engineering",
			},
			{
				ID:       "topic-storage",
				ParentID: "topic-engineering",
				Slug:     "storage",
				Title:    "Storage",
			},
		},
		Placement: &pagewiki.PagePlacement{
			PageID:  page.ID,
			TopicID: "topic-storage",
		},
	}
}
