package main

import (
	"testing"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/stretchr/testify/suite"
)

type seedSuite struct{ suite.Suite }

func TestSeedSuite(t *testing.T) { suite.Run(t, new(seedSuite)) }

func (s *seedSuite) TestBuildsSafeLeadAndSeparateEvidenceWithCrossAgentOrigin() {
	evalCase := v2.Case{
		ID: "temporal_1", Category: "temporal", Question: "What is the enterprise Reporting deadline?",
		Expected: "2025-07-18", AskingUserID: "User_7", AnsweringAgentID: "answerer",
		SupportingAgentIDs: []string{"answerer", "source-agent"},
	}
	runs, err := seedCandidates(evalCase, fixedObservationTime)
	s.Require().NoError(err)
	s.Require().Len(runs, 2)
	lead, evidence := runs[0].Candidates[0], runs[1].Candidates[0]
	s.NotContains(lead.Body, evalCase.Expected)
	s.Contains(lead.Subject, "temporal 1")
	s.Equal([]string{evidence.Subject}, lead.RelatedSubjects)
	s.Equal(evalCase.Expected, evidence.Body)
	s.Equal("source-agent", lead.Origin.AgentID)
	s.Equal(lead.Origin, evidence.Origin)
	s.Equal(lead.EvidenceEventIDs[0], runs[0].Evidence[0].ID)
}

func (s *seedSuite) TestQueryAnchorUsesOneDistinctiveTerm() {
	s.Equal("enterprise", queryAnchor("What is the enterprise deadline?"))
	s.Equal("topic", queryAnchor("?"))
}

func (s *seedSuite) TestAbstentionSeedsLeadWithoutGoldEvidence() {
	runs, err := seedCandidates(v2.Case{
		ID: "abstention_1", Category: "abstention", Question: "What unsupported value should be returned?",
		Expected: "unsupported designated answer", AskingUserID: "User_1",
	}, fixedObservationTime)
	s.Require().NoError(err)
	s.Require().Len(runs, 1)
	s.NotContains(runs[0].Candidates[0].Body, "unsupported designated answer")
}

func (s *seedSuite) TestFixedObservationAndChecksumsAreStable() {
	evalCase := v2.Case{
		ID: "knowledge_update_1", Category: "knowledge_update", Question: "What changed?",
		Expected: "the approved value", AskingUserID: "User_1", AnsweringAgentID: "agent-2",
	}
	first, err := seedCandidates(evalCase, fixedObservationTime)
	s.Require().NoError(err)
	second, err := seedCandidates(evalCase, fixedObservationTime)
	s.Require().NoError(err)
	s.Equal(first, second)
	s.NotEmpty(first[0].InputChecksum)

	firstDigest, err := observationChecksum([]seededCase{{CaseID: evalCase.ID, Runs: first}})
	s.Require().NoError(err)
	secondDigest, err := observationChecksum([]seededCase{{CaseID: evalCase.ID, Runs: second}})
	s.Require().NoError(err)
	s.Equal(firstDigest, secondDigest)
	s.Equal(fixedObservationTime, first[0].Evidence[0].OccurredAt)
}
