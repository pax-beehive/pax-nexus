package extractor_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type sourceSpanSuite struct {
	suite.Suite
}

func TestSourceSpanSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(sourceSpanSuite))
}

func sourceSpanBody(subject string, topics string) string {
	content := fmt.Sprintf(`{"source_span":{"subject":%s,"topics":[%s]}}`, fmt.Sprintf("%q", subject), topics)
	return fmt.Sprintf(`{"choices":[{"message":{"content":%s}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`, fmt.Sprintf("%q", content))
}

func newSourceSpanAdapter(s *sourceSpanSuite, client *http.Client) extractor.Extractor {
	return newSourceSpanAdapterWithStrategy(s, client, extractor.CandidateStrategySourceSpanV1)
}

func newSourceSpanAdapterWithStrategy(s *sourceSpanSuite, client *http.Client, strategy string) extractor.Extractor {
	adapter, err := extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: "http://extractor.test", Model: "model", Client: client,
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: extractor.ExtractionVersionV2, V2Variant: strategy,
	})
	s.Require().NoError(err)
	return adapter
}

func (s *sourceSpanSuite) TestV2ShardsLongRawEventWithoutDroppingSourceText() {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, sourceSpanBody("consent evidence", `"consent","audit-trail"`)), nil
	})}
	adapter := newSourceSpanAdapterWithStrategy(s, client, extractor.CandidateStrategySourceSpanV2)
	slice := v2Slice()
	raw := "source-start " + strings.Repeat("audit-trail-consent-timestamp ", 800) + "source-end"
	slice.Events[0].Content = raw

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "source-span-v2-scope"), slice)

	s.Require().NoError(err)
	s.Greater(len(result.Candidates), 10)
	s.Len(result.SourceSpans, len(result.Candidates))
	s.Equal(extractor.ExtractionVersionSourceSpanV2, result.ExtractionVersion)
	for _, candidate := range result.Candidates {
		s.Equal(teamnote.KindSourceSpan, candidate.Kind)
		s.Equal([]string{"event-1"}, candidate.EvidenceEventIDs)
		s.LessOrEqual(len(candidate.Body), 900)
	}
	s.Equal(raw, sourceSpanRawText(result.Candidates))
}

func sourceSpanRawText(candidates []teamnote.Candidate) string {
	eventHeader := regexp.MustCompile(`(?m)^\[event_id=.* part=\d+/\d+\]\n`)
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		body := strings.TrimPrefix(candidate.Body, "[source span shard; raw session content, not normalized state]\n\n")
		body = eventHeader.ReplaceAllString(body, "")
		parts = append(parts, body)
	}
	return strings.ReplaceAll(strings.Join(parts, ""), "\n\n", "")
}

func (s *sourceSpanSuite) TestPersistsRawNewEventsWithModelMetadataOnly() {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		s.Contains(string(body), "Do not create canonical state")
		return response(http.StatusOK, sourceSpanBody("release evidence", `"release","validation"`)), nil
	})}
	adapter := newSourceSpanAdapter(s, client)
	slice := v2Slice()
	second := slice.Events[0]
	second.ID = "event-2"
	second.Sequence++
	second.Content = "Finance confirms the evidence bundle is ready."
	slice.Events = append(slice.Events, second)
	slice.NewEventIDs = []string{"event-1", "event-2"}

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "source-span-scope"), slice)

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	candidate := result.Candidates[0]
	s.Equal(teamnote.ActionCreate, candidate.Action)
	s.Equal(teamnote.KindSourceSpan, candidate.Kind)
	s.Equal("release evidence", candidate.Subject)
	s.Equal([]string{"release", "validation"}, candidate.RelatedSubjects)
	s.Equal([]string{"event-1", "event-2"}, candidate.EvidenceEventIDs)
	s.Require().NoError(teamnote.ValidateCandidate(candidate, teamnote.DefaultTTLPolicy()))
	s.Contains(candidate.Body, "Tests are failing.")
	s.Contains(candidate.Body, "Finance confirms the evidence bundle is ready.")
	s.Contains(candidate.Body, "event_id=event-1")
	s.Contains(candidate.Body, "event_id=event-2")
	s.NotContains(candidate.Body, "release evidence")
	s.Equal(extractor.ExtractionVersionSourceSpanV1, result.ExtractionVersion)
	s.Require().Len(result.SourceSpans, 1)
	s.Equal(candidate.EvidenceEventIDs, result.SourceSpans[0].EvidenceEventIDs)
}

func (s *sourceSpanSuite) TestFallsBackToDeterministicMetadataWhenModelOmitsIt() {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, sourceSpanBody("", "")), nil
	})}
	adapter := newSourceSpanAdapter(s, client)

	result, err := adapter.Extract(teamnote.WithScope(context.Background(), "source-span-fallback"), v2Slice())

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal("session source span", result.Candidates[0].Subject)
	s.Empty(result.Candidates[0].RelatedSubjects)
	s.Contains(result.Candidates[0].Body, "Tests are failing.")
}
