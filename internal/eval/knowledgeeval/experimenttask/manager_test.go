package experimenttask

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/stretchr/testify/suite"
)

type managerSuite struct {
	suite.Suite
	now time.Time
}

func TestManager(t *testing.T) {
	suite.Run(t, new(managerSuite))
}

func (s *managerSuite) SetupTest() {
	s.now = time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
}

func (s *managerSuite) TestCreatesExecutesPersistsAndDeduplicatesTasks() {
	directory := s.T().TempDir()
	executor := &fakeExecutor{result: ExecutionResult{
		RunIDs: []string{"run-1"}, ArtifactIDs: []string{"artifact-1"},
		ResultPath: "tasks/task-1",
	}}
	manager := s.newManager(directory, &fakePreviewer{preview: eligiblePreview()}, executor)
	task, err := manager.Create(context.Background(), baselineRequest(), "request-1")
	s.Require().NoError(err)
	s.Equal("task-1", task.ID)
	s.Require().Eventually(func() bool {
		current, getErr := manager.Get(task.ID)
		return getErr == nil && current.Status == StatusCompleted
	}, time.Second, 10*time.Millisecond)

	task, err = manager.Get(task.ID)
	s.Require().NoError(err)
	s.Equal([]string{"run-1"}, task.RunIDs)
	s.Equal([]string{"artifact-1"}, task.ArtifactIDs)
	s.Len(task.Events, 3)
	s.Require().NotNil(task.StartedAt)
	s.Require().NotNil(task.CompletedAt)
	s.True(task.StartedAt.Before(*task.CompletedAt))
	duplicate, err := manager.Create(context.Background(), baselineRequest(), "request-1")
	s.Require().NoError(err)
	s.Equal(task.ID, duplicate.ID)
	_, err = manager.Create(context.Background(), Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-44",
	}, "request-1")
	s.Require().ErrorIs(err, ErrConflict)
	manager.Close()

	reloaded := s.newManager(
		directory,
		&fakePreviewer{preview: eligiblePreview()},
		&fakeExecutor{},
	)
	defer reloaded.Close()
	tasks := reloaded.List()
	s.Require().Len(tasks, 1)
	s.Equal(StatusCompleted, tasks[0].Status)
	s.Require().FileExists(filepath.Join(directory, taskStoreFile))
}

func (s *managerSuite) TestRequiresPaidConfirmationAndSupportsCancellation() {
	preview := eligiblePreview()
	preview.Paid = true
	executor := &fakeExecutor{started: make(chan struct{}), waitForCancel: true}
	manager := s.newManager(s.T().TempDir(), &fakePreviewer{preview: preview}, executor)
	defer manager.Close()
	_, err := manager.Create(
		context.Background(),
		Request{
			Dataset: "locomo", Partition: "train", GroupID: "conv-26",
			Mode: ModeMaintainer,
		},
		"paid-without-confirmation",
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	task, err := manager.Create(
		context.Background(),
		Request{
			Dataset: "locomo", Partition: "train", GroupID: "conv-26",
			Mode: ModeMaintainer, ConfirmPaid: true,
		},
		"paid-confirmed",
	)
	s.Require().NoError(err)
	<-executor.started
	cancelled, err := manager.Cancel(task.ID)
	s.Require().NoError(err)
	s.True(cancelled.CancellationRequested)
	s.Require().Eventually(func() bool {
		current, getErr := manager.Get(task.ID)
		return getErr == nil && current.Status == StatusCancelled
	}, time.Second, 10*time.Millisecond)
}

func (s *managerSuite) TestReportsValidationExecutionAndLookupErrors() {
	_, err := NewManager(ManagerConfig{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewManager(ManagerConfig{
		Directory: "", Previewer: &fakePreviewer{}, Executor: &fakeExecutor{},
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	manager := s.newManager(
		s.T().TempDir(),
		&fakePreviewer{preview: eligiblePreview()},
		&fakeExecutor{err: errors.New("runner unavailable")},
	)
	defer manager.Close()
	preview, err := manager.Preview(context.Background(), baselineRequest())
	s.Require().NoError(err)
	s.True(preview.Eligible)
	_, err = manager.Create(context.Background(), baselineRequest(), "")
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = manager.Get("missing")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
	_, err = manager.Cancel("missing")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)

	task, err := manager.Create(context.Background(), baselineRequest(), "failing")
	s.Require().NoError(err)
	s.Require().Eventually(func() bool {
		current, getErr := manager.Get(task.ID)
		return getErr == nil && current.Status == StatusFailed
	}, time.Second, 10*time.Millisecond)
	completed, err := manager.Get(task.ID)
	s.Require().NoError(err)
	s.Contains(completed.Error, "runner unavailable")
	_, err = manager.Cancel(task.ID)
	s.Require().ErrorIs(err, ErrConflict)

	randomID, err := randomTaskID()
	s.Require().NoError(err)
	s.Regexp(`^task-[a-f0-9]{12}$`, randomID)
}

func (s *managerSuite) TestRetriesFailedTaskAsNewTaskAndPreservesOriginal() {
	directory := s.T().TempDir()
	ids := []string{"task-original", "task-retry"}
	manager, err := NewManager(ManagerConfig{
		Directory: directory,
		Previewer: &fakePreviewer{preview: eligiblePreview()},
		Executor:  &fakeExecutor{err: errors.New("provider interrupted")},
		Now: func() time.Time {
			s.now = s.now.Add(time.Second)
			return s.now
		},
		IDGenerator: func() (string, error) {
			id := ids[0]
			ids = ids[1:]
			return id, nil
		},
	})
	s.Require().NoError(err)
	defer manager.Close()

	original, err := manager.Create(context.Background(), baselineRequest(), "original-key")
	s.Require().NoError(err)
	s.Require().Eventually(func() bool {
		current, getErr := manager.Get(original.ID)
		return getErr == nil && current.Status == StatusFailed
	}, time.Second, 10*time.Millisecond)

	retry, err := manager.Retry(context.Background(), original.ID, "retry-key")
	s.Require().NoError(err)
	s.Equal("task-retry", retry.ID)
	s.Equal(original.ID, retry.RetryOfTaskID)
	s.Equal(original.Request, retry.Request)
	s.Equal(StatusQueued, retry.Status)

	preserved, err := manager.Get(original.ID)
	s.Require().NoError(err)
	s.Equal(StatusFailed, preserved.Status)
	s.Empty(preserved.RetryOfTaskID)

	duplicate, err := manager.Retry(context.Background(), original.ID, "retry-key")
	s.Require().NoError(err)
	s.Equal(retry.ID, duplicate.ID)

	_, err = manager.Retry(context.Background(), "missing", "missing-key")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
	_, err = manager.Continue(
		context.Background(), original.ID, ContinueOptions{AdditionalRounds: 10}, "baseline",
	)
	s.Require().ErrorIs(err, ErrConflict)
}

func (s *managerSuite) TestContinuesFailedMaintainerWithBoundedAdditionalRounds() {
	directory := s.T().TempDir()
	ids := []string{
		"task-original",
		"task-continue",
		"task-default-rounds",
		"task-max-rounds",
	}
	preview := eligiblePreview()
	preview.Paid = true
	manager, err := NewManager(ManagerConfig{
		Directory: directory,
		Previewer: &fakePreviewer{preview: preview},
		Executor:  &fakeExecutor{err: errors.New("invalid Wiki")},
		Now: func() time.Time {
			s.now = s.now.Add(time.Second)
			return s.now
		},
		IDGenerator: func() (string, error) {
			id := ids[0]
			ids = ids[1:]
			return id, nil
		},
	})
	s.Require().NoError(err)
	defer manager.Close()

	originalRequest := Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		Mode: ModeMaintainer, Model: "model", ReaderModel: "reader",
		QuestionLimit: 10, MaxRounds: 30, ConfirmPaid: true,
	}
	original, err := manager.Create(context.Background(), originalRequest, "original")
	s.Require().NoError(err)
	s.Require().Eventually(func() bool {
		current, getErr := manager.Get(original.ID)
		return getErr == nil && current.Status == StatusFailed
	}, time.Second, 10*time.Millisecond)

	continued, err := manager.Continue(
		context.Background(), original.ID, ContinueOptions{AdditionalRounds: 10}, "continue",
	)
	s.Require().NoError(err)
	s.Equal("task-continue", continued.ID)
	s.Equal(original.ID, continued.ContinuedFromTaskID)
	s.Equal(original.ID, continued.Request.ContinueFromTaskID)
	s.Equal(10, continued.Request.MaxRounds)
	s.Empty(continued.RetryOfTaskID)

	defaultRounds, err := manager.Continue(
		context.Background(), original.ID, ContinueOptions{}, "continue-default",
	)
	s.Require().NoError(err)
	s.Equal(10, defaultRounds.Request.MaxRounds)
	maxRounds, err := manager.Continue(
		context.Background(), original.ID, ContinueOptions{AdditionalRounds: 200}, "continue-max",
	)
	s.Require().NoError(err)
	s.Equal(200, maxRounds.Request.MaxRounds)

	_, err = manager.Continue(
		context.Background(), original.ID, ContinueOptions{AdditionalRounds: 201}, "too-many",
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = manager.Continue(
		context.Background(), "missing", ContinueOptions{AdditionalRounds: 10}, "missing",
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
}

func (s *managerSuite) TestContinuesCompletedMaintainerByReusingArtifactForMoreQuestions() {
	directory := s.T().TempDir()
	ids := []string{"task-original", "task-more-questions"}
	previewer := &requestPreviewer{availableQuestions: 15}
	manager, err := NewManager(ManagerConfig{
		Directory: directory,
		Previewer: previewer,
		Executor:  &fakeExecutor{result: ExecutionResult{RunIDs: []string{"run"}, ArtifactIDs: []string{"artifact"}}},
		Now: func() time.Time {
			s.now = s.now.Add(time.Second)
			return s.now
		},
		IDGenerator: func() (string, error) {
			id := ids[0]
			ids = ids[1:]
			return id, nil
		},
	})
	s.Require().NoError(err)
	defer manager.Close()

	original, err := manager.Create(context.Background(), Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		Mode: ModeMaintainer, Model: "model", ReaderModel: "reader",
		QuestionLimit: 10, MaxRounds: 30, ConfirmPaid: true,
	}, "original")
	s.Require().NoError(err)
	s.Require().Eventually(func() bool {
		current, getErr := manager.Get(original.ID)
		return getErr == nil && current.Status == StatusCompleted
	}, time.Second, 10*time.Millisecond)

	continued, err := manager.Continue(
		context.Background(), original.ID,
		ContinueOptions{AdditionalQuestions: 5},
		"more-questions",
	)
	s.Require().NoError(err)
	s.Equal(original.ID, continued.ContinuedFromTaskID)
	s.Equal(original.ID, continued.Request.ReuseArtifactFromTaskID)
	s.Equal(10, continued.Request.QuestionOffset)
	s.Equal(5, continued.Request.QuestionLimit)
	s.Zero(continued.Request.MaxRounds)
	s.Equal(5, continued.Preview.SelectedQuestions)
	s.Equal(15, continued.Preview.CumulativeQuestions)

	_, err = manager.Continue(
		context.Background(), original.ID,
		ContinueOptions{AdditionalRounds: 1, AdditionalQuestions: 1},
		"mixed",
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	_, err = manager.Continue(
		context.Background(), "missing",
		ContinueOptions{AdditionalQuestions: 1},
		"missing-questions",
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
}

func (s *managerSuite) TestTreatsRoundLimitAsAwaitingContinuation() {
	manager := s.newManager(
		s.T().TempDir(),
		&fakePreviewer{preview: eligiblePreview()},
		&fakeExecutor{err: fmt.Errorf("execute: %w", ErrRoundLimitReached)},
	)
	defer manager.Close()
	task, err := manager.Create(context.Background(), baselineRequest(), "round-limit")
	s.Require().NoError(err)
	s.Require().Eventually(func() bool {
		current, getErr := manager.Get(task.ID)
		return getErr == nil && current.Status == StatusNeedsMoreRounds
	}, time.Second, 10*time.Millisecond)
	s.True(isPersistedRoundLimit(
		"agent exhausted 200 rounds with invalid Wiki: broken link",
	))
	s.False(isPersistedRoundLimit("provider unavailable"))
}

func (s *managerSuite) newManager(
	directory string,
	previewer Previewer,
	executor Executor,
) *Manager {
	manager, err := NewManager(ManagerConfig{
		Directory: directory, Previewer: previewer, Executor: executor,
		Now: func() time.Time {
			s.now = s.now.Add(time.Second)
			return s.now
		},
		IDGenerator: func() (string, error) { return "task-1", nil },
	})
	s.Require().NoError(err)
	return manager
}

func baselineRequest() Request {
	return Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		Mode: ModeBaseline, QuestionLimit: 1,
	}
}

func eligiblePreview() Preview {
	return Preview{
		Eligible: true, Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		SelectedQuestions: 1, PlannedRuns: 1, IncludesSourceOnly: true,
	}
}

type fakePreviewer struct {
	preview Preview
	err     error
}

type requestPreviewer struct {
	availableQuestions int
}

func (p *requestPreviewer) Preview(_ context.Context, request Request) (Preview, error) {
	selected := min(request.QuestionLimit, max(0, p.availableQuestions-request.QuestionOffset))
	return Preview{
		Eligible: true, Paid: request.Mode == ModeMaintainer,
		Dataset: request.Dataset, Partition: request.Partition, GroupID: request.GroupID,
		AvailableQuestions: p.availableQuestions, SelectedQuestions: selected,
		CumulativeQuestions: request.QuestionOffset + selected,
		PlannedRuns:         1, IncludesMaintainer: request.Mode == ModeMaintainer,
	}, nil
}

func (p *fakePreviewer) Preview(context.Context, Request) (Preview, error) {
	return p.preview, p.err
}

type fakeExecutor struct {
	result        ExecutionResult
	err           error
	started       chan struct{}
	waitForCancel bool
}

func (e *fakeExecutor) Execute(
	ctx context.Context,
	_ string,
	_ Request,
) (ExecutionResult, error) {
	if e.started != nil {
		close(e.started)
	}
	if e.waitForCancel {
		<-ctx.Done()
		return e.result, ctx.Err()
	}
	return e.result, e.err
}
