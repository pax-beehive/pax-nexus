package datasetinstall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
)

type managerSuite struct {
	suite.Suite
	root    string
	runner  *fakeRunner
	manager *Manager
	nextID  int
}

func TestManager(t *testing.T) {
	suite.Run(t, new(managerSuite))
}

func (s *managerSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.runner = newFakeRunner()
	s.manager = s.newManager(s.runner)
}

func (s *managerSuite) TearDownTest() {
	if s.manager != nil {
		s.manager.Close()
	}
}

func (s *managerSuite) TestReportsDownloadAndPreparedState() {
	sources := s.manager.Sources()
	s.Require().Len(sources, 3)
	s.Equal("not_downloaded", sources[0].InstallStatus)

	rawPath := filepath.Join(s.root, "data", "raw", "locomo", "locomo10.json")
	s.Require().NoError(os.MkdirAll(filepath.Dir(rawPath), 0o755))
	s.Require().NoError(os.WriteFile(rawPath, []byte("data"), 0o644))
	sources = s.manager.Sources()
	s.True(sources[0].Downloaded)
	s.Equal("downloaded", sources[0].InstallStatus)

	manifest := filepath.Join(s.root, "data", "prepared", "manifests", "locomo.json")
	s.Require().NoError(os.MkdirAll(filepath.Dir(manifest), 0o755))
	s.Require().NoError(os.WriteFile(manifest, []byte("{}"), 0o644))
	sources = s.manager.Sources()
	s.True(sources[0].Prepared)
	s.Equal("ready", sources[0].InstallStatus)
}

func (s *managerSuite) TestCompletesPersistsAndHonorsIdempotency() {
	task, err := s.manager.Create("locomo", "key-one")
	s.Require().NoError(err)
	s.Equal(StatusQueued, task.Status)

	s.runner.waitStarted(s.T())
	repeated, err := s.manager.Create("locomo", "key-one")
	s.Require().NoError(err)
	s.Equal(task.ID, repeated.ID)
	_, err = s.manager.Create("longmemeval", "key-one")
	s.Require().ErrorIs(err, ErrConflict)

	s.runner.release <- struct{}{}
	s.Eventually(func() bool {
		completed, getErr := s.manager.Get(task.ID)
		return getErr == nil && completed.Status == StatusCompleted
	}, time.Second, 10*time.Millisecond)
	s.manager.Close()
	s.manager = nil

	reloaded := s.newManager(newFakeRunner())
	s.manager = reloaded
	tasks := reloaded.List()
	s.Require().Len(tasks, 1)
	s.Equal(StatusCompleted, tasks[0].Status)
	s.Len(tasks[0].Events, 3)
}

func (s *managerSuite) TestRejectsInvalidAndConflictingRequests() {
	_, err := s.manager.Create("unknown", "key")
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = s.manager.Create("locomo", "")
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	_, err = s.manager.Create("locomo", "first")
	s.Require().NoError(err)
	s.runner.waitStarted(s.T())
	_, err = s.manager.Create("locomo", "second")
	s.Require().ErrorIs(err, ErrConflict)
	s.runner.release <- struct{}{}
}

func (s *managerSuite) TestCancelsRunningAndQueuedTasks() {
	first, err := s.manager.Create("locomo", "first")
	s.Require().NoError(err)
	s.runner.waitStarted(s.T())
	second, err := s.manager.Create("longmemeval", "second")
	s.Require().NoError(err)

	cancelledQueued, err := s.manager.Cancel(second.ID)
	s.Require().NoError(err)
	s.Equal(StatusCancelled, cancelledQueued.Status)
	cancellationRequested, err := s.manager.Cancel(first.ID)
	s.Require().NoError(err)
	s.True(cancellationRequested.CancellationRequested)
	s.Eventually(func() bool {
		cancelled, getErr := s.manager.Get(first.ID)
		return getErr == nil && cancelled.Status == StatusCancelled
	}, time.Second, 10*time.Millisecond)
	_, err = s.manager.Cancel(first.ID)
	s.Require().ErrorIs(err, ErrConflict)
	_, err = s.manager.Get("missing")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
}

func (s *managerSuite) TestRecordsRunnerFailure() {
	s.runner.err = errors.New("network unavailable")
	task, err := s.manager.Create("locomo", "failed")
	s.Require().NoError(err)
	s.runner.waitStarted(s.T())
	s.runner.release <- struct{}{}
	s.Eventually(func() bool {
		failed, getErr := s.manager.Get(task.ID)
		return getErr == nil && failed.Status == StatusFailed && failed.Error != ""
	}, time.Second, 10*time.Millisecond)
}

func (s *managerSuite) TestRejectsIncompleteConfiguration() {
	tests := []struct {
		name   string
		config ManagerConfig
	}{
		{name: "missing data root", config: ManagerConfig{Directory: s.root, Runner: s.runner}},
		{name: "missing directory", config: ManagerConfig{DataRoot: s.root, Runner: s.runner}},
		{name: "missing runner", config: ManagerConfig{DataRoot: s.root, Directory: s.root}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			manager, err := NewManager(test.config)
			s.Nil(manager)
			s.ErrorIs(err, knowledgeeval.ErrInvalidRecord)
		})
	}
}

func (s *managerSuite) TestMarksInterruptedRunningTaskFailedOnReload() {
	root := s.T().TempDir()
	directory := filepath.Join(root, "tasks")
	s.Require().NoError(os.MkdirAll(directory, 0o755))
	createdAt := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	encoded, err := json.Marshal([]Task{{
		ID: "interrupted", Dataset: "locomo", Status: StatusRunning,
		CreatedAt: createdAt, UpdatedAt: createdAt, IdempotencyKey: "persisted-key",
	}})
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(
		filepath.Join(directory, taskStoreFile),
		encoded,
		0o644,
	))
	manager, err := NewManager(ManagerConfig{
		DataRoot: filepath.Join(root, "data"), Directory: directory,
		Runner: newFakeRunner(), Now: func() time.Time { return createdAt.Add(time.Hour) },
	})
	s.Require().NoError(err)
	defer manager.Close()
	task, err := manager.Get("interrupted")
	s.Require().NoError(err)
	s.Equal(StatusFailed, task.Status)
	s.ErrorContains(errors.New(task.Error), "API process stopped")
}

func (s *managerSuite) newManager(runner Runner) *Manager {
	manager, err := NewManager(ManagerConfig{
		DataRoot:  filepath.Join(s.root, "data"),
		Directory: filepath.Join(s.root, "tasks"),
		Runner:    runner,
		Now: func() time.Time {
			return time.Date(2026, time.July, 31, 12, 0, s.nextID, 0, time.UTC)
		},
		IDGenerator: func() (string, error) {
			s.nextID++
			return "task-" + time.Duration(s.nextID).String(), nil
		},
	})
	s.Require().NoError(err)
	return manager
}

type fakeRunner struct {
	mu      sync.Mutex
	err     error
	started chan struct{}
	release chan struct{}
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{started: make(chan struct{}, 8), release: make(chan struct{}, 8)}
}

func (r *fakeRunner) Install(ctx context.Context, _ string, _ string) error {
	r.started <- struct{}{}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.release:
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.err
	}
}

func (r *fakeRunner) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
}
