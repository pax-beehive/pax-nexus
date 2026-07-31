package pagewiki

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	domain "github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type DriverSuite struct {
	suite.Suite
	ctx    context.Context
	store  *knowledgeeval.ArtifactStore
	driver *Driver
	group  knowledgeeval.BenchmarkGroup
}

func TestDriverSuite(t *testing.T) {
	suite.Run(t, new(DriverSuite))
}

func (s *DriverSuite) SetupTest() {
	s.ctx = context.Background()
	var err error
	s.store, err = knowledgeeval.NewArtifactStore(s.T().TempDir())
	s.Require().NoError(err)
	s.driver, err = NewDriver(s.store, func() time.Time {
		return time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	})
	s.Require().NoError(err)
	s.group = knowledgeeval.BenchmarkGroup{
		GroupID: "group", WorldID: "world", CheckpointID: "checkpoint",
	}
}

func (s *DriverSuite) TestPublishesAndExposesAllCapabilities() {
	artifact, err := s.driver.Publish(s.ctx, validSnapshot(), s.group, knowledgeeval.Provenance{})
	s.Require().NoError(err)
	opened, err := s.driver.Open(s.ctx, artifact)
	s.Require().NoError(err)
	s.Len(opened.Capabilities(), 4)

	projector, ok := opened.(knowledgeeval.Projector)
	s.Require().True(ok)
	projection, err := projector.Project(s.ctx, knowledgeeval.ProjectionRequest{
		Name: knowledgeeval.WikiCorpusCapability, Version: "v1",
	})
	s.Require().NoError(err)
	encoded, err := s.store.OpenBytes(s.ctx, projection.Payload)
	s.Require().NoError(err)
	var corpus knowledgeeval.WikiCorpus
	s.Require().NoError(json.Unmarshal(encoded, &corpus))
	s.Require().Len(corpus.Documents, 2)
	s.Equal("architecture", corpus.Documents[0].Ref)

	searcher, ok := opened.(knowledgeeval.Searcher)
	s.Require().True(ok)
	search, err := searcher.Search(s.ctx, knowledgeeval.SearchRequest{
		Query: "local-first architecture", MaxItems: 1, TokenBudget: 100,
	})
	s.Require().NoError(err)
	s.Require().Len(search.Hits, 1)
	s.Equal("architecture", search.Hits[0].Ref)
	getter, ok := opened.(knowledgeeval.Getter)
	s.Require().True(ok)
	page, err := getter.Get(s.ctx, knowledgeeval.GetRequest{Ref: "architecture"})
	s.Require().NoError(err)
	s.Contains(page.Text, "local-first")
	navigator, ok := opened.(knowledgeeval.Navigator)
	s.Require().True(ok)
	navigation, err := navigator.Navigate(s.ctx, knowledgeeval.NavigateRequest{})
	s.Require().NoError(err)
	s.Require().Len(navigation.Roots, 1)
	s.Len(navigation.Roots[0].Children, 2)
}

func (s *DriverSuite) TestRendersViewsAndRejectsBadRequests() {
	artifact, err := s.driver.Publish(s.ctx, validSnapshot(), s.group, knowledgeeval.Provenance{})
	s.Require().NoError(err)
	for _, kind := range []string{"native", "canonical", "raw"} {
		view, err := s.driver.RenderView(s.ctx, knowledgeeval.ArtifactViewRequest{
			Artifact: artifact, Kind: kind,
		})
		s.Require().NoError(err)
		content, err := s.store.OpenBytes(s.ctx, view.Payload)
		s.Require().NoError(err)
		s.Contains(string(content), "Architecture")
	}
	_, err = s.driver.RenderView(s.ctx, knowledgeeval.ArtifactViewRequest{
		Artifact: artifact, Kind: "diff",
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = s.driver.Open(s.ctx, knowledgeeval.ArtifactRecord{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *DriverSuite) TestValidatesSnapshotAndLookupErrors() {
	_, err := s.driver.Publish(s.ctx, Snapshot{}, s.group, knowledgeeval.Provenance{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	bad := validSnapshot()
	bad.Pages[0].CurrentRevisionID = "missing"
	_, err = s.driver.Publish(s.ctx, bad, s.group, knowledgeeval.Provenance{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	artifact, err := s.driver.Publish(s.ctx, validSnapshot(), s.group, knowledgeeval.Provenance{})
	s.Require().NoError(err)
	opened, err := s.driver.Open(s.ctx, artifact)
	s.Require().NoError(err)
	getter, ok := opened.(knowledgeeval.Getter)
	s.Require().True(ok)
	_, err = getter.Get(s.ctx, knowledgeeval.GetRequest{Ref: "missing"})
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
	navigator, ok := opened.(knowledgeeval.Navigator)
	s.Require().True(ok)
	_, err = navigator.Navigate(s.ctx, knowledgeeval.NavigateRequest{Ref: "missing"})
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
	searcher, ok := opened.(knowledgeeval.Searcher)
	s.Require().True(ok)
	_, err = searcher.Search(s.ctx, knowledgeeval.SearchRequest{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	projector, ok := opened.(knowledgeeval.Projector)
	s.Require().True(ok)
	_, err = projector.Project(s.ctx, knowledgeeval.ProjectionRequest{Name: "other"})
	s.Require().ErrorIs(err, knowledgeeval.ErrCapabilityMissing)
}

func validSnapshot() Snapshot {
	return Snapshot{
		Pages: []domain.Page{
			{ID: "p1", Slug: "architecture", Title: "Architecture", CurrentRevisionID: "r1"},
			{ID: "p2", Slug: "roadmap", Title: "Roadmap", CurrentRevisionID: "r2"},
		},
		Revisions: []domain.PageRevision{
			{
				ID: "r1", PageID: "p1", Title: "Architecture",
				Summary: "System design", Markdown: "The architecture is local-first.",
				Citations: []domain.PageCitation{{SourceAnchors: []domain.SourceAnchor{{
					ID: "a1", SourceRevisionID: "source-1",
				}}}},
				Links: []domain.PageLink{{TargetPageID: "p2"}},
			},
			{
				ID: "r2", PageID: "p2", Title: "Roadmap",
				Summary: "Delivery plan", Markdown: "Ship the evaluation platform.",
				Citations: []domain.PageCitation{{SourceAnchors: []domain.SourceAnchor{{
					ID: "a2", SourceRevisionID: "source-1",
				}}}},
			},
		},
		TopicTree: domain.TopicTree{
			Topics: []domain.Topic{{ID: "t1", Slug: "eval", Title: "Evaluation"}},
			Placements: []domain.PagePlacement{
				{PageID: "p1", TopicID: "t1", Rank: 1},
				{PageID: "p2", TopicID: "t1", Rank: 2},
			},
		},
	}
}
