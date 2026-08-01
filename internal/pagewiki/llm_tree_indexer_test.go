package pagewiki_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type llmTreeIndexerSuite struct {
	suite.Suite
}

func TestLLMTreeIndexerSuite(t *testing.T) {
	suite.Run(t, new(llmTreeIndexerSuite))
}

func indexerCatalog(size int) pagewiki.PageCatalog {
	catalog := make(pagewiki.PageCatalog, 0, size)
	for index := 0; index < size; index++ {
		slug := fmt.Sprintf("page-%02d", index)
		catalog = append(catalog, pagewiki.PageCatalogEntry{
			ID: "id-" + slug, Slug: slug, Title: "Page " + slug,
			CurrentRevisionID: "revision-" + slug, Summary: "Summary of " + slug,
		})
	}
	return catalog
}

func newIndexer(s *llmTreeIndexerSuite, responses ...string) (*pagewiki.LLMTreeIndexer, *wikiChatClient) {
	client := &wikiChatClient{responsesByIndex: responses}
	indexer, err := pagewiki.NewLLMTreeIndexer(pagewiki.LLMTreeIndexerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	return indexer, client
}

func newDepthIndexer(
	s *llmTreeIndexerSuite, maxDepth int, responses ...string,
) (*pagewiki.LLMTreeIndexer, *wikiChatClient) {
	client := &wikiChatClient{responsesByIndex: responses}
	indexer, err := pagewiki.NewLLMTreeIndexer(pagewiki.LLMTreeIndexerConfig{
		Client: client, Model: "test-model", MaxDepth: maxDepth,
	})
	s.Require().NoError(err)
	return indexer, client
}

func (s *llmTreeIndexerSuite) TestBuildsTwoLevelTreeWithStableIDs() {
	indexer, client := newIndexer(s, `{"root_pages":["page-00"],"topics":[
		{"title":"Engineering","pages":["page-01","page-02","page-03"]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(4),
	})
	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 1)
	s.Equal("engineering", tree.Topics[0].Slug)
	s.Empty(tree.Topics[0].ParentID)
	s.Require().Len(tree.Placements, 3)
	s.Equal("id-page-01", tree.Placements[0].PageID)
	s.Equal(tree.Topics[0].ID, tree.Placements[0].TopicID)
	s.Equal(0, tree.Placements[0].Rank)
	s.Equal(1, tree.Placements[1].Rank)
	// request carried summaries and the current tree shape
	s.Contains(client.requests[0].Messages[1].Content, "Summary of page-01")
	s.Contains(client.requests[0].Messages[0].Content, "Misc")
}

func (s *llmTreeIndexerSuite) TestCollapsesUnderfullTopicsAndCoversEveryPage() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-01","page-02","page-03"],
		 "children":[{"title":"Tiny","pages":["page-04"]}]},
		{"title":"Lonely","pages":["page-05"]},
		{"title":"Ghost","pages":["missing-slug","page-01"]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(7),
	})
	s.Require().NoError(err)
	// "Tiny" (1 page) collapsed into Engineering; "Lonely" (1 page) collapsed
	// to root; "Ghost" kept nothing: unknown slug dropped, page-01 already placed.
	s.Require().Len(tree.Topics, 1)
	s.Equal("engineering", tree.Topics[0].Slug)
	s.Require().Len(tree.Placements, 4) // page-01..04 under Engineering
	placed := make(map[string]struct{})
	for _, placement := range tree.Placements {
		placed[placement.PageID] = struct{}{}
	}
	s.Contains(placed, "id-page-04")
	s.NotContains(placed, "id-page-05") // at root: page-00, 05, 06
}

func (s *llmTreeIndexerSuite) TestFlattensThirdLevelIntoSecond() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-00"],"children":[
			{"title":"Runtime","pages":["page-01","page-02"],"children":[
				{"title":"Deep","pages":["page-03"]}
			]}
		]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(4),
	})
	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 2)
	byID := make(map[string]pagewiki.Topic)
	for _, topic := range tree.Topics {
		byID[topic.ID] = topic
	}
	for _, placement := range tree.Placements {
		s.Contains(byID, placement.TopicID)
	}
	// page-03 landed under Runtime (level 2), not a third level
	var runtimeID string
	for _, topic := range tree.Topics {
		if topic.Slug == "runtime" {
			runtimeID = topic.ID
		}
	}
	counted := 0
	for _, placement := range tree.Placements {
		if placement.TopicID == runtimeID {
			counted++
		}
	}
	s.Equal(3, counted)
}

func (s *llmTreeIndexerSuite) TestRetriesOnceThenFails() {
	indexer, client := newIndexer(s, "not json", "still not json")
	_, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(2),
	})
	s.Require().Error(err)
	s.Len(client.requests, 2)
}

func (s *llmTreeIndexerSuite) TestRequestCarriesCurrentTree() {
	indexer, client := newIndexer(s, `{"root_pages":["page-00"],"topics":[]}`)
	current := pagewiki.TopicTree{
		Topics: []pagewiki.Topic{
			{ID: "topic-1", Slug: "existing", Title: "Existing Topic"},
		},
		Placements: []pagewiki.PagePlacement{
			{PageID: "id-page-00", TopicID: "topic-1", Rank: 0},
		},
	}
	_, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(1),
		Current: current,
	})
	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	s.Contains(client.requests[0].Messages[1].Content, "Existing Topic")
}

// Three-level response survives intact under the default depth (5): each
// level gets its own topic with a parent-chained stable ID and placements.
func (s *llmTreeIndexerSuite) TestKeepsThreeLevelTreeUnderDefaultDepth() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-00","page-01","page-02"],"children":[
			{"title":"Backend","pages":["page-03","page-04","page-05"],"children":[
				{"title":"Storage","pages":["page-06","page-07","page-08"]}
			]}
		]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(9),
	})

	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 3)
	engineering, backend, storage := tree.Topics[0], tree.Topics[1], tree.Topics[2]
	s.Empty(engineering.ParentID)
	s.Equal(engineering.ID, backend.ParentID)
	s.Equal(backend.ID, storage.ParentID)
	s.Equal("storage", storage.Slug)
	s.Len(tree.Placements, 9)
	placementsByTopic := make(map[string]int)
	for _, placement := range tree.Placements {
		placementsByTopic[placement.TopicID]++
	}
	s.Equal(3, placementsByTopic[storage.ID])
}

// Levels beyond MaxDepth flatten into the deepest kept topic.
func (s *llmTreeIndexerSuite) TestFlattensLevelsBeyondMaxDepth() {
	indexer, _ := newDepthIndexer(s, 2, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-00","page-01","page-02"],"children":[
			{"title":"Backend","pages":["page-03","page-04","page-05"],"children":[
				{"title":"Storage","pages":["page-06","page-07","page-08"]}
			]}
		]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(9),
	})

	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 2)
	backend := tree.Topics[1]
	s.Equal("backend", backend.Slug)
	placementsByTopic := make(map[string]int)
	for _, placement := range tree.Placements {
		placementsByTopic[placement.TopicID]++
	}
	// Backend keeps its own 3 pages plus Storage's 3 flattened pages.
	s.Equal(6, placementsByTopic[backend.ID])
}

// A deep topic below the minimum folds its pages into its parent, recursively.
func (s *llmTreeIndexerSuite) TestFoldsUndersizedDeepTopicIntoParent() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"Engineering","pages":["page-00","page-01","page-02"],"children":[
			{"title":"Backend","pages":["page-03","page-04","page-05"],"children":[
				{"title":"Storage","pages":["page-06"]}
			]}
		]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(7),
	})

	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 2)
	backend := tree.Topics[1]
	placementsByTopic := make(map[string]int)
	for _, placement := range tree.Placements {
		placementsByTopic[placement.TopicID]++
	}
	s.Equal(4, placementsByTopic[backend.ID])
}

func (s *llmTreeIndexerSuite) TestRejectsNegativeMaxDepth() {
	_, err := pagewiki.NewLLMTreeIndexer(pagewiki.LLMTreeIndexerConfig{
		Client: &wikiChatClient{}, Model: "test-model", MaxDepth: -1,
	})
	s.Require().ErrorContains(err, "max depth")
}

func (s *llmTreeIndexerSuite) TestPromptStatesConfiguredMaxDepth() {
	indexer, client := newDepthIndexer(s, 3, `{"root_pages":["page-00"],"topics":[]}`)
	_, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(1),
	})
	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	s.Contains(client.requests[0].Messages[0].Content, "at most 3 levels")
	// The example response shows a nested "children" key inside a child
	// object, so the LLM does not imitate a flat two-level schema example
	// instead of the "at most N levels" prose.
	s.Contains(
		client.requests[0].Messages[0].Content,
		`"children":[{"title":"...","pages":["slug"],"children":`,
	)
}

// A page slug duplicated across sibling branches is claimed depth-first:
// the first branch's subtree wins, matching the pre-refactor behavior. A
// has no own pages but a child holding page-01..03 (child needs >=3 pages
// to survive pruning); B lists page-01 among its own pages too, plus
// enough other pages to survive pruning on its own. page-01 must land
// under A's child, not B.
func (s *llmTreeIndexerSuite) TestDuplicatePageAcrossBranchesClaimsDepthFirst() {
	indexer, _ := newIndexer(s, `{"root_pages":[],"topics":[
		{"title":"A","children":[
			{"title":"AChild","pages":["page-01","page-02","page-03"]}
		]},
		{"title":"B","pages":["page-01","page-04","page-05","page-06"]}
	]}`)
	tree, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(7),
	})

	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 3)
	var aChildID, bID string
	for _, topic := range tree.Topics {
		switch topic.Slug {
		case "achild":
			aChildID = topic.ID
		case "b":
			bID = topic.ID
		}
	}
	s.Require().NotEmpty(aChildID)
	s.Require().NotEmpty(bID)
	placementsByTopic := make(map[string][]string)
	for _, placement := range tree.Placements {
		placementsByTopic[placement.TopicID] = append(placementsByTopic[placement.TopicID], placement.PageID)
	}
	s.Contains(placementsByTopic[aChildID], "id-page-01")
	s.NotContains(placementsByTopic[bID], "id-page-01")
	s.Len(placementsByTopic[aChildID], 3)
	s.Len(placementsByTopic[bID], 3)
	s.Len(tree.Placements, 6)
}

func (s *llmTreeIndexerSuite) TestAppliesGenerationDirectivesToSystemPrompt() {
	indexer, client := newIndexer(s, `{"root_pages":["page-00"],"topics":[]}`)

	_, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(1),
		Directives: pagewiki.GenerationDirectives{
			Language: "简体中文", CustomInstructions: "prefer tables",
		},
	})

	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	system := client.requests[0].Messages[0].Content
	s.True(strings.HasPrefix(system, pagewiki.TreeIndexerPromptForTest(pagewiki.TreeDefaultMaxDepthForTest)))
	s.Contains(system, "in 简体中文.")
	s.Contains(system, "prefer tables")
}

func (s *llmTreeIndexerSuite) TestZeroGenerationDirectivesLeaveSystemPromptUnchanged() {
	indexer, client := newDepthIndexer(s, 3, `{"root_pages":["page-00"],"topics":[]}`)

	_, err := indexer.Index(context.Background(), pagewiki.TreeIndexInput{
		Catalog: indexerCatalog(1),
	})

	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	s.Equal(pagewiki.TreeIndexerPromptForTest(3), client.requests[0].Messages[0].Content)
}

func (s *llmTreeIndexerSuite) TestTreeIndexerPromptPinsShortTopicNames() {
	prompt := pagewiki.TreeIndexerPromptForTest(pagewiki.TreeDefaultMaxDepthForTest)
	s.Contains(prompt, "one to three words")
	s.Contains(prompt, "never a sentence")
}
