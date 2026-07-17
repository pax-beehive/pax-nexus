package v2

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type runnerSuite struct{ suite.Suite }

func TestRunnerSuite(t *testing.T) { suite.Run(t, new(runnerSuite)) }

func (s *runnerSuite) TestTrialVariablesExposeRunOutputDirectory() {
	outputDirectory := s.T().TempDir()
	variables := trialVariables(RunRecord{ID: "run"}, Case{ID: "case"}, "arm", outputDirectory)

	s.Equal(outputDirectory, variables["output_dir"])
}

func (s *runnerSuite) TestRunCompletesResumableMatrixAndExports() {
	store := newFakeStore()
	executor := &fakeExecutor{}
	runner, err := NewRunner(store, executor, slog.New(slog.DiscardHandler))
	s.Require().NoError(err)
	runner.now = func() time.Time { return time.Unix(10, 0).UTC() }
	config := testConfig(s.T().TempDir())
	config.BeforeRun = &CommandSpec{Program: "setup"}
	config.AfterRun = &CommandSpec{Program: "teardown"}
	cases := []Case{{
		ID: "case-1", Category: "temporal", Question: "question", Expected: "answer", AskingUserID: "same-user", ScopeID: "scope",
		AnsweringAgentID: "groupmembench-User_3", AnswererSeed: "seed-1", StrictCrossAgent: true,
		AnswererSourceOverlap: "excluded",
	}}

	run, results, err := runner.Run(context.Background(), config, cases, "revision")
	s.Require().NoError(err)
	s.Equal("run", run.ID)
	s.Len(results, 2)
	s.True(store.finished)
	s.Len(store.results, 2)
	s.Equal(1, executor.count("setup"))
	s.Equal(1, executor.count("teardown"))
	s.Equal(1, executor.count("producer"))
	s.Equal(2, executor.count("consumer"))
	for _, result := range store.results {
		s.Equal("same-user", result.AskingUserID)
		s.Equal("groupmembench-User_3", result.AnsweringAgentID)
		s.Equal("seed-1", result.AnswererSeed)
		s.True(result.StrictCrossAgent)
		s.Equal("excluded", result.AnswererSourceOverlap)
		s.True(result.Exact)
		if result.Arm == "memory" {
			s.InDelta(0.3, result.Cost, 0.000001)
		} else {
			s.InDelta(0.1, result.Cost, 0.000001)
		}
	}

	previousCalls := executor.total()
	_, resumed, err := runner.Run(context.Background(), config, cases, "revision")
	s.Require().NoError(err)
	s.Require().Len(resumed, 2)
	s.Equal("question", resumed[0].Question)
	s.Equal(previousCalls+2, executor.total())
}

func (s *runnerSuite) TestRunJudgesCompletedAnswers() {
	store := newFakeStore()
	executor := &fakeExecutor{}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.Judge = &CommandSpec{Program: "judge"}

	_, results, err := runner.Run(context.Background(), config, []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().NoError(err)
	s.Len(results, 2)
	s.Equal(2, executor.count("judge"))
	for _, result := range results {
		s.True(result.Judged)
		s.True(result.Correct)
		s.Contains(result.JudgeAnswer, "Final: Correct")
		s.Equal(7, result.JudgeInputTokens)
		s.Equal(3, result.JudgeOutputTokens)
		s.InDelta(0.05, result.JudgeCost, 0.000001)
		s.Equal(int64(1), result.JudgeDurationMS)
	}
}

func (s *runnerSuite) TestJudgeFailurePreservesCompletedConsumerResultAndFailsRun() {
	store := newFakeStore()
	runner, err := NewRunner(store, &fakeExecutor{failProgram: "judge"}, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.Judge = &CommandSpec{Program: "judge"}

	_, results, err := runner.Run(context.Background(), config, []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().ErrorContains(err, "completed trials remain unjudged")
	s.Require().Len(results, 2)
	for _, result := range results {
		s.Equal("completed", result.Status)
		s.False(result.Judged)
		s.Contains(result.JudgeError, "run judge")
	}
}

func (s *runnerSuite) TestJudgeExistingRunOnlyJudgesMissingCompletedAnswers() {
	executor := &fakeExecutor{}
	config := testConfig(s.T().TempDir())
	config.Judge = &CommandSpec{Program: "judge"}
	results := []TrialResult{
		{RunID: "run", CaseID: "new", Arm: "control", Status: "completed", Question: "q", Expected: "answer", Answer: "answer"},
		{RunID: "run", CaseID: "done", Arm: "control", Status: "completed", Judged: true, Correct: false},
		{RunID: "run", CaseID: "failed", Arm: "control", Status: "failed"},
	}

	run, judged, err := JudgeExistingRun(context.Background(), executor, nil, config, "revision", results)
	s.Require().NoError(err)
	s.Equal("run", run.ID)
	s.Equal(1, executor.count("judge"))
	s.True(judged[0].Judged)
	s.True(judged[0].Correct)
	s.True(judged[1].Judged)
	s.False(judged[1].Correct)
	s.False(judged[2].Judged)
}

func (s *runnerSuite) TestRunPreflightsOnlyWhenWorkIsRunnable() {
	store := newFakeStore()
	executor := &fakeExecutor{}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.Preflight = &CommandSpec{Program: "preflight"}
	cases := []Case{{ID: "case", Category: "temporal", Question: "q", Expected: "answer", AskingUserID: "user"}}

	_, _, err = runner.Run(context.Background(), config, cases, "revision")
	s.Require().NoError(err)
	_, _, err = runner.Run(context.Background(), config, cases, "revision")
	s.Require().NoError(err)
	s.Equal(1, executor.count("preflight"))
}

func (s *runnerSuite) TestPreflightFailureStopsBeforePaidTrials() {
	store := newFakeStore()
	executor := &fakeExecutor{failProgram: "preflight"}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.Preflight = &CommandSpec{Program: "preflight"}

	_, _, err = runner.Run(context.Background(), config, []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().ErrorContains(err, "preflight eval run")
	s.Zero(executor.count("producer"))
	s.Zero(executor.count("consumer"))
}

func (s *runnerSuite) TestSharedProducerRunsOnceAndFeedsBothMemoryArms() {
	store := newFakeStore()
	executor := &fakeExecutor{}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.SharedProducer = sharedProducerCommand()
	config.Arms[1].Producer = nil
	config.Arms[1].Ingest = &CommandSpec{Program: "ingest"}
	config.Arms = append(config.Arms, ArmConfig{Name: "memory-2", Ingest: &CommandSpec{Program: "ingest"}, Consumer: CommandSpec{Program: "consumer"}})

	_, results, err := runner.Run(context.Background(), config, []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().NoError(err)
	s.Len(results, 3)
	s.Equal(1, executor.count("producer"))
	s.Equal(2, executor.count("ingest"))
	s.Equal(3, executor.count("consumer"))
	for _, result := range results {
		if result.Arm != "control" {
			s.Zero(result.ProducerCost)
		}
	}
	_, err = os.Stat(filepath.Join(config.Run.OutputDir, "trials", "case", "shared", "producer.txt"))
	s.Require().NoError(err)
}

func (s *runnerSuite) TestNativeIngestRunsWithoutSharedProducer() {
	store := newFakeStore()
	executor := &fakeExecutor{}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.Arms[1].Producer = nil
	config.Arms[1].Ingest = &CommandSpec{Program: "ingest"}

	_, results, err := runner.Run(context.Background(), config, []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().NoError(err)
	s.Len(results, 2)
	s.Zero(executor.count("producer"))
	s.Equal(1, executor.count("ingest"))
	s.Equal(2, executor.count("consumer"))
	s.Zero(findResult(results, "memory").ProducerDurationMS)
}

func (s *runnerSuite) TestRunPersistsMemoryIngestNoOpReceipt() {
	store := newFakeStore()
	executor := &fakeExecutor{ingestOutput: []byte(`{"provider":"mem0","accepted":0,"duplicate":0,"created":0,"updated":0,"deleted":0,"noop_known":true,"noop":true}`)}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.SharedProducer = sharedProducerCommand()
	config.Arms[1].Producer = nil
	config.Arms[1].Ingest = &CommandSpec{Program: "ingest"}

	_, _, err = runner.Run(context.Background(), config, []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().NoError(err)
	result := findResult(store.results, "memory")
	s.Equal("mem0", result.MemoryIngestProvider)
	s.True(result.MemoryIngestNoOpKnown)
	s.True(result.MemoryIngestNoOp)
	s.Zero(result.MemoryIngestCreated)
}

func (s *runnerSuite) TestSharedProducerFailureIsReusedAcrossDependentArms() {
	store := newFakeStore()
	executor := &fakeExecutor{failProgram: "producer"}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.SharedProducer = sharedProducerCommand()
	config.Arms[1].Producer = nil
	config.Arms[1].Ingest = &CommandSpec{Program: "ingest"}
	config.Arms = append(config.Arms, ArmConfig{Name: "memory-2", Ingest: &CommandSpec{Program: "ingest"}, Consumer: CommandSpec{Program: "consumer"}})

	_, _, err = runner.Run(context.Background(), config, []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().NoError(err)
	s.Equal(1, executor.count("producer"))
	s.Equal("completed", findResult(store.results, "control").Status)
	s.Equal("failed", findResult(store.results, "memory").Status)
	s.Equal("failed", findResult(store.results, "memory-2").Status)
}

func (s *runnerSuite) TestBoundedRetryReusesPersistedSharedProducer() {
	store := newFakeStore()
	executor := &fakeExecutor{failProgram: "consumer"}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.SharedProducer = sharedProducerCommand()
	config.Arms[1].Producer = nil
	config.Arms[1].Ingest = &CommandSpec{Program: "ingest"}
	config.RetryFailed = true
	config.RetryMaxAttempts = 2
	cases := []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}

	_, _, err = runner.Run(context.Background(), config, cases, "revision")
	s.Require().NoError(err)
	_, _, err = runner.Run(context.Background(), config, cases, "revision")
	s.Require().NoError(err)
	s.Equal(1, executor.count("producer"))
	s.Equal(4, executor.count("consumer"))
}

func (s *runnerSuite) TestSharedProducerRecoversPlainTextFromCompletedJSONL() {
	store := newFakeStore()
	executor := &fakeExecutor{}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.SharedProducer = sharedProducerCommand()
	config.Arms[1].Producer = nil
	config.Arms[1].Ingest = &CommandSpec{Program: "ingest"}
	sharedDirectory := filepath.Join(config.Run.OutputDir, "trials", "case", "shared")
	s.Require().NoError(os.MkdirAll(sharedDirectory, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(sharedDirectory, "producer.jsonl"), producerJSONL(), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(sharedDirectory, "producer.command-success"), []byte("success\n"), 0o644))

	_, _, err = runner.Run(context.Background(), config, []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().NoError(err)
	s.Zero(executor.count("producer"))
	text, err := os.ReadFile(filepath.Join(sharedDirectory, "producer.txt"))
	s.Require().NoError(err)
	s.Equal("handoff", string(text))
	_, err = os.Stat(filepath.Join(sharedDirectory, "producer.complete"))
	s.Require().NoError(err)
}

func (s *runnerSuite) TestBoundedRetryReplacesIncompleteSharedProducerArtifact() {
	store := newFakeStore()
	executor := &fakeExecutor{failProgram: "producer"}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.SharedProducer = sharedProducerCommand()
	config.Arms[1].Producer = nil
	config.Arms[1].Ingest = &CommandSpec{Program: "ingest"}
	config.RetryFailed = true
	config.RetryMaxAttempts = 2
	cases := []Case{{ID: "case", Question: "q", Expected: "answer", AskingUserID: "user"}}

	_, _, err = runner.Run(context.Background(), config, cases, "revision")
	s.Require().NoError(err)
	executor.failProgram = ""
	_, _, err = runner.Run(context.Background(), config, cases, "revision")
	s.Require().NoError(err)
	s.Equal(2, executor.count("producer"))
	s.Equal("completed", findResult(store.results, "memory").Status)
}

func (s *runnerSuite) TestFailureCostMatrix() {
	tests := []struct {
		name             string
		failProgram      string
		configure        func(*Config)
		wantMemoryCost   float64
		wantConsumerCost float64
	}{
		{name: "producer failure", failProgram: "producer", configure: func(*Config) {}, wantMemoryCost: 0.2},
		{name: "readiness failure", failProgram: "wait", configure: func(config *Config) {
			config.Arms[1].AfterProducer = &CommandSpec{Program: "wait"}
		}, wantMemoryCost: 0.2},
		{name: "consumer failure", failProgram: "consumer", configure: func(*Config) {}, wantMemoryCost: 0.3, wantConsumerCost: 0.1},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			store := newFakeStore()
			runner, err := NewRunner(store, &fakeExecutor{failProgram: test.failProgram}, nil)
			s.Require().NoError(err)
			config := testConfig(s.T().TempDir())
			test.configure(&config)
			cases := []Case{{ID: "case", Category: "temporal", Question: "q", Expected: "answer", AskingUserID: "user"}}
			_, _, runErr := runner.Run(context.Background(), config, cases, "revision")
			s.Require().NoError(runErr)
			memory := findResult(store.results, "memory")
			s.Equal("failed", memory.Status)
			s.InDelta(test.wantMemoryCost, memory.Cost, 0.000001)
			s.InDelta(0.2, memory.ProducerCost, 0.000001)
			s.InDelta(test.wantConsumerCost, memory.ConsumerCost, 0.000001)
		})
	}
}

func findResult(results []TrialResult, arm string) TrialResult {
	for _, result := range results {
		if result.Arm == arm {
			return result
		}
	}
	return TrialResult{}
}

func (s *runnerSuite) TestTeardownFailureIsReturned() {
	store := newFakeStore()
	executor := &fakeExecutor{failProgram: "teardown"}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	config.AfterRun = &CommandSpec{Program: "teardown"}
	_, _, err = runner.Run(context.Background(), config, []Case{{ID: "case", Category: "temporal", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
	s.Require().Error(err)
	s.Contains(err.Error(), "tear down eval run")
}

func (s *runnerSuite) TestConstructionAndRunErrors() {
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "missing store", run: func() error { _, err := NewRunner(nil, &fakeExecutor{}, nil); return err }},
		{name: "missing executor", run: func() error { _, err := NewRunner(newFakeStore(), nil, nil); return err }},
		{name: "missing cases", run: func() error {
			runner, err := NewRunner(newFakeStore(), &fakeExecutor{}, nil)
			if err != nil {
				return err
			}
			_, _, err = runner.Run(context.Background(), testConfig(s.T().TempDir()), nil, "revision")
			return err
		}},
		{name: "invalid config", run: func() error {
			runner, err := NewRunner(newFakeStore(), &fakeExecutor{}, nil)
			if err != nil {
				return err
			}
			config := testConfig(s.T().TempDir())
			config.Version = "bad"
			_, _, err = runner.Run(context.Background(), config, []Case{{ID: "case"}}, "revision")
			return err
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() { s.Require().Error(test.run()) })
	}
}

type fakeStore struct {
	mu       sync.Mutex
	statuses map[TrialKey]string
	attempts map[TrialKey]int
	results  []TrialResult
	finished bool
	acquired bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{statuses: make(map[TrialKey]string), attempts: make(map[TrialKey]int)}
}

func (s *fakeStore) Acquire(context.Context, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acquired {
		return false, nil
	}
	s.acquired = true
	return true, nil
}

func (s *fakeStore) Release(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquired = false
	return nil
}

func (s *fakeStore) Initialize(_ context.Context, _ RunRecord, trials []TrialKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range trials {
		if _, exists := s.statuses[key]; !exists {
			s.statuses[key] = "pending"
		}
	}
	return nil
}

func (s *fakeStore) ResetRunning(context.Context, string) error { return nil }

func (s *fakeStore) HasRunnable(_ context.Context, _ string, retry bool, maxAttempts int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, status := range s.statuses {
		if status == "pending" || (retry && status == "failed" && s.attempts[key] < maxAttempts) {
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) Claim(_ context.Context, key TrialKey, retry bool, maxAttempts int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.statuses[key]
	if status != "pending" && (!retry || status != "failed" || s.attempts[key] >= maxAttempts) {
		return false, nil
	}
	s.statuses[key] = "running"
	s.attempts[key]++
	return true, nil
}

func (s *fakeStore) Complete(_ context.Context, result TrialResult) error { return s.record(result) }
func (s *fakeStore) Fail(_ context.Context, result TrialResult) error     { return s.record(result) }

func (s *fakeStore) record(result TrialResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := TrialKey{RunID: result.RunID, CaseID: result.CaseID, Arm: result.Arm}
	s.statuses[key] = result.Status
	for index := range s.results {
		if s.results[index].RunID == result.RunID && s.results[index].CaseID == result.CaseID && s.results[index].Arm == result.Arm {
			s.results[index] = result
			return nil
		}
	}
	s.results = append(s.results, result)
	return nil
}

func (s *fakeStore) Results(context.Context, string) ([]TrialResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TrialResult(nil), s.results...), nil
}

func (s *fakeStore) Finish(context.Context, string) error { s.finished = true; return nil }

type fakeExecutor struct {
	mu           sync.Mutex
	programs     []string
	failProgram  string
	ingestOutput []byte
}

func (e *fakeExecutor) Execute(_ context.Context, spec CommandSpec, _ map[string]string, stdoutPath, _ string) (CommandResult, error) {
	e.mu.Lock()
	e.programs = append(e.programs, spec.Program)
	e.mu.Unlock()
	var result CommandResult
	if spec.Program == "producer" {
		result = CommandResult{Output: producerJSONL(), Duration: time.Millisecond}
	}
	if spec.Program == "consumer" {
		result = CommandResult{Output: []byte(`{"type":"text","sessionID":"session","part":{"text":"answer"}}` + "\n" + `{"type":"step_finish","part":{"cost":0.1,"tokens":{"input":4,"output":2}}}` + "\n"), Duration: time.Millisecond}
	}
	if spec.Program == "ingest" {
		output := e.ingestOutput
		if len(output) == 0 {
			output = []byte(`{"provider":"test","accepted":1,"duplicate":0,"created":1,"updated":0,"deleted":0,"noop":false}`)
		}
		result = CommandResult{Output: append([]byte(nil), output...), Duration: time.Millisecond}
	}
	if spec.Program == "judge" {
		result = CommandResult{Output: []byte(`{"type":"text","sessionID":"judge-session","part":{"text":"The answer matches.\nFinal: Correct"}}` + "\n" + `{"type":"step_finish","part":{"cost":0.05,"tokens":{"input":7,"output":3}}}` + "\n"), Duration: time.Millisecond}
	}
	if result.Duration == 0 {
		result.Duration = time.Millisecond
	}
	if len(result.Output) > 0 {
		if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o755); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(stdoutPath, result.Output, 0o644); err != nil {
			return CommandResult{}, err
		}
	}
	if spec.Program == e.failProgram {
		return result, errors.New("command failed")
	}
	return result, nil
}

func producerJSONL() []byte {
	return []byte(`{"type":"text","sessionID":"producer-session","part":{"text":"handoff"}}` + "\n" + `{"type":"step_finish","part":{"cost":0.2,"tokens":{"input":3,"output":1}}}` + "\n")
}

func sharedProducerCommand() *CommandSpec {
	return &CommandSpec{Program: "producer", SuccessMarker: "{{shared_artifact_dir}}/producer.command-success"}
}

func (e *fakeExecutor) count(program string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, current := range e.programs {
		if current == program {
			count++
		}
	}
	return count
}

func (e *fakeExecutor) total() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.programs)
}
