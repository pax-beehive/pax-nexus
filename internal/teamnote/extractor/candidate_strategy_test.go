package extractor_test

import (
	"os"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type candidateStrategySuite struct {
	suite.Suite
}

func TestCandidateStrategySuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(candidateStrategySuite))
}

func (s *candidateStrategySuite) TestRegistryExposesStableDistributionNames() {
	s.Equal([]string{
		extractor.CandidateStrategyCurrent,
		extractor.CandidateStrategyInteractionSlim,
		extractor.CandidateStrategyTyped2,
		extractor.CandidateStrategySourceSpanV1,
		extractor.CandidateStrategySourceSpanV2,
		extractor.CandidateStrategyClaimCardV1,
		extractor.CandidateStrategyClaimCardV2,
	}, extractor.CandidateStrategyNames())
}

func (s *candidateStrategySuite) TestBuildDefaultIsARegisteredStrategy() {
	s.Contains(extractor.CandidateStrategyNames(), extractor.DefaultCandidateStrategy())
}

func (s *candidateStrategySuite) TestBuildDefaultMatchesInjectedValue() {
	want := os.Getenv("TEAM_MEMORY_TEST_BUILD_DEFAULT_CANDIDATE_STRATEGY")
	if want == "" {
		s.T().Skip("build-default assertion requires an injected linker value")
	}
	s.Equal(want, extractor.DefaultCandidateStrategy())
}
