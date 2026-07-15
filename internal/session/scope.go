package session

import (
	"context"
	"errors"
	"strings"
)

var ErrMissingScope = errors.New("authenticated collaboration scope is missing")

type scopeContextKey struct{}

func WithScope(ctx context.Context, scopeID string) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, strings.TrimSpace(scopeID))
}

func ScopeFromContext(ctx context.Context) (string, error) {
	scopeID, ok := ctx.Value(scopeContextKey{}).(string)
	if !ok || scopeID == "" {
		return "", ErrMissingScope
	}
	return scopeID, nil
}
