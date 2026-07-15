package observability_test

import (
	"bytes"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/stretchr/testify/suite"
)

type loggingSuite struct {
	suite.Suite
}

func TestLoggingSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(loggingSuite))
}

func (s *loggingSuite) TestLoggerPolicies() {
	tests := []struct {
		name   string
		logger func(*bytes.Buffer)
		want   string
		empty  bool
	}{
		{
			name: "JSON logger writes structured fields",
			logger: func(output *bytes.Buffer) {
				observability.NewLogger(output).Info("event", "agent_id", "agent")
			},
			want: `"agent_id":"agent"`,
		},
		{
			name: "discard logger remains silent",
			logger: func(_ *bytes.Buffer) {
				observability.DiscardLogger().Info("event", "agent_id", "agent")
			},
			empty: true,
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			var output bytes.Buffer
			test.logger(&output)
			if test.empty {
				s.Empty(output.String())
				return
			}
			s.Contains(output.String(), test.want)
		})
	}
}
