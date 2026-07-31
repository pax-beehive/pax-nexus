package pagewiki_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type recordingIndexer struct {
	calls int
	tree  pagewiki.TopicTree
	err   error
	// build, when set, computes the tree to return (and record) from the
	// catalog the service loaded, letting tests reference page IDs that are
	// only known after the run has published a page.
	build func(pagewiki.PageCatalog) pagewiki.TopicTree
	// lastInput records the TreeIndexInput the last Index call was given,
	// letting tests assert what the service threaded in (e.g. the loaded
	// GenerationDirectives).
	lastInput pagewiki.TreeIndexInput
}

func (r *recordingIndexer) Index(
	_ context.Context,
	input pagewiki.TreeIndexInput,
) (pagewiki.TopicTree, error) {
	r.calls++
	r.lastInput = input
	if r.err != nil {
		return pagewiki.TopicTree{}, r.err
	}
	if r.build != nil {
		r.tree = r.build(input.Catalog)
	}
	return r.tree, nil
}

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

func (s *treeReindexSuite) TestSuccessfulRunReplacesTree() {
	planner, editor, request := s.createBriefAndEditor()
	indexer := &recordingIndexer{
		build: func(catalog pagewiki.PageCatalog) pagewiki.TopicTree {
			s.Require().Len(catalog, 1)
			pageID := catalog[0].ID
			return pagewiki.TopicTree{
				Topics: []pagewiki.Topic{
					{ID: "topic-databases", Slug: "databases", Title: "Databases"},
				},
				Placements: []pagewiki.PagePlacement{
					{PageID: pageID, TopicID: "topic-databases", Rank: 0},
				},
			}
		},
	}
	service := pagewiki.NewService(s.repository, planner, editor, pagewiki.WithTreeIndexer(indexer, nil))

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Equal(1, indexer.calls)

	tree, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(indexer.tree, tree)
	s.Require().Len(tree.Topics, 1)
	s.Require().Equal("topic-databases", tree.Topics[0].ID)
	s.Require().Len(tree.Placements, 1)

	page, err := s.repository.PageBySlug(context.Background(), "sqlite")
	s.Require().NoError(err)
	s.Require().Equal(page.ID, tree.Placements[0].PageID)
}

func (s *treeReindexSuite) TestSourceOnlyRunSkipsIndexer() {
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
	indexer := &recordingIndexer{}
	service := pagewiki.NewService(s.repository, planner, editor, pagewiki.WithTreeIndexer(indexer, nil))

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
	s.Require().Equal(0, indexer.calls)
}

func (s *treeReindexSuite) TestIndexerFailureKeepsRunAndOldTree() {
	seeded := pagewiki.TopicTree{
		Topics: []pagewiki.Topic{
			{ID: "topic-seed", Slug: "seed", Title: "Seed"},
		},
	}
	s.Require().NoError(s.repository.ReplaceTopicTree(context.Background(), seeded))
	seededSnapshot, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)

	planner, editor, request := s.createBriefAndEditor()
	indexer := &recordingIndexer{err: errors.New("boom")}
	service := pagewiki.NewService(s.repository, planner, editor, pagewiki.WithTreeIndexer(indexer, nil))

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Equal(1, indexer.calls)

	tree, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(seededSnapshot, tree)
}

func (s *treeReindexSuite) TestServiceWithoutIndexerStillWorks() {
	planner, editor, request := s.createBriefAndEditor()
	service := pagewiki.NewService(s.repository, planner, editor)

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)

	tree, err := s.repository.TopicTree(context.Background())
	s.Require().NoError(err)
	s.Require().Empty(tree.Topics)
	s.Require().Empty(tree.Placements)
}
