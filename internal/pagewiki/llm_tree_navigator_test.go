package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type llmTreeNavigatorSuite struct {
	suite.Suite
}

func TestLLMTreeNavigator(t *testing.T) {
	suite.Run(t, new(llmTreeNavigatorSuite))
}

func newNavigator(
	s *llmTreeNavigatorSuite, responses ...string,
) (*pagewiki.LLMTreeNavigator, *wikiChatClient) {
	client := &wikiChatClient{responsesByIndex: responses}
	navigator, err := pagewiki.NewLLMTreeNavigator(pagewiki.LLMTreeNavigatorConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	return navigator, client
}

func placementInput(children []pagewiki.TreeChildTopic, allowCreate bool) pagewiki.TreePlacementInput {
	return pagewiki.TreePlacementInput{
		Page: pagewiki.PageCatalogEntry{
			ID: "id-page-01", Slug: "page-01", Title: "Page 01", Summary: "About page 01",
		},
		Path:        []string{"Engineering"},
		Children:    children,
		AllowCreate: allowCreate,
	}
}

func (s *llmTreeNavigatorSuite) TestChoosePlacementEntersKnownChild() {
	navigator, client := newNavigator(s, `{"action":"enter","slug":"backend"}`)
	choice, err := navigator.ChoosePlacement(context.Background(), placementInput(
		[]pagewiki.TreeChildTopic{{Slug: "backend", Title: "Backend", Pages: 4}}, true,
	))
	s.Require().NoError(err)
	s.Equal(pagewiki.TreePlacementEnter, choice.Action)
	s.Equal("backend", choice.Slug)
	s.Require().Len(client.requests, 1)
}

func (s *llmTreeNavigatorSuite) TestChoosePlacementFallsBackToStayOnUnknownEnterSlug() {
	navigator, _ := newNavigator(s, `{"action":"enter","slug":"unknown-topic"}`)
	choice, err := navigator.ChoosePlacement(context.Background(), placementInput(
		[]pagewiki.TreeChildTopic{{Slug: "backend", Title: "Backend", Pages: 4}}, true,
	))
	s.Require().NoError(err)
	s.Equal(pagewiki.TreePlacementStay, choice.Action)
}

func (s *llmTreeNavigatorSuite) TestChoosePlacementFallsBackToStayOnGarbageAction() {
	navigator, _ := newNavigator(s, `{"action":"teleport"}`)
	choice, err := navigator.ChoosePlacement(context.Background(), placementInput(nil, true))
	s.Require().NoError(err)
	s.Equal(pagewiki.TreePlacementStay, choice.Action)
}

func (s *llmTreeNavigatorSuite) TestChoosePlacementFallsBackToStayWhenCreateNotAllowed() {
	navigator, _ := newNavigator(s, `{"action":"create","title":"New Topic"}`)
	choice, err := navigator.ChoosePlacement(context.Background(), placementInput(nil, false))
	s.Require().NoError(err)
	s.Equal(pagewiki.TreePlacementStay, choice.Action)
}

func (s *llmTreeNavigatorSuite) TestChoosePlacementCreatesNewTopicWithTitle() {
	navigator, _ := newNavigator(s, `{"action":"create","title":"New Topic"}`)
	choice, err := navigator.ChoosePlacement(context.Background(), placementInput(nil, true))
	s.Require().NoError(err)
	s.Equal(pagewiki.TreePlacementCreate, choice.Action)
	s.Equal("New Topic", choice.Title)
}

func (s *llmTreeNavigatorSuite) TestChoosePlacementFallsBackToStayWhenCreateTitleEmpty() {
	navigator, _ := newNavigator(s, `{"action":"create","title":"   "}`)
	choice, err := navigator.ChoosePlacement(context.Background(), placementInput(nil, true))
	s.Require().NoError(err)
	s.Equal(pagewiki.TreePlacementStay, choice.Action)
}

// splitInput returns five pages (page-01..page-05) so tests can distinguish
// the "too few pages in a group" rule from the "missing slug from the
// union" rule.
func splitInput(forbidden ...string) pagewiki.TreeSplitInput {
	pages := make([]pagewiki.TreeSplitPage, 0, 5)
	for index := 1; index <= 5; index++ {
		slug := "page-0" + string(rune('0'+index))
		pages = append(pages, pagewiki.TreeSplitPage{
			Slug: slug, Title: "Page " + string(rune('0'+index)), Summary: "summary",
		})
	}
	return pagewiki.TreeSplitInput{
		Path:      []string{"Engineering"},
		Pages:     pages,
		Forbidden: forbidden,
	}
}

func (s *llmTreeNavigatorSuite) TestSplitTopicAcceptsValidGroups() {
	navigator, client := newNavigator(s, `{"groups":[
		{"title":"Backend","pages":["page-01","page-02","page-03"]},
		{"title":"Frontend","pages":["page-04","page-05"]}
	]}`)
	groups, err := navigator.SplitTopic(context.Background(), splitInput())
	s.Require().NoError(err)
	s.Require().Len(groups, 2)
	s.Equal("Backend", groups[0].Title)
	s.Equal([]string{"page-01", "page-02", "page-03"}, groups[0].Pages)
	s.Require().Len(client.requests, 1)
}

func (s *llmTreeNavigatorSuite) TestSplitTopicRetriesWithPreviousErrorThenAccepts() {
	navigator, client := newNavigator(s,
		// round 1: valid group sizes, but page-05 is missing from the union.
		`{"groups":[
			{"title":"Backend","pages":["page-01","page-02"]},
			{"title":"Frontend","pages":["page-03","page-04"]}
		]}`,
		// round 2: corrected, covers every page exactly once.
		`{"groups":[
			{"title":"Backend","pages":["page-01","page-02","page-05"]},
			{"title":"Frontend","pages":["page-03","page-04"]}
		]}`,
	)
	groups, err := navigator.SplitTopic(context.Background(), splitInput())
	s.Require().NoError(err)
	s.Require().Len(groups, 2)
	s.Require().Len(client.requests, 2)
	s.Contains(client.requests[1].Messages[1].Content, "previous_error")
}

func (s *llmTreeNavigatorSuite) TestSplitTopicFailsAfterTwoInvalidRounds() {
	navigator, client := newNavigator(s,
		`{"groups":[{"title":"Backend","pages":["page-01"]}]}`,
		`{"groups":[{"title":"Backend","pages":["page-01"]}]}`,
	)
	_, err := navigator.SplitTopic(context.Background(), splitInput())
	s.Require().Error(err)
	s.Require().Len(client.requests, 2)
}

func (s *llmTreeNavigatorSuite) TestSplitTopicRejectsGroupSlugCollisionWithForbidden() {
	response := `{"groups":[
		{"title":"Backend","pages":["page-01","page-02","page-03"]},
		{"title":"Frontend","pages":["page-04","page-05"]}
	]}`
	navigator, _ := newNavigator(s, response, response)
	_, err := navigator.SplitTopic(context.Background(), splitInput("backend"))
	s.Require().Error(err)
	s.Contains(err.Error(), "backend")
}
