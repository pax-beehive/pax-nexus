package llm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/stretchr/testify/suite"
)

type jsonSuite struct {
	suite.Suite
}

func TestJSONSuite(t *testing.T) {
	suite.Run(t, new(jsonSuite))
}

type payload struct {
	Name  string `json:"name"`
	Extra string `json:"extra"`
}

type scriptedChatClient struct {
	responses    []llm.ChatResponse
	errs         []error
	requests     []llm.ChatRequest
	finishReason string
}

func (c *scriptedChatClient) Complete(
	_ context.Context,
	request llm.ChatRequest,
) (llm.ChatResponse, error) {
	index := len(c.requests)
	c.requests = append(c.requests, request)
	if index < len(c.errs) && c.errs[index] != nil {
		return llm.ChatResponse{}, c.errs[index]
	}
	if index >= len(c.responses) {
		return llm.ChatResponse{}, errors.New("scripted chat client: no response left")
	}
	response := c.responses[index]
	if response.FinishReason == "" {
		response.FinishReason = c.finishReason
	}
	return response, nil
}

func textResponse(content string) llm.ChatResponse {
	return llm.ChatResponse{Message: llm.ChatMessage{Role: "assistant", Content: content}}
}

func (s *jsonSuite) TestDecodesFirstReplyAndStripsMarkdownFence() {
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		textResponse("```json\n{\"name\":\"ok\"}\n```"),
	}}

	decoded, err := llm.CompleteJSON[payload](context.Background(), client, llm.ChatRequest{}, 2)

	s.Require().NoError(err)
	s.Equal("ok", decoded.Name)
	s.Len(client.requests, 1)
}

func (s *jsonSuite) TestRetriesTransportErrorThenSucceeds() {
	client := &scriptedChatClient{
		errs:      []error{errors.New("connection reset")},
		responses: []llm.ChatResponse{{}, textResponse(`{"name":"ok"}`)},
	}

	decoded, err := llm.CompleteJSON[payload](context.Background(), client, llm.ChatRequest{}, 2)

	s.Require().NoError(err)
	s.Equal("ok", decoded.Name)
	s.Len(client.requests, 2)
}

func (s *jsonSuite) TestInvalidJSONExhaustsAttemptsWithSentinelAndFinishReason() {
	client := &scriptedChatClient{
		responses: []llm.ChatResponse{
			textResponse(`{"name":"trunc`),
			textResponse(`{"name":"trunc`),
		},
		finishReason: "length",
	}

	_, err := llm.CompleteJSON[payload](context.Background(), client, llm.ChatRequest{}, 2)

	s.Require().ErrorIs(err, llm.ErrInvalidJSON)
	s.Require().ErrorContains(err, "length")
	s.Len(client.requests, 2)
}

func (s *jsonSuite) TestAcceptRejectionRetriesAndDecodesFreshValueEachAttempt() {
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		textResponse(`{"name":"","extra":"stale"}`),
		textResponse(`{"name":"ok"}`),
	}}
	accepted, err := llm.CompleteJSONAs(
		context.Background(), client, llm.ChatRequest{}, 2,
		func(decoded payload) (string, error) {
			if decoded.Name == "" {
				return "", errors.New("name is blank")
			}
			// A stale Extra would prove attempt two reused attempt one's value.
			if decoded.Extra != "" {
				return "", errors.New("stale field leaked across attempts")
			}
			return decoded.Name, nil
		},
	)

	s.Require().NoError(err)
	s.Equal("ok", accepted)
	s.Len(client.requests, 2)
}

func (s *jsonSuite) TestAcceptErrorSurfacesUnwrappedAfterAllAttempts() {
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		textResponse(`{"name":""}`),
		textResponse(`{"name":""}`),
	}}
	blank := errors.New("name is blank")

	_, err := llm.CompleteJSONAs(
		context.Background(), client, llm.ChatRequest{}, 2,
		func(decoded payload) (string, error) {
			if decoded.Name == "" {
				return "", blank
			}
			return decoded.Name, nil
		},
	)

	s.Require().ErrorIs(err, blank)
	s.Require().NotErrorIs(err, llm.ErrInvalidJSON)
}
