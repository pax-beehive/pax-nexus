namespace go knowledgeeval.api

struct KnowledgeEvalListRequest {
  1: optional string dataset (api.query="dataset")
  2: optional string partition (api.query="partition")
  3: optional string status (api.query="status")
  4: optional string benchmark_id (api.query="benchmark_id")
  5: optional string solution_version_id (api.query="solution_version_id")
  6: optional i32 limit (api.query="limit")
  7: optional string cursor (api.query="cursor")
}

struct KnowledgeEvalRunByIDRequest {
  1: required string run_id (api.path="run_id")
}

struct KnowledgeEvalDatasetByIDRequest {
  1: required string dataset (api.path="dataset")
  2: required string partition (api.path="partition")
  3: required string case_id (api.path="case_id")
}

struct KnowledgeEvalDatasetGroupsRequest {
  1: required string dataset (api.path="dataset")
  2: optional string partition (api.query="partition")
  3: optional string status (api.query="status")
  4: optional i32 limit (api.query="limit")
  5: optional string cursor (api.query="cursor")
}

struct KnowledgeEvalDatasetSessionsRequest {
  1: required string dataset (api.path="dataset")
  2: required string partition (api.path="partition")
  3: required string case_id (api.path="case_id")
  4: optional i32 limit (api.query="limit")
  5: optional string cursor (api.query="cursor")
}

struct KnowledgeEvalDatasetSessionViewRequest {
  1: required string dataset (api.path="dataset")
  2: required string partition (api.path="partition")
  3: required string case_id (api.path="case_id")
  4: required string session_id (api.path="session_id")
}

struct KnowledgeEvalArtifactByIDRequest {
  1: required string artifact_id (api.path="artifact_id")
}

struct KnowledgeEvalArtifactViewRequest {
  1: required string artifact_id (api.path="artifact_id")
  2: required string view (api.path="view")
  3: optional string path (api.query="path")
}

struct KnowledgeEvalMatrixRequest {
  1: optional string dataset (api.query="dataset")
  2: optional string partition (api.query="partition")
  3: optional string case_id (api.query="case_id")
  4: optional string benchmark_id (api.query="benchmark_id")
}

struct KnowledgeEvalExperimentTaskRequest {
  1: required string dataset (api.body="dataset")
  2: required string partition (api.body="partition")
  3: required string group_id (api.body="group_id")
  4: optional string mode (api.body="mode")
  5: optional string model (api.body="model")
  6: optional string reader_model (api.body="reader_model")
  7: optional i32 question_limit (api.body="question_limit")
  8: optional i32 max_rounds (api.body="max_rounds")
  9: optional bool confirm_paid (api.body="confirm_paid")
  10: optional i32 question_offset (api.body="question_offset")
  11: optional string reuse_artifact_from_task_id (api.body="reuse_artifact_from_task_id")
}

struct KnowledgeEvalExperimentTaskByIDRequest {
  1: required string task_id (api.path="task_id")
}

struct KnowledgeEvalDatasetInstallRequest {
  1: required string dataset (api.body="dataset")
}

struct KnowledgeEvalDatasetInstallTaskByIDRequest {
  1: required string task_id (api.path="task_id")
}

struct KnowledgeEvalExperimentTaskContinueRequest {
  1: required string task_id (api.path="task_id")
  2: optional i32 additional_rounds (api.body="additional_rounds")
  3: optional i32 additional_questions (api.body="additional_questions")
}

struct KnowledgeEvalCohortDatasetSelection {
  1: required string dataset
  2: required string partition
}

struct KnowledgeEvalCohortRecipe {
  1: required string mode
  2: optional string model
  3: optional string reader_model
  4: optional i32 max_rounds
}

struct KnowledgeEvalCohortCampaignRequest {
  1: required string name (api.body="name")
  2: required list<KnowledgeEvalCohortDatasetSelection> selections (api.body="selections")
  3: required KnowledgeEvalCohortRecipe recipe (api.body="recipe")
  4: optional bool confirm_paid (api.body="confirm_paid")
  5: optional i64 llm_call_limit (api.body="llm_call_limit")
}

struct KnowledgeEvalCohortCampaignByIDRequest {
  1: required string campaign_id (api.path="campaign_id")
}

struct KnowledgeEvalExperimentModel {
  1: required string id
  2: required string name
  3: required string provider
}

struct KnowledgeEvalDatasetSource {
  1: required string id
  2: required string name
  3: required string provider
  4: required string repository
  5: required string revision
  6: required string license
  7: required string download_size
  8: required string data_root
  9: required bool downloaded
  10: required bool prepared
  11: required string install_status
  12: optional string note
}

struct KnowledgeEvalDatasetInstallEvent {
  1: required string status
  2: required string message
  3: required string created_at
}

struct KnowledgeEvalDatasetInstallTask {
  1: required string id
  2: required string dataset
  3: required string status
  4: required string data_root
  5: required string created_at
  6: required string updated_at
  7: optional string started_at
  8: optional string completed_at
  9: required bool cancellation_requested
  10: optional string error
  11: required list<KnowledgeEvalDatasetInstallEvent> events
}

struct KnowledgeEvalPage {
  1: required i32 limit
  2: optional string next_cursor
}

struct KnowledgeEvalSolutionVersion {
  1: required string id
  2: required string builder_id
  3: required string builder_version
  4: required string code_revision
  5: optional string model
  6: optional string config_digest
}

struct KnowledgeEvalDatasetPartition {
  1: required string name
  2: required i32 group_count
  3: required i32 run_group_count
}

struct KnowledgeEvalDatasetFamily {
  1: required string id
  2: required string name
  3: optional string revision
  4: optional string license
  5: required string group_kind
  6: required i32 group_count
  7: required i32 run_group_count
  8: required i32 run_count
  9: required i32 artifact_count
  10: required list<KnowledgeEvalDatasetPartition> partitions
}

struct KnowledgeEvalDataset {
  1: required string id
  2: required string dataset
  3: required string partition
  4: required string case_id
  5: required string status
  6: required i32 sessions
  7: required i32 turns
  8: required i32 sources
  9: required i32 questions
  10: required i32 experiment_count
  11: optional string updated_at
  12: required string group_kind
  13: required string source_kind
  14: required i32 trajectories
  15: required i32 evaluation_cases
  16: required i32 run_count
  17: required i32 artifact_count
  18: required list<string> case_ids
}

struct KnowledgeEvalDatasetDetail {
  1: required KnowledgeEvalDataset dataset
  2: required string source_artifact_id
  3: required list<string> run_ids
}

struct KnowledgeEvalDatasetSession {
  1: required string id
  2: required string source_path
  3: required i32 turns
}

struct KnowledgeEvalMetric {
  1: required string name
  2: required double value
  3: required string unit
}

struct KnowledgeEvalTrial {
  1: required string id
  2: required string run_id
  3: required string benchmark_id
  4: required string benchmark_fingerprint
  5: required string status
  6: optional string ineligible_reason
  7: optional string result_status
  8: optional list<KnowledgeEvalMetric> metrics
}

struct KnowledgeEvalRun {
  1: required string id
  2: required string dataset_id
  3: required string dataset
  4: required string partition
  5: required string case_id
  6: required string solution_version_id
  7: required string artifact_id
  8: required string status
  9: required string created_at
  10: optional string completed_at
  11: required list<string> benchmark_ids
  12: optional map<string, string> metadata
}

struct KnowledgeEvalEvent {
  1: required string id
  2: required string run_id
  3: optional string trial_id
  4: optional string attempt_id
  5: required string stage
  6: required string message
  7: required string created_at
}

struct KnowledgeEvalArtifact {
  1: required string artifact_id
  2: required string product
  3: required string kind
  4: required string role
  5: required string dataset_id
  6: required string solution_version_id
  7: required string sha256
  8: required string created_at
  9: required map<string, string> views
}

struct KnowledgeEvalBenchmark {
  1: required string id
  2: required string name
  3: required string description
  4: required string primary_metric
  5: required bool executed
}

struct KnowledgeEvalMatrixCell {
  1: required string benchmark_id
  2: required bool executed
  3: optional double score
  4: optional list<KnowledgeEvalMetric> metrics
}

struct KnowledgeEvalMatrixRow {
  1: required string run_id
  2: required string solution_version_id
  3: required string artifact_id
  4: required list<KnowledgeEvalMatrixCell> cells
}

struct KnowledgeEvalExperimentTaskPreview {
  1: required bool eligible
  2: optional string ineligible_reason
  3: required bool paid
  4: required bool llm_configured
  5: required string dataset
  6: required string partition
  7: required string group_id
  8: required string source_kind
  9: required i32 available_questions
  10: required i32 selected_questions
  11: required i32 planned_runs
  12: required i32 max_llm_calls
  13: required list<string> benchmarks
  14: required bool includes_source_only
  15: required bool includes_maintainer
  16: required i32 cumulative_questions
}

struct KnowledgeEvalExperimentTaskEvent {
  1: required string status
  2: required string message
  3: required string created_at
}

struct KnowledgeEvalExperimentTask {
  1: required string id
  2: required KnowledgeEvalExperimentTaskRequest request
  3: required KnowledgeEvalExperimentTaskPreview preview
  4: required string status
  5: required string created_at
  6: required string updated_at
  7: optional string started_at
  8: optional string completed_at
  9: required bool cancellation_requested
  10: optional string error
  11: required list<string> run_ids
  12: required list<string> artifact_ids
  13: optional string result_path
  14: required list<KnowledgeEvalExperimentTaskEvent> events
  15: optional string retry_of_task_id
  16: optional string continued_from_task_id
}

struct KnowledgeEvalCohortIssue {
  1: required string dataset
  2: required string partition
  3: optional string group_id
  4: required string reason
}

struct KnowledgeEvalCohortDatasetPreview {
  1: required string dataset
  2: required string partition
  3: required i32 total_groups
  4: required i32 eligible_groups
  5: required i32 ineligible_groups
  6: required i32 total_questions
  7: required i32 planned_questions
  8: required i64 max_llm_calls
}

struct KnowledgeEvalCohortCampaignPreview {
  1: required bool eligible
  2: required bool paid
  3: required i32 total_groups
  4: required i32 eligible_groups
  5: required i32 ineligible_groups
  6: required i32 total_questions
  7: required i32 planned_questions
  8: required i32 planned_tasks
  9: required i64 max_llm_calls
  10: required list<KnowledgeEvalCohortDatasetPreview> datasets
  11: required list<KnowledgeEvalCohortIssue> issues
}

struct KnowledgeEvalCohortExecution {
  1: required string dataset
  2: required string partition
  3: required string group_id
  4: required i32 questions
  5: required string status
  6: optional string ineligible_reason
  7: optional string task_id
  8: optional string error
  9: optional string run_id
  10: required i32 evaluated_questions
  11: required i32 correct_questions
  12: required double accuracy
}

struct KnowledgeEvalCohortSummary {
  1: required i32 total_groups
  2: required i32 eligible_groups
  3: required i32 evaluated_groups
  4: required i32 failed_groups
  5: required i32 total_questions
  6: required i32 evaluated_questions
  7: required i32 correct_questions
  8: required double micro_accuracy
  9: required double macro_accuracy
  10: required double group_coverage
  11: required double question_coverage
}

struct KnowledgeEvalCohortCampaign {
  1: required string id
  2: required KnowledgeEvalCohortCampaignRequest request
  3: required KnowledgeEvalCohortCampaignPreview preview
  4: required string status
  5: required KnowledgeEvalCohortSummary summary
  6: required list<KnowledgeEvalCohortExecution> executions
  7: required string created_at
  8: required string updated_at
  9: optional string completed_at
  10: required bool cancellation_requested
}

struct KnowledgeEvalSolutionsResponse {
  1: required list<KnowledgeEvalSolutionVersion> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalDatasetsResponse {
  1: required list<KnowledgeEvalDatasetFamily> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalDatasetGroupsResponse {
  1: required list<KnowledgeEvalDataset> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalDatasetDetailResponse {
  1: required KnowledgeEvalDatasetDetail detail
}

struct KnowledgeEvalDatasetSessionsResponse {
  1: required list<KnowledgeEvalDatasetSession> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalRunsResponse {
  1: required list<KnowledgeEvalRun> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalRunResponse {
  1: required KnowledgeEvalRun run
  2: required list<KnowledgeEvalTrial> trials
  3: required list<KnowledgeEvalEvent> events
  4: optional KnowledgeEvalArtifact artifact
}

struct KnowledgeEvalTrialsResponse {
  1: required list<KnowledgeEvalTrial> items
}

struct KnowledgeEvalEventsResponse {
  1: required list<KnowledgeEvalEvent> items
}

struct KnowledgeEvalBenchmarksResponse {
  1: required list<KnowledgeEvalBenchmark> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalMatrixResponse {
  1: required list<KnowledgeEvalBenchmark> benchmarks
  2: required list<KnowledgeEvalMatrixRow> rows
}

struct KnowledgeEvalArtifactResponse {
  1: required KnowledgeEvalArtifact artifact
}

struct KnowledgeEvalArtifactViewResponse {
  1: required string content_type
  2: required binary content
}

struct KnowledgeEvalExperimentTaskPreviewResponse {
  1: required KnowledgeEvalExperimentTaskPreview preview
}

struct KnowledgeEvalExperimentTasksResponse {
  1: required list<KnowledgeEvalExperimentTask> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalExperimentTaskResponse {
  1: required KnowledgeEvalExperimentTask task
}

struct KnowledgeEvalCohortCampaignPreviewResponse {
  1: required KnowledgeEvalCohortCampaignPreview preview
}

struct KnowledgeEvalCohortCampaignsResponse {
  1: required list<KnowledgeEvalCohortCampaign> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalCohortCampaignResponse {
  1: required KnowledgeEvalCohortCampaign campaign
}

struct KnowledgeEvalExperimentModelsResponse {
  1: required list<KnowledgeEvalExperimentModel> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalDatasetSourcesResponse {
  1: required list<KnowledgeEvalDatasetSource> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalDatasetInstallTasksResponse {
  1: required list<KnowledgeEvalDatasetInstallTask> items
  2: required KnowledgeEvalPage page
}

struct KnowledgeEvalDatasetInstallTaskResponse {
  1: required KnowledgeEvalDatasetInstallTask task
}

service KnowledgeEvalService {
  KnowledgeEvalSolutionsResponse ListSolutions(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/solutions")
  KnowledgeEvalDatasetsResponse ListDatasets(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/datasets")
  KnowledgeEvalDatasetSourcesResponse ListDatasetSources(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/dataset-sources")
  KnowledgeEvalDatasetInstallTaskResponse CreateDatasetInstallTask(1: KnowledgeEvalDatasetInstallRequest request) (api.post="/v1/knowledge-eval/dataset-install-tasks")
  KnowledgeEvalDatasetInstallTasksResponse ListDatasetInstallTasks(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/dataset-install-tasks")
  KnowledgeEvalDatasetInstallTaskResponse GetDatasetInstallTask(1: KnowledgeEvalDatasetInstallTaskByIDRequest request) (api.get="/v1/knowledge-eval/dataset-install-tasks/:task_id")
  KnowledgeEvalDatasetInstallTaskResponse CancelDatasetInstallTask(1: KnowledgeEvalDatasetInstallTaskByIDRequest request) (api.post="/v1/knowledge-eval/dataset-install-tasks/:task_id/cancel")
  KnowledgeEvalDatasetGroupsResponse ListDatasetGroups(1: KnowledgeEvalDatasetGroupsRequest request) (api.get="/v1/knowledge-eval/datasets/:dataset/groups")
  KnowledgeEvalDatasetDetailResponse GetDataset(1: KnowledgeEvalDatasetByIDRequest request) (api.get="/v1/knowledge-eval/datasets/:dataset/:partition/:case_id")
  KnowledgeEvalDatasetSessionsResponse ListDatasetSessions(1: KnowledgeEvalDatasetSessionsRequest request) (api.get="/v1/knowledge-eval/datasets/:dataset/:partition/:case_id/sessions")
  KnowledgeEvalArtifactViewResponse GetDatasetSessionView(1: KnowledgeEvalDatasetSessionViewRequest request) (api.get="/v1/knowledge-eval/datasets/:dataset/:partition/:case_id/sessions/:session_id/view")
  KnowledgeEvalRunsResponse ListRuns(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/runs")
  KnowledgeEvalRunResponse GetRun(1: KnowledgeEvalRunByIDRequest request) (api.get="/v1/knowledge-eval/runs/:run_id")
  KnowledgeEvalTrialsResponse ListRunTrials(1: KnowledgeEvalRunByIDRequest request) (api.get="/v1/knowledge-eval/runs/:run_id/trials")
  KnowledgeEvalEventsResponse ListRunEvents(1: KnowledgeEvalRunByIDRequest request) (api.get="/v1/knowledge-eval/runs/:run_id/events")
  KnowledgeEvalBenchmarksResponse ListBenchmarks(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/benchmarks")
  KnowledgeEvalMatrixResponse GetResultMatrix(1: KnowledgeEvalMatrixRequest request) (api.get="/v1/knowledge-eval/results/matrix")
  KnowledgeEvalArtifactResponse GetArtifact(1: KnowledgeEvalArtifactByIDRequest request) (api.get="/v1/knowledge-eval/artifacts/:artifact_id")
  KnowledgeEvalArtifactViewResponse GetArtifactView(1: KnowledgeEvalArtifactViewRequest request) (api.get="/v1/knowledge-eval/artifacts/:artifact_id/views/:view")
  KnowledgeEvalExperimentModelsResponse ListExperimentModels(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/experiment-models")
  KnowledgeEvalExperimentTaskPreviewResponse PreviewExperimentTask(1: KnowledgeEvalExperimentTaskRequest request) (api.post="/v1/knowledge-eval/experiment-tasks/preview")
  KnowledgeEvalExperimentTaskResponse CreateExperimentTask(1: KnowledgeEvalExperimentTaskRequest request) (api.post="/v1/knowledge-eval/experiment-tasks")
  KnowledgeEvalExperimentTasksResponse ListExperimentTasks(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/experiment-tasks")
  KnowledgeEvalExperimentTaskResponse GetExperimentTask(1: KnowledgeEvalExperimentTaskByIDRequest request) (api.get="/v1/knowledge-eval/experiment-tasks/:task_id")
  KnowledgeEvalExperimentTaskResponse CancelExperimentTask(1: KnowledgeEvalExperimentTaskByIDRequest request) (api.post="/v1/knowledge-eval/experiment-tasks/:task_id/cancel")
  KnowledgeEvalExperimentTaskResponse RetryExperimentTask(1: KnowledgeEvalExperimentTaskByIDRequest request) (api.post="/v1/knowledge-eval/experiment-tasks/:task_id/retry")
  KnowledgeEvalExperimentTaskResponse ContinueExperimentTask(1: KnowledgeEvalExperimentTaskContinueRequest request) (api.post="/v1/knowledge-eval/experiment-tasks/:task_id/continue")
  KnowledgeEvalCohortCampaignPreviewResponse PreviewCohortCampaign(1: KnowledgeEvalCohortCampaignRequest request) (api.post="/v1/knowledge-eval/cohort-campaigns/preview")
  KnowledgeEvalCohortCampaignResponse CreateCohortCampaign(1: KnowledgeEvalCohortCampaignRequest request) (api.post="/v1/knowledge-eval/cohort-campaigns")
  KnowledgeEvalCohortCampaignsResponse ListCohortCampaigns(1: KnowledgeEvalListRequest request) (api.get="/v1/knowledge-eval/cohort-campaigns")
  KnowledgeEvalCohortCampaignResponse GetCohortCampaign(1: KnowledgeEvalCohortCampaignByIDRequest request) (api.get="/v1/knowledge-eval/cohort-campaigns/:campaign_id")
  KnowledgeEvalCohortCampaignResponse CancelCohortCampaign(1: KnowledgeEvalCohortCampaignByIDRequest request) (api.post="/v1/knowledge-eval/cohort-campaigns/:campaign_id/cancel")
}
