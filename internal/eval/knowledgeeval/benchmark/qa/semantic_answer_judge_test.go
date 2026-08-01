package qa

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/stretchr/testify/suite"
)

type SemanticAnswerJudgeSuite struct {
	suite.Suite
	client *judgeChatClient
}

func TestSemanticAnswerJudgeSuite(t *testing.T) {
	suite.Run(t, new(SemanticAnswerJudgeSuite))
}

func (s *SemanticAnswerJudgeSuite) SetupTest() {
	s.client = &judgeChatClient{}
}

func (s *SemanticAnswerJudgeSuite) TestJudgesSemanticEquivalence() {
	s.client.response = llm.ChatResponse{Message: llm.ChatMessage{
		Role: "assistant",
		Content: `{"verdict":"correct","confidence":0.96,"disputed":false,` +
			`"reason_code":"semantic_match","reason":"Equivalent location."}`,
	}}
	judge, err := NewSemanticAnswerJudge(s.client, "judge-model")
	s.Require().NoError(err)

	judgment, err := judge.Judge(context.Background(), AnswerJudgmentRequest{
		Question: "Where was it purchased?", Expected: "In France",
		Actual: "France", Kind: AnswerFact,
	})
	s.Require().NoError(err)
	s.True(judgment.Correct)
	s.Equal("correct", judgment.Verdict)
	s.InDelta(1, judgment.Score, 0.0001)
	s.InDelta(0.96, judgment.Confidence, 0.0001)
	s.False(judgment.Disputed)
	s.Equal("semantic_match", judgment.ReasonCode)
	s.Equal("Equivalent location.", judgment.Reason)
	s.Equal("semantic-answer-judge:v1:judge-model", judge.ID())
	s.Require().Len(s.client.requests, 1)
	s.Contains(s.client.requests[0].Messages[1].Content, "In France")
	s.Contains(s.client.requests[0].Messages[1].Content, "France")
}

func (s *SemanticAnswerJudgeSuite) TestMarksLowConfidenceAsDisputed() {
	s.client.response = llm.ChatResponse{Message: llm.ChatMessage{
		Content: "```json\n" +
			`{"verdict":"incorrect","confidence":0.61,"disputed":false,` +
			`"reason_code":"partial_answer","reason":"A required item may be missing."}` +
			"\n```",
	}}
	judge, err := NewSemanticAnswerJudge(s.client, "judge-model")
	s.Require().NoError(err)

	judgment, err := judge.Judge(context.Background(), AnswerJudgmentRequest{
		Expected: "red, blue", Actual: "red", Kind: AnswerList,
	})
	s.Require().NoError(err)
	s.False(judgment.Correct)
	s.True(judgment.Disputed)
	s.InDelta(0.61, judgment.Confidence, 0.0001)
}

func (s *SemanticAnswerJudgeSuite) TestRejectsInvalidResponsesAndConfiguration() {
	_, err := NewSemanticAnswerJudge(nil, "judge-model")
	s.Require().Error(err)
	_, err = NewSemanticAnswerJudge(s.client, "")
	s.Require().Error(err)

	tests := []struct {
		name     string
		response string
	}{
		{name: "invalid JSON", response: "correct"},
		{name: "invalid verdict", response: `{"verdict":"partial","confidence":0.8}`},
		{name: "invalid confidence", response: `{"verdict":"correct","confidence":2}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.client.response = llm.ChatResponse{Message: llm.ChatMessage{Content: test.response}}
			judge, judgeErr := NewSemanticAnswerJudge(s.client, "judge-model")
			s.Require().NoError(judgeErr)
			_, judgeErr = judge.Judge(context.Background(), AnswerJudgmentRequest{})
			s.Require().Error(judgeErr)
		})
	}

	s.client.err = errors.New("judge unavailable")
	judge, err := NewSemanticAnswerJudge(s.client, "judge-model")
	s.Require().NoError(err)
	_, err = judge.Judge(context.Background(), AnswerJudgmentRequest{})
	s.Require().ErrorContains(err, "judge unavailable")
}

type judgeChatClient struct {
	response llm.ChatResponse
	requests []llm.ChatRequest
	err      error
}

func (c *judgeChatClient) Complete(
	_ context.Context,
	request llm.ChatRequest,
) (llm.ChatResponse, error) {
	c.requests = append(c.requests, request)
	return c.response, c.err
}
