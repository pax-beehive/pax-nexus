package httpapi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/todoapp"
)

const handlerContextKey = "todo-app.http-handler"

// Service is the domain surface the transport consumes.
type Service interface {
	CreateTodo(ctx context.Context, scopeID, userID, title, body string) (todoapp.Todo, error)
	CompleteTodo(ctx context.Context, scopeID, userID, todoID string) (todoapp.Todo, error)
	ListTodos(ctx context.Context, scopeID string, status todoapp.TodoStatus) ([]todoapp.Todo, error)
	PendingSuggestions(ctx context.Context, scopeID string) ([]todoapp.Suggestion, error)
	RefreshSuggestions(ctx context.Context, scopeID string) (int, error)
	AcceptSuggestion(ctx context.Context, scopeID, userID, suggestionID string) (todoapp.Todo, error)
	DismissSuggestion(ctx context.Context, scopeID, userID, suggestionID string) error
}

// HumanAuthenticator validates a Human Portal session token.
// *onprem.IdentityService satisfies it.
type HumanAuthenticator interface {
	AuthenticateSession(ctx context.Context, token string) (onprem.HumanPrincipal, error)
}

// Handler serves the Todo App HTTP transport. A nil identity is a valid,
// deliberate configuration (e.g. the on-prem human portal is disabled): every
// route then answers 501 not_configured instead of panicking.
type Handler struct {
	service  Service
	identity HumanAuthenticator // nil => endpoints answer 501 not_configured
	logger   *slog.Logger
}

// Option customizes the Handler.
type Option func(*Handler)

// WithLogger sets the logger used for error detail that must not reach the
// browser. Defaults to slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(h *Handler) {
		if logger != nil {
			h.logger = logger
		}
	}
}

// New creates the Todo App HTTP handler. service is required; identity may be
// nil when the human portal is not configured for this deployment.
func New(service Service, identity HumanAuthenticator, opts ...Option) (*Handler, error) {
	if service == nil {
		return nil, fmt.Errorf("create Todo App HTTP handler: service is required")
	}
	handler := &Handler{service: service, identity: identity, logger: slog.Default()}
	for _, opt := range opts {
		opt(handler)
	}
	return handler, nil
}

// InstanceMiddleware attaches the handler to every request in the group it is
// registered on, mirroring internal/pagewiki/transport/httpapi.
func InstanceMiddleware(handler *Handler) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		request.Set(handlerContextKey, handler)
		request.Next(ctx)
	}
}

func handlerFromRequest(request *app.RequestContext) (*Handler, bool) {
	configured, found := request.Get(handlerContextKey)
	if !found {
		return nil, false
	}
	handler, ok := configured.(*Handler)
	return handler, ok && handler != nil
}
