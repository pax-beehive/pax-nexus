package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type llmCuratorSuite struct {
	suite.Suite
}

func TestLLMCuratorSuite(t *testing.T) {
	suite.Run(t, new(llmCuratorSuite))
}

func curatorPageA() pagewiki.CurationPageView {
	return pagewiki.CurationPageView{
		PageID:   "page-a",
		Title:    "Release Policy",
		Summary:  "How releases ship.",
		Markdown: "Releases ship weekly.",
		Quotes: []pagewiki.CurationQuote{
			{ExactText: "releases ship weekly", SourceOrdinal: 1},
		},
	}
}

func curatorPageB() pagewiki.CurationPageView {
	return pagewiki.CurationPageView{
		PageID:   "page-b",
		Title:    "Release Cadence",
		Summary:  "How often releases ship.",
		Markdown: "Releases ship every two weeks now.",
		Quotes: []pagewiki.CurationQuote{
			{ExactText: "releases ship every two weeks now", SourceOrdinal: 2},
		},
	}
}

func (s *llmCuratorSuite) TestJudgePairDecodesMergeVerdictFromFencedReply() {
	client := &wikiChatClient{responsesByIndex: []string{
		"```json\n" + `{"verdict":"merge","rationale":"Same subject, complementary facts.",
		 "draft":{"title":"Release Policy","summary":"How releases ship.",
		 "sections":[{"key":"cadence","heading":"Cadence","markdown":"Releases ship every two weeks."}]}}` + "\n```",
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	verdict, err := curator.JudgePair(context.Background(), pagewiki.PairJudgeInput{
		A: curatorPageA(), B: curatorPageB(),
	})

	s.Require().NoError(err)
	s.Equal(pagewiki.CurationVerdictMerge, verdict.Verdict)
	s.Equal("Same subject, complementary facts.", verdict.Rationale)
	s.Require().NotNil(verdict.Draft)
	s.Equal("Release Policy", verdict.Draft.Title)
	s.Equal("How releases ship.", verdict.Draft.Summary)
	s.Require().Len(verdict.Draft.Sections, 1)
	s.Equal("cadence", verdict.Draft.Sections[0].Key)
	s.Equal("Cadence", verdict.Draft.Sections[0].Heading)
	s.Equal("Releases ship every two weeks.", verdict.Draft.Sections[0].Markdown)

	s.Require().Len(client.requests, 1)
	s.Equal("test-model", client.requests[0].Model)
	s.Contains(client.requests[0].Messages[1].Content, "releases ship weekly")
	s.Contains(client.requests[0].Messages[1].Content, "\"recency_rank\":1")
	s.Contains(client.requests[0].Messages[1].Content, "\"recency_rank\":2")
	s.NotContains(client.requests[0].Messages[1].Content, "page-a")
}

func (s *llmCuratorSuite) TestJudgePairDecodesDistinctVerdictAndIgnoresDraft() {
	client := &wikiChatClient{responsesByIndex: []string{
		`{"verdict":"distinct","rationale":"Different subjects.",
		 "draft":{"title":"Ignored","summary":"Ignored","sections":[]}}`,
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	verdict, err := curator.JudgePair(context.Background(), pagewiki.PairJudgeInput{
		A: curatorPageA(), B: curatorPageB(),
	})

	s.Require().NoError(err)
	s.Equal(pagewiki.CurationVerdictDistinct, verdict.Verdict)
	s.Equal("Different subjects.", verdict.Rationale)
	s.Nil(verdict.Draft)
}

func (s *llmCuratorSuite) TestJudgePairRetriesOnUnknownVerdictThenErrors() {
	client := &wikiChatClient{responsesByIndex: []string{
		`{"verdict":"maybe","rationale":"unsure"}`,
		`{"verdict":"unclear","rationale":"still unsure"}`,
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	_, err = curator.JudgePair(context.Background(), pagewiki.PairJudgeInput{
		A: curatorPageA(), B: curatorPageB(),
	})

	s.Error(err)
	s.Len(client.requests, 2)
}

func (s *llmCuratorSuite) TestJudgePairErrorsWhenDraftMissingForMerge() {
	client := &wikiChatClient{responsesByIndex: []string{
		`{"verdict":"merge","rationale":"Same subject."}`,
		`{"verdict":"merge","rationale":"Same subject, still no draft."}`,
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	_, err = curator.JudgePair(context.Background(), pagewiki.PairJudgeInput{
		A: curatorPageA(), B: curatorPageB(),
	})

	s.Error(err)
	s.Len(client.requests, 2)
}

func (s *llmCuratorSuite) TestJudgePairAppendsDirectivesToSystemPrompt() {
	client := &wikiChatClient{responsesByIndex: []string{
		`{"verdict":"distinct","rationale":"Different subjects."}`,
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	_, err = curator.JudgePair(context.Background(), pagewiki.PairJudgeInput{
		A: curatorPageA(), B: curatorPageB(),
		Directives: pagewiki.GenerationDirectives{Language: "Spanish"},
	})

	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	s.Contains(client.requests[0].Messages[0].Content, "Spanish")
}

func (s *llmCuratorSuite) TestJudgePageDecodesRewriteVerdict() {
	client := &wikiChatClient{responsesByIndex: []string{
		`{"verdict":"rewrite","rationale":"Durable subject, weak structure.",
		 "draft":{"title":"Release Policy","summary":"How releases ship.",
		 "sections":[{"key":"cadence","heading":"Cadence","markdown":"Releases ship weekly."}]}}`,
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	verdict, err := curator.JudgePage(context.Background(), pagewiki.PageJudgeInput{
		Page: curatorPageA(), Signals: []string{"orphan", "sentence-title"},
	})

	s.Require().NoError(err)
	s.Equal(pagewiki.CurationVerdictRewrite, verdict.Verdict)
	s.Equal("Durable subject, weak structure.", verdict.Rationale)
	s.Require().NotNil(verdict.Draft)
	s.Equal("Release Policy", verdict.Draft.Title)

	s.Require().Len(client.requests, 1)
	s.Contains(client.requests[0].Messages[1].Content, "orphan")
	s.Contains(client.requests[0].Messages[1].Content, "sentence-title")
}

func (s *llmCuratorSuite) TestJudgePageDecodesRetireVerdictWithoutDraft() {
	client := &wikiChatClient{responsesByIndex: []string{
		`{"verdict":"retire","rationale":"No durable value."}`,
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	verdict, err := curator.JudgePage(context.Background(), pagewiki.PageJudgeInput{
		Page: curatorPageA(), Signals: []string{"orphan"},
	})

	s.Require().NoError(err)
	s.Equal(pagewiki.CurationVerdictRetire, verdict.Verdict)
	s.Nil(verdict.Draft)
}

func (s *llmCuratorSuite) TestJudgePageErrorsWhenDraftMissingForRewrite() {
	client := &wikiChatClient{responsesByIndex: []string{
		`{"verdict":"rewrite","rationale":"Missing draft."}`,
		`{"verdict":"rewrite","rationale":"Still missing draft."}`,
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	_, err = curator.JudgePage(context.Background(), pagewiki.PageJudgeInput{
		Page: curatorPageA(), Signals: []string{"orphan"},
	})

	s.Error(err)
	s.Len(client.requests, 2)
}

func (s *llmCuratorSuite) TestVerifyDecodesRefutedVerdict() {
	client := &wikiChatClient{responsesByIndex: []string{
		`{"refuted":true,"rationale":"The pages cover different subjects."}`,
	}}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	verdict, err := curator.Verify(context.Background(), pagewiki.VerifyInput{
		Action: pagewiki.CurationVerdictMerge, Rationale: "Same subject.",
		Pages: []pagewiki.CurationPageView{curatorPageA(), curatorPageB()},
	})

	s.Require().NoError(err)
	s.True(verdict.Refuted)
	s.Equal("The pages cover different subjects.", verdict.Rationale)

	s.Require().Len(client.requests, 1)
	s.Contains(client.requests[0].Messages[1].Content, "\"action\":\"merge\"")
}

func (s *llmCuratorSuite) TestVerifyRetriesOnCallErrorThenErrors() {
	client := &wikiChatClient{err: context.DeadlineExceeded}
	curator, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: client, Model: "test-model"})
	s.Require().NoError(err)

	_, err = curator.Verify(context.Background(), pagewiki.VerifyInput{
		Action: pagewiki.CurationVerdictRetire, Rationale: "No durable value.",
		Pages: []pagewiki.CurationPageView{curatorPageA()},
	})

	s.Error(err)
	s.Len(client.requests, 2)
}

func (s *llmCuratorSuite) TestNewLLMCuratorRequiresClient() {
	_, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Model: "test-model"})
	s.Error(err)
}

func (s *llmCuratorSuite) TestNewLLMCuratorRequiresModel() {
	_, err := pagewiki.NewLLMCurator(pagewiki.LLMCuratorConfig{Client: &wikiChatClient{}})
	s.Error(err)
}
