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
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
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
	s.Equal([]string{"event-1"}, create.EvidenceEventIDs)
	s.Require().Len(create.Evidence, 1)
	s.Equal("releases ship weekly", create.Evidence[0].ExactText)

	update := briefs[1]
	s.Equal(pagewiki.PageActionUpdate, update.Action)
	s.Equal("page-1", update.TargetPageID)
	s.Equal("revision-1", update.ExpectedBaseRevisionID)
	s.Equal("existing-page", update.Key)
	s.Empty(update.ProposedSlug)

	s.Require().Len(client.requests, 1)
	s.Equal("test-model", client.requests[0].Model)
	s.Contains(client.requests[0].Messages[1].Content, "event-1")
	s.Contains(client.requests[0].Messages[1].Content, "existing-page")
}

func (s *llmSessionPlannerSuite) TestRemapsCreateToUpdateWhenSlugMatchesCatalog() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
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
}

func (s *llmSessionPlannerSuite) TestRemapsCreateToUpdateEvenWithSentenceShapedTitle() {
	sentenceTitle := "Fixing the planner so that xanadu links are created on the LLM planner path"
	client := &wikiChatClient{responsesByIndex: []string{fmt.Sprintf(`{"briefs":[
		{"action":"create","proposed_slug":"Existing  Page","proposed_title":%q,
		 "reader_goal":"Refresh the existing page.","topic_path":["Engineering"],
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}
	]}`, sentenceTitle)}}
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
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
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
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
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
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
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
	client := &wikiChatClient{responsesByIndex: []string{"not-json", "still-not-json"}}
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
	client := &wikiChatClient{responsesByIndex: []string{body.String()}}
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

func (s *llmSessionPlannerSuite) TestResolvesRelatedSlugsAgainstCatalogAndSiblings() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
		{"action":"create","proposed_slug":"alpha","proposed_title":"Alpha",
		 "reader_goal":"Alpha.","related_slugs":["existing-page","beta","alpha","missing-page","beta"],
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"beta","proposed_title":"Beta",
		 "reader_goal":"Beta.","related_slugs":["existing-page"],
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
	s.Require().Len(briefs, 2)

	alpha := briefs[0]
	// Catalog and sibling slugs resolve; self, unknown, and duplicates drop.
	s.Require().Len(alpha.RelatedPages, 2)
	s.Equal(pagewiki.RelatedPage{ID: "page-1", Title: "Existing Page"}, alpha.RelatedPages[0])
	sibling := alpha.RelatedPages[1]
	s.Equal("Beta", sibling.Title)
	s.True(strings.HasPrefix(sibling.ID, "page_"), "sibling create resolves to a stable page ID")
	s.NotEqual("page-1", sibling.ID)

	beta := briefs[1]
	s.Require().Len(beta.RelatedPages, 1)
	s.Equal(pagewiki.RelatedPage{ID: "page-1", Title: "Existing Page"}, beta.RelatedPages[0])
}

func (s *llmSessionPlannerSuite) TestCapsRelatedSlugsAtFour() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
		{"action":"create","proposed_slug":"alpha","proposed_title":"Alpha",
		 "reader_goal":"Alpha.","related_slugs":["p-1","p-2","p-3","p-4","p-5"],
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}
	]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	catalog := pagewiki.PageCatalog{}
	for _, suffix := range []string{"1", "2", "3", "4", "5"} {
		catalog = append(catalog, pagewiki.PageCatalogEntry{
			ID: "page-" + suffix, Slug: "p-" + suffix, Title: "Page " + suffix,
			CurrentRevisionID: "revision-" + suffix,
		})
	}
	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
		PageCatalog:    catalog,
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Require().Len(briefs[0].RelatedPages, 4)
	s.Equal("page-4", briefs[0].RelatedPages[3].ID)
}

func (s *llmSessionPlannerSuite) TestPlannerPromptPinsUpdateRuleAndNoisePolicy() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[]}`}}
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

func (s *llmSessionPlannerSuite) TestPlanRequestCarriesCatalogSummaries() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	_, err = planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
		PageCatalog: pagewiki.PageCatalog{{
			ID: "page-1", Slug: "existing-page", Title: "Existing Page",
			CurrentRevisionID: "revision-1",
			Summary:           "How weekly releases are cut.",
		}},
	})

	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	s.Contains(client.requests[0].Messages[1].Content, "How weekly releases are cut.")
	s.Contains(client.requests[0].Messages[0].Content, "summary")
}

func (s *llmSessionPlannerSuite) TestAppliesGenerationDirectivesToSystemPrompt() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	_, err = planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
		Directives: pagewiki.GenerationDirectives{
			Language: "简体中文", CustomInstructions: "prefer tables",
		},
	})

	s.Require().NoError(err)
	s.Require().NotEmpty(client.requests)
	system := client.requests[0].Messages[0].Content
	s.True(strings.HasPrefix(system, pagewiki.PageWikiPlannerPromptForTest))
	s.Contains(system, "in 简体中文.")
	s.Contains(system, "prefer tables")
}

func (s *llmSessionPlannerSuite) TestZeroGenerationDirectivesLeaveSystemPromptUnchanged() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	_, err = planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Require().NotEmpty(client.requests)
	s.Equal(pagewiki.PageWikiPlannerPromptForTest, client.requests[0].Messages[0].Content)
}

func (s *llmSessionPlannerSuite) TestStripsTrailingPeriodFromProposedTitle() {
	client := &wikiChatClient{responsesByIndex: []string{`{"briefs":[
		{"action":"create","proposed_slug":"release-policy","proposed_title":"Release Policy.",
		 "reader_goal":"Understand the release cadence.",
		 "evidence":[{"event_id":"event-1","exact_quote":"releases ship weekly"}]}
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
	s.Equal(pagewiki.PageActionCreate, briefs[0].Action)
	s.Equal("Release Policy", briefs[0].ProposedTitle)
}

func (s *llmSessionPlannerSuite) TestRejectsSentenceShapedTitles() {
	longByWords := "Fixing the planner so that xanadu links are created on the LLM planner path"
	longByRunes := strings.Repeat("ab", 45) // 90 runes, one word
	client := &wikiChatClient{responsesByIndex: []string{fmt.Sprintf(`{"briefs":[
		{"action":"create","proposed_slug":"planner-fix","proposed_title":%q,
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"long-rune-title","proposed_title":%q,
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"nine-word-title",
		 "proposed_title":"One Two Three Four Five Six Seven Eight Nine",
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}
	]}`, longByWords, longByRunes)}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	// The two over-limit titles drop their briefs; the 9-word boundary title survives.
	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Equal("nine-word-title", briefs[0].ProposedSlug)
	s.Equal("One Two Three Four Five Six Seven Eight Nine", briefs[0].ProposedTitle)
}

func (s *llmSessionPlannerSuite) TestRejectsTitlesPastTheExactRuneCap() {
	exactly80Runes := strings.Repeat("a", 80) // at the cap: one word, passes
	exactly81Runes := strings.Repeat("a", 81) // one over the cap: rejected
	client := &wikiChatClient{responsesByIndex: []string{fmt.Sprintf(`{"briefs":[
		{"action":"create","proposed_slug":"eighty-rune-title","proposed_title":%q,
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"eighty-one-rune-title","proposed_title":%q,
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}
	]}`, exactly80Runes, exactly81Runes)}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	// The 80-rune title is exactly at the cap and survives; 81 runes drops the brief.
	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Equal("eighty-rune-title", briefs[0].ProposedSlug)
	s.Equal(exactly80Runes, briefs[0].ProposedTitle)
}

func (s *llmSessionPlannerSuite) TestPlannerPromptPinsConceptIdentityAndTitleStyle() {
	prompt := pagewiki.PageWikiPlannerPromptForTest
	s.Contains(prompt, "durable concept")
	s.Contains(prompt, "never an activity")
	s.Contains(prompt, "at most five words")
	s.Contains(prompt, "no trailing period")
}
