package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

var ErrUnauthorized = errors.New("unauthorized")

const handlerContextKey = "team-memory.http-handler"

type ScopeResolver interface {
	ResolveScope(*app.RequestContext) (string, error)
}

// Handler adapts HTTP requests to one Team Note runtime instance.
type Handler struct {
	runtime     teamnote.Runtime
	resolver    ScopeResolver
	credentials CredentialLifecycle
	memory      recall.Service
	logger      *slog.Logger
}

type CredentialLifecycle interface {
	Authenticate(context.Context, string) (onprem.Principal, error)
	CreateEnrollment(context.Context, onprem.Principal, onprem.EnrollmentRequest) (onprem.Enrollment, error)
	ExchangeEnrollment(context.Context, string) (onprem.IssuedCredential, error)
	RotateCredential(context.Context, onprem.Principal) (onprem.IssuedCredential, error)
	RevokeCredential(context.Context, onprem.Principal, string) error
}

func New(runtime teamnote.Runtime, resolver ScopeResolver, logger *slog.Logger) (*Handler, error) {
	if runtime == nil || resolver == nil || logger == nil {
		return nil, fmt.Errorf("create HTTP handler: runtime, scope resolver, and logger are required")
	}
	return &Handler{runtime: runtime, resolver: resolver, logger: logger}, nil
}

func NewOnPrem(
	runtime teamnote.Runtime,
	credentials CredentialLifecycle,
	memory recall.Service,
	logger *slog.Logger,
) (*Handler, error) {
	if runtime == nil || credentials == nil || memory == nil || logger == nil {
		return nil, fmt.Errorf("create on-prem HTTP handler: runtime, credentials, memory, and logger are required")
	}
	return &Handler{
		runtime: runtime, resolver: StaticAPIKeys{}, credentials: credentials, memory: memory, logger: logger,
	}, nil
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
	key, err := bearerKey(request)
	if err != nil {
		return "", ErrUnauthorized
	}
	scopeID := strings.TrimSpace(keys[key])
	if scopeID == "" {
		return "", ErrUnauthorized
	}
	return scopeID, nil
}

func bearerKey(request *app.RequestContext) (string, error) {
	authorization := strings.TrimSpace(string(request.GetHeader("Authorization")))
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return "", ErrUnauthorized
	}
	key := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	if key == "" {
		return "", ErrUnauthorized
	}
	return key, nil
}
