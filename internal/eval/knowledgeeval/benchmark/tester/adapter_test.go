package tester

import (
	"context"
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

func (s *AdapterSuite) TestRunsMultiToolTrajectory() {
	adapter, err := NewAdapter(s.store, Config{Tasks: []Task{{
		ID: "inspect", Steps: []Step{
			{Tool: "search", Input: "architecture", Expected: "doc"},
			{Tool: "get", Input: "doc", Expected: "local-first"},
			{Tool: "navigate", Input: "doc", Expected: "Architecture"},
		},
	}}})
	s.Require().NoError(err)
	result, err := adapter.Run(s.ctx, fakeSubject{})
	s.Require().NoError(err)
	s.Equal("passed", result.Status)
	s.InDelta(1.0, result.Metrics[0].Value, 0.0001)
	s.Require().Len(result.CaseResults, 1)
	s.True(result.CaseResults[0].Correct)
	observations, err := s.store.OpenBytes(s.ctx, result.Observations)
	s.Require().NoError(err)
	s.Contains(string(observations), `"tool": "navigate"`)
}

func (s *AdapterSuite) TestReportsAssertionAndBudgetFailures() {
	adapter, err := NewAdapter(s.store, Config{MaxSteps: 1, Tasks: []Task{
		{ID: "assert", Steps: []Step{{Tool: "get", Input: "doc", Expected: "cloud-only"}}},
		{ID: "budget", Steps: []Step{
			{Tool: "get", Input: "doc", Expected: "local-first"},
			{Tool: "get", Input: "doc", Expected: "local-first"},
		}},
	}})
	s.Require().NoError(err)
	result, err := adapter.Run(s.ctx, fakeSubject{})
	s.Require().NoError(err)
	s.Equal("failed", result.Status)
	s.Equal("agent_assertion", result.CaseResults[0].FailureStage)
	s.Equal("step_budget", result.CaseResults[1].FailureStage)
}

func (s *AdapterSuite) TestValidatesConfigurationAndSubjectTools() {
	_, err := NewAdapter(nil, Config{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewAdapter(s.store, Config{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewAdapter(s.store, Config{Tasks: []Task{{ID: "bad"}}})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewAdapter(s.store, Config{Tasks: []Task{{
		ID: "bad", Steps: []Step{{Tool: "unknown", Input: "x", Expected: "x"}},
	}}})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	adapter, err := NewAdapter(s.store, Config{Tasks: []Task{{
		ID: "recall", Steps: []Step{{Tool: "recall", Input: "q", Expected: "x"}},
	}}})
	s.Require().NoError(err)
	_, err = adapter.Run(s.ctx, fakeSubject{})
	s.Require().ErrorIs(err, knowledgeeval.ErrCapabilityMissing)
	s.Len(adapter.Descriptor().RequiredCapabilities, 1)
}

type fakeSubject struct{}

func (fakeSubject) ID() string { return "fake" }
func (fakeSubject) Capabilities() knowledgeeval.CapabilitySet {
	return knowledgeeval.CapabilitySet{
		{Name: knowledgeeval.SearchCapability, Version: "v1"},
		{Name: knowledgeeval.GetCapability, Version: "v1"},
		{Name: knowledgeeval.NavigateCapability, Version: "v1"},
	}
}
func (fakeSubject) Search(
	_ context.Context,
	_ knowledgeeval.SearchRequest,
) (knowledgeeval.SearchResponse, error) {
	return knowledgeeval.SearchResponse{Hits: []knowledgeeval.SearchHit{{
		Ref: "doc", Text: "Architecture", Score: 1,
	}}}, nil
}
func (fakeSubject) Get(
	_ context.Context,
	_ knowledgeeval.GetRequest,
) (knowledgeeval.GetResponse, error) {
	return knowledgeeval.GetResponse{Ref: "doc", Text: "The system is local-first."}, nil
}
func (fakeSubject) Navigate(
	_ context.Context,
	_ knowledgeeval.NavigateRequest,
) (knowledgeeval.NavigateResponse, error) {
	return knowledgeeval.NavigateResponse{Roots: []knowledgeeval.NavigationNode{{
		Ref: "doc", Title: "Architecture",
	}}}, nil
}
