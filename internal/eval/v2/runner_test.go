package v2

import (
	"context"
	"errors"
	"log/slog"
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

	s.Require().NoError(runner.Run(context.Background(), config, cases, "revision"))
	s.True(store.finished)
	s.Len(store.results, 2)
	s.Equal(1, executor.count("setup"))
	s.Equal(1, executor.count("teardown"))
	s.Equal(1, executor.count("producer"))
	s.Equal(2, executor.count("consumer"))
	s.FileExists(filepath.Join(config.Run.OutputDir, "summary.csv"))
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
	s.Require().NoError(runner.Run(context.Background(), config, cases, "revision"))
	s.Equal(previousCalls+2, executor.total())
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
			s.Require().NoError(runner.Run(context.Background(), config, cases, "revision"))
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
	err = runner.Run(context.Background(), config, []Case{{ID: "case", Category: "temporal", Question: "q", Expected: "answer", AskingUserID: "user"}}, "revision")
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
			return runner.Run(context.Background(), testConfig(s.T().TempDir()), nil, "revision")
		}},
		{name: "invalid config", run: func() error {
			runner, err := NewRunner(newFakeStore(), &fakeExecutor{}, nil)
			if err != nil {
				return err
			}
			config := testConfig(s.T().TempDir())
			config.Version = "bad"
			return runner.Run(context.Background(), config, []Case{{ID: "case"}}, "revision")
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() { s.Require().Error(test.run()) })
	}
}

type fakeStore struct {
	mu       sync.Mutex
	statuses map[TrialKey]string
	results  []TrialResult
	finished bool
	acquired bool
}

func newFakeStore() *fakeStore { return &fakeStore{statuses: make(map[TrialKey]string)} }

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

func (s *fakeStore) Claim(_ context.Context, key TrialKey, retry bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.statuses[key]
	if status != "pending" && (!retry || status != "failed") {
		return false, nil
	}
	s.statuses[key] = "running"
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

func (e *fakeExecutor) Execute(_ context.Context, spec CommandSpec, _ map[string]string, _, _ string) (CommandResult, error) {
	e.mu.Lock()
	e.programs = append(e.programs, spec.Program)
	e.mu.Unlock()
	var result CommandResult
	if spec.Program == "producer" {
		result = CommandResult{Output: []byte(`{"type":"text","sessionID":"producer-session","part":{"text":"handoff"}}` + "\n" + `{"type":"step_finish","part":{"cost":0.2,"tokens":{"input":3,"output":1}}}` + "\n"), Duration: time.Millisecond}
	}
	if spec.Program == "consumer" {
		result = CommandResult{Output: []byte(`{"type":"text","sessionID":"session","part":{"text":"answer"}}` + "\n" + `{"type":"step_finish","part":{"cost":0.1,"tokens":{"input":4,"output":2}}}` + "\n"), Duration: time.Millisecond}
	}
	if result.Duration == 0 {
		result.Duration = time.Millisecond
	}
	if spec.Program == e.failProgram {
		return result, errors.New("command failed")
	}
	return result, nil
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
