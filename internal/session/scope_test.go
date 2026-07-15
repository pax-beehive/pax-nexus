package session_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/suite"
)

type scopeSuite struct {
	suite.Suite
}

func TestScopeSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(scopeSuite))
}

func (s *scopeSuite) TestScopeLifecycle() {
	tests := []struct {
		name      string
		ctx       context.Context
		wantScope string
		wantError error
	}{
		{name: "present", ctx: session.WithScope(context.Background(), " scope-a "), wantScope: "scope-a"},
		{name: "missing", ctx: context.Background(), wantError: session.ErrMissingScope},
		{name: "blank", ctx: session.WithScope(context.Background(), " "), wantError: session.ErrMissingScope},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			scopeID, err := session.ScopeFromContext(test.ctx)
			if test.wantError != nil {
				s.Require().ErrorIs(err, test.wantError)
				return
			}
			s.Require().NoError(err)
			s.Equal(test.wantScope, scopeID)
		})
	}
}
