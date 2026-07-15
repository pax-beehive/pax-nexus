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
	}

	previousCalls := executor.total()
	s.Require().NoError(runner.Run(context.Background(), config, cases, "revision"))
	s.Equal(previousCalls+2, executor.total())
}

func (s *runnerSuite) TestTrialFailureIsPersistedWithoutStoppingOtherTrials() {
	store := newFakeStore()
	executor := &fakeExecutor{failProgram: "producer"}
	runner, err := NewRunner(store, executor, nil)
	s.Require().NoError(err)
	config := testConfig(s.T().TempDir())
	cases := []Case{{ID: "case", Category: "temporal", Question: "q", Expected: "answer", AskingUserID: "user"}}
	s.Require().NoError(runner.Run(context.Background(), config, cases, "revision"))
	s.Len(store.results, 2)
	statuses := map[string]string{}
	for _, result := range store.results {
		statuses[result.Arm] = result.Status
	}
	s.Equal("completed", statuses["control"])
	s.Equal("failed", statuses["memory"])
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
	if spec.Program == e.failProgram {
		return CommandResult{}, errors.New("command failed")
	}
	if spec.Program == "consumer" {
		return CommandResult{Output: []byte(`{"type":"text","sessionID":"session","part":{"text":"answer"}}` + "\n"), Duration: time.Millisecond}, nil
	}
	return CommandResult{Duration: time.Millisecond}, nil
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
