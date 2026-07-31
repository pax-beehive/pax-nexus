package quality

import (
	"context"
	"encoding/json"
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

func (s *AdapterSuite) TestScoresGoodAndBadCorpora() {
	adapter, err := NewAdapter(s.store, Config{MinimumScore: 0.8})
	s.Require().NoError(err)
	good := s.subject(knowledgeeval.WikiCorpus{Documents: []knowledgeeval.WikiDocument{{
		Ref: "a", Title: "Architecture", Body: "The system is local-first.",
		Citations: []string{"source#1"},
	}}})
	result, err := adapter.Run(s.ctx, good)
	s.Require().NoError(err)
	s.Equal("passed", result.Status)
	s.InDelta(1.0, result.Metrics[0].Value, 0.0001)
	s.Require().Len(result.CaseResults, 1)
	s.True(result.CaseResults[0].Correct)
	_, err = s.store.OpenBytes(s.ctx, result.RawReport)
	s.Require().NoError(err)

	bad := s.subject(knowledgeeval.WikiCorpus{Documents: []knowledgeeval.WikiDocument{{
		Ref: "a", Title: "", Body: "", Links: []string{"missing"},
	}}})
	result, err = adapter.Run(s.ctx, bad)
	s.Require().NoError(err)
	s.Equal("failed", result.Status)
	s.Equal("artifact_quality", result.CaseResults[0].FailureStage)
}

func (s *AdapterSuite) TestValidatesConfigurationAndSubject() {
	_, err := NewAdapter(nil, Config{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewAdapter(s.store, Config{MinimumScore: 2})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	adapter, err := NewAdapter(s.store, Config{})
	s.Require().NoError(err)
	_, err = adapter.Run(s.ctx, bareSubject{})
	s.Require().ErrorIs(err, knowledgeeval.ErrCapabilityMissing)
	s.NotEmpty(adapter.Descriptor().Fingerprint())
}

func (s *AdapterSuite) TestEmptyCorpusFails() {
	adapter, err := NewAdapter(s.store, Config{})
	s.Require().NoError(err)
	result, err := adapter.Run(s.ctx, s.subject(knowledgeeval.WikiCorpus{}))
	s.Require().NoError(err)
	s.Equal("failed", result.Status)
}

func (s *AdapterSuite) subject(corpus knowledgeeval.WikiCorpus) projectedSubject {
	encoded, err := json.Marshal(corpus)
	s.Require().NoError(err)
	ref, err := s.store.PutBytes(s.ctx, "wiki-corpus", "v1", encoded)
	s.Require().NoError(err)
	return projectedSubject{ref: ref}
}

type bareSubject struct{}

func (bareSubject) ID() string { return "bare" }
func (bareSubject) Capabilities() knowledgeeval.CapabilitySet {
	return nil
}

type projectedSubject struct {
	ref knowledgeeval.OpaqueRef
}

func (projectedSubject) ID() string { return "wiki" }
func (projectedSubject) Capabilities() knowledgeeval.CapabilitySet {
	return knowledgeeval.CapabilitySet{{
		Name: knowledgeeval.WikiCorpusCapability, Version: "v1",
	}}
}
func (s projectedSubject) Project(
	_ context.Context,
	_ knowledgeeval.ProjectionRequest,
) (knowledgeeval.ProjectionResponse, error) {
	return knowledgeeval.ProjectionResponse{Payload: s.ref}, nil
}
