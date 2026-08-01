package handler

import (
	"context"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/cohorttask"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/dashboard"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/datasetinstall"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/experimenttask"
)

const handlerContextKey = "knowledge-eval.http-handler"

type Handler struct {
	registry *dashboard.Registry
	tasks    TaskService
	cohorts  CohortService
	installs DatasetInstallService
}

type DatasetInstallService interface {
	Sources() []datasetinstall.Source
	Create(string, string) (datasetinstall.Task, error)
	List() []datasetinstall.Task
	Get(string) (datasetinstall.Task, error)
	Cancel(string) (datasetinstall.Task, error)
}

type TaskService interface {
	Preview(context.Context, experimenttask.Request) (experimenttask.Preview, error)
	Create(context.Context, experimenttask.Request, string) (experimenttask.Task, error)
	List() []experimenttask.Task
	Get(string) (experimenttask.Task, error)
	Cancel(string) (experimenttask.Task, error)
	Retry(context.Context, string, string) (experimenttask.Task, error)
	Continue(
		context.Context,
		string,
		experimenttask.ContinueOptions,
		string,
	) (experimenttask.Task, error)
}

type CohortService interface {
	Preview(context.Context, cohorttask.Request) (cohorttask.Preview, error)
	Create(context.Context, cohorttask.Request, string) (cohorttask.Campaign, error)
	List() []cohorttask.Campaign
	Get(string) (cohorttask.Campaign, error)
	Cancel(string) (cohorttask.Campaign, error)
}

type Option func(*Handler)

func WithTaskService(tasks TaskService) Option {
	return func(handler *Handler) {
		handler.tasks = tasks
	}
}

func WithCohortService(cohorts CohortService) Option {
	return func(handler *Handler) {
		handler.cohorts = cohorts
	}
}

func WithDatasetInstallService(installs DatasetInstallService) Option {
	return func(handler *Handler) {
		handler.installs = installs
	}
}

func New(registry *dashboard.Registry, options ...Option) (*Handler, error) {
	if registry == nil {
		return nil, fmt.Errorf("create knowledge eval HTTP handler: registry is required")
	}
	handler := &Handler{registry: registry}
	for _, option := range options {
		option(handler)
	}
	return handler, nil
}

func InstanceMiddleware(handler *Handler) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		request.Set(handlerContextKey, handler)
		request.Next(ctx)
	}
}

func handlerFromRequest(request *app.RequestContext) (*Handler, bool) {
	value, found := request.Get(handlerContextKey)
	if !found {
		return nil, false
	}
	handler, ok := value.(*Handler)
	return handler, ok && handler != nil
}
