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

func (s *runnerSuite) TestRunCompletesResumableMatrixAndExports() {
	store := newFakeStore()
	executor := &fakeExecutor{}
	runner, err := NewRunner(store, executor, slog.New(slog.DiscardHandler))
	s.Require().NoError(err)
	runner.now = func() time.Time { return time.Unix(10, 0).UTC() }
	config := testConfig(s.T().TempDir())
	config.BeforeRun = &CommandSpec{Program: "setup"}
	config.AfterRun = &CommandSpec{Program: "teardown"}
	cases := []Case{{ID: "case-1", Category: "temporal", Question: "question", Expected: "answer", AskingUserID: "same-user", ScopeID: "scope"}}

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
	mu          sync.Mutex
	programs    []string
	failProgram string
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
