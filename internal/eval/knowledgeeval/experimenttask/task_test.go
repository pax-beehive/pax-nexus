package experimenttask

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/dashboard"
	"github.com/stretchr/testify/suite"
)

type plannerSuite struct {
	suite.Suite
	ctx context.Context
}

func TestPlanner(t *testing.T) {
	suite.Run(t, new(plannerSuite))
}

func (s *plannerSuite) SetupTest() {
	s.ctx = context.Background()
}

func (s *plannerSuite) TestPreviewsSupportedModesAndIneligibleSources() {
	tests := []struct {
		name          string
		group         dashboard.Dataset
		llmConfigured bool
		request       Request
		eligible      bool
		paid          bool
		plannedRuns   int
		maxCalls      int
		reason        string
	}{
		{
			name: "baseline",
			group: dashboard.Dataset{
				Name: "locomo", Partition: "train", CaseID: "conv-26",
				SourceKind: "long-running-conversation", EvaluationCases: 152,
				CaseIDs: []string{"conv-26"},
			},
			request: Request{
				Dataset: "locomo", Partition: "train", GroupID: "conv-26",
				Mode: ModeBaseline, QuestionLimit: 7,
			},
			eligible: true, plannedRuns: 1,
		},
		{
			name: "maintainer",
			group: dashboard.Dataset{
				Name: "longmemeval", Partition: "train", CaseID: "case-1",
				SourceKind: "chat-session-history", EvaluationCases: 1,
				CaseIDs: []string{"case-1"},
			},
			llmConfigured: true,
			request: Request{
				Dataset: "longmemeval", Partition: "train", GroupID: "case-1",
				Mode: ModeMaintainer, QuestionLimit: 5, MaxRounds: 10,
			},
			eligible: true, paid: true, plannedRuns: 2, maxCalls: 14,
		},
		{
			name: "reuse maintained artifact for additional questions",
			group: dashboard.Dataset{
				Name: "locomo", Partition: "train", CaseID: "conv-26",
				SourceKind: "long-running-conversation", EvaluationCases: 152,
				CaseIDs: []string{"conv-26"},
			},
			llmConfigured: true,
			request: Request{
				Dataset: "locomo", Partition: "train", GroupID: "conv-26",
				Mode: ModeMaintainer, ReaderModel: "reader", QuestionLimit: 5,
				QuestionOffset: 10, ReuseArtifactFromTaskID: "task-source",
			},
			eligible: true, paid: true, plannedRuns: 1, maxCalls: 10,
		},
		{
			name: "missing LLM key",
			group: dashboard.Dataset{
				Name: "locomo", Partition: "train", CaseID: "conv-26",
				SourceKind: "long-running-conversation", EvaluationCases: 2,
				CaseIDs: []string{"conv-26"},
			},
			request: Request{
				Dataset: "locomo", Partition: "train", GroupID: "conv-26",
				Mode: ModeMaintainer,
			},
			paid: true, plannedRuns: 2, maxCalls: 38,
			reason: "DEEPSEEK_API_KEY",
		},
		{
			name: "trajectory environment",
			group: dashboard.Dataset{
				Name: "longmemeval-v2", Partition: "train", CaseID: "env-1",
				SourceKind: "agent-trajectory-haystack", EvaluationCases: 8,
				CaseIDs: []string{"case-1"},
			},
			request: Request{
				Dataset: "longmemeval-v2", Partition: "train", GroupID: "env-1",
				Mode: ModeBaseline,
			},
			plannedRuns: 1, reason: "trajectory",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			planner, err := NewPlanner(
				&plannerCatalog{group: test.group},
				test.llmConfigured,
			)
			s.Require().NoError(err)
			preview, err := planner.Preview(s.ctx, test.request)
			s.Require().NoError(err)
			s.Equal(test.eligible, preview.Eligible)
			s.Equal(test.paid, preview.Paid)
			s.Equal(test.plannedRuns, preview.PlannedRuns)
			s.Equal(test.maxCalls, preview.MaxLLMCalls)
			s.Contains(preview.IneligibleReason, test.reason)
		})
	}
}

func (s *plannerSuite) TestRejectsArtifactReuseAfterAllQuestionsWereEvaluated() {
	planner, err := NewPlanner(&plannerCatalog{group: dashboard.Dataset{
		Name: "locomo", Partition: "train", CaseID: "conv-26",
		SourceKind: "long-running-conversation", EvaluationCases: 10,
		CaseIDs: []string{"conv-26"},
	}}, true)
	s.Require().NoError(err)
	preview, err := planner.Preview(s.ctx, Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		Mode: ModeMaintainer, QuestionLimit: 5, QuestionOffset: 10,
		ReuseArtifactFromTaskID: "task-source",
	})
	s.Require().NoError(err)
	s.False(preview.Eligible)
	s.Contains(preview.IneligibleReason, "all questions")
}

func (s *plannerSuite) TestValidatesRequestAndDependencies() {
	_, err := NewPlanner(nil, false)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	planner, err := NewPlanner(&plannerCatalog{err: knowledgeeval.ErrNotFound}, false)
	s.Require().NoError(err)
	_, err = planner.Preview(s.ctx, Request{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = planner.Preview(s.ctx, Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26", Mode: "unknown",
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = planner.Preview(s.ctx, Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)

	tests := []struct {
		name   string
		group  dashboard.Dataset
		reason string
	}{
		{
			name: "unknown source",
			group: dashboard.Dataset{
				Name: "dataset", Partition: "train", CaseID: "case",
				SourceKind: "unknown", EvaluationCases: 1, CaseIDs: []string{"case"},
			},
			reason: "no experiment adapter",
		},
		{
			name: "grouped cases",
			group: dashboard.Dataset{
				Name: "dataset", Partition: "train", CaseID: "group",
				SourceKind: "chat-session-history", EvaluationCases: 2,
				CaseIDs: []string{"one", "two"},
			},
			reason: "multiple cases",
		},
		{
			name: "no questions",
			group: dashboard.Dataset{
				Name: "dataset", Partition: "train", CaseID: "case",
				SourceKind: "chat-session-history", CaseIDs: []string{"case"},
			},
			reason: "no evaluation questions",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			candidate, plannerErr := NewPlanner(&plannerCatalog{group: test.group}, true)
			s.Require().NoError(plannerErr)
			preview, previewErr := candidate.Preview(s.ctx, Request{
				Dataset: "dataset", Partition: "train", GroupID: test.group.CaseID,
			})
			s.Require().NoError(previewErr)
			s.False(preview.Eligible)
			s.Contains(preview.IneligibleReason, test.reason)
		})
	}
}

func (s *plannerSuite) TestListsSupportedModels() {
	models := SupportedModels()
	s.Equal(
		[]ModelOption{
			{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Provider: "deepseek"},
			{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Provider: "deepseek"},
		},
		models,
	)
}

type plannerCatalog struct {
	group dashboard.Dataset
	err   error
}

func (c *plannerCatalog) GetDataset(
	context.Context,
	string,
	string,
	string,
) (dashboard.Dataset, []string, error) {
	return c.group, nil, c.err
}

var _ DatasetCatalog = (*plannerCatalog)(nil)
