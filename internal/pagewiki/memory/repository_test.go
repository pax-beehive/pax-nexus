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
	s.Require().Equal("SQLite", stored.Links[0].ExactText)

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

func (s *RepositorySuite) TestPageCatalogCarriesCurrentSummary() {
	page, revision := pageFixture()
	revision.Summary = "Weekly release cadence."
	publication := publicationFixture(page, revision)

	err := s.repository.PublishPage(s.ctx, publication)
	s.Require().NoError(err)

	catalog, err := s.repository.PageCatalog(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(catalog, 1)
	s.Equal("Weekly release cadence.", catalog[0].Summary)
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

func (s *RepositorySuite) TestGivenInvalidLinkWhenPublishedThenNothingIsStored() {
	page, revision := pageFixture()
	tests := []struct {
		name   string
		mutate func(*pagewiki.PageRevision)
	}{
		{
			name: "target Page is unknown",
			mutate: func(value *pagewiki.PageRevision) {
				value.Links[0].TargetPageID = "page-missing"
			},
		},
		{
			name: "revision identity differs",
			mutate: func(value *pagewiki.PageRevision) {
				value.Links[0].PageRevisionID = "revision-other"
			},
		},
		{
			name: "Section is unknown",
			mutate: func(value *pagewiki.PageRevision) {
				value.Links[0].SectionKey = "missing"
			},
		},
		{
			name: "exact range differs",
			mutate: func(value *pagewiki.PageRevision) {
				value.Links[0].StartByte = 1
			},
		},
		{
			name: "exact text is repeated",
			mutate: func(value *pagewiki.PageRevision) {
				value.Sections[0].Markdown = "SQLite and SQLite."
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.repository = memory.NewRepository()
			changed := revision
			changed.Sections = append([]pagewiki.PageSection(nil), revision.Sections...)
			changed.Links = append([]pagewiki.PageLink(nil), revision.Links...)
			tt.mutate(&changed)
			publication := publicationFixture(page, changed)

			err := s.repository.PublishPage(s.ctx, publication)

			s.Require().ErrorIs(err, pagewiki.ErrInvalidLink)
			s.Require().Zero(s.repository.PageCount())
			s.Require().Zero(s.repository.PageRevisionCount())
			s.Require().Zero(s.repository.SearchChunkCount())
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

func (s *RepositorySuite) TestGivenEqualScoresWhenSearchedThenResultsAndLinksAreSorted() {
	sqlitePage, sqliteRevision := searchablePageFixture(
		"page-sqlite",
		"sqlite",
		"SQLite",
		"revision-sqlite",
	)
	architecturePage, architectureRevision := searchablePageFixture(
		"page-architecture",
		"architecture",
		"Architecture",
		"revision-architecture",
	)
	s.Require().NoError(s.repository.PublishPage(
		s.ctx,
		publicationFixture(sqlitePage, sqliteRevision),
	))
	s.Require().NoError(s.repository.PublishPage(
		s.ctx,
		publicationFixture(architecturePage, architectureRevision),
	))
	hubPage := pagewiki.Page{
		ID:                "page-hub",
		Slug:              "hub",
		Title:             "Hub",
		CurrentRevisionID: "revision-hub",
	}
	hubRevision := pagewiki.PageRevision{
		ID:     "revision-hub",
		PageID: hubPage.ID,
		Title:  hubPage.Title,
		Sections: []pagewiki.PageSection{
			{
				Key:      "links",
				Heading:  "Links",
				Markdown: "SQLite and Architecture.",
			},
		},
		Links: []pagewiki.PageLink{
			{
				ID:             "link-sqlite",
				PageRevisionID: "revision-hub",
				SectionKey:     "links",
				StartByte:      0,
				EndByte:        6,
				ExactText:      "SQLite",
				TargetPageID:   sqlitePage.ID,
			},
			{
				ID:             "link-architecture",
				PageRevisionID: "revision-hub",
				SectionKey:     "links",
				StartByte:      11,
				EndByte:        23,
				ExactText:      "Architecture",
				TargetPageID:   architecturePage.ID,
			},
		},
	}
	s.Require().NoError(s.repository.PublishPage(
		s.ctx,
		publicationFixture(hubPage, hubRevision),
	))

	results, err := s.repository.Search(s.ctx, "SQLite SQLite")

	s.Require().NoError(err)
	s.Require().Equal(
		[]string{"architecture", "hub", "sqlite"},
		[]string{results[0].Page.Slug, results[1].Page.Slug, results[2].Page.Slug},
	)
	results[1].Links[0].ExactText = "Changed"
	again, err := s.repository.Search(s.ctx, "SQLite")
	s.Require().NoError(err)
	s.Require().Equal("SQLite", again[1].Links[0].ExactText)
	links, err := s.repository.PageLinks(s.ctx, hubPage.ID)
	s.Require().NoError(err)
	s.Require().Equal(
		[]string{"architecture", "sqlite"},
		[]string{links.Outgoing[0].TargetPage.Slug, links.Outgoing[1].TargetPage.Slug},
	)
	sqliteLinks, err := s.repository.PageLinks(s.ctx, sqlitePage.ID)
	s.Require().NoError(err)
	s.Require().Equal(
		[]string{"hub", "sqlite"},
		[]string{
			sqliteLinks.Incoming[0].SourcePage.Slug,
			sqliteLinks.Incoming[1].SourcePage.Slug,
		},
	)
}

func (s *RepositorySuite) TestGivenInvalidQueriesWhenReadThenErrorsAreReturned() {
	_, searchErr := s.repository.Search(s.ctx, " -- ")
	_, linksErr := s.repository.PageLinks(s.ctx, "missing")
	_, backlinksErr := s.repository.SourceBacklinks(s.ctx, "missing")

	s.Require().ErrorIs(searchErr, pagewiki.ErrInvalidSearch)
	s.Require().ErrorIs(linksErr, pagewiki.ErrNotFound)
	s.Require().ErrorIs(backlinksErr, pagewiki.ErrNotFound)
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

func (s *RepositorySuite) TestReplaceTopicTreeSwapsWholeTree() {
	// publish two pages (existing helper)
	page1, revision1 := pageFixture()
	s.Require().NoError(s.repository.PublishPage(s.ctx, publicationFixture(page1, revision1)))

	page2 := pagewiki.Page{
		ID:                "page-2",
		Slug:              "architecture",
		Title:             "Architecture",
		CurrentRevisionID: "revision-2",
	}
	revision2 := pagewiki.PageRevision{ID: "revision-2", PageID: "page-2"}
	s.Require().NoError(s.repository.PublishPage(s.ctx, publicationFixture(page2, revision2)))

	first := pagewiki.TopicTree{
		Topics:     []pagewiki.Topic{{ID: "topic-a", Slug: "runtime", Title: "Runtime"}},
		Placements: []pagewiki.PagePlacement{{PageID: "page-1", TopicID: "topic-a"}},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(s.ctx, first))

	second := pagewiki.TopicTree{
		Topics:     []pagewiki.Topic{{ID: "topic-b", Slug: "storage", Title: "Storage"}},
		Placements: []pagewiki.PagePlacement{{PageID: "page-2", TopicID: "topic-b"}},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(s.ctx, second))

	tree, err := s.repository.TopicTree(s.ctx)
	s.Require().NoError(err)
	s.Equal(second, tree)
}

func (s *RepositorySuite) TestReplaceTopicTreeRejectsInvalidTrees() {
	// Publish pages so we can reference them in placements (without topics)
	page1, revision1 := pageFixture()
	publication1 := publicationFixture(page1, revision1)
	publication1.Topics = nil // clear topics to avoid interference
	publication1.Placement = nil
	s.Require().NoError(s.repository.PublishPage(s.ctx, publication1))

	page2 := pagewiki.Page{
		ID:                "page-2",
		Slug:              "architecture",
		Title:             "Architecture",
		CurrentRevisionID: "revision-2",
	}
	revision2 := pagewiki.PageRevision{ID: "revision-2", PageID: "page-2"}
	publication2 := publicationFixture(page2, revision2)
	publication2.Topics = nil // clear topics to avoid interference
	publication2.Placement = nil
	s.Require().NoError(s.repository.PublishPage(s.ctx, publication2))

	cases := []struct {
		name string
		tree pagewiki.TopicTree
	}{
		{"unknown page", pagewiki.TopicTree{
			Topics:     []pagewiki.Topic{{ID: "t", Slug: "s", Title: "S"}},
			Placements: []pagewiki.PagePlacement{{PageID: "ghost", TopicID: "t"}},
		}},
		{"unknown topic", pagewiki.TopicTree{
			Placements: []pagewiki.PagePlacement{{PageID: "page-1", TopicID: "ghost"}},
		}},
		{"missing parent", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{{ID: "t", ParentID: "ghost", Slug: "s", Title: "S"}},
		}},
		{"three levels", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{
				{ID: "a", Slug: "a", Title: "A"},
				{ID: "b", ParentID: "a", Slug: "b", Title: "B"},
				{ID: "c", ParentID: "b", Slug: "c", Title: "C"},
			},
		}},
		{"duplicate placement", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{{ID: "t", Slug: "s", Title: "S"}},
			Placements: []pagewiki.PagePlacement{
				{PageID: "page-1", TopicID: "t"},
				{PageID: "page-1", TopicID: "t"},
			},
		}},
		{"empty topic ID", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{{ID: "", Slug: "s", Title: "S"}},
		}},
		{"empty topic slug", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{{ID: "t", Slug: "", Title: "S"}},
		}},
		{"empty topic title", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{{ID: "t", Slug: "s", Title: ""}},
		}},
		{"duplicate topic IDs", pagewiki.TopicTree{
			Topics: []pagewiki.Topic{
				{ID: "t", Slug: "s1", Title: "S1"},
				{ID: "t", Slug: "s2", Title: "S2"},
			},
		}},
		{"negative placement rank", pagewiki.TopicTree{
			Topics:     []pagewiki.Topic{{ID: "t", Slug: "s", Title: "S"}},
			Placements: []pagewiki.PagePlacement{{PageID: "page-1", TopicID: "t", Rank: -1}},
		}},
	}
	for _, testCase := range cases {
		err := s.repository.ReplaceTopicTree(s.ctx, testCase.tree)
		s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict, testCase.name)
	}
	// prior valid state is untouched (should be empty since no successful ReplaceTopicTree calls)
	tree, err := s.repository.TopicTree(s.ctx)
	s.Require().NoError(err)
	s.Empty(tree.Topics)
}

func (s *RepositorySuite) TestReplaceTopicTreeAtomicityPreservesValidState() {
	// Publish pages so we can reference them
	page1, revision1 := pageFixture()
	publication1 := publicationFixture(page1, revision1)
	publication1.Topics = nil
	publication1.Placement = nil
	s.Require().NoError(s.repository.PublishPage(s.ctx, publication1))

	page2 := pagewiki.Page{
		ID:                "page-2",
		Slug:              "architecture",
		Title:             "Architecture",
		CurrentRevisionID: "revision-2",
	}
	revision2 := pagewiki.PageRevision{ID: "revision-2", PageID: "page-2"}
	publication2 := publicationFixture(page2, revision2)
	publication2.Topics = nil
	publication2.Placement = nil
	s.Require().NoError(s.repository.PublishPage(s.ctx, publication2))

	// Step 1: Perform successful ReplaceTopicTree with valid non-empty tree
	validTree := pagewiki.TopicTree{
		Topics: []pagewiki.Topic{
			{ID: "topic-1", Slug: "runtime", Title: "Runtime"},
			{ID: "topic-2", ParentID: "topic-1", Slug: "memory", Title: "Memory"},
		},
		Placements: []pagewiki.PagePlacement{
			{PageID: "page-1", TopicID: "topic-1", Rank: 0},
			{PageID: "page-2", TopicID: "topic-2", Rank: 1},
		},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(s.ctx, validTree))

	// Step 2: Attempt invalid ReplaceTopicTree (references non-existent topic)
	invalidTree := pagewiki.TopicTree{
		Topics: []pagewiki.Topic{{ID: "t", Slug: "s", Title: "S"}},
		Placements: []pagewiki.PagePlacement{
			{PageID: "page-1", TopicID: "ghost"}, // ghost topic doesn't exist
		},
	}
	err := s.repository.ReplaceTopicTree(s.ctx, invalidTree)
	s.Require().ErrorIs(err, pagewiki.ErrRevisionConflict)

	// Step 3: Verify the valid tree is still there (atomicity preserved)
	tree, err := s.repository.TopicTree(s.ctx)
	s.Require().NoError(err)
	s.Equal(validTree, tree)
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
			{
				ID:             "link-1",
				PageRevisionID: "revision-1",
				SectionKey:     "decision",
				StartByte:      0,
				EndByte:        6,
				ExactText:      "SQLite",
				TargetPageID:   "page-1",
			},
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

func searchablePageFixture(
	pageID string,
	slug string,
	title string,
	revisionID string,
) (pagewiki.Page, pagewiki.PageRevision) {
	page := pagewiki.Page{
		ID:                pageID,
		Slug:              slug,
		Title:             title,
		CurrentRevisionID: revisionID,
	}
	revision := pagewiki.PageRevision{
		ID:     revisionID,
		PageID: pageID,
		Title:  title,
		Sections: []pagewiki.PageSection{
			{
				Key:      "decision",
				Heading:  "Decision",
				Markdown: "SQLite is local.",
			},
		},
		Links: []pagewiki.PageLink{
			{
				ID:             "link-" + pageID,
				PageRevisionID: revisionID,
				SectionKey:     "decision",
				StartByte:      0,
				EndByte:        6,
				ExactText:      "SQLite",
				TargetPageID:   pageID,
			},
		},
	}
	return page, revision
}
