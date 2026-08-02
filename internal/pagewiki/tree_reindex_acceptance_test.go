package pagewiki_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type treeReindexSuite struct {
	suite.Suite
	repository *memory.Repository
}

func TestTreeReindexAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(treeReindexSuite))
}

func (s *treeReindexSuite) SetupTest() {
	s.repository = memory.NewRepository()
}

func (s *treeReindexSuite) createBriefAndEditor() (pagewiki.ScriptedPlanner, pagewiki.ScriptedEditor, pagewiki.InjectSessionRequest) {
	raw := []byte("event-1: The team selected SQLite for the local store.")
	eventText := "The team selected SQLite for the local store."
	eventStart := len("event-1: ")
	planner := pagewiki.ScriptedPlanner{
		Briefs: []pagewiki.PageBrief{
			{
				Key:              "sqlite",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "sqlite",
				ProposedTitle:    "SQLite",
				ReaderGoal:       "Explain why SQLite is the local store.",
				EvidenceEventIDs: []string{"event-1"},
			},
		},
	}
	editor := pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			"sqlite": {
				Slug:    "sqlite",
				Title:   "SQLite",
				Summary: "SQLite is the selected local persistence layer.",
				Sections: []pagewiki.SectionDraft{
					{
						Key:      "decision",
						Heading:  "Decision",
						Markdown: "The team selected SQLite for the local store.",
					},
				},
				Citations: []pagewiki.CitationDraft{
					{
						SectionKey: "decision",
						ExactText:  "selected SQLite",
						Evidence: []pagewiki.EvidenceQuoteDraft{
							{
								EventID:   "event-1",
								ExactText: eventText,
							},
						},
					},
				},
			},
		},
	}
	request := pagewiki.InjectSessionRequest{
		SourceID:       "session-1",
		IdempotencyKey: "session-1-injection",
		Raw:            raw,
		Events: []pagewiki.SourceEventInput{
			{
				ID:        "event-1",
				StartByte: eventStart,
				EndByte:   eventStart + len(eventText),
			},
		},
	}
	return planner, editor, request
}

func (s *treeReindexSuite) TestSuccessfulRunPlacesThePublishedPage() {
	planner, editor, request := s.createBriefAndEditor()
	navigator := &fakeTreeNavigator{placements: []pagewiki.TreePlacementChoice{
		{Action: pagewiki.TreePlacementCreate, Title: "Databases"},
	}}
	service := newTreeMaintenanceService(s.repository, planner, editor, navigator)

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Equal(1, service.PendingTreeTasksForTest(), "placement runs off the ingest path")

	service.FlushTreeReindex(context.Background())
	s.Require().Len(navigator.placementCalls(), 1)

	tree, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Require().Len(tree.Topics, 1)
	s.Require().Equal("databases", tree.Topics[0].Slug)
	s.Require().Len(tree.Placements, 1)

	page, err := s.repository.PageBySlug(context.Background(), "sqlite")
	s.Require().NoError(err)
	s.Require().Equal(page.ID, tree.Placements[0].PageID)
	s.Require().Equal(tree.Topics[0].ID, tree.Placements[0].TopicID)
}

func (s *treeReindexSuite) TestSourceOnlyRunQueuesNothing() {
	raw := []byte("event-1: Housekeeping note that is not knowledge.")
	planner := pagewiki.ScriptedPlanner{
		Briefs: []pagewiki.PageBrief{
			{
				Key:              "noise",
				Action:           pagewiki.PageActionSourceOnly,
				EvidenceEventIDs: []string{"event-1"},
			},
		},
	}
	editor := pagewiki.ScriptedEditor{}
	navigator := &fakeTreeNavigator{}
	service := pagewiki.NewService(
		s.repository, planner, editor,
		pagewiki.WithTreeNavigator(pagewiki.TreeMaintenanceConfig{Navigator: navigator}),
	)

	result, err := service.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-source-only",
		IdempotencyKey: "session-source-only-injection",
		Raw:            raw,
		Events: []pagewiki.SourceEventInput{
			{ID: "event-1", StartByte: 0, EndByte: len(raw)},
		},
	})

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Zero(service.PendingTreeTasksForTest())

	service.FlushTreeReindex(context.Background())
	s.Require().Empty(navigator.placementCalls())
}

func (s *treeReindexSuite) TestNavigatorFailureKeepsRunAndOldTree() {
	seeded := pagewiki.TopicTree{
		Topics: []pagewiki.Topic{
			{ID: "topic-seed", Slug: "seed", Title: "Seed"},
		},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(context.Background(), seeded))
	seededSnapshot, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)

	planner, editor, request := s.createBriefAndEditor()
	navigator := &fakeTreeNavigator{placementErr: errors.New("boom")}
	service := newTreeMaintenanceService(s.repository, planner, editor, navigator)

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)

	service.FlushTreeReindex(context.Background())
	s.Require().Len(navigator.placementCalls(), 1)

	tree, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(seededSnapshot, tree)
}

func (s *treeReindexSuite) TestServiceWithoutNavigatorStillWorks() {
	planner, editor, request := s.createBriefAndEditor()
	service := pagewiki.NewService(s.repository, planner, editor)

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Zero(service.PendingTreeTasksForTest())

	tree, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Require().Empty(tree.Topics)
	s.Require().Empty(tree.Placements)
}

// TestTwoRunsPlaceBothPagesIndependently is the incremental counterpart of
// the old "two runs, one rebuild" coalescing test: each published page is its
// own queued task, so two runs — even across two Service instances sharing a
// repository — place exactly their own page and nothing else.
func (s *treeReindexSuite) TestTwoRunsPlaceBothPagesIndependently() {
	planner, editor, request := s.createBriefAndEditor()
	navigator := &fakeTreeNavigator{}
	service := newTreeMaintenanceService(s.repository, planner, editor, navigator)

	first, err := service.InjectSession(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, first.Run.Status)

	secondRaw := []byte("event-2: The wiki search stays lexical for now.")
	secondText := "The wiki search stays lexical for now."
	secondStart := len("event-2: ")
	secondPlanner := pagewiki.ScriptedPlanner{
		Briefs: []pagewiki.PageBrief{{
			Key:              "wiki-search",
			Action:           pagewiki.PageActionCreate,
			ProposedSlug:     "wiki-search",
			ProposedTitle:    "Wiki Search",
			EvidenceEventIDs: []string{"event-2"},
		}},
	}
	secondEditor := pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			"wiki-search": {
				Slug:    "wiki-search",
				Title:   "Wiki Search",
				Summary: "Wiki search stays lexical in this iteration.",
				Sections: []pagewiki.SectionDraft{{
					Key:      "retrieval",
					Heading:  "Retrieval",
					Markdown: "The wiki search stays lexical for now.",
				}},
				Citations: []pagewiki.CitationDraft{{
					SectionKey: "retrieval",
					ExactText:  "stays lexical",
					Evidence: []pagewiki.EvidenceQuoteDraft{{
						EventID:   "event-2",
						ExactText: secondText,
					}},
				}},
			},
		},
	}
	secondNavigator := &fakeTreeNavigator{}
	secondService := newTreeMaintenanceService(s.repository, secondPlanner, secondEditor, secondNavigator)
	second, err := secondService.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-2",
		IdempotencyKey: "session-2-injection",
		Raw:            secondRaw,
		Events: []pagewiki.SourceEventInput{{
			ID:        "event-2",
			StartByte: secondStart,
			EndByte:   secondStart + len(secondText),
		}},
	})
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, second.Run.Status)

	// Neither run placed anything inline; each service drains only its own
	// queue, and a second flush finds nothing left to do.
	s.Require().Empty(navigator.placementCalls())
	secondService.FlushTreeReindex(context.Background())
	s.Require().Len(secondNavigator.placementCalls(), 1)
	secondService.FlushTreeReindex(context.Background())
	s.Require().Len(secondNavigator.placementCalls(), 1)
	service.FlushTreeReindex(context.Background())
	s.Require().Len(navigator.placementCalls(), 1)
}
