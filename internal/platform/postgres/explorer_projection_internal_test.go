package postgres

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type explorerProjectionSuite struct {
	suite.Suite
}

func TestExplorerProjectionSuite(t *testing.T) {
	suite.Run(t, new(explorerProjectionSuite))
}

func (s *explorerProjectionSuite) TestCandidateRejectionReasonUsesControlledCodes() {
	tests := []struct {
		name   string
		stored string
		want   string
	}{
		{name: "empty", stored: "", want: ""},
		{name: "known", stored: "missing_evidence", want: "missing_evidence"},
		{name: "code shaped secret", stored: "credential_sk_live_abc123", want: "candidate_rejected"},
		{name: "raw error", stored: "candidate payload leaked: super-secret", want: "candidate_rejected"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.Equal(test.want, safeCandidateRejectionReason(test.stored))
		})
	}
}
