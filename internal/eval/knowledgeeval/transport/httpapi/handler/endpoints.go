package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/dashboard"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/datasetinstall"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/experimenttask"
	api "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/transport/httpapi/model/knowledgeeval/api"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

func (h *Handler) ListSolutions(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	catalog, ok := h.loadCatalog(ctx, c)
	if !ok {
		return
	}
	items := make([]dashboard.SolutionVersion, 0, len(catalog.Solutions))
	for _, solution := range catalog.Solutions {
		if request.GetSolutionVersionID() != "" && solution.ID != request.GetSolutionVersionID() {
			continue
		}
		items = append(items, solution)
	}
	pageItems, page, err := paginate(items, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalSolutionsResponse{
		Items: make([]*api.KnowledgeEvalSolutionVersion, 0, len(pageItems)),
		Page:  page,
	}
	for _, solution := range pageItems {
		response.Items = append(response.Items, toAPISolution(solution))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) ListDatasets(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	catalog, ok := h.loadCatalog(ctx, c)
	if !ok {
		return
	}
	items := make([]dashboard.DatasetFamily, 0, len(catalog.Families))
	for _, family := range catalog.Families {
		if request.GetDataset() != "" &&
			!strings.EqualFold(request.GetDataset(), family.ID) &&
			!strings.EqualFold(request.GetDataset(), family.Name) {
			continue
		}
		items = append(items, family)
	}
	pageItems, page, err := paginate(items, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalDatasetsResponse{
		Items: make([]*api.KnowledgeEvalDatasetFamily, 0, len(pageItems)),
		Page:  page,
	}
	for _, family := range pageItems {
		response.Items = append(response.Items, toAPIDatasetFamily(family))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) ListDatasetGroups(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalDatasetGroupsRequest,
) {
	catalog, ok := h.loadCatalog(ctx, c)
	if !ok {
		return
	}
	items := make([]dashboard.Dataset, 0, len(catalog.Datasets))
	for _, group := range catalog.Datasets {
		if !strings.EqualFold(group.Name, request.GetDataset()) {
			continue
		}
		if request.GetPartition() != "" && group.Partition != request.GetPartition() {
			continue
		}
		if request.GetStatus() != "" && group.Status != request.GetStatus() {
			continue
		}
		items = append(items, group)
	}
	pageItems, page, err := paginate(items, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalDatasetGroupsResponse{
		Items: make([]*api.KnowledgeEvalDataset, 0, len(pageItems)),
		Page:  page,
	}
	for _, group := range pageItems {
		response.Items = append(response.Items, toAPIDataset(group))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetDataset(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalDatasetByIDRequest,
) {
	dataset, runIDs, err := h.registry.GetDataset(
		ctx,
		request.Dataset,
		request.Partition,
		request.CaseID,
	)
	if err != nil {
		writeError(c, err)
		return
	}
	sourceArtifactID := ""
	if dataset.SourceArtifact != nil {
		sourceArtifactID = dataset.SourceArtifact.Record.ArtifactID
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalDatasetDetailResponse{
		Detail: &api.KnowledgeEvalDatasetDetail{
			Dataset:          toAPIDataset(dataset),
			SourceArtifactID: sourceArtifactID,
			RunIds:           runIDs,
		},
	})
}

func (h *Handler) ListDatasetSessions(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalDatasetSessionsRequest,
) {
	sessions, err := h.registry.ListDatasetSessions(
		ctx,
		request.Dataset,
		request.Partition,
		request.CaseID,
	)
	if err != nil {
		writeError(c, err)
		return
	}
	pageItems, page, err := paginate(sessions, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalDatasetSessionsResponse{
		Items: make([]*api.KnowledgeEvalDatasetSession, 0, len(pageItems)),
		Page:  page,
	}
	for _, session := range pageItems {
		response.Items = append(response.Items, &api.KnowledgeEvalDatasetSession{
			ID: session.ID, SourcePath: session.SourcePath, Turns: int32(session.Turns),
		})
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetDatasetSessionView(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalDatasetSessionViewRequest,
) {
	content, contentType, err := h.registry.OpenDatasetSession(
		ctx,
		request.Dataset,
		request.Partition,
		request.CaseID,
		request.SessionID,
	)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Data(consts.StatusOK, contentType, content)
}

func (h *Handler) ListRuns(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	catalog, ok := h.loadCatalog(ctx, c)
	if !ok {
		return
	}
	items := make([]dashboard.Run, 0, len(catalog.Runs))
	for _, run := range catalog.Runs {
		if !matchesDataset(request, run.Dataset, run.Partition, string(run.Detail.Run.Status)) {
			continue
		}
		if request.GetSolutionVersionID() != "" &&
			run.SolutionVersion.ID != request.GetSolutionVersionID() {
			continue
		}
		if request.GetBenchmarkID() != "" &&
			!slices.Contains(run.BenchmarkIDs, request.GetBenchmarkID()) {
			continue
		}
		items = append(items, run)
	}
	pageItems, page, err := paginate(items, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalRunsResponse{
		Items: make([]*api.KnowledgeEvalRun, 0, len(pageItems)),
		Page:  page,
	}
	for _, run := range pageItems {
		response.Items = append(response.Items, toAPIRun(run))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetRun(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalRunByIDRequest,
) {
	run, ok := h.findRun(ctx, c, request.RunID)
	if !ok {
		return
	}
	response := &api.KnowledgeEvalRunResponse{
		Run:    toAPIRun(run),
		Trials: toAPITrials(run.Detail.Trials),
		Events: toAPIEvents(run.Detail.Events),
	}
	if run.Artifact != nil {
		response.Artifact = toAPIArtifact(*run.Artifact)
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) ListRunTrials(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalRunByIDRequest,
) {
	run, ok := h.findRun(ctx, c, request.RunID)
	if !ok {
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalTrialsResponse{
		Items: toAPITrials(run.Detail.Trials),
	})
}

func (h *Handler) ListRunEvents(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalRunByIDRequest,
) {
	run, ok := h.findRun(ctx, c, request.RunID)
	if !ok {
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalEventsResponse{
		Items: toAPIEvents(run.Detail.Events),
	})
}

func (h *Handler) ListBenchmarks(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	catalog, ok := h.loadCatalog(ctx, c)
	if !ok {
		return
	}
	items := make([]dashboard.Benchmark, 0, len(catalog.Benchmarks))
	for _, benchmark := range catalog.Benchmarks {
		if request.GetBenchmarkID() != "" && benchmark.ID != request.GetBenchmarkID() {
			continue
		}
		items = append(items, benchmark)
	}
	pageItems, page, err := paginate(items, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalBenchmarksResponse{
		Items: make([]*api.KnowledgeEvalBenchmark, 0, len(pageItems)),
		Page:  page,
	}
	for _, benchmark := range pageItems {
		response.Items = append(response.Items, toAPIBenchmark(benchmark))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetResultMatrix(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalMatrixRequest,
) {
	catalog, ok := h.loadCatalog(ctx, c)
	if !ok {
		return
	}
	benchmarks := make([]dashboard.Benchmark, 0, len(catalog.Benchmarks))
	for _, benchmark := range catalog.Benchmarks {
		if request.GetBenchmarkID() == "" || benchmark.ID == request.GetBenchmarkID() {
			benchmarks = append(benchmarks, benchmark)
		}
	}
	response := &api.KnowledgeEvalMatrixResponse{
		Benchmarks: make([]*api.KnowledgeEvalBenchmark, 0, len(benchmarks)),
	}
	for _, benchmark := range benchmarks {
		response.Benchmarks = append(response.Benchmarks, toAPIBenchmark(benchmark))
	}
	for _, run := range catalog.Runs {
		if request.GetDataset() != "" && run.Dataset != request.GetDataset() {
			continue
		}
		if request.GetPartition() != "" && run.Partition != request.GetPartition() {
			continue
		}
		if request.GetCaseID() != "" && run.CaseID != request.GetCaseID() {
			continue
		}
		response.Rows = append(response.Rows, toAPIMatrixRow(run, benchmarks))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetArtifact(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalArtifactByIDRequest,
) {
	catalog, ok := h.loadCatalog(ctx, c)
	if !ok {
		return
	}
	for _, artifact := range catalog.Artifacts {
		if artifact.Record.ArtifactID == request.ArtifactID {
			c.JSON(consts.StatusOK, &api.KnowledgeEvalArtifactResponse{
				Artifact: toAPIArtifact(artifact),
			})
			return
		}
	}
	writeError(c, fmt.Errorf("%w: artifact %s", knowledgeeval.ErrNotFound, request.ArtifactID))
}

func (h *Handler) GetArtifactView(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalArtifactViewRequest,
) {
	content, contentType, err := h.registry.OpenArtifactView(
		ctx,
		request.ArtifactID,
		request.View,
		request.GetPath(),
	)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Data(consts.StatusOK, contentType, content)
}

func (h *Handler) loadCatalog(
	ctx context.Context,
	c *app.RequestContext,
) (dashboard.Catalog, bool) {
	catalog, err := h.registry.Load(ctx)
	if err != nil {
		writeError(c, err)
		return dashboard.Catalog{}, false
	}
	return catalog, true
}

func (h *Handler) findRun(
	ctx context.Context,
	c *app.RequestContext,
	runID string,
) (dashboard.Run, bool) {
	catalog, ok := h.loadCatalog(ctx, c)
	if !ok {
		return dashboard.Run{}, false
	}
	for _, run := range catalog.Runs {
		if run.Detail.Run.ID == runID {
			return run, true
		}
	}
	writeError(c, fmt.Errorf("%w: run %s", knowledgeeval.ErrNotFound, runID))
	return dashboard.Run{}, false
}

func matchesDataset(
	request *api.KnowledgeEvalListRequest,
	dataset,
	partition,
	status string,
) bool {
	return (request.GetDataset() == "" || dataset == request.GetDataset()) &&
		(request.GetPartition() == "" || partition == request.GetPartition()) &&
		(request.GetStatus() == "" || status == request.GetStatus())
}

func paginate[T any](
	items []T,
	requestedLimit int32,
	cursor string,
) ([]T, *api.KnowledgeEvalPage, error) {
	limit := int(requestedLimit)
	if limit == 0 {
		limit = defaultPageLimit
	}
	if limit < 1 || limit > maxPageLimit {
		return nil, nil, fmt.Errorf(
			"%w: limit must be between 1 and %d",
			knowledgeeval.ErrInvalidRecord,
			maxPageLimit,
		)
	}
	offset, err := decodeCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	if offset > len(items) {
		return nil, nil, fmt.Errorf("%w: cursor is outside the result set", knowledgeeval.ErrInvalidRecord)
	}
	end := min(offset+limit, len(items))
	page := &api.KnowledgeEvalPage{Limit: int32(limit)}
	if end < len(items) {
		next := encodeCursor(end)
		page.NextCursor = &next
	}
	return items[offset:end], page, nil
}

func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("%w: cursor is invalid", knowledgeeval.ErrInvalidRecord)
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("%w: cursor is invalid", knowledgeeval.ErrInvalidRecord)
	}
	return offset, nil
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func toAPISolution(solution dashboard.SolutionVersion) *api.KnowledgeEvalSolutionVersion {
	result := &api.KnowledgeEvalSolutionVersion{
		ID: solution.ID, BuilderID: solution.BuilderID,
		BuilderVersion: solution.BuilderVersion, CodeRevision: solution.CodeRevision,
	}
	if solution.Model != "" {
		result.Model = stringPointer(solution.Model)
	}
	if solution.ConfigDigest != "" {
		result.ConfigDigest = stringPointer(solution.ConfigDigest)
	}
	return result
}

func toAPIDataset(dataset dashboard.Dataset) *api.KnowledgeEvalDataset {
	result := &api.KnowledgeEvalDataset{
		ID: dataset.ID, Dataset: dataset.Name, Partition: dataset.Partition,
		CaseID: dataset.CaseID, Status: dataset.Status,
		Sessions: int32(dataset.Sessions), Turns: int32(dataset.Turns),
		Sources: int32(dataset.Sources), Questions: int32(dataset.Questions),
		ExperimentCount: int32(dataset.ExperimentCount),
		GroupKind:       dataset.GroupKind, SourceKind: dataset.SourceKind,
		Trajectories:    int32(dataset.Trajectories),
		EvaluationCases: int32(dataset.EvaluationCases),
		RunCount:        int32(dataset.RunCount), ArtifactCount: int32(dataset.ArtifactCount),
		CaseIds: slices.Clone(dataset.CaseIDs),
	}
	if !dataset.UpdatedAt.IsZero() {
		result.UpdatedAt = stringPointer(dataset.UpdatedAt.UTC().Format(timeLayout))
	}
	return result
}

func toAPIDatasetFamily(family dashboard.DatasetFamily) *api.KnowledgeEvalDatasetFamily {
	partitions := make([]*api.KnowledgeEvalDatasetPartition, 0, len(family.Partitions))
	for _, partition := range family.Partitions {
		partitions = append(partitions, &api.KnowledgeEvalDatasetPartition{
			Name: partition.Name, GroupCount: int32(partition.GroupCount),
			RunGroupCount: int32(partition.RunGroupCount),
		})
	}
	result := &api.KnowledgeEvalDatasetFamily{
		ID: family.ID, Name: family.Name, GroupKind: family.GroupKind,
		GroupCount:    int32(family.GroupCount),
		RunGroupCount: int32(family.RunGroupCount),
		RunCount:      int32(family.RunCount), ArtifactCount: int32(family.ArtifactCount),
		Partitions: partitions,
	}
	if family.Revision != "" {
		result.Revision = stringPointer(family.Revision)
	}
	if family.License != "" {
		result.License = stringPointer(family.License)
	}
	return result
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

func toAPIRun(run dashboard.Run) *api.KnowledgeEvalRun {
	result := &api.KnowledgeEvalRun{
		ID: run.Detail.Run.ID, DatasetID: run.DatasetID, Dataset: run.Dataset,
		Partition: run.Partition, CaseID: run.CaseID,
		SolutionVersionID: run.SolutionVersion.ID, ArtifactID: run.ArtifactID,
		Status:       string(run.Detail.Run.Status),
		CreatedAt:    run.Detail.Run.CreatedAt.UTC().Format(timeLayout),
		BenchmarkIds: slices.Clone(run.BenchmarkIDs),
		Metadata:     maps.Clone(run.Detail.Run.Metadata),
	}
	if !run.Detail.Run.CompletedAt.IsZero() {
		result.CompletedAt = stringPointer(run.Detail.Run.CompletedAt.UTC().Format(timeLayout))
	}
	return result
}

func toAPIArtifact(artifact dashboard.Artifact) *api.KnowledgeEvalArtifact {
	views := make(map[string]string, len(artifact.Views))
	for kind := range artifact.Views {
		views[kind] = fmt.Sprintf(
			"/v1/knowledge-eval/artifacts/%s/views/%s",
			artifact.Record.ArtifactID,
			kind,
		)
	}
	return &api.KnowledgeEvalArtifact{
		ArtifactID: artifact.Record.ArtifactID, Product: artifact.Product,
		Kind: artifact.Record.Kind, Role: artifact.Role, DatasetID: artifact.DatasetID,
		SolutionVersionID: artifact.SolutionVersionID,
		Sha256:            artifact.Record.Payload.SHA256,
		CreatedAt:         artifact.Record.CreatedAt.UTC().Format(timeLayout),
		Views:             views,
	}
}

func toAPITrials(trials []knowledgeeval.Trial) []*api.KnowledgeEvalTrial {
	result := make([]*api.KnowledgeEvalTrial, 0, len(trials))
	for _, trial := range trials {
		item := &api.KnowledgeEvalTrial{
			ID: trial.ID, RunID: trial.RunID, BenchmarkID: trial.BenchmarkID,
			BenchmarkFingerprint: trial.BenchmarkFingerprint, Status: string(trial.Status),
		}
		if trial.IneligibleReason != "" {
			item.IneligibleReason = stringPointer(trial.IneligibleReason)
		}
		if trial.Result != nil {
			item.ResultStatus = stringPointer(trial.Result.Status)
			item.Metrics = toAPIMetrics(trial.Result.Metrics)
		}
		result = append(result, item)
	}
	return result
}

func toAPIEvents(events []knowledgeeval.Event) []*api.KnowledgeEvalEvent {
	result := make([]*api.KnowledgeEvalEvent, 0, len(events))
	for _, event := range events {
		item := &api.KnowledgeEvalEvent{
			ID: event.ID, RunID: event.RunID, Stage: string(event.Stage),
			Message: event.Message, CreatedAt: event.CreatedAt.UTC().Format(timeLayout),
		}
		if event.TrialID != "" {
			item.TrialID = stringPointer(event.TrialID)
		}
		if event.AttemptID != "" {
			item.AttemptID = stringPointer(event.AttemptID)
		}
		result = append(result, item)
	}
	return result
}

func toAPIMetrics(metrics []knowledgeeval.Metric) []*api.KnowledgeEvalMetric {
	result := make([]*api.KnowledgeEvalMetric, 0, len(metrics))
	for _, metric := range metrics {
		result = append(result, &api.KnowledgeEvalMetric{
			Name: metric.Name, Value: metric.Value, Unit: metric.Unit,
		})
	}
	return result
}

func toAPIBenchmark(benchmark dashboard.Benchmark) *api.KnowledgeEvalBenchmark {
	return &api.KnowledgeEvalBenchmark{
		ID: benchmark.ID, Name: benchmark.Name, Description: benchmark.Description,
		PrimaryMetric: benchmark.PrimaryMetric, Executed: benchmark.Executed,
	}
}

func toAPIMatrixRow(
	run dashboard.Run,
	benchmarks []dashboard.Benchmark,
) *api.KnowledgeEvalMatrixRow {
	row := &api.KnowledgeEvalMatrixRow{
		RunID: run.Detail.Run.ID, SolutionVersionID: run.SolutionVersion.ID,
		ArtifactID: run.ArtifactID,
		Cells:      make([]*api.KnowledgeEvalMatrixCell, 0, len(benchmarks)),
	}
	trials := make(map[string]knowledgeeval.Trial, len(run.Detail.Trials))
	for _, trial := range run.Detail.Trials {
		trials[trial.BenchmarkID] = trial
	}
	for _, benchmark := range benchmarks {
		cell := &api.KnowledgeEvalMatrixCell{BenchmarkID: benchmark.ID}
		trial, exists := trials[benchmark.ID]
		if exists && trial.Result != nil {
			cell.Executed = true
			cell.Metrics = toAPIMetrics(trial.Result.Metrics)
			for _, metric := range trial.Result.Metrics {
				if metric.Name == benchmark.PrimaryMetric {
					cell.Score = floatPointer(metric.Value)
					break
				}
			}
		}
		row.Cells = append(row.Cells, cell)
	}
	return row
}

func writeError(c *app.RequestContext, err error) {
	status := consts.StatusInternalServerError
	switch {
	case errors.Is(err, knowledgeeval.ErrNotFound):
		status = consts.StatusNotFound
	case errors.Is(err, knowledgeeval.ErrInvalidRecord):
		status = consts.StatusBadRequest
	case errors.Is(err, experimenttask.ErrConflict), errors.Is(err, datasetinstall.ErrConflict):
		status = consts.StatusConflict
	}
	c.JSON(status, map[string]string{"error": err.Error()})
}

func stringPointer(value string) *string {
	return &value
}

func floatPointer(value float64) *float64 {
	return &value
}
