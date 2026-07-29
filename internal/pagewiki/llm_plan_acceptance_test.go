package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type llmPlanAcceptanceSuite struct {
	suite.Suite
}

func TestLLMPlanAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(llmPlanAcceptanceSuite))
}

func (s *llmPlanAcceptanceSuite) TestNoisySessionProducesOnlyGenuinePages() {
	skillDoc := "## Checklist\nYou MUST create a task for each of these items."
	codeDiff := "diff --git a/main.go b/main.go\n+func added() {}"
	knowledge := "决定：team memory 的召回默认走 BM25，向量召回只做补充。"
	raw := skillDoc + codeDiff + knowledge

	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: &wikiChatClient{responses: []string{`{"briefs":[
			{"action":"skip_noise","reader_goal":"agent skill instructions",
			 "evidence":[{"event_id":"event-skill","exact_quote":"## Checklist"}]},
			{"action":"skip_noise","reader_goal":"code diff",
			 "evidence":[{"event_id":"event-diff","exact_quote":"diff --git"}]},
			{"action":"create","proposed_slug":"recall-strategy","proposed_title":"Recall Strategy",
			 "reader_goal":"Understand the default recall path.","topic_path":["Engineering","Retrieval"],
			 "evidence":[{"event_id":"event-knowledge","exact_quote":"召回默认走 BM25"}]}
		]}`}},
		Model: "test-model",
	})
	s.Require().NoError(err)
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: &wikiChatClient{responses: []string{
			`{"title":"Recall Strategy","summary":"BM25 is the default recall path.","sections":[{"key":"decision","heading":"Decision","markdown":"The team defaults recall to BM25 and treats vector recall as a supplement."}]}`,
		}},
		Model: "test-model",
	})
	s.Require().NoError(err)
	repository := memory.NewRepository()
	service := pagewiki.NewService(repository, planner, editor)

	result, err := service.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-noisy",
		IdempotencyKey: "session-noisy-injection",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{
			{ID: "event-skill", StartByte: 0, EndByte: len(skillDoc)},
			{ID: "event-diff", StartByte: len(skillDoc), EndByte: len(skillDoc) + len(codeDiff)},
			{ID: "event-knowledge", StartByte: len(skillDoc) + len(codeDiff), EndByte: len(raw)},
		},
	})

	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Len(result.Run.Targets, 1)
	s.Equal(pagewiki.PageActionCreate, result.Run.Targets[0].Action)

	page, err := repository.PageBySlug(context.Background(), "recall-strategy")
	s.Require().NoError(err)
	revision, err := repository.PageRevision(context.Background(), page.CurrentRevisionID)
	s.Require().NoError(err)
	s.Equal("Recall Strategy", revision.Title)
	s.Contains(revision.Markdown, "defaults recall to BM25")
	s.Contains(revision.Markdown, "召回默认走 BM25")
	s.Require().Len(revision.Citations, 1)
	s.Equal("event-knowledge", revision.Citations[0].SourceAnchors[0].EventID)

	navigation, err := repository.Navigation(context.Background())
	s.Require().NoError(err)
	s.Empty(navigation.Roots)
	s.Zero(repository.TopicCount())
	s.Zero(repository.PlacementCount())

	_, err = repository.PageBySlug(context.Background(), "knowledge-checklist")
	s.Require().ErrorIs(err, pagewiki.ErrNotFound)
}

func (s *llmPlanAcceptanceSuite) TestUpdatePathRevisesAnExistingPageThroughInjectSession() {
	plannerClient := &wikiChatClient{responses: []string{
		`{"briefs":[
			{"action":"create","proposed_slug":"release-policy","proposed_title":"Release Policy",
			 "reader_goal":"Understand the release cadence.","topic_path":["Engineering","Runtime"],
			 "evidence":[{"event_id":"event-1","exact_quote":"releases ship weekly"}]}
		]}`,
		`{"briefs":[
			{"action":"update","target_slug":"release-policy",
			 "reader_goal":"Refresh the release cadence.",
			 "evidence":[{"event_id":"event-2","exact_quote":"releases ship twice weekly now"}]}
		]}`,
	}}
	editorClient := &wikiChatClient{responses: []string{
		`{"title":"Release Policy","summary":"How the team ships releases.","sections":[{"key":"policy","heading":"Policy","markdown":"Releases ship weekly."}]}`,
		`{"title":"Release Policy","summary":"How the team ships releases, revised.","sections":[{"key":"policy","heading":"Policy","markdown":"Releases ship twice weekly now."}]}`,
	}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: plannerClient, Model: "test-model",
	})
	s.Require().NoError(err)
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: editorClient, Model: "test-model",
	})
	s.Require().NoError(err)
	repository := memory.NewRepository()
	service := pagewiki.NewService(repository, planner, editor)

	firstRaw := "decision: releases ship weekly."
	firstResult, err := service.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-release-1",
		IdempotencyKey: "release-run-1",
		Raw:            []byte(firstRaw),
		Events: []pagewiki.SourceEventInput{{
			ID: "event-1", StartByte: 0, EndByte: len(firstRaw),
		}},
	})
	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, firstResult.Run.Status)
	s.Require().Len(firstResult.Run.Targets, 1)
	s.Equal(pagewiki.PageActionCreate, firstResult.Run.Targets[0].Action)

	page, err := repository.PageBySlug(context.Background(), "release-policy")
	s.Require().NoError(err)
	firstRevisionID := page.CurrentRevisionID

	secondRaw := "decision: releases ship twice weekly now."
	secondResult, err := service.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-release-2",
		IdempotencyKey: "release-run-2",
		Raw:            []byte(secondRaw),
		Events: []pagewiki.SourceEventInput{{
			ID: "event-2", StartByte: 0, EndByte: len(secondRaw),
		}},
	})

	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, secondResult.Run.Status)
	s.Require().Len(secondResult.Run.Targets, 1)
	s.Equal(pagewiki.PageActionUpdate, secondResult.Run.Targets[0].Action)
	s.Equal(page.ID, secondResult.Run.Targets[0].PageID)
	s.NotEqual(firstRevisionID, secondResult.Run.Targets[0].PageRevisionID)

	updatedPage, err := repository.PageBySlug(context.Background(), "release-policy")
	s.Require().NoError(err)
	s.Equal(secondResult.Run.Targets[0].PageRevisionID, updatedPage.CurrentRevisionID)
	updatedRevision, err := repository.PageRevision(context.Background(), updatedPage.CurrentRevisionID)
	s.Require().NoError(err)
	s.Contains(updatedRevision.Markdown, "Releases ship twice weekly now.")
	s.Contains(updatedRevision.Markdown, "releases ship twice weekly now")
}
