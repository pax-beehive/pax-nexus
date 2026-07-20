package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

var ErrUnauthorized = errors.New("unauthorized")

const handlerContextKey = "team-memory.http-handler"

type ScopeResolver interface {
	ResolveScope(*app.RequestContext) (string, error)
}

// Handler adapts HTTP requests to one Team Note runtime instance.
type Handler struct {
	runtime  teamnote.Runtime
	resolver ScopeResolver
	logger   *slog.Logger
}

func New(runtime teamnote.Runtime, resolver ScopeResolver, logger *slog.Logger) (*Handler, error) {
	if runtime == nil || resolver == nil || logger == nil {
		return nil, fmt.Errorf("create HTTP handler: runtime, scope resolver, and logger are required")
	}
	return &Handler{runtime: runtime, resolver: resolver, logger: logger}, nil
}

// InstanceMiddleware binds a handler to requests served by one Hertz instance.
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

type StaticAPIKeys map[string]string

func (keys StaticAPIKeys) ResolveScope(request *app.RequestContext) (string, error) {
	authorization := strings.TrimSpace(string(request.GetHeader("Authorization")))
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return "", ErrUnauthorized
	}
	key := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	scopeID := strings.TrimSpace(keys[key])
	if scopeID == "" {
		return "", ErrUnauthorized
	}
	return scopeID, nil
}
