package knowledgeeval

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"
)

type RunStore interface {
	CreateRun(context.Context, Run) error
	AddTrial(context.Context, Trial) error
	MarkIneligible(context.Context, string, string) error
	StartAttempt(context.Context, string) (Attempt, error)
	AdvanceAttempt(context.Context, string, Stage, string) error
	CompleteAttempt(context.Context, string, BenchmarkResult) error
	FailAttempt(context.Context, string, Stage, error) error
	FinishRun(context.Context, string) error
	ListRuns(context.Context) ([]Run, error)
	GetRun(context.Context, string) (RunDetail, error)
}

type MemoryRunStore struct {
	mu       sync.RWMutex
	now      func() time.Time
	runs     map[string]Run
	trials   map[string]Trial
	attempts map[string]Attempt
	events   []Event
}

func NewMemoryRunStore(now func() time.Time) *MemoryRunStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryRunStore{
		now:      now,
		runs:     make(map[string]Run),
		trials:   make(map[string]Trial),
		attempts: make(map[string]Attempt),
	}
}

func (s *MemoryRunStore) CreateRun(_ context.Context, run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.ID == "" || run.WorldID == "" || run.GroupID == "" || run.CheckpointID == "" {
		return fmt.Errorf("%w: run identity is incomplete", ErrInvalidRecord)
	}
	if _, exists := s.runs[run.ID]; exists {
		return fmt.Errorf("%w: run %s already exists", ErrConflict, run.ID)
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = s.now()
	}
	run.Status = RunStatusPlanned
	run.Metadata = maps.Clone(run.Metadata)
	s.runs[run.ID] = run
	s.appendEvent(run.ID, "", "", StagePlanned, "run planned")
	return nil
}

func (s *MemoryRunStore) AddTrial(_ context.Context, trial Trial) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, exists := s.runs[trial.RunID]
	if !exists {
		return fmt.Errorf("%w: run %s", ErrNotFound, trial.RunID)
	}
	if trial.ID == "" || trial.BenchmarkID == "" || trial.CaseID == "" {
		return fmt.Errorf("%w: trial identity is incomplete", ErrInvalidRecord)
	}
	if _, exists := s.trials[trial.ID]; exists {
		return fmt.Errorf("%w: trial %s already exists", ErrConflict, trial.ID)
	}
	trial.Status = TrialStatusPlanned
	s.trials[trial.ID] = trial
	if run.Status == RunStatusPlanned {
		run.Status = RunStatusRunning
		s.runs[run.ID] = run
	}
	s.appendEvent(run.ID, trial.ID, "", StagePlanned, "trial planned")
	return nil
}

func (s *MemoryRunStore) MarkIneligible(_ context.Context, trialID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	trial, exists := s.trials[trialID]
	if !exists {
		return fmt.Errorf("%w: trial %s", ErrNotFound, trialID)
	}
	if trial.Status != TrialStatusPlanned {
		return fmt.Errorf("%w: trial %s is %s", ErrInvalidTransition, trialID, trial.Status)
	}
	trial.Status = TrialStatusIneligible
	trial.IneligibleReason = reason
	s.trials[trialID] = trial
	s.appendEvent(trial.RunID, trial.ID, "", StageFailed, "trial ineligible: "+reason)
	return nil
}

func (s *MemoryRunStore) StartAttempt(_ context.Context, trialID string) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	trial, exists := s.trials[trialID]
	if !exists {
		return Attempt{}, fmt.Errorf("%w: trial %s", ErrNotFound, trialID)
	}
	if trial.Status != TrialStatusPlanned && trial.Status != TrialStatusFailed {
		return Attempt{}, fmt.Errorf("%w: trial %s is %s", ErrInvalidTransition, trialID, trial.Status)
	}
	number := 1
	for _, attempt := range s.attempts {
		if attempt.TrialID == trialID && attempt.Number >= number {
			number = attempt.Number + 1
		}
	}
	attempt := Attempt{
		ID:        fmt.Sprintf("%s-attempt-%d", trialID, number),
		TrialID:   trialID,
		Number:    number,
		Status:    AttemptStatusRunning,
		Stage:     StagePlanned,
		StartedAt: s.now(),
	}
	s.attempts[attempt.ID] = attempt
	trial.Status = TrialStatusRunning
	s.trials[trialID] = trial
	s.appendEvent(trial.RunID, trial.ID, attempt.ID, StagePlanned, "attempt started")
	return attempt, nil
}

func (s *MemoryRunStore) AdvanceAttempt(
	_ context.Context,
	attemptID string,
	stage Stage,
	message string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, exists := s.attempts[attemptID]
	if !exists {
		return fmt.Errorf("%w: attempt %s", ErrNotFound, attemptID)
	}
	if err := validateStageAdvance(attempt.Stage, stage); err != nil {
		return err
	}
	attempt.Stage = stage
	s.attempts[attemptID] = attempt
	trial := s.trials[attempt.TrialID]
	s.appendEvent(trial.RunID, trial.ID, attempt.ID, stage, message)
	return nil
}

func (s *MemoryRunStore) CompleteAttempt(
	_ context.Context,
	attemptID string,
	result BenchmarkResult,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, exists := s.attempts[attemptID]
	if !exists {
		return fmt.Errorf("%w: attempt %s", ErrNotFound, attemptID)
	}
	if attempt.Status != AttemptStatusRunning {
		return fmt.Errorf("%w: attempt %s is %s", ErrInvalidTransition, attemptID, attempt.Status)
	}
	if err := validateStageAdvance(attempt.Stage, StageCompleted); err != nil {
		return err
	}
	completed := s.now()
	attempt.Status = AttemptStatusCompleted
	attempt.Stage = StageCompleted
	attempt.CompletedAt = completed
	s.attempts[attemptID] = attempt
	trial := s.trials[attempt.TrialID]
	trial.Status = TrialStatusCompleted
	trial.Result = cloneBenchmarkResult(result)
	s.trials[trial.ID] = trial
	s.appendEvent(trial.RunID, trial.ID, attempt.ID, StageCompleted, "attempt completed")
	return nil
}

func (s *MemoryRunStore) FailAttempt(
	_ context.Context,
	attemptID string,
	stage Stage,
	failure error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, exists := s.attempts[attemptID]
	if !exists {
		return fmt.Errorf("%w: attempt %s", ErrNotFound, attemptID)
	}
	if attempt.Status != AttemptStatusRunning {
		return fmt.Errorf("%w: attempt %s is %s", ErrInvalidTransition, attemptID, attempt.Status)
	}
	if stage != StageFailed && stage != attempt.Stage {
		if err := validateStageAdvance(attempt.Stage, stage); err != nil {
			return err
		}
		attempt.Stage = stage
	}
	attempt.Status = AttemptStatusFailed
	attempt.Stage = StageFailed
	attempt.Error = failure.Error()
	attempt.CompletedAt = s.now()
	s.attempts[attemptID] = attempt
	trial := s.trials[attempt.TrialID]
	trial.Status = TrialStatusFailed
	s.trials[trial.ID] = trial
	s.appendEvent(trial.RunID, trial.ID, attempt.ID, StageFailed, failure.Error())
	return nil
}

func (s *MemoryRunStore) FinishRun(_ context.Context, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, exists := s.runs[runID]
	if !exists {
		return fmt.Errorf("%w: run %s", ErrNotFound, runID)
	}
	hasTrial := false
	failed := false
	for _, trial := range s.trials {
		if trial.RunID != runID {
			continue
		}
		hasTrial = true
		switch trial.Status {
		case TrialStatusCompleted, TrialStatusIneligible:
		case TrialStatusFailed:
			failed = true
		default:
			return fmt.Errorf(
				"%w: trial %s is not terminal",
				ErrInvalidTransition,
				trial.ID,
			)
		}
	}
	if !hasTrial {
		return fmt.Errorf("%w: run %s has no trials", ErrInvalidTransition, runID)
	}
	run.Status = RunStatusCompleted
	if failed {
		run.Status = RunStatusFailed
	}
	run.CompletedAt = s.now()
	s.runs[runID] = run
	s.appendEvent(runID, "", "", StageCompleted, "run finished")
	return nil
}

func (s *MemoryRunStore) ListRuns(_ context.Context) ([]Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Run, 0, len(s.runs))
	for _, run := range s.runs {
		run.Metadata = maps.Clone(run.Metadata)
		result = append(result, run)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].CreatedAt.Before(result[right].CreatedAt)
	})
	return result, nil
}

func (s *MemoryRunStore) GetRun(_ context.Context, runID string) (RunDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, exists := s.runs[runID]
	if !exists {
		return RunDetail{}, fmt.Errorf("%w: run %s", ErrNotFound, runID)
	}
	run.Metadata = maps.Clone(run.Metadata)
	result := RunDetail{Run: run}
	for _, trial := range s.trials {
		if trial.RunID == runID {
			trial.Result = cloneBenchmarkResultValue(trial.Result)
			result.Trials = append(result.Trials, trial)
		}
	}
	for _, attempt := range s.attempts {
		trial := s.trials[attempt.TrialID]
		if trial.RunID == runID {
			result.Attempts = append(result.Attempts, attempt)
		}
	}
	for _, event := range s.events {
		if event.RunID == runID {
			result.Events = append(result.Events, event)
		}
	}
	sort.Slice(result.Trials, func(left, right int) bool {
		return result.Trials[left].ID < result.Trials[right].ID
	})
	sort.Slice(result.Attempts, func(left, right int) bool {
		if result.Attempts[left].TrialID == result.Attempts[right].TrialID {
			return result.Attempts[left].Number < result.Attempts[right].Number
		}
		return result.Attempts[left].TrialID < result.Attempts[right].TrialID
	})
	return result, nil
}

func (s *MemoryRunStore) appendEvent(runID, trialID, attemptID string, stage Stage, message string) {
	s.events = append(s.events, Event{
		ID:        fmt.Sprintf("event-%d", len(s.events)+1),
		RunID:     runID,
		TrialID:   trialID,
		AttemptID: attemptID,
		Stage:     stage,
		Message:   message,
		CreatedAt: s.now(),
	})
}

func cloneBenchmarkResult(result BenchmarkResult) *BenchmarkResult {
	cloned := result
	cloned.Metrics = slices.Clone(result.Metrics)
	cloned.CaseResults = slices.Clone(result.CaseResults)
	return &cloned
}

func cloneBenchmarkResultValue(result *BenchmarkResult) *BenchmarkResult {
	if result == nil {
		return nil
	}
	return cloneBenchmarkResult(*result)
}
