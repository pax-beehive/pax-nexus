package handler

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/experimenttask"
	api "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/transport/httpapi/model/knowledgeeval/api"
)

func (h *Handler) PreviewExperimentTask(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalExperimentTaskRequest,
) {
	tasks, ok := h.taskService(c)
	if !ok {
		return
	}
	preview, err := tasks.Preview(ctx, fromAPITaskRequest(request))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalExperimentTaskPreviewResponse{
		Preview: toAPITaskPreview(preview),
	})
}

func (h *Handler) ListExperimentModels(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	models := experimenttask.SupportedModels()
	pageItems, page, err := paginate(models, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalExperimentModelsResponse{
		Items: make([]*api.KnowledgeEvalExperimentModel, 0, len(pageItems)),
		Page:  page,
	}
	for _, model := range pageItems {
		response.Items = append(response.Items, &api.KnowledgeEvalExperimentModel{
			ID: model.ID, Name: model.Name, Provider: model.Provider,
		})
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) CreateExperimentTask(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalExperimentTaskRequest,
) {
	tasks, ok := h.taskService(c)
	if !ok {
		return
	}
	task, err := tasks.Create(
		ctx,
		fromAPITaskRequest(request),
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, &api.KnowledgeEvalExperimentTaskResponse{
		Task: toAPITask(task),
	})
}

func (h *Handler) ListExperimentTasks(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	tasks, ok := h.taskService(c)
	if !ok {
		return
	}
	items := tasks.List()
	if status := request.GetStatus(); status != "" {
		filtered := make([]experimenttask.Task, 0, len(items))
		for _, task := range items {
			if task.Status == status {
				filtered = append(filtered, task)
			}
		}
		items = filtered
	}
	pageItems, page, err := paginate(items, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalExperimentTasksResponse{
		Items: make([]*api.KnowledgeEvalExperimentTask, 0, len(pageItems)),
		Page:  page,
	}
	for _, task := range pageItems {
		response.Items = append(response.Items, toAPITask(task))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetExperimentTask(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalExperimentTaskByIDRequest,
) {
	tasks, ok := h.taskService(c)
	if !ok {
		return
	}
	task, err := tasks.Get(request.TaskID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalExperimentTaskResponse{
		Task: toAPITask(task),
	})
}

func (h *Handler) CancelExperimentTask(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalExperimentTaskByIDRequest,
) {
	tasks, ok := h.taskService(c)
	if !ok {
		return
	}
	task, err := tasks.Cancel(request.TaskID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalExperimentTaskResponse{
		Task: toAPITask(task),
	})
}

func (h *Handler) RetryExperimentTask(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalExperimentTaskByIDRequest,
) {
	tasks, ok := h.taskService(c)
	if !ok {
		return
	}
	task, err := tasks.Retry(
		ctx,
		request.TaskID,
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, &api.KnowledgeEvalExperimentTaskResponse{
		Task: toAPITask(task),
	})
}

func (h *Handler) ContinueExperimentTask(
	ctx context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalExperimentTaskContinueRequest,
) {
	tasks, ok := h.taskService(c)
	if !ok {
		return
	}
	task, err := tasks.Continue(
		ctx,
		request.TaskID,
		experimenttask.ContinueOptions{
			AdditionalRounds:    int(request.GetAdditionalRounds()),
			AdditionalQuestions: int(request.GetAdditionalQuestions()),
		},
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, &api.KnowledgeEvalExperimentTaskResponse{
		Task: toAPITask(task),
	})
}

func (h *Handler) taskService(c *app.RequestContext) (TaskService, bool) {
	if h.tasks == nil {
		c.JSON(consts.StatusServiceUnavailable, map[string]string{
			"error": "knowledge eval experiment tasks are not configured",
		})
		return nil, false
	}
	return h.tasks, true
}

func fromAPITaskRequest(request *api.KnowledgeEvalExperimentTaskRequest) experimenttask.Request {
	return experimenttask.Request{
		Dataset: request.Dataset, Partition: request.Partition, GroupID: request.GroupID,
		Mode: request.GetMode(), Model: request.GetModel(),
		ReaderModel:             request.GetReaderModel(),
		QuestionLimit:           int(request.GetQuestionLimit()),
		QuestionOffset:          int(request.GetQuestionOffset()),
		MaxRounds:               int(request.GetMaxRounds()),
		ConfirmPaid:             request.GetConfirmPaid(),
		ReuseArtifactFromTaskID: request.GetReuseArtifactFromTaskID(),
	}
}

func toAPITask(task experimenttask.Task) *api.KnowledgeEvalExperimentTask {
	result := &api.KnowledgeEvalExperimentTask{
		ID: task.ID, Request: toAPITaskRequest(task.Request),
		Preview: toAPITaskPreview(task.Preview), Status: task.Status,
		CreatedAt:             task.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:             task.UpdatedAt.Format(time.RFC3339Nano),
		CancellationRequested: task.CancellationRequested,
		RunIds:                slicesOrEmpty(task.RunIDs), ArtifactIds: slicesOrEmpty(task.ArtifactIDs),
		Events: make([]*api.KnowledgeEvalExperimentTaskEvent, 0, len(task.Events)),
	}
	if task.StartedAt != nil {
		result.StartedAt = stringPointer(task.StartedAt.Format(time.RFC3339Nano))
	}
	if task.CompletedAt != nil {
		result.CompletedAt = stringPointer(task.CompletedAt.Format(time.RFC3339Nano))
	}
	if task.Error != "" {
		result.Error = stringPointer(task.Error)
	}
	if task.ResultPath != "" {
		result.ResultPath = stringPointer(task.ResultPath)
	}
	if task.RetryOfTaskID != "" {
		result.RetryOfTaskID = stringPointer(task.RetryOfTaskID)
	}
	if task.ContinuedFromTaskID != "" {
		result.ContinuedFromTaskID = stringPointer(task.ContinuedFromTaskID)
	}
	for _, event := range task.Events {
		result.Events = append(result.Events, &api.KnowledgeEvalExperimentTaskEvent{
			Status: event.Status, Message: event.Message,
			CreatedAt: event.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	return result
}

func toAPITaskRequest(request experimenttask.Request) *api.KnowledgeEvalExperimentTaskRequest {
	return &api.KnowledgeEvalExperimentTaskRequest{
		Dataset: request.Dataset, Partition: request.Partition, GroupID: request.GroupID,
		Mode: stringPointer(request.Mode), Model: stringPointer(request.Model),
		ReaderModel:             stringPointer(request.ReaderModel),
		QuestionLimit:           int32Pointer(int32(request.QuestionLimit)),
		QuestionOffset:          int32Pointer(int32(request.QuestionOffset)),
		MaxRounds:               int32Pointer(int32(request.MaxRounds)),
		ConfirmPaid:             boolPointer(request.ConfirmPaid),
		ReuseArtifactFromTaskID: stringPointer(request.ReuseArtifactFromTaskID),
	}
}

func toAPITaskPreview(preview experimenttask.Preview) *api.KnowledgeEvalExperimentTaskPreview {
	result := &api.KnowledgeEvalExperimentTaskPreview{
		Eligible: preview.Eligible, Paid: preview.Paid,
		LlmConfigured: preview.LLMConfigured,
		Dataset:       preview.Dataset, Partition: preview.Partition, GroupID: preview.GroupID,
		SourceKind:          preview.SourceKind,
		AvailableQuestions:  int32(preview.AvailableQuestions),
		SelectedQuestions:   int32(preview.SelectedQuestions),
		CumulativeQuestions: int32(preview.CumulativeQuestions),
		PlannedRuns:         int32(preview.PlannedRuns),
		MaxLlmCalls:         int32(preview.MaxLLMCalls),
		Benchmarks:          slicesOrEmpty(preview.Benchmarks),
		IncludesSourceOnly:  preview.IncludesSourceOnly,
		IncludesMaintainer:  preview.IncludesMaintainer,
	}
	if preview.IneligibleReason != "" {
		result.IneligibleReason = stringPointer(preview.IneligibleReason)
	}
	return result
}

func slicesOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func int32Pointer(value int32) *int32 {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}
