package qa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/stretchr/testify/suite"
)

type AdapterSuite struct {
	suite.Suite
	ctx   context.Context
	store *knowledgeeval.ArtifactStore
}

func TestAdapterSuite(t *testing.T) {
	suite.Run(t, new(AdapterSuite))
}

func (s *AdapterSuite) SetupTest() {
	s.ctx = context.Background()
	var err error
	s.store, err = knowledgeeval.NewArtifactStore(s.T().TempDir())
	s.Require().NoError(err)
}

func (s *AdapterSuite) TestReportsAnswerAndRetrievalStages() {
	adapter, err := NewAdapter(s.store, Config{Cases: []Case{
		{ID: "correct", Question: "architecture", Expected: "local-first", SupportRefs: []string{"doc"}},
		{ID: "reader", Question: "architecture", Expected: "cloud-only", SupportRefs: []string{"doc"}},
		{ID: "retrieval", Question: "missing", Expected: "unknown", SupportRefs: []string{"other"}},
	}})
	s.Require().NoError(err)
	result, err := adapter.Run(s.ctx, fakeSubject{})
	s.Require().NoError(err)
	s.Equal("failed", result.Status)
	s.InDelta(1.0/3.0, result.Metrics[0].Value, 0.0001)
	s.True(result.CaseResults[0].Correct)
	s.Equal("reader", result.CaseResults[1].FailureStage)
	s.Equal("retrieval", result.CaseResults[2].FailureStage)
	s.Equal("context-reader:v1", result.CaseResults[0].Metadata["reader_id"])
	_, err = s.store.OpenBytes(s.ctx, result.Observations)
	s.Require().NoError(err)
}

func (s *AdapterSuite) TestDistinguishesArtifactFailure() {
	adapter, err := NewAdapter(s.store, Config{Cases: []Case{{
		ID: "artifact", Question: "missing", Expected: "unknown",
		SupportRefs: []string{"D9:9"},
	}}})
	s.Require().NoError(err)
	result, err := adapter.Run(s.ctx, projectingSubject{
		fakeSubject: fakeSubject{}, store: s.store,
	})
	s.Require().NoError(err)
	s.Require().Len(result.CaseResults, 1)
	s.Equal("artifact", result.CaseResults[0].FailureStage)
	s.InDelta(0, metric(result.Metrics, "artifact_support_rate"), 0.0001)
	s.InDelta(0, metric(result.Metrics, "conditional_retrieval_rate"), 0.0001)
}

func (s *AdapterSuite) TestArtifactObservationDoesNotSkipReader() {
	reader := &recordingReader{answer: "unknown"}
	adapter, err := NewAdapter(s.store, Config{
		Cases: []Case{{
			ID: "artifact", Question: "missing",
			Expected:    "The information provided is not enough. You mentioned living in Harajuku but not Shinjuku.",
			AnswerKind:  AnswerUnanswerable,
			SupportRefs: []string{"D9:9"},
		}},
		Reader: reader,
	})
	s.Require().NoError(err)

	result, err := adapter.Run(s.ctx, projectingSubject{
		fakeSubject: fakeSubject{}, store: s.store,
	})
	s.Require().NoError(err)
	s.Require().Len(result.CaseResults, 1)
	s.True(result.CaseResults[0].Correct)
	s.Empty(result.CaseResults[0].FailureStage)
	s.Equal([]string{"missing"}, reader.questions)
	s.Equal("deterministic-answer-judge:v1", result.CaseResults[0].Metadata["judge_id"])
	s.Equal("abstention_match", result.CaseResults[0].Metadata["judge_reason_code"])
	s.InDelta(1, metric(result.CaseResults[0].Metrics, "answer_score"), 0.0001)
	s.InDelta(1, metric(result.Metrics, "answer_accuracy"), 0.0001)
	s.InDelta(1, metric(result.Metrics, "answer_score"), 0.0001)
	s.InDelta(0, metric(result.Metrics, "artifact_support_rate"), 0.0001)
}

func (s *AdapterSuite) TestReportsJudgeConfidenceAndDisputes() {
	adapter, err := NewAdapter(s.store, Config{
		Cases: []Case{{
			ID: "ambiguous", Question: "Where?", Expected: "In France",
		}},
		Reader: &recordingReader{answer: "France"},
		Judge: fixedJudge{judgment: AnswerJudgment{
			Correct: true, Score: 1, Verdict: "correct",
			Confidence: 0.72, Disputed: true,
			ReasonCode: "semantic_match", Reason: "Equivalent but underspecified.",
		}},
	})
	s.Require().NoError(err)

	result, err := adapter.Run(s.ctx, fakeSubject{})
	s.Require().NoError(err)
	s.Require().Len(result.CaseResults, 1)
	s.Equal("0.7200", result.CaseResults[0].Metadata["judge_confidence"])
	s.Equal("true", result.CaseResults[0].Metadata["judge_disputed"])
	s.Equal("Equivalent but underspecified.", result.CaseResults[0].Metadata["judge_reason"])
	s.InDelta(0.72, metric(result.Metrics, "judge_mean_confidence"), 0.0001)
	s.InDelta(1, metric(result.Metrics, "judge_disputed_rate"), 0.0001)
}

func (s *AdapterSuite) TestChatReaderIsAnswerBlind() {
	client := &readerChatClient{response: llm.ChatResponse{
		Message: llm.ChatMessage{Role: "assistant", Content: "local-first"},
	}}
	reader, err := NewChatReader(client, "reader-model")
	s.Require().NoError(err)
	answer, err := reader.Answer(s.ctx, "Where?", []knowledgeeval.GetResponse{{
		Ref: "wiki/index.md", Text: "The architecture is local-first.",
	}})
	s.Require().NoError(err)
	s.Equal("local-first", answer)
	s.Require().Len(client.requests, 1)
	encoded, err := json.Marshal(client.requests[0])
	s.Require().NoError(err)
	s.NotContains(string(encoded), "Expected")
	s.Contains(string(encoded), "Where?")
	s.Contains(reader.ID(), "reader-model")

	_, err = NewChatReader(nil, "reader-model")
	s.Require().Error(err)
	_, err = NewChatReader(client, "")
	s.Require().Error(err)
	client.err = errors.New("reader unavailable")
	_, err = reader.Answer(s.ctx, "Where?", nil)
	s.Require().ErrorContains(err, "reader unavailable")
}

func (s *AdapterSuite) TestValidatesInputAndCapabilities() {
	_, err := NewAdapter(nil, Config{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewAdapter(s.store, Config{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewAdapter(s.store, Config{Cases: []Case{{ID: "bad"}}})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	adapter, err := NewAdapter(s.store, Config{Cases: []Case{{
		ID: "a", Question: "q", Expected: "a",
	}}})
	s.Require().NoError(err)
	_, err = adapter.Run(s.ctx, bareSubject{})
	s.Require().ErrorIs(err, knowledgeeval.ErrCapabilityMissing)
	s.NotEmpty(adapter.Descriptor().Fingerprint())
	s.Equal("v2", adapter.Descriptor().Version)
}

type bareSubject struct{}

func (bareSubject) ID() string { return "bare" }
func (bareSubject) Capabilities() knowledgeeval.CapabilitySet {
	return nil
}

type fakeSubject struct{}

func (fakeSubject) ID() string { return "fake" }
func (fakeSubject) Capabilities() knowledgeeval.CapabilitySet {
	return knowledgeeval.CapabilitySet{
		{Name: knowledgeeval.SearchCapability, Version: "v1"},
		{Name: knowledgeeval.GetCapability, Version: "v1"},
	}
}

type projectingSubject struct {
	fakeSubject
	store *knowledgeeval.ArtifactStore
}

func (s projectingSubject) Project(
	ctx context.Context,
	_ knowledgeeval.ProjectionRequest,
) (knowledgeeval.ProjectionResponse, error) {
	corpus := knowledgeeval.WikiCorpus{
		SchemaVersion: "pax.knowledge-eval.wiki-corpus.v1",
		Documents: []knowledgeeval.WikiDocument{{
			Ref: "wiki/index.md", Title: "Index", Body: "Known facts.",
			Metadata: map[string]string{"support_refs": "D1:1"},
		}},
	}
	encoded, err := json.Marshal(corpus)
	if err != nil {
		return knowledgeeval.ProjectionResponse{}, err
	}
	ref, err := s.store.PutBytes(ctx, "wiki-corpus", "v1", encoded)
	if err != nil {
		return knowledgeeval.ProjectionResponse{}, err
	}
	return knowledgeeval.ProjectionResponse{Payload: ref}, nil
}

type readerChatClient struct {
	response llm.ChatResponse
	requests []llm.ChatRequest
	err      error
}

type recordingReader struct {
	answer    string
	questions []string
}

type fixedJudge struct {
	judgment AnswerJudgment
}

func (fixedJudge) ID() string { return "fixed-judge:v1" }

func (j fixedJudge) Judge(
	context.Context,
	AnswerJudgmentRequest,
) (AnswerJudgment, error) {
	return j.judgment, nil
}

func (*recordingReader) ID() string { return "recording-reader:v1" }

func (r *recordingReader) Answer(
	_ context.Context,
	question string,
	_ []knowledgeeval.GetResponse,
) (string, error) {
	r.questions = append(r.questions, question)
	return r.answer, nil
}

func (c *readerChatClient) Complete(
	_ context.Context,
	request llm.ChatRequest,
) (llm.ChatResponse, error) {
	c.requests = append(c.requests, request)
	return c.response, c.err
}

func metric(metrics []knowledgeeval.Metric, name string) float64 {
	for _, candidate := range metrics {
		if candidate.Name == name {
			return candidate.Value
		}
	}
	return -1
}
func (fakeSubject) Search(
	_ context.Context,
	request knowledgeeval.SearchRequest,
) (knowledgeeval.SearchResponse, error) {
	if request.Query == "missing" {
		return knowledgeeval.SearchResponse{}, nil
	}
	return knowledgeeval.SearchResponse{Hits: []knowledgeeval.SearchHit{{
		Ref: "doc", Text: "summary", Score: 1, Tokens: 2,
	}}}, nil
}
func (fakeSubject) Get(
	_ context.Context,
	request knowledgeeval.GetRequest,
) (knowledgeeval.GetResponse, error) {
	if request.Ref != "doc" {
		return knowledgeeval.GetResponse{}, fmt.Errorf("%w: document", knowledgeeval.ErrNotFound)
	}
	return knowledgeeval.GetResponse{
		Ref: "doc", Text: "The architecture is local-first.",
	}, nil
}
