package qa

import (
	"context"
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
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
	_, err = s.store.OpenBytes(s.ctx, result.Observations)
	s.Require().NoError(err)
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
