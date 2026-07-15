package teamnote

import (
	"context"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

var ErrMissingScope = session.ErrMissingScope

func WithScope(ctx context.Context, scopeID string) context.Context {
	return session.WithScope(ctx, scopeID)
}

func ScopeFromContext(ctx context.Context) (string, error) {
	return session.ScopeFromContext(ctx)
}
