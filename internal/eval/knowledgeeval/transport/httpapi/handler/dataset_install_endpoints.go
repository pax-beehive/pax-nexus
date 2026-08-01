package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	api "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/transport/httpapi/model/knowledgeeval/api"
)

func ListDatasetSources(ctx context.Context, c *app.RequestContext) {
	var request api.KnowledgeEvalListRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid dataset source list request")
		return
	}
	handler, ok := handlerFromRequest(c)
	if !ok {
		c.String(consts.StatusInternalServerError, "knowledge eval runtime is not configured")
		return
	}
	handler.ListDatasetSources(ctx, c, &request)
}

func CreateDatasetInstallTask(ctx context.Context, c *app.RequestContext) {
	var request api.KnowledgeEvalDatasetInstallRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid dataset install request")
		return
	}
	handler, ok := handlerFromRequest(c)
	if !ok {
		c.String(consts.StatusInternalServerError, "knowledge eval runtime is not configured")
		return
	}
	handler.CreateDatasetInstallTask(ctx, c, &request)
}

func ListDatasetInstallTasks(ctx context.Context, c *app.RequestContext) {
	var request api.KnowledgeEvalListRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid dataset install task list request")
		return
	}
	handler, ok := handlerFromRequest(c)
	if !ok {
		c.String(consts.StatusInternalServerError, "knowledge eval runtime is not configured")
		return
	}
	handler.ListDatasetInstallTasks(ctx, c, &request)
}

func GetDatasetInstallTask(ctx context.Context, c *app.RequestContext) {
	var request api.KnowledgeEvalDatasetInstallTaskByIDRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid dataset install task request")
		return
	}
	handler, ok := handlerFromRequest(c)
	if !ok {
		c.String(consts.StatusInternalServerError, "knowledge eval runtime is not configured")
		return
	}
	handler.GetDatasetInstallTask(ctx, c, &request)
}

func CancelDatasetInstallTask(ctx context.Context, c *app.RequestContext) {
	var request api.KnowledgeEvalDatasetInstallTaskByIDRequest
	if err := c.BindAndValidate(&request); err != nil {
		c.String(consts.StatusBadRequest, "invalid dataset install cancellation request")
		return
	}
	handler, ok := handlerFromRequest(c)
	if !ok {
		c.String(consts.StatusInternalServerError, "knowledge eval runtime is not configured")
		return
	}
	handler.CancelDatasetInstallTask(ctx, c, &request)
}
