package pagewiki_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
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

func (s *llmSessionPlannerSuite) TestRemapsCreateToUpdateWhenSlugMatchesCatalog() {
	client := &wikiChatClient{responses: []string{`{"briefs":[
		{"action":"create","proposed_slug":"Existing  Page","proposed_title":"Existing Page",
		 "reader_goal":"Refresh the existing page.","topic_path":["Engineering"],
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}
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
	s.Require().Len(briefs, 1)

	update := briefs[0]
	s.Equal(pagewiki.PageActionUpdate, update.Action)
	s.Equal("existing-page", update.Key)
	s.Equal("page-1", update.TargetPageID)
	s.Equal("revision-1", update.ExpectedBaseRevisionID)
	s.Empty(update.ProposedSlug)
	s.Empty(update.ProposedTitle)
	s.Empty(update.TopicPath)
}

func (s *llmSessionPlannerSuite) TestDropsLaterQuoteThatDuplicatesAnAcceptedQuoteAcrossEvents() {
	eventOneContent := "Team ships weekly. ship weekly cadence recorded here."
	eventTwoContent := "Second mention of ship weekly cadence appears here."
	raw := eventOneContent + eventTwoContent
	revision := pagewiki.SourceRevision{
		ID:  "source-revision-cross-quote",
		Raw: []byte(raw),
		Events: []pagewiki.SourceEvent{
			{ID: "event-1", StartByte: 0, EndByte: len(eventOneContent)},
			{ID: "event-2", StartByte: len(eventOneContent), EndByte: len(raw)},
		},
	}
	client := &wikiChatClient{responses: []string{`{"briefs":[
		{"action":"create","proposed_slug":"release-cadence","proposed_title":"Release Cadence",
		 "topic_path":["Engineering"],
		 "evidence":[
			{"event_id":"event-1","exact_quote":"ship weekly cadence"},
			{"event_id":"event-2","exact_quote":"ship weekly cadence"}
		 ]}
	]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: revision,
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Equal(pagewiki.PageActionCreate, briefs[0].Action)
	s.Require().Len(briefs[0].Evidence, 1)
	s.Equal("event-1", briefs[0].Evidence[0].EventID)
	s.Equal("ship weekly cadence", briefs[0].Evidence[0].ExactText)
}

func (s *llmSessionPlannerSuite) TestDropsLaterQuoteThatIsASubstringOfAnAcceptedQuote() {
	eventOneContent := "Releases ship weekly after the validation gate passes."
	eventTwoContent := "The validation gate passes before every release."
	raw := eventOneContent + eventTwoContent
	revision := pagewiki.SourceRevision{
		ID:  "source-revision-substring-quote",
		Raw: []byte(raw),
		Events: []pagewiki.SourceEvent{
			{ID: "event-1", StartByte: 0, EndByte: len(eventOneContent)},
			{ID: "event-2", StartByte: len(eventOneContent), EndByte: len(raw)},
		},
	}
	client := &wikiChatClient{responses: []string{`{"briefs":[
		{"action":"create","proposed_slug":"release-policy","proposed_title":"Release Policy",
		 "topic_path":["Engineering"],
		 "evidence":[
			{"event_id":"event-1","exact_quote":"Releases ship weekly after the validation gate passes."},
			{"event_id":"event-2","exact_quote":"validation gate passes"}
		 ]}
	]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: revision,
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Require().Len(briefs[0].Evidence, 1)
	s.Equal("event-1", briefs[0].Evidence[0].EventID)
	s.Equal("Releases ship weekly after the validation gate passes.", briefs[0].Evidence[0].ExactText)
}

func (s *llmSessionPlannerSuite) TestLogsTheLastAttemptErrorWhenDegraded() {
	var logs bytes.Buffer
	client := &wikiChatClient{err: context.DeadlineExceeded}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Equal("plan-degraded", briefs[0].Key)
	s.Contains(logs.String(), `"level":"WARN"`)
	s.Contains(logs.String(), `"source_revision_id":"source-revision-1"`)
	s.Contains(logs.String(), context.DeadlineExceeded.Error())
}

func (s *llmSessionPlannerSuite) TestDropsInvalidEvidenceAndBriefs() {
	client := &wikiChatClient{responses: []string{`{"briefs":[
		{"action":"create","proposed_slug":"ghost","proposed_title":"Ghost",
		 "topic_path":["Engineering"],
		 "evidence":[{"event_id":"missing-event","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"ambiguous","proposed_title":"Ambiguous",
		 "topic_path":["Engineering"],
		 "evidence":[{"event_id":"event-1","exact_quote":"e"}]},
		{"action":"update","target_slug":"absent-page",
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"","proposed_title":"No Slug",
		 "topic_path":["Engineering"],
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}
	]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Equal("source-only", briefs[0].Key)
	s.Equal(pagewiki.PageActionSourceOnly, briefs[0].Action)
	s.Equal([]string{"event-1", "event-2"}, briefs[0].EvidenceEventIDs)
}

func (s *llmSessionPlannerSuite) TestRetriesOnceThenDegradesToSourceOnly() {
	client := &wikiChatClient{responses: []string{"not-json", "still-not-json"}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Len(client.requests, 2)
	s.Require().Len(briefs, 1)
	s.Equal("plan-degraded", briefs[0].Key)
	s.Equal(pagewiki.PageActionSourceOnly, briefs[0].Action)
}

func (s *llmSessionPlannerSuite) TestDegradesWhenTheModelIsUnreachable() {
	client := &wikiChatClient{err: context.DeadlineExceeded}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Len(client.requests, 2)
	s.Require().Len(briefs, 1)
	s.Equal("plan-degraded", briefs[0].Key)
}

func (s *llmSessionPlannerSuite) TestCapsAcceptedBriefsAtEight() {
	var body strings.Builder
	body.WriteString(`{"briefs":[`)
	for index := 0; index < 10; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body,
			`{"action":"create","proposed_slug":"page-%d","proposed_title":"Page %d",
			  "topic_path":["Engineering"],
			  "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}`,
			index, index)
	}
	body.WriteString(`]}`)
	client := &wikiChatClient{responses: []string{body.String()}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Len(briefs, 8)
}

func (s *llmSessionPlannerSuite) TestPlannerPromptPinsUpdateRuleAndNoisePolicy() {
	client := &wikiChatClient{responses: []string{`{"briefs":[]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	_, err = planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Require().NotEmpty(client.requests)
	system := client.requests[0].Messages[0].Content
	s.Contains(system, "still need in a month")
	s.Contains(system, "one-off session narratives")
	s.Contains(system, "MUST be update")
}
