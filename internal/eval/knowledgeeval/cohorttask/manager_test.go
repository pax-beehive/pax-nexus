package cohorttask

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/dashboard"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/experimenttask"
	"github.com/stretchr/testify/suite"
)

type ManagerSuite struct {
	suite.Suite
	ctx     context.Context
	catalog *cohortCatalog
	tasks   *cohortTasks
	manager *Manager
}

func TestManagerSuite(t *testing.T) {
	suite.Run(t, new(ManagerSuite))
}

func (s *ManagerSuite) SetupTest() {
	s.ctx = context.Background()
	s.catalog = &cohortCatalog{catalog: dashboard.Catalog{Datasets: []dashboard.Dataset{
		{
			Name: "locomo", Partition: "holdout", CaseID: "conv-1",
			SourceKind: "long-running-conversation", EvaluationCases: 2,
			CaseIDs: []string{"conv-1"},
		},
		{
			Name: "longmemeval-v2", Partition: "holdout", CaseID: "env-1",
			SourceKind: "agent-trajectory-haystack", EvaluationCases: 3,
			CaseIDs: []string{"env-1:a", "env-1:b", "env-1:c"},
		},
	}}}
	s.tasks = newCohortTasks()
	var err error
	s.manager, err = NewManager(ManagerConfig{
		Directory: s.T().TempDir(), Catalog: s.catalog, Tasks: s.tasks,
		Now:          func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) },
		IDGenerator:  func() (string, error) { return "cohort-1", nil },
		TickInterval: time.Hour,
	})
	s.Require().NoError(err)
	s.T().Cleanup(s.manager.Close)
}

func (s *ManagerSuite) TestPreviewExpandsGroupsAndReportsUnsupportedCoverage() {
	preview, err := s.manager.Preview(s.ctx, paidRequest())
	s.Require().NoError(err)
	s.True(preview.Eligible)
	s.True(preview.Paid)
	s.Equal(2, preview.TotalGroups)
	s.Equal(1, preview.EligibleGroups)
	s.Equal(1, preview.IneligibleGroups)
	s.Equal(5, preview.TotalQuestions)
	s.Equal(2, preview.PlannedQuestions)
	s.Equal(1, preview.PlannedTasks)
	s.Equal(38, preview.MaxLLMCalls)
	s.Require().Len(preview.Issues, 1)
	s.Contains(preview.Issues[0].Reason, "trajectory")
}

func (s *ManagerSuite) TestCreatesReconcilesAndAggregatesCompletedGroups() {
	campaign, err := s.manager.Create(s.ctx, paidRequest(), "create-1")
	s.Require().NoError(err)
	s.Equal(StatusQueued, campaign.Status)
	s.Require().NoError(s.manager.Reconcile(s.ctx))
	campaign, err = s.manager.Get("cohort-1")
	s.Require().NoError(err)
	s.Equal(StatusRunning, campaign.Status)
	s.Require().Len(s.tasks.created, 1)
	childID := campaign.Executions[0].TaskID
	s.tasks.complete(childID, "run-1")
	s.catalog.catalog.Runs = []dashboard.Run{{
		Dataset: "locomo", Partition: "holdout", CaseID: "conv-1",
		SolutionVersion: dashboard.SolutionVersion{BuilderID: "llmwiki-maintainer"},
		Detail: knowledgeeval.RunDetail{
			Run: knowledgeeval.Run{ID: "run-1"},
			Trials: []knowledgeeval.Trial{{
				BenchmarkID: "knowledge-search-get-qa",
				Result: &knowledgeeval.BenchmarkResult{Metrics: []knowledgeeval.Metric{
					{Name: "answer_accuracy", Value: 0.5, Unit: "ratio"},
					{Name: "case_count", Value: 2, Unit: "count"},
				}},
			}},
		},
	}}

	s.Require().NoError(s.manager.Reconcile(s.ctx))
	campaign, err = s.manager.Get("cohort-1")
	s.Require().NoError(err)
	s.Equal(StatusCompletedWithGaps, campaign.Status)
	s.Equal(1, campaign.Summary.EvaluatedGroups)
	s.Equal(2, campaign.Summary.EvaluatedQuestions)
	s.Equal(1, campaign.Summary.CorrectQuestions)
	s.InDelta(0.5, campaign.Summary.MicroAccuracy, 0.0001)
	s.InDelta(0.5, campaign.Summary.MacroAccuracy, 0.0001)
	s.InDelta(0.5, campaign.Summary.GroupCoverage, 0.0001)
}

func (s *ManagerSuite) TestRequiresPaidConfirmationAndCallCeiling() {
	request := paidRequest()
	request.ConfirmPaid = false
	_, err := s.manager.Create(s.ctx, request, "unconfirmed")
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	request = paidRequest()
	request.LLMCallLimit = 37
	_, err = s.manager.Create(s.ctx, request, "under-budget")
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *ManagerSuite) TestCancelOnlyCancelsOwnedChildTask() {
	campaign, err := s.manager.Create(s.ctx, paidRequest(), "cancel-1")
	s.Require().NoError(err)
	s.Require().NoError(s.manager.Reconcile(s.ctx))
	campaign, err = s.manager.Get(campaign.ID)
	s.Require().NoError(err)
	childID := campaign.Executions[0].TaskID

	campaign, err = s.manager.Cancel(campaign.ID)
	s.Require().NoError(err)
	s.Equal(StatusCancelled, campaign.Status)
	s.Equal([]string{childID}, s.tasks.cancelled)
	s.NotContains(s.tasks.cancelled, "task-7ec2fb1c4bb7")
}

func (s *ManagerSuite) TestPersistsAndReloadsCampaigns() {
	directory := s.T().TempDir()
	manager, err := NewManager(ManagerConfig{
		Directory: directory, Catalog: s.catalog, Tasks: s.tasks,
		IDGenerator:  func() (string, error) { return "cohort-persisted", nil },
		TickInterval: time.Hour,
	})
	s.Require().NoError(err)
	campaign, err := manager.Create(s.ctx, paidRequest(), "persist-key")
	s.Require().NoError(err)
	manager.Close()

	reloaded, err := NewManager(ManagerConfig{
		Directory: directory, Catalog: s.catalog, Tasks: s.tasks,
		TickInterval: time.Hour,
	})
	s.Require().NoError(err)
	s.T().Cleanup(reloaded.Close)
	s.Require().Len(reloaded.List(), 1)
	loaded, err := reloaded.Get(campaign.ID)
	s.Require().NoError(err)
	s.Equal(campaign.RequestDigest, loaded.RequestDigest)
	duplicate, err := reloaded.Create(s.ctx, paidRequest(), "persist-key")
	s.Require().NoError(err)
	s.Equal(campaign.ID, duplicate.ID)

	changed := paidRequest()
	changed.Name = "different"
	_, err = reloaded.Create(s.ctx, changed, "persist-key")
	s.Require().ErrorIs(err, ErrConflict)
}

func (s *ManagerSuite) TestValidatesManagerConfigurationAndGeneratesIDs() {
	_, err := NewManager(ManagerConfig{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewManager(ManagerConfig{Catalog: s.catalog, Tasks: s.tasks})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	id, err := randomCampaignID()
	s.Require().NoError(err)
	s.Regexp(`^cohort-[0-9a-f]{12}$`, id)
}

func paidRequest() Request {
	return Request{
		Name: "holdout candidate",
		Selections: []DatasetSelection{
			{Dataset: "locomo", Partition: "holdout"},
			{Dataset: "longmemeval-v2", Partition: "holdout"},
		},
		Recipe: Recipe{
			Mode: experimenttask.ModeMaintainer, Model: "deepseek-v4-flash",
			ReaderModel: "deepseek-v4-flash", MaxRounds: 30,
		},
		ConfirmPaid: true, LLMCallLimit: 38,
	}
}

type cohortCatalog struct {
	catalog dashboard.Catalog
}

func (c *cohortCatalog) Load(context.Context) (dashboard.Catalog, error) {
	return c.catalog, nil
}

type cohortTasks struct {
	tasks     map[string]experimenttask.Task
	created   []experimenttask.Request
	cancelled []string
}

func newCohortTasks() *cohortTasks {
	return &cohortTasks{tasks: make(map[string]experimenttask.Task)}
}

func (t *cohortTasks) Preview(
	_ context.Context,
	request experimenttask.Request,
) (experimenttask.Preview, error) {
	if request.Dataset == "longmemeval-v2" {
		return experimenttask.Preview{
			Eligible: false, IneligibleReason: "agent trajectory adapter is unavailable",
		}, nil
	}
	return experimenttask.Preview{
		Eligible: true, Paid: request.Mode == experimenttask.ModeMaintainer,
		SelectedQuestions: request.QuestionLimit,
		MaxLLMCalls:       request.MaxRounds + 4*request.QuestionLimit,
	}, nil
}

func (t *cohortTasks) Create(
	_ context.Context,
	request experimenttask.Request,
	key string,
) (experimenttask.Task, error) {
	id := "task-" + key
	task := experimenttask.Task{
		ID: id, Request: request, Status: experimenttask.StatusQueued,
		Preview: experimenttask.Preview{SelectedQuestions: request.QuestionLimit},
	}
	t.tasks[id] = task
	t.created = append(t.created, request)
	return task, nil
}

func (t *cohortTasks) Get(id string) (experimenttask.Task, error) {
	return t.tasks[id], nil
}

func (t *cohortTasks) Cancel(id string) (experimenttask.Task, error) {
	t.cancelled = append(t.cancelled, id)
	task := t.tasks[id]
	task.Status = experimenttask.StatusCancelled
	t.tasks[id] = task
	return task, nil
}

func (t *cohortTasks) complete(id, runID string) {
	task := t.tasks[id]
	task.Status = experimenttask.StatusCompleted
	task.RunIDs = []string{runID}
	t.tasks[id] = task
}
