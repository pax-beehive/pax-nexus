package main

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type mainSuite struct {
	suite.Suite
}

func TestMainSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(mainSuite))
}

func (s *mainSuite) TestExportScopeResolverSupportsSharedEvalV3Scope() {
	resolve, prefix := exportScopeResolver("run-v3", "run-v3-groupmembench-Finance", "")

	s.Equal("run-v3-groupmembench-Finance", prefix)
	s.Equal("run-v3-groupmembench-Finance", resolve("multi_hop_1"))
	s.Equal("run-v3-groupmembench-Finance", resolve("knowledge_update_20"))
}

func (s *mainSuite) TestExportScopeResolverKeepsPerCaseEvalV2Scopes() {
	resolve, prefix := exportScopeResolver("run-v2", "", "-team-note-hybrid")

	s.Equal("run-v2-groupmembench-", prefix)
	s.Equal("run-v2-groupmembench-case-1-team-note-hybrid", resolve("case-1"))
}
