package handler

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/datasetinstall"
	api "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/transport/httpapi/model/knowledgeeval/api"
)

func (h *Handler) ListDatasetSources(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	installs, ok := h.datasetInstallService(c)
	if !ok {
		return
	}
	items := installs.Sources()
	if dataset := request.GetDataset(); dataset != "" {
		filtered := make([]datasetinstall.Source, 0, len(items))
		for _, item := range items {
			if strings.EqualFold(item.ID, dataset) || strings.EqualFold(item.Name, dataset) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	pageItems, page, err := paginate(items, request.GetLimit(), request.GetCursor())
	if err != nil {
		writeError(c, err)
		return
	}
	response := &api.KnowledgeEvalDatasetSourcesResponse{
		Items: make([]*api.KnowledgeEvalDatasetSource, 0, len(pageItems)),
		Page:  page,
	}
	for _, item := range pageItems {
		response.Items = append(response.Items, toAPIDatasetSource(item))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) CreateDatasetInstallTask(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalDatasetInstallRequest,
) {
	installs, ok := h.datasetInstallService(c)
	if !ok {
		return
	}
	task, err := installs.Create(
		strings.TrimSpace(request.Dataset),
		strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))),
	)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, &api.KnowledgeEvalDatasetInstallTaskResponse{
		Task: toAPIDatasetInstallTask(task),
	})
}

func (h *Handler) ListDatasetInstallTasks(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalListRequest,
) {
	installs, ok := h.datasetInstallService(c)
	if !ok {
		return
	}
	items := installs.List()
	if status := request.GetStatus(); status != "" {
		filtered := make([]datasetinstall.Task, 0, len(items))
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
	response := &api.KnowledgeEvalDatasetInstallTasksResponse{
		Items: make([]*api.KnowledgeEvalDatasetInstallTask, 0, len(pageItems)),
		Page:  page,
	}
	for _, task := range pageItems {
		response.Items = append(response.Items, toAPIDatasetInstallTask(task))
	}
	c.JSON(consts.StatusOK, response)
}

func (h *Handler) GetDatasetInstallTask(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalDatasetInstallTaskByIDRequest,
) {
	installs, ok := h.datasetInstallService(c)
	if !ok {
		return
	}
	task, err := installs.Get(request.TaskID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalDatasetInstallTaskResponse{
		Task: toAPIDatasetInstallTask(task),
	})
}

func (h *Handler) CancelDatasetInstallTask(
	_ context.Context,
	c *app.RequestContext,
	request *api.KnowledgeEvalDatasetInstallTaskByIDRequest,
) {
	installs, ok := h.datasetInstallService(c)
	if !ok {
		return
	}
	task, err := installs.Cancel(request.TaskID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &api.KnowledgeEvalDatasetInstallTaskResponse{
		Task: toAPIDatasetInstallTask(task),
	})
}

func (h *Handler) datasetInstallService(
	c *app.RequestContext,
) (DatasetInstallService, bool) {
	if h.installs == nil {
		c.JSON(consts.StatusServiceUnavailable, map[string]string{
			"error": "knowledge eval dataset installation is not configured",
		})
		return nil, false
	}
	return h.installs, true
}

func toAPIDatasetSource(source datasetinstall.Source) *api.KnowledgeEvalDatasetSource {
	result := &api.KnowledgeEvalDatasetSource{
		ID: source.ID, Name: source.Name, Provider: source.Provider,
		Repository: source.Repository, Revision: source.Revision, License: source.License,
		DownloadSize: source.DownloadSize, DataRoot: source.DataRoot,
		Downloaded: source.Downloaded, Prepared: source.Prepared,
		InstallStatus: source.InstallStatus,
	}
	if source.Note != "" {
		result.Note = stringPointer(source.Note)
	}
	return result
}

func toAPIDatasetInstallTask(task datasetinstall.Task) *api.KnowledgeEvalDatasetInstallTask {
	result := &api.KnowledgeEvalDatasetInstallTask{
		ID: task.ID, Dataset: task.Dataset, Status: task.Status, DataRoot: task.DataRoot,
		CreatedAt: task.CreatedAt.Format(timeLayout), UpdatedAt: task.UpdatedAt.Format(timeLayout),
		CancellationRequested: task.CancellationRequested,
		Events:                make([]*api.KnowledgeEvalDatasetInstallEvent, 0, len(task.Events)),
	}
	if task.StartedAt != nil {
		result.StartedAt = stringPointer(task.StartedAt.Format(timeLayout))
	}
	if task.CompletedAt != nil {
		result.CompletedAt = stringPointer(task.CompletedAt.Format(timeLayout))
	}
	if task.Error != "" {
		result.Error = stringPointer(task.Error)
	}
	for _, event := range task.Events {
		result.Events = append(result.Events, &api.KnowledgeEvalDatasetInstallEvent{
			Status: event.Status, Message: event.Message,
			CreatedAt: event.CreatedAt.Format(timeLayout),
		})
	}
	return result
}
