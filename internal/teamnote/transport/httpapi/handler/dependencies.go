package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

var (
	ErrUnauthorized        = errors.New("unauthorized")
	ErrNotConfigured       = errors.New("HTTP handler is not configured")
	configuredDependencies dependencies
	dependenciesMu         sync.RWMutex
)

type ScopeResolver interface {
	ResolveScope(*app.RequestContext) (string, error)
}

type dependencies struct {
	runtime  teamnote.Runtime
	resolver ScopeResolver
	logger   *slog.Logger
}

func Configure(runtime teamnote.Runtime, resolver ScopeResolver) error {
	return ConfigureWithLogger(runtime, resolver, observability.DiscardLogger())
}

func ConfigureWithLogger(runtime teamnote.Runtime, resolver ScopeResolver, logger *slog.Logger) error {
	if runtime == nil || resolver == nil || logger == nil {
		return fmt.Errorf("configure HTTP handler: runtime, scope resolver, and logger are required")
	}
	dependenciesMu.Lock()
	configuredDependencies = dependencies{runtime: runtime, resolver: resolver, logger: logger}
	dependenciesMu.Unlock()
	return nil
}

func currentDependencies() (dependencies, error) {
	dependenciesMu.RLock()
	result := configuredDependencies
	dependenciesMu.RUnlock()
	if result.runtime == nil || result.resolver == nil {
		return dependencies{}, ErrNotConfigured
	}
	return result, nil
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
