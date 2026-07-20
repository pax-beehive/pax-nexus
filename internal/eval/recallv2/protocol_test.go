package recallv2_test

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallv2"
	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/stretchr/testify/suite"
)

type protocolSuite struct{ suite.Suite }

func TestProtocolSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(protocolSuite))
}

func (s *protocolSuite) TestValidateConfigMatrix() {
	tests := []struct {
		name    string
		mutate  func(*recallv2.Config)
		wantErr string
	}{
		{name: "valid"},
		{name: "judge required", mutate: func(config *recallv2.Config) { config.Judge = nil }, wantErr: "judge"},
		{name: "replay required", mutate: func(config *recallv2.Config) { config.Recall.DeterministicReplay = "" }, wantErr: "deterministic_replay"},
		{name: "hint replay required", mutate: func(config *recallv2.Config) { config.Recall.HintReplay = "" }, wantErr: "hint_replay"},
		{name: "annotations required", mutate: func(config *recallv2.Config) { config.Recall.CaseAnnotations = "" }, wantErr: "case_annotations"},
		{name: "baseline fixed", mutate: func(config *recallv2.Config) { config.BaselineArm = recallv2.ArmTeamNote }, wantErr: "baseline_arm"},
		{name: "diagnostic pilot accepts fifteen cases", mutate: func(config *recallv2.Config) {
			config.Recall.Diagnostic = true
			config.Recall.MinAgentCases = 15
			config.Recall.MaxAgentCases = 15
		}},
		{name: "diagnostic pilot rejects sixteen cases", mutate: func(config *recallv2.Config) {
			config.Recall.Diagnostic = true
			config.Recall.MinAgentCases = 16
			config.Recall.MaxAgentCases = 16
		}, wantErr: "diagnostic agent cohort"},
		{name: "production arm required", mutate: func(config *recallv2.Config) { config.Arms[1].Name = "memory_mock" }, wantErr: "arms"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			config := validConfig()
			if test.mutate != nil {
				test.mutate(&config)
			}
			err := recallv2.Validate(config)
			if test.wantErr == "" {
				s.NoError(err)
				return
			}
			s.ErrorContains(err, test.wantErr)
		})
	}
}

func (s *protocolSuite) TestAuditRequiresRealAgentRecallAndJudgeEvidence() {
	results := []v2.TrialResult{
		completed("case-1", recallv2.ArmNoMemory),
		completed("case-1", recallv2.ArmTeamNote),
		completed("case-1", recallv2.ArmHintRecall),
		completed("case-2", recallv2.ArmNoMemory),
		completed("case-2", recallv2.ArmTeamNote),
		completed("case-2", recallv2.ArmHintRecall),
	}
	results[0].Correct = false
	results[1].Correct = true
	results[2].Correct = true
	for index := range results {
		results[index].Category = "temporal"
		results[index].TemporalMode = "temporal_question"
		results[index].KnowledgeSourceStatus = "reviewed"
		results[index].StrictCrossAgent = true
	}
	results[4].MemoryRecallObserved = false

	report := recallv2.Audit(results, 2)

	s.Equal(6, report.ExpectedTrials)
	s.Equal(5, report.ScoredTrials)
	s.Equal(1, report.UnscoredTrials)
	s.Equal(1, report.UnscoredReasons[recallv2.ReasonRecallNotObserved])
	s.Equal(1, report.PairedCases)
	s.Equal(1, report.CandidateWins)
	s.InDelta(1.0, report.CandidateAccuracy, 0.0001)
	s.InDelta(0.0, report.ActiveRecallRate, 0.0001)
	s.InDelta(1.0, report.CategorySlices["temporal"].AccuracyDelta, 0.0001)
	s.Empty(report.Regressions)
}

func (s *protocolSuite) TestAuditRequiresActiveRecallInstrumentationForHintArm() {
	result := completed("case", recallv2.ArmHintRecall)
	result.ActiveRecallObserved = false

	report := recallv2.Audit([]v2.TrialResult{result}, 1)

	s.Equal(1, report.UnscoredReasons[recallv2.ReasonActiveRecallMissing])
}

func (s *protocolSuite) TestAuditNamesSliceRegression() {
	baseline := completed("case", recallv2.ArmNoMemory)
	candidate := completed("case", recallv2.ArmTeamNote)
	baseline.Category, candidate.Category = "temporal", "temporal"
	baseline.TemporalMode, candidate.TemporalMode = "as_of", "as_of"
	baseline.KnowledgeSourceStatus, candidate.KnowledgeSourceStatus = "reviewed", "reviewed"
	baseline.StrictCrossAgent, candidate.StrictCrossAgent = true, true
	baseline.Correct = true

	report := recallv2.Audit([]v2.TrialResult{baseline, candidate}, 1)

	s.Contains(report.Regressions, "category:temporal")
	s.Contains(report.Regressions, "temporal:as_of")
	s.Contains(report.Regressions, "identity:strict_cross_agent")
	s.Contains(report.Regressions, "knowledge_source:reviewed")
}

func (s *protocolSuite) TestAuditRejectsMissingSessionProviderCallAndJudgeSession() {
	tests := []struct {
		name   string
		mutate func(*v2.TrialResult)
		reason string
	}{
		{name: "session", mutate: func(result *v2.TrialResult) { result.SessionID = "" }, reason: recallv2.ReasonAgentSessionMissing},
		{name: "provider call", mutate: func(result *v2.TrialResult) { result.MemoryRecallProviderCalls = 0 }, reason: recallv2.ReasonProviderCallMissing},
		{name: "team provider", mutate: func(result *v2.TrialResult) { result.MemoryRecallProviderType = "mem0" }, reason: recallv2.ReasonTeamProviderMissing},
		{name: "judge", mutate: func(result *v2.TrialResult) { result.Judged = false }, reason: recallv2.ReasonJudgeMissing},
		{name: "judge session", mutate: func(result *v2.TrialResult) { result.JudgeSessionID = "" }, reason: recallv2.ReasonJudgeSessionMissing},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			result := completed("case", recallv2.ArmTeamNote)
			test.mutate(&result)
			report := recallv2.Audit([]v2.TrialResult{result}, 1)
			s.Equal(1, report.UnscoredReasons[test.reason])
		})
	}
}

func (s *protocolSuite) TestAuditRejectsRecallContaminatedNoMemoryArm() {
	result := completed("case", recallv2.ArmNoMemory)
	result.MemoryRecallObserved = true
	result.MemoryRecallProviderCalls = 1
	result.MemoryRecallProviders = map[string]int{"team": 1}

	report := recallv2.Audit([]v2.TrialResult{result}, 1)

	s.Equal(1, report.UnscoredReasons[recallv2.ReasonBaselineRecallLeak])
}

func validConfig() recallv2.Config {
	judge := v2.CommandSpec{Program: "judge"}
	before := v2.CommandSpec{Program: "ingest"}
	return recallv2.Config{
		Config: v2.Config{
			Version: recallv2.ConfigVersion,
			Run:     v2.RunConfig{ID: "run", Dataset: "dataset", Manifest: "manifest.json", OutputDir: "out", Parallelism: 1},
			Store:   v2.StoreConfig{DSNEnv: "DSN"}, BaselineArm: recallv2.ArmNoMemory, TrialTimeout: "1m",
			BeforeRun: &before, Judge: &judge,
			AnswererSeed: "recall-v2-seed",
			Arms: []v2.ArmConfig{
				{Name: recallv2.ArmNoMemory, Consumer: v2.CommandSpec{Program: "consumer"}},
				{Name: recallv2.ArmTeamNote, Consumer: v2.CommandSpec{Program: "consumer"}},
				{Name: recallv2.ArmHintRecall, Consumer: v2.CommandSpec{Program: "consumer"}},
			},
		},
		Recall: recallv2.ProtocolConfig{DeterministicReplay: "replay.json", HintReplay: "hint.json", CaseAnnotations: "annotations.json", MinReplayCases: 30, MinAgentCases: 30, MaxAgentCases: 50},
	}
}

func completed(caseID, arm string) v2.TrialResult {
	return v2.TrialResult{
		CaseID: caseID, Arm: arm, Status: "completed", SessionID: "agent-session-" + caseID + "-" + arm,
		Judged: true, JudgeSessionID: "judge-session-" + caseID + "-" + arm,
		MemoryRecallObserved:      arm == recallv2.ArmTeamNote || arm == recallv2.ArmHintRecall,
		MemoryRecallSuccess:       arm == recallv2.ArmTeamNote || arm == recallv2.ArmHintRecall,
		MemoryRecallProviderCalls: boolInt(arm == recallv2.ArmTeamNote || arm == recallv2.ArmHintRecall),
		MemoryRecallProviderType: func() string {
			if arm == recallv2.ArmTeamNote || arm == recallv2.ArmHintRecall {
				return "team-memory"
			}
			return ""
		}(),
		ActiveRecallObserved: arm == recallv2.ArmHintRecall, ActiveRecallSuccess: arm == recallv2.ArmHintRecall,
		MemoryRecallProviders: func() map[string]int {
			if arm == recallv2.ArmTeamNote || arm == recallv2.ArmHintRecall {
				return map[string]int{"team": 1}
			}
			return nil
		}(),
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
