package v2

import (
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/harness"
	"github.com/stretchr/testify/suite"
)

type modelSuite struct{ suite.Suite }

func TestModelSuite(t *testing.T) { suite.Run(t, new(modelSuite)) }

func (s *modelSuite) TestValidationMatrix() {
	valid := testConfig(s.T().TempDir())
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "version", mutate: func(config *Config) { config.Version = "v1" }},
		{name: "run id", mutate: func(config *Config) { config.Run.ID = "" }},
		{name: "parallelism", mutate: func(config *Config) { config.Run.Parallelism = 0 }},
		{name: "dsn env", mutate: func(config *Config) { config.Store.DSNEnv = "" }},
		{name: "timeout syntax", mutate: func(config *Config) { config.TrialTimeout = "soon" }},
		{name: "timeout positive", mutate: func(config *Config) { config.TrialTimeout = "0s" }},
		{name: "arms", mutate: func(config *Config) { config.Arms = config.Arms[:1] }},
		{name: "baseline", mutate: func(config *Config) { config.BaselineArm = "missing" }},
		{name: "duplicate arm", mutate: func(config *Config) { config.Arms[1].Name = config.Arms[0].Name }},
		{name: "arm name", mutate: func(config *Config) { config.Arms[1].Name = "" }},
		{name: "consumer", mutate: func(config *Config) { config.Arms[1].Consumer.Program = "" }},
		{name: "wait without producer", mutate: func(config *Config) { config.Arms[0].AfterProducer = &CommandSpec{Program: "wait"} }},
		{name: "lifecycle", mutate: func(config *Config) { config.BeforeRun = &CommandSpec{} }},
		{name: "secret runtime", mutate: func(config *Config) { config.RuntimeEnv = []string{"API_KEY"} }},
		{name: "invalid runtime", mutate: func(config *Config) { config.RuntimeEnv = []string{"BAD NAME"} }},
		{name: "output format", mutate: func(config *Config) { config.Output.Formats = []string{"html"} }},
		{name: "duplicate output", mutate: func(config *Config) { config.Output.Formats = []string{"csv", "csv"} }},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			config := valid
			config.Arms = append([]ArmConfig(nil), valid.Arms...)
			test.mutate(&config)
			s.Error(config.Validate())
		})
	}
	s.Require().NoError(valid.Validate())
	firstHash, err := valid.Hash()
	s.Require().NoError(err)
	secondHash, err := valid.Hash()
	s.Require().NoError(err)
	s.Equal(firstHash, secondHash)
	s.Equal([]string{"csv", "jsonl"}, valid.OutputFormats())
	valid.Output.Formats = []string{"csv"}
	s.Equal([]string{"csv"}, valid.OutputFormats())
	valid.RuntimeEnv = []string{"MODEL"}
	runtimeValues, err := valid.ResolveRuntime(func(string) string { return "v1" })
	s.Require().NoError(err)
	s.Equal(map[string]string{"MODEL": "v1"}, runtimeValues)
	_, err = valid.ResolveRuntime(func(string) string { return "" })
	s.Require().Error(err)
	runtimeHash, err := valid.HashWithRuntime(runtimeValues)
	s.Require().NoError(err)
	s.NotEqual(firstHash, runtimeHash)
}

func (s *modelSuite) TestScoreResult() {
	started := time.Now().Add(-time.Second).UTC()
	result := ScoreResult(
		RunRecord{ID: "run", Dataset: "suite", DatasetRevision: "revision"},
		Case{ID: "case", Category: "temporal", Expected: "answer", AskingUserID: "user"},
		"memory",
		harness.AgentOutput{Text: "handoff", InputTokens: 3, OutputTokens: 1, Cost: 0.2},
		harness.AgentOutput{Text: "answer", SessionID: "session", InputTokens: 4, OutputTokens: 2, Cost: 0.1},
		started, [3]time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond},
	)
	s.True(result.Exact)
	s.InDelta(1, result.TokenF1, 0.000001)
	s.Equal(int64(1), result.ProducerDurationMS)
	s.Equal(int64(2), result.ReadinessDurationMS)
	s.Equal(int64(3), result.ConsumerDurationMS)
	s.Equal("user", result.AskingUserID)
	s.Equal(7, result.InputTokens)
	s.Equal(3, result.OutputTokens)
	s.InDelta(0.3, result.Cost, 0.000001)
	s.InDelta(0.2, result.ProducerCost, 0.000001)
	s.InDelta(0.1, result.ConsumerCost, 0.000001)
	s.Equal("opencode_reported", result.CostScope)
}

func testConfig(output string) Config {
	return Config{
		Version: ConfigVersion, Run: RunConfig{ID: "run", Dataset: "suite", Manifest: "manifest.json", OutputDir: output, Parallelism: 2},
		Store: StoreConfig{DSNEnv: "EVAL_DSN"}, BaselineArm: "control", TrialTimeout: "1m",
		Arms: []ArmConfig{
			{Name: "control", Consumer: CommandSpec{Program: "consumer"}},
			{Name: "memory", Producer: &CommandSpec{Program: "producer"}, Consumer: CommandSpec{Program: "consumer"}},
		},
	}
}
