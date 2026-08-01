package datasetinstall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
)

const taskStoreFile = "dataset-install-tasks.json"

var ErrConflict = errors.New("dataset install task conflict")

type ManagerConfig struct {
	DataRoot    string
	Directory   string
	Runner      Runner
	Now         func() time.Time
	IDGenerator func() (string, error)
}

type Manager struct {
	mu          sync.Mutex
	dataRoot    string
	directory   string
	runner      Runner
	now         func() time.Time
	idGenerator func() (string, error)
	tasks       map[string]*Task
	order       []string
	idempotency map[string]string
	cancels     map[string]context.CancelFunc
	queue       chan string
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.DataRoot == "" || config.Directory == "" || config.Runner == nil {
		return nil, fmt.Errorf(
			"%w: data root, task directory, and runner are required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	dataRoot, err := filepath.Abs(config.DataRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve dataset root: %w", err)
	}
	directory, err := filepath.Abs(config.Directory)
	if err != nil {
		return nil, fmt.Errorf("resolve dataset task directory: %w", err)
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create dataset root: %w", err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create dataset task directory: %w", err)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.IDGenerator == nil {
		config.IDGenerator = randomTaskID
	}
	managerContext, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		dataRoot: dataRoot, directory: directory, runner: config.Runner,
		now: config.Now, idGenerator: config.IDGenerator,
		tasks: make(map[string]*Task), idempotency: make(map[string]string),
		cancels: make(map[string]context.CancelFunc), queue: make(chan string, 32),
		ctx: managerContext, cancel: cancel, done: make(chan struct{}),
	}
	queued, err := manager.load()
	if err != nil {
		cancel()
		return nil, err
	}
	go manager.work()
	for _, taskID := range queued {
		manager.queue <- taskID
	}
	return manager, nil
}

func (m *Manager) Close() {
	m.cancel()
	<-m.done
}

func (m *Manager) Sources() []Source {
	result := make([]Source, 0, len(recipes()))
	for _, item := range recipes() {
		source := item.Source
		source.DataRoot = m.dataRoot
		source.Downloaded = true
		for _, relative := range item.RawFiles {
			info, err := os.Stat(filepath.Join(m.dataRoot, "raw", relative))
			if err != nil || info.Size() == 0 {
				source.Downloaded = false
				break
			}
		}
		manifest := filepath.Join(m.dataRoot, "prepared", "manifests", item.Manifest)
		if info, err := os.Stat(manifest); err == nil && info.Size() > 0 {
			source.Prepared = true
		}
		source.InstallStatus = "not_downloaded"
		if source.Downloaded {
			source.InstallStatus = "downloaded"
		}
		if source.Prepared {
			source.InstallStatus = "ready"
		}
		result = append(result, source)
	}
	return result
}

func (m *Manager) Create(dataset string, idempotencyKey string) (Task, error) {
	if idempotencyKey == "" {
		return Task{}, fmt.Errorf(
			"%w: Idempotency-Key is required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	if !supportedDataset(dataset) {
		return Task{}, fmt.Errorf(
			"%w: unsupported dataset %q",
			knowledgeeval.ErrInvalidRecord,
			dataset,
		)
	}
	m.mu.Lock()
	if taskID, exists := m.idempotency[idempotencyKey]; exists {
		task := cloneTask(*m.tasks[taskID])
		m.mu.Unlock()
		if task.Dataset != dataset {
			return Task{}, fmt.Errorf("%w: Idempotency-Key was reused", ErrConflict)
		}
		return task, nil
	}
	for _, task := range m.tasks {
		if task.Dataset == dataset && (task.Status == StatusQueued || task.Status == StatusRunning) {
			m.mu.Unlock()
			return Task{}, fmt.Errorf("%w: %s is already being installed", ErrConflict, dataset)
		}
	}
	taskID, err := m.idGenerator()
	if err != nil {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("generate dataset install task ID: %w", err)
	}
	if _, exists := m.tasks[taskID]; exists {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("%w: duplicate task ID %s", ErrConflict, taskID)
	}
	now := m.now().UTC()
	task := &Task{
		ID: taskID, Dataset: dataset, Status: StatusQueued, DataRoot: m.dataRoot,
		CreatedAt: now, UpdatedAt: now, IdempotencyKey: idempotencyKey,
		Events: []Event{{Status: StatusQueued, Message: "Dataset install queued.", CreatedAt: now}},
	}
	m.tasks[taskID] = task
	m.order = append(m.order, taskID)
	m.idempotency[idempotencyKey] = taskID
	if err := m.persistLocked(); err != nil {
		delete(m.tasks, taskID)
		delete(m.idempotency, idempotencyKey)
		m.order = m.order[:len(m.order)-1]
		m.mu.Unlock()
		return Task{}, err
	}
	result := cloneTask(*task)
	m.mu.Unlock()
	m.queue <- taskID
	return result, nil
}

func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Task, 0, len(m.order))
	for index := len(m.order) - 1; index >= 0; index-- {
		result = append(result, cloneTask(*m.tasks[m.order[index]]))
	}
	return result
}

func (m *Manager) Get(taskID string) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return Task{}, fmt.Errorf("%w: task %s", knowledgeeval.ErrNotFound, taskID)
	}
	return cloneTask(*task), nil
}

func (m *Manager) Cancel(taskID string) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return Task{}, fmt.Errorf("%w: task %s", knowledgeeval.ErrNotFound, taskID)
	}
	now := m.now().UTC()
	switch task.Status {
	case StatusQueued:
		task.Status = StatusCancelled
		task.CompletedAt = &now
		task.Events = append(task.Events, Event{
			Status: StatusCancelled, Message: "Queued dataset install cancelled.", CreatedAt: now,
		})
	case StatusRunning:
		task.CancellationRequested = true
		task.Events = append(task.Events, Event{
			Status: StatusRunning, Message: "Cancellation requested.", CreatedAt: now,
		})
		if cancel := m.cancels[taskID]; cancel != nil {
			cancel()
		}
	default:
		return cloneTask(*task), fmt.Errorf(
			"%w: task %s is already %s",
			ErrConflict,
			taskID,
			task.Status,
		)
	}
	task.UpdatedAt = now
	if err := m.persistLocked(); err != nil {
		return Task{}, err
	}
	return cloneTask(*task), nil
}

func (m *Manager) work() {
	defer close(m.done)
	for {
		select {
		case <-m.ctx.Done():
			return
		case taskID := <-m.queue:
			m.execute(taskID)
		}
	}
}

func (m *Manager) execute(taskID string) {
	m.mu.Lock()
	task, exists := m.tasks[taskID]
	if !exists || task.Status != StatusQueued {
		m.mu.Unlock()
		return
	}
	startedAt := m.now().UTC()
	task.Status = StatusRunning
	task.StartedAt = &startedAt
	task.UpdatedAt = startedAt
	task.Events = append(task.Events, Event{
		Status:    StatusRunning,
		Message:   "Downloading pinned source files and preparing answer-blind splits.",
		CreatedAt: startedAt,
	})
	taskContext, cancel := context.WithCancel(m.ctx)
	m.cancels[taskID] = cancel
	dataset := task.Dataset
	if err := m.persistLocked(); err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
	}
	m.mu.Unlock()

	runErr := m.runner.Install(taskContext, dataset, m.dataRoot)
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	task = m.tasks[taskID]
	delete(m.cancels, taskID)
	completedAt := m.now().UTC()
	task.UpdatedAt = completedAt
	task.CompletedAt = &completedAt
	switch {
	case task.CancellationRequested || errors.Is(runErr, context.Canceled):
		task.Status = StatusCancelled
		task.Error = ""
		task.Events = append(task.Events, Event{
			Status: StatusCancelled, Message: "Dataset install cancelled.", CreatedAt: completedAt,
		})
	case runErr != nil:
		task.Status = StatusFailed
		task.Error = runErr.Error()
		task.Events = append(task.Events, Event{
			Status: StatusFailed, Message: runErr.Error(), CreatedAt: completedAt,
		})
	default:
		task.Status = StatusCompleted
		task.Error = ""
		task.Events = append(task.Events, Event{
			Status:    StatusCompleted,
			Message:   "Dataset downloaded, validated, and prepared for experiments.",
			CreatedAt: completedAt,
		})
	}
	if err := m.persistLocked(); err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
	}
}

func (m *Manager) load() ([]string, error) {
	encoded, err := os.ReadFile(filepath.Join(m.directory, taskStoreFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dataset install task store: %w", err)
	}
	var tasks []Task
	if err := json.Unmarshal(encoded, &tasks); err != nil {
		return nil, fmt.Errorf("decode dataset install task store: %w", err)
	}
	queued := make([]string, 0)
	now := m.now().UTC()
	for index := range tasks {
		task := cloneTask(tasks[index])
		if task.Status == StatusRunning {
			task.Status = StatusFailed
			task.Error = "API process stopped while dataset installation was running."
			task.UpdatedAt = now
			task.CompletedAt = &now
			task.Events = append(task.Events, Event{
				Status: StatusFailed, Message: task.Error, CreatedAt: now,
			})
		}
		m.tasks[task.ID] = &task
		m.order = append(m.order, task.ID)
		m.idempotency[task.IdempotencyKey] = task.ID
		if task.Status == StatusQueued {
			queued = append(queued, task.ID)
		}
	}
	sort.SliceStable(m.order, func(left, right int) bool {
		return m.tasks[m.order[left]].CreatedAt.Before(m.tasks[m.order[right]].CreatedAt)
	})
	if err := m.persistLocked(); err != nil {
		return nil, err
	}
	return queued, nil
}

func (m *Manager) persistLocked() (returnedErr error) {
	tasks := make([]Task, 0, len(m.order))
	for _, taskID := range m.order {
		tasks = append(tasks, cloneTask(*m.tasks[taskID]))
	}
	encoded, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("encode dataset install task store: %w", err)
	}
	temporary, err := os.CreateTemp(m.directory, ".dataset-install-tasks-*.json")
	if err != nil {
		return fmt.Errorf("create dataset install task store: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if closeErr := temporary.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("close dataset install task store: %w", closeErr))
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("clean dataset install task store: %w", removeErr))
		}
	}()
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("write dataset install task store: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync dataset install task store: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close dataset install task store before rename: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(m.directory, taskStoreFile)); err != nil {
		return fmt.Errorf("replace dataset install task store: %w", err)
	}
	return nil
}

func supportedDataset(dataset string) bool {
	for _, item := range recipes() {
		if item.ID == dataset {
			return true
		}
	}
	return false
}

func randomTaskID() (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "dataset-task-" + hex.EncodeToString(random[:]), nil
}

func cloneTask(task Task) Task {
	task.Events = slices.Clone(task.Events)
	return task
}
