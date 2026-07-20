package teamnote_test

import (
	"os"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type recallCandidateStrategySuite struct {
	suite.Suite
}

func TestRecallCandidateStrategySuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(recallCandidateStrategySuite))
}

func (s *recallCandidateStrategySuite) TestRegistryExposesStableDistributionNames() {
	s.Equal([]string{
		teamnote.RecallCandidateStrategyPassiveV1,
		teamnote.RecallCandidateStrategyHintV1Selective,
	}, teamnote.RecallCandidateStrategyNames())
}

func (s *recallCandidateStrategySuite) TestPassiveV1KeepsHintRecallDisabled() {
	strategy, err := teamnote.ResolveRecallCandidateStrategy(teamnote.RecallCandidateStrategyPassiveV1)

	s.Require().NoError(err)
	s.Equal(teamnote.RecallCandidateStrategyPassiveV1, strategy.Name)
	s.InDelta(0.50, strategy.Policy.SemanticThreshold, 0.0001)
	s.Equal(16, strategy.Policy.CandidateLimit)
	s.False(strategy.Policy.EnableHintRecall)
	s.True(strategy.Policy.SuppressDuplicates)
	s.True(strategy.Policy.DegradeRelated)
}

func (s *recallCandidateStrategySuite) TestHintV1SelectiveExtendsPassiveV1WithOneFocusedRecall() {
	passive, err := teamnote.ResolveRecallCandidateStrategy(teamnote.RecallCandidateStrategyPassiveV1)
	s.Require().NoError(err)
	hint, err := teamnote.ResolveRecallCandidateStrategy(teamnote.RecallCandidateStrategyHintV1Selective)

	s.Require().NoError(err)
	s.InDelta(passive.Policy.SemanticThreshold, hint.Policy.SemanticThreshold, 0.0001)
	s.Equal(passive.Policy.CandidateLimit, hint.Policy.CandidateLimit)
	s.True(hint.Policy.EnableHintRecall)
	s.InDelta(0.20, hint.Policy.HintSemanticThreshold, 0.0001)
	s.InDelta(0.60, hint.Policy.HintThreshold, 0.0001)
	s.InDelta(0.10, hint.Policy.HintMinQueryRelevance, 0.0001)
	s.InDelta(0.85, hint.Policy.HintMinMarginalUtility, 0.0001)
}

func (s *recallCandidateStrategySuite) TestRejectsUnsupportedStrategy() {
	_, err := teamnote.ResolveRecallCandidateStrategy("unknown")

	s.Require().Error(err)
}

func (s *recallCandidateStrategySuite) TestBuildDefaultIsARegisteredStrategy() {
	s.Contains(teamnote.RecallCandidateStrategyNames(), teamnote.DefaultRecallCandidateStrategy())
}

func (s *recallCandidateStrategySuite) TestBuildDefaultMatchesInjectedValue() {
	want := os.Getenv("TEAM_MEMORY_TEST_BUILD_DEFAULT_RECALL_CANDIDATE_STRATEGY")
	if want == "" {
		s.T().Skip("build-default assertion requires an injected linker value")
	}
	s.Equal(want, teamnote.DefaultRecallCandidateStrategy())
}
