package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type llmSessionPlannerSuite struct {
	suite.Suite
}

func TestLLMSessionPlannerSuite(t *testing.T) {
	suite.Run(t, new(llmSessionPlannerSuite))
}

func plannerRevision() pagewiki.SourceRevision {
	raw := "decision: releases ship weekly.diff --git a/main.go b/main.go"
	return pagewiki.SourceRevision{
		ID:  "source-revision-1",
		Raw: []byte(raw),
		Events: []pagewiki.SourceEvent{
			{ID: "event-1", StartByte: 0, EndByte: 30},
			{ID: "event-2", StartByte: 30, EndByte: len(raw)},
		},
	}
}

func (s *llmSessionPlannerSuite) TestPlansCreateUpdateAndDropsNoise() {
	client := &wikiChatClient{responses: []string{`{"briefs":[
		{"action":"create","proposed_slug":"Release  Policy","proposed_title":"Release Policy",
		 "reader_goal":"Understand the release cadence.","topic_path":["Engineering","Runtime"],
		 "evidence":[{"event_id":"event-1","exact_quote":"releases ship weekly"}]},
		{"action":"update","target_slug":"existing-page",
		 "reader_goal":"Refresh the existing page.","topic_path":["Engineering"],
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"skip_noise","reader_goal":"code diff",
		 "evidence":[{"event_id":"event-2","exact_quote":"diff --git"}]}
	]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
		PageCatalog: pagewiki.PageCatalog{{
			ID: "page-1", Slug: "existing-page", Title: "Existing Page",
			CurrentRevisionID: "revision-1",
		}},
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 2)

	create := briefs[0]
	s.Equal(pagewiki.PageActionCreate, create.Action)
	s.Equal("release-policy", create.ProposedSlug)
	s.Equal("release-policy", create.Key)
	s.Equal("Release Policy", create.ProposedTitle)
	s.Equal([]string{"Engineering", "Runtime"}, create.TopicPath)
	s.Equal([]string{"event-1"}, create.EvidenceEventIDs)
	s.Require().Len(create.Evidence, 1)
	s.Equal("releases ship weekly", create.Evidence[0].ExactText)

	update := briefs[1]
	s.Equal(pagewiki.PageActionUpdate, update.Action)
	s.Equal("page-1", update.TargetPageID)
	s.Equal("revision-1", update.ExpectedBaseRevisionID)
	s.Equal("existing-page", update.Key)
	s.Empty(update.ProposedSlug)
	s.Empty(update.TopicPath)

	s.Require().Len(client.requests, 1)
	s.Equal("test-model", client.requests[0].Model)
	s.Contains(client.requests[0].Messages[1].Content, "event-1")
	s.Contains(client.requests[0].Messages[1].Content, "existing-page")
}
