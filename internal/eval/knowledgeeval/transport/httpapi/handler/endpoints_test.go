package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/stretchr/testify/suite"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/cohorttask"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/dashboard"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/datasetinstall"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/experimenttask"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/transport/httpapi/router"
)

type endpointSuite struct {
	suite.Suite
	server   *server.Hertz
	tasks    *endpointTaskService
	cohorts  *endpointCohortService
	installs *endpointDatasetInstallService
}

func TestEndpoints(t *testing.T) {
	suite.Run(t, new(endpointSuite))
}

func (s *endpointSuite) SetupTest() {
	root := s.T().TempDir()
	datasetRoot := filepath.Join(root, "locomo", "train", "conv-26")
	s.Require().NoError(os.MkdirAll(filepath.Join(datasetRoot, "views"), 0o755))
	treeRoot := filepath.Join(
		datasetRoot,
		"artifacts",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"tree",
		"wiki",
	)
	s.Require().NoError(os.MkdirAll(treeRoot, 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(datasetRoot, "views", "maintained-native.html"),
		[]byte(
			`<html><title>Maintained Wiki</title>`+
				`<a href="/wiki/caroline.md">Caroline</a></html>`,
		),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(treeRoot, "index.md"),
		[]byte("# Wiki\n\n- [Caroline](caroline.md)\n"),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(treeRoot, "caroline.md"),
		[]byte("# Caroline\n\nProfile page.\n"),
		0o644,
	))
	sourceRoot := filepath.Join(filepath.Dir(treeRoot), "sources")
	s.Require().NoError(os.MkdirAll(sourceRoot, 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(sourceRoot, "session-1.md"),
		[]byte(
			"# Immutable Session Source\n\n"+
				"- Session: `conv-26:session_1`\n"+
				"- Turn range: `[0,18)`\n\n"+
				"## user\n\nHello.\n",
		),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(datasetRoot, "dataset-run.json"),
		[]byte(testBundle),
		0o644,
	))
	registry, err := dashboard.NewRegistry(root)
	s.Require().NoError(err)
	s.tasks = &endpointTaskService{}
	s.cohorts = &endpointCohortService{}
	s.installs = &endpointDatasetInstallService{}
	httpHandler, err := handler.New(
		registry,
		handler.WithTaskService(s.tasks),
		handler.WithCohortService(s.cohorts),
		handler.WithDatasetInstallService(s.installs),
	)
	s.Require().NoError(err)
	s.server = server.Default()
	s.server.Use(handler.InstanceMiddleware(httpHandler))
	router.GeneratedRegister(s.server)
}

func (s *endpointSuite) TestDatasetInstallLifecycleAPI() {
	sources := s.getJSON("/v1/knowledge-eval/dataset-sources", http.StatusOK)
	s.Len(sources["items"], 1)
	source := s.asMap(s.asSlice(sources["items"])[0])
	s.Equal("locomo", source["id"])
	s.Equal("not_downloaded", source["install_status"])
	s.NotEmpty(source["note"])
	filteredSources := s.getJSON(
		"/v1/knowledge-eval/dataset-sources?dataset=missing",
		http.StatusOK,
	)
	s.Empty(filteredSources["items"])
	s.getJSON("/v1/knowledge-eval/dataset-sources?cursor=bad", http.StatusBadRequest)

	created := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/dataset-install-tasks",
		`{"dataset":"locomo"}`,
		ut.Header{Key: "Idempotency-Key", Value: "dataset-create-1"},
	)
	s.Equal(http.StatusAccepted, created.Code)
	s.Equal("dataset-create-1", s.installs.idempotencyKey)
	var createdBody map[string]any
	s.Require().NoError(json.Unmarshal(created.Body.Bytes(), &createdBody))
	s.Equal("dataset-task-1", s.asMap(createdBody["task"])["id"])
	s.NotEmpty(s.asMap(createdBody["task"])["started_at"])
	s.NotEmpty(s.asMap(createdBody["task"])["completed_at"])
	s.NotEmpty(s.asMap(createdBody["task"])["error"])

	list := s.getJSON("/v1/knowledge-eval/dataset-install-tasks", http.StatusOK)
	s.Len(list["items"], 1)
	filteredTasks := s.getJSON(
		"/v1/knowledge-eval/dataset-install-tasks?status=missing",
		http.StatusOK,
	)
	s.Empty(filteredTasks["items"])
	s.getJSON("/v1/knowledge-eval/dataset-install-tasks?cursor=bad", http.StatusBadRequest)
	detail := s.getJSON(
		"/v1/knowledge-eval/dataset-install-tasks/dataset-task-1",
		http.StatusOK,
	)
	s.Equal("queued", s.asMap(detail["task"])["status"])

	cancelled := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/dataset-install-tasks/dataset-task-1/cancel",
		"",
	)
	s.Equal(http.StatusOK, cancelled.Code)
	var cancelledBody map[string]any
	s.Require().NoError(json.Unmarshal(cancelled.Body.Bytes(), &cancelledBody))
	s.Equal("cancelled", s.asMap(cancelledBody["task"])["status"])

	s.installs.err = fmt.Errorf("%w: fixture", knowledgeeval.ErrInvalidRecord)
	failedCreate := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/dataset-install-tasks",
		`{"dataset":"locomo"}`,
	)
	s.Equal(http.StatusBadRequest, failedCreate.Code)
	s.getJSON(
		"/v1/knowledge-eval/dataset-install-tasks/dataset-task-1",
		http.StatusBadRequest,
	)
	failedCancel := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/dataset-install-tasks/dataset-task-1/cancel",
		"",
	)
	s.Equal(http.StatusBadRequest, failedCancel.Code)
}

func (s *endpointSuite) TestListsAndMatrix() {
	tests := []struct {
		name       string
		path       string
		itemCount  int
		assertBody func(map[string]any)
	}{
		{
			name: "solutions", path: "/v1/knowledge-eval/solutions", itemCount: 1,
			assertBody: func(body map[string]any) {
				items := s.asSlice(body["items"])
				s.Equal("llmwiki-maintainer", s.asMap(items[0])["builder_id"])
			},
		},
		{
			name: "datasets", path: "/v1/knowledge-eval/datasets", itemCount: 1,
			assertBody: func(body map[string]any) {
				items := s.asSlice(body["items"])
				s.Equal("locomo", s.asMap(items[0])["id"])
				s.InDelta(1, s.asFloat(s.asMap(items[0])["group_count"]), 0.0001)
			},
		},
		{
			name:      "dataset groups",
			path:      "/v1/knowledge-eval/datasets/locomo/groups?partition=train",
			itemCount: 1,
			assertBody: func(body map[string]any) {
				items := s.asSlice(body["items"])
				s.Equal("conv-26", s.asMap(items[0])["case_id"])
				s.Equal("conversation", s.asMap(items[0])["group_kind"])
			},
		},
		{
			name: "runs", path: "/v1/knowledge-eval/runs?benchmark_id=knowledge-search-get-qa", itemCount: 1,
			assertBody: func(body map[string]any) {
				items := s.asSlice(body["items"])
				s.Equal("run-maintained", s.asMap(items[0])["id"])
			},
		},
		{
			name: "benchmarks", path: "/v1/knowledge-eval/benchmarks", itemCount: 3,
			assertBody: func(body map[string]any) {
				items := s.asSlice(body["items"])
				s.Equal(true, s.asMap(items[0])["executed"])
			},
		},
		{
			name: "experiment models", path: "/v1/knowledge-eval/experiment-models", itemCount: 2,
			assertBody: func(body map[string]any) {
				items := s.asSlice(body["items"])
				s.Equal("deepseek-v4-flash", s.asMap(items[0])["id"])
				s.Equal("deepseek", s.asMap(items[0])["provider"])
			},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			response := ut.PerformRequest(s.server.Engine, http.MethodGet, test.path, nil)
			s.Equal(http.StatusOK, response.Code)
			var body map[string]any
			s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
			s.Len(body["items"], test.itemCount)
			test.assertBody(body)
		})
	}

	response := ut.PerformRequest(
		s.server.Engine,
		http.MethodGet,
		"/v1/knowledge-eval/results/matrix?dataset=locomo&case_id=conv-26",
		nil,
	)
	s.Equal(http.StatusOK, response.Code)
	var matrix map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &matrix))
	rows := s.asSlice(matrix["rows"])
	s.Len(rows, 1)
	cells := s.asSlice(s.asMap(rows[0])["cells"])
	s.InDelta(0.2, s.asFloat(s.asMap(cells[0])["score"]), 0.0001)
}

func (s *endpointSuite) TestRunArtifactAndView() {
	run := s.getJSON("/v1/knowledge-eval/runs/run-maintained", http.StatusOK)
	s.Len(run["trials"], 2)
	s.Len(run["events"], 1)
	runRecord := s.asMap(run["run"])
	s.Equal("deepseek-v4-pro", s.asMap(runRecord["metadata"])["model"])
	artifact := s.asMap(run["artifact"])
	s.Equal("artifact-maintained", artifact["artifact_id"])
	views := s.asMap(artifact["views"])
	s.Equal(
		"/v1/knowledge-eval/artifacts/artifact-maintained/views/native",
		views["native"],
	)

	trials := s.getJSON("/v1/knowledge-eval/runs/run-maintained/trials", http.StatusOK)
	s.Len(trials["items"], 2)
	events := s.getJSON("/v1/knowledge-eval/runs/run-maintained/events", http.StatusOK)
	s.Len(events["items"], 1)
	detail := s.getJSON(
		"/v1/knowledge-eval/artifacts/artifact-maintained",
		http.StatusOK,
	)
	s.NotNil(detail["artifact"])

	view := ut.PerformRequest(
		s.server.Engine,
		http.MethodGet,
		"/v1/knowledge-eval/artifacts/artifact-maintained/views/native",
		nil,
	)
	s.Equal(http.StatusOK, view.Code)
	s.Contains(view.Header().Get("Content-Type"), "text/html")
	s.Contains(view.Body.String(), "Maintained Wiki")
	s.Contains(
		view.Body.String(),
		"/v1/knowledge-eval/artifacts/artifact-maintained/views/native?path=wiki/caroline.md",
	)

	page := ut.PerformRequest(
		s.server.Engine,
		http.MethodGet,
		"/v1/knowledge-eval/artifacts/artifact-maintained/views/native?path=wiki/caroline.md",
		nil,
	)
	s.Equal(http.StatusOK, page.Code)
	s.Contains(page.Header().Get("Content-Type"), "text/html")
	s.Contains(page.Body.String(), "<h1>Caroline</h1>")
}

func (s *endpointSuite) TestDatasetDetailSessionsAndView() {
	detail := s.getJSON(
		"/v1/knowledge-eval/datasets/locomo/train/conv-26",
		http.StatusOK,
	)
	datasetDetail := s.asMap(detail["detail"])
	s.Equal("artifact-maintained", datasetDetail["source_artifact_id"])
	s.Equal([]any{"run-maintained"}, s.asSlice(datasetDetail["run_ids"]))

	sessions := s.getJSON(
		"/v1/knowledge-eval/datasets/locomo/train/conv-26/sessions?limit=10",
		http.StatusOK,
	)
	items := s.asSlice(sessions["items"])
	s.Require().Len(items, 1)
	s.Equal("conv-26:session_1", s.asMap(items[0])["id"])

	view := ut.PerformRequest(
		s.server.Engine,
		http.MethodGet,
		"/v1/knowledge-eval/datasets/locomo/train/conv-26/sessions/conv-26%3Asession_1/view",
		nil,
	)
	s.Equal(http.StatusOK, view.Code)
	s.Contains(view.Body.String(), "Immutable Session Source")
}

func (s *endpointSuite) TestValidationAndNotFound() {
	s.getJSON("/v1/knowledge-eval/runs?limit=201", http.StatusBadRequest)
	s.getJSON("/v1/knowledge-eval/runs?cursor=not-a-cursor", http.StatusBadRequest)
	s.getJSON("/v1/knowledge-eval/runs/missing", http.StatusNotFound)
	s.getJSON("/v1/knowledge-eval/artifacts/missing", http.StatusNotFound)
}

func (s *endpointSuite) TestFiltersAndCursorPagination() {
	filtered := s.getJSON(
		"/v1/knowledge-eval/datasets?dataset=missing",
		http.StatusOK,
	)
	s.Empty(filtered["items"])
	filtered = s.getJSON(
		"/v1/knowledge-eval/runs?solution_version_id=missing",
		http.StatusOK,
	)
	s.Empty(filtered["items"])
	filtered = s.getJSON(
		"/v1/knowledge-eval/solutions?solution_version_id=missing",
		http.StatusOK,
	)
	s.Empty(filtered["items"])

	first := s.getJSON("/v1/knowledge-eval/benchmarks?limit=1", http.StatusOK)
	s.Len(first["items"], 1)
	page := s.asMap(first["page"])
	cursor, ok := page["next_cursor"].(string)
	s.True(ok)
	second := s.getJSON(
		"/v1/knowledge-eval/benchmarks?limit=1&cursor="+cursor,
		http.StatusOK,
	)
	s.Len(second["items"], 1)

	matrix := s.getJSON(
		"/v1/knowledge-eval/results/matrix?benchmark_id=wiki-artifact-quality",
		http.StatusOK,
	)
	s.Len(matrix["benchmarks"], 1)
	rows := s.asSlice(matrix["rows"])
	s.Len(s.asMap(rows[0])["cells"], 1)
}

func (s *endpointSuite) TestExperimentTaskLifecycleAPI() {
	request := `{"dataset":"locomo","partition":"train","group_id":"conv-26",` +
		`"mode":"baseline","question_limit":3}`
	preview := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/experiment-tasks/preview",
		request,
	)
	s.Equal(http.StatusOK, preview.Code)
	var previewBody map[string]any
	s.Require().NoError(json.Unmarshal(preview.Body.Bytes(), &previewBody))
	s.Equal(true, s.asMap(previewBody["preview"])["eligible"])

	created := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/experiment-tasks",
		request,
		ut.Header{Key: "Idempotency-Key", Value: "create-1"},
	)
	s.Equal(http.StatusAccepted, created.Code)
	s.Equal("create-1", s.tasks.idempotencyKey)
	var createdBody map[string]any
	s.Require().NoError(json.Unmarshal(created.Body.Bytes(), &createdBody))
	s.Equal("task-1", s.asMap(createdBody["task"])["id"])
	createdTask := s.asMap(createdBody["task"])
	s.NotEmpty(createdTask["started_at"])
	s.NotEmpty(createdTask["completed_at"])
	s.NotEmpty(createdTask["error"])
	s.NotEmpty(createdTask["result_path"])

	list := s.getJSON("/v1/knowledge-eval/experiment-tasks", http.StatusOK)
	s.Len(list["items"], 1)
	filtered := s.getJSON(
		"/v1/knowledge-eval/experiment-tasks?status=missing",
		http.StatusOK,
	)
	s.Empty(filtered["items"])
	detail := s.getJSON(
		"/v1/knowledge-eval/experiment-tasks/task-1",
		http.StatusOK,
	)
	s.Equal("queued", s.asMap(detail["task"])["status"])

	cancelled := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/experiment-tasks/task-1/cancel",
		"",
	)
	s.Equal(http.StatusOK, cancelled.Code)
	var cancelledBody map[string]any
	s.Require().NoError(json.Unmarshal(cancelled.Body.Bytes(), &cancelledBody))
	s.Equal("cancelled", s.asMap(cancelledBody["task"])["status"])

	retried := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/experiment-tasks/task-1/retry",
		"",
		ut.Header{Key: "Idempotency-Key", Value: "retry-1"},
	)
	s.Equal(http.StatusAccepted, retried.Code)
	s.Equal("retry-1", s.tasks.idempotencyKey)
	var retriedBody map[string]any
	s.Require().NoError(json.Unmarshal(retried.Body.Bytes(), &retriedBody))
	s.Equal("task-1", s.asMap(retriedBody["task"])["retry_of_task_id"])

	continued := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/experiment-tasks/task-1/continue",
		`{"additional_rounds":10}`,
		ut.Header{Key: "Idempotency-Key", Value: "continue-1"},
	)
	s.Equal(http.StatusAccepted, continued.Code)
	s.Equal("continue-1", s.tasks.idempotencyKey)
	var continuedBody map[string]any
	s.Require().NoError(json.Unmarshal(continued.Body.Bytes(), &continuedBody))
	s.Equal("task-1", s.asMap(continuedBody["task"])["continued_from_task_id"])

	moreQuestions := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/experiment-tasks/task-1/continue",
		`{"additional_questions":5}`,
		ut.Header{Key: "Idempotency-Key", Value: "questions-1"},
	)
	s.Equal(http.StatusAccepted, moreQuestions.Code)
	s.Equal("questions-1", s.tasks.idempotencyKey)
	s.Equal(5, s.tasks.continueOptions.AdditionalQuestions)

	s.tasks.err = fmt.Errorf("%w: fixture", knowledgeeval.ErrInvalidRecord)
	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{
			method: http.MethodPost,
			path:   "/v1/knowledge-eval/experiment-tasks/preview",
			body:   request,
		},
		{
			method: http.MethodPost,
			path:   "/v1/knowledge-eval/experiment-tasks",
			body:   request,
		},
		{
			method: http.MethodGet,
			path:   "/v1/knowledge-eval/experiment-tasks/task-1",
		},
		{
			method: http.MethodPost,
			path:   "/v1/knowledge-eval/experiment-tasks/task-1/cancel",
		},
		{
			method: http.MethodPost,
			path:   "/v1/knowledge-eval/experiment-tasks/task-1/retry",
		},
		{
			method: http.MethodPost,
			path:   "/v1/knowledge-eval/experiment-tasks/task-1/continue",
		},
	} {
		response := s.performJSON(test.method, test.path, test.body)
		s.Equal(http.StatusBadRequest, response.Code, test.path)
	}
}

func (s *endpointSuite) TestCohortCampaignLifecycleAPI() {
	request := `{"name":"all holdout","selections":[{"dataset":"locomo","partition":"holdout"}],` +
		`"recipe":{"mode":"maintainer","model":"deepseek-v4-flash",` +
		`"reader_model":"deepseek-v4-flash","max_rounds":30},` +
		`"confirm_paid":true,"llm_call_limit":34}`
	preview := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/cohort-campaigns/preview",
		request,
	)
	s.Equal(http.StatusOK, preview.Code)
	var previewBody map[string]any
	s.Require().NoError(json.Unmarshal(preview.Body.Bytes(), &previewBody))
	s.InDelta(1, s.asFloat(s.asMap(previewBody["preview"])["total_groups"]), 0.0001)

	created := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/cohort-campaigns",
		request,
		ut.Header{Key: "Idempotency-Key", Value: "cohort-create-1"},
	)
	s.Equal(http.StatusAccepted, created.Code)
	s.Equal("cohort-create-1", s.cohorts.idempotencyKey)
	var createdBody map[string]any
	s.Require().NoError(json.Unmarshal(created.Body.Bytes(), &createdBody))
	s.Equal("cohort-1", s.asMap(createdBody["campaign"])["id"])

	list := s.getJSON("/v1/knowledge-eval/cohort-campaigns", http.StatusOK)
	s.Len(list["items"], 1)
	detail := s.getJSON(
		"/v1/knowledge-eval/cohort-campaigns/cohort-1",
		http.StatusOK,
	)
	s.Equal("queued", s.asMap(detail["campaign"])["status"])
	cancelled := s.performJSON(
		http.MethodPost,
		"/v1/knowledge-eval/cohort-campaigns/cohort-1/cancel",
		"",
	)
	s.Equal(http.StatusOK, cancelled.Code)
	var cancelledBody map[string]any
	s.Require().NoError(json.Unmarshal(cancelled.Body.Bytes(), &cancelledBody))
	s.Equal("cancelled", s.asMap(cancelledBody["campaign"])["status"])
}

func (s *endpointSuite) TestTaskRoutesReportMissingService() {
	registry, err := dashboard.NewRegistry(s.T().TempDir())
	s.Require().NoError(err)
	httpHandler, err := handler.New(registry)
	s.Require().NoError(err)
	hertz := server.Default()
	hertz.Use(handler.InstanceMiddleware(httpHandler))
	router.GeneratedRegister(hertz)

	response := ut.PerformRequest(
		hertz.Engine,
		http.MethodGet,
		"/v1/knowledge-eval/experiment-tasks",
		nil,
	)
	s.Equal(http.StatusServiceUnavailable, response.Code)
	response = ut.PerformRequest(
		hertz.Engine,
		http.MethodGet,
		"/v1/knowledge-eval/cohort-campaigns",
		nil,
	)
	s.Equal(http.StatusServiceUnavailable, response.Code)
	response = ut.PerformRequest(
		hertz.Engine,
		http.MethodGet,
		"/v1/knowledge-eval/dataset-sources",
		nil,
	)
	s.Equal(http.StatusServiceUnavailable, response.Code)
}

func (s *endpointSuite) TestRoutesRequireConfiguredRuntime() {
	unconfigured := server.Default()
	router.GeneratedRegister(unconfigured)
	paths := []string{
		"/v1/knowledge-eval/solutions",
		"/v1/knowledge-eval/datasets",
		"/v1/knowledge-eval/dataset-sources",
		"/v1/knowledge-eval/dataset-install-tasks",
		"/v1/knowledge-eval/dataset-install-tasks/task-1",
		"/v1/knowledge-eval/datasets/locomo/groups",
		"/v1/knowledge-eval/datasets/locomo/train/conv-26",
		"/v1/knowledge-eval/datasets/locomo/train/conv-26/sessions",
		"/v1/knowledge-eval/datasets/locomo/train/conv-26/sessions/session-1/view",
		"/v1/knowledge-eval/runs",
		"/v1/knowledge-eval/runs/run-1",
		"/v1/knowledge-eval/runs/run-1/trials",
		"/v1/knowledge-eval/runs/run-1/events",
		"/v1/knowledge-eval/benchmarks",
		"/v1/knowledge-eval/results/matrix",
		"/v1/knowledge-eval/artifacts/artifact-1",
		"/v1/knowledge-eval/artifacts/artifact-1/views/native",
		"/v1/knowledge-eval/experiment-tasks",
		"/v1/knowledge-eval/cohort-campaigns",
	}
	for _, path := range paths {
		response := ut.PerformRequest(unconfigured.Engine, http.MethodGet, path, nil)
		s.Equal(http.StatusInternalServerError, response.Code, path)
	}
}

type endpointDatasetInstallService struct {
	task           datasetinstall.Task
	idempotencyKey string
	err            error
}

func (s *endpointDatasetInstallService) Sources() []datasetinstall.Source {
	return []datasetinstall.Source{{
		ID: "locomo", Name: "LoCoMo", Provider: "github",
		Repository: "snap-research/locomo", Revision: "revision-1",
		License: "CC-BY-NC-4.0", DownloadSize: "2.7 MB", DataRoot: "/data",
		InstallStatus: "not_downloaded", Note: "fixture note",
	}}
}

func (s *endpointDatasetInstallService) Create(
	dataset string,
	idempotencyKey string,
) (datasetinstall.Task, error) {
	if s.err != nil {
		return datasetinstall.Task{}, s.err
	}
	now := time.Date(2026, time.July, 31, 8, 0, 0, 0, time.UTC)
	startedAt := now.Add(time.Second)
	completedAt := now.Add(2 * time.Second)
	s.idempotencyKey = idempotencyKey
	s.task = datasetinstall.Task{
		ID: "dataset-task-1", Dataset: dataset, Status: datasetinstall.StatusQueued,
		DataRoot: "/data", CreatedAt: now, UpdatedAt: now,
		StartedAt: &startedAt, CompletedAt: &completedAt, Error: "fixture warning",
		Events: []datasetinstall.Event{{
			Status: datasetinstall.StatusQueued, Message: "queued", CreatedAt: now,
		}},
	}
	return s.task, nil
}

func (s *endpointDatasetInstallService) List() []datasetinstall.Task {
	if s.task.ID == "" {
		return nil
	}
	return []datasetinstall.Task{s.task}
}

func (s *endpointDatasetInstallService) Get(taskID string) (datasetinstall.Task, error) {
	if s.err != nil {
		return datasetinstall.Task{}, s.err
	}
	if taskID != s.task.ID {
		return datasetinstall.Task{}, fmt.Errorf("%w: task", knowledgeeval.ErrNotFound)
	}
	return s.task, nil
}

func (s *endpointDatasetInstallService) Cancel(taskID string) (datasetinstall.Task, error) {
	task, err := s.Get(taskID)
	if err != nil {
		return datasetinstall.Task{}, err
	}
	task.Status = datasetinstall.StatusCancelled
	s.task = task
	return task, nil
}

func (s *endpointSuite) getJSON(path string, status int) map[string]any {
	response := ut.PerformRequest(s.server.Engine, http.MethodGet, path, nil)
	s.Equal(status, response.Code)
	var body map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	return body
}

func (s *endpointSuite) performJSON(
	method string,
	path string,
	body string,
	headers ...ut.Header,
) *ut.ResponseRecorder {
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	}
	headers = append(
		[]ut.Header{{Key: "Content-Type", Value: "application/json"}},
		headers...,
	)
	return ut.PerformRequest(s.server.Engine, method, path, requestBody, headers...)
}

func (s *endpointSuite) asSlice(value any) []any {
	result, ok := value.([]any)
	s.Require().True(ok)
	return result
}

func (s *endpointSuite) asMap(value any) map[string]any {
	result, ok := value.(map[string]any)
	s.Require().True(ok)
	return result
}

func (s *endpointSuite) asFloat(value any) float64 {
	result, ok := value.(float64)
	s.Require().True(ok)
	return result
}

type endpointTaskService struct {
	task            experimenttask.Task
	idempotencyKey  string
	continueOptions experimenttask.ContinueOptions
	err             error
}

type endpointCohortService struct {
	campaign       cohorttask.Campaign
	idempotencyKey string
}

func (s *endpointCohortService) Preview(
	_ context.Context,
	_ cohorttask.Request,
) (cohorttask.Preview, error) {
	return cohorttask.Preview{
		Eligible: true, Paid: true, TotalGroups: 1, EligibleGroups: 1,
		TotalQuestions: 2, PlannedQuestions: 2, PlannedTasks: 1,
		MaxLLMCalls: 34,
	}, nil
}

func (s *endpointCohortService) Create(
	ctx context.Context,
	request cohorttask.Request,
	idempotencyKey string,
) (cohorttask.Campaign, error) {
	preview, err := s.Preview(ctx, request)
	if err != nil {
		return cohorttask.Campaign{}, err
	}
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	s.idempotencyKey = idempotencyKey
	s.campaign = cohorttask.Campaign{
		ID: "cohort-1", Request: request, Preview: preview,
		Status: cohorttask.StatusQueued, CreatedAt: now, UpdatedAt: now,
		Summary: cohorttask.Summary{TotalGroups: 1, EligibleGroups: 1, TotalQuestions: 2},
		Executions: []cohorttask.Execution{{
			Dataset: "locomo", Partition: "holdout", GroupID: "conv-1",
			Questions: 2, Status: cohorttask.ExecutionPlanned,
		}},
	}
	return s.campaign, nil
}

func (s *endpointCohortService) List() []cohorttask.Campaign {
	if s.campaign.ID == "" {
		return nil
	}
	return []cohorttask.Campaign{s.campaign}
}

func (s *endpointCohortService) Get(id string) (cohorttask.Campaign, error) {
	if s.campaign.ID != id {
		return cohorttask.Campaign{}, fmt.Errorf("%w: cohort", knowledgeeval.ErrNotFound)
	}
	return s.campaign, nil
}

func (s *endpointCohortService) Cancel(id string) (cohorttask.Campaign, error) {
	campaign, err := s.Get(id)
	if err != nil {
		return cohorttask.Campaign{}, err
	}
	campaign.Status = cohorttask.StatusCancelled
	s.campaign = campaign
	return campaign, nil
}

func (s *endpointTaskService) Preview(
	_ context.Context,
	request experimenttask.Request,
) (experimenttask.Preview, error) {
	if s.err != nil {
		return experimenttask.Preview{}, s.err
	}
	return experimenttask.Preview{
		Eligible: true, Dataset: request.Dataset, Partition: request.Partition,
		GroupID: request.GroupID, SourceKind: "long-running-conversation",
		AvailableQuestions: 152, SelectedQuestions: request.QuestionLimit,
		PlannedRuns: 1, Benchmarks: []string{
			"wiki-artifact-quality",
			"knowledge-search-get-qa",
		},
		IncludesSourceOnly: true, IneligibleReason: "fixture reason",
	}, nil
}

func (s *endpointTaskService) Create(
	ctx context.Context,
	request experimenttask.Request,
	idempotencyKey string,
) (experimenttask.Task, error) {
	if s.err != nil {
		return experimenttask.Task{}, s.err
	}
	preview, err := s.Preview(ctx, request)
	if err != nil {
		return experimenttask.Task{}, err
	}
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	startedAt := now.Add(time.Second)
	completedAt := now.Add(2 * time.Second)
	s.idempotencyKey = idempotencyKey
	s.task = experimenttask.Task{
		ID: "task-1", Request: request, Preview: preview,
		Status: experimenttask.StatusQueued, CreatedAt: now, UpdatedAt: now,
		StartedAt: &startedAt, CompletedAt: &completedAt,
		Error: "recorded fixture", ResultPath: "tasks/task-1",
		RunIDs: []string{"run-1"}, ArtifactIDs: []string{"artifact-1"},
		Events: []experimenttask.Event{{
			Status: experimenttask.StatusQueued, Message: "Task queued.", CreatedAt: now,
		}},
	}
	return s.task, nil
}

func (s *endpointTaskService) List() []experimenttask.Task {
	if s.task.ID == "" {
		return nil
	}
	return []experimenttask.Task{s.task}
}

func (s *endpointTaskService) Get(taskID string) (experimenttask.Task, error) {
	if s.err != nil {
		return experimenttask.Task{}, s.err
	}
	if taskID != s.task.ID {
		return experimenttask.Task{}, os.ErrNotExist
	}
	return s.task, nil
}

func (s *endpointTaskService) Cancel(taskID string) (experimenttask.Task, error) {
	task, err := s.Get(taskID)
	if err != nil {
		return experimenttask.Task{}, err
	}
	task.Status = experimenttask.StatusCancelled
	s.task = task
	return task, nil
}

func (s *endpointTaskService) Retry(
	_ context.Context,
	taskID string,
	idempotencyKey string,
) (experimenttask.Task, error) {
	task, err := s.Get(taskID)
	if err != nil {
		return experimenttask.Task{}, err
	}
	s.idempotencyKey = idempotencyKey
	task.RetryOfTaskID = taskID
	task.Status = experimenttask.StatusQueued
	s.task = task
	return task, nil
}

func (s *endpointTaskService) Continue(
	_ context.Context,
	taskID string,
	options experimenttask.ContinueOptions,
	idempotencyKey string,
) (experimenttask.Task, error) {
	task, err := s.Get(taskID)
	if err != nil {
		return experimenttask.Task{}, err
	}
	s.idempotencyKey = idempotencyKey
	s.continueOptions = options
	task.ContinuedFromTaskID = taskID
	task.Status = experimenttask.StatusQueued
	s.task = task
	return task, nil
}

const testBundle = `{
  "schema_version": "pax.knowledge-eval.session-dataset-run.v1",
  "generated_at": "2026-07-30T13:00:41Z",
  "dataset": "locomo",
  "partition": "train",
  "case_id": "conv-26",
  "mode": "builder-comparison",
  "build_status": "completed",
  "blocker": "",
  "ingest": {"sessions": 19, "turns": 419, "sources": 19},
  "questions": 5,
  "artifact": {},
  "arms": [{
    "id": "maintained",
    "role": "candidate",
    "build_status": "completed",
    "run_id": "run-maintained",
    "artifact": {
      "product": "locomo conv-26 maintained",
      "artifact": {
        "artifact_id": "artifact-maintained",
        "kind": "llmwiki-workspace",
        "world_id": "locomo",
        "group_id": "locomo-conv-26",
        "checkpoint_id": "train-conv-26",
        "payload": {
          "kind": "llmwiki-workspace",
          "schema_version": "pax.llmwiki.workspace.v1",
          "uri": "artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        },
        "provenance": {
          "builder_id": "llmwiki-maintainer",
          "builder_version": "v1",
          "code_revision": "revision-1",
          "config_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
          "seed": 0,
          "metadata": {"model": "deepseek-v4-pro"}
        },
        "created_at": "2026-07-30T13:00:20Z"
      },
      "capabilities": [],
      "views": {"native": "views/maintained-native.html"}
    }
  }],
  "query": {
    "schema_version": "pax.knowledge-eval.query.v1",
    "generated_at": "2026-07-30T13:00:40Z",
    "runs": [{
      "run": {
        "id": "run-maintained",
        "world_id": "locomo",
        "group_id": "locomo-conv-26",
        "checkpoint_id": "train-conv-26",
        "artifact_id": "artifact-maintained",
        "status": "completed",
        "created_at": "2026-07-30T13:00:20Z",
        "completed_at": "2026-07-30T13:00:40Z"
      },
      "trials": [{
        "id": "trial-qa",
        "run_id": "run-maintained",
        "case_id": "locomo-conv-26",
        "benchmark_id": "knowledge-search-get-qa",
        "benchmark_fingerprint": "qa:v1",
        "status": "completed",
        "result": {
          "status": "failed",
          "metrics": [
            {"name": "answer_accuracy", "value": 0.2, "unit": "ratio"},
            {"name": "retrieval_hit_rate", "value": 0.4, "unit": "ratio"}
          ],
          "case_results": []
        }
      }, {
        "id": "trial-quality",
        "run_id": "run-maintained",
        "case_id": "locomo-conv-26",
        "benchmark_id": "wiki-artifact-quality",
        "benchmark_fingerprint": "quality:v1",
        "status": "completed",
        "result": {
          "status": "passed",
          "metrics": [
            {"name": "artifact_quality_score", "value": 0.96, "unit": "ratio"},
            {"name": "document_count", "value": 6, "unit": "count"}
          ],
          "case_results": []
        }
      }],
      "attempts": [],
      "events": [{
        "id": "event-1",
        "run_id": "run-maintained",
        "stage": "completed",
        "message": "run finished",
        "created_at": "2026-07-30T13:00:40Z"
      }]
    }]
  },
  "failures": []
}`
