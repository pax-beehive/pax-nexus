package v3_test

import (
	"os"
	"path/filepath"
	"testing"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	v3 "github.com/pax-beehive/pax-nexus/internal/eval/v3"
	"github.com/stretchr/testify/suite"
)

type protocolSuite struct{ suite.Suite }

func TestProtocolSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(protocolSuite))
}

func (s *protocolSuite) TestSelectAnswererMatrix() {
	tests := []struct {
		name        string
		evalCase    v2.Case
		wantAgent   string
		wantStrict  bool
		wantOverlap string
	}{
		{
			name: "annotated strict", wantAgent: "groupmembench-User_3", wantStrict: true, wantOverlap: v3.SourceOverlapExcluded,
			evalCase: v2.Case{ID: "case-1", AskingUserID: "User_1", ParticipantAgentIDs: []string{"groupmembench-User_1", "groupmembench-User_2", "groupmembench-User_3"}, SupportingAgentIDs: []string{"groupmembench-User_2"}},
		},
		{
			name: "unannotated overlap", wantAgent: "groupmembench-User_2", wantOverlap: v3.SourceOverlapUnknown,
			evalCase: v2.Case{ID: "case-2", AskingUserID: "User_1", ParticipantAgentIDs: []string{"groupmembench-User_1", "groupmembench-User_2"}},
		},
		{
			name: "no eligible answerer", wantOverlap: v3.SourceOverlapNoEligible,
			evalCase: v2.Case{ID: "case-3", AskingUserID: "User_1", ParticipantAgentIDs: []string{"groupmembench-User_1", "groupmembench-User_2"}, SupportingAgentIDs: []string{"groupmembench-User_2"}},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			first := v3.SelectAnswerer(test.evalCase, "revision", "seed-1")
			second := v3.SelectAnswerer(test.evalCase, "revision", "seed-1")
			s.Equal(first, second)
			s.Equal(test.wantAgent, first.AgentID)
			s.Equal(test.wantStrict, first.StrictCrossAgent)
			s.Equal(test.wantOverlap, first.SourceOverlap)
		})
	}
}

func (s *protocolSuite) TestValidateRequiresExactlyTheThreeArchitectureArms() {
	tests := []struct {
		name string
		arms []v2.ArmConfig
		want bool
	}{
		{name: "valid", want: true, arms: architectureArms()},
		{name: "missing arm", arms: architectureArms()[:2]},
		{name: "extra arm", arms: append(architectureArms(), v2.ArmConfig{Name: "team_note_hybrid", Consumer: command()})},
		{name: "wrong arm", arms: []v2.ArmConfig{{Name: "control", Consumer: command()}, architectureArms()[1], architectureArms()[2]}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			config := baseConfig()
			config.Arms = test.arms
			err := v3.Validate(config)
			if test.want {
				s.NoError(err)
				return
			}
			s.Error(err)
		})
	}
}

func (s *protocolSuite) TestValidateRejectsUnverifiedMem0ReproductionClaims() {
	tests := []struct{ name, level string }{
		{name: "exact", level: v3.ReproductionExact},
		{name: "protocol", level: v3.ReproductionProtocol},
		{name: "empty"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			config := baseConfig()
			config.Mem0ReproductionLevel = test.level

			s.Error(v3.Validate(config))
		})
	}
}

func (s *protocolSuite) TestLoadCasesAssignsOnePairedAnswerer() {
	directory := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(directory, "domain", "producer"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(directory, "domain", "producer", "session-batches.json"), []byte("[]\n"), 0o644))
	manifest := `{"protocol":"multi-agent-groupmembench-v3","domain_session_batches":"domain/producer/session-batches.json","full_domain_messages":2,"dataset_revision":"revision","cases":[{"id":"case","question":"q","answer":"a","asking_user_id":"User_1","scope_id":"domain","participant_agent_ids":["groupmembench-User_1","groupmembench-User_2"]}]}`
	path := filepath.Join(directory, "manifest.json")
	s.Require().NoError(os.WriteFile(path, []byte(manifest), 0o644))

	cases, revision, err := v3.LoadCases(path, "seed")

	s.Require().NoError(err)
	s.Equal("revision", revision)
	s.Require().Len(cases, 1)
	s.Equal("groupmembench-User_2", cases[0].AnsweringAgentID)
	s.Equal("seed", cases[0].AnswererSeed)
	s.Equal(v3.SourceOverlapUnknown, cases[0].AnswererSourceOverlap)
}

func (s *protocolSuite) TestLoadCasesRejectsQuestionConditionedV2Manifest() {
	directory := s.T().TempDir()
	path := filepath.Join(directory, "manifest.json")
	manifest := `{"dataset_revision":"revision","cases":[{"id":"case","question":"q","answer":"a","asking_user_id":"User_1","scope_id":"domain","participant_agent_ids":["groupmembench-User_1","groupmembench-User_2"]}]}`
	s.Require().NoError(os.WriteFile(path, []byte(manifest), 0o644))

	_, _, err := v3.LoadCases(path, "seed")

	s.Error(err)
}

func (s *protocolSuite) TestAssignAnswerersMarksCaseWhenStrictSelectionIsImpossible() {
	cases := []v2.Case{{
		ID: "case", AskingUserID: "User 1",
		ParticipantAgentIDs: []string{"groupmembench-User-1", "groupmembench-User-2"},
		SupportingAgentIDs:  []string{"groupmembench-User-2"},
	}}

	assigned, err := v3.AssignAnswerers(cases, "revision", "seed")

	s.Require().NoError(err)
	s.Require().Len(assigned, 1)
	s.Empty(assigned[0].AnsweringAgentID)
	s.Equal(v3.SourceOverlapNoEligible, assigned[0].AnswererSourceOverlap)
}

func baseConfig() v2.Config {
	return v2.Config{
		Version: v3.ConfigVersion,
		Run:     v2.RunConfig{ID: "run", Dataset: "GroupMemBench", Manifest: "manifest.json", OutputDir: "out", Parallelism: 1},
		Store:   v2.StoreConfig{DSNEnv: "DSN"}, BaselineArm: v3.ArmNoMemoryTeam,
		TrialTimeout: "1m", AnswererSeed: "seed-1", Mem0ReproductionLevel: v3.ReproductionComparable,
		Arms: architectureArms(), BeforeRun: &v2.CommandSpec{Program: "ingest-domain"},
	}
}

func architectureArms() []v2.ArmConfig {
	return []v2.ArmConfig{
		{Name: v3.ArmNoMemoryTeam, Consumer: command()},
		{Name: v3.ArmGroupMemBenchMem0, Consumer: command()},
		{Name: v3.ArmPrivateSQLiteTeamNote, Consumer: command()},
	}
}

func command() v2.CommandSpec { return v2.CommandSpec{Program: "consumer"} }
